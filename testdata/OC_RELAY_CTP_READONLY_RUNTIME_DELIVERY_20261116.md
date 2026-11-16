# OC 给 Relay：东吴 TEST 真实只读入口增量交付

日期：2026-11-16，Asia/Shanghai。计划联调日：2026-11-17。

## 1. 实际状态

| 项目 | 本次状态 |
| --- | --- |
| A：真实 SDK 认证/登录、四查询、Redis 回包与心跳运行接线 | 已实现未部署；本机编译、离线假 SPI/传输验证通过，尚无在线柜台/Redis 联合验收 |
| B：Redis 单笔报单/撤单、实时 order/fill 事件 | 未完成，本入口不执行；不能保证明天完成券商报撤测评 |
| 真实 UserID/InvestorID、权限、TradingDay | 待在线验证；通知中的测评账号分别配置为两个候选字段，不视为已验证 |
| 生产/华鑫 | 未修改配置、未停止服务、未切环境 |

代码提交：`2290dc9`（有界原始查询审计）、`89fb095`（独立 TEST 只读运行入口）。
查询执行组件继续使用既有持久意图、dispatch、查询转换、outbox 和 ACK，不新建临时报撤路径。
本轮没有登录柜台、连接业务 Redis、初始化 epoch/group、确认结算、发送报撤或部署。
F0 不标整体完成。本说明替代旧准备回签中“真实只读主循环尚未接线”的状态，不升级其他验收结论。

## 2. 主机、模式与配置

用户确认在当前 `/home/Titian_Cpp` 所在 Ubuntu 20.04 x86_64 开发机运行。
SDK 版本：`v6.7.13_20260225 14:16:30.12079`，本机实际动态库指纹：

| 库 | SHA256 |
| --- | --- |
| thosttraderapi_se.so | 8f919d0f75f407b0d7960358acee5f55864744be589f16cb545a91ab8cb80403 |
| thostmduserapi_se.so | 204ad48dd082b2282422c5826b3d006d7e865833ae91556d6cf7c6aea8c56a18 |

预检核对实际加载路径、版本与哈希；不只检查某目录里的同名文件。
Trader 工厂使用 `CreateFtdcTraderApi(flow_path, false)`，第二参数明确为测评模式。
入口不创建 MdAPI 对象、不订阅行情；仅核验配套行情库版本/指纹。

示例：[config.readonly.example.json](config.readonly.example.json)。它含占位符，不是已经填好凭据的运行配置。
真实文件须为当前用户拥有、权限 0600 的普通文件；独立 state 目录须事先存在且为当前用户私有。
密码、AppID、AuthCode、Redis 密码仅由授权人员写入私有文件，不放命令行或 Git。
`server_run_id`、`epoch` 必须按实际部署核验填写；本轮没有取得或猜填它们。

| 身份 | 配置 |
| --- | --- |
| env / routing.broker_id | test / ctp_4900 |
| gateway_id / account_id | ctp-dongwu-assessment-20261117 / 300052141 |
| native BrokerID | 4900，来自券商通知 |
| native UserID / InvestorID | 两个独立字段均暂按通知账号 300052141 配置，待真实登录及查询核实 |
| group / consumer | oc-ctp-dongwu-assessment-20261117 / oc-ctp-dongwu-assessment-20261117-owner1 |
| source_id | shared-test-redis，配置绑定而非服务器身份认证 |
| 本地 state | `/home/Titian_Cpp/oceanus/src/oc_trader_commander_ctp/.private/dongwu-assessment-20261117/state` |

固定物理键如下，尚未初始化/预留：

```text
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:cmd.query
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:cmd.trade
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:reply
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:event
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:dlq
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:hb
relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117:cmd.trade:transport-epoch
```

epoch 沿用既有 transport binding 作用域；不能将 run_id、epoch、进程 session 或连接代次互换。
新路由的初始化须单独协调。入口要求已有匹配的命令组及 epoch，不自动建组、覆盖 epoch、清 pending 或修复 NOGROUP。
启动前若存在旧键/绑定，保留并核对；本机账户锁仅保护同 OS 用户、同环境/原生 BrokerID/InvestorID，不证明其他主机或其他用户没有登录。

