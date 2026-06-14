# relay 统一交易接口 Schema

更新时间：`2026-06-14`

## 当前状态

第一版 schema 已落在 Go 包 `internal/trading`，版本号为 `relay.trading.v1alpha1`。

该 schema 定义对象、枚举、基础校验和状态机语义。当前 API 模式已将资金、持仓、单笔下单、批量下单、撤单、订单查询、成交查询和前置刷新接入测试链路；下单/批量下单/撤单写 Redis `cmd.trade`，资金/持仓/订单/成交查询读取 PostgreSQL 本地账本，刷新接口写 Redis `cmd.query`。

## 参考来源

1. 前置服务 Redis Stream 对接手册：`docs/THIRD_PARTY_INTEGRATION_GUIDE.md`
2. C++ 交易结构体：`/home/Titans/resource/include/ti_trader_struct.h`
3. C++ 交易客户端接口：`/home/Titans/resource/include/ti_trader_client.h`
4. C++ 回调接口：`/home/Titans/resource/include/ti_trader_callback.h`

当前前置测试环境已启动，后续需要联调时可以直接基于 Redis Stream 做查询、下单、撤单和事件消费验证。凭据仍只放在部署机本地配置或安全渠道，不写入仓库。

## HTTP Envelope

API 模式统一返回 JSON envelope：

```json
{
  "ok": true,
  "data": {},
  "request_id": "relay-...",
  "time": "2026-06-13T18:00:00+08:00"
}
```

错误响应：

```json
{
  "ok": false,
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "qty must be positive"
  },
  "request_id": "relay-...",
  "time": "2026-06-13T18:00:00+08:00"
}
```

时间字段约定：

1. 业务展示时间统一按 `Asia/Shanghai` 输出，格式为 RFC3339/RFC3339Nano，例如 `2026-06-14T11:00:00+08:00`。
2. PostgreSQL `timestamptz` 仍记录绝对时刻，API 响应在序列化阶段转换为东八区。
3. 订单、成交、资金、持仓、订单事件、成交事件和任务运行记录中的零值时间会省略，不返回 `0001-01-01T00:00:00Z`。

## 枚举

| 名称 | 值 | 说明 |
| --- | --- | --- |
| `exchange` | `SH`、`SZ`、`BJ` | 上海、深圳、北京交易所 |
| `trade_side` | `B`、`S`、`P`、`R` | 买入、卖出、申购、赎回；当前 relay 二级市场下单只开放 `B/S` |
| `business_type` | `S`、`E` | `S` 表示二级市场证券买卖，股票和 ETF 二级市场买卖均使用 `S`；`E` 预留给 ETF 申购/赎回专项，当前 `/v1/orders` 返回 `NOT_IMPLEMENTED` |
| `offset_type` | `O`、`C` | 开仓、平仓；A 股股票通常为 `C` |
| `reply_status` | `accepted`、`partial`、`completed`、`rejected`、`failed` | 前置命令级回包状态 |
| `gateway_status` | `accepted`、`working`、`filled`、`cancelled`、`rejected` | 前置/柜台订单状态 |
| `order_status` | `created`、`accepted`、`working`、`partially_filled`、`filled`、`cancelled`、`rejected` | relay 标准订单状态 |
| `event_type` | `order.event`、`fill.event` | 订单事件、成交事件 |

终态集合：

```text
filled
cancelled
rejected
```

## 标准对象

### Account

账户对象描述 relay 内部可路由账户：

```json
{
  "account_id": "00030484",
  "broker_id": "huaxin",
  "gateway_id": "00030484",
  "stream_prefix": "relay:prod:v1:huaxin:00030484",
  "status": "enabled",
  "enabled": true,
  "trading_enabled": false,
  "simulated": false
}
```

### Asset

资金对象映射 `TiRspAccountInfo` 和前置 `asset_page`：

```json
{
  "account_id": "00030484",
  "cash_available": 50000000.0,
  "cash_total": 50000000.0,
  "net_asset": 50000000.0,
  "market_value": 0.0,
  "stock_value": 0.0,
  "fund_value": 0.0,
  "day_profit": 0.0,
  "position_profit": 0.0,
  "close_profit": 0.0
}
```

