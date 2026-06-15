# relay - TI Relay Trader

relay 是量化研究系统的基础数据项目，负责标准化实盘/券商测试柜台接口对接、交易账表落盘、盘后对账和研究侧导出能力。

## 线程恢复卡片

- Agent 名称: relay
- 工作目录: `/home/ti-relay-trader`
- 对外端口: `9092`
- 最终服务口径: `http://relay-trader.quantstage.com`
- 当前状态: P1/P2/P4/P5/P6/P7 底座已可用于券商测试环境联调，P8 历史数据与盈亏统计已完成第一版。9092 当前以文档门户同源挂载 `/v1/*` API，接入测试 PostgreSQL、测试 Redis 和测试账户 `00030484`；已实现单笔/批量下单、撤单、资金/持仓/订单/成交查询与刷新、Redis reply/event 到 PostgreSQL 账本同步、SSE 事件流、Python SDK 0.1.9、盘前/盘后任务、close 快照、对账输入/差异表、绩效序列、Meridian bars 基准对照、Meridian level1 SSE 持仓实时估值、研究侧 PostgreSQL 导出 view，以及测试/生产运行环境显式切换护栏。P9 内置模拟柜台已明确暂缓，下一步优先补 `/trade` 批量下单测试视图、Playwright 页面回归测试、worker 心跳状态建模和 DLQ 告警。
- 当前 9092 运行态: 使用未跟踪本地配置 `config/relay.prod.yaml` 启动生产查询/订阅模式，`service.environment=production`，生产 Redis ping 正常，账户路由为 `501000114077`，别名 `生产查询账户`，`enabled=true`、`trading_enabled=false`、`auto_refresh=false`。允许手动账户/资产/持仓/订单/成交查询刷新和订单成交推送订阅，不开放下单或撤单交易权限。只读扫描发现生产 stream 前缀 `relay:prod:v1:huaxin:501000114077`，当前存在 `cmd.trade`、`cmd.query` 和 `hb`，心跳持续更新。该文件包含凭据且不提交；生产 Redis 凭据只允许进入未跟踪本地配置或安全运行环境，不写入仓库。
- 最近更新时间: `2026-06-15`
- 恢复方式: 新线程进入本目录后，先阅读本 README 的“线程恢复卡片”“当前进展”“待办事项”“工作日志”，再继续执行下一项待办。

## 项目目标

1. 提供统一的 A 股交易接口，供交易软件和策略调用。
2. 通过 Redis Stream 对接托管机房前置服务，前置层已统一券商结构体和协议。
3. 支持多账户、多 broker、多 gateway 的交易路由和状态管理。
4. 提供盘后对账、历史数据接入、账户盈亏统计和研究侧导出。
5. 对外暴露稳定服务端口 `9092`，供量化研究系统内其他模块调用。
6. 将关键设计决策、运行状态、接口约定和未完成事项持续沉淀在本 README 中，保证 Codex 线程中断后可以快速恢复。

## 技术栈与当前边界

- Go: 负责 9092 在线服务、标准化交易 API、多账户订单状态机、Redis Stream 对接、实时账表写入和健康监控。
- Python: 负责盘后对账、历史数据拉取、账户盈亏统计、研究侧脚本、验收脚本和批处理任务。
- PostgreSQL: 建议作为交易账表、资产快照、对账结果和盈亏统计的主存储。
- Redis: 用于和托管机房前置服务通信，遵循 `relay.stream.v1` Redis Stream 协议。
- Meridian: 用于 A 股历史数据和行情数据接入，入口为 `http://meridian-data.quantstage.com`。

详细架构见 [docs/ARCHITECTURE.md](/home/ti-relay-trader/docs/ARCHITECTURE.md:1)。

## 职责范围

### 标准化实盘交易柜台接口

- 账户查询
- 资金查询
- 持仓查询
- 委托下单
- 撤单
- 委托回报
- 成交回报
- 柜台连接状态与心跳
- 错误码标准化
- 多账户路由和权限状态

### 盘后与数据服务

- 盘后对账
- 历史数据接入
- 账户盈亏统计
- 资产快照
- 持仓快照
- 对账差异记录
- 研究侧数据导出

### 模拟撮合边界

- relay 暂不内置模拟柜台和撮合引擎。
- 实盘接口调试优先使用券商测试环境。
- 基于历史行情的模拟撮合放在回测引擎中实现，避免 relay 的实盘交易边界和行情撮合边界混淆。
- 如果后续需要接入外部模拟柜台，应通过前置服务和 `relay.stream.v1` Redis Stream 协议接入，relay 仍只负责标准接口、账本、事件和审计。

## 目录结构

```text
.
├── README.md
├── cmd/
│   ├── relay-docs/      # 9092 文档门户入口，不包含交易核心逻辑
│   └── relayctl/        # 运维和联调 CLI，当前包含 Redis Stream 探测、账本同步和 migration
├── config/              # 本地配置、示例配置、环境变量模板
├── docs/                # 设计文档、接口文档、状态补充说明
├── internal/
│   ├── api/             # 9092 API 服务、健康检查、交易接口、页面和状态查询
│   ├── config/          # Go 配置加载、服务模式和账户路由配置模型
│   ├── db/              # PostgreSQL migration runner
│   ├── events/          # 9092 进程内 SSE 事件 hub
│   ├── httpx/           # HTTP request_id、中间件、统一 JSON envelope
│   ├── ledger/          # PostgreSQL 账本写入 repository，覆盖账户、订单、事件、成交和原始 stream 消息
│   ├── logging/         # 结构化日志初始化
│   ├── market/          # Meridian 行情薄客户端，不重新定义行情字段
│   ├── orderflow/       # 订单 API 编排：账户路由、订单草稿、Redis 命令写入和命令归档
│   ├── redisstream/     # Redis Stream 命名、命令 envelope、消息摘要、账本同步和探测边界
│   ├── timeutil/        # Asia/Shanghai 业务时区和时间格式化
│   ├── trading/         # 统一交易接口 schema、枚举、基础校验和状态机语义
│   └── worker/          # 后台 worker 常驻进程，承接 Redis 同步、checkpoint 和自动刷新
├── migrations/
│   └── postgres/        # PostgreSQL 交易账本 migration
├── public/
│   └── sdk/             # 9092 /sdk/ 下载入口发布的 SDK 安装包
├── scripts/             # 开发、运维、迁移、数据初始化脚本
├── sdk/
│   └── python/          # relay-sdk Python 客户端包
├── src/
│   └── relay/
│       ├── api/         # 对外 API 服务，默认监听 9092
│       ├── connectors/  # 实盘柜台、券商、网关适配器
│       ├── jobs/        # Python 盘前初始化和收盘后结算任务
│       ├── schemas/     # 标准化请求、响应、事件、账表模型
│       └── services/    # 业务服务与编排逻辑
└── tests/
    ├── integration/     # 集成测试
    └── unit/            # 单元测试
```

## 端口约定

- 对外服务端口固定为 `9092`。
- 最终服务口径固定为 `http://relay-trader.quantstage.com`。
- 当前 9092 由本地配置控制监听地址，文档门户/交易 API 均可监听 `0.0.0.0:9092`，方便域名映射和内网系统访问。
- 当前优先运行文档门户模式，展示项目框架、文档和测试目录；文档门户也会同源挂载 `/v1/*` API handler，基础发现接口可直接测试，交易和账本接口是否可用取决于本地配置是否包含 PostgreSQL、测试 Redis 和账户路由。

## 9092 文档门户

启动命令：

```bash
scripts/serve-docs.sh
```

也可以直接指定监听地址：

```bash
RELAY_DOCS_ADDR=0.0.0.0:9092 scripts/serve-docs.sh
```

页面入口：

| 路径 | 内容 |
| --- | --- |
| `http://relay-trader.quantstage.com/` | 文档门户首页 |
| `http://relay-trader.quantstage.com/healthz` | 文档门户健康检查 |
| `http://relay-trader.quantstage.com/api-console` | Apifox 风格接口测试台 |
| `http://relay-trader.quantstage.com/trade` | 成熟交易软件风格手动交易测试终端 |
| `http://relay-trader.quantstage.com/jobs` | 后台任务状态监控，展示盘前初始化、盘后结算等任务 |
| `http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.9.tar.gz` | Python SDK 安装包 |
| `http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.9.tar.gz.sha256` | Python SDK 安装包 SHA256 |
| `http://relay-trader.quantstage.com/docs` | 文档列表 |
| `http://relay-trader.quantstage.com/docs/readme` | README |
| `http://relay-trader.quantstage.com/docs/architecture` | 架构与当前实现 |
| `http://relay-trader.quantstage.com/docs/roadmap` | 开发路线图 |
| `http://relay-trader.quantstage.com/docs/data-model` | 数据模型与落盘设计 |
| `http://relay-trader.quantstage.com/docs/migrations` | PostgreSQL migration |
| `http://relay-trader.quantstage.com/docs/trading-api-schema` | 统一交易接口 Schema |
| `http://relay-trader.quantstage.com/docs/api-test-console` | 接口测试台设计说明 |
| `http://relay-trader.quantstage.com/docs/trading-terminal` | 交易终端设计说明 |
| `http://relay-trader.quantstage.com/docs/python-sdk` | Python SDK 设计 |
| `http://relay-trader.quantstage.com/docs/operations` | 运行配置与任务管理 |
| `http://relay-trader.quantstage.com/docs/trading-day-workflow` | 交易日流程 |
| `http://relay-trader.quantstage.com/docs/redis-stream-probe` | Redis Stream 只读探测 |
| `http://relay-trader.quantstage.com/docs/redis-ledger-sync` | Redis reply/event 到 PostgreSQL 账本同步 |
| `http://relay-trader.quantstage.com/docs/third-party-integration` | 前置服务 Redis Stream 对接手册 |
| `http://relay-trader.quantstage.com/tests` | 测试目录索引和测试目录树 |
| `http://relay-trader.quantstage.com/tree` | 项目结构 |