## 3. 实际入口及副作用

以下命令在仓库根目录执行。将私有配置实际路径设置到 `CFG`；示例文件本身不能通过凭据/来源占位符检查。

```bash
BIN=/home/Titian_Cpp/oceanus/src/oc_trader_commander_ctp/build/oc_trader_commander_ctp
CFG=/home/Titian_Cpp/oceanus/src/oc_trader_commander_ctp/.private/dongwu-assessment-20261117/readonly.json

# 默认仅离线预检：不连 Redis/柜台、不创建 API/flow/状态文件。
"$BIN" --test-readonly-config "$CFG"
```

只有确认登录窗口及路由初始化后，才由本机操作人执行：

```bash
"$BIN" --test-readonly-config "$CFG" --run-test-readonly
```

显式运行会取得本机账户锁，打开 journal/identity/audit，连接 TEST Redis、核验来源和恢复责任、发布心跳；恢复未清则不创建柜台 API。
恢复清楚后才创建私有 flow、真实 Trader API、认证并登录。登录不是无副作用操作；不得与其他同账户会话争用。
强制 `settlement_confirm=never`、`trading_enabled=false`，配置为 `on_login` 或 true 会被拒绝，不提供暗含确认结算的只读启动。
如果柜台需结算确认才能查询，保留实际失败，停止在该层级，不能改开关绕过。原 transport-only 入口行为不变。

默认运行 300 秒、每秒心跳、最多领取 16 条查询命令；可设 1 至 300 秒、1 至 5 秒心跳、1 至 64 条命令。
查询串行发送、间隔至少 1 秒，最多 4 项在途/排队；查询默认 TTL 为 60 秒，握手有 15 秒截止检查。
SIGINT/SIGTERM 或窗口结束会关闭连接，将已取得的部分查询按失败收口，尝试持久发布原结果，再发停止心跳。
Redis/SDK 阻塞调用仍受其自身超时/关闭行为约束，不承诺硬实时退出期限。不得用本入口延长生产进程。
退出 0 表示本轮观察到登录且命令已收口，不代表所有业务查询成功；failed reply 也可能完成收口。

## 4. 四查询、恢复与证据边界

| 动作 | 实际接线与当前限制 |
| --- | --- |
| account.asset.query | 真实 ReqQryTradingAccount、原始多 scope 行、原有资金标准转换及有限分页；不推断全账户总额 |
| account.positions.query | 真实 ReqQryInvestorPosition、完整/空/失败、原始持仓维度及分页 |
| order.list.query | 真实 ReqQryOrder 与既有身份转换；缺可信本地/外部身份关联时保留原始证据并失败，不伪造身份 |
| fill.list.query | 真实 ReqQryTrade 与既有成交关联；未关联/冲突仍失败，不造订单绕过关联 |

命令只从 `cmd.query` 读取；不消费新的 `cmd.trade`。错误动作/路由、身份、PEL 所有者或本地历史不一致则保留未决并停止。
四查询均经过持久意图和 SDK 调用前 dispatch 标记；原始结果先落盘，然后转换、保存完整输出、发布确认、ACK 原命令。
重投恢复原 message/body，不再次调用 SDK；未知结果不自动重查，更不重报报撤。新采样须使用新查询身份。
发布/ACK 不确定仍保守阻断，不用 PEL=0 代替 durable_ack。不改变现有业务错误枚举或交易 accepted 语义。

