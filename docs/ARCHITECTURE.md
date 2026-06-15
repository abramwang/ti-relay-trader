# relay 架构与当前实现

更新时间：`2026-06-14`

## 结论摘要

relay 采用 Go + Python 的双语言架构：

- Go 负责 9092 在线服务、标准化交易 API、多账户订单状态机、Redis Stream 对接、实时账表写入和健康监控。
- Python 负责盘后对账、历史数据拉取、账户盈亏统计、研究侧脚本、验收脚本和批处理任务。
- Python SDK 负责给策略开发者封装 9092 标准 API，统一请求模型、幂等键、订单状态查询和事件订阅。
- 业务时间统一使用 `Asia/Shanghai`，盘前初始化、收盘后结算、A 股交易日、cron 调度、报表和页面展示都按东八区解释。

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

当前 Go 工程模块：

| 包 | 职责 |
| --- | --- |
| `internal/config` | YAML 配置加载、`docs/api/worker` 模式、多账户路由配置校验 |
| `internal/db/migrations` | PostgreSQL migration runner 和版本记录 |
| `internal/logging` | 结构化日志初始化，默认 JSON 输出 |
| `internal/httpx` | HTTP request_id、中间件、统一 JSON envelope 和标准错误码骨架 |
| `internal/api` | API handler、统一交易接口、SSE 事件流、依赖健康检查、任务状态和页面工作台 |
| `internal/events` | 9092 进程内事件 hub，将账本变化广播到 SSE |
| `internal/ledger` | PostgreSQL 账本 repository，封装订单、成交、资金、持仓、任务和原始 stream 归档 |
| `internal/market` | Meridian 同源薄代理客户端，不重新定义行情字段 |
| `internal/orderflow` | 订单/撤单/刷新 API 编排，负责路由、幂等、草稿订单和 Redis command 发布 |
| `internal/redisstream` | Redis Stream 命名、command envelope、消息解析、账本同步和只读探测 |
| `internal/timeutil` | `Asia/Shanghai` 业务时区和 API 时间格式化 |
| `internal/trading` | 统一交易接口 schema、枚举、基础校验、状态机语义和 `/v1/schema` 目录 |
| `internal/worker` | worker 模式常驻进程，承接 Redis output stream 同步、checkpoint 和事件驱动自动刷新 |

## Python SDK 职责

1. 面向策略开发者封装 9092 HTTP/WebSocket 接口。
2. 提供账户、资金、持仓、订单、成交查询方法。
3. 提供单笔下单、批量下单和撤单方法。
4. 统一生成和传递 `client_order_id`、`gateway_order_id`、`idempotency_key`。
5. 提供 `wait_order_terminal` 和事件订阅能力。
6. 保留实盘异步语义，不把 accepted 误报为最终成交或撤单成功。

SDK 设计见 [docs/PYTHON_SDK.md](/home/ti-relay-trader/docs/PYTHON_SDK.md:1)。

当前主要 HTTP 接口：

| 模块 | 接口 |
| --- | --- |
| 健康检查 | `GET /healthz` |
| 账户 | `GET /v1/accounts`、`GET /v1/accounts/{account_id}` |
| 资金 | `GET /v1/accounts/{account_id}/asset`、`POST /v1/accounts/{account_id}/asset/refresh` |
| 持仓 | `GET /v1/accounts/{account_id}/positions`、`GET /v1/accounts/{account_id}/positions/history`、`POST /v1/accounts/{account_id}/positions/refresh` |
| 交易 | `POST /v1/orders`、`POST /v1/orders/batch`、`POST /v1/orders/{gateway_order_id}/cancel` |
| 查询 | `GET /v1/orders`、`GET /v1/fills`、`GET /v1/history/orders`、`GET /v1/history/fills`、订单/成交刷新接口 |
| 事件 | `GET /v1/events/stream` |
| 交易日任务 | `GET /v1/jobs/runs`、`POST /v1/jobs/runs`、`POST /v1/settlements/snapshots` |
| 绩效与研究 | `GET /v1/accounts/{account_id}/performance/daily`、`GET /v1/accounts/{account_id}/performance/series`、`GET /v1/accounts/{account_id}/performance/series.csv` |
| 对账 | `GET /v1/reconciliations/breaks` |
| 行情薄代理 | `GET /v1/meridian/metadata/instruments`、`GET /v1/meridian/market/snapshots`、`GET /v1/meridian/stream/market/snapshots`、`GET /v1/meridian/market/bars` |
| 监控 | `GET /v1/status`，当前覆盖 PostgreSQL、Redis、订单服务、行情代理、事件流、自动刷新、交易阶段和日流程任务摘要 |

