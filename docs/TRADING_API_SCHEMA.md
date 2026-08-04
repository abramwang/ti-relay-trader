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
  "total_cost": 954.0,
  "avg_cost_source": "broker_total_position_cost",
  "cost_complete": true,
  "market_value": 954.0,
  "unrealized_pnl": 0.0,
  "day_unrealized_pnl": 0.0,
  "shareholder_id": "A00030484"
}
```

`sellable_qty` 当前按前置/柜台 `position_page` 原样落盘和返回，relay 不在账本层重新计算 A 股 T+1 可卖数量。券商测试环境或外部前置如果返回同日买入可卖/不可卖差异，页面和 SDK 会如实展示；需要统一 T+1 规则时应优先在前置持仓回报中统一字段语义。

当前持仓账本只展示 `quantity > 0` 的 `positions` 行。`position_page` 被视为账户全量快照批次：同一 `origin_message_id/correlation_id` 下的 `partial` 回包逐条 upsert，`completed` 回包到达后清理该账户本次查询开始前仍未更新的旧当前持仓，避免旧批次残留标的被误认为仍持有。历史持仓查询仍读取 `position_snapshots`，不受当前表清理影响。

持仓盈亏统一保留两列：`unrealized_pnl` 是按买入成本计算的总持仓浮盈；`day_unrealized_pnl` 是当日持仓浮动贡献，老仓按今日开盘价作日内基准，当日买入按当日买入成交成本作基准。当前持仓查询会在账本字段缺失或行情更新时结合 Meridian level1 snapshot 和当日成交账本重算，历史持仓查询只读取 `position_snapshots` 已落盘字段。

OC 2026-08-03 版本为持仓增加 `total_cost`、`avg_cost_source` 和 `cost_complete`。Relay 优先使用 `cost_complete=true` 的柜台总成本作为可信起算成本；`avg_cost_source=unavailable` 时不得回退信任 `avg_cost`。旧 OC 的 `market_value` 实际承载 `TotalPosCost`，不是盯市市值，因此 Relay 不再把该字段直接写入标准市值；当前市值统一使用 Meridian 行情重估，只有 OC 同时提供可靠 `last_price` 时才可在同步层计算 `quantity * last_price`。

### SubmitOrderRequest

下单请求映射前置 `order.submit` payload 和 `TiReqOrderInsert`：

```json
{
  "account_id": "00030484",
  "client_order_id": "cli-0001",
  "gateway_order_id": "gw-cli-0001",
  "trade_date": "20260724",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "business_type": "S",
  "offset_type": "C",
  "price": 9.54,
  "qty": 100,
  "idempotency_key": "idem-submit-0001",
  "strategy_type": "stock_cross_section",
  "strategy_id": "alpha-basket-v1",
  "basket_id": "basket-20260724-001",
  "parent_order_id": "",
  "t0_order_group_id": ""
}
```

基础校验：

1. `account_id`、`symbol`、`exchange`、`trade_side`、`business_type` 必填。
2. `price` 必须大于 0。
3. `qty` 必须大于 0。
4. `gateway_order_id` 可由调用方传入；未传时 relay 会生成 `gw-*`。无论来源如何，它都是撤单和事件匹配的北向主键。
5. `trade_date` 可选；未传时 relay 按 `Asia/Shanghai` 当前日期写入本地草稿。查询/回包链路会优先使用 OC payload 中的 `trade_date`，否则从订单时间推导。
6. `strategy_type/strategy_id/basket_id/parent_order_id/t0_order_group_id` 可选，用于后续绩效归因，不影响下单校验和 OC 交易语义。

订单编号和幂等规则：

1. `gateway_order_id` 在 `account_id + trade_date` 内唯一；`000012` 已将订单、订单事件和成交外键统一到该业务键。OC 查询页与实时推送必须对同一订单使用同一 ID。
2. `client_order_id` 未传时默认等于 `gateway_order_id`；非空时在 `account_id` 内唯一。
3. `idempotency_key` 未传时，单笔下单默认 `order:{account_id}:{gateway_order_id}`。
4. 重复提交同一 `gateway_order_id + idempotency_key + payload` 会返回已有订单，`replayed=true`，不再写 Redis。
5. 同一 `gateway_order_id` 换幂等键，或同一幂等键换 `gateway_order_id/payload`，返回 `409` 冲突。
6. 批量下单的整批 `idempotency_key` 对整批 payload 生效，子订单仍各自拥有 `gateway_order_id` 和子订单幂等键。

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
  "business_type": "S",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "price": 9.54,
  "qty": 100,
  "fee": 0.0,
  "trade_date": "2026-07-24",
  "match_timestamp": 1777103459957,
  "strategy_type": "stock_cross_section",
  "strategy_id": "alpha-basket-v1",
  "basket_id": "basket-20260724-001"
}
```

成交去重优先级：

1. `account_id + trade_date + gateway_order_id + fill_id`
2. `order_stream_id + order_id + symbol + exchange + match_timestamp + qty + price`

