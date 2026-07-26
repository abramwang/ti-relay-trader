# relay 开发路线图

更新时间：`2026-07-26`

## 状态口径

- `done`: 已完成并提交。
- `doing`: 当前优先推进。
- `todo`: 已规划，尚未开始。
- `blocked`: 受外部信息、权限或环境阻塞。
- `deferred`: 已明确暂缓，不纳入近期 relay 边界。

## 总体阶段

| 阶段 | 状态 | 目标 | 主要产出 |
| --- | --- | --- | --- |
| P0 文档门户与恢复机制 | done | 9092 可访问项目框架、文档、测试目录和开发路线图 | Go 文档门户、README 恢复卡片、ROADMAP |
| P1 工程化底座 | done | 建立正式服务骨架和配置体系 | 服务模式拆分、配置文件、日志、错误模型、基础测试 |
| P2 标准交易接口设计 | done | 定义统一 A 股交易 API 和 schema | 账户、资金、持仓、下单、撤单、订单、成交、事件 schema |
| P3 多账户路由 | done | 管理 account/broker/gateway/stream prefix 关系 | 多账户配置、账户启停状态、路由校验和路由诊断接口 |
| P4 Redis Stream 前置对接 | doing | 对接托管机房前置服务协议 | 命令写入、reply/event/hb/dlq 消费、幂等和位点管理 |
| P5 交易账表持久化 | doing | 建立标准交易账表和审计流水 | PostgreSQL migration、订单表、成交表、资金持仓表、事件表 |
| P6 9092 正式交易 API 与 SDK | doing | 给交易软件和策略提供统一接口 | HTTP API、Python SDK、事件订阅、状态查询、错误码 |
| P7 交易日流程与盘后对账 | doing | 管理盘前初始化、收盘后结算和盘后对账 | Python jobs、任务状态、对账批次、差异表、修复入口 |
| P8 历史数据与盈亏统计 | doing | 接入 Meridian 并计算账户绩效 | 历史行情拉取、资产快照、PnL、收益率、回撤、绩效归因 |
| P9 模拟柜台 | deferred | 暂缓，不纳入 relay 近期边界 | 实盘调试使用券商测试环境；历史数据模拟撮合放在回测引擎 |
| P10 运维发布 | doing | 形成可部署、可观测、可回滚的服务 | 容器自启动、进程管理、监控、告警、备份、发布手册 |

## 当前优先级

1. 完成 N8 绩效分析 Phase 2/3：将 `/trade#performance` 收敛为净值、基准、超额收益、回撤、收益贡献、交易质量和数据质量工作区。
2. 完成 N9 Redis Stream 运行可观测：把 `hb` 合并为 gateway 在线状态，补 stream lag、DLQ 告警和处置状态。
3. 完成 N10 账本生产化：明确测试/生产数据隔离方案，补数据库级幂等约束、临时 PostgreSQL CI 和备份恢复演练。
4. 完成 N11 交易日与对账闭环：输出人工复核报告，修正非交易日 `trading_day.phase` 语义，并增强任务失败/账户异常告警。
5. 完成 N12 回归与发布：增加 Playwright 页面交互测试、API 断言集合、批量下单测试视图和发布检查清单。
6. P9 模拟柜台继续暂缓；relay 保持实盘接入、账本、审计、对账和策略交易 API 的职责边界。

## 下一步任务

### N8 绩效分析页面 Phase 2/3

状态：`doing`

目标：把 `/trade#performance` 从数据验证页整理为可用于日终复盘的绩效工作区。

当前暂停点：

- [x] 完成现有绩效 API、页面代码和三账户生产账本的只读数据审计。
- [x] 确认现有 `asset_page.net_asset` 主要是资金余额，逆回购、ETF 申赎、费用和外部现金流不能直接套用现有收益公式。
- [x] 以 `2026-07-22` 至 `2026-07-24` 的订单、成交、ETF 成分划转和 Meridian PCF 梳理策略事实：确认 ETF 申赎 T0、股票截面和 ETF 截面三类策略，并识别同一 ETF 在 T0 与截面持仓之间的数量交叉。
- [x] 确认 ETF 申赎 T0 使用赎回时点 Meridian IOPV 作为估算卖出价值，并按篮子价值计提 15bp 综合摩擦成本；T0 买入成本由委托总量构成最小申赎单位整数倍的订单组及其实际成交独立归集，不与 ETF 底仓按日均价摊分。
- [ ] 与用户继续确认正式净值、日内资产桥、股票/ETF 截面、逆回购、费用和外部现金流口径；全部确认前不修改计算逻辑或回填历史数据。
- [ ] 设计跨账户、跨策略归因标识，并修正订单“账户内当日唯一”业务键没有包含交易日的问题。