## Python 任务职责

1. 盘前初始化：确认交易日、依赖、账户路由、Redis 位点、初始资金持仓和风险基线。
2. 收盘后结算：追平回报、刷新终态账本、生成资产/持仓快照、编排对账和盈亏输入。
3. 盘后对账：柜台查询结果、Redis 事件流水、内部账表三方核对。
4. 历史数据：通过 Meridian `bars` 拉取账表计算所需的日线/分钟线行情；`metadata/instruments`、`snapshots` 和 level1 snapshot SSE 仅作为交易页面代码补全、行情展示和当前持仓实时估值薄代理。
5. 盈亏统计：日内/日终账户权益、持仓市值、浮动盈亏、已实现盈亏、费用、回撤、收益率。
6. 数据修复：重放 Redis 事件、补写账表、修复状态机断点。
7. 验收脚本：对接 `docs/THIRD_PARTY_INTEGRATION_GUIDE.md` 里的 Redis Stream 验收流程。
8. 研究侧导出：为策略和研究软件生成标准 CSV、Parquet 或数据库视图。

## 多账户模型

relay 的核心域对象建议如下：

| 对象 | 说明 |
| --- | --- |
| `Account` | 资金账户，包含 account_id、broker_id、gateway_id、交易权限、启停状态 |
| `Gateway` | 前置服务实例，包含 env、broker_id、gateway_id、Redis stream prefix、心跳状态 |
| `Order` | relay 标准订单，使用 gateway_order_id 作为跨系统主键 |
| `Fill` | 成交事实，优先按 account_id + gateway_order_id + fill_id 或 match_stream_id 去重 |
| `Position` | 持仓快照和可卖数量，需考虑 A 股 T+1 |
| `CashLedger` | 资金流水、冻结、解冻、成交扣款、费用 |
| `ReconciliationRun` | 一次盘后对账任务 |
| `ReconciliationBreak` | 对账差异项 |

账户路由规则：

1. `account_id` 必须能映射到唯一的 `broker_id + gateway_id + stream_prefix`。
2. 下单、撤单、查询都必须带 `account_id`。
3. `gateway_order_id` 由 relay 或调用方生成，但必须在账户维度唯一。
4. 同一个 Redis 输出流可能有多账户消息，消费端必须按 `account_id`、`origin_message_id`、`gateway_order_id` 过滤。
5. `GET /v1/account-routes` 是生产和测试环境的只读路由诊断入口，展示每个账户的查询/交易权限、只读状态、环境和 Redis `cmd.trade/cmd.query/reply/event/hb/dlq` key。

## Redis Stream 实现口径

relay 与前置服务之间只使用 `relay.stream.v1`。Redis 只承担前置通信和事件传输，不作为最终账本。

每个账户路由对应一个 `stream_prefix`：

```text
relay:{env}:v1:{broker_id}:{gateway_id}
```

完整 stream 由 prefix 加 role 组成：

| Role | Stream | 方向 | 当前用途 |
| --- | --- | --- | --- |
| `cmd.trade` | `{prefix}:cmd.trade` | relay -> 前置 | `order.submit`、`order.batch.submit`、`order.cancel` |
| `cmd.query` | `{prefix}:cmd.query` | relay -> 前置 | 资金、持仓、订单、成交查询刷新 |
| `reply` | `{prefix}:reply` | 前置 -> relay | command 回包、分页查询结果、拒单/失败信息 |
| `event` | `{prefix}:event` | 前置 -> relay | `order.event`、`fill.event` 持续推送 |
| `hb` | `{prefix}:hb` | 前置 -> relay | 心跳原始归档；合并 gateway 状态仍待补充 |
| `dlq` | `{prefix}:dlq` | 前置 -> relay | 死信原始归档；告警和处置状态仍待补充 |

