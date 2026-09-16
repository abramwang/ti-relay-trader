# Chronos TEST 跨日订单需求响应

日期：`2026-09-16`

## 实现状态

| 项目 | 状态 | Relay 处理 |
| --- | --- | --- |
| R1 撤单尝试分页账本 | 已实现 | `GET /v1/order-cancel-attempts`；SDK 提供 page/item 迭代器 |
| R2 柜台会话身份 | Relay 已兼容，等待 OC | 可选 `counter_session_id` 已进入 readiness、Order、OrderCancelAttempt |
| R3 TEST 遗留状态处置 | 失败关闭 | 等待 R2 真实证据后实现；不伪造 cancelled/rejected |
| R4 filled 原子投影 | 已实现 | 入库及 GET 投影均要求数量闭合，否则降为 working/partially_filled |

## R1 查询契约

`GET /v1/order-cancel-attempts` 要求 `account_id`，支持 `trade_date` 或
`date_from/date_to`、`gateway_order_id`、`status`、`limit<=500` 和 offset cursor。
结果按 `occurred_at DESC, cancel_attempt_pk DESC` 排序，空 `next_cursor` 表示完成。

SDK `0.1.37` 提供：

```python
attempts = list(client.iter_cancel_attempts(
    trade_date="20260916",
    status="rejected",
    page_size=500,
))

pages = list(client.iter_cancel_attempt_pages(
    trade_date="20260916",
    status="rejected",
))
```

返回保留 `attempt_id`、三个订单 ID、命令关联 ID、错误码与消息、重试/状态变更/对账标志、
权威发生时间、Stream 身份和 adapter 审计上下文。

## 安全边界

`ORDER_NOT_FOUND` 仍只表示撤单失败，不修改原订单。`counter_session_id` 缺失时无法证明跨结算
周期的命名空间变化，因此不会开放人工 resolution。华鑫 7x24 TEST 柜台每日四次结算，Relay
不会把该身份按自然日生成，也不会根据配置时间表猜测变化；只接受 OC 提供的权威不透明 ID。
该门禁仅适用于 `environment=test + counter_mode=huaxin_7x24_test`，生产正常 A 股逻辑不变。OC 字段要求见
`docs/OC_COUNTER_SESSION_ID_REQUIREMENT_20260916.md`。
