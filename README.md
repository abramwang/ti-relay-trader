# relay - TI Relay Trader

relay 是量化研究系统的交易基础数据项目，负责标准化实盘柜台接口、多账户路由、Redis Stream 对接、交易账本、交易日任务、绩效计算和研究侧导出。

## 线程恢复卡片

| 项目 | 当前口径 |
| --- | --- |
| Agent | `relay` |
| 工作目录 | `/home/ti-relay-trader` |
| 对外服务 | `http://relay-trader.quantstage.com`，端口 `9092` |
| 业务时区 | `Asia/Shanghai`，所有交易日、任务和业务时间按东八区解释 |
| 当前环境 | 测试环境，`config/relay.local.yaml`，API 内嵌账本同步，PostgreSQL `relay_trader_test` |
| 安全状态 | 测试账户 `00030484` 查询/交易路由开启；生产六账户及生产数据库不在当前运行态，运维管理写操作关闭 |
| 当前阶段 | P0-P4 完成，P5-P8/P10 持续生产化；N8-N12 完成；N13 可信成本账与绩效重建进行中 |
| 最近确认 | `2026-09-06` 测试 OC 查询链路正常；非交易日下单可见性已修复：测试环境订单/成交默认东八区自然日，生产环境仍默认 Meridian 最近交易日；行情、绩效和资金持仓口径不变 |
| 更新时间 | `2026-09-06` |

新线程按以下顺序恢复：

1. 阅读本节和“当前工作”。
2. 阅读 [开发路线图](/home/ti-relay-trader/docs/ROADMAP.md:1)，确认最新未完成项。
3. 执行 `git status --short`、`scripts/relay-runtime-service.sh status` 和 `curl -fsS http://127.0.0.1:9092/v1/status`。
4. 涉及生产任务时先查看 `/jobs`、`/operations` 及 `/var/log/relay/`，不得仅凭页面摘要修改账本。
5. 需要追溯旧决策时再阅读 [README 完整历史归档](/home/ti-relay-trader/docs/README_HISTORY_20260826.md:1)。

## 当前工作

### 已验证运行态

- `2026-09-06` 当前运行态已持久切换到测试环境：`.runtime/active-config.yaml -> config/relay.local.yaml`，账户 `00030484` 的查询和交易路由开启；API 使用内嵌账本同步，因此独立 `relay-worker` 按配置停用。测试 OC 状态为 `UP`，`redis_ready/broker_ready/order_snapshot_ready=true`；资金、持仓、订单、成交查询均成功终结，账本无解析错误或 DLQ，完整只读 SDK 冒烟通过。分段持仓回包已容忍 OC/Relay 毫秒级时钟偏差，旧回包不能覆盖新持仓，当前 10 条持仓稳定保留。
- `2026-09-06` 测试批量单已验证 `HTTP 202 -> accepted reply -> order.event -> PostgreSQL -> Web`。OC 后续状态事件曾把同一订单交易日从 `20260906` 改为 `20450624` 并丢失命令关联，Relay 已按订单时间防止异常日期拆单、保留原值审计，并让批量页按 `message_id` 自动刷新回报；OC 侧反馈见 [测试批量订单回报反馈](/home/ti-relay-trader/docs/OC_TEST_BATCH_ORDER_FEEDBACK_20260906.md:1)。
- 交易终端日期按环境分流：测试环境订单/成交监控默认东八区自然日，允许周末和节假日查看测试柜台订单；生产环境仍默认 Meridian 最近交易日。K 线、绩效和资金持仓在两个环境中均继续使用最近交易日口径。
- 生产 API、worker、PostgreSQL、Redis、事件桥、Meridian 行情代理和订单服务均已接通；API 监听 `0.0.0.0:9092`，worker 健康端口只监听 `127.0.0.1:19092`。
- 生产账户为 `501000114077`、`314000046830`、`314000045768`、`307000051388`、`307000051389`、`307000051387`；别名由 PostgreSQL 管理，账户 ID 始终作为路由和账本主键。
- 每个资金账户都带必填 `broker_id` 所属券商标签；当前六户均为 `huaxin`。该标签与账户别名、Gateway 和环境分离，后续新增券商沿用同一账户路由模型。
- `2026-08-26` 已验证 `archive_incomplete -> Level1 provisional -> canonical daily` 全链路：3 个活跃账户 ready，1 个空账户 not_applicable，0 blocked；权威日线复算与 provisional NAV 差异为 0。
- Meridian 权威日线父任务当前 16:30 启动、16:45 为完成 SLA；Relay 16:40 首查并每 10 分钟重试至 18:50。窗口内显示等待，18:50 仍未就绪则标记 Meridian 上游阻塞；同一交易日所有轮询复用一个 `run_id`。
- 生产 schema 当前为 `27 order_submission_identity`，Python SDK 当前版本为 `relay-sdk==0.1.33`。
- 公网绩效写入口和生产下单权限保持关闭；本机任务可按质量门禁写入版本化绩效结果。

### 当前进展与阻塞