每条 Redis Stream entry 只使用一个 `body` field，值为 JSON 字符串。relay 写入 command 时生成统一 envelope：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "command",
  "message_id": "msg-order-submit-...",
  "request_id": "relay-...",
  "correlation_id": "relay-...",
  "idempotency_key": "order:00030484:gw-...",
  "action": "order.submit",
  "payload": {},
  "sent_at": "2026-06-14T10:30:00+08:00"
}
```

output stream 消费由 `internal/redisstream` 统一解析。所有消息先归档到 `raw_stream_messages`，再根据类型进入标准账表：

1. `reply + asset_page` 写入 `asset_snapshots(intraday)`。
2. `reply + position_page` 写入 `positions`。
3. `reply + order_page` upsert `orders`，必要时补 `relay-summary:<gateway_order_id>` 汇总成交。
4. `reply + fill_page` 幂等写入 `fills`。
5. `reply.status=rejected/failed` 且 action 为下单类时，回写草稿订单为 `rejected` 并保留 `reject_code/reject_message/adapter_context`。
6. `event + order.event` 更新订单主表并追加 `order_events`。
7. `event + fill.event` 写入 `fills`。
8. `hb/dlq` 当前原始归档，后续进入 gateway 状态和告警页面。

9092 docs/api 进程和正式 worker 都复用同一套同步循环。生产部署建议使用 worker 常驻消费 output stream，API 进程专注处理 HTTP 请求。消费位点写入 `stream_checkpoints(stream_key)`，重启后从 `last_stream_id` 继续 `XREAD`；如果没有 checkpoint，则按配置起点从 `0` 追赶历史。重复消费依靠 PostgreSQL 唯一约束和 `ON CONFLICT` 保持幂等。

订单和成交落账后，服务端会通过自动刷新调度器按账户合并触发 `account.asset.query` 和 `account.positions.query`，默认 2 秒 debounce、20 秒 cooldown，避免每条订单推送都查询柜台。

Redis Stream 详细协议、排错和重放口径见 [docs/REDIS_LEDGER_SYNC.md](/home/ti-relay-trader/docs/REDIS_LEDGER_SYNC.md:1) 与 [docs/THIRD_PARTY_INTEGRATION_GUIDE.md](/home/ti-relay-trader/docs/THIRD_PARTY_INTEGRATION_GUIDE.md:1)。

## 数据与基础设施依赖

当前内网资源入口：

| 资源 | 用途 |
| --- | --- |
| `http://doc.quantstage.com` | 内网服务资源文档，例如 MySQL、PostgreSQL、Redis 等 |
| `http://meridian-data.quantstage.com` | A 股基础数据与行情数据文档门户 |
| `docs/THIRD_PARTY_INTEGRATION_GUIDE.md` | 前置服务 Redis Stream 对接手册 |

Meridian 当前纳入 relay 规划的市场数据契约入口：

- `GET /v1/contracts/market`
- `GET /v1/market/bars`
- `GET /v1/market/snapshots`

P8 账表计算只依赖 `bars`。交易端暂不接入实时 level2，也不规划 `trades/orders/order-queues`，后续如需更多行情字段应优先推动 Meridian 在 bars 或证券主数据口径中补充。

不要把内网资源里的密码、Token、生产账号、柜台地址写入仓库。

## 持久化实现

PostgreSQL 是 relay 账表主库：

1. 最终账户交易数据、订单数据、成交数据、资金持仓快照和盘后对账结果必须落盘。
2. 交易账表天然关系型，适合强约束和审计查询。
3. 盘后对账、盈亏统计、跨账户聚合查询更适合 SQL。
4. Redis 只作为前置通信、短期状态和消费位点协调，不作为最终账本。
5. PostgreSQL 访问方式查阅 `http://doc.quantstage.com`，仓库不保存真实连接串或密码。

数据模型和字段映射见 [docs/DATA_MODEL.md](/home/ti-relay-trader/docs/DATA_MODEL.md:1)。

当前表和视图：

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
| `reconciliation_inputs` | 对账输入摘要 |
| `reconciliation_breaks` | 对账差异 |
| `stream_checkpoints` | Redis output stream 消费位点 |
| `job_runs` | 盘前初始化、收盘后结算等任务运行记录 |
| `research_account_daily_performance_v1` | 研究侧账户日绩效导出 view |
| `research_order_fill_export_v1` | 研究侧订单成交关联导出 view |

## 编号与幂等边界

relay 同时保留本地、前置和交易所三个订单编号口径：

| 编号 | 字段 | 唯一范围 | 用途 |
| --- | --- | --- | --- |
| 本地请求/委托 ID | `client_order_id` | `account_id` 内唯一，非空时有唯一索引 | 策略或页面侧追踪请求 |
| 北向订单主键 | `gateway_order_id` | `account_id` 内唯一，强制唯一 | relay 与前置之间的订单关联、撤单、事件归属和账本主键 |
| 前置/柜台订单 ID | `order_id` | 柜台当日口径 | 排查柜台回报和券商侧订单 |
| 交易所委托流号 | `order_stream_id` | 交易所当日口径 | 与交易所回报、成交回报交叉校验 |