`fill_id` 对应的柜台成交流号或 `adapter_context.match_stream_id` 只要求在订单作用域内稳定，不要求在账户当日或全历史范围内唯一。策略端如果自行做成交回调去重，也应把 `gateway_order_id` 纳入 key。

如果前置只推送订单累计成交量，而没有同步推送完整 `fill.event` 或 `fill_page`，relay 会在新订单事件入账时补一条汇总成交，保证订单账本和成交账本的数量口径向前一致。该补齐记录的 `fill_id` 形如 `relay-summary:<gateway_order_id>`，并在 `adapter_context` 中标记 `relay_synthesized=true`、`relay_synthesis_source=order.event/order_page`、`relay_synthesis_reason=order_filled_without_complete_fill_ledger`。这类记录不是柜台逐笔成交，策略端如果需要严格逐笔成交，可以按该标记过滤。

### ComponentTransfer

ETF 申赎成分证券划转、现金替代和 0 价记录使用独立 `ComponentTransfer` 账本。Relay 接收实时 `transfer.event` 和查询 `fill_page.component_transfers[]`，写入 `etf_component_transfers`，不会写入 `fills`。`component_value=null` 表示 OC 未提供可估值金额，不解释为 0；柜台原始方向保留在 `broker_trade_side/broker_business_type`。

OC v1.2 生成的 `gateway_order_id` 是不透明稳定标识。Relay 不从 `basket_id` 或 OC 内部 token 重建、截断或改写 ID；新订单的实时事件、查询回包和 OC 重启后查询都必须保留 Relay 原始 ID。只有归档重放旧 `etfarb#...` 消息时保留一次兼容修复。

### 撤单动作结果

`order.cancel` 的 `reply.status=accepted` 只表示撤单请求已交给柜台接口。柜台明确拒绝时，OC v1.2 发布 `event_type=order.cancel.event/event_name=order.cancel.rejected`；响应超时则写入 `CANCEL_RESPONSE_TIMEOUT` DLQ。Relay 将这些结果独立写入 `order_cancel_attempts` 并发布 `order.cancel.rejected` SSE，不修改原订单的 `status/gateway_status/reject_code/reject_message`。成功撤单仍只以普通 `order.event.gateway_status=cancelled` 为准。

`COMMAND_OUTCOME_UNKNOWN` 表示 OC 重启时交易命令结果不可安全推断，Relay 不把草稿订单改成拒绝，必须先查询对账。`QUERY_INTERRUPTED` 可使用新 `message_id` 重试查询。批量下单 reply 的 `failed_orders[]` 按 `index/gateway_order_id` 逐笔回写对应失败子单，不把整个 batch 合并成一个虚拟订单。

## API 路由规划