## 外部依赖

- 前置服务 Redis Stream 对接手册: [docs/THIRD_PARTY_INTEGRATION_GUIDE.md](/home/ti-relay-trader/docs/THIRD_PARTY_INTEGRATION_GUIDE.md:1)
- 内网资源文档: `http://doc.quantstage.com`
- Meridian A 股数据源文档: `http://meridian-data.quantstage.com`

注意：不要把内网资源里的密码、Token、生产账号、柜台地址写入仓库。

## 当前进展

- [x] 初始化项目根目录。
- [x] 创建基础目录骨架。
- [x] 创建 README 并写入线程恢复信息。
- [x] 添加目录占位文件，确保目录骨架可被 Git 跟踪。
- [x] 形成 Go + Python 技术栈和职责边界。
- [x] 明确前置服务已统一券商协议，relay 聚焦多账户和标准 API。
- [x] 实现 9092 文档门户模式，可通过 Web 查看项目框架、文档和测试目录。
- [x] 在首页提供开发路线图入口，路线图详情通过 `/docs/roadmap` 查看。
- [x] 明确账户交易数据、订单数据、成交数据最终需要落盘，PostgreSQL 作为优先候选。
- [x] 明确真实凭据可放在部署机本地配置文件，后台批处理可采用 cron 管理。
- [x] 明确业务时间统一为 `Asia/Shanghai` 东八区，A 股交易日、cron、报表、页面和 API 业务字段都按该时区解释。
- [x] 明确每日交易主流程包含 `pre_open_init` 盘前初始化和 `post_close_settlement` 收盘后结算。
- [x] 新增统一时间工具，HTTP envelope、`/healthz`、SSE 事件、Redis command `sent_at` 和探测/同步报告生成时间按 `Asia/Shanghai` 输出。
- [x] 新增 Python 日流程任务入口：`python -m relay.jobs.pre_open_init` 和 `python -m relay.jobs.post_close_settlement`，支持交易日判断、依赖检查、账户刷新、账本快照摘要和 JSON 报告输出。
- [x] 已封装 Python SDK 给策略开发使用。
- [x] 参考 Meridian SDK，relay SDK 当前通过内网 HTTP 安装包供策略机 pip 安装。
- [x] 定义服务运行模式 `docs`、`api`、`worker`。
- [x] 增加基础配置加载，支持 `RELAY_CONFIG_PATH` 和 `config/relay.example.yaml` 模板。
- [x] 增加 `service.environment=test|production` 运行环境字段，`/v1/status` 和 `/trade` 顶部环境标识会展示实际加载环境。
- [x] 新增 `config/relay.test.example.yaml` 和 `config/relay.prod.example.yaml`，测试/生产环境通过未跟踪配置文件和 `RELAY_CONFIG_PATH` 切换，生产模板默认关闭账户交易权限。
- [x] 配置加载增加生产切换护栏：校验账户交易开关、生产模拟账户冲突和 Redis Stream 前缀与 `redis.env/broker/gateway` 一致性。
- [x] 首页新增运行环境控制台，直观展示当前测试/生产环境、配置文件、Redis/数据库配置、账户路由、下单权限、自动刷新和切换 runbook 入口。
- [x] 账户路由新增 `alias` 默认显示名，`GET /v1/accounts` 返回账户别名，`/trade` 可编辑别名并落库到 PostgreSQL `accounts.account_name`。
- [x] 增加结构化日志，默认 JSON 输出，HTTP 请求带 `request_id`。
- [x] 增加统一 JSON 响应 envelope 和标准错误码。
- [x] 增加 API/worker 启动入口，API 模式已提供 `/healthz`、`/v1/status`、`/v1/accounts`。
- [x] 定义第一版标准交易柜台接口 schema，覆盖账户、资金、持仓、下单、撤单、订单、成交和事件。
- [x] 记录前置测试环境已启动，并已基于 Redis Stream 完成查询、下单、撤单和事件消费联调。
- [x] 新增 Redis Stream 只读探测命令 `relayctl redis-probe` 和账户前缀扫描命令 `relayctl redis-scan`，支持本地配置和 `HX_REDIS_*` 环境变量。
- [x] 新增 Redis Stream 到 PostgreSQL 账本同步命令 `relayctl ledger-sync`，支持 `reply/event` 归档和完整事件落盘。
- [x] 9092 docs/api 模式启动轻量后台同步循环，持续消费测试 Redis `reply/event` 并更新 PostgreSQL 订单、成交、资金和持仓账本。
- [x] 正式 worker 模式接入 Redis 同步循环，可持续消费 `reply/event/hb/dlq` 并通过 PostgreSQL `stream_checkpoints` 恢复位点。
- [x] 新增 Apifox 风格接口测试台 `/api-console`，用于 API 联调。
- [x] 9092 文档门户同源挂载 `/v1/*` API handler，修复接口测试台无法发送请求查看返回的问题。
- [x] 参考 Meridian API 测试页优化 `/api-console`，每个接口按 path/query/body 参数生成表单，响应同时提供 JSON 和表格视图。
- [x] 将接口测试台从 Go 内联字符串拆分为 `web/templates/api_console.html`、`web/static/api-console.css`、`web/static/api-console.js` 和 `web/static/api-console.catalog.json`，由 Go `embed` 打包。
- [x] 新增 9092 页面轻量冒烟测试脚本 `tests/integration/page_smoke.py`，覆盖首页、文档、测试索引、API Console、交易终端、关键静态资源、基础 API 和 SDK 下载入口。
- [x] 新增 `/trade` 手动交易测试终端，参考成熟交易软件布局，支持账户切换、资金持仓、委托成交、下单、撤单、订单详情和轮询状态高亮。
- [x] 新增 PostgreSQL 首版账本 migration，覆盖账户、网关、订单、事件、成交、原始 stream、资金、持仓和对账表。
- [x] 新增 `stream_checkpoints` migration，持久化 Redis Stream 消费位点、处理计数和最近错误摘要。
- [x] 安装 PostgreSQL client，并新增 `relayctl migrate status/up/down` migration runner。
- [x] 使用真实 PostgreSQL 资源创建专用 `relay_trader` 数据库并应用首版账本 migration。
- [x] 新增 PostgreSQL 账本 repository，支持账户 upsert、订单 upsert、订单事件追加、成交幂等写入和 Redis 原始消息归档。
- [x] 新增可选 PostgreSQL 账本集成测试，设置 `RELAY_LEDGER_TEST_DATABASE_URL` 后可验证 repository 真实写库。
- [x] 使用真实 Redis 和 PostgreSQL 跑通 `ledger-sync` 小批量联调，归档 10 条 reply 和 10 条 event。
- [x] 新增 Redis command envelope/publisher 和 `internal/orderflow` 下单编排服务。
- [x] 实现 API 模式 `POST /v1/orders`，支持订单草稿落盘、Redis `cmd.trade` 写入和命令 raw 归档。
- [x] 使用测试 Redis 跑通一次单笔下单闭环：API 返回 `202`，订单草稿写入 PostgreSQL，命令写入 Redis，回流 reply/event/fill 后订单更新到 `filled`。
- [x] 实现 API 模式 `POST /v1/orders/{gateway_order_id}/cancel`，支持订单状态校验、Redis `order.cancel` 写入和命令 raw 归档。
- [x] 实现 API 模式 `GET /v1/orders` 和 `GET /v1/fills`，从 PostgreSQL 账本读取订单和成交，支持常用过滤条件和 limit 上限。
- [x] 实现 API 模式 `GET /v1/accounts/{account_id}/asset` 和 `GET /v1/accounts/{account_id}/positions`，从 PostgreSQL 最新资金快照和当前持仓表读取。
- [x] 实现 API 模式 `POST /v1/orders/batch`，支持批内校验、子订单草稿落盘、Redis `order.batch.submit` 写入和命令 raw 归档。
- [x] 确认测试下单参考行情口径：优先从 Meridian 读取 `2026-06-12` 分钟线；`600000.SH` 在 `15:00` 的 1m close 为 `9.67`，可作为测试委托参考价。
- [x] 实现 API 模式 `POST /v1/accounts/{account_id}/asset/refresh` 和 `POST /v1/accounts/{account_id}/positions/refresh`，写入 Redis `cmd.query` 并通过 `ledger-sync` 合并 `asset_page/position_page` 到 PostgreSQL。
- [x] 实现 API 模式 `POST /v1/accounts/{account_id}/orders/refresh` 和 `POST /v1/accounts/{account_id}/fills/refresh`，写入 Redis `cmd.query` 并通过 `ledger-sync` 合并非空 `order_page/fill_page` 到 PostgreSQL。
- [x] 为 `/trade` 接入 Meridian `/v1/metadata/instruments` 和 `/v1/market/snapshots` 薄代理，支持代码前缀补全、非交易日自动取最近交易日快照、行情头和盘口刷新。
- [x] `/trade` 价格显示和下单价格框按 Meridian `instrument_type` 控制精度：股票 2 位，ETF 3 位。
- [x] 9092 后台同步循环在订单事件或成交事件落账后，按账户自动限频发送资金和持仓刷新查询，默认 2 秒合并、20 秒冷却。
- [x] `/trade` 左侧工作区切换完成：移除独立“成交回报”入口，将成交表并入“订单监控”；补全“资金持仓”和“订单监控”完整页面。
- [x] 实现 `GET /v1/events/stream` SSE 实时事件流，支持 `order.changed`、`fill.changed`、`asset.changed`、`positions.changed` 和 heartbeat；`/trade` 通过 EventSource 实时刷新并保留 3 秒轮询兜底，`/api-console` 支持事件流连接预览。
- [x] 初始化 `sdk/python/relay_sdk` Python SDK 包，提供无外部依赖 HTTP 客户端、dataclass 模型、异常封装、SSE 事件迭代器和 mock API 单元测试。
- [x] 新增 `scripts/build-python-sdk.py`，生成 `public/sdk/relay-sdk-0.1.0.tar.gz` 和 `.sha256`，并通过 9092 `/sdk/` 路径提供内网下载。
- [x] 实现 `/v1/status` 依赖健康检查，覆盖 PostgreSQL、Redis、订单服务、Meridian 行情代理、SSE 事件流、自动刷新和账户摘要；错误信息不泄露 DSN、密码或 Redis URL。
- [x] Python SDK 升级到 `0.1.1`，新增 `on_order_status()`、`on_fill()` 后台回调，以及 `watch_order_status()`、`watch_fills()` 阻塞式回调循环，便于策略程序处理订单状态和成交回报。
- [x] 根据 2026-06-14 SDK/接口压测反馈修复下单幂等：同一订单同 payload 返回 `replayed=true` 且不再发布 Redis 命令；同 gateway 不同幂等键或同幂等键不同 payload 返回 409；SDK 升级到 `0.1.2` 暴露 `CommandReceipt.replayed`。
- [x] Python SDK 升级到 `0.1.3`，新增 `status()` 只读探活方法、真实 9092 只读 live smoke 和 SDK 发布检查脚本。
- [x] Python SDK 升级到 `0.1.4`，新增历史订单/成交/持仓查询参数和 `record_job_run()` 任务报告落盘方法。
- [x] Python SDK 升级到 `0.1.5`，新增 `record_settlement_snapshot()`，收盘任务可调用 9092 固化 close 资产/持仓快照和 reconciliation run。
- [x] 根据 2026-06-14 `relay-sdk 0.1.4` 压测反馈，修复已全成订单仍停留在 `accepted` 的状态归一化问题，账本终态不再被后续非终态推送回退；SDK 升级到 `0.1.6`，`record_job_run()` 支持 `completed` 到 `succeeded` 的兼容映射和显式任务字段。
- [x] 明确 ETF 二级市场买卖使用 `business_type=S`；`business_type=E` 为 ETF 申购/赎回专项，当前 `/v1/orders` 标记未实现并返回 `NOT_IMPLEMENTED`。
- [x] 新增 `000003_job_runs` migration 并应用到测试 PostgreSQL，`/v1/status` 已暴露交易阶段和最近盘前/盘后任务状态。
- [x] 新增 `/jobs` 后台任务状态监控页，展示盘前初始化、盘后结算等任务的状态、交易日、开始/完成时间、耗时、错误摘要和 report_json。
- [x] `GET /v1/orders`、`GET /v1/fills` 默认按 `Asia/Shanghai` 当日查询；新增 `/v1/history/orders`、`/v1/history/fills` 和 `/v1/accounts/{account_id}/positions/history` 历史查询口径。
- [x] 账本 API 时间字段统一按 `Asia/Shanghai` 输出，订单、成交、资金、持仓、订单事件、成交事件和任务运行记录的零值时间字段不再展示为 `0001-01-01T00:00:00Z`。
- [x] 新增 `POST /v1/settlements/snapshots`，按指定交易日将当前资金写入 `asset_snapshots(close)`、当前持仓写入 `position_snapshots`，并 upsert `reconciliation_runs` 批次；`post_close_settlement` 已接入该接口。
- [x] 新增盘后对账输入和差异记录第一版：`POST /v1/settlements/snapshots` 会写入 `reconciliation_inputs`、生成 `reconciliation_breaks`，并提供 `GET /v1/reconciliations/breaks` 查询入口。
- [x] 进入 P8 历史数据与盈亏统计，新增 `GET /v1/accounts/{account_id}/performance/daily`，按交易日读取 close 资产快照、上一 close 净资产、持仓快照和成交账本，返回日终权益、日盈亏、收益率、浮动盈亏、成交额和费用汇总；API Console 已提供表单测试入口。
- [x] 新增 `GET /v1/meridian/market/bars` 同源薄代理，保留 Meridian `market_bar.v1` 原始字段，并在 API Console 提供 bars 表单测试入口。
- [x] 新增 `GET /v1/accounts/{account_id}/performance/series`，按区间读取 close 资产快照，返回账户日绩效、累计收益和最大回撤；API Console 已提供表单测试入口。
- [x] 新增 `GET /v1/accounts/{account_id}/performance/series.csv`，导出账户绩效序列 CSV，作为研究侧导出输入第一版。
- [x] `/trade` 新增 `#performance` 绩效分析工作区，直观展示区间净资产/收益/回撤、日终快照、绩效序列表、CSV 下载和 Meridian bars 查询结果。
- [x] `/trade` 取消单独“盘后对账”导航入口，盘后展示收敛到“绩效分析”；底层结算快照、reconciliation inputs/breaks 和 API Console 调试入口保留。
- [x] 订单、成交、当前持仓和历史持仓查询新增服务端 `cursor` 翻页响应 `next_cursor`；`/trade` 的订单监控和资金持仓工作区支持按交易日查询并使用服务端分页。
- [x] 拒绝/失败的下单 reply 会更新本地草稿订单为 `rejected`，并把前置/柜台错误抽取到 `reject_code`、`reject_message` 和 `adapter_context.relay_error_message`；`/trade` 订单监控表和详情时间线显示错误/柜台信息。
- [x] `/trade` 交易测试视图压缩右侧持仓版面，保留资金持仓完整工作区的信息密度。
- [x] `/trade` 交易测试主界面新增本地 ECharts 当日分钟 K 线，使用 Meridian bars 的 `open/high/low/close` 绘制 candlestick 并叠加成交量；若当天不是交易日，bars 请求会通过 Meridian 交易日接口回退到最近交易日。
- [x] `/trade` 分钟 K 线支持当前账户、标的、交易日的买卖点标注：优先使用成交价/成交时间，未成交订单使用委托价/委托时间，并在代码切换、手动刷新和订单/成交 SSE 推送后刷新标注。
- [x] `/trade` 分钟 K 线新增自动刷新：交易测试主界面、页面可见且 K 线日期为东八区当前交易日时，每 30 秒静默刷新 Meridian 1m bars 和买卖点；切到历史日期、隐藏页面或离开交易视图会暂停。
- [x] `/trade` 交易终端所有默认交易日统一为东八区当前日期；当 Meridian snapshot/bars 返回最近交易日时，会自动回填仍处于默认值的资金持仓、订单监控、绩效和 K 线日期输入框。
- [x] 当前交易日行情请求不再回放旧实时缓存：relay 会将当天 snapshot/bars 请求显式限定为 `trade_date=东八区当天`，bars 同时使用 `data_scope=realtime`；只有非交易日才回退到最近交易日 historical。
- [x] Meridian bars 代理新增短 TTL 缓存、同 key 并发请求合并和 stale fallback，降低读压下 `/v1/meridian/market/bars` 与绩效 `benchmark_security_id` 对上游的重复打穿。
- [x] 新增 `GET /v1/meridian/stream/market/snapshots` 同源 SSE 薄代理，保留 Meridian `market_snapshots` 原始事件；`/trade` 当前交易日资金持仓会拉取全量持仓清单并按标的分片订阅 level1 SSE，用 Meridian `last` 实时计算现价、市值和全量浮动盈亏合计，历史日继续展示日终/历史账本字段。
- [x] `/trade` 分页请求层增加非 JSON 响应保护，订单、成交和持仓分页失败时会展示具体请求路径、HTTP 状态和 content-type，并回滚 page/cursor，避免一次失败后分页状态错位。
- [x] `/trade` 资金持仓页补齐测试前置缺失的资金指标展示：股票市值/基金市值按全量持仓和 Meridian `instrument_type` 拆分，其中基金市值只统计 ETF；手续费按资金持仓交易日的全量成交 `fee/adapter_context.fee/nFee` 汇总；当前日若前置未给 `day_profit`，页面用持仓浮盈 + 估算平仓盈亏 - 手续费兜底展示。
- [x] 订单事件/order_page 显示已全成但成交明细缺失时，向前补一条 `relay-summary:<gateway_order_id>` 汇总成交，标记 `adapter_context.relay_synthesized=true`，避免订单账本和成交账本数量口径断裂。
- [x] Python SDK 升级到 `0.1.7`，封装 `get_performance_daily()`、`get_performance_series()`、`get_performance_series_csv()`、`list_reconciliation_breaks()` 和 `get_meridian_bars()`。
- [x] 修复成交账本去重范围：`fill_id/match_stream_id` 按 `account_id + gateway_order_id + fill_id` 处理，不再误丢不同订单复用成交流号的合法成交；Python SDK 升级到 `0.1.8`，成交回调采用同一唯一键。
- [x] 绩效序列增加 Meridian bars 基准对照：`GET /v1/accounts/{account_id}/performance/series` 和 `.csv` 支持 `benchmark_security_id`，输出基准收益、基准回撤和超额收益字段；`/api-console`、`/trade#performance` 和 Python SDK `0.1.9` 已同步。
- [x] P8 第一版完整 PnL 口径已补齐：`realized_pnl=settled_profit`、`gross_pnl=realized_pnl+unrealized_pnl`、`net_pnl=gross_pnl-fee_total`，并保留原始 `settled_profit/unrealized_pnl/fee_total/return_rate` 字段。
- [x] 新增研究侧 PostgreSQL 导出 view：`research_account_daily_performance_v1` 和 `research_order_fill_export_v1`，`000006_research_performance_views` 已应用到测试库。
- [x] 将 Meridian 行情代理默认超时从 5 秒放宽到 15 秒，修复 `688981.SH` 分钟 bars 上游慢响应被 Relay 转成 502 的问题。
- [x] 明确内置模拟柜台暂缓：实盘调试使用券商测试环境，历史数据模拟撮合放在回测引擎。
- [x] 实现正式交易服务模式下的 9092 健康检查接口。

