# OC 测试环境批量订单回报反馈（2026-09-06）

## 结论

测试账户 `00030484` 的批量命令、OC 接单和实时 `order.event` 均已到达 Relay。终端未显示回报不是消息丢失，而是同一订单的后续 OC 回报发生交易日漂移，叠加非交易日页面默认查询最近交易日所致。

## 复现证据

- Relay 请求时间：`2026-09-06 20:16:59+08:00`。
- 批量命令：`msg-order-batch-submit-1788697019694607653-5`。
- 子单：`manual-gateway-1788696109389-4-e8b21d11`，`600000.SH`，买入 `1,000` 股，限价 `9.24`。
- OC reply：`status=accepted`、`accepted_count=1`、`failed_count=0`。
- 第一条 `order.event`：`trade_date=20260906`，`origin_message_id` 正确关联批量命令。
- 后续两条 `order.event`：`trade_date=20450624`，状态依次为 `accepted/working`；`origin_message_id` 未继续关联原批量命令。
- 三条事件的 `order_id=1680001`、`order_stream_id=110018000000001`、`gateway_order_id` 完全相同，业务上是同一笔订单。

原始 Redis 消息已经保存在测试库 `raw_stream_messages`，对应 event stream ID 为：

- `1788697019732-0`
- `1788697019734-0`
- `1788697019745-0`

## 请 OC 确认

1. 同一订单生命周期内，标准字段 `payload.trade_date` 必须保持稳定；如果 `20450624` 是模拟柜台内部业务日，请放入 `adapter_context.broker_trade_date`，不要覆盖 Relay 标准交易日。
2. 本地发起订单的后续 `order.event` 应持续携带原始 `origin_message_id`；至少应保证 Relay 可通过命令 ID完整回查全部子单状态。
3. `gateway_order_id/order_id/order_stream_id` 当前保持稳定，符合预期。
4. `order.batch.submit` 的 `accepted/order_action_receipt` 已足以表示命令接收完成，无需为此新增查询式 final chunk；订单最终状态仍由 `order.event` 驱动。

## Relay 已加保护

- 原始报文不改写，继续作为审计证据保存。
- 当订单回报的交易日与订单自身时间相差超过 7 天时，标准账本使用订单时间归一化交易日，并在 `adapter_context` 保存 `relay_reported_trade_date` 等标记。
- 交易类 `accepted/order_action_receipt` 在命令状态接口中返回 `state=accepted, terminal=true, success=true`。
- 批量页面按 `message_id` 自动读取子单账本，并在非交易日提交后切换到实际提交日期。

该保护用于避免异常日期拆分订单身份，OC 仍应修复标准字段在同一订单上的不一致。
