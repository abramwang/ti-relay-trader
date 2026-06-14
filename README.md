# relay - TI Relay Trader

relay 是量化研究系统的基础数据项目，负责标准化实盘交易柜台接口对接，以及模拟柜台交易账表能力。

## 线程恢复卡片

- Agent 名称: relay
- 工作目录: `/home/ti-relay-trader`
- 对外端口: `9092`
- 最终服务口径: `http://relay-trader.quantstage.com`
- 当前状态: 已推进 P1/P2/P4/P5/P6 部分底座，完成服务模式、配置加载、结构化日志、统一响应 envelope、API/worker 启动骨架、交易 schema、Redis 只读探测、接口测试台表单化 UI、接口测试台模板/静态资源拆分、成熟交易软件风格 `/trade` 手动测试终端、文档门户同源 `/v1/*` API 挂载、PostgreSQL 首版账本 migration、真实库 migration 验证、账本 repository 骨架、Redis reply/event 到账本同步批处理、单笔下单 API、批量下单 API、撤单 API、资金/持仓查询与前置刷新 API，以及订单/成交账本查询 API；常驻 worker、订单/成交前置查询刷新、实时 SSE/WebSocket 推送和 Python SDK 尚未实现。
- 当前 9092 运行态: 使用未跟踪本地配置 `config/relay.local.yaml` 启动文档门户和同源 API，已接入测试 PostgreSQL、测试 Redis 和测试账户 `00030484`；该文件包含凭据且不提交。
- 最近更新时间: `2026-06-14`
- 恢复方式: 新线程进入本目录后，先阅读本 README 的“线程恢复卡片”“当前进展”“待办事项”“工作日志”，再继续执行下一项待办。

## 项目目标

1. 提供统一的 A 股交易接口，供交易软件和策略调用。
2. 通过 Redis Stream 对接托管机房前置服务，前置层已统一券商结构体和协议。
3. 支持多账户、多 broker、多 gateway 的交易路由和状态管理。
4. 提供盘后对账、历史数据接入、账户盈亏统计和模拟柜台交易账表。
5. 对外暴露稳定服务端口 `9092`，供量化研究系统内其他模块调用。
6. 将关键设计决策、运行状态、接口约定和未完成事项持续沉淀在本 README 中，保证 Codex 线程中断后可以快速恢复。

## 技术栈草案

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

### 模拟柜台交易账表

- 模拟账户表
- 资金流水表
- 持仓表
- 委托表
- 成交表
- 交易日与结算状态
- 冻结资金、冻结持仓、可用资金、可卖持仓计算

## 目录结构

```text
.
├── README.md
├── cmd/
│   ├── relay-docs/      # 9092 文档门户入口，不包含交易核心逻辑
│   └── relayctl/        # 运维和联调 CLI，当前包含 Redis Stream 探测、账本同步和 migration
├── config/              # 本地配置、示例配置、环境变量模板
├── data/
│   └── simdesk/         # 模拟柜台本地数据与账表样例
├── docs/                # 设计文档、接口文档、状态补充说明
├── internal/
│   ├── api/             # 9092 API 服务骨架、健康检查和配置态账户列表
│   ├── config/          # Go 配置加载、服务模式和账户路由配置模型
│   ├── httpx/           # HTTP request_id、中间件、统一 JSON envelope
│   ├── ledger/          # PostgreSQL 账本写入 repository，覆盖账户、订单、事件、成交和原始 stream 消息
│   ├── logging/         # 结构化日志初始化
│   ├── orderflow/       # 订单 API 编排：账户路由、订单草稿、Redis 命令写入和命令归档
│   ├── redisstream/     # Redis Stream 命名、命令 envelope、消息摘要、账本同步和探测边界
│   ├── trading/         # 统一交易接口 schema、枚举、基础校验和状态机语义
│   └── worker/          # 后台 worker 常驻进程骨架
├── migrations/
│   └── postgres/        # PostgreSQL 交易账本 migration
├── scripts/             # 开发、运维、迁移、数据初始化脚本
├── src/
│   └── relay/
│       ├── api/         # 对外 API 服务，默认监听 9092
│       ├── connectors/  # 实盘柜台、券商、网关适配器
│       ├── schemas/     # 标准化请求、响应、事件、账表模型
│       ├── services/    # 业务服务与编排逻辑
│       └── simdesk/     # 模拟柜台撮合、账表、结算逻辑
└── tests/
    ├── integration/     # 集成测试
    └── unit/            # 单元测试
```

