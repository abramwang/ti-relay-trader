# relay - TI Relay Trader

relay 是量化研究系统的交易基础数据项目，负责标准化实盘柜台接口、多账户路由、Redis Stream 对接、交易账本、交易日任务、绩效计算和研究侧导出。

## 线程恢复卡片

| 项目 | 当前口径 |
| --- | --- |
| Agent | `relay` |
| 工作目录 | `/home/ti-relay-trader` |
| 对外服务 | `http://relay-trader.quantstage.com`，端口 `9092` |
| 业务时区 | `Asia/Shanghai`，所有交易日、任务和业务时间按东八区解释 |
| 当前环境 | 生产环境，`.runtime/active-config.yaml -> config/relay.prod.yaml`，独立 API/worker |
| 安全状态 | 6 个生产账户保留、5 个启用查询、0 个开放交易；`501000114077` 继续停用且历史账本保留 |
| 当前阶段 | P0-P4 完成，P5-P8/P10 持续生产化；N8-N12 完成；N13 可信成本账与绩效重建进行中 |
| 最近确认 | `2026-09-17 14:42 Asia/Shanghai` 已按明确指令切回生产只读；五个生产 OC 使用新版进程会话且全部 ready |
| 更新时间 | `2026-09-17` |

新线程按以下顺序恢复：

1. 阅读本节和“当前工作”。
2. 阅读 [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1)，确认最新未完成项。
3. 执行 `git status --short`、`scripts/relay-runtime-service.sh status` 和 `curl -fsS http://127.0.0.1:9092/v1/status`。
4. 涉及生产任务时先查看 `/jobs`、`/operations` 及 `/var/log/relay/`，不得仅凭页面摘要修改账本。
5. 需要追溯旧决策时再阅读 [README 完整历史归档](/home/ti-relay-trader/docs/README_HISTORY_20260826.md:1)。

## 当前工作

### 已验证运行态