| 方法 | 路径 | 请求 | 响应 | 当前状态 |
| --- | --- | --- | --- | --- |
| `GET` | `/healthz` | - | `StatusView` | 已有骨架 |
| `GET` | `/v1/status` | - | `StatusView` | 已实现，包含依赖健康、账户摘要、交易阶段、Meridian 交易日状态和最近日流程任务状态 |
| `GET` | `/v1/schema` | - | `CatalogDocument` | 已有骨架 |
| `GET` | `/v1/accounts` | - | `[]Account` | 已实现，配置账户列表并合并 PostgreSQL 账户别名 |
| `GET` | `/v1/account-routes` | - | `[]AccountRoute` | 已实现，展示账户路由、查询/交易权限、环境和 Redis stream key |
| `PATCH` | `/v1/accounts/{account_id}/alias` | `{alias}` | `Account` | 已实现，写入 PostgreSQL `accounts.account_name` |
| `GET` | `/v1/accounts/{account_id}/asset` | - | `Asset` | 已实现，读取 PostgreSQL 最新快照 |
| `POST` | `/v1/accounts/{account_id}/asset/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/accounts/{account_id}/positions` | `PositionQuery` | `[]Position` | 已实现，默认读取 PostgreSQL 当前持仓 |
| `GET` | `/v1/accounts/{account_id}/positions/history` | `PositionQuery` | `[]Position` | 已实现，读取 `position_snapshots` 历史快照，默认 `snapshot_type=close`，可传 `snapshot_type=open` |
| `GET` | `/v1/accounts/{account_id}/performance/daily` | `trade_date` query | `DailyPerformance` | 已实现，读取日初 open、日终 close、持仓快照和成交汇总 |
| `GET` | `/v1/accounts/{account_id}/performance/contributions` | `trade_date` query | `ContributionResult` | 已实现，只读聚合证券/策略贡献、费用、贡献 bp 和质量标记；空日期取 Meridian 当前或最近交易日 |
| `GET` | `/v1/accounts/{account_id}/performance/series` | `date_from/date_to/benchmark_security_id` query | `PerformanceSeries` | 已实现，读取 close 资产快照生成净值序列，同时返回 open-to-close 日内绩效字段，并可用 Meridian bars 增加基准和超额收益 |
| `GET` | `/v1/accounts/{account_id}/performance/series.csv` | `date_from/date_to/benchmark_security_id` query | `text/csv` | 已实现，导出账户绩效、基准和超额收益 CSV |
| `GET` | `/v1/performance/settings` | - | `PerformanceSettings` | 已实现，返回经济净值公式版本、对账阈值和人工输入写入开关 |
| `GET` | `/v1/performance/fee-rules` | `account_id/status/effective_on/limit` query | `[]FeeRule` | 已实现，读取账户级、生效区间版本化费率规则 |
| `POST` | `/v1/performance/fee-rules` | `FeeRule` | `FeeRule` | 已实现，新增费率规则；默认因 `performance.settings_write_enabled=false` 返回 `403` |
| `GET` | `/v1/accounts/{account_id}/cash-ledger` | `trade_date/date_from/date_to/flow_class/status/limit` query | `[]CashLedgerEntry` | 已实现，读取人工资金流水、外部入出金和柜台间划转 |
| `POST` | `/v1/accounts/{account_id}/cash-ledger` | `CashLedgerEntry` | `CashLedgerEntry` | 已实现，新增手工资金流水；默认写入关闭 |
| `POST` | `/v1/accounts/{account_id}/cash-ledger/{entry_id}/confirm` | `{operator}` | `CashLedgerEntry` | 已实现，在账户维度确认 draft 流水 |
| `POST` | `/v1/accounts/{account_id}/cash-ledger/{entry_id}/void` | `{operator}` | `CashLedgerEntry` | 已实现，在账户维度作废 draft/confirmed 流水 |
| `GET` | `/v1/accounts/{account_id}/performance/baselines` | - | `[]NavBaseline` | 已实现，读取手工维护的日初经济净值基线 |
| `POST` | `/v1/accounts/{account_id}/performance/baselines` | `NavBaseline` | `NavBaseline` | 已实现，新增日初经济净值基线；默认写入关闭 |
| `GET` | `/v1/accounts/{account_id}/performance/reverse-repo` | `trade_date` query | `ReverseRepoResult` | 已实现，从成交账本估算 `204001.SH` 逆回购本金、占款天数、毛息、费用和净息，不落库 |
| `POST` | `/v1/accounts/{account_id}/performance/reverse-repo/rebuild` | `trade_date` query | `ReverseRepoResult` | 已实现，重建并落库逆回购应计结果；默认写入关闭 |
| `GET` | `/v1/accounts/{account_id}/performance/reverse-repo/accruals` | `trade_date` query | `[]ReverseRepoAccrual` | 已实现，查询已落库逆回购应计结果 |
| `GET` | `/v1/accounts/{account_id}/performance/economic-nav/preview` | `trade_date/status` query | `EconomicNAVResult` | 已实现，按当前账本试算经济净值，不写库 |
| `POST` | `/v1/accounts/{account_id}/performance/economic-nav/rebuild` | `{trade_date,status}` | `EconomicNAVResult` | 已实现，重建并落库当前 economic NAV 版本和对账记录；默认写入关闭 |
| `GET` | `/v1/accounts/{account_id}/performance/economic-nav` | `trade_date` 或 `date_from/date_to` query | `[]PerformanceNAV` | 已实现，查询当前版本化经济净值结果 |
| `GET` | `/v1/accounts/{account_id}/performance/nav-reconciliations` | `trade_date` 或 `date_from/date_to` query | `[]NAVReconciliation` | 已实现，查询经济净值对账结果 |
| `POST` | `/v1/accounts/{account_id}/positions/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/accounts/{account_id}/orders/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/accounts/{account_id}/fills/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/query-status/{origin_message_id}` | - | `QueryCommandStatus` | 已实现，从归档 reply 判断查询是否存在唯一 completed final 终态 |
| `POST` | `/v1/orders` | `SubmitOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders/batch` | `BatchSubmitOrderRequest` | `[]Order` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders/{gateway_order_id}/cancel` | `CancelOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/orders` | `OrderQuery` | `[]Order` | 已实现，默认按 `Asia/Shanghai` 当日读取 PostgreSQL 账本 |
| `GET` | `/v1/fills` | `FillQuery` | `[]Fill` | 已实现，默认按 `Asia/Shanghai` 当日读取 PostgreSQL 账本 |
| `GET` | `/v1/transfers` | `ComponentTransferQuery` | `[]ComponentTransfer` | 已实现，默认按 `Asia/Shanghai` 当日读取 ETF 成分股划转账本 |
| `GET` | `/v1/history/orders` | `OrderQuery` | `[]Order` | 已实现，显式历史订单查询 |
| `GET` | `/v1/history/fills` | `FillQuery` | `[]Fill` | 已实现，显式历史成交查询 |
| `GET` | `/v1/history/transfers` | `ComponentTransferQuery` | `[]ComponentTransfer` | 已实现，显式历史 ETF 成分股划转查询 |
| `GET` | `/v1/events/stream` | - | `SSE Event` | 已实现，支持订单、成交、资金和持仓变化 |
| `GET` | `/v1/meridian/market/bars` | Meridian query | `market_bar.v1` | 已实现，同源薄代理，保留 Meridian 原始字段 |
| `GET` | `/v1/meridian/stream/market/bars` | Meridian query | Meridian SSE | 已实现，同源 SSE 薄代理，默认 `frequency=1m`、`data_scope=realtime` |
| `GET` | `/v1/meridian/market/etf-components` | Meridian query | Meridian payload | 已实现，ETF PCF 成分清单薄代理 |
| `GET` | `/v1/meridian/market/etf-cash-components` | Meridian query | `etf_cash_component.v1` | 已实现，ETF PCF 现金清单及最小申赎单位薄代理 |
| `GET` | `/v1/meridian/market/etf-pcf-status` | - | Meridian payload | 已实现，PCF 同步状态薄代理 |
| `GET` | `/v1/meridian/metadata/adjust-factors` | Meridian query | Meridian payload | 已实现，同源薄代理，保留 Meridian 原始字段 |
| `GET` | `/v1/jobs/runs` | `job_name` query | `[]JobRun` | 已实现，查询最近任务运行 |
| `POST` | `/v1/jobs/runs` | `JobRunRequest` | `JobRun` | 已实现，日流程任务报告落盘 |
| `POST` | `/v1/settlements/snapshots` | `SettlementSnapshotRequest` | `SettlementSnapshotResult` | 已实现，盘前 open 资产快照、收盘 close 资产/持仓快照和 reconciliation run 落盘 |

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

若同一 `account_id + gateway_order_id` 和同一 `idempotency_key` 的请求与原始下单核心字段一致，relay 不会再次写 Redis，而是返回已有订单并标记 `replayed=true`，HTTP 状态为 `200 OK`。核心字段包含 `trade_date` 和策略归因字段；同一订单号重放但策略标签不同会返回 `409 IDEMPOTENCY_CONFLICT`。若同一 `gateway_order_id` 使用不同幂等键，返回 `409 CONFLICT`；若同一 `idempotency_key` 指向不同订单或不同 payload，返回 `409 IDEMPOTENCY_CONFLICT`。该语义由应用预检和 PostgreSQL insert-only 原子占位共同保证，并发请求不能同时发布同一笔订单命令。

涨跌停等柜台规则当前以异步回报为准。relay 同步层只做 schema、账户路由、重复订单和已知 unsupported 交易类型校验；超涨跌停价格可能先返回 `202 Accepted`，随后通过订单账本/SSE 进入 `rejected`。策略端必须订阅订单状态或轮询账本判断最终结果。若需要同步涨跌停预校验，应以后续接入 Meridian 涨跌停/交易规则数据后单独实现。

拒绝/失败的下单 reply 会被归档到 `raw_stream_messages`，同时回写对应草稿订单为 `rejected`。同步层会从 reply 顶层 `code/message`、payload 的 `reject_code/reject_message/error_* /message` 和 `adapter_context.error_text/broker_status_text` 等字段抽取柜台错误，写入订单的 `reject_code`、`reject_message`，并在 `adapter_context.relay_error_code`、`adapter_context.relay_error_message` 保留归一化后的排错信息。`BROKER_NOT_READY` 和 `COMMAND_OUTCOME_UNKNOWN` 是例外：前者表示券商柜台尚未 ready，后者表示 OC 重启时命令结果不可安全推断；Relay 只归档并提示重试或先查询对账，不把订单账本改成 `rejected`。撤单 reply/event/DLQ 无论失败、超时或结果未知都只写 `order_cancel_attempts`。`/trade` 订单监控表展示订单摘要，raw archive 保留完整上下文。

ETF 二级市场买卖按普通证券二级市场订单提交，使用 `business_type=S`、`trade_side=B/S`，价格精度按 Meridian `instrument_type=etf` 保留 3 位。ETF 申购/赎回不是普通买卖参数，涉及最小申赎单位、申赎清单等数据，当前 relay `/v1/orders` 未实现，`business_type=E` 会返回 `NOT_IMPLEMENTED`。

`POST /v1/orders/{gateway_order_id}/cancel` 会先读取 PostgreSQL 订单账本，只有非终态且 `leaves_qty > 0` 的订单才会写入 Redis `order.cancel`。撤单 `202 Accepted` 只表示撤单请求已提交到前置，是否撤成仍以 `order.event.gateway_status=cancelled` 为准。

`POST /v1/orders/batch` 会为每笔子订单写入本地草稿，再向 Redis `cmd.trade` 写入一条 `order.batch.submit` command。批量请求的 `202 Accepted` 不表示交易所接单或成交，最终仍以回流事件为准。

当前 `GET /v1/accounts/{account_id}/asset`、`GET /v1/accounts/{account_id}/positions`、`GET /v1/orders`、`GET /v1/fills` 和 `GET /v1/transfers` 是本地账本查询，不主动查询柜台。对应的 `POST .../refresh` 接口会向前置发送 `account.asset.query`、`account.positions.query`、`order.list.query` 或 `fill.list.query`，由 9092 同步循环把 `asset_page/position_page/order_page/fill_page` 合并回 PostgreSQL；同一 `fill_page` 中的普通成交和 `component_transfers[]` 会分别入账。

刷新回执中的 `message_id` 是查询终态关联键。`GET /v1/query-status/{origin_message_id}` 要求查询只有一个终态，成功终态必须同时满足 `status=completed`、与 action 匹配的 `result_type` 和 `chunk.is_last=true`；`failed/rejected` 返回 `state=failed`，缺少 final、结果类型不匹配或多个终态返回 `pending/invalid`。盘前初始化和盘后结算同时检查本地账本新鲜度与该终态，不能用新鲜时间戳掩盖 OC 查询失败。

`GET /v1/orders` 和 `GET /v1/fills` 不传 `trade_date/date_from/date_to/history` 时，默认按 `Asia/Shanghai` 当日过滤。历史订单和成交应使用 `/v1/history/orders`、`/v1/history/fills`，或在原查询接口显式传 `history=true`、`trade_date=YYYYMMDD`、`date_from=YYYYMMDD`、`date_to=YYYYMMDD`。订单查询优先使用 `orders.trade_date` 过滤，缺失时按东八区订单时间兜底；成交查询优先使用 `fills.trade_date`，缺失时按成交时间兜底。订单和成交查询都支持 `strategy_type`、`strategy_id`、`basket_id`、`parent_order_id`、`t0_order_group_id` 过滤。历史持仓使用 `/v1/accounts/{account_id}/positions/history`，数据来源为 `position_snapshots`；默认读取 `snapshot_type=close` 的日终持仓，可传 `snapshot_type=open` 读取盘前初始化固化的日初持仓。

订单、成交、ETF 划转、当前持仓和历史持仓查询均支持 `limit` + `cursor` 翻页。第一版 cursor 采用 offset 语义，响应中如果存在 `next_cursor`，客户端可在下一次查询带上该值继续向后读取；如果 `next_cursor` 为空，表示当前条件已到末页。`/trade` 页面默认使用每页 50 条，通过 `next_cursor` 做服务端分页。

OC 无法确定单笔委托身份或发现普通成交与订单证券、交易所、方向、业务类型不一致时，会写入 `dlq` 且 `action=adapter.data_quality`。Relay 9092 进程消费并归档 DLQ，独立统计 `dead_letters/data_quality_dead_letters`，不会把问题记录落入普通订单或成交账本。

`GET /v1/accounts/{account_id}/performance/daily?trade_date=YYYYMMDD` 返回账户日终权益和 PnL 输入汇总。该接口以指定交易日 `asset_snapshots(snapshot_type=close)` 为主记录，读取上一条 close 净资产保留兼容字段 `daily_pnl`、`return_rate` 和 `asset_change`，同时读取当日 `asset_snapshots(snapshot_type=open)` 生成 `open_net_asset`、`overnight_adjustment=open_net_asset-previous_close_net_asset`、`intraday_pnl=close_net_asset-open_net_asset`、`intraday_return=intraday_pnl/open_net_asset`、`open_snapshot_source` 和 `quality_flags`。如果缺少 open 快照，会用上一 close 兜底并标记 `missing_open_asset/open_asset_fallback`。接口还汇总同日 `position_snapshots(snapshot_type=close)` 的持仓市值、总持仓浮盈、当日持仓浮动盈亏以及 `fills` 的买入金额、卖出金额、成交额和费用。研究侧派生口径为 `realized_pnl=settled_profit`、`gross_pnl=realized_pnl+day_unrealized_pnl`、`net_pnl=gross_pnl-fee_total`。接口只读取本地账本，不主动查询柜台；如果目标日尚未写入 close 资产快照，会返回 `404 NOT_FOUND`。

`GET /v1/accounts/{account_id}/performance/contributions?trade_date=YYYYMMDD` 返回 `ContributionResult`。普通股票和 ETF 截面使用 `close_value + sell_amount - buy_amount - open_value - effective_fee`；期初价格取 Meridian 当日 `pre_close`，期末价格取 Meridian 当日 `close`，缺失时才回退券商 open/close 持仓快照并标记质量缺口。费用优先使用 `order_fee_records` 中 `fee_complete=true && association_complete=true` 的 OC 订单实际费用，同一订单无论有多少成交只扣一次；其次才使用可信成交费用或账户生效费率，缺少规则时标记 `missing_fee_rule`。

`GET /v1/accounts/{account_id}/performance/cost-ledger/preview?trade_date=YYYYMMDD` 只读试算 `performance_position_cost.v3`，`POST .../cost-ledger/rebuild` 在开启绩效写保护后保存结果。非起算日以上一 close 成本状态承接总成本，以当日券商 open 持仓作为物理数量，并查询 Meridian `adjust-factors`：数量比匹配 `ex_factor` 时保存 `corporate_action_type=quantity_adjustment`，总成本不变、单位成本随数量调整；因子存在但数量不变时保存 `price_adjustment`；无法闭合时保存 `mismatch` 并阻断。普通持仓使用 `cost_bucket=CORE`；完整 ETF T0 买入/赎回组使用 `ETF_T0:{group_id}`，买入成本与底仓隔离且赎回后日内归零。赎回名义价格不进入成本账，IOPV 退出估值与 15bp 摩擦仍由 contributions 返回。摘要增加 `t0_cost_buckets/t0_blocked_buckets/t0_buy_quantity/t0_redemption_quantity/t0_buy_amount`。

`POST /v1/accounts/{account_id}/fees/refresh` 发布 OC `fee.list.query`，只支持 OC 当前柜台交易日；`GET /v1/accounts/{account_id}/fees` 查询已落库的订单费用，支持 `trade_date/date_from/date_to/gateway_order_id/fee_complete/limit/cursor`。`fee_page.account_total_fee` 仅用于账户级核对，不参与逐订单累计；费用记录由 `account_id + fee_record_id` 幂等更新，只有完整且关联成功的订单费用可进入绩效。历史费用不由该刷新接口回补。

费用缺失不等于订单不可用。订单和成交核心字段及关联完整时，Relay 仍使用它计算数量桥、移动成本、成交额和毛收益；费用完整性按当日有成交的唯一 `gateway_order_id` 单独统计。覆盖不完整时，贡献摘要返回 `fee_required_orders/fee_covered_orders/fee_coverage_complete/fee_coverage_source`，并将净绩效标记为 provisional、等待券商交割单，不把订单标记为数据质量失败。

ETF 申赎 T0 单独返回 `strategy_type=etf_redemption_t0`。同一赎回订单的多条成交先按 `gateway_order_id` 聚合；历史买单只有在赎回前的目标委托量精确闭合赎回量时才组成推断 T0 订单组，其实际成交额作为买入成本。估算退出价值使用每条赎回成交时刻之前最近的 Meridian historical Level1 `iopv * qty`，再按服务端 `performance.etf_t0_friction_rate` 扣减综合摩擦成本。无法闭合买单、缺 IOPV 或缺价格时不返回伪精确盈亏，而是标记 `missing/estimated`。无日初/日终持仓且只有卖出的赎回成分股归为 `etf_component_transfer`，只保留成交额并从 T0 估算收益中排除，避免与 IOPV 退出价值重复计算。逆回购本金不进入普通卖出额，只有按实际占款天数计算的净息进入 `cash_management`。

`GET /v1/accounts/{account_id}/performance/trade-quality` 是只读交易质量接口，支持单日 `trade_date` 或区间 `date_from/date_to`。接口完整扫描本地订单、成交和订单费用账本，不触发 OC 查询；成交先按 `account_id + trade_date + gateway_order_id + fill_id` 去重，并在存在真实成交时排除同订单的 `relay-summary:*` 汇总成交。`trade_quality.v2` 的费用按 `trade_date + gateway_order_id` 精确关联：完整订单费用存在时覆盖该订单成交行费用且只计一次，否则回退成交费用，跨日复用订单号不会串账。`summary` 返回有实际成交订单率、完全成交率、按委托数量计算的成交率、撤单率、拒单率、未终态订单、异常订单、孤立成交、成交额和费用；`anomalies` 返回拒单、未终态、订单/成交数量不一致、成交证券/方向/业务类型错配、终态时间缺失或跨日、终态冲突、柜台错误残留和成交缺委托等可追溯明细。OC 委托时间与 Redis 事件时间存在精度差时，`terminal_before_created` 使用 5 秒容差。`gateway_order_id` 只按账户内交易日唯一处理，区间统计不会把跨交易日复用的同 ID 错误关联。

`GET /v1/accounts/{account_id}/performance/series?date_from=YYYYMMDD&date_to=YYYYMMDD&benchmark_security_id=000001.SH` 返回账户 close 净值绩效序列，并在每个交易日返回同样的日初资产、隔夜调整、日内盈亏和日内收益率字段。服务读取区间内 `asset_snapshots(snapshot_type=close)` 形成长期净值主线，按上一条 close 净资产计算兼容的单日收益，并在响应层计算 `cumulative_return`、`drawdown`、`summary.total_return` 和 `summary.max_drawdown`。绩效页面和 API Console 默认用上证指数 `000001.SH` 作为基准；如果传入其他 `benchmark_security_id`，relay 会按绩效序列中的交易日逐日读取 Meridian `bars` 的 14:55-15:00 窗口最后一条 1m close，生成 `benchmark_return`、`benchmark_cumulative_return`、`benchmark_drawdown`、`excess_return` 和 `excess_cumulative_return`，并在 `summary` 中返回基准区间收益、基准最大回撤和超额收益。该接口不主动查询柜台。

`GET /v1/accounts/{account_id}/performance/series.csv?date_from=YYYYMMDD&date_to=YYYYMMDD&benchmark_security_id=000001.SH` 复用同一绩效序列口径，返回 CSV 文件，便于研究侧脚本、表格工具或验收脚本直接下载。CSV 当前包含账户、交易日、净资产、上一 close 净资产、日初资产、隔夜调整、日内盈亏、日内收益率、close-to-close 收益、累计收益、回撤、基准标的、基准 close、基准收益、基准回撤、超额收益、已实现 PnL、总持仓浮盈、当日持仓浮盈、总/净 PnL、成交额、费用和快照时间等列。

经济净值输入层由 `000009_performance_accounting` 提供：

- `performance_fee_rules` 记录账户级、生效区间、版本化费用规则。柜台成交实际费用优先；实际费用缺失时使用生效费率估算；ETF 申赎 T0 的 15bp 摩擦成本通过 `estimated_friction_rate` 单独维护。
- `cash_ledger` 扩展为可人工维护的资金流水，支持 `external` 外部入出金、`internal_transfer` 极速/普通柜台内部划转、`settlement_income` 清算收入、`fee` 和 `adjustment`。外部净流入修正收益率，内部划转成对记录但不计入账户净收益。
- `performance_nav_baselines` 记录手工确认的日初经济净值基线，解决逆回购回款、占款释放和柜台间划转导致的前一日日终资产与当日日初资产差异。
- `performance_nav_versions` 与 `performance_nav_reconciliations` 保存滚动经济净值和 T+1 对账结果。`economic-nav/preview` 可无写权限试算，`economic-nav/rebuild` 会把同账户同交易日旧 current 版本退役并插入新 current 版本；`economic-nav/reconcile` 读取下一交易日 open 资产/持仓快照，计算 T+1 观测资产残差并可受写开关保护落库；`nav-reconciliations/confirm` 和 `/block` 可人工确认或阻断对账，并将 current economic NAV 就地推进为 `finalized/blocked`；策略归因尚未拆分完成的账户日收益写入 `pnl_components.unattributed`。
- `reverse_repo_accruals` 保存 `204001.SH` 逆回购应计结果。估算口径为 `principal=qty*100`，`gross_interest=principal*(成交年化利率/100)*实际占款天数/365`，费用优先取 OC 订单实际费用，其次取可信成交费用和账户费率规则，缺失时标记 `missing_repo_fee`。实际占款天数由 Meridian 交易日接口向后取两个交易日计算，跨周末会自然得到 3 天。

`GET /v1/accounts/{account_id}/performance/contributions?trade_date=YYYYMMDD` 使用普通证券现金流恒等式 `close_value + sell_amount - buy_amount - open_value - effective_fee`。ETF 申赎 T0 使用赎回成交时刻之前最近的 Meridian Level1 IOPV 作为估算退出价值，并扣除配置项 `performance.etf_t0_friction_rate`；多个赎回组最多 8 路并发查询 IOPV。缺少当日 open 持仓时，只在前一交易日 close 快照存在且 `open + buy - sell = close` 数量桥闭合时估算，否则返回 `pnl_status=missing`，不会把缺失持仓当作 0。接口只读，不触发 OC 查询。

`GET /v1/accounts/{account_id}/performance/economic-nav/preview?trade_date=YYYYMMDD` 的 v2.1 公式为：

```text
base_close_nav = close_visible_cash + meridian_close_position_value
principal_receivable = reverse_repo_principal - principal_already_in_visible_cash
close_economic_nav = base_close_nav + principal_receivable
account_day_pnl = close_economic_nav - open_economic_nav - external_net_flow - settlement_adjustment
daily_return = account_day_pnl / (open_economic_nav + sum(weight_i * external_flow_i))
```

其中 `open_economic_nav` 优先使用 `asset_snapshots(snapshot_type=open)`，缺失时使用上一 close 或手工 `performance_nav_baselines` 并打质量标记；`external_flow` 只读取已确认手工资金流水，用 Modified Dietz 盘中权重修正收益率分母；`settlement_adjustment` 不计入策略收益；`internal_transfer` 要求净额接近 0，否则标记 `internal_transfer_unbalanced`。逆回购优先使用已落库 `reverse_repo_accruals`，没有时按成交账本只读试算；系统比较含/不含本金两条候选 NAV 对正式证券贡献的残差，输出 `principal_treatment=embedded/separate/ambiguous`、`principal_cash_overlap`、`principal_receivable`、`resolution_residual` 和 `alternate_residual`，歧义时阻断。`estimated_net_interest/estimated_receivable` 只作诊断，不进入正式 NAV/PnL；实际净息由确认的 `income_expense` 在到账日进入 `cash_management`。剩余 `account_day_pnl` 暂记 `unattributed` 并标记 `strategy_attribution_pending`。

`GET /v1/accounts/{account_id}/performance/economic-nav/reconcile?trade_date=YYYYMMDD&observed_trade_date=YYYYMMDD` 只读预览 T+1 对账；`POST /v1/accounts/{account_id}/performance/economic-nav/reconcile` 持久化对账结果，仍由 `performance.settings_write_enabled` 控制。`observed_trade_date` 为空时会通过 Meridian 交易日接口向后取下一交易日。第一版公式为 `observed_open_assets = asset_snapshots(open).cash_total + sum(position_snapshots(open).market_value)`，再扣减 `provisional_close_economic_nav`、盘前已确认 `external_flow` 和盘前已确认 `income_expense` 后得到 `residual`；状态按配置阈值写为 `auto_completed/review_required/blocked`。

`POST /v1/accounts/{account_id}/performance/nav-reconciliations/confirm` 请求体使用 `{trade_date, operator, note?, force?, reconciliation_id?}`，确认同一 current NAV 对应的 T+1 对账记录，写入 `reviewed_by/reviewed_at/details.review`，并把 current `performance_nav_versions.status` 就地改为 `finalized`。如果对账已 blocked 或 residual 超过 warning threshold，必须显式 `force=true`。`POST /v1/accounts/{account_id}/performance/nav-reconciliations/block` 使用同类请求体，把对账和 current NAV 标记为 `blocked`。两类接口都是绩效写入口，默认生产配置关闭。

人工输入类写接口默认关闭，由配置 `performance.settings_write_enabled` 控制。生产 9092 当前应保持 `false`，仅在服务器侧明确切换配置后才能通过 `/trade#performance-settings` 或 API Console 新增费率、资金流水、日初净值和持久化逆回购估算。

