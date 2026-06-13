# relay 统一交易接口 Schema

更新时间：`2026-06-13`

## 当前状态

第一版 schema 已落在 Go 包 `internal/trading`，版本号为 `relay.trading.v1alpha1`。

该 schema 定义对象、枚举、基础校验和状态机语义。当前 API 模式已将 `POST /v1/orders` 接入 PostgreSQL 订单草稿和 Redis `cmd.trade` 测试链路。

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
  "time": "2026-06-13T10:00:00Z"
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
  "time": "2026-06-13T10:00:00Z"
}
```

## 枚举

| 名称 | 值 | 说明 |
| --- | --- | --- |
| `exchange` | `SH`、`SZ`、`BJ` | 上海、深圳、北京交易所 |
| `trade_side` | `B`、`S`、`P`、`R` | 买入、卖出、申购、赎回 |
| `business_type` | `S`、`E` | 股票、ETF |
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

1. `fill_id`
2. `order_stream_id + match_timestamp + qty + price`

## API 路由规划

| 方法 | 路径 | 请求 | 响应 | 当前状态 |
| --- | --- | --- | --- | --- |
| `GET` | `/healthz` | - | `StatusView` | 已有骨架 |
| `GET` | `/v1/status` | - | `StatusView` | 已有骨架 |
| `GET` | `/v1/schema` | - | `CatalogDocument` | 已有骨架 |
| `GET` | `/v1/accounts` | - | `[]Account` | 已有配置态骨架 |
| `GET` | `/v1/accounts/{account_id}/asset` | - | `Asset` | 待实现 |
| `GET` | `/v1/accounts/{account_id}/positions` | - | `[]Position` | 待实现 |
| `POST` | `/v1/orders` | `SubmitOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders/batch` | `BatchSubmitOrderRequest` | `[]Order` | 待实现 |
| `POST` | `/v1/orders/{gateway_order_id}/cancel` | `CancelOrderRequest` | `Order` | 待实现 |
| `GET` | `/v1/orders` | `OrderQuery` | `[]Order` | 待实现 |
| `GET` | `/v1/fills` | `FillQuery` | `[]Fill` | 待实现 |
| `GET` | `/v1/events/stream` | - | `OrderEvent | FillEvent` | 待实现 |

## Redis Stream 映射

HTTP API 不直接暴露前置 Redis envelope，但后端会映射到以下 action：

| HTTP API | Redis action | Stream |
| --- | --- | --- |
| `POST /v1/orders` | `order.submit` | `cmd.trade` |
| `POST /v1/orders/batch` | `order.batch.submit` | `cmd.trade` |
| `POST /v1/orders/{gateway_order_id}/cancel` | `order.cancel` | `cmd.trade` |
| `GET /v1/accounts/{account_id}/asset` | `account.asset.query` | `cmd.query` |
| `GET /v1/accounts/{account_id}/positions` | `account.positions.query` | `cmd.query` |
| `GET /v1/orders` | `order.list.query` | `cmd.query` |
| `GET /v1/fills` | `fill.list.query` | `cmd.query` |

`POST /v1/orders` 的 `202 Accepted` 仅表示 relay 已接受请求、写入订单草稿并向 Redis `cmd.trade` 写入 `order.submit`，不表示交易所接单或成交。最终状态以 `order.event` 和 `fill.event` 回流为准。

## 后续工作

1. 实现撤单 API，将 `CancelOrderRequest` 转换为 `order.cancel`。
2. 实现订单和成交查询 API，从 PostgreSQL 账本读取。
3. 增加常驻 worker，持续同步 `reply/event/hb/dlq`。
4. 初始化 Python SDK，直接复用本 schema 文档和 `/v1/schema`。