- `2026-09-17 14:42 Asia/Shanghai` 按用户明确指令从测试切回生产：独立 API/worker、数据库、Redis、行情和事件桥正常，6 个账户保留、5 个启用查询、0 个开放交易。五个启用账户的 OC heartbeat、券商登录和订单快照均 ready，并已提供不同的 `hxproc-*` 进程会话 ID；Stream `pending=0,lag=0`、无待处理 DLQ。总体暂显示 `degraded` 仅因为无事件账户 `307000051389` 尚未创建 `event` Stream，其他 Stream 与 heartbeat 正常，未作为协议或数据故障处理。
- `2026-09-17 10:00 Asia/Shanghai` 按用户明确指令从生产切到测试环境：TEST schema 29 和 API 内嵌 worker 正常，数据库、Redis、行情、事件桥和订单服务均为 `ok`，Stream `pending=0,lag=0`。测试 OC 最后 heartbeat 为 `2026-09-16 22:22:05 Asia/Shanghai`，当前 `HEARTBEAT_STALE`、`order_entry_ready=false`，因此总体显示 `degraded`；这是 OC 尚未启动造成的失败关闭，不是 Relay 切换或协议错误。
- `2026-09-16 22:22 Asia/Shanghai` 按用户明确指令从测试切回生产：独立 API/worker 和全部依赖健康，6 个账户保留、5 个启用查询、0 个开放交易；业务 Stream consumer group `pending=0,lag=0`，无新 WARN/ERROR。`307000051389:event` 仍未创建但其他 Stream 存在，沿用“尚未产生事件”的监控结论。
- `2026-09-16 15:18 Asia/Shanghai` 按用户明确指令从生产重启到测试环境：TEST schema 29 已就绪，API 内嵌账本同步 worker 健康；账户 `00030484` 的 Redis、券商登录、订单快照和报单准入均 ready，当前 `counter_session_id=hxproc-8c52fcb4c88e9250`。六条 Stream 中仅不存在正常的空 DLQ Stream，consumer group `pending=0,lag=0`；生产配置和数据未修改。
- `2026-09-16 14:40 Asia/Shanghai` 按用户明确指令从测试切回生产：生产库已升级到 schema 29，独立 API/worker 健康，6 个账户保留、5 个启用查询、0 个开放交易。五个启用账户的旧版 OC 均为 `UP`，Redis、券商登录和订单快照 ready；资产、当日订单、当日成交只读接口全部通过。旧版 heartbeat 暂无可选 `counter_session_id`，Relay 兼容为空且不影响查询和落账；业务 Stream consumer group `pending=0,lag=0`、无 DLQ 或解析错误，因此未增加吞错规则。`307000051389:event` 尚未创建但 heartbeat 正常，当前按“未产生事件”监控，不标记协议故障。
- `2026-09-16 13:58 Asia/Shanghai` Meridian 实时 Level1 与测试柜台撮合盘口并非完全同步，仅作为报价参考；7 笔 TEST 主动成交矩阵最终为 3 笔成交、4 笔撤单、0 笔活动残留。3 笔成交均满足 `cum_filled_qty=order_qty=100`、`leaves_qty=0`、`is_terminal=true`，订单与普通成交数量逐笔闭合。schema 29 已在 TEST 应用并把 3 笔既有成交全部回填为 `strategy_id=active-fill-matrix`；后续写入也按同账户、同交易日、同 `gateway_order_id` 继承订单策略归属。四条业务 Stream `lag=0`，DLQ 不存在。
- `2026-09-16 13:44 Asia/Shanghai` TEST 连续人工重启的会话 ID 从 `hxproc-a5463f8735b83449` 换为 `hxproc-8c52fcb4c88e9250`；第二个会话的最小测试单多次完成 `created -> working -> cancelled`，订单事件、撤单尝试和 heartbeat 会话 ID 一致。OC 新增的 `order.cancel.accepted` 已按同一 `origin_message_id` 幂等合并，柜台 event 正确把 reply 的暂态 `reconciliation_required=true` 更新为 `false`，不再新增 unsupported 错误；四条 Stream `lag=0`、无 DLQ。
- `2026-09-16 13:18 Asia/Shanghai` OC 新版在线验收：`counter_session_id=hxcs-0da546ea12434a71` 在 `disconnected -> ready` 期间保持稳定；对上一会话遗留订单撤单返回 `ORDER_NOT_FOUND`，完整携带 `retry_safe=false`、`order_state_changed=false`、`reconciliation_required=true`、东八区 `occurred_at` 和当前会话 ID，原订单仍为 working。TEST 会话现收敛为“一次 OC 进程生命周期一个 ID”，进程内短线重连不变、人工重启换新，OC 不实现四时段判断。13:18 的 100 股最小测试单到达柜台后被以“当前状态禁止此项操作”终态拒绝，订单身份和会话字段正确，但实际下单/撤单仍需目标交易区间人工重启 OC 后复验。
- `2026-09-16` 已把 OC 后续任务合并到 [测试柜台进程会话与订单可靠性契约](/home/ti-relay-trader/docs/OC_COUNTER_SESSION_ID_REQUIREMENT_20260916.md:1)：`counter_session_id` 采用一次 OC 进程生命周期一个 ID 的最小实现，并约束撤单结果六个结构化语义字段及 `filled` 数量/残量/终态原子一致；历史字段允许留空，新版本订单必须满足。
- `2026-09-16 10:52 Asia/Shanghai` 已发布撤单尝试分页账本和 `relay-sdk 0.1.37`：`iter_cancel_attempts(page_size=7)` 在线完整读取 20 条唯一 attempt/Gateway ID，均为 `ORDER_NOT_FOUND` 且 `reconciliation_required=true`；对应前一日 20 笔订单仍为 `working/is_terminal=false`。Relay 已兼容 readiness/订单/撤单尝试的可选 `counter_session_id`，当前 OC heartbeat 未提供该字段，因此 TEST 遗留订单人工 resolution 保持失败关闭，生产更不会据此自动终态化。
- `2026-09-16 10:26 Asia/Shanghai` 测试 OC 恢复并完成无交易写入的主动验收：账户 `00030484` 的资金、持仓、订单、成交、费用查询均取得唯一 completed 终态；资金更新时间为 `10:26:21`，持仓返回 15 条，今日订单/成交/费用均为空结果；4 条 Stream 全部健康、总 lag 为 0、pending DLQ 为 0，`order_entry_ready=true`。测试配置沿用 OC 约定的 `relay:prod:v1:huaxin:00030484` 键名前缀，但 Redis 与 PostgreSQL 均为测试环境独立实例。
- `2026-09-16 09:58 Asia/Shanghai` 按用户明确指令从生产切到测试环境：测试库 migration 成功，API 内嵌账本同步 worker 启动；Redis、数据库、行情和事件流均正常，但测试 OC 柜台会话为 `disconnected`、订单快照未 ready，账户 `00030484` 当前 `order_entry_ready=false`，Relay 以 `BROKER_DISCONNECTED` 失败关闭并拒绝交易命令。生产配置未修改。
- `2026-09-15 22:39 Asia/Shanghai` 按用户明确指令从测试切回生产环境：生产库 migration 成功，独立 API/worker 启动健康；6 个账户保留、5 个启用查询、0 个开放交易，`501000114077` 继续停用且历史账本不删除。
- `2026-09-15 15:18 Asia/Shanghai` 按用户明确指令从生产切到测试环境：测试库 migration 成功，API 内嵌账本同步 worker 启动健康；账户 `00030484` 的 Redis、柜台会话和订单快照均 ready，`order_entry_ready=true`、`trading_enabled=1`。生产配置未修改，后续切回生产必须等待用户新的明确指令。
- `2026-09-15 15:16 Asia/Shanghai` 修复 `/jobs` 账户复核的日初资产口径：旧页面误用盘前 OC 原始资金摘要，现始终回读已落库的 `open` 资产快照；该快照按上一交易日 Meridian 未复权收盘价汇总持仓市值。在线确认富盈13号日初资金 `34,644,380.14`、市值 `5,008,603.10`、总资产 `39,652,983.24`，智算汇利混合日初资金 `46,326,170.32`、市值 `3,216,148.50`、总资产 `49,542,318.82`。后续盘前/盘后任务报告也直接携带最终估值资产，不再混用 OC 原始摘要。
- `2026-09-15 14:31 Asia/Shanghai` 按用户明确指令从测试环境切回生产：目标库 migration 成功，独立 API/worker 启动健康；6 个账户保留、5 个启用查询、0 个开放交易，`501000114077` 继续停用且历史账本不删除。
- `2026-09-15 13:41 Asia/Shanghai` 已按华鑫 7x24 测试柜台的集合竞价、休市和交易分段增加账户级 `order_entry_ready`；只在测试配置生效，生产仍使用正常 A 股时段。`GET /v1/accounts/00030484/readiness?force=true` 实测为 ready、下一次切换 `15:30`；Python SDK `0.1.36` 增加类型化读取和失败关闭 `verify_ready()`，详见 [Chronos 测试柜台准入说明](/home/ti-relay-trader/docs/CHRONOS_TEST_ORDER_ENTRY_READINESS_20260915.md:1)。
- `2026-09-15 09:37 Asia/Shanghai` 按用户明确指令切到测试环境：`.runtime/active-config.yaml -> config/relay.local.yaml`，API 内嵌账本同步 worker；测试 OC 的 Redis、柜台和订单快照均 ready，账户 `00030484` 可接收交易及撤单命令，4 条 Stream `lag=0`、pending DLQ=0。生产配置未修改，切回生产仍需用户明确指令。
- `2026-09-14 21:01 Asia/Shanghai` 按用户明确指令切回生产环境：`.runtime/active-config.yaml -> config/relay.prod.yaml`，独立 API/worker 均健康；6 个账户配置中 5 个启用查询、0 个开放交易，20 条生产 Stream `lag=0`、pending DLQ=0。盘后 `off_hours` 为正常监控状态。
- `2026-09-14 19:18 Asia/Shanghai` 测试链路完成只读复测：账户 `00030484` 的 OC `redis_ready/broker_ready/order_snapshot_ready` 均为 true，资金及 10 条持仓查询取得 completed 终态并成功落库，四条 Stream `lag=0` 且无 DLQ。`off_hours` 仅表示盘后监控阶段；测试库已补齐 migration 25-28，环境切换脚本现在会先迁移目标数据库，成功后才停止旧服务。生产配置未修改，后续切回生产仍必须等待用户明确指令。
- `2026-09-14` Chronos R2c 测试环境联调已完成。13:23 曾在未取得本轮明确授权时恢复生产，用户于 13:28 要求切回测试；14:29 收到用户明确指令后才切回生产只读。Relay 必须等待新的明确指令，不得把历史讨论、README 提醒或计划任务时间视为切换授权。
- `2026-09-08` 生产部署基线为 `.runtime/active-config.yaml -> config/relay.prod.yaml`，独立 API/worker；六个生产账户及数据库别名保留，其中 `501000114077` 为 `enabled=false/trading_enabled=false`，其余五户启用查询但均关闭交易。每日任务默认账户集合已验证不包含停用户，历史账本读取不受影响。
- `2026-09-09` 09:01 盘前初始化在 OC 登录前发布查询，五个启用账户均因 180 秒无 reply 而阻断，未写 open 快照；OC 就绪后 09:07 手工重跑仅耗时 7.2 秒，20 类账户查询均取得唯一 completed 终态，写入 5 个日初资产快照和 227 条日初持仓，0 账户错误。原失败任务保留为历史记录，最新任务状态为 succeeded。
- `2026-09-06` 测试批量单已验证 `HTTP 202 -> accepted reply -> order.event -> PostgreSQL -> Web`。OC 后续状态事件曾把同一订单交易日从 `20260906` 改为 `20450624` 并丢失命令关联，Relay 已按订单时间防止异常日期拆单、保留原值审计，并让批量页按 `message_id` 自动刷新回报；OC 侧反馈见 [测试批量订单回报反馈](/home/ti-relay-trader/docs/OC_TEST_BATCH_ORDER_FEEDBACK_20260906.md:1)。
- `2026-09-06` OC `f4a683a/5eaea72` 已完成在线复测：两个批量子单均收到 accepted/working/cancelled 多阶段事件，所有事件保持 `trade_date=20260906`、原批量 `origin_message_id` 及稳定的订单 ID；`20450624` 只出现在 adapter 审计字段，主动订单查询后身份不变，command groups 为 `pending=0,lag=0`。详见 [OC 测试订单回报验收](/home/ti-relay-trader/docs/OC_TEST_ORDER_REPORT_VALIDATION_20260906.md:1)。
- 交易终端日期按环境分流：测试环境订单/成交监控默认东八区自然日，允许周末和节假日查看测试柜台订单；生产环境仍默认 Meridian 最近交易日。K 线、绩效和资金持仓在两个环境中均继续使用最近交易日口径。
- 生产 API、worker、PostgreSQL、Redis、事件桥、Meridian 行情代理和订单服务均已接通；API 监听 `0.0.0.0:9092`，worker 健康端口只监听 `127.0.0.1:19092`。
- 生产配置保留账户 `501000114077`、`314000046830`、`314000045768`、`307000051388`、`307000051389`、`307000051387`；其中 `501000114077` 自 `2026-09-08` 起停止 OC 接入并停用路由，其历史信息不删除。别名由 PostgreSQL 管理，账户 ID 始终作为路由和账本主键。
- 每个资金账户都带必填 `broker_id` 所属券商标签；当前六户均为 `huaxin`。该标签与账户别名、Gateway 和环境分离，后续新增券商沿用同一账户路由模型。
- `2026-08-26` 已验证 `archive_incomplete -> Level1 provisional -> canonical daily` 全链路：3 个活跃账户 ready，1 个空账户 not_applicable，0 blocked；权威日线复算与 provisional NAV 差异为 0。
- Meridian 权威日线父任务当前 16:30 启动、16:45 为完成 SLA；Relay 16:40 首查并每 10 分钟重试至 18:50。窗口内显示等待，18:50 仍未就绪则标记 Meridian 上游阻塞；同一交易日所有轮询复用一个 `run_id`。
- TEST 与生产 schema 均为 `29 fill_order_context_inheritance`，Python SDK 当前版本为 `relay-sdk==0.1.37`。
- 公网绩效写入口和生产下单权限保持关闭；本机任务可按质量门禁写入版本化绩效结果。