范围：

- 主图展示账户净值、上证指数基准、超额收益和账户/基准回撤。
- KPI 同时展示上日收盘、日初资产、隔夜调整、日终资产、日内盈亏、费用和数据质量状态。
- 新增只读 `performance/contributions` 聚合接口，按证券输出持仓贡献、成交额、费用、贡献 bps 和质量标记。
- 增加交易质量区：成交率、撤单率、拒单率、未终态订单和异常订单。
- 分钟 K 线继续只保留在交易测试页，绩效页不再把 bars 图当作绩效主图。

验收口径：

- 指定账户和区间能回答净值、收益、回撤、超额收益和主要证券贡献。
- 缺少 open/close 快照、Meridian bars 或柜台字段时明确标记 `missing/estimated`。
- 聚合逻辑有 Go 单元测试，页面有 Playwright 交互测试。
- 页面只读取本地账本和 Meridian，不主动查询柜台。

### N9 Redis Stream 与 gateway 可观测

状态：`todo`

目标：把已经归档的 `hb/dlq` 和 `stream_checkpoints` 变成可判断、可告警、可处置的运行状态。

范围：

- 按 broker/gateway/account 汇总最后心跳时间、在线状态、重连状态和 `BROKER_NOT_READY`。
- 计算每条 output stream 的最新 ID、checkpoint、lag、最近消费时间和最近错误。
- 为 DLQ 建立待处理、已确认、已忽略、已重放状态和审计字段。
- 在 `/v1/status` 和独立运维页面展示 gateway、stream 和 DLQ 状态。
- 定义分级告警阈值，避免柜台非服务时间产生无意义告警。

### N10 账本生产化与环境隔离

状态：`todo`

目标：消除测试/生产共库按账户区分的长期风险，并把应用层幂等提升为数据库约束。

范围：

- 优先采用测试/生产独立 PostgreSQL DSN；如必须共库，再设计 `environment` 进入核心表主键/唯一键的 migration。
- 清理历史重复键后，为 `orders(account_id, idempotency_key)` 增加部分唯一约束。
- 使用临时 PostgreSQL 跑 migration/repository 集成测试。
- 编写数据库备份、恢复和按交易日回放验证手册，并完成一次恢复演练。

### N11 交易日任务与人工复核闭环

状态：`todo`

目标：让盘前/盘后任务不只“运行完成”，还可清楚判断结果是否可信并形成可归档复核材料。

范围：

- 输出账户级人工复核报告，汇总资产、持仓、订单、成交、未终态订单、对账差异和异常账户。
- 非交易日 `trading_day.phase` 返回明确的 `non_trading`，避免仅按时钟显示 `continuous`。
- 增加任务失败、账户异常、刷新超时和快照阻断告警。
- 保留 09:01 盘前初始化和 15:05 生产盘后结算口径。

### N12 API Console、回归测试与发布

状态：`todo`

目标：形成可重复执行的接口、页面和发布验收流程。

范围：

- API catalog 从 handler/schema 自动生成或增加一致性检查。
- API Console 支持请求样例保存/导出和响应断言集合。
- `/trade` 增加批量下单手动测试视图；生产环境保持 `trading_enabled=false`，写入测试只在券商测试环境执行。
- Playwright 覆盖环境/账户切换、日期、分页、排序、K 线、订单详情、绩效页和 jobs 页。
- 补齐 API/worker 独立部署、日志采集、版本回滚和发布检查清单。

## 里程碑细化

### P0 文档门户与恢复机制