下单接口会在写 Redis 前先写本地草稿订单。若调用方没有传 `gateway_order_id`，relay 会生成 `gw-*`；若没有传 `client_order_id`，默认使用 `gateway_order_id`；若没有传 `idempotency_key`，单笔默认使用 `order:{account_id}:{gateway_order_id}`。

下单幂等的预检顺序：

1. 先查 `orders(account_id, gateway_order_id)`。
2. 如果同一 `gateway_order_id` 已存在但 `idempotency_key` 不同，返回冲突，不发布 Redis 命令。
3. 如果同一 `gateway_order_id + idempotency_key` 已存在且核心 payload 一致，返回已有订单并标记 `replayed=true`。
4. 如果同一 `gateway_order_id + idempotency_key` 已存在但 payload 不一致，返回 `IDEMPOTENCY_CONFLICT`。
5. 如果 `gateway_order_id` 不存在，再查 `orders(account_id, idempotency_key)`；同一幂等键指向不同订单或不同 payload 时返回冲突。

批量下单对整批有一个 `idempotency_key`，每个子订单也会拥有独立 `gateway_order_id` 和子订单幂等键。当前实现不允许同一批中混合“已重放订单”和“新订单”，避免部分重放导致重复下单语义不清。

成交去重以订单作用域为准：优先使用 `account_id + gateway_order_id + fill_id`，缺少稳定成交流号时使用 `account_id + order_stream_id + match_timestamp + qty + price` fallback。`fill_id/match_stream_id` 只要求在同一订单内稳定，不要求账户级全局唯一。

## 关键语义

1. `reply.status=accepted` 只代表前置服务接受命令，不代表交易所接单或成交。
2. 订单最终状态以 `order.event` 为准。
3. 成交事实以 `fill.event` 为准，不从订单累计成交差分反推成交明细。
4. 撤单 accepted 不等于撤单成功，最终仍以订单事件为准。
5. 查询必须等待 `status=completed` 且 `chunk.is_last=true`。
6. 交易命令必须使用稳定的 `idempotency_key`。
7. 业务时间、交易日和 cron 调度统一使用 `Asia/Shanghai`；交易日判断和非交易日最近交易日回退以 Meridian 为准。
8. 每个交易日必须有 `pre_open_init` 盘前初始化和 `post_close_settlement` 收盘后结算两个主流程。
9. DLQ、pending_trade_count、pending_query_count 必须纳入监控。

## 当前主链路

单笔下单：

```text
策略/页面/SDK
  -> POST /v1/orders
  -> 账户路由和交易权限校验
  -> gateway_order_id/idempotency_key 预检
  -> PostgreSQL orders 草稿落盘
  -> Redis {prefix}:cmd.trade XADD order.submit
  -> raw_stream_messages 归档 command
  -> 前置 reply/event/fill.event 回流
  -> worker/docs-api sync 合并 orders/order_events/fills
  -> 9092 SSE 广播 order.changed/fill.changed
```

查询刷新：

```text
页面/SDK
  -> POST /v1/accounts/{account_id}/asset|positions|orders|fills/refresh
  -> Redis {prefix}:cmd.query XADD account.*.query/order.list.query/fill.list.query
  -> 前置 reply asset_page/position_page/order_page/fill_page
  -> PostgreSQL asset_snapshots/positions/orders/fills
  -> 本地 GET 查询和 SSE/轮询刷新页面
```

收盘后结算：

```text
cron/manual python -m relay.jobs.post_close_settlement
  -> /v1/status 依赖检查和交易日判断
  -> 触发资金/持仓/订单/成交 refresh
  -> 读取本地账本快照摘要
  -> POST /v1/settlements/snapshots
  -> 写入 close 资产快照、持仓快照、reconciliation run/input/break
  -> POST /v1/jobs/runs
  -> /jobs 和 /v1/status 展示任务结果
```

模拟撮合边界：relay 暂缓内置模拟柜台。实盘接口调试使用券商测试环境；基于历史行情的模拟撮合放在回测引擎。如未来接入外部模拟柜台，也应通过前置服务和 Redis Stream 协议进入 relay。
