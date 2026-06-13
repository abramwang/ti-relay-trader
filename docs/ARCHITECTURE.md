# relay 架构草案

更新时间：`2026-06-13`

## 结论摘要

relay 采用 Go + Python 的双语言架构：

- Go 负责 9092 在线服务、标准化交易 API、多账户订单状态机、Redis Stream 对接、实时账表写入和健康监控。
- Python 负责盘后对账、历史数据拉取、账户盈亏统计、研究侧脚本、验收脚本和批处理任务。
- Python SDK 负责给策略开发者封装 9092 标准 API，统一请求模型、幂等键、订单状态查询和事件订阅。

当前前置程序已经在托管机房内网统一了各券商结构体和协议，relay 不直接对接券商 SDK，不处理柜台原生结构体，只面向前置服务提供的 `relay.stream.v1` Redis Stream 协议。

## 系统边界

```text
交易软件 / 策略 / 研究系统
        |
        | HTTP/WebSocket, port 9092
        v
relay Go 在线服务
        |
        | Redis Stream: relay:{env}:v1:{broker_id}:{gateway_id}:*
        v
托管机房前置服务
        |
        | 券商 SDK / 交易前置
        v
A 股实盘柜台
```

盘后任务：

```text
调度器 / 手动任务
        |
        v
relay Python jobs
        |
        +--> Redis / 前置查询回包
        +--> PostgreSQL / MySQL 账表
        +--> Meridian 数据源
```

## Go 在线服务职责

1. 对外暴露统一 A 股交易接口，默认监听 `0.0.0.0:9092`。
2. 管理多账户、多 broker、多 gateway 的路由关系。
3. 按 Redis Stream 协议写入交易命令和查询命令。
4. 消费 reply、order event、fill event、heartbeat、deadletter。
5. 维护订单状态机，处理 accepted、working、filled、cancelled、rejected 等状态。
6. 将订单、成交、资金、持仓、事件和错误写入持久化账表。
7. 提供事件订阅能力，供交易软件和策略接收订单回报与成交回报。
8. 暴露健康检查、组件状态、Redis lag、DLQ、pending query/trade 等监控接口。
9. 提供 9092 页面内的接口测试台，支持 API 联调、响应查看和后续冒烟测试。

当前 Go 工程底座：

| 包 | 职责 |
| --- | --- |
| `internal/config` | YAML 配置加载、`docs/api/worker` 模式、多账户路由配置校验 |
| `internal/logging` | 结构化日志初始化，默认 JSON 输出 |
| `internal/httpx` | HTTP request_id、中间件、统一 JSON envelope 和标准错误码骨架 |
| `internal/api` | API 模式服务骨架，当前提供 `/healthz`、`/v1/status`、`/v1/accounts` |
| `internal/redisstream` | Redis Stream 命名、前置消息摘要解析和只读探测边界 |
| `internal/trading` | 统一交易接口 schema、枚举、基础校验、状态机语义和 `/v1/schema` 目录 |
| `internal/worker` | worker 模式常驻进程骨架，后续承接 Redis 消费和后台任务 |

## Python SDK 职责

1. 面向策略开发者封装 9092 HTTP/WebSocket 接口。
2. 提供账户、资金、持仓、订单、成交查询方法。
3. 提供单笔下单、批量下单和撤单方法。
4. 统一生成和传递 `client_order_id`、`gateway_order_id`、`idempotency_key`。
5. 提供 `wait_order_terminal` 和事件订阅能力。
6. 保留实盘异步语义，不把 accepted 误报为最终成交或撤单成功。

SDK 设计见 [docs/PYTHON_SDK.md](/home/ti-relay-trader/docs/PYTHON_SDK.md:1)。

建议首批接口：

| 模块 | 接口 |
| --- | --- |
| 健康检查 | `GET /healthz` |
| 账户 | `GET /v1/accounts`、`GET /v1/accounts/{account_id}` |
| 资金 | `GET /v1/accounts/{account_id}/asset` |
| 持仓 | `GET /v1/accounts/{account_id}/positions` |
| 交易 | `POST /v1/orders`、`POST /v1/orders/batch`、`POST /v1/orders/{gateway_order_id}/cancel` |
| 查询 | `GET /v1/orders`、`GET /v1/fills` |
| 事件 | `GET /v1/events/stream` |
| 监控 | `GET /v1/gateways`、`GET /v1/gateways/{gateway_id}/status` |

## Python 任务职责

1. 盘后对账：柜台查询结果、Redis 事件流水、内部账表三方核对。
2. 历史数据：通过 Meridian 数据源拉取 bars、snapshots、trades、orders、order-queues 等数据。
3. 盈亏统计：日内/日终账户权益、持仓市值、浮动盈亏、已实现盈亏、费用、回撤、收益率。
4. 数据修复：重放 Redis 事件、补写账表、修复状态机断点。
5. 验收脚本：对接 `docs/THIRD_PARTY_INTEGRATION_GUIDE.md` 里的 Redis Stream 验收流程。
6. 研究侧导出：为策略和研究软件生成标准 CSV、Parquet 或数据库视图。