- [x] 初始化项目目录。
- [x] 创建 README 恢复卡片。
- [x] 建立 `docs/ARCHITECTURE.md`。
- [x] 启动 9092 文档门户。
- [x] 固化最终服务口径 `http://relay-trader.quantstage.com`。
- [x] 在首页提供开发路线图入口。

### P1 工程化底座

- [x] 定义服务运行模式：`docs`、`api`、`worker`。
- [x] 明确真实凭据放在部署机本地配置文件，模板文件可提交。
- [x] 增加基础配置加载：端口、数据库、Redis、账户路由。
- [x] 增加结构化日志。
- [x] 增加统一错误码和响应 envelope。
- [x] 增加基础单元测试和健康检查测试。
- [x] 增加配置加载单元测试。

### P2 标准交易接口设计

- [x] 明确接口体参考源：Redis Stream 对接文档和 `/home/Titans/resource/include` C++ 头文件。
- [x] 定义账户模型。
- [x] 定义资金模型。
- [x] 定义持仓模型，覆盖 A 股 T+1 可卖数量。
- [x] 定义下单、批量下单、撤单请求。
- [x] 定义订单、成交、订单事件、成交事件模型。
- [x] 定义标准错误码和状态机。
- [x] 增加 `/v1/schema` 发现接口骨架。
- [x] 记录前置测试环境已启动，可用于后续 Redis Stream 联调。

### P3 多账户路由

- [x] 定义 `account_id -> broker_id + gateway_id + stream_prefix` 映射。
- [x] 定义账户启停、只读、可交易、账户环境类型等状态。
- [x] 增加路由冲突校验。
- [x] 增加多账户查询过滤。
- [x] 增加 `GET /v1/account-routes` 路由诊断接口，展示账户权限、环境、stream prefix 和 Redis stream key。

### P4 Redis Stream 前置对接

- [x] 记录前置测试环境已启动。
- [x] 实现只读探测命令 `relayctl redis-probe`。
- [x] 定义 stream prefix、`cmd.trade/cmd.query/reply/event/hb/dlq` 命名辅助。
- [x] 定义 Redis Stream `body` 消息摘要解析，不打印完整 body。
- [x] 增加 Redis body envelope 解析，提取 routing、reply、event、payload 和 adapter_context。
- [x] 增加 `relayctl ledger-sync`，支持批量读取 `reply/event` 并写入 PostgreSQL 账本。
- [x] 使用真实 Redis 小批量联调 `reply/event` 归档。
- [x] 在 9092 docs/api 模式启动轻量后台同步循环，持续消费测试 Redis `reply/event` 更新本地账本。
- [x] 实现 Redis command envelope 和 `cmd.trade` 单笔下单写入。
- [x] 实现撤单命令写入 `cmd.trade`。
- [x] 实现批量下单命令写入 `cmd.trade`。
- [x] 实现账户资金/持仓查询命令写入 `cmd.query`。
- [x] 实现订单/成交查询命令写入 `cmd.query`。
- [x] 消费 `reply` 并归档 raw。
- [x] 合并 `asset_page/position_page/order_page/fill_page` reply 到 PostgreSQL 账本。
- [x] 将下单类 `rejected/failed` reply 回写本地订单终态，并保留前置/柜台错误信息。
- [x] 消费 `event` 并归档 raw。
- [x] 将字段完整的 `order.event/fill.event` 持续消费接入 9092 轻量后台同步循环。
- [x] 将持续消费迁移到正式 worker 并持久化 stream 位点。
- [x] worker 原始归档 `hb`。
- [x] worker 原始归档 `dlq`。
- [x] 实现 consumer 位点和重放策略。
- [x] 实现幂等键和 `gateway_order_id` 管理。
- [x] 支持外部订单按 `order_stream_id` 建立稳定单笔委托 ID，并保留原始篮子/柜台 ID。
- [x] 支持 `synthetic_from_fill` 最小订单事件和 ETF `transfer.event` 语义，普通成交与零价划转分离。
- [x] 接入 `BROKER_NOT_READY` 瞬时错误，不把柜台未登录/重连误写为业务拒单。
- [ ] 将 `hb` 合并为 gateway 心跳状态。
- [ ] 增加 `dlq` 告警和处置状态。
- [ ] 增加 Redis output stream lag、最近消费时间和 checkpoint 健康状态。