### 当前进展与阻塞

- Chronos 跨日订单 R1/R2/R4 已完成；R3 TEST-only 人工遗留订单处置待实现。TEST 定义为一次 OC 进程生命周期一个 `counter_session_id`，进程内短线重连不变；OC 不识别 7x24 四时段，Relay 时间表只控制准入且不推导会话变化。生产正常 A 股逻辑严格隔离。详见 [需求响应](/home/ti-relay-trader/docs/CHRONOS_CROSS_DAY_ORDER_REQUIREMENT_RESPONSE_20260916.md:1)。
- Chronos 本轮验收把测试订单的 `accepted_at/last_updated_at=09:46:44` 与逐笔成交时间混淆；权威成交字段为 `Fill.matched_at=13:53:00+08:00`。Chronos 底层投影已读取该字段，仍需修正验收取证、页面或持久化消费口径；测试柜台状态时钟差异不外推到生产，详见 [Chronos 成交时间字段语义纠正](/home/ti-relay-trader/docs/CHRONOS_FILL_TIME_SEMANTICS_20260915.md:1)。
- 首页已移除主栏 `200/126/280px` 固定 Grid 行约束：5 个快捷入口自动排布，账户路由表按实际账户行数撑开，左右两栏共同决定 dashboard 高度。Playwright 已在 `1600x900` 与 `1366x768` 验证入口、6 行账户路由和右侧运行边界无裁切、无重叠。
- `/jobs` 已移除任务计划区的固定高度约束：任务卡按内容自适应，账户复核、历史记录和报告区随内容顺序布局，超出视口时由页面主区域统一滚动；任务报告工作区限制为随视口变化的 `360-520px`，完整 JSON 在模块内部滚动。Playwright 已验证 5 张任务卡无裁切、无区域重叠，78KB 长报告不会撑高外层页面。
- Chronos 在 `2026-09-14 19:11` 遇到的测试资产 HTTP 500 已定位为测试 PostgreSQL 缺 migration 28，并非 OC 未 ready。测试库已升级、目标库迁移已纳入环境切换前置门禁；`GET /asset?enrich=false` 和默认资产均恢复 200。资产错误现区分 `404 NOT_FOUND`、`503 ASSET_NOT_READY` 和 `500 INTERNAL`，复测证据见 [Chronos 测试资产基线验收](/home/ti-relay-trader/docs/CHRONOS_TEST_ASSET_BASELINE_ACCEPTANCE_20260914.md:1)。
- Chronos 消费端已独立关闭上述阻断并完成 Direct 两子单闭环；Relay 复核本轮两个 100 股子单均已撤成、零成交、零剩余，账户活动订单、命令 pending、Stream lag 和 DLQ 均为 0。该结论不外推到 TWAP、R3 故障注入或生产权限。
- 已修正盘前/盘后资产快照口径：OC 华鑫 `asset_page` 当前只返回可见资金，Relay 的 `open` 现在按上一交易日 Meridian 未复权收盘价估值盘前持仓，`close` 汇总当日收盘持仓，统一形成现金加证券市值的总资产；原始 OC 值继续保留审计，缺行情时阻断而不写现金-only 总资产。
- 标准日终资产另行列示 `reverse_repo_receivable`：按去重后的 `204001.SH` 当日成交计算本金应收，且只补成交时间不晚于资金快照捕获时间的本金，避免把晚于旧快照的成交重复叠加；应收加入总资产但不加入证券市值，预估利息继续延后到实际资金事件确认。
- `2026-09-14` 已从各账户确认的 Relay 可信起点重算历史 close 资产：`307000051387/1388` 从 `2026-07-27`、`307000051389` 从 `2026-07-28`、`314000046830/501000114077` 从 `2026-07-22` 起算。四个非空账户共修正 146 个账户日，其中 110 日从不可变 `broker_close` 结合 Meridian 日线重估，36 日使用已验收券商交割单 close 持仓汇总；93 个逆回购账户日按快照时点判为 84 日本金应收、9 日本金仍在旧现金。6,511 条持仓及 180 条资产恒等式零失败，133 条正式 NAV 只读复算数值零差异，因此不新增绩效版本。富盈13号仍等待起算日和期初持仓锚点确认，未纳入本轮历史重算。详见 [历史资产重估报告](/home/ti-relay-trader/docs/HISTORICAL_ASSET_REVALUATION_20260914.md:1)。
- 债享5号券商资金表覆盖 `2026-06-01..08-26` 共 62 个交易日，逐日资产恒等式全部闭合；正式绩效仍从有 OC 空仓干净锚点的 `2026-07-22` 起算。该日起 26 个交易日的日初、日末资产及每日盈亏均与券商记录精确到分，原 7 个 legacy 阻断日已全部恢复为 v2.7 provisional。
- 添利1号从 `2026-07-22` 干净起点开始核对。14 个已取得真实清算证据的 `159915.SZ` 赎回日已从 IOPV+15bp 暂估升级为版本化终值，公式使用实际 ETF 买入、OC 成分卖出、真实现金差额/现金替代、实际费用及 Meridian PCF；`2026-07-22..08-24` 共 24 个 NAV 日连续可算、归因残差为 0。`2026-08-25` 的 `512700.SH` 仍缺最终 cash component，保持 pending，8 月 25/26 日不发布伪终值。
- 债享5号资金流水一次性确认 14 笔逆回购实际净息、6 笔分红、3 笔红利税和 1 笔 `3,100 CNY` 银证转出。分红事实保留为 operational 审计记录，绩效由 Meridian 公司行为调整后的前收盘口径体现，避免重复计收益；逆回购净息和红利税进入 `income_expense`。
- 两份开户以来的券商历史资金各形成 23 个版本化资产基数金标；`2026-08-06` 通过可信 `reconcile` 快照承接 8 月 5 日 ETF 待结算资产后，两户残差降至 `-1,554.76 / -1,621.70 CNY`，状态由 blocked 改为 provisional。OC 原始 `open/close/broker_close` 快照未覆盖。
- 资金流水和历史资金只作本次事故的外部权威证据，不建设日常导入任务。未来仍依赖对应券商 OC 当日数据补充柜台范围、内部划转和资金明细；华鑫极速柜台可见性限制不得外推到其他券商。
- 两户均于 `2026-07-27` 盘前入金并开始交易；原起算日误用了 Relay 首次取得 OC 快照的日期（分别为 7 月 29 日和 28 日）。券商资金与交割单已一次性恢复两户 7 月 27 日及 `307000051387` 的 7 月 28 日账本，逐证券数量桥和 Meridian 收盘市值均闭合。
- 两户 `2026-07-27..2026-08-26` 各 23 个交易日、共 46 个账户日已按 `performance_economic_nav.v2.7` 顺序重建，0 blocked。结果仍为 provisional，因为部分历史费用、ETF 清算资产和归因使用明确标记的估算口径。
- `performance_position_cost.v3.2` 已实现 `meridian_pre_close_mark_to_market`：仅在人工确认起算日按盘前数量和 Meridian 未复权前收盘建立 CORE 初始成本，缺行情即阻断且不回退柜台污染成本。富盈13号仍待确认具体起算日和盘前持仓锚点，生产账户配置未修改。
- Chronos 已独立确认 `relay-sdk 0.1.33` 的 P0 数据与事件能力、P1 接口契约、全量分页审计和 SSE 对账页证据均通过。报告中的 4 条旧终态拒单正残量已追到 OC 原始归档：终态和零成交可信，但无标准拒绝文本；`trade_quality.v7` 不再把内部迁移审计 `reason` 误作柜台原因，原始账本保持不变且残量不可执行。不支持的北交所留作未来升级。
- `relay-sdk 0.1.34` 已按 Chronos R2c 要求补齐单笔、批量和撤单成功回执的稳定 `action`；真实测试覆盖单笔、批量、working 订单首次撤单和三类幂等重放，所有重放均为 0 次额外 Redis 发布。SDK 对缺失/错误动作按结果未知失败关闭，用户已确认 Chronos 完成当天联调，详见 [Chronos R2c 写回执验收](/home/ti-relay-trader/docs/CHRONOS_R2C_WRITE_RECEIPT_ACCEPTANCE_20260914.md:1)。
- `2026-09-02` 的 14 条 `meridian_watermark_poll` 是 `16:40..18:50` 对同一权威绩效复算的重复水位检查，并非 14 个独立任务；根因是 Meridian 当日父任务受 3 只新股行业映射缺口阻断。现已补写单一终态记录 `performance_canonical-20260902-watermark-poll`，旧记录保留审计但在页面折叠。

