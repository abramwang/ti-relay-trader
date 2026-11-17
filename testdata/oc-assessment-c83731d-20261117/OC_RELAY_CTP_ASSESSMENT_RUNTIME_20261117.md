# OC 东吴单笔报撤运行入口增量说明

2026-11-17，Asia/Shanghai。对应用户转交的 Relay《东吴 TEST 单笔报撤执行单》。

实现版本 `c83731d46254bb244400f8ed42caea02beff4080`，含原生限价前序提交 `b2e0800`。
本机主程序及指定SDK指纹见 `assessment_review_20261117/version.json`；只提供指纹，不交换二进制。

## 当前交付边界

现有 `oc_trader_commander_ctp` 新增显式 `test_broker_assessment` 模式，
不是临时直连程序。仍由 Relay 发布原协议命令，经 OC journal/dispatch/SDK/
receipt、order.event、fill.event/outbox 返回；没有 OC 自行下单或自动撤单的定时器。

**本次交付未启动真实会话、未确认真实结算、未发真实报撤；没有新 session/代次可回传。**
本地编译及模拟 SDK 测试不等于柜台测评通过。实际窗口须在 Relay 接受下面的心跳模式后启动。
旧只读模式和 transport-only 模式不会获得交易/结算副作用；华鑫和生产配置未改。

## 入口与一次性副作用

同一个主程序，配置模板 `config.assessment.example.json`。受限配置使用已核验凭据和既有
asset-retest 路由绑定，不能运行占位模板、覆盖旧状态或创建新 epoch 规避未决责任。

```bash
./oc_trader_commander_ctp --test-assessment-config /absolute/private/assessment.json --check-config
./oc_trader_commander_ctp --test-assessment-config /absolute/private/assessment.json --run-test-assessment
```

第一条只检查配置和指定 SDK，不联网、不创建 flow、不确认结算；省略运行开关也是预检。
第二条是有副作用的显式操作：先恢复核对和账户锁，再认证/登录，使用 TraderAPI 第二参数
`false`；不创建 MdAPI。`confirm_settlement_once=true` 才执行本次确认，底层配置仍为
`settlement_confirm=never`，不启用每天自动确认。

确认前以 O_EXCL 创建 `state/settlement-confirmation.once.json` 并 fsync 文件和目录，
记录 session/代次/交易日/请求原值和 SDK request_id，随后才调用 SDK。
存在该文件（包括半写或结果未知）就不能重用授权登录测评；不删除文件、不换状态目录重试。
文件的 `outcome=not_proven` 只是不可复用的写前标记，不会伪称成功，实际确认以匹配请求的
真实成功末页及 `readonly.audit.jsonl` 为证。错误、超时、重启不得自动再次确认。

运行上限300秒，配置必须给带 `+08:00` 的实际截止时间，启动时距截止不超过300秒。
仅允许指定交易日的日间09:00-10:15、10:30-11:30、13:30-15:00；这个额外保守范围不宣称
证明柜台时段可用。时间到、绑定丢失或恢复冲突即停，不重建组/epoch，不自动重连重报。

## Relay 接线顺序

1. 模式解析通过后确认新窗口；OC 使用旧绑定及状态，不使用已停止会话身份。
2. 收到真实新 session/代次/TradingDay 的登录、结算确认信息。
3. Relay 以新身份分别发一笔 `order.list.query` 和 `account.positions.query`。
   OC 必须取得当前会话完整标准成功页并持久化；历史09:24快照不能满足本次门禁。
   不要求重复资金查询；资金失败原文不变。四类查询编码仍可用，但本轮只需上述两项初始化。
4. OC 自动只读查询明确合约 `rb2701/SHFE`，串行获取 Trader API `ReqQryDepthMarketData`
   上下限。不是扫描全市场，不调用 Meridian，不创建行情 API。
5. 新鲜心跳允许报单后，Relay 现场刷新行情/原文授权，只发布一笔买开投机1手。
   OC 不自动采用2964，不调整价格、不自动加量或换合约。
6. 实际 working 订单事件带 SysID 后，Relay 新建 cancel_id 发一次撤单。
   cancelled 必须来自真实 OnRtnOrder；SDK同步0/accepted receipt都不替代撤成。

不改变规范键：

```text
relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117:cmd.query
relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117:cmd.trade
relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117:reply
relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117:event
relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117:hb
relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117:dlq
```

group/consumer、SourceID和原 epoch 沿模板及原绑定核实；run_id 启动前重新读取。
本模式不初始化/清空这些键，也不把空路由当首次创建。

## 心跳与准入