### P5 交易账表持久化

- [x] 明确 PostgreSQL 为最终账本候选，Redis 不作为最终账本。
- [x] 建立数据模型和字段映射设计文档。
- [x] 选择 migration 文件口径：工具无关 SQL，兼容 `psql`、`golang-migrate`、`goose`。
- [x] 建立 `accounts`、`gateways`、`account_gateway_routes`。
- [x] 建立 `orders`、`order_events`。
- [x] 建立 `fills`。
- [x] 建立 `raw_stream_messages`。
- [x] 建立 `positions`、`position_snapshots`。
- [x] 建立 `cash_ledger`、`asset_snapshots`。
- [x] 建立 `reconciliation_runs`、`reconciliation_inputs`、`reconciliation_breaks`。
- [x] 安装 PostgreSQL client。
- [x] 增加数据库连接和 migration runner。
- [x] 增加 `relayctl migrate status/up/down`。
- [x] 使用真实 PostgreSQL DSN 执行首版 migration。
- [x] 增加账本 repository 骨架，覆盖账户、订单、订单事件、成交和原始 stream 消息写入。
- [x] 增加账本 repository 单元测试，不依赖真实数据库即可验证 SQL 参数和 JSON payload。
- [x] 增加可选 PostgreSQL 账本集成测试，可通过 `RELAY_LEDGER_TEST_DATABASE_URL` 启用真实写库验证。
- [x] 将 Redis `reply/event` 批量归档接入 `raw_stream_messages`。
- [x] 新订单先写订单草稿，再用缺字段 `order.event` 更新订单状态并追加事件。
- [x] 新增 `stream_checkpoints` 表，持久化 Redis Stream 消费位点、处理计数和最近错误摘要。
- [x] 成交唯一键收敛为 `account_id + gateway_order_id + fill_id`，查询页多条成交使用稳定派生 stream ID。
- [x] 当前持仓全量查询完成后清理旧批次残留行，日终快照等待本轮资金/持仓刷新确认。
- [x] 增加 open/close 资产快照、持仓当日盈亏字段和研究侧绩效 view migration。
- [ ] 让历史 `order.event.payload` 补齐 `trade_side/business_type` 后启用无草稿事件重建订单主表。
- [ ] 明确测试/生产 PostgreSQL 独立 DSN 或共库 `environment` 主键/唯一键 migration。
- [ ] 清理历史重复幂等键后增加 `orders(account_id, idempotency_key)` 部分唯一约束。
- [ ] 增加基于临时 PostgreSQL 的 CI 集成测试。

### P6 9092 正式交易 API 与 SDK