### 下一步

1. 与 OC 协调当前交易日的多柜台资金范围、按需柜台划转事件和资金明细字段，让今后同类日直接依赖 OC，不要求 OC 提供历史查询。
2. 从后续自然交易日持续验收 OC 当日资金、逆回购净息、公司行为和外部资金流，确保历史券商文件只停留在一次性事故修复边界。
3. 等待添利1号 `2026-08-25` 赎回的真实清算资金证据；到账后以同一版本化终值口径完成 8 月 25/26 日，不使用 PCF 预计现金提前确认。
4. 与用户确认富盈13号的可信起算日和盘前持仓锚点，再启用 `meridian_pre_close_mark_to_market` 顺序重建；确认前不改生产配置。
5. 次优先项为内部 Webhook 告警实配、数据库异机备份及长区间交易质量查询性能优化。
6. 当前处于测试环境，账户 `00030484` 已可用于策略联调；任何切回生产或调整生产交易权限都必须收到用户新的明确指令。

## 系统边界

- **Go**：9092 在线 API、交易终端、API Console、多账户路由、订单状态机、Redis Stream 消费、PostgreSQL 账本和健康监控。
- **Python**：盘前初始化、收盘捕获、盘后结算、绩效计算、历史修复和验收脚本。
- **OC 前置**：统一券商协议和字段，负责交易日当天的命令、查询、订单、成交、费用和 ETF 划转事实；不提供历史查询能力。
- **PostgreSQL**：测试库 `relay_trader_test`、生产库 `relay_trader`，保存标准账本、快照、任务、对账、成本和绩效版本。
- **Redis Stream**：Relay 与 OC 的实时通信协议，遵循 `relay.stream.v1`；worker 独占生产 output stream 消费并持久化 checkpoint。
- **Meridian**：证券主数据、交易日、Level1、bars、adjust factors、ETF PCF/IOPV 的唯一行情标准来源。Relay 只做薄代理和业务计算，不另造行情字段。
- **模拟撮合**：不属于 Relay；实盘接口调试使用券商测试环境，历史撮合归回测引擎。

