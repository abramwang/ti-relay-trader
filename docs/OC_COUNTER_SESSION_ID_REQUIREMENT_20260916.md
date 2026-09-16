# OC 跨日订单会话身份契约

日期：`2026-09-16`

## 背景

Chronos 在 TEST 柜台对前一日 20 笔 `working` 订单逐笔撤单，当前健康 OC 均返回
`ORDER_NOT_FOUND`。这证明当前柜台无法解析这些 ID，但不能证明订单已撤销，也不能区分日切清理、
OC 重启、柜台会话切换或映射丢失。Relay 因此保留原订单状态，并把拒撤写入独立撤单尝试账本。

## OC 必需字段

请新增不透明字符串 `counter_session_id`，建议最长 128 字符，并在以下消息中保持一致：

1. `heartbeat.payload.counter_session_id`
2. `order.event.payload.counter_session_id`
3. `order_page.items[].counter_session_id`
4. `order.cancel.event.payload.counter_session_id`
5. `order.cancel` 的 accepted/rejected reply payload（能够关联原订单时）

Relay 已兼容上述字段并在 readiness、订单和撤单尝试中透传；字段缺失时不猜测。

## 稳定性规则

1. 同一柜台订单 ID 命名空间内，断线重连、Redis 重连和 OC 进程重启不得改变 ID。
2. 柜台日切并清空前一交易日订单映射，或订单 ID 命名空间确实重建时，必须生成新 ID。
3. ID 变化必须先出现在 heartbeat，再接受新会话的交易命令。
4. 历史 `order_page` 若来自不同会话，应返回订单实际所属会话 ID，不能统一覆盖成当前值。
5. OC 无法确认历史订单所属会话时应留空，并保留原始柜台上下文；不得填当前会话 ID。

## Relay 处置门禁

在 OC 完成上述契约并经过一轮 TEST 验证前，Relay 不提供遗留订单终态化操作。后续人工
`orphaned` 处置至少要求：TEST 环境、原订单和拒撤均有非空会话 ID、两者不同、完整分页证据、
操作员及原因、幂等处置 ID。生产环境始终不得仅凭 `ORDER_NOT_FOUND` 自动改变订单状态。

## 验收样例

1. 同一会话内 OC 重启后，订单查询/事件/撤单结果的 `counter_session_id` 不变。
2. 日切清理映射后 heartbeat 使用新 ID，前一日订单仍保留旧 ID。
3. 对前一日订单拒撤时，撤单尝试携带当前会话 ID；Relay 可证明其与原订单会话不同。
4. 当前会话 ID 缺失或相同，Relay 的人工处置必须失败关闭。