- [x] `GET /healthz` 正式服务健康检查骨架。
- [x] `GET /v1/status` 服务状态和依赖健康检查，覆盖账户摘要、PostgreSQL、Redis、订单服务、行情代理、事件流和自动刷新。
- [x] `GET /v1/accounts` 配置态账户列表骨架。
- [x] 增加 Apifox 风格接口测试台骨架 `/api-console`。
- [x] 文档门户模式同源挂载 `/v1/*` API handler，接口测试台可直接发送请求查看返回。
- [x] `POST /v1/orders` 单笔下单：订单草稿落盘、Redis `cmd.trade` 写入、命令 raw 归档。
- [x] 使用测试 Redis 完成一次单笔下单 API 冒烟，订单回流后落盘到 `filled`。
- [x] 测试下单参考 Meridian `2026-06-12` 分钟线，示例 `600000.SH` `15:00` close 为 `9.67`。
- [x] `GET /v1/accounts/{account_id}/asset`。
- [x] `POST /v1/accounts/{account_id}/asset/refresh`。
- [x] `GET /v1/accounts/{account_id}/positions`。
- [x] `POST /v1/accounts/{account_id}/positions/refresh`。
- [x] `POST /v1/accounts/{account_id}/orders/refresh`。
- [x] `POST /v1/accounts/{account_id}/fills/refresh`。
- [x] `POST /v1/orders/batch`。
- [x] `POST /v1/orders/{gateway_order_id}/cancel`。
- [x] `GET /v1/orders`。
- [x] `GET /v1/fills`。
- [x] `GET /v1/orders` 和 `GET /v1/fills` 默认按 `Asia/Shanghai` 当日过滤。
- [x] `GET /v1/history/orders` 和 `GET /v1/history/fills`。
- [x] `GET /v1/accounts/{account_id}/positions/history`，读取 `position_snapshots` 历史持仓快照。
- [x] `GET /v1/events/stream`。
- [x] 规划 Python SDK 的包形态、核心方法、错误处理和实盘语义。
- [x] 参考 Meridian SDK，明确内网 HTTP tar.gz 安装包和 pip 安装方式。
- [x] 初始化 `sdk/python/relay_sdk` 包。
- [x] 实现 SDK 账户、资金、持仓、订单和成交查询。
- [x] 实现 SDK 资金、持仓、订单和成交刷新指令。
- [x] 实现 SDK 下单、批量下单、撤单。
- [x] 实现 SDK 事件订阅和 `wait_order_terminal` 基础能力。
- [x] 实现 SDK 订单状态和成交回报回调：`on_order_status()`、`on_fill()`、`watch_order_status()`、`watch_fills()`。
- [x] 增加 SDK mock API 单元测试。
- [x] 增加 SDK 集成测试。
- [x] 增加 SDK 打包脚本和 `/sdk/relay-sdk-<version>.tar.gz` 下载入口。
- [x] 发布 `public/sdk/relay-sdk-0.1.0.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.1.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.2.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.3.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.4.tar.gz` 和 SHA256 校验文件，支持历史查询和任务报告落盘。
- [x] 发布 `public/sdk/relay-sdk-0.1.5.tar.gz` 和 SHA256 校验文件，支持收盘结算快照落盘。
- [x] 发布 `public/sdk/relay-sdk-0.1.6.tar.gz` 和 SHA256 校验文件，支持 job run 显式字段和 `completed` 状态兼容。
- [x] 发布 `public/sdk/relay-sdk-0.1.7.tar.gz` 和 SHA256 校验文件，支持 performance、Meridian bars 和 reconciliation helper。
- [x] 发布 `public/sdk/relay-sdk-0.1.8.tar.gz` 和 SHA256 校验文件，修复不同订单复用 `fill_id` 时的成交回调去重。
- [x] 发布 `public/sdk/relay-sdk-0.1.9.tar.gz` 和 SHA256 校验文件，支持绩效序列 `benchmark_security_id` 基准对照。
- [x] 发布 `public/sdk/relay-sdk-0.1.10.tar.gz` 和 SHA256 校验文件，`Position` 增加 `day_unrealized_pnl` 当日持仓浮盈字段。
- [x] 增加 SDK 版本发布检查清单。

### P6.1 接口测试台

- [x] 左侧接口集合。
- [x] 中间请求编辑区：method、base URL、path、query、headers、body。
- [x] 右侧响应查看区：HTTP 状态、耗时、响应 JSON。
- [x] 早期未实现交易写接口默认禁用发送；正式 handler 接入后已开放测试账户链路。
- [x] 9092 文档门户同源暴露 `/v1/*`，基础接口可直接从测试台发送。
- [x] 每个接口按 path/query/body 参数生成表单。
- [x] 响应结果支持 JSON 和表格视图。
- [x] 页面模板、样式、脚本和接口 catalog 从 Go handler 中拆分到 `web/` 资源目录。
- [x] 支持 `GET /v1/events/stream` SSE 事件流连接和最近事件预览。
- [x] 增加订单和成交前置查询刷新模板。
- [x] 增加 9092 页面轻量冒烟测试脚本，覆盖首页、文档、测试索引、API Console、交易终端、静态资源、基础 API 和 SDK 下载入口。
- [ ] API handler 完成后自动同步 endpoint 状态。
- [ ] 增加请求样例保存和导出。
- [ ] 增加响应断言和冒烟测试集合。

### P6.2 手动交易测试终端