详细设计见 [架构文档](/home/ti-relay-trader/docs/ARCHITECTURE.md:1)、[交易接口 Schema](/home/ti-relay-trader/docs/TRADING_API_SCHEMA.md:1) 和 [数据模型](/home/ti-relay-trader/docs/DATA_MODEL.md:1)。

## 核心契约

- 环境选择完全在 Relay 服务端完成，SDK 只连接 `base_url`；测试和生产使用独立 Redis 配置与 PostgreSQL 数据库。环境切换属于受保护操作，只能依据当前用户明确给出的目标环境执行，时间提醒、历史指令和任务计划均不构成授权。
- 真实凭据只允许存在于未跟踪配置，例如 `config/relay.prod.yaml`，不得写入 README、日志、提交或前端响应。
- 生产切换默认只读。只有账户 `enabled=true && trading_enabled=true` 才允许发交易命令，生产启动脚本还要求显式人工确认。
- 订单业务唯一键为 `account_id + trade_date + gateway_order_id`；`req_id` 是客户端请求 ID，`order_id` 是柜台 ID，`order_stream_id` 是交易所/柜台委托流 ID，均保留用于关联和审计。
- 本地订单首次提交的 `origin_message_id`、`request_id` 和 `idempotency_key` 是不可变命令身份，后续查询回报和状态事件不得覆盖；批量子单通过同一 `origin_message_id` 完整回查。
- 成交必须关联订单并按账户、交易日、订单作用域幂等；ETF 赎回 0 价成分划转使用 `transfer.event`，不得伪装成普通成交。
- 逐笔成交时间唯一消费口径为 `fill.matched_at`；订单 `accepted_at/last_updated_at/terminal_at` 只描述订单生命周期，不能用于成交展示、成交回调、TCA 或逐笔成交事实。
- 相同幂等键和相同 payload 返回原回执并标记 replay；相同键不同 payload 返回 `IDEMPOTENCY_CONFLICT`，终态订单不得被重复提交回退。
- 测试环境订单和成交默认查询东八区自然日，生产环境默认查询 Meridian 最近交易日；历史订单、成交和持仓使用独立历史接口。表格查询使用服务端 cursor 分页。
- 价格展示位数和可报步长分别读取 Meridian `price_decimals`、`price_tick`；当前沪深股票为 2 位、ETF/可转债为 3 位。北交所未纳入当前实盘能力。
- 绩效正式净值使用资金、Meridian 重估持仓、确认资金流和可审计调整，不依赖 ETF 申赎后受污染的柜台平均成本。
- 原始 Redis 消息、DLQ、券商回包和修复证据永久保留；修复采用版本化或审计记录，不覆盖原始事实。

