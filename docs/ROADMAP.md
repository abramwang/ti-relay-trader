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
| P1 工程化底座 | doing | 建立正式服务骨架和配置体系 | 服务模式拆分、配置文件、日志、错误模型、基础测试 |
| P2 标准交易接口设计 | todo | 定义统一 A 股交易 API 和 schema | 账户、资金、持仓、下单、撤单、订单、成交、事件 schema |
| P3 多账户路由 | todo | 管理 account/broker/gateway/stream prefix 关系 | 多账户配置、账户启停状态、路由校验 |
| P4 Redis Stream 前置对接 | todo | 对接托管机房前置服务协议 | 命令写入、reply/event/hb/dlq 消费、幂等和位点管理 |
| P5 交易账表持久化 | todo | 建立标准交易账表和审计流水 | PostgreSQL migration、订单表、成交表、资金持仓表、事件表 |
| P6 9092 正式交易 API | todo | 给交易软件和策略提供统一接口 | HTTP API、事件订阅、状态查询、错误码 |
| P7 盘后对账任务 | todo | 对账柜台、Redis 事件和内部账表 | Python jobs、对账批次、差异表、修复入口 |
| P8 历史数据与盈亏统计 | todo | 接入 Meridian 并计算账户绩效 | 历史行情拉取、资产快照、PnL、收益率、回撤 |
| P9 模拟柜台 | todo | 支持研究和策略联调的模拟交易账表 | 模拟账户、撮合、资金持仓、结算 |
| P10 运维发布 | todo | 形成可部署、可观测、可回滚的服务 | systemd/container、监控、告警、备份、发布手册 |

## 当前优先级

1. 保持 9092 文档门户在线，并让首页成为开发进度面板。
2. 拆分文档门户模式和正式交易服务模式，避免 9092 入口职责混淆。
3. 定义账户、gateway、Redis stream prefix 的多账户路由配置。
4. 定义第一版统一交易接口 schema。
5. 设计 PostgreSQL 交易账表 migration。

## 里程碑细化

### P0 文档门户与恢复机制

- [x] 初始化项目目录。
- [x] 创建 README 恢复卡片。
- [x] 建立 `docs/ARCHITECTURE.md`。
- [x] 启动 9092 文档门户。
- [x] 固化最终服务口径 `http://relay-trader.quantstage.com`。
- [x] 在首页展示开发路线图。

### P1 工程化底座

- [ ] 定义服务运行模式：`docs`、`api`、`worker`。
- [ ] 增加统一配置加载：端口、数据库、Redis、账户路由。
- [ ] 增加结构化日志。
- [ ] 增加统一错误码和响应 envelope。
- [ ] 增加基础单元测试和健康检查测试。

### P2 标准交易接口设计

- [ ] 定义账户模型。
- [ ] 定义资金模型。
- [ ] 定义持仓模型，覆盖 A 股 T+1 可卖数量。
- [ ] 定义下单、批量下单、撤单请求。
- [ ] 定义订单、成交、订单事件、成交事件模型。
- [ ] 定义标准错误码和状态机。

### P3 多账户路由

- [ ] 定义 `account_id -> broker_id + gateway_id + stream_prefix` 映射。
- [ ] 定义账户启停、只读、可交易、模拟账户等状态。
- [ ] 增加路由冲突校验。
- [ ] 增加多账户查询过滤。

### P4 Redis Stream 前置对接

- [ ] 实现命令写入 `cmd.trade` 和 `cmd.query`。
- [ ] 消费 `reply`。
- [ ] 消费 `event`。
- [ ] 消费 `hb`。
- [ ] 消费 `dlq`。
- [ ] 实现 consumer 位点和重放策略。
- [ ] 实现幂等键和 `gateway_order_id` 管理。

### P5 交易账表持久化

- [ ] 选择 migration 工具。
- [ ] 建立 `accounts`、`gateways`。
- [ ] 建立 `orders`、`order_events`。
- [ ] 建立 `fills`。
- [ ] 建立 `positions`、`position_snapshots`。
- [ ] 建立 `cash_ledger`、`asset_snapshots`。
- [ ] 建立 `reconciliation_runs`、`reconciliation_breaks`。

### P6 9092 正式交易 API

- [ ] `GET /healthz` 正式服务健康检查。
- [ ] `GET /v1/accounts`。
- [ ] `GET /v1/accounts/{account_id}/asset`。
- [ ] `GET /v1/accounts/{account_id}/positions`。
- [ ] `POST /v1/orders`。
- [ ] `POST /v1/orders/batch`。
- [ ] `POST /v1/orders/{gateway_order_id}/cancel`。
- [ ] `GET /v1/orders`。
- [ ] `GET /v1/fills`。
- [ ] `GET /v1/events/stream`。

### P7 盘后对账任务

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
- [ ] 增加 metrics 和日志采集。
- [ ] 增加 Redis lag、DLQ、心跳超时告警。
- [ ] 增加数据库备份和恢复说明。
- [ ] 增加发布检查清单。