## 端口约定

- 对外服务端口固定为 `9092`。
- 最终服务口径固定为 `http://relay-trader.quantstage.com`。
- 默认监听地址后续建议使用 `0.0.0.0:9092`，方便容器或外部系统访问。
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
| `http://relay-trader.quantstage.com/docs` | 文档列表 |
| `http://relay-trader.quantstage.com/docs/readme` | README |
| `http://relay-trader.quantstage.com/docs/architecture` | 架构草案 |
| `http://relay-trader.quantstage.com/docs/roadmap` | 开发路线图 |
| `http://relay-trader.quantstage.com/docs/data-model` | 数据模型与落盘设计 |
| `http://relay-trader.quantstage.com/docs/migrations` | PostgreSQL migration |
| `http://relay-trader.quantstage.com/docs/trading-api-schema` | 统一交易接口 Schema |
| `http://relay-trader.quantstage.com/docs/api-test-console` | 接口测试台设计说明 |
| `http://relay-trader.quantstage.com/docs/trading-terminal` | 交易终端设计说明 |
| `http://relay-trader.quantstage.com/docs/python-sdk` | Python SDK 设计 |
| `http://relay-trader.quantstage.com/docs/operations` | 运行配置与任务管理 |
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
- [x] 形成 Go + Python 初版技术栈草案。
- [x] 明确前置服务已统一券商协议，relay 聚焦多账户和标准 API。
- [x] 实现 9092 文档门户模式，可通过 Web 查看项目框架、文档和测试目录。
- [x] 在首页提供开发路线图入口，路线图详情通过 `/docs/roadmap` 查看。
- [x] 明确账户交易数据、订单数据、成交数据最终需要落盘，PostgreSQL 作为优先候选。
- [x] 明确真实凭据可放在部署机本地配置文件，后台批处理可采用 cron 管理。
- [x] 明确 9092 正式交易接口后续需要封装 Python SDK 给策略开发使用。
- [x] 参考 Meridian SDK，明确 relay SDK 后续通过内网 HTTP 安装包供策略机 pip 安装。
- [x] 定义服务运行模式 `docs`、`api`、`worker`。
- [x] 增加基础配置加载，支持 `RELAY_CONFIG_PATH` 和 `config/relay.example.yaml` 模板。
- [x] 增加结构化日志，默认 JSON 输出，HTTP 请求带 `request_id`。
- [x] 增加统一 JSON 响应 envelope 和标准错误码骨架。
- [x] 增加 API/worker 启动骨架，API 模式已提供 `/healthz`、`/v1/status`、`/v1/accounts`。
- [x] 定义第一版标准交易柜台接口 schema，覆盖账户、资金、持仓、下单、撤单、订单、成交和事件。
- [x] 记录前置测试环境已启动，后续可基于 Redis Stream 做接口联调。
- [x] 新增 Redis Stream 只读探测命令 `relayctl redis-probe`，支持本地配置和 `HX_REDIS_*` 环境变量。
- [x] 新增 Redis Stream 到 PostgreSQL 账本同步命令 `relayctl ledger-sync`，支持 `reply/event` 归档和完整事件落盘。
- [x] 新增 Apifox 风格接口测试台骨架 `/api-console`，用于后续 API 联调。
- [x] 9092 文档门户同源挂载 `/v1/*` API handler，修复接口测试台无法发送请求查看返回的问题。
- [x] 参考 Meridian API 测试页优化 `/api-console`，每个接口按 path/query/body 参数生成表单，响应同时提供 JSON 和表格视图。
- [x] 将接口测试台从 Go 内联字符串拆分为 `web/templates/api_console.html`、`web/static/api-console.css`、`web/static/api-console.js` 和 `web/static/api-console.catalog.json`，由 Go `embed` 打包。
- [x] 新增 `/trade` 手动交易测试终端，参考成熟交易软件布局，支持账户切换、资金持仓、委托成交、下单、撤单、订单详情和轮询状态高亮。
- [x] 新增 PostgreSQL 首版账本 migration，覆盖账户、网关、订单、事件、成交、原始 stream、资金、持仓和对账表。
- [x] 安装 PostgreSQL client，并新增 `relayctl migrate status/up/down` migration runner。
- [x] 使用真实 PostgreSQL 资源创建专用 `relay_trader` 数据库并应用首版账本 migration。
- [x] 新增 PostgreSQL 账本 repository 骨架，支持账户 upsert、订单 upsert、订单事件追加、成交幂等写入和 Redis 原始消息归档。
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
- [ ] 设计模拟柜台账表 schema。
- [x] 实现正式交易服务模式下的 9092 健康检查接口骨架。