Redis Stream 细节见 [前置对接手册](/home/ti-relay-trader/docs/THIRD_PARTY_INTEGRATION_GUIDE.md:1) 和 [账本同步设计](/home/ti-relay-trader/docs/REDIS_LEDGER_SYNC.md:1)。

## 交易日流程

| 时间/触发 | 任务 | 关键行为 |
| --- | --- | --- |
| 09:01 | `pre_open_init` | 判断交易日，刷新账户数据，固化 `open` 资金和持仓 |
| 15:01 | `post_close_capture` | 不依赖 Meridian，查询 OC 最终资金、持仓、订单、成交和费用，固化不可变 `broker_close` |
| 捕获成功后 | `post_close_settlement` | 从 `broker_close` 结合 Meridian 生成正式 `close`、对账输入和差异 |
| 结算成功后 | `performance_daily` | 计算移动成本、经济 NAV 和质量状态，阻断账户不影响其他账户 |
| 16:40-18:50 每 10 分钟 | `performance_canonical` | 对齐 Meridian 16:30 启动、16:45 SLA；同一交易日轮询复用一条任务记录，18:50 仍未就绪则明确标记上游阻塞 |

非交易日通过 Meridian 交易日接口跳过。账户级查询失败单独标注；只有系统依赖失败、全部账户阻断或写库失败才使整项任务失败。完整流程见 [交易日工作流](/home/ti-relay-trader/docs/TRADING_DAY_WORKFLOW.md:1)。