- [x] 参考 Stitch 设计稿确定成熟交易软件式页面布局。
- [x] 新增 `/trade` 全屏终端页面，不复用文档门户文章外壳。
- [x] 使用本地模板和静态资源实现，不依赖 Tailwind CDN、Google Fonts 或外部 icon font。
- [x] 接入账户列表、资金、持仓、订单和成交查询。
- [x] 接入单笔下单和撤单。
- [x] 接入资金/持仓刷新指令。
- [x] 订单列表采用 3 秒轮询，状态签名变化时行高亮并写入推送日志。
- [x] 订单详情展示状态轨迹、订单 JSON 和成交执行记录。
- [x] 接入 `GET /v1/events/stream` SSE 实时推送，订单、成交、资金、持仓事件触发页面合并刷新，并保留 3 秒轮询兜底。
- [x] 接入 Meridian `/v1/market/snapshots` 薄代理，替换 `/trade` 盘口占位数据。
- [x] 接入订单/成交前置刷新指令，订单监控区可手动刷新委托和成交。
- [x] 订单监控表和订单详情展示 `reject_message`、柜台错误和 raw adapter context。
- [x] 订单累计成交量存在但成交明细缺失时，向前生成标记型汇总成交，避免订单/成交账本数量口径断裂。
- [x] 交易测试视图压缩右侧持仓版面，资金持仓独立工作区保留完整展示。
- [x] 交易测试主界面接入 ECharts 分钟 K 线，使用 Meridian `bars` 的 open/high/low/close 绘制 candlestick，并按当前账户、标的、交易日标注买卖委托/成交点。
- [x] 资金持仓、订单和成交支持按交易日查询、服务端 cursor 分页和表头排序。
- [x] 证券名称和类型通过 Meridian instruments 补齐，当前持仓通过 Meridian level1 SSE 实时估值。
- [x] 账户别名可在终端修改并落库，不影响真实账户路由和权限。
- [x] 申购/赎回在订单与成交表中单独显示，K 线点位按对应分钟 close 标注。
- [x] 修复 `/trade#asset`、`/trade%23asset` 和 `/trade/asset` 的终端入口兼容。
- [x] 持仓行点击可联动交易标的和行情/K 线。
- [ ] 增加批量下单测试视图。
- [ ] 增加 Playwright 页面冒烟测试。

### P7 交易日流程与盘后对账

- [x] 明确盘后、快照、PnL 等后台批处理可优先采用 cron 管理。
- [x] 明确业务时间统一使用 `Asia/Shanghai`。
- [x] 规划 `pre_open_init` 盘前初始化流程。
- [x] 规划 `post_close_settlement` 收盘后结算流程。
- [x] 增加统一时间工具，集中提供 `Asia/Shanghai` location、业务日期和 API 展示格式。
- [x] 检查订单/成交/资金/持仓账本 API 的历史时间字段展示是否全部转换为 `Asia/Shanghai`，并省略零值时间。
- [x] 增加 Python 任务入口。
- [x] 实现 `python -m relay.jobs.pre_open_init` 任务骨架。
- [x] 实现 `python -m relay.jobs.post_close_settlement` 任务骨架。
- [x] 任务报告输出交易日、依赖状态、账户范围、刷新回执、账本快照摘要和未终态订单列表。
- [x] 建立任务运行账表，记录日流程报告、耗时、终态和错误摘要。
- [x] 将 `pre_open_init` 与 `post_close_settlement` 报告写入任务运行账表。
- [x] `/v1/status` 暴露交易日、交易阶段和日流程最近运行状态。
- [x] 新增 `/jobs` 后台任务状态监控页，展示任务状态、交易日、开始/完成时间、耗时、错误摘要和 report JSON。
- [x] 拉取柜台资金、持仓、订单、成交查询结果。
- [x] 盘前初始化写入 `asset_snapshots(open)` 日初资产快照，作为日内绩效基线；open 快照只写资产，不覆盖日终持仓快照。
- [x] 写入日终 `asset_snapshots(close)`、`position_snapshots` 和 `reconciliation_runs` 对账批次。
- [x] 对比 Redis 原始消息窗口摘要和内部账表摘要。
- [x] 记录 `reconciliation_inputs` 和 `reconciliation_breaks` 差异。
- [x] 生产盘前初始化固定为交易日 09:01，盘后结算固定为交易日 15:05 `Asia/Shanghai`。
- [x] 任务使用 Meridian 交易日信息跳过非交易日，并在 `/jobs` 区分计划、上次运行和历史记录。
- [x] 盘前/盘后任务支持账户级异常隔离和陈旧快照阻断，单账户未就绪不再误判整体失败。
- [ ] 非交易日 `trading_day.phase` 返回 `non_trading`，不再仅按时钟显示交易阶段。
- [ ] 输出人工复核报告。