## 待办事项

1. 增加 `/trade` 批量下单测试视图。
2. 增加 Playwright 页面交互冒烟测试。
3. 补充 worker 心跳状态建模、DLQ 告警和正式部署脚本。

## README 状态维护规则

后续每次完成重要工作，都需要同步更新以下内容：

- “线程恢复卡片”的当前状态和最近更新时间。
- “当前进展”的复选框。
- “待办事项”的新增、完成或调整。
- “工作日志”的新增记录。
- 如出现阻塞，记录在“阻塞与风险”。
- 每次项目更新后自动执行一次 Git 提交。

不要在 README 中写入密钥、账号、Token、生产柜台地址或其他敏感信息。

## 阻塞与风险

- 当前无阻塞。
- 业务时间口径已统一为 `Asia/Shanghai`；HTTP envelope、`/healthz`、SSE、Redis command `sent_at`、探测/同步报告和账本 API 展示时间已输出东八区。账本内部 `received_at`、checkpoint 和 PostgreSQL `timestamptz` 仍记录绝对时刻，API 序列化层会省略零值时间字段。
- 每日交易主流程已完成 Python 任务、任务运行报告落盘、收盘后 close 资产/持仓快照落盘、`reconciliation_runs` 批次 upsert、`reconciliation_inputs` 输入摘要、`reconciliation_breaks` 差异记录和账户日终权益/PnL 输入汇总第一版；下一步需要输出更完整的人工复核报告。
- `GET /v1/accounts/{account_id}/performance/daily` 当前依赖日终 close 资产快照；如果未先执行收盘结算快照，会返回 404。第一版 `daily_pnl/return_rate` 以相邻 close 净资产计算，成交已实现盈亏仍需后续结合成本、现金流水和 Meridian bars 精细化。
- `GET /v1/accounts/{account_id}/performance/series` 当前以 close 净资产为主线计算累计收益和回撤，并支持 `benchmark_security_id` 从 Meridian bars 拉取基准 close，输出基准收益、回撤和超额收益。持仓复权估值和更精细交易归因仍需后续版本。
- `GET /v1/accounts/{account_id}/performance/series.csv` 是轻量 CSV 导出；研究侧 PostgreSQL view 已提供 `research_account_daily_performance_v1` 和 `research_order_fill_export_v1`。后续如需大批量离线消费，可再补 Parquet/批量文件任务。
- P8 账表计算只接入 Meridian `bars`。交易端暂不接入实时 level2 数据，也不规划 `trades/orders/order-queues`，避免把不存在或非必要的数据源纳入核心路径。
- `GET /v1/meridian/market/bars` 当前是同源薄代理，不做字段映射和持久化；当 `trade_date` 为空或等于东八区当天时，会先调用 Meridian 交易日接口取得 `previous_or_current_trading_date`，交易日当天使用 `data_scope=realtime`，非交易日自动回退到最近交易日 historical。bars 代理对标准化后同 key 请求做 2 秒短缓存、singleflight 合并和 60 秒 stale fallback，降低读压下直接 bars 查询和绩效 benchmark 对上游的重复冲击。
- `/trade` 分钟 K 线图只用于手工测试与点位理解；买卖点来自本地订单/成交账本，不新增行情字段定义。成交点优先于委托点，同一订单已有成交时不会重复绘制委托价。`/trade#performance` 后续改为净值曲线、收益贡献和交易归因等绩效图。
- 9092 当前线上仍运行文档门户模式；真实交易 API 需要以 `service.mode=api` 和本地凭据配置启动。
- 9092 文档门户模式已同源挂载 `/v1/*` API handler；`/v1/status`、`/v1/schema` 等基础接口可直接从 `/api-console` 发送请求。若启动时未加载数据库和 Redis 本地配置，交易写接口和账本查询会返回明确的服务不可用或空结果。
- `/healthz` 只表示 9092 进程存活；`/v1/status` 才包含 PostgreSQL、Redis、订单服务、行情代理、事件流和自动刷新状态。健康检查只返回 `ok/error/timeout/not_configured` 等摘要，不返回 DSN、密码、Token 或 Redis URL。
- 当前刷新 API 只负责写入前置 `cmd.query`；9092 轻量后台同步循环和 worker 都可消费测试 Redis `reply/event` 并合并到本地账表。正式生产化建议用 `service.mode=worker` 承接持续同步，用 9092 API 进程专注服务请求。
- 下单幂等当前在应用层基于 PostgreSQL 账本预检实现：先查 `account_id + gateway_order_id`，再查 `account_id + idempotency_key`，相同 payload 返回 `replayed=true`，冲突请求不发布 Redis 命令。数据库唯一索引级别的 `idempotency_key` 约束待后续 migration 清理历史重复数据后补充。
- 2026-06-14 压测暴露测试前置会在不同订单间复用 `fill_id/match_stream_id`；relay 已通过 `000005_fill_id_order_scope` 把成交唯一键调整为 `account_id + gateway_order_id + fill_id`。Redis Stream 协议仍要求同一订单内成交编号稳定，并建议前置 `fill.event` 始终携带 `gateway_order_id`、`order_id` 和 `order_stream_id`。
- 同轮排查过程中曾观察到原始 `adapter_context.order_status_name=unAccept` 与标准订单 `filled` 不一致；回放完整事件后目标订单 raw 状态已更新为 `dealt`。若后续再次出现 raw status 与标准状态冲突，Relay 继续以标准字段和数量口径保证账本终态，并建议前置侧澄清原始状态字段语义。
- 已通过内网文档临时读取 PostgreSQL 连接信息，并在专用 `relay_trader` 数据库执行首版 migration；真实 DSN 不写入仓库。
- 前置测试环境已启动，已使用真实 Redis 跑通 `reply/event` 到 `raw_stream_messages` 的小批量归档。
- 当前观察到的历史 `order.event.payload` 缺少 `trade_side` 和 `business_type`；relay 下单 API 已通过先写订单草稿解决新订单事件回流更新问题，但历史无草稿事件仍只能归档 raw。
- API 下单只会在账户配置 `enabled=true` 且 `trading_enabled=true` 时发送 Redis 命令。
- 联调凭据只放本地配置或安全渠道，不写入仓库。
- 测试/生产 Redis 不通过修改同一个配置文件来回切换；推荐使用未跟踪 `config/relay.test.yaml` 与 `config/relay.prod.yaml`，并通过 `RELAY_CONFIG_PATH` 选择。生产配置上线前必须先保持 `trading_enabled=false` 完成只读探测和 `/v1/status`、`/trade` 环境标识核对。
- 2026-06-15 当前生产配置已加入账户 `501000114077`，保持 `trading_enabled=false` 且 `auto_refresh=false`；手动刷新接口可发布 `cmd.query`，订单/成交推送由 Redis `reply/event` 同步到账本和 SSE，交易写接口仍由账户交易权限拦截。
- 账户别名只用于展示，不参与 Redis Stream 前缀、账本主键、下单幂等或权限判断；真实路由仍以 `account_id/broker_id/gateway_id/stream_prefix` 为准。配置 `accounts[].alias` 是默认值，交易终端修改后的别名写入 PostgreSQL `accounts.account_name` 并优先展示。
- SDK 不参与测试/生产环境选择；SDK 只连接 `base_url`，实际后端是测试 Redis 还是生产 Redis 完全由 relay 服务端配置、账户路由和交易权限决定。
- 当前测试/生产查询阶段仍使用同一 PostgreSQL DSN，账表主要按 `account_id` 区分。由于核心表尚未普遍把 `environment` 纳入唯一键，长期生产化建议改为生产/测试独立 DSN 或 schema；如果必须同库，需要补 migration 将 `environment` 纳入 accounts、orders、fills、asset/position snapshots 等核心账表约束。
- 接口测试台当前可在 9092 文档门户同源发送 `/v1/*` 请求；资金、持仓、单笔下单、批量下单、撤单、订单查询、成交查询和前置刷新接口需要启动时加载本地 PostgreSQL、测试 Redis 和账户路由配置。
- 资金/持仓/订单/成交查询默认读取 PostgreSQL 本地账表；可通过刷新接口主动发前置 `cmd.query`，由 9092 轻量后台同步循环或正式 worker 合并 reply 到本地账表。
- `order_page/fill_page` 合并路径已实现并通过单元测试覆盖；测试前置返回非空查询页后，可继续补一组真实样例归档和回放记录。
- 订单/成交事件会触发服务端自动刷新资金和持仓，但会按账户合并并限频，默认 `auto_refresh.debounce_seconds=2`、`auto_refresh.cooldown_seconds=20`；这能让 `/trade` 持仓跟随成交更新，同时避免每条订单推送都查询柜台。
- 测试下单参考价不要硬编码；`/trade` 当前通过 relay 的 Meridian 薄代理读取 `/v1/market/snapshots` 和 level1 snapshot SSE，如果当天不是交易日会调用 Meridian `/v1/metadata/trading-day` 获取最近交易日后读取 historical 快照。当前交易日持仓现价、市值和浮动盈亏由 Meridian level1 SSE 推送更新，不通过前端轮询行情计算。
- 资金持仓汇总区的实时浮动盈亏按全量持仓清单求和，不按当前表格分页估算；页面表格仍保留服务端分页，切换账户、日期或收到持仓/成交 SSE 后会刷新本地全量持仓清单，再重新订阅 Meridian level1 SSE。
- 当前测试前置 `asset` 快照未提供 `stock_value/fund_value/day_profit/commission/position_profit`，页面已做展示兜底。股票/ETF 市值拆分只使用 Meridian 的 `instrument_type`；若首批行情尚未返回，拆分字段暂显 `--`，避免把未知类型错分到股票或基金。后续前置补齐标准资金字段后，页面会优先使用前置字段。
- 行情和证券主数据字段口径全部以 Meridian 为准；relay 不新增行情标准字段。如需要更多补全能力，应推动 Meridian 增加或完善接口。
- Meridian `688981.SH` 1m bars 在 2026-06-14 现场验证可直接返回，但响应耗时约 6 秒，超过 Relay 旧默认 5 秒超时；默认超时已调至 15 秒并验证通过。若后续单只标的仍偶发超时，应先检查 Meridian 上游耗时，再评估是否做页面级重试或异步加载。
- 行情价格精度按 Meridian `instrument_type` 解释：`stock` 保留 2 位，`etf` 保留 3 位；账本订单/成交/持仓若缺少标的类型，则先尝试使用当前快照或已缓存证券主数据匹配，仍无法识别时默认股票 2 位。
- Python SDK 当前可用 `PYTHONPATH=sdk/python`、`python -m pip install -e sdk/python` 或 `python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.9.tar.gz"` 安装；安装包由 `scripts/build-python-sdk.py` 生成并提交到 `public/sdk/`。
- 历史持仓查询读取 `position_snapshots`；收盘任务现在会通过 `/v1/settlements/snapshots` 写入日终持仓快照，非交易日补跑时也会按 Meridian 回退后的目标交易日写入。
- worker 模式当前会从 `stream_checkpoints` 恢复每条 Redis output stream 的 `last_stream_id`；如果 checkpoint 表为空，则按配置的起始位点从 `0` 追赶历史，重复消息依赖账表唯一约束保持幂等。

