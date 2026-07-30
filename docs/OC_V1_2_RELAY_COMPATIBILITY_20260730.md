# OC v1.2 Relay 兼容实施记录

日期：`2026-07-30`

依据：

- `docs/THIRD_PARTY_INTEGRATION_GUIDE.md` v1.2
- `docs/RELAY_COMPATIBILITY_NOTICE_20260730.md`

## 已实现

1. 保留 `event_type` 与 `event_name`，识别 `order.cancel.event/order.cancel.rejected`。
2. 新增 `order_cancel_attempts`，独立记录撤单接受、明确拒绝、响应超时和结果未知；这些结果不修改原订单状态或下单拒绝原因。
3. `CANCEL_RESPONSE_TIMEOUT` DLQ 自动标记 `reconciliation_required=true`。
4. `order.batch.submit.payload.failed_orders[]` 按 `index/gateway_order_id` 逐笔回写失败子单。
5. `BROKER_NOT_READY` 与 `COMMAND_OUTCOME_UNKNOWN` 均不推断为业务拒单；后者要求先查询对账。
6. 未知事件先写入 `raw_stream_messages`，记录 unsupported 告警后继续推进 checkpoint，不阻塞后续订单和成交。
7. `/v1/operations/status` 增加 `redis_ready/broker_ready/order_snapshot_ready/accepting_trade_commands/accepting_cancel_commands`，`/operations` 展示普通交易和撤单是否可用。
8. SSE 新增 `order.cancel.rejected`；`relay-sdk 0.1.21` 新增 `on_cancel_rejected()/watch_cancel_rejections()` 及结果未知相关异常类型。

## 数据库

`000017_oc_v1_2_cancel_attempts` 已于 `2026-07-30 23:06:26 Asia/Shanghai` 应用到当前生产账本。迁移只新增表和索引，不改写历史订单。

## 已验证

1. `go test ./...` 全部通过。
2. SDK release check 通过，mock 单测 `16/16`。
3. `relay-sdk-0.1.21.tar.gz` 与 SHA256 已由 9092 返回 `200`。
4. 9092 重启后 Redis、PostgreSQL、Meridian、订单服务和事件流均为 `ok`。
5. 生产仍为 6 个只读账户、`trading_enabled=0`。
6. 24 条输出 stream 当前 lag 为 0。

## 待联合验证

当前为收盘后，最新 OC 心跳停在 `15:15 Asia/Shanghai`，仍是升级前字段，因此新增 readiness 字段尚无在线样本。下一交易窗口继续验证：

1. OC v1.2 heartbeat 五个 readiness 字段。
2. 不可撤订单产生 `order.cancel.rejected` 且原订单状态不变。
3. 撤单响应超时进入需对账审计且不自动重撤。
4. 长 `gateway_order_id` 在 OC 重启后实时/查询身份保持一致。
5. 重复多页查询完整重放 `partial + completed`，consumer group PEL 清零。
6. `307000051387` 的实时订单上下文证券错位不再复现。

联合验收完成前，生产交易开关继续保持关闭。