## 运维速查

```bash
# 运行状态、启动、重启和日志
scripts/relay-runtime-service.sh status
scripts/relay-runtime-service.sh start
scripts/relay-runtime-service.sh restart
scripts/relay-runtime-service.sh logs-api
scripts/relay-runtime-service.sh logs-worker

# 测试/生产环境切换；生产默认拒绝带下单权限的配置
scripts/switch-relay-env.sh test
scripts/switch-relay-env.sh production

# 安装容器重启自恢复与交易日任务 cron
scripts/relay-runtime-service.sh install-cron
scripts/trading-jobs-cron.sh install

# 基础验证
curl -fsS http://127.0.0.1:9092/healthz
curl -fsS http://127.0.0.1:9092/v1/status
go test ./...
PYTHONPATH=src:sdk/python .venv/bin/python -m unittest discover -s tests/unit -p 'test_*.py'
PYTHONPATH=sdk/python .venv/bin/python -m unittest discover -s sdk/python/tests -p 'test_*.py'
```

生产日志位于 `/var/log/relay/`；任务报告位于 `/var/log/relay/reports/`；canonical 完成标记位于 `/var/log/relay/state/`。完整说明见 [运维手册](/home/ti-relay-trader/docs/OPERATIONS.md:1) 和 [运行进程说明](/home/ti-relay-trader/docs/RUNTIME_PROCESSES.md:1)。