## 工作日志

- `2026-06-13`: relay 初始化项目目录，创建 README，记录端口 `9092`、职责范围、目录结构、恢复方式和初始待办。
- `2026-06-13`: 添加 `.gitkeep` 占位文件，保证空目录在后续提交中保留。
- `2026-06-13`: 根据用户补充信息，形成 Go + Python 技术栈和职责边界；明确 relay 聚焦多账户标准交易 API、盘后对账、历史数据和账户盈亏统计，前置层负责券商协议统一。
- `2026-06-13`: 按用户要求优先实现 9092 文档门户模式，支持 Web 查看 README、架构文档、前置对接手册、测试目录和项目结构；未触碰交易核心逻辑。
- `2026-06-13`: 确认域名映射完成，将 `http://relay-trader.quantstage.com` 记录为最终服务口径。
- `2026-06-13`: 新增 `docs/ROADMAP.md`，并在文档门户首页展示整体开发步骤和阶段进度。
- `2026-06-13`: 新增 `docs/DATA_MODEL.md`，记录 PostgreSQL 落盘口径、C++ 结构体参考源、标准字段映射和首批账表建议。
- `2026-06-13`: 新增 `docs/OPERATIONS.md` 和 `config/relay.example.yaml`，记录本地配置文件、凭据管理和 cron 后台任务口径。
- `2026-06-13`: 新增 `docs/PYTHON_SDK.md`，规划面向策略开发的 9092 API Python SDK。
- `2026-06-13`: 参考 Meridian SDK 发布方式，规划 relay SDK 通过 `http://relay-trader.quantstage.com/sdk/relay-sdk-<version>.tar.gz` 供内网 pip 安装。
- `2026-06-13`: 根据用户反馈，首页不再直接展开开发路线图，仅保留 `/docs/roadmap` 入口。
- `2026-06-13`: 推进 P1 工程化底座，新增 `internal/config`，实现 YAML 配置加载、`docs/api/worker` 模式校验和账户路由重复检查，并让文档门户可选读取 `RELAY_CONFIG_PATH`。
- `2026-06-13`: 继续推进 P1，新增结构化日志、HTTP request_id、统一 JSON envelope、API 服务骨架和 worker 常驻进程骨架；API 模式已提供健康检查、服务状态和配置态账户列表。
- `2026-06-13`: 进入 P2，新增 `internal/trading` 和 `docs/TRADING_API_SCHEMA.md`，定义第一版统一交易接口 schema、枚举、基础校验、状态机语义和 Redis Stream 映射；记录前置测试环境可用于后续联调。
- `2026-06-13`: 进入 P4 前置对接准备，新增 `cmd/relayctl redis-probe`、`internal/redisstream` 和 `docs/REDIS_STREAM_PROBE.md`，实现 Redis Stream 只读探测边界；当前环境未配置 Redis 凭据，尚未现场读取真实 stream。
- `2026-06-13`: 根据用户要求新增 `/api-console` 接口测试台，采用 Apifox 风格三栏布局：接口集合、请求编辑和响应查看；早期未接入 handler 的交易写接口曾禁用发送，当前已随正式接口开放。
- `2026-06-13`: 推进 P5 交易账表持久化，新增 `migrations/postgres/000001_init_ledger.*.sql` 和 `docs/MIGRATIONS.md`，覆盖账户、网关、订单、订单事件、成交、原始 stream、持仓、资产、资金流水和盘后对账表。
- `2026-06-13`: 安装 PostgreSQL client，新增 `internal/db/migrations` 和 `relayctl migrate status/up/down`，支持通过 `RELAY_DATABASE_URL`、`-database-url` 或配置文件执行 migration。
- `2026-06-13`: 根据用户确认可直连数据，创建专用 PostgreSQL 数据库 `relay_trader`，执行 `relayctl migrate status/up/status`，确认 `000001_init_ledger` 已应用并生成 15 张业务表。
- `2026-06-13`: 新增 `internal/ledger` 账本 repository 骨架、单元测试和可选 PostgreSQL 集成测试，覆盖账户、订单、订单事件、成交和原始 Redis Stream 消息落盘入口；已在真实 `relay_trader` 库验证写入通过。
- `2026-06-13`: 新增 `relayctl ledger-sync` 和 `docs/REDIS_LEDGER_SYNC.md`，支持从 Redis `reply/event` 批量归档到 PostgreSQL；真实联调已归档 10 条 reply 和 10 条 event，并记录 `order.event` 缺少 `trade_side/business_type` 的字段缺口。
- `2026-06-13`: 新增 Redis command publisher、`internal/orderflow` 和 API 模式 `POST /v1/orders`，实现订单草稿落盘、Redis `cmd.trade` 写入和命令 raw 归档；使用测试 Redis 完成一笔单笔下单闭环，订单最终更新为 `filled`，并落盘 3 条订单事件、1 条成交和 6 条原始消息。
- `2026-06-13`: 新增 API 模式 `POST /v1/orders/{gateway_order_id}/cancel`、`GET /v1/orders` 和 `GET /v1/fills`；撤单会先校验本地订单非终态再写入 Redis `order.cancel`，查询接口从 PostgreSQL 账本读取，接口测试台同步开放对应 API-mode 模板。
- `2026-06-13`: 根据测试环境行情说明，确认后续测试下单先从 Meridian 拉 `2026-06-12` 行情作为参考价；当前日线接口返回空，分钟线可用，示例 `600000.SH` `15:00` 1m close 为 `9.67`。
- `2026-06-13`: 新增 API 模式 `GET /v1/accounts/{account_id}/asset`、`GET /v1/accounts/{account_id}/positions` 和 `POST /v1/orders/batch`；资金读取最新 `asset_snapshots`，持仓读取 `positions` 当前表，批量下单写入多笔订单草稿并发布 Redis `order.batch.submit`。
- `2026-06-13`: 修复 `/api-console` 无法发送请求查看返回的问题：文档门户模式现在同源挂载 `/v1/*` API handler，基础发现接口可直接返回，交易和账本接口按本地配置状态启用。
- `2026-06-13`: 新增资金/持仓前置刷新 API：`POST /v1/accounts/{account_id}/asset/refresh` 和 `/positions/refresh` 写入 Redis `cmd.query`，`ledger-sync` 支持把 `asset_page/position_page` reply 写入 `asset_snapshots/positions`。
- `2026-06-13`: 为 9092 生成未跟踪本地配置 `config/relay.local.yaml` 并用测试资源重启服务，公网验证 `/v1/accounts/00030484/asset`、`/positions`、`/orders`、`/fills` 均可返回数据。
- `2026-06-14`: 参考 Meridian `/api-tests/level2/level2-snapshots` 页面优化 `/api-console`，将接口参数改为表单填写，发送后展示 HTTP 状态、耗时、JSON 和可表格化响应。
- `2026-06-14`: 将 `/api-console` 从 Go 内联 HTML/JS/CSS 重构为模板和静态资源：Go 仅负责 `embed`、`/assets/` 静态路由和模板渲染，接口字段清单迁移到 JSON catalog。
- `2026-06-14`: 根据 Stitch 设计稿方向新增 `/trade` 手动交易测试终端，采用全屏交易软件式布局，并接入现有账户、资金、持仓、订单、成交、下单、撤单和刷新 API；当前订单状态刷新用 3 秒轮询模拟持续推新。
- `2026-06-14`: 精简 `/trade` 顶部右侧未落地功能按钮，仅保留 RT 身份块；将操作反馈从右侧详情栏固定区域改为右下角悬浮弹出框。
- `2026-06-14`: 根据前置程序订单状态语义，确认 `order.event` 状态变化应按整单快照 upsert；`/trade` 当日委托表改为展示本地 `ReqID/client_order_id`、柜台 `order_id` 和交易所 `order_stream_id`。
- `2026-06-14`: 补充 `/trade` 成交回报的订单关联展示：成交表和订单详情执行明细展示 `fill_id`、关联订单 `ReqID`、柜台 `order_id` 和交易所 `order_stream_id`。
- `2026-06-14`: 修复 `/trade` 当日委托状态不刷新的根因：9092 现在启动轻量后台 Redis `reply/event` 同步循环，从测试 Redis 追赶并持续写入 PostgreSQL，页面轮询 `/v1/orders` 后可看到 `filled/cancelled/rejected` 等终态。
- `2026-06-14`: 为 `/trade` 接入 Meridian 行情：新增 `/v1/meridian/metadata/instruments` 和 `/v1/meridian/market/snapshots` 薄代理，保留 Meridian 原始 `data/meta/error` 字段；前端代码输入通过证券主数据补全，行情头、五档盘口和默认委托价会随当前代码刷新。
- `2026-06-14`: 按用户补充的行情精度规则优化 `/trade`：股票价格保留 2 位，ETF 价格保留 3 位；精度判断只使用 Meridian `instrument_type`，并同步影响行情头、五档盘口、委托/成交价格、持仓成本/现价和下单价格框步长。
- `2026-06-14`: 增加订单/成交事件驱动的自动资金持仓刷新：`ledger-sync-loop` 处理到 `order.event` 或 `fill.event` 后按账户调度 `account.asset.query` 和 `account.positions.query`，默认 2 秒 debounce、20 秒 cooldown，查询 reply 继续合并到 PostgreSQL 后由 `/trade` 轮询展示。
- `2026-06-14`: 优化 `/trade` 左侧导航和页面布局：移除独立“成交回报”入口，成交回报作为“订单监控”的 tab 展示；“订单监控”扩展为完整工作区并保留委托详情栏，“资金持仓”扩展为完整工作区并展示资金拆分和持仓表。
- `2026-06-14`: 新增 `GET /v1/events/stream` SSE 实时事件流和内部事件 hub，9092 轻量 Redis 同步循环在订单、成交、资金、持仓落账后广播 `order.changed/fill.changed/asset.changed/positions.changed`；`/trade` 接入 EventSource 实时刷新并保留轮询兜底，`/api-console` 支持事件流连接预览。
- `2026-06-14`: 新增订单/成交前置查询刷新：`POST /v1/accounts/{account_id}/orders/refresh` 和 `/fills/refresh` 写入 `order.list.query/fill.list.query`，`ledger-sync` 支持将非空 `order_page/fill_page` 合并到账本；`/trade` 和 `/api-console` 均已增加手动刷新入口。
- `2026-06-14`: 初始化 Python SDK 首版源码包 `sdk/python/relay_sdk`，实现标准库 HTTP 客户端、账户/资金/持仓/订单/成交查询、资金/持仓/订单/成交刷新、单笔/批量下单、撤单、等待订单终态、SSE 事件迭代、异常映射和 mock 9092 API 单元测试。
- `2026-06-14`: 发布 Python SDK 内网安装包入口：新增 `scripts/build-python-sdk.py`、`public/sdk/relay-sdk-0.1.0.tar.gz` 和 `.sha256`，9092 文档门户通过 `/sdk/` 直接提供下载，已验证本地 tar.gz 可被 pip 安装。
- `2026-06-14`: 完成 `/v1/status` 依赖健康检查：服务状态现在包含账户摘要和 PostgreSQL、Redis、订单服务、Meridian 行情代理、SSE 事件流、自动刷新状态；数据库/Redis ping 失败只返回通用错误摘要，避免泄露本地连接凭据。
- `2026-06-14`: 根据策略开发便利性反馈升级 Python SDK 到 `0.1.1`，新增订单状态和成交回报回调封装；SDK 收到 SSE 变化事件后自动查询本地账本并去重触发回调，安装包发布为 `public/sdk/relay-sdk-0.1.1.tar.gz`。
- `2026-06-14`: 根据 11:03-11:16 Asia/Shanghai relay 交易接口功能与压力测试反馈，修复重复 `gateway_order_id/idempotency_key` 会重复发布和覆盖终态订单的问题；补充 ETF 二级市场/申赎语义说明，`business_type=E` 在当前普通下单接口返回 `NOT_IMPLEMENTED`；发布 `relay-sdk 0.1.2` 支持 `CommandReceipt.replayed`。
- `2026-06-14`: 推进正式 worker：新增 `000002_stream_checkpoints` migration 并应用到测试 PostgreSQL，账本 repository 支持读写 stream 位点；`worker` 模式现在可持续消费 `reply/event/hb/dlq`，用 PostgreSQL checkpoint 恢复 Redis Stream 进度，并沿用订单/成交事件触发的资金持仓自动刷新。
- `2026-06-14`: 补齐 SDK 发布质量门：`relay-sdk 0.1.3` 新增 `status()` 只读探活方法，新增 `tests/integration/sdk_live_smoke.py` 对真实 9092 做只读集成测试，新增 `scripts/check-python-sdk-release.py` 校验版本一致性、tar.gz 内容、sha256、单元测试和可选 live smoke。
- `2026-06-14`: 固化全系统业务时间统一 `Asia/Shanghai`，新增 `docs/TRADING_DAY_WORKFLOW.md` 规划每日 `pre_open_init` 盘前初始化和 `post_close_settlement` 收盘后结算；配置模板新增 `service.timezone` 和两个交易日任务，`/v1/status` 暴露 `timezone`。
- `2026-06-14`: 新增 `internal/timeutil` 统一东八区时间工具，HTTP envelope、`/healthz`、SSE 事件、Redis command `sent_at`、Redis 探测报告和账本同步报告生成时间改为 `Asia/Shanghai` 输出。
- `2026-06-14`: 首页顶部导航新增 `SDK` 页面入口，指向 `/docs/python-sdk`，方便策略开发直接查看 SDK 文档。
- `2026-06-14`: 新增 Python 日流程任务骨架：`src/relay/jobs/pre_open_init.py` 和 `post_close_settlement.py`，复用 relay SDK 执行 `/v1/status` 检查、Meridian 交易日判断、资金/持仓/订单/成交刷新、账本快照摘要和 JSON 报告输出；新增单元测试覆盖交易日跳过、刷新顺序和未终态订单统计。
- `2026-06-14`: 更新开发路线图，将 P7 标记为进行中；下一步任务明确为交易日任务运行状态落盘，并在 `/v1/status` 暴露最近盘前/盘后任务状态。
- `2026-06-14`: 新增 `000003_job_runs` migration、`POST/GET /v1/jobs/runs` 和 `/v1/status.job_runs`；盘前/盘后 Python 任务支持 `--persist`，已用 2026-06-14 非交易日 dry-run 写入两条 `skipped` 状态记录。
- `2026-06-14`: 查询口径调整为订单/成交默认按东八区当日查询；新增 `/v1/history/orders`、`/v1/history/fills` 和 `/v1/accounts/{account_id}/positions/history`，SDK 发布 `relay-sdk 0.1.4` 支持历史查询和任务报告落盘。
- `2026-06-14`: 清理账本 API 零值时间展示：订单、成交、资金、持仓、订单事件、成交事件和 `job_runs` 响应按东八区格式化非空时间，并省略零值字段，避免策略端和页面看到 `0001-01-01T00:00:00Z`。
- `2026-06-14`: 新增 `POST /v1/settlements/snapshots` 和 SDK `record_settlement_snapshot()`；`post_close_settlement` 会在刷新/读取账户摘要后写入 close 资产快照、持仓快照和 `reconciliation_runs` 批次，SDK 发布 `relay-sdk 0.1.5`。
- `2026-06-14`: 根据 `tmp/relay_sdk_014_feedback_20260614.md` 反馈，订单标准状态会按成交数量归一化：`cum_filled_qty >= order_qty` 且 `leaves_qty=0` 时统一进入 `filled/filled/is_terminal=true`；账本冲突更新和状态更新新增终态保护，避免已撤/已成/已拒订单被后续 `created/accepted/working` 推送回退；SDK 发布 `relay-sdk 0.1.6`，`record_job_run()` 增加 `target_trade_date`、`timezone`、`duration_ms` 显式参数并兼容 `status="completed"`。
- `2026-06-14`: 推进 N3 盘后对账输入与差异记录：新增 `000004_reconciliation_idempotency` migration，为 `reconciliation_inputs` 和 `reconciliation_breaks` 增加幂等唯一索引；结算快照接口会写入 relay 账本摘要、PnL 输入摘要、Redis raw 窗口摘要和柜台查询摘要，并生成未终态订单、订单成交数量不一致、快照缺失/刷新失败 break；新增 `GET /v1/reconciliations/breaks` 和 API Console 查询入口。
- `2026-06-14`: 更新开发路线图并进入 P8，新增 `GET /v1/accounts/{account_id}/performance/daily` 日终权益/PnL 输入汇总接口和 API Console 表单入口；第一版基于 close 资产快照、上一 close 净资产、日终持仓快照和成交账本计算日盈亏、收益率、持仓汇总、成交额和费用。
- `2026-06-14`: 根据用户确认收窄 P8 市场数据范围：账表计算只接入 Meridian `bars`，暂不接入实时 level2，也不规划 `trades/orders/order-queues`。
- `2026-06-14`: 接入 Meridian `bars` 同源薄代理：新增 `/v1/meridian/market/bars`、API Console 表单入口和单元测试，保留 Meridian `market_bar.v1` 原始 `data/meta/error` 结构。
- `2026-06-14`: 新增账户绩效序列接口 `/v1/accounts/{account_id}/performance/series`，基于 close 资产快照计算日收益、累计收益和最大回撤，并更新 API Console 和路线图。
- `2026-06-14`: 新增账户绩效序列 CSV 导出 `/v1/accounts/{account_id}/performance/series.csv`，复用同一 close 净值序列口径，作为研究侧导出输入第一版。
- `2026-06-14`: 完善 `/trade` 交易终端展示，把 P8 新增能力放入 `#performance` 绩效分析工作区；页面可按账户和日期查询 close 净值绩效序列、查看日终快照摘要、下载 CSV，并通过同源 Meridian bars 入口检查行情基准数据。
- `2026-06-14`: 根据页面口径调整，取消 `/trade` 左侧“盘后对账”入口，避免与“绩效分析”形成重复页面；盘后差异和结算快照仍作为底层 API、任务和 API Console 调试能力保留。
- `2026-06-14`: 为订单、成交、当前持仓和历史持仓查询补充服务端 cursor 分页，响应包含 `next_cursor`；`/trade` 订单监控和资金持仓页面新增交易日查询框、查询按钮、上一页/下一页分页控件，默认按东八区当日显示。
- `2026-06-14`: 修复拒单排错信息链路：同步层不再丢弃 `rejected/failed` 的下单 reply，而是把前置 payload、顶层 code/message 和 `adapter_context` 中的柜台错误合并回订单账本；`/trade` 订单监控新增“错误/柜台信息”列，订单详情时间线和 raw JSON 可直接排查拒单原因。
- `2026-06-14`: 优化 `/trade` 交易测试页面右侧持仓信息密度，并在 `/trade#performance` 左侧新增本地 ECharts 分钟线图；bars 请求对当天/空日期通过 Meridian 交易日接口回退到最近交易日。
- `2026-06-14`: `/trade#performance` 分钟线新增买卖点标注：按当前账户、标的和交易日分页读取历史订单/成交，成交点优先，未成交订单用委托点；订单/成交 SSE 推送会触发标注刷新。
- `2026-06-14`: 将分钟行情图迁移到 `/trade` 交易测试主界面并改为 ECharts candlestick K 线加成交量；图表跟随证券代码、交易日、手动刷新和订单/成交 SSE 推送更新，绩效页后续改为净值和收益贡献类图表。
- `2026-06-14`: 调整 `/trade` 交易测试主界面三列布局，将右侧资产持仓列从 240px 放宽到 320px，中间 K 线让出空间以提升持仓表可读性。
- `2026-06-14`: 继续放宽 `/trade` 交易测试主界面右侧资产持仓列到 440px，并为交易终端 CSS 增加版本参数，避免浏览器或代理缓存旧列宽。
- `2026-06-14`: 按页面反馈继续将 `/trade` 右侧资产持仓列从 440px 放宽到 540px，并同步提高交易终端最小桌面宽度。
- `2026-06-14`: 按页面反馈继续将 `/trade` 右侧资产持仓列从 540px 放宽到 590px。
- `2026-06-14`: 根据 `tmp/relay_sdk_016_round2_feedback_20260614.md` 反馈，新增订单累计成交量到成交账本的汇总补齐逻辑，后续若前置只给订单全成累计量但未给完整成交明细，会生成 `relay-summary:<gateway_order_id>` 标记成交，保证策略侧成交回调和账表汇总可用。
- `2026-06-14`: SDK 发布 `relay-sdk 0.1.7`，补齐 P8 新增 HTTP 能力封装，并把 API Console Meridian bars 示例改为 `trade_date + 1m + 09:30-15:00 + limit=300`。
- `2026-06-14`: 根据 `tmp/relay_sdk_017_feedback_20260614.md` 反馈定位成交缺失根因：测试前置已发送 `fill.event`，但不同订单间复用 `fill_id/match_stream_id`，旧账本唯一键 `account_id + fill_id` 误丢合法成交；新增 `000005_fill_id_order_scope` migration，将成交唯一键改为 `account_id + gateway_order_id + fill_id`，并发布 `relay-sdk 0.1.8` 让成交回调采用同一去重口径。
- `2026-06-14`: 排查 `688981.SH` Meridian bars 502：Relay 旧默认 5 秒超时先失败，直接 Meridian 约 6 秒返回 200；将 market 默认超时和示例配置调至 15 秒，重启 9092 后本地 `/v1/meridian/market/bars?security_id=688981.SH&trade_date=20260612...` 返回 200。
- `2026-06-14`: 用户侧复测确认 `relay-sdk 0.1.8` 安装包 SHA256 校验通过、包内单测 14/14 通过；新并发写压 30 笔中 18 笔成交，订单/成交一致性 18/18 通过，未再复现 `filled` 但 `fills` 缺失的问题。
- `2026-06-14`: 根据路线图推进 9092 页面轻量冒烟测试，新增 `tests/integration/page_smoke.py`；本机 `http://127.0.0.1:9092` 已通过 18 个检查点，覆盖首页、README 文档、测试索引、API Console、交易终端、任务状态、静态资源、`/healthz`、`/v1/status` 和 `relay-sdk 0.1.9` 下载入口。
- `2026-06-14`: 新增 `/jobs` 后台任务状态监控页，参考 Meridian `realtime-status` 的任务状态面板思路，展示盘前初始化、盘后结算等 `job_runs` 的状态、交易日、开始/完成时间、耗时、错误摘要和完整 report JSON；交易终端侧边栏和首页已增加入口。
- `2026-06-14`: 完善 P8 绩效研究导出输入：账户绩效序列和 CSV 增加 `benchmark_security_id`，通过 Meridian `bars` 的 14:55-15:00 分钟 close 生成基准收益、基准回撤和超额收益字段；API Console、`/trade#performance` 和 `relay-sdk 0.1.9` 已同步，下一步继续补研究侧 PostgreSQL 导出 view 和更完整已实现/浮动盈亏口径。
- `2026-06-14`: 收尾 P8：`DailyPerformance` 和 CSV 增加 `realized_pnl/gross_pnl/net_pnl` 第一版研究侧口径；新增并应用 `000006_research_performance_views`，提供 `research_account_daily_performance_v1` 与 `research_order_fill_export_v1` 两个 PostgreSQL 导出 view。测试库验证 view 可读：日绩效 view 1 行，订单成交导出 view 199 行。路线图中 P8 已切换为 done。
- `2026-06-14`: 根据用户确认重新收敛 relay 边界：P9 内置模拟柜台暂缓，实盘联调用券商测试环境，基于历史行情的模拟撮合放回测引擎；relay 继续聚焦标准交易 API、账本落盘、事件流、对账、绩效和研究导出。
- `2026-06-14`: 梳理整体文档，删除 `data/simdesk` 和 `src/relay/simdesk` 过时占位，压缩 README 恢复卡片；补全 Redis Stream 命名、command envelope、reply/event 合并、checkpoint、订单编号唯一性、下单幂等、成交订单作用域去重和终态保护说明。
- `2026-06-15`: 统一 `/trade` 交易终端默认日期：前端以 `Asia/Shanghai` 当前日期初始化，Meridian snapshot/bars 如果回退到最近交易日，会只回填仍处于自动默认值的日期输入框；模板中移除固定样例日期占位符。
- `2026-06-15`: 排查交易终端仍显示 `20260612` 行情：Meridian 交易日接口已确认 `20260615` 是交易日，但实时 snapshot/bars 不带当天过滤时会返回旧 Redis journal 的 `20260612` 数据；relay 已调整为交易日当天显式请求 `trade_date=当天`，bars 默认补 `data_scope=realtime`，避免交易终端误用旧行情。
- `2026-06-15`: 跟进 `relay_sdk_019_trading_day_pressure` 报告：买卖双向交易主链路稳定，瓶颈集中在并发 bars/benchmark；relay 为 Meridian bars 代理增加 2 秒新鲜缓存、同 key singleflight 合并和 60 秒 stale fallback，并补并发合并与上游 5xx 回退单元测试。
- `2026-06-15`: 新增 Meridian level1 snapshot SSE 同源代理 `/v1/meridian/stream/market/snapshots`，`/trade` 当前交易日资金持仓按全量持仓分片订阅行情流，实时刷新持仓现价、市值和全账户浮动盈亏合计；历史日期不使用实时行情重估。
- `2026-06-15`: 排查 `/trade` 订单监控分页 `JSON Parse error: Unrecognized token '<'`：服务端订单/成交/持仓分页当前均返回 JSON，前端请求层已补非 JSON 响应诊断和分页失败状态回滚，后续若再遇到 HTML 错误页可直接看到具体 URL 与响应类型。
- `2026-06-15`: 补齐 `/trade` 资金持仓页股票市值、ETF 基金市值、手续费、持仓盈亏和平仓/当日盈亏展示兜底；这些指标在测试前置资金快照缺字段时由全量持仓、Meridian level1 SSE 和当日全量成交账本计算。
- `2026-06-15`: `/trade` 交易测试主界面分钟 K 线接入 30 秒自动刷新，使用现有 Meridian bars 薄代理；自动刷新只在当前交易日、交易视图和页面可见时运行，避免历史图和后台标签页持续打上游。
- `2026-06-15`: 设计并落地测试/生产环境切换护栏：新增 `service.environment`、`relay.test/prod.example.yaml` 模板、配置一致性校验、`/v1/status.environment` 和 `/trade` 环境 badge；生产 Redis 凭据不写入仓库，当前 9092 未切换到生产。
- `2026-06-15`: 按用户给出的生产 Redis 凭据切入生产观察模式：生成未跟踪 `config/relay.prod.yaml`，账户路由为空、自动刷新关闭、下单权限未开放；只读扫描生产 Redis stream，识别前缀 `relay:prod:v1:huaxin:501000114077`，`cmd.trade/cmd.query` 为空且 `hb` 心跳正常。
- `2026-06-15`: 按用户补充权限口径，将生产账户 `501000114077` 加入未跟踪 `config/relay.prod.yaml`，开启账户查询和订单/成交订阅，保持 `trading_enabled=false` 且 `auto_refresh=false`；交易终端环境 badge 改为服务端按当前环境直出。
- `2026-06-15`: 明确 SDK 与环境切换解耦：SDK 只面向同一个 HTTP/SSE base URL，测试/生产由 relay 服务端配置控制；首页新增运行环境控制台，并记录当前数据库仍是同库按账户隔离，后续需做独立 DSN/schema 或环境维度 migration。
- `2026-06-15`: 为多账户可读性新增账户别名：配置 `accounts[].alias`、`/v1/accounts.alias` 和 `/trade` 账户展示均已支持；当前生产账户 `501000114077` 配置默认别名为 `生产查询账户`。
- `2026-06-15`: 账户别名从浏览器临时状态改为服务端持久化，新增 `PATCH /v1/accounts/{account_id}/alias`，写入 PostgreSQL `accounts.account_name`；`GET /v1/accounts` 会用落库别名覆盖配置默认值。
- `2026-06-15`: 生产 Redis 发现第二账户 `314000046830`，新增只读 `relayctl redis-scan` 用于扫描 `relay:<env>:v1:*:*` stream 前缀；未跟踪生产配置已加入该账户，保持 `trading_enabled=false`，9092 已重启并显示 2 个生产账户。
- `2026-06-15`: `/trade` 持仓、委托和成交表格新增独立“证券名称”列，前端通过 Meridian `metadata/instruments?security_ids=...` 批量补齐名称和 `instrument_type`，继续遵循 Meridian 字段标准，不在 relay 自建证券主数据。
- `2026-06-15`: 优化 `/trade` 右下角 toast，从常驻提示改为新消息短暂弹出后自动隐藏，避免遮挡订单监控和资金持仓分页按钮。
- `2026-06-15`: 按用户授权开放生产环境两个账户的订单查询和下单权限：未跟踪 `config/relay.prod.yaml` 中账户级 `trading_enabled=true`，9092 已重启；`/v1/status` 显示 production、enabled 2、trading_enabled 2、simulated 0。
- `2026-06-15`: 排查生产账户 `314000046830` 当日成交为空：relay 手动触发 `order.list.query/fill.list.query` 后生产前置均返回 `completed + items=0`，Redis 仅存在 `cmd.query/reply/hb` 且无 `event` stream，本地账本全历史 `orders/fills=0`；relay 已补充 refresh 接口 `trade_date` 透传，带 `20260615` 再测前置仍返回空页，下一步需前置/券商侧确认该账户成交查询源。
- `2026-06-15`: 按用户要求暂时关闭生产环境下单权限：未跟踪 `config/relay.prod.yaml` 中两个生产账户 `trading_enabled=false`，9092 已重启；`/v1/status` 显示 production、enabled 2、trading_enabled 0、simulated 0，订单/成交查询能力保留。