### Position

持仓对象映射 `TiRspQryPosition`，保留 A 股 T+1 可卖数量：

```json
{
  "account_id": "00030484",
  "symbol": "600000",
  "name": "浦发银行",
  "exchange": "SH",
  "quantity": 100,
  "sellable_qty": 100,
  "initial_qty": 100,
  "today_qty": 0,
  "avg_cost": 9.54,
  "market_value": 954.0,
  "unrealized_pnl": 0.0,
  "shareholder_id": "A00030484"
}
```

`sellable_qty` 当前按前置/柜台 `position_page` 原样落盘和返回，relay 不在账本层重新计算 A 股 T+1 可卖数量。模拟柜台如果返回同日买入可卖或不可卖的差异，页面和 SDK 会如实展示；需要统一 T+1 规则时应优先在模拟柜台或前置持仓回报中统一字段语义。

### SubmitOrderRequest

下单请求映射前置 `order.submit` payload 和 `TiReqOrderInsert`：

```json
{
  "account_id": "00030484",
  "client_order_id": "cli-0001",
  "gateway_order_id": "gw-cli-0001",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "business_type": "S",
  "offset_type": "C",
  "price": 9.54,
  "qty": 100,
  "idempotency_key": "idem-submit-0001"
}
```

基础校验：

1. `account_id`、`symbol`、`exchange`、`trade_side`、`business_type` 必填。
2. `price` 必须大于 0。
3. `qty` 必须大于 0。
4. `gateway_order_id` 强烈建议传入，后续撤单和事件匹配都依赖它。

### BatchSubmitOrderRequest

批量下单请求：

```json
{
  "account_id": "00030484",
  "orders": [
    {
      "account_id": "00030484",
      "gateway_order_id": "gw-cli-batch-0001-1",
      "symbol": "600000",
      "exchange": "SH",
      "trade_side": "B",
      "business_type": "S",
      "offset_type": "C",
      "price": 9.54,
      "qty": 100
    }
  ],
  "idempotency_key": "idem-batch-0001"
}
```

基础校验：

1. `orders` 至少包含一笔订单。
2. 子订单 `account_id` 必须与批量请求 `account_id` 一致。
3. 同一批内非空 `gateway_order_id` 不允许重复。

### CancelOrderRequest

撤单请求映射前置 `order.cancel` payload 和 `TiReqOrderDelete`：

```json
{
  "account_id": "00030484",
  "gateway_order_id": "gw-cli-0001",
  "cancel_id": "cancel-0001",
  "idempotency_key": "idem-cancel-0001"
}
```

注意：撤单 reply `accepted` 只表示撤单请求已提交，是否撤成必须等待 `order.event.gateway_status=cancelled`。

### Order

订单对象映射前置 `order.event.payload` 和 `TiRtnOrderStatus`：

```json
{
  "account_id": "00030484",
  "gateway_order_id": "gw-cli-0001",
  "order_id": 1680001,
  "order_stream_id": "110018100000001",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "business_type": "S",
  "limit_price": 9.54,
  "order_qty": 100,
  "cum_filled_qty": 0,
  "leaves_qty": 100,
  "status": "working",
  "gateway_status": "working",
  "is_terminal": false
}
```

### Fill

成交对象映射前置 `fill.event.payload` 和 `TiRtnOrderMatch`：

```json
{
  "fill_id": "match-stream-id",
  "account_id": "00030484",
  "gateway_order_id": "gw-cli-0001",
  "order_id": 1680001,
  "order_stream_id": "110018100000001",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "price": 9.54,
  "qty": 100,
  "fee": 0.0,
  "match_timestamp": 1777103459957
}
```

成交去重优先级：

1. `account_id + gateway_order_id + fill_id`
2. `order_stream_id + match_timestamp + qty + price`

`fill_id` 对应的柜台成交流号或 `adapter_context.match_stream_id` 只要求在订单作用域内稳定，不要求在账户当日或全历史范围内唯一。策略端如果自行做成交回调去重，也应把 `gateway_order_id` 纳入 key。