## Web 入口

| 路径 | 用途 |
| --- | --- |
| `/` | 项目文档首页和环境摘要 |
| `/trade` | 交易终端、资金持仓、订单监控、绩效分析与设置 |
| `/api-console` | Apifox 风格接口测试台 |
| `/jobs` | 盘前、盘后和绩效任务状态 |
| `/operations` | Gateway、Redis lag、checkpoint 和 DLQ 运维 |
| `/sdk/` | Python SDK 下载与文档 |
| `/docs` | 全部设计与运行文档 |
| `/docs/readme-history-20260826` | README 精简前的完整历史归档 |
| `/tests` | 测试目录与页面冒烟入口 |

## 核心里程碑

| 阶段 | 状态 | 已交付结果 |
| --- | --- | --- |
| P0-P1 | 完成 | 9092 文档门户、Go/Python 工程骨架、配置、日志、测试和线程恢复机制 |
| P2-P4 | 完成 | 标准交易 schema、多账户路由、Redis Stream 命令/事件/恢复/checkpoint/DLQ |
| P5-P6 | 持续生产化 | PostgreSQL 标准账本、交易 API、SSE、Python SDK、API Console 和交易终端 |
| P7 | 持续生产化 | 盘前、收盘捕获、盘后结算、任务页、对账和账户级故障隔离 |
| P8/N8 | 完成主能力 | 历史查询、绩效页面、CSV、收益/回撤/贡献/成本/质量展示 |
| N9-N12 | 完成 | Stream 运维、环境隔离、人工复核、回归测试、API/worker 常驻和发布流程 |
| N13 | 进行中 | 可信成本、公司行为、ETF T0/CORE 分账、实际费用、经济 NAV 和 canonical 复算 |
| P9 | 暂缓 | 模拟撮合归回测引擎，不进入 Relay 实盘边界 |

详细完成项和验收证据以 [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1) 为准。精简前逐日工作日志保存在 [历史归档](/home/ti-relay-trader/docs/README_HISTORY_20260826.md:1)。

## 关键文档

- [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1)
- [架构与当前实现](/home/ti-relay-trader/docs/ARCHITECTURE.md:1)
- [交易接口 Schema](/home/ti-relay-trader/docs/TRADING_API_SCHEMA.md:1)
- [交易终端](/home/ti-relay-trader/docs/TRADING_TERMINAL.md:1)
- [Python SDK](/home/ti-relay-trader/docs/PYTHON_SDK.md:1)
- [绩效分析设计](/home/ti-relay-trader/docs/PERFORMANCE_ANALYSIS_DESIGN.md:1)
- [绩效净值金标](/home/ti-relay-trader/docs/PERFORMANCE_NAV_GOLD.md:1)
- [两户券商资金流水一次性审计](/home/ti-relay-trader/docs/BROKER_CASH_FLOW_AUDIT_20260827.md:1)
- [添利1号 ETF T0 最终清算审计](/home/ti-relay-trader/docs/TIANLI1_ETF_SETTLEMENT_RECONCILIATION_20260828.md:1)
- [Meridian 证券价位契约验收](/home/ti-relay-trader/docs/MERIDIAN_INSTRUMENT_PRICE_TICK_REQUIREMENTS_20260902.md:1)
- [Meridian 盘后水位协调](/home/ti-relay-trader/docs/MERIDIAN_POSTCLOSE_READINESS_COORDINATION_20260826.md:1)
- [2026-08-06 延后结算记录](/home/ti-relay-trader/docs/SETTLEMENT_HOLD_20260806.md:1)
- [数据库迁移](/home/ti-relay-trader/docs/MIGRATIONS.md:1)
- [备份与恢复](/home/ti-relay-trader/docs/DATABASE_BACKUP_RESTORE.md:1)

## 状态维护规则

- 重要变更只更新本 README 的恢复卡片、当前阻塞、下一步和核心里程碑；详细实现清单更新到 `docs/ROADMAP.md`，专项结论写入对应文档。
- 不再在 README 追加逐日工作日志。需要长期保留的故障、验收和决策应写独立文档并在路线图中链接。
- 每次项目更新后自动执行 Git 提交。
- 禁止把密码、Token、DSN、生产柜台地址或其他敏感信息写入仓库。
