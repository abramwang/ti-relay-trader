# relay 开发路线图

更新时间：`2026-06-14`

## 状态口径

- `done`: 已完成并提交。
- `doing`: 当前优先推进。
- `todo`: 已规划，尚未开始。
- `blocked`: 受外部信息、权限或环境阻塞。

## 总体阶段

| 阶段 | 状态 | 目标 | 主要产出 |
| --- | --- | --- | --- |
| P0 文档门户与恢复机制 | done | 9092 可访问项目框架、文档、测试目录和开发路线图 | Go 文档门户、README 恢复卡片、ROADMAP |
| P1 工程化底座 | done | 建立正式服务骨架和配置体系 | 服务模式拆分、配置文件、日志、错误模型、基础测试 |
| P2 标准交易接口设计 | doing | 定义统一 A 股交易 API 和 schema | 账户、资金、持仓、下单、撤单、订单、成交、事件 schema |
| P3 多账户路由 | todo | 管理 account/broker/gateway/stream prefix 关系 | 多账户配置、账户启停状态、路由校验 |
| P4 Redis Stream 前置对接 | doing | 对接托管机房前置服务协议 | 命令写入、reply/event/hb/dlq 消费、幂等和位点管理 |
| P5 交易账表持久化 | doing | 建立标准交易账表和审计流水 | PostgreSQL migration、订单表、成交表、资金持仓表、事件表 |
| P6 9092 正式交易 API 与 SDK | doing | 给交易软件和策略提供统一接口 | HTTP API、Python SDK、事件订阅、状态查询、错误码 |
| P7 交易日流程与盘后对账 | doing | 管理盘前初始化、收盘后结算和盘后对账 | Python jobs、任务状态、对账批次、差异表、修复入口 |
| P8 历史数据与盈亏统计 | todo | 接入 Meridian 并计算账户绩效 | 历史行情拉取、资产快照、PnL、收益率、回撤 |
| P9 模拟柜台 | todo | 支持研究和策略联调的模拟交易账表 | 模拟账户、撮合、资金持仓、结算 |
| P10 运维发布 | todo | 形成可部署、可观测、可回滚的服务 | systemd/container、监控、告警、备份、发布手册 |

## 当前优先级

1. 保持 9092 文档门户在线，继续将恢复状态沉淀在 README。
2. 推进盘后对账输入和差异记录：写入 `reconciliation_inputs`，对比柜台查询快照、Redis 原始消息和 relay 标准账本，生成 `reconciliation_breaks`。
3. 增加 9092 页面冒烟测试。
4. 增加批量下单测试视图。
5. 补充 worker 心跳状态建模、DLQ 告警和正式部署脚本。
6. 设计模拟柜台账表 schema。

## 下一步任务

### N3 盘后对账输入与差异记录

状态：`doing`

目标：在 N2 已写入 close 快照和 `reconciliation_runs` 的基础上，补齐盘后对账输入、差异记录和 PnL 输入摘要，让人工复核和后续账户盈亏统计有可追踪依据。

范围：

- 记录 `reconciliation_inputs`：柜台查询快照摘要、relay 标准订单/成交/资金/持仓摘要、Redis 原始消息窗口摘要。
- 生成第一版 `reconciliation_breaks`：未终态订单、订单成交数量不一致、资产/持仓快照缺失、账户刷新失败。
- 在 `post_close_settlement` 报告中增加对账输入数量、差异数量、严重级别和 PnL 输入摘要。
- 为 9092 增加对账批次/差异查询接口或在 `/v1/jobs/runs` 报告中保留足够摘要。
- 增加单元测试覆盖无差异、存在未终态订单、缺少快照和重复运行幂等。

验收口径：

- 同一 `run_id` 重复执行不会重复生成相同输入和差异。
- `reconciliation_runs.summary` 能看到输入数量、差异数量、账户范围和失败摘要。
- 有未终态订单时会形成可查询/可复核的 break，而不是只停留在任务 stdout。
- 后续 PnL 任务至少能读取日终权益、持仓市值、成交金额和费用摘要。

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

- [ ] 定义 `account_id -> broker_id + gateway_id + stream_prefix` 映射。
- [ ] 定义账户启停、只读、可交易、模拟账户等状态。
- [ ] 增加路由冲突校验。
- [ ] 增加多账户查询过滤。

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
- [x] 消费 `event` 并归档 raw。
- [x] 将字段完整的 `order.event/fill.event` 持续消费接入 9092 轻量后台同步循环。
- [x] 将持续消费迁移到正式 worker 并持久化 stream 位点。
- [x] worker 原始归档 `hb`。
- [x] worker 原始归档 `dlq`。
- [x] 实现 consumer 位点和重放策略。
- [x] 实现幂等键和 `gateway_order_id` 管理。
- [ ] 将 `hb` 合并为 gateway 心跳状态。
- [ ] 增加 `dlq` 告警和处置状态。

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
- [ ] 让历史 `order.event.payload` 补齐 `trade_side/business_type` 后启用无草稿事件重建订单主表。
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
- [x] 增加 SDK 版本发布检查清单。

### P6.1 接口测试台

- [x] 左侧接口集合。
- [x] 中间请求编辑区：method、base URL、path、query、headers、body。
- [x] 右侧响应查看区：HTTP 状态、耗时、响应 JSON。
- [x] 未实现交易写接口默认禁用发送。
- [x] 9092 文档门户同源暴露 `/v1/*`，基础接口可直接从测试台发送。
- [x] 每个接口按 path/query/body 参数生成表单。
- [x] 响应结果支持 JSON 和表格视图。
- [x] 页面模板、样式、脚本和接口 catalog 从 Go handler 中拆分到 `web/` 资源目录。
- [x] 支持 `GET /v1/events/stream` SSE 事件流连接和最近事件预览。
- [x] 增加订单和成交前置查询刷新模板。
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
- [x] 拉取柜台资金、持仓、订单、成交查询结果。
- [x] 写入日终 `asset_snapshots(close)`、`position_snapshots` 和 `reconciliation_runs` 对账批次。
- [ ] 对比 Redis 事件流水和内部账表。
- [ ] 记录 `reconciliation_inputs` 和 `reconciliation_breaks` 差异。
- [ ] 输出人工复核报告。

### P8 历史数据与盈亏统计

- [ ] 接入 Meridian `bars`。
- [x] 接入 Meridian `metadata/instruments` 和 `snapshots` 作为 `/trade` 代码补全和行情刷新薄代理。
- [ ] 接入 Meridian `trades/orders/order-queues`。
- [ ] 计算账户日终权益。
- [ ] 计算已实现盈亏、浮动盈亏、费用和收益率。
- [ ] 生成研究侧导出视图。

### P9 模拟柜台

- [ ] 定义模拟账户配置。
- [ ] 定义模拟资金、持仓、订单、成交账表。
- [ ] 实现简化撮合规则。
- [ ] 实现交易日结算。
- [ ] 复用正式交易 API。

### P10 运维发布

- [ ] 定义部署方式。
- [ ] 增加启动、停止、重载脚本。
- [ ] 增加 cron 安装模板和任务锁约定。
- [ ] 增加 SDK 版本发布和安装包维护清单。
- [ ] 增加 metrics 和日志采集。
- [ ] 增加 Redis lag、DLQ、心跳超时告警。
- [ ] 增加数据库备份和恢复说明。
- [ ] 增加发布检查清单。
