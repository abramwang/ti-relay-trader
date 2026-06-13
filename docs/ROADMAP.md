# relay 开发路线图

更新时间：`2026-06-13`

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
| P4 Redis Stream 前置对接 | todo | 对接托管机房前置服务协议 | 命令写入、reply/event/hb/dlq 消费、幂等和位点管理 |
| P5 交易账表持久化 | doing | 建立标准交易账表和审计流水 | PostgreSQL migration、订单表、成交表、资金持仓表、事件表 |
| P6 9092 正式交易 API 与 SDK | todo | 给交易软件和策略提供统一接口 | HTTP API、Python SDK、事件订阅、状态查询、错误码 |
| P7 盘后对账任务 | todo | 对账柜台、Redis 事件和内部账表 | Python jobs、对账批次、差异表、修复入口 |
| P8 历史数据与盈亏统计 | todo | 接入 Meridian 并计算账户绩效 | 历史行情拉取、资产快照、PnL、收益率、回撤 |
| P9 模拟柜台 | todo | 支持研究和策略联调的模拟交易账表 | 模拟账户、撮合、资金持仓、结算 |
| P10 运维发布 | todo | 形成可部署、可观测、可回滚的服务 | systemd/container、监控、告警、备份、发布手册 |

## 当前优先级

1. 保持 9092 文档门户在线，继续将恢复状态沉淀在 README。
2. 将 Redis Stream `reply/event` 消费接入 PostgreSQL 账本 repository。
3. 补充数据库状态检查到 `/v1/status`。
4. 初始化 Python SDK 包骨架，让策略开发通过 SDK 使用 9092 标准 API。
5. 基于已启动的前置测试环境做 Redis Stream 查询联调和命令写入边界设计。

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
- [ ] 实现命令写入 `cmd.trade` 和 `cmd.query`。
- [ ] 消费 `reply`。
- [ ] 消费 `event`。
- [ ] 消费 `hb`。
- [ ] 消费 `dlq`。
- [ ] 实现 consumer 位点和重放策略。
- [ ] 实现幂等键和 `gateway_order_id` 管理。

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
- [ ] 增加基于临时 PostgreSQL 的 CI 集成测试。

### P6 9092 正式交易 API 与 SDK

- [x] `GET /healthz` 正式服务健康检查骨架。
- [x] `GET /v1/status` 服务状态骨架。
- [x] `GET /v1/accounts` 配置态账户列表骨架。
- [x] 增加 Apifox 风格接口测试台骨架 `/api-console`。
- [ ] `GET /v1/accounts/{account_id}/asset`。
- [ ] `GET /v1/accounts/{account_id}/positions`。
- [ ] `POST /v1/orders`。
- [ ] `POST /v1/orders/batch`。
- [ ] `POST /v1/orders/{gateway_order_id}/cancel`。
- [ ] `GET /v1/orders`。
- [ ] `GET /v1/fills`。
- [ ] `GET /v1/events/stream`。
- [x] 规划 Python SDK 的包形态、核心方法、错误处理和实盘语义。
- [x] 参考 Meridian SDK，明确内网 HTTP tar.gz 安装包和 pip 安装方式。
- [ ] 初始化 `sdk/python/relay_sdk` 包。
- [ ] 实现 SDK 账户、资金、持仓查询。
- [ ] 实现 SDK 下单、批量下单、撤单。
- [ ] 实现 SDK 事件订阅和 `wait_order_terminal`。
- [ ] 增加 SDK mock API 单元测试和集成测试。
- [ ] 增加 SDK 打包脚本和 `/sdk/relay-sdk-<version>.tar.gz` 下载入口。

### P6.1 接口测试台

- [x] 左侧接口集合。
- [x] 中间请求编辑区：method、base URL、path、query、headers、body。
- [x] 右侧响应查看区：HTTP 状态、耗时、响应 JSON。
- [x] 未实现交易写接口默认禁用发送。
- [ ] API handler 完成后自动同步 endpoint 状态。
- [ ] 增加请求样例保存和导出。
- [ ] 增加响应断言和冒烟测试集合。

### P7 盘后对账任务

- [x] 明确盘后、快照、PnL 等后台批处理可优先采用 cron 管理。
- [ ] 增加 Python 任务入口。
- [ ] 拉取柜台资金、持仓、订单、成交查询结果。
- [ ] 对比 Redis 事件流水和内部账表。
- [ ] 记录对账批次和差异。
- [ ] 输出人工复核报告。

### P8 历史数据与盈亏统计

- [ ] 接入 Meridian `bars`。
- [ ] 接入 Meridian `snapshots`。
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