`adapter_context.runtime_mode="test_broker_assessment"`，每秒一条。
payload 与顶层既有镜像字段保持一致。登录前交易日/代次为 null；之后只来自 SDK。
纯 JSON **离线样例**见 `assessment_review_20261117/runtime.examples.json` 的
`cases[].heartbeats[]`，由模拟 SDK 运行原编码器产生，不能投递真实账户 Stream。
`cases[].outputs[]` 保留同次模拟查询/receipt/order.event；这不是本轮真实柜台回包。

新单门禁：恢复清楚、当前登录/结算确认、当前完整订单和持仓标准快照、有效OrderRef起点、
原生合约静态检查、5秒内完整原生限价查询、预算尚未消费。动态查询有串行间隔、5秒超时和次数上限。
合约失效、空/部分/错误/过期行情均不放行；实际命令在 SDK 调用前再次检查价格/手数/意图和期限。

撤单独立判定：同会话恢复/结算/快照、真实目标身份、可撤状态、未消费撤单预算；
不要求重新取得报价，也不因报单预算已消费而禁止这笔合法撤单。
`state_text` 使用 `broker_not_ready/settlement_not_confirmed/order_snapshot_not_ready/`
`trade_constraints_not_ready/recovery_blocked/running/stopping`。
`adapter_context.admission_evidence` 给出已消费次数、持仓快照、原生限价请求及采集时间、
静态价位/乘数/手数和各就绪事实，均为审计补充，不改变订单 wire。

**资金边界明确不升级：** `asset_scope_verified=false`。没有删除原资金查询的 BizType
校验，没有把 NUL 补1，也不自动再发资金查询。已有发送组件没有本地保证金足额计算；
本轮限定买开1手，不声称资金/账户交易权限预先查验完备，最终账户风险校验由柜台报单检查执行。
如果双方要求报单前必须完成本地资金足额/作用域证明，本入口尚不满足，应保持不发单，
不能将本次 readiness 解释为该证明。普通交易/生产准入仍未交付。

## 持久证据和停止条件

- 原始命令、请求/回调命名字段hex、SDK返回码、真实时间分别保留；origin继续指向原报单。
- 每次事件发布保存实际物理键、Stream ID和原body；结果/outbox/ACK沿既有 journal，
  未知执行不重报。活动窗口内同命令重投复用原结果，不再次调用SDK、不消耗第二笔预算。
- 成交先到先持久等待关联；不能补造委托。意外成交、拒单、拒撤、未知状态或待关联超时即停，
  不反手平仓，不为凑次数补单；真实持仓风险由用户决定后续处置。
- 尚未完成的错误回调标准化保持原始 callback pending 并关闭窗口；**不是已完成全部拒撤event验收**。
  原始错误不会被伪造成 cancelled，也不把停机状态当成订单失败终态。
- 异常关闭仍尽力留存晚到回调，不在异常处理里再次发布/ACK/调用SDK；落盘失败另记
  `shutdown_evidence_incomplete`，不得认为恢复清楚。已发布事件的确认丢失仍需原输出恢复。
- 窗口结束未取得该笔 cancelled 时返回失败并留核对标记。进程返回0本身也不代替Relay原文/柜台核验。

本轮共享 TEST 存储仍不用于持久性验收；CentOS7、真实交易错误分支、真实崩溃恢复及普通生产
交易准入不在本次离线通过范围内。部署版本和实际测试日志随本目录交付，不交换凭据或二进制。

## 本机验证材料

- Ubuntu完整构建及39项CTest通过：`assessment_review_20261117/ctest.txt`。
- 非PIE ASan/UBSan及泄漏检查三个入口通过：assessment运行时、原生报撤、原生限价查询；
  原日志 `assessment_review_20261117/sanitizer.txt`。不声称旧PIE异常已消除。
- 运行时七场景：正常单笔报撤、同命令重复、恢复阻断、确认失败、空限价、事件发布失败、
  错误消费组。正常/重复场景均只调用一次报单、一次撤单，重启不重复确认。
- `runtime.examples.json`及`runtime-test.txt`由同一测试程序实际输出；模拟SDK和发布器，
  只使用本地临时持久文件。业务时钟固定为合成13:30样例；采集时刻是本机测试时间，
  不作为真实行情/柜台时效证据。不连接业务Redis，不向真实Stream发送合成内容。
- CLI预检覆盖默认不运行、互斥参数、配置权限、生产拒绝及凭据不输出。

复现（仓库根目录，测试程序不会登录）：

```bash
cmake --build oceanus/src/oc_trader_commander_ctp/build -- -j4
(cd oceanus/src/oc_trader_commander_ctp/build && ctest --output-on-failure)
```

**请Relay只先适配新模式及JSON，不沿用旧session发单。** 双方确认下午实际窗口后再读取
真实绑定、登录、确认结算及初始化两查询；OC届时回传实际session/代次/交易日/截止时间。
若资金前置证明或真实原生约束不满足现场约定，停止在已取得的证据层，不以赶测评放宽。