研究侧 PostgreSQL 导出 view 已通过 `000006_research_performance_views` 提供：

- `research_account_daily_performance_v1`：账户日绩效、持仓汇总、成交汇总和第一版 PnL 字段。
- `research_order_fill_export_v1`：订单与成交关联明细，包含本地/柜台/交易所订单 ID、委托状态、拒单信息和成交价量。

`GET /v1/meridian/market/bars` 是 Meridian `GET /v1/market/bars` 的同源薄代理，用于 P8 账表计算、绩效序列和交易终端分钟线的行情输入。relay 不重新定义 bars 字段，也不做字段映射；响应保持 Meridian `market_bar.v1` 的 `data/meta/error` 结构。典型参数包括 `security_id`、`security_ids`、`trade_date`、`start_date`、`end_date`、`frequency`、`adjustment`、`start_time`、`end_time` 和 `limit`，具体字段约束以 Meridian 为准。例如分钟线查询可使用 `security_id=600000.SH&trade_date=20260615&frequency=1m&adjustment=none&start_time=09:30:00&end_time=15:00:00&limit=300`；批量日线使用 `security_ids=600000.SH,000001.SZ&start_date=20260615&end_date=20260615&frequency=1d&adjustment=none`。仅当没有 `start_date/end_date` 且 `trade_date` 为空或等于东八区当天时，relay 才会调用 Meridian 交易日接口取得 `previous_or_current_trading_date`；范围查询原样透传，不补入互斥的 `trade_date`。当前交易日 15:00 前默认使用 `data_scope=realtime`，15:00 后使用 `auto` 读取 Meridian 当日归档，非交易日自动读取最近交易日 historical bars。为降低读压和 benchmark 重复查询，bars 代理对标准化后同 key 请求做 2 秒短缓存、singleflight 合并和 60 秒 stale fallback；该缓存只作用于 relay 到 Meridian 的代理层，不改变响应字段结构。