新增本地 `readonly.audit.jsonl` 有界为 64 MiB，逐项 fsync、范围/序列/摘要链及重开校验；损坏、半行、满额或写失败会阻止继续发布成功。
`kind=query_result` 保留 SDK 同步返回码、回调错误、实际末页、ticket/request_id、scope/session/generation 和部分行。
字段格式 `ctp.6.7.13.named-member-hex.le.v1` 按命名成员保存小端原字节十六进制，排除结构体 padding；资金50、持仓51、订单68、成交33个成员。
它保留定长字符串及原始浮点位模式，不把 NUL/GBK/无效 double 强行塞进标准 JSON 字符串或金额；这是本地审计格式，不新增交易 wire。
命令原 body、实际输出键/Stream ID/body、心跳及会话状态变化单独记录。会话记录是轮询观察，不声称每个认证回调均已完整录制。
审计含原生账户事实，应留本地受限目录，对外样本脱敏。

本入口没有实时 order.event/fill.event 生产者；external 来源证明、待关联事件释放和交易状态机仍待 B 阶段。
不能把订单/成交“SDK 查询成功但标准转换失败”解释为空成功，更不能发旧缓存替代本次结果。

## 5. Relay 需要接收的新只读心跳模式

保留既有信封/顶层镜像，`adapter_context.runtime_mode=test_broker_read_only` 与旧 `test_transport_recovery_only` 区分。
`counter_session_id` 来自驱动进程；`connection_generation` 来自真实连接回调；`trade_date` 来自登录 TradingDay，尚未知时为 null。
`broker_ready` 表示实际登录状态，`query_collection_ready` 表示本入口当前允许领取查询，不代表完整快照、账户交易权限或行情就绪。
`UP/read_only` 可以出现，但 settlement_confirmed/order_snapshot_ready/accepting_trade_commands/accepting_cancel_commands 始终 false。
这里 order_snapshot_ready 为尚未接通的交易准入快照门禁，不因一次只读 order_page 成功手动置 true。
恢复阻断、未登录、停止分别使用 recovery_blocked/broker_not_ready/stopping；预算用尽也不会继续接受新查询。

请 Relay 为该明确 TEST 模式接通原文审计及只读采集门禁，不把旧 transport-only guard 复活，不以 UP 放开交易。
本轮没有独立查询注册/发送的 Relay 运行授权；双方确认接收能力后再发业务查询，不手工 XADD 占坑。
未新增 Meridian 需求，标的定义与行情仍由 Relay 协调。

## 6. 离线材料与下一步

[readonly_review](readonly_review/) 提供测试原 stdout/stderr、纯 JSON 原输入/输出/心跳、32项 CTest 日志、两个非 PIE ASan/UBSan 入口日志及 SHA256SUMS。
8组运行场景覆盖四查询、恢复阻断、认证失败、重复命令、SDK同步失败、退出部分回包、发布失败、查询流中的交易动作。
另含10项配置拒绝、8项心跳反例、同账户互斥、CLI 默认无连接检查。
测试使用真实驱动 SPI 路径及本地持久文件，但 SDK 对象和 Redis IO 为注入替身；JSON 的 Stream ID 是合成值，不是本轮业务 Redis 证据。
两套构建使用同一合成账户锁，需顺序运行。此前同时跑两套测试触发锁拒绝，顺序复跑通过；不把锁拒绝当成柜台故障。
本轮修复非 PIE 中函数地址落到可执行程序 PLT 导致库路径误判，改为解析实际 SDK 导出符号；不宣称历史 PIE sanitizer 问题已消除。
CentOS7、真实查询超时/重连、真实 Redis+本入口联合链路尚未验收；此前组件恢复证据不升级为此次在线验收。

下一步先核对私有配置、run_id/epoch/命令组和独立接收环境；授权窗口内尝试真实认证/登录，核实两个原生身份及四查询结果。
本机操作由用户协调，具体开始钟点未确认。若登录不成功，保留返回码/回调及状态，不无限重试。
B 阶段仍需 SHFE 撤单身份替代旧 CFFEX 路径、完整 O/C/CT/CY、持久 OrderRef、异步订单/成交与 pending 释放、独立准入和有效静态/动态约束。
在 B 未交付及用户逐项授权前，不进行报单、撤单或结算确认；只读联调通过也不能写成东吴报撤测评通过。