### P8 历史数据与盈亏统计

- [x] 接入 Meridian `bars` 同源薄代理，保留 Meridian `market_bar.v1` 原始字段。
- [x] `bars` 请求当天或空日期时通过 Meridian 交易日接口回退到最近交易日。
- [x] 接入 Meridian `metadata/instruments` 和 `snapshots` 作为 `/trade` 代码补全和行情刷新薄代理。
- [x] 计算账户日终权益。
- [x] 计算第一版完整已实现盈亏、浮动盈亏、费用和收益率：保留 `settled_profit/unrealized_pnl/day_unrealized_pnl/fee_total/return_rate`，新增 `realized_pnl/gross_pnl/net_pnl` 研究侧口径，其中 `gross_pnl` 使用当日持仓浮动贡献。
- [x] 提供第一版日终 PnL 输入汇总：上一 close 净资产、日盈亏、收益率、持仓快照汇总和成交汇总。
- [x] 绩效 v2 接入当日 `asset_snapshots(open)`，在日绩效、绩效序列、CSV 和 `/trade#performance` 展示日初资产、隔夜调整、日内盈亏和 open-to-close 收益率。
- [x] 提供账户 close 净值绩效序列：日收益、累计收益和最大回撤。
- [x] 在 `/trade` 交易测试主界面使用 Meridian `bars` 绘制当日分钟 K 线和成交量，辅助理解下单点位。
- [x] 基于 Meridian `bars` 生成账户绩效序列、回撤和研究侧导出输入：`benchmark_security_id` 输出基准收益、基准回撤、超额收益并进入 CSV。
- [x] 提供研究侧导出输入第一版：账户绩效序列 CSV。
- [x] 生成研究侧数据库导出视图：`research_account_daily_performance_v1` 和 `research_order_fill_export_v1`。
- [x] 完成绩效分析页面第一版设计文档，明确净值曲线、收益贡献、交易归因和数据质量口径。
- [ ] 完成 `/trade#performance` Phase 2 UI：净值、基准、超额收益、回撤和数据质量。
- [ ] 增加 `performance/contributions` 只读聚合接口和按证券贡献表。
- [ ] 增加交易质量统计：成交率、撤单率、拒单率、未终态和异常订单。
- [ ] 后续按数据完整度评估精确成本引擎、现金流水、逆回购、ETF 申赎和公司行为归因。

### P9 模拟柜台

状态：`deferred`

- [x] 明确 relay 暂缓内置模拟柜台；实盘调试优先使用券商测试环境。
- [x] 明确历史数据驱动的模拟撮合放在回测引擎，不放在 relay 内部，避免实盘边界和行情撮合边界混淆。
- [ ] 如后续需要接入外部模拟柜台，应通过同一前置/Redis Stream 协议进入 relay，而不是在 relay 内实现撮合。

### P10 运维发布

- [x] 明确当前容器内 9092 使用生产只读配置、cron 自启动和健康守护的部署方式。
- [x] 增加 `scripts/relay-docs-service.sh` 启动、停止、重启和状态脚本。
- [x] 增加 9092 `@reboot`/分钟 watchdog 安装脚本，生产下单权限开启时默认拒绝自动启动。
- [x] 增加交易日 cron 示例、生产任务安装、`flock` 任务锁和日志目录约定。
- [x] 增加 SDK 版本发布和安装包维护清单。
- [ ] 将 API、worker 和 docs 职责拆分为独立常驻进程，并提供统一部署/回滚入口。
- [ ] 增加 metrics 和日志采集。
- [ ] 增加 Redis lag、DLQ、心跳超时告警。
- [ ] 增加数据库备份和恢复说明。
- [ ] 增加发布检查清单。
