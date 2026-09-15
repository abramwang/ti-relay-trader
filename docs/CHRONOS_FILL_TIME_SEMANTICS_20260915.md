# Chronos 成交时间字段语义纠正说明

日期：`2026-09-15`

## 结论

Chronos 展示和保存逐笔成交时间时必须使用 Relay SDK `Fill.matched_at`。不得使用订单对象的 `accepted_at`、`last_updated_at`、`terminal_at` 或 `created_at` 推导成交时间。

本轮华鑫 7x24 测试柜台中，平安银行订单在 `13:53 Asia/Shanghai` 提交并成交：

| 事实 | 字段 | 值 | 语义 |
| --- | --- | --- | --- |
| 下单时间 | `order.created_at` | `13:53:00` | Relay/OC 建立订单的时间 |
| 测试柜台状态时间 | `order.accepted_at` / `order.last_updated_at` | `09:46:44` | 测试柜台用于状态回报的模拟行情时钟 |
| 逐笔成交时间 | `fill.matched_at` | `13:53:00+08:00` | 策略成交、TCA 和成交展示应使用的权威时间 |

因此 `09:46:44` 不是该笔成交的发生时间，也不是 Relay 成交时间倒退。Chronos 验收报告中把订单状态字段与成交字段混用的结论需要修正。

## 字段边界

- `Fill.matched_at`：逐笔成交的标准 RFC3339 时间，带时区；成交回调、成交明细、持久化事实、滑点和 TCA 均以此为准。
- `Fill.match_timestamp`：OC 原始毫秒时间戳，主要用于审计和成交幂等，不作为消费端首选展示字段。
- `Order.created_at` / `inserted_at`：订单建立或报入时间。
- `Order.accepted_at`：柜台受理订单的状态时间。
- `Order.last_updated_at`：订单状态最后更新时间。
- `Order.terminal_at`：订单进入终态的状态时间；一笔订单可能有多笔成交，终态时间不能代表每笔成交时间。
- SSE 事件 envelope 时间：Relay 收到和发布事件的时间，不等于业务成交时间。

订单与成交通过 `account_id + trade_date + gateway_order_id` 关联；每笔成交保留独立的 `fill_id` 和 `matched_at`。多笔成交不得共用订单终态时间。

## 环境边界

本次 `09:46:44` 与 `13:53:00` 并存是当前华鑫 7x24 测试柜台的模拟撮合语义。测试柜台可在非正常 A 股时段运行，并可能用回放行情时钟生成部分订单状态字段。

生产环境仍按正常 A 股交易时段运行，目前没有证据表明正式柜台存在同类时间差异。无论环境如何，消费端字段规则保持一致：成交只读 `fill.matched_at`，订单状态只读各自的订单时间字段。

## Chronos 整改要求

1. 所有成交列表、成交回调、执行事实和 TCA 输入均读取 `Fill.matched_at`。
2. 不从 `Order.last_updated_at`、`terminal_at` 或 `accepted_at` 合成成交时间。
3. 同一订单有多笔成交时，逐笔保存各自的 `fill_id + matched_at`。
4. `matched_at` 缺失时将成交事实视为不完整并告警，不静默回退到订单时间。
5. 将 `RELAY-SDK-0.1.36-ORDER-ENTRY-CONSUMER-ACCEPTANCE-20260915.md` 中“成交发生在 13:53，但订单时间倒退”的表述改为“成交时间为 13:53；09:46:44 是测试柜台订单状态时钟”。

Chronos 当前 `relay_projection.py` 的 `_project_fill()` 已优先读取 `matched_at`，应重点检查验收取证、页面展示和后续持久化链路是否绕过该投影。

## Relay 页面口径

Relay 交易终端按以下方式展示：

- `当日成交` 表和订单详情中的成交执行记录使用 `fill.matched_at`。
- K 线成交标记使用 `fill.matched_at`；未成交委托标记才使用订单建立时间。
- 订单轨迹继续展示订单生命周期时间，并在测试环境标注“测试柜台状态时钟”，不将其命名为成交时间。
- 浏览器格式化显式固定 `Asia/Shanghai`，不受客户端或容器本地时区影响。

## 验收标准

- 平安银行本轮成交在 Chronos 成交列表和持久化事实中显示 `13:53:00`，不是 `09:46:44`。
- 成交回调时间、全量成交查询时间和重启恢复后的成交事实一致。
- 多笔部分成交按各自 `matched_at` 排序，订单 `terminal_at` 不覆盖逐笔时间。
- Relay 与 Chronos 页面中所有名为“成交时间”的列均来自 `fill.matched_at`。