## 待办事项

1. 初始化 Python SDK 包骨架，封装账户、资金、持仓、下单、批量下单、撤单、订单查询和成交查询。
2. 为 `/trade` 增加 `GET /v1/events/stream` SSE 或 WebSocket 实时订单/成交推送。
3. 增加订单和成交前置查询刷新命令：`order.list.query`、`fill.list.query` 写入及 reply 合并。
4. 设计模拟柜台账表 schema。
5. 增加 Python 盘后对账与账户盈亏统计任务骨架。
6. 将数据库状态接入 `/v1/status`。
7. 增加常驻 worker，持续同步 `reply/event/hb/dlq`。

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
- 9092 当前线上仍运行文档门户模式；真实交易 API 需要以 `service.mode=api` 和本地凭据配置启动。
- 9092 文档门户模式已同源挂载 `/v1/*` API handler；`/v1/status`、`/v1/schema` 等基础接口可直接从 `/api-console` 发送请求。若启动时未加载数据库和 Redis 本地配置，交易写接口和账本查询会返回明确的服务不可用或空结果。
- 当前刷新 API 只负责写入前置 `cmd.query`；刷新结果落库需要执行 `relayctl ledger-sync` 或等待后续常驻 worker。当前人工验证命令使用 `-roles reply -block 1s -timeout 5s` 同步刷新后的小段 reply。
- 已通过内网文档临时读取 PostgreSQL 连接信息，并在专用 `relay_trader` 数据库执行首版 migration；真实 DSN 不写入仓库。
- 前置测试环境已启动，已使用真实 Redis 跑通 `reply/event` 到 `raw_stream_messages` 的小批量归档。
- 当前观察到的历史 `order.event.payload` 缺少 `trade_side` 和 `business_type`；relay 下单 API 已通过先写订单草稿解决新订单事件回流更新问题，但历史无草稿事件仍只能归档 raw。
- API 下单只会在账户配置 `enabled=true` 且 `trading_enabled=true` 时发送 Redis 命令。
- 联调凭据只放本地配置或安全渠道，不写入仓库。
- 接口测试台当前可在 9092 文档门户同源发送 `/v1/*` 请求；资金、持仓、单笔下单、批量下单、撤单、订单查询和成交查询需要启动时加载本地 PostgreSQL、测试 Redis 和账户路由配置。
- 资金/持仓查询默认读取 PostgreSQL 本地账表；可通过刷新接口主动发前置 `cmd.query`，再用 `ledger-sync` 或后续 worker 合并 reply 到本地账表。
- 测试下单参考价不要硬编码；当前测试行情口径为 Meridian `2026-06-12` 分钟线，示例 `600000.SH` 在 `15:00` 的 close 为 `9.67`。
- `/trade` 第一版行情盘口为 UI 占位数据，真实下单价格仍需要参考 Meridian 或前置行情；订单刷新当前采用 3 秒轮询，后续需要改成 SSE 或 WebSocket。

## 工作日志

- `2026-06-13`: relay 初始化项目目录，创建 README，记录端口 `9092`、职责范围、目录结构、恢复方式和初始待办。
- `2026-06-13`: 添加 `.gitkeep` 占位文件，保证空目录在后续提交中保留。
- `2026-06-13`: 根据用户补充信息，形成 Go + Python 技术栈草案；明确 relay 聚焦多账户标准交易 API、盘后对账、历史数据和账户盈亏统计，前置层负责券商协议统一。
- `2026-06-13`: 按用户要求优先实现 9092 文档门户模式，支持 Web 查看 README、架构草案、前置对接手册、测试目录和项目结构；未触碰交易核心逻辑。
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
- `2026-06-13`: 根据用户要求新增 `/api-console` 接口测试台骨架，采用 Apifox 风格三栏布局：接口集合、请求编辑和响应查看；交易写接口当前标记 `planned` 并禁用发送。
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