`GET /v1/meridian/metadata/adjust-factors` 是 Meridian `GET /v1/metadata/adjust-factors` 的同源薄代理，用于股票/ETF 截面绩效中的除权除息、分红和 ETF 份额折算校验。relay 只透传 `security_id/security_ids/trade_date/start_date/end_date/limit` 等 Meridian 参数并保留上游 `data/meta/error` 结构，不在本项目内另建复权因子标准。

ETF PCF 三个接口同样是透明代理，不转换字符串数值，也不在 Relay 中复制 PCF schema。单日查询使用 `trade_date`，范围查询使用 `start_date/end_date`，互斥约束以 Meridian 为准。绩效贡献仅从 `etf_cash_component.v1.unit_subscribe_redeem` 读取最小申赎单位，校验赎回量是否为整数倍；缺失、上游失败或数量不匹配时输出质量标记，不猜测单位，也不使用 PCF 估算基金管理人最终清算。

`GET /v1/meridian/stream/market/bars` 直接转发 Meridian SSE 事件，不改变 event/data 内容。默认参数只用于交易时实时分钟线订阅；Relay 当前交易终端仍使用带缓存的 HTTP bars 做初始加载和定时刷新，避免在本轮适配中改变页面刷新语义。

`POST /v1/jobs/runs` 用于 Python 日流程任务将 JSON 报告写入 `job_runs`，`/v1/status` 只展示最近盘前/盘后任务摘要，不返回完整 `report_json`。

`POST /v1/settlements/snapshots` 用于盘前初始化和收盘后结算任务内部调用。请求体包含 `trade_date`、`account_ids`、`run_id`、`snapshot_type`、`source` 和可选 `dry_run`，其中 `snapshot_type` 支持 `intraday/open/close/reconcile`。`pre_open_init` 使用 `snapshot_type=open` 写入 `asset_snapshots(open)` 和 `position_snapshots(snapshot_type=open)`，用于绩效分析区分隔夜调整、公司行为后的实际持仓和日内盈亏；`post_close_settlement` 使用 `snapshot_type=close` 写入 `asset_snapshots(close)`、`position_snapshots(snapshot_type=close)` 和 `reconciliation_runs`。该接口不向前置发送查询命令；调用前应先执行资金/持仓/订单/成交 refresh 并等待账本合并。

## 后续工作

1. 增加常驻 worker 心跳状态和 DLQ 告警。
2. 如需同步涨跌停预校验，等待 Meridian 提供交易规则或涨跌停口径后再接入，不在 relay 内自建行情规则。