如果前置只推送订单累计成交量，而没有同步推送完整 `fill.event` 或 `fill_page`，relay 会在新订单事件入账时补一条汇总成交，保证订单账本和成交账本的数量口径向前一致。该补齐记录的 `fill_id` 形如 `relay-summary:<gateway_order_id>`，并在 `adapter_context` 中标记 `relay_synthesized=true`、`relay_synthesis_source=order.event/order_page`、`relay_synthesis_reason=order_filled_without_complete_fill_ledger`。这类记录不是柜台逐笔成交，策略端如果需要严格逐笔成交，可以按该标记过滤。

## API 路由规划

| 方法 | 路径 | 请求 | 响应 | 当前状态 |
| --- | --- | --- | --- | --- |
| `GET` | `/healthz` | - | `StatusView` | 已有骨架 |
| `GET` | `/v1/status` | - | `StatusView` | 已实现，包含依赖健康、账户摘要、交易阶段和最近日流程任务状态 |
| `GET` | `/v1/schema` | - | `CatalogDocument` | 已有骨架 |
| `GET` | `/v1/accounts` | - | `[]Account` | 已有配置态骨架 |
| `GET` | `/v1/accounts/{account_id}/asset` | - | `Asset` | 已实现，读取 PostgreSQL 最新快照 |
| `POST` | `/v1/accounts/{account_id}/asset/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/accounts/{account_id}/positions` | `PositionQuery` | `[]Position` | 已实现，默认读取 PostgreSQL 当前持仓 |
| `GET` | `/v1/accounts/{account_id}/positions/history` | `PositionQuery` | `[]Position` | 已实现，读取 `position_snapshots` 历史快照 |
| `GET` | `/v1/accounts/{account_id}/performance/daily` | `trade_date` query | `DailyPerformance` | 已实现，读取日终 close 资产快照、持仓快照和成交汇总 |
| `GET` | `/v1/accounts/{account_id}/performance/series` | `date_from/date_to` query | `PerformanceSeries` | 已实现，读取 close 资产快照生成日绩效、累计收益和回撤序列 |
| `GET` | `/v1/accounts/{account_id}/performance/series.csv` | `date_from/date_to` query | `text/csv` | 已实现，导出账户绩效序列 CSV |
| `POST` | `/v1/accounts/{account_id}/positions/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/accounts/{account_id}/orders/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/accounts/{account_id}/fills/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders` | `SubmitOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders/batch` | `BatchSubmitOrderRequest` | `[]Order` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders/{gateway_order_id}/cancel` | `CancelOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/orders` | `OrderQuery` | `[]Order` | 已实现，默认按 `Asia/Shanghai` 当日读取 PostgreSQL 账本 |
| `GET` | `/v1/fills` | `FillQuery` | `[]Fill` | 已实现，默认按 `Asia/Shanghai` 当日读取 PostgreSQL 账本 |
| `GET` | `/v1/history/orders` | `OrderQuery` | `[]Order` | 已实现，显式历史订单查询 |
| `GET` | `/v1/history/fills` | `FillQuery` | `[]Fill` | 已实现，显式历史成交查询 |
| `GET` | `/v1/events/stream` | - | `SSE Event` | 已实现，支持订单、成交、资金和持仓变化 |
| `GET` | `/v1/meridian/market/bars` | Meridian query | `market_bar.v1` | 已实现，同源薄代理，保留 Meridian 原始字段 |
| `GET` | `/v1/jobs/runs` | `job_name` query | `[]JobRun` | 已实现，查询最近任务运行 |
| `POST` | `/v1/jobs/runs` | `JobRunRequest` | `JobRun` | 已实现，日流程任务报告落盘 |
| `POST` | `/v1/settlements/snapshots` | `SettlementSnapshotRequest` | `SettlementSnapshotResult` | 已实现，收盘结算 close 资产/持仓快照和 reconciliation run 落盘 |

## Redis Stream 映射

HTTP API 不直接暴露前置 Redis envelope，但后端会映射到以下 action：

| HTTP API | Redis action | Stream |
| --- | --- | --- |
| `POST /v1/orders` | `order.submit` | `cmd.trade` |
| `POST /v1/orders/batch` | `order.batch.submit` | `cmd.trade` |
| `POST /v1/orders/{gateway_order_id}/cancel` | `order.cancel` | `cmd.trade` |
| `POST /v1/accounts/{account_id}/asset/refresh` | `account.asset.query` | `cmd.query` |
| `POST /v1/accounts/{account_id}/positions/refresh` | `account.positions.query` | `cmd.query` |
| `POST /v1/accounts/{account_id}/orders/refresh` | `order.list.query` | `cmd.query` |
| `POST /v1/accounts/{account_id}/fills/refresh` | `fill.list.query` | `cmd.query` |

`POST /v1/orders` 的 `202 Accepted` 仅表示 relay 已接受请求、写入订单草稿并向 Redis `cmd.trade` 写入 `order.submit`，不表示交易所接单或成交。最终状态以 `order.event` 和 `fill.event` 回流为准。

若同一 `account_id + gateway_order_id` 和同一 `idempotency_key` 的请求与原始下单核心字段一致，relay 不会再次写 Redis，而是返回已有订单并标记 `replayed=true`，HTTP 状态为 `200 OK`。若同一 `gateway_order_id` 使用不同幂等键，返回 `409 CONFLICT`；若同一 `idempotency_key` 指向不同订单或不同 payload，返回 `409 IDEMPOTENCY_CONFLICT`。

涨跌停等柜台规则当前以异步回报为准。relay 同步层只做 schema、账户路由、重复订单和已知 unsupported 交易类型校验；超涨跌停价格可能先返回 `202 Accepted`，随后通过订单账本/SSE 进入 `rejected`。策略端必须订阅订单状态或轮询账本判断最终结果。若需要同步涨跌停预校验，应以后续接入 Meridian 涨跌停/交易规则数据后单独实现。

拒绝/失败的下单 reply 会被归档到 `raw_stream_messages`，同时回写对应草稿订单为 `rejected`。同步层会从 reply 顶层 `code/message`、payload 的 `reject_code/reject_message/error_* /message` 和 `adapter_context.error_text/broker_status_text` 等字段抽取柜台错误，写入订单的 `reject_code`、`reject_message`，并在 `adapter_context.relay_error_code`、`adapter_context.relay_error_message` 保留归一化后的排错信息。`/trade` 订单监控表展示摘要，订单详情 raw JSON 保留完整上下文。

ETF 二级市场买卖按普通证券二级市场订单提交，使用 `business_type=S`、`trade_side=B/S`，价格精度按 Meridian `instrument_type=etf` 保留 3 位。ETF 申购/赎回不是普通买卖参数，涉及最小申赎单位、申赎清单等数据，当前 relay `/v1/orders` 未实现，`business_type=E` 会返回 `NOT_IMPLEMENTED`。

`POST /v1/orders/{gateway_order_id}/cancel` 会先读取 PostgreSQL 订单账本，只有非终态且 `leaves_qty > 0` 的订单才会写入 Redis `order.cancel`。撤单 `202 Accepted` 只表示撤单请求已提交到前置，是否撤成仍以 `order.event.gateway_status=cancelled` 为准。

`POST /v1/orders/batch` 会为每笔子订单写入本地草稿，再向 Redis `cmd.trade` 写入一条 `order.batch.submit` command。批量请求的 `202 Accepted` 不表示交易所接单或成交，最终仍以回流事件为准。

当前 `GET /v1/accounts/{account_id}/asset`、`GET /v1/accounts/{account_id}/positions`、`GET /v1/orders` 和 `GET /v1/fills` 是本地账本查询，不主动查询柜台。对应的 `POST .../refresh` 接口会向前置发送 `account.asset.query`、`account.positions.query`、`order.list.query` 或 `fill.list.query`，由 9092 轻量同步循环、`relayctl ledger-sync` 或后续正式 worker 把 `asset_page/position_page/order_page/fill_page` 合并回 PostgreSQL。

`GET /v1/orders` 和 `GET /v1/fills` 不传 `trade_date/date_from/date_to/history` 时，默认按 `Asia/Shanghai` 当日过滤。历史订单和成交应使用 `/v1/history/orders`、`/v1/history/fills`，或在原查询接口显式传 `history=true`、`trade_date=YYYYMMDD`、`date_from=YYYYMMDD`、`date_to=YYYYMMDD`。历史持仓使用 `/v1/accounts/{account_id}/positions/history`，数据来源为日终 `position_snapshots`。

订单、成交、当前持仓和历史持仓查询均支持 `limit` + `cursor` 翻页。第一版 cursor 采用 offset 语义，响应中如果存在 `next_cursor`，客户端可在下一次查询带上该值继续向后读取；如果 `next_cursor` 为空，表示当前条件已到末页。`/trade` 页面默认使用每页 50 条，通过 `next_cursor` 做服务端分页。

`GET /v1/accounts/{account_id}/performance/daily?trade_date=YYYYMMDD` 返回账户日终权益和第一版 PnL 输入汇总。该接口以指定交易日 `asset_snapshots(snapshot_type=close)` 为主记录，读取上一条 close 净资产计算 `daily_pnl` 和 `return_rate`，并汇总同日 `position_snapshots` 的持仓市值/浮动盈亏以及 `fills` 的买入金额、卖出金额、成交额和费用。接口只读取本地账本，不主动查询柜台；如果目标日尚未写入 close 资产快照，会返回 `404 NOT_FOUND`。

`GET /v1/accounts/{account_id}/performance/series?date_from=YYYYMMDD&date_to=YYYYMMDD` 返回账户 close 净值绩效序列。服务读取区间内 `asset_snapshots(snapshot_type=close)`，按上一条 close 净资产计算单日收益，并在响应层计算 `cumulative_return`、`drawdown`、`summary.total_return` 和 `summary.max_drawdown`。该接口当前只依赖本地账本，不主动查询柜台；后续可结合 Meridian bars 增加基准序列和研究导出输入。

`GET /v1/accounts/{account_id}/performance/series.csv?date_from=YYYYMMDD&date_to=YYYYMMDD` 复用同一绩效序列口径，返回 CSV 文件，便于研究侧脚本、表格工具或验收脚本直接下载。CSV 当前包含账户、交易日、净资产、日收益、累计收益、回撤、成交额、费用和快照时间等列。

`GET /v1/meridian/market/bars` 是 Meridian `GET /v1/market/bars` 的同源薄代理，用于 P8 账表计算、绩效序列和交易终端分钟线的行情输入。relay 不重新定义 bars 字段，也不做字段映射；响应保持 Meridian `market_bar.v1` 的 `data/meta/error` 结构。典型参数包括 `security_id`、`trade_date`、`frequency`、`adjustment`、`start_time`、`end_time` 和 `limit`，具体字段约束以 Meridian 为准。bars 查询使用 `trade_date`，不使用 `start_date/end_date`；例如 `security_id=600000.SH&trade_date=20260612&frequency=1m&adjustment=none&start_time=09:30:00&end_time=15:00:00&limit=300`。当 `trade_date` 为空或等于东八区当天时，relay 会先调用 Meridian 交易日接口取得 `previous_or_current_trading_date`，非交易日自动读取最近交易日 bars。

`POST /v1/jobs/runs` 用于 Python 日流程任务将 JSON 报告写入 `job_runs`，`/v1/status` 只展示最近盘前/盘后任务摘要，不返回完整 `report_json`。

`POST /v1/settlements/snapshots` 用于收盘后结算任务内部调用。请求体包含 `trade_date`、`account_ids`、`run_id`、`snapshot_type=close`、`source=post_close_settlement` 和可选 `dry_run`。服务会从本地账本读取指定账户的最新资金、当前持仓、目标交易日订单和成交，将资金写入 `asset_snapshots(close)`，将持仓写入 `position_snapshots`，并 upsert `reconciliation_runs`。该接口不向前置发送查询命令；调用前应先执行资金/持仓/订单/成交 refresh 并等待账本合并。

## 后续工作

1. 接入 Meridian `bars`，补全历史行情和账户绩效序列。
2. 增加常驻 worker 心跳状态和 DLQ 告警。
