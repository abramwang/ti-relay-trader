# OC 给 Relay 的事实查询与常驻增量

承接 2026-11-18 职责收敛清单。业务实现 `f0f0477`，包括 `ffde36b` 的事实查询调度和 `beeb778` 的常驻配置；测试修订至 `62011e2`。本目录是离线运行输出，不是实际柜台回包或整日稳定性验收。本轮未连接 Redis/柜台、未登录或确认结算、未部署或重启服务。

## 已实现

| 项目 | 状态与边界 |
| --- | --- |
| 独立事实查询 | 已接现有只读和 assessment 主循环、串行 SDK 调度、原始审计、journal/outbox/发布及 ACK 路径；未在线验收 |
| 常驻配置 | 沿用原入口和 runtime_mode；进程时限与报撤授权分离，day 模式开窗前及授权结束后可继续只读查询和接收回调 |
| 普通命令失败 | 参数错误、当前模式不支持的开平/批量、过期命令持久失败后继续；已有同步和异步拒单/拒撤继续服务路径保留 |

常驻配置和完整启动参数见同目录的 `TEST_RESIDENT_GUIDE.md`。固定原路由、epoch、状态及 once，不清历史。只读进程新增可选顶层 `process_duration_seconds`，不填仍用旧时限；示例为 32400 秒，最大 86400 秒。既有 `max_commands` 现在可配置到 2000，历史 journal 仍计入总容量限制，不是无限常驻或无人值守生产能力。

## 事实查询的实际差异

仍为 TEST `instrument.constraints.query`、`cmd.query -> reply`、`result_type=instrument_constraints`，payload 五字段和大小写不变。合成 rb2701/SHFE 意图 SHA256 已核对为 `43a15b367f8f304a85491349f62ed53be43cebdc034bf399ee2b525102b58b03`，原七动作编码不变。

- 一个命令关联合约及限价两次请求，每次分别持久写前标记、SDK request_id 和同步返回码；不在重投时重查柜台。
- 完整且唯一目标才输出完整事实。附带期权行保留原始审计，不混入目标；两段分别采样，`atomic_snapshot=false`。
- 完整但数值缺失沿原候选输出 null 和不可用字段清单；失败没有 items 或成功末页，不能使用旧事实冒充新成功。
- **新增失败边界需要 Relay 接收**：未实际执行的一段在 `adapter_context.query_evidence` 中为 null。例如合约阶段失败，instrument 保留实际错误/末页状态，depth=null；入队前失败时两者均为 null。不补 SDK ID、返回 0 或假末页。
- 限价请求失败、超时或错目标只失败本次查询。原始请求/结果写入 `readonly.audit.jsonl`；新增记录 kind 为 `native_facts_request`、`native_facts_depth_result`，合约结果仍走原 query_result 审计。

`native_facts_runtime.json` 是九个场景的协调器原输出，含原命令、原 body/摘要、两段请求及模拟调用计数：完整、数值缺失、合约失败/超时、限价失败/超时、错目标、断连、附带期权。末尾统一 `synthetic_only=true/real_sdk_calls=false`。采集时间与测试构造的业务日不是现场交易依据，禁止发布到真实账户路由。

## 单命令失败及恢复

`command_rejections.json` 给出六组实际编码：数量错误、价格类型错误、缺 cancel_id、当前运行模式不支持的开平、错交易所、过期命令。每组随后合法报撤继续，零数量重复输入不会重复业务输出。文件的 SDK 次数是整组测试总次数，失败命令自身 `adapter_context.sdk_calls=0`。

- 不改成订单 rejected/cancelled 终态；只发布关联原命令的 failed reply。沿用 `ORDER_SUBMIT_REJECTED`、`BROKER_CANCEL_REJECTED` 或 `QUERY_FAILED`，不新增业务枚举。
- 本地校验失败带 `failure_stage=command_validation`，本地运行准入失败为 `command_admission`；sdk_request_id/return_code 为 null。这不是伪装成柜台返回的错误。原回报中的错误值不被替换。
- 失败指令只取得不可执行的 journal handle；不填合法数量/价格，不分配新 OrderRef，不产生 SDK 调用。原无效 payload 的本地幂等域为 `ctp.invalid-payload.v1`，不改变合法命令意图编码或 wire。
- 相同命令重投恢复原失败输出；修正参数后复用原消息/幂等键仍冲突。发布确认后才 ACK。仅有意图、没有保存结果的恢复窗口继续保守阻断，不据此自动报撤。
- 缺命令身份、错账户、错物理流、未知动作等无法安全归属的信封仍保留原始审计和 pending 并停止相应入口；本轮没有新增独立隔离服务或自动处理坏信封。
- 读取含新事实查询或无效参数意图的状态需要本版兼容代码。旧二进制可能拒绝启动，不可删状态解决降级问题。

异步柜台拒撤仍通过独立撤单尝试事件，不改变原委托；明确拒撤后只能接收 Relay 新身份的显式撤单，不自动重撤。运行窗口到期若仍有 working/未知责任，最终退出码可非零，不能把这种收尾告警误认为收到普通拒撤立即崩溃。

## 心跳与接线顺序

没有新增 heartbeat 字段或 runtime_mode。继续 `accepting_query_commands` 镜像和 `operation_reasons`、`native_order_snapshot_ready` 与 `order_projection_ready` 分离。day 模式查询可在交易授权窗外为 true，报单/撤单仍为 false；这不授权结算或扩大交易时段。

`heartbeat_examples.json` 是本次离线主循环导出的三条心跳，标记 `synthetic_only=true/deployed=false`。其日志见 `heartbeat-examples.log`；它不是已经启动的新会话，也不是当前可下单证据。

Relay 可据本次输出接在线事实发送、连续接收和拒撤尝试落库。**当前逐单原生检查尚未删除**；双方替代准入链路通过后再移除。OC 不选择报价、不调整数量、不调用 Meridian，也没有引入自主撮合/模拟资金或结算模块。测试替身只在离线测试入口。

下一轮在用户确认的 TEST 窗口固定版本验证完整交易日、多笔报撤、错误后继续、明确窗口的重启不重复执行。本次离线通过不替代真实重连、整日稳定性或生产验收；资金 BizType=00 原失败及普通生产准入缺口保持原样。

## 验证记录

源码构建通过。`component-ctest.txt` 是 44 项组件、schema、CLI 回归原日志；`runtime-ctest.txt` 的五项主循环回归全部通过，耗时 353.09 秒。六组坏请求的单独运行全部通过，日志为 `command-rejections.log`。首次主循环回归发现旧用例仍假定错交易所会停止后续合法命令，已将单独拒绝与错误后继续拆分验证，保留最后复跑结果。测试使用独立临时状态、模拟 SDK/I/O，不访问业务 Redis；运行测试以 nobody 执行，避免与 root 的真实账户进程锁相互影响。

`SHA256SUMS` 为本目录实际文件摘要。它是交付完整性校验，不是第三方验收或真实柜台签名。仅将增量材料放入本机 `/home/Titian_Cpp/reference/query_runtime_review_20261118/`，不宣称已复制到 Relay 远端主机。