## 多账户模型

relay 的核心域对象建议如下：

| 对象 | 说明 |
| --- | --- |
| `Account` | 资金账户，包含 account_id、broker_id、gateway_id、交易权限、启停状态 |
| `Gateway` | 前置服务实例，包含 env、broker_id、gateway_id、Redis stream prefix、心跳状态 |
| `Order` | relay 标准订单，使用 gateway_order_id 作为跨系统主键 |
| `Fill` | 成交事实，优先按 fill_id 或 match_stream_id 去重 |
| `Position` | 持仓快照和可卖数量，需考虑 A 股 T+1 |
| `CashLedger` | 资金流水、冻结、解冻、成交扣款、费用 |
| `ReconciliationRun` | 一次盘后对账任务 |
| `ReconciliationBreak` | 对账差异项 |

账户路由规则：

1. `account_id` 必须能映射到唯一的 `broker_id + gateway_id + stream_prefix`。
2. 下单、撤单、查询都必须带 `account_id`。
3. `gateway_order_id` 由 relay 或调用方生成，但必须在账户维度唯一。
4. 同一个 Redis 输出流可能有多账户消息，消费端必须按 `account_id`、`origin_message_id`、`gateway_order_id` 过滤。

## 数据与基础设施依赖

当前内网资源入口：

| 资源 | 用途 |
| --- | --- |
| `http://doc.quantstage.com` | 内网服务资源文档，例如 MySQL、PostgreSQL、Redis 等 |
| `http://meridian-data.quantstage.com` | A 股基础数据与行情数据文档门户 |
| `docs/THIRD_PARTY_INTEGRATION_GUIDE.md` | 前置服务 Redis Stream 对接手册 |

Meridian 当前可参考的市场数据契约入口：

- `GET /v1/contracts/market`
- `GET /v1/market/bars`
- `GET /v1/market/snapshots`
- `GET /v1/market/trades`
- `GET /v1/market/orders`
- `GET /v1/market/order-queues`

不要把内网资源里的密码、Token、生产账号、柜台地址写入仓库。

## 持久化建议

首选 PostgreSQL 作为 relay 账表主库：

1. 最终账户交易数据、订单数据、成交数据、资金持仓快照和盘后对账结果必须落盘。
2. 交易账表天然关系型，适合强约束和审计查询。
3. 盘后对账、盈亏统计、跨账户聚合查询更适合 SQL。
4. MySQL 可作为兼容选项，但不作为第一优先级。
5. Redis 只作为前置通信、短期状态和消费位点协调，不作为最终账本。
6. PostgreSQL 访问方式查阅 `http://doc.quantstage.com`，仓库不保存真实连接串或密码。

数据模型和字段映射见 [docs/DATA_MODEL.md](/home/ti-relay-trader/docs/DATA_MODEL.md:1)。

建议首批表：

| 表 | 说明 |
| --- | --- |
| `accounts` | 账户配置与状态 |
| `gateways` | 前置服务和 Redis stream 路由 |
| `orders` | 标准订单主表 |
| `order_events` | 订单状态事件流水 |
| `fills` | 成交流水 |
| `positions` | 当前持仓 |
| `position_snapshots` | 日终持仓快照 |
| `cash_ledger` | 资金流水 |
| `asset_snapshots` | 账户资产快照 |
| `reconciliation_runs` | 对账批次 |
| `reconciliation_breaks` | 对账差异 |

## 关键语义

1. `reply.status=accepted` 只代表前置服务接受命令，不代表交易所接单或成交。
2. 订单最终状态以 `order.event` 为准。
3. 成交事实以 `fill.event` 为准，不从订单累计成交差分反推成交明细。
4. 撤单 accepted 不等于撤单成功，最终仍以订单事件为准。
5. 查询必须等待 `status=completed` 且 `chunk.is_last=true`。
6. 交易命令必须使用稳定的 `idempotency_key`。
7. DLQ、pending_trade_count、pending_query_count 必须纳入监控。

## 实施顺序

1. 初始化 Go module，提供 `GET /healthz`，监听 `9092`。
2. 定义配置文件和账户路由模型。
3. 实现 Redis Stream client，支持命令写入和输出流消费。
4. 定义 PostgreSQL schema migration。
5. 实现账户、资金、持仓、订单、成交的只读 API。
6. 实现单笔下单、批量下单、撤单 API。
7. 实现 Python 盘后对账任务。
8. 接入 Meridian 历史数据，补充 PnL 和账户统计。
9. 增加模拟柜台，复用同一套标准 API 和账表。