- 债享5号券商资金表覆盖 `2026-06-01..08-26` 共 62 个交易日，逐日资产恒等式全部闭合；正式绩效仍从有 OC 空仓干净锚点的 `2026-07-22` 起算。该日起 26 个交易日的日初、日末资产及每日盈亏均与券商记录精确到分，原 7 个 legacy 阻断日已全部恢复为 v2.7 provisional。
- 添利1号从 `2026-07-22` 干净起点开始核对。14 个已取得真实清算证据的 `159915.SZ` 赎回日已从 IOPV+15bp 暂估升级为版本化终值，公式使用实际 ETF 买入、OC 成分卖出、真实现金差额/现金替代、实际费用及 Meridian PCF；`2026-07-22..08-24` 共 24 个 NAV 日连续可算、归因残差为 0。`2026-08-25` 的 `512700.SH` 仍缺最终 cash component，保持 pending，8 月 25/26 日不发布伪终值。
- 债享5号资金流水一次性确认 14 笔逆回购实际净息、6 笔分红、3 笔红利税和 1 笔 `3,100 CNY` 银证转出。分红事实保留为 operational 审计记录，绩效由 Meridian 公司行为调整后的前收盘口径体现，避免重复计收益；逆回购净息和红利税进入 `income_expense`。
- 两份开户以来的券商历史资金各形成 23 个版本化资产基数金标；`2026-08-06` 通过可信 `reconcile` 快照承接 8 月 5 日 ETF 待结算资产后，两户残差降至 `-1,554.76 / -1,621.70 CNY`，状态由 blocked 改为 provisional。OC 原始 `open/close/broker_close` 快照未覆盖。
- 资金流水和历史资金只作本次事故的外部权威证据，不建设日常导入任务。未来仍依赖对应券商 OC 当日数据补充柜台范围、内部划转和资金明细；华鑫极速柜台可见性限制不得外推到其他券商。
- 两户均于 `2026-07-27` 盘前入金并开始交易；原起算日误用了 Relay 首次取得 OC 快照的日期（分别为 7 月 29 日和 28 日）。券商资金与交割单已一次性恢复两户 7 月 27 日及 `307000051387` 的 7 月 28 日账本，逐证券数量桥和 Meridian 收盘市值均闭合。
- 两户 `2026-07-27..2026-08-26` 各 23 个交易日、共 46 个账户日已按 `performance_economic_nav.v2.7` 顺序重建，0 blocked。结果仍为 provisional，因为部分历史费用、ETF 清算资产和归因使用明确标记的估算口径。
- `performance_position_cost.v3.2` 已实现 `meridian_pre_close_mark_to_market`：仅在人工确认起算日按盘前数量和 Meridian 未复权前收盘建立 CORE 初始成本，缺行情即阻断且不回退柜台污染成本。富盈13号仍待确认具体起算日和盘前持仓锚点，生产账户配置未修改。
- Chronos 已独立确认 `relay-sdk 0.1.33` 的 P0 数据与事件能力、P1 接口契约、全量分页审计和 SSE 对账页证据均通过，Relay SDK 消费端验收正式关闭。报告中的 4 条旧终态拒单正残量已追到 OC 原始归档：终态和零成交可信，但无标准拒绝文本；`trade_quality.v7` 不再把内部迁移审计 `reason` 误作柜台原因，原始账本保持不变且残量不可执行。真实写验收等待测试环境，不支持的北交所留作未来升级。
- `2026-09-02` 的 14 条 `meridian_watermark_poll` 是 `16:40..18:50` 对同一权威绩效复算的重复水位检查，并非 14 个独立任务；根因是 Meridian 当日父任务受 3 只新股行业映射缺口阻断。现已补写单一终态记录 `performance_canonical-20260902-watermark-poll`，旧记录保留审计但在页面折叠。

### 下一步

1. 与 OC 协调当前交易日的多柜台资金范围、按需柜台划转事件和资金明细字段，让今后同类日直接依赖 OC，不要求 OC 提供历史查询。
2. 从后续自然交易日持续验收 OC 当日资金、逆回购净息、公司行为和外部资金流，确保历史券商文件只停留在一次性事故修复边界。
3. 等待添利1号 `2026-08-25` 赎回的真实清算资金证据；到账后以同一版本化终值口径完成 8 月 25/26 日，不使用 PCF 预计现金提前确认。
4. 与用户确认富盈13号的可信起算日和盘前持仓锚点，再启用 `meridian_pre_close_mark_to_market` 顺序重建；确认前不改生产配置。
5. 次优先项为内部 Webhook 告警实配、数据库异机备份及长区间交易质量查询性能优化。
6. 当前测试 OC 和资金快照已就绪；下一步可在测试账户执行 Chronos P1 的批量部分拒绝、`BROKER_NOT_READY` 和结果未知等真实写验收。切回生产前仍保持生产账户只读，不为验收向生产账户制造订单。

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

- 环境选择完全在 Relay 服务端完成，SDK 只连接 `base_url`；测试和生产使用独立 Redis 配置与 PostgreSQL 数据库。
- 真实凭据只允许存在于未跟踪配置，例如 `config/relay.prod.yaml`，不得写入 README、日志、提交或前端响应。
- 生产切换默认只读。只有账户 `enabled=true && trading_enabled=true` 才允许发交易命令，生产启动脚本还要求显式人工确认。
- 订单业务唯一键为 `account_id + trade_date + gateway_order_id`；`req_id` 是客户端请求 ID，`order_id` 是柜台 ID，`order_stream_id` 是交易所/柜台委托流 ID，均保留用于关联和审计。
- 本地订单首次提交的 `origin_message_id`、`request_id` 和 `idempotency_key` 是不可变命令身份，后续查询回报和状态事件不得覆盖；批量子单通过同一 `origin_message_id` 完整回查。
- 成交必须关联订单并按账户、交易日、订单作用域幂等；ETF 赎回 0 价成分划转使用 `transfer.event`，不得伪装成普通成交。
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
