# OC 测试柜台跨结算周期订单可靠性契约

日期：`2026-09-16`

## 背景

Chronos 在 TEST 柜台对前一日 20 笔 `working` 订单逐笔撤单，当前健康 OC 均返回
`ORDER_NOT_FOUND`。这证明当前柜台无法解析这些 ID，但不能证明订单已撤销，也不能区分结算周期
切换、日切清理、OC 重启、柜台会话切换或映射丢失。Relay 因此保留原订单状态，并把拒撤写入
独立撤单尝试账本。

当前华鑫 7x24 TEST 柜台每日有四个独立运行/结算周期，约在 `05:40`、`11:40`、`17:40`、
`23:40` 结束。因此会话边界不能等同于自然日或交易日；同一天内也可能发生多次订单 ID
命名空间切换。

本轮请 OC 一并完成三项 P0：稳定提供柜台会话身份、补齐撤单结果语义、保证成交终态字段原子
一致。三项均为现有 `relay.stream.v1` 的增量字段或约束，不改变 Stream 名称、订单主键和命令
回执的既有含义。

## 任务一：柜台会话身份

请新增不透明字符串 `counter_session_id`，建议最长 128 字符。该值表示柜台订单 ID 命名空间的
一次有效代际，不是日期字段，Relay 不解析其格式。字段应在以下消息中保持一致：

1. `heartbeat.payload.counter_session_id`
2. `order.event.payload.counter_session_id`
3. `order_page.items[].counter_session_id`
4. `order.cancel.event.payload.counter_session_id`
5. `order.cancel` 的 accepted/rejected reply payload（能够关联原订单时）

Relay 已兼容上述字段并在 readiness、订单和撤单尝试中透传；字段缺失时不猜测。

## 稳定性规则

1. 同一柜台订单 ID 命名空间内，断线重连、Redis 重连和 OC 进程重启不得改变 ID。
2. 柜台任一结算周期结束后，只有订单映射被清空或订单 ID 命名空间确实重建时才生成新 ID；
   不能固定按自然日生成，也不能由 Relay 根据时间表猜测。
3. ID 变化必须先出现在 heartbeat，再接受新会话的交易命令。
4. 历史 `order_page` 若来自不同会话，应返回订单实际所属会话 ID，不能统一覆盖成当前值。
5. OC 无法确认历史订单所属会话时应留空，并保留原始柜台上下文；不得填当前会话 ID。
6. 生产正式柜台继续遵循正常 A 股会话和自身订单映射生命周期，不采用 TEST 的四段时间表。
   同一字段可以作为生产审计信息，但不得改变生产准入、订单终态或自动处置逻辑。

## 任务二：撤单结果语义

`order.cancel` 的最终 reply 和对应撤单事件应提供以下标准字段，不能仅返回错误码或文本：

1. `cancel_status`：撤单结果，至少区分 `accepted`、`rejected`。
2. `retry_safe`：同一订单身份下是否适合自动重试。
3. `order_state_changed`：本次撤单是否已改变原订单状态。
4. `reconciliation_required`：是否需要后续查询或人工核对。
5. `occurred_at`：OC 形成该结果的东八区时间，使用带 `+08:00` 的 RFC3339 时间。
6. `counter_session_id`：处理本次撤单请求时的当前柜台会话身份。

对“当前会话找不到上一会话订单”的 `ORDER_NOT_FOUND`，推荐明确返回：

```json
{
  "code": "ORDER_NOT_FOUND",
  "cancel_status": "rejected",
  "retry_safe": false,
  "order_state_changed": false,
  "reconciliation_required": true,
  "occurred_at": "2026-09-16T11:30:00+08:00",
  "counter_session_id": "opaque-current-session-id"
}
```

该结果只证明本次撤单未找到可操作订单，不证明原订单已撤销或已终态。OC 不应据此伪造原订单
状态。`counter_session_id` 表示本次撤单实际使用的命名空间；原订单所属会话仍由订单自身字段表达。

## 任务三：成交终态原子一致

OC 在 `order.event` 和 `order_page.items[]` 中输出 `status=filled` 时，同一条消息必须同时满足：

1. `cum_filled_qty == order_qty`
2. `leaves_qty == 0`
3. `is_terminal == true`

若累计成交量小于委托量，状态应保持 `partially_filled` 或仍可工作的等价状态，不能先发布
`filled` 再通过后续消息补数量。订单查询与实时事件必须遵循同一规则。Relay 会继续保留读写两侧的
防御性归一化，但该保护不能替代 OC 的源头一致性。

历史消息和升级前建立的身份映射无法补齐时允许字段为空，须保留原始柜台上下文，不能推测或
伪造；新版本部署后创建的订单及撤单请求必须满足本契约。

## Relay 处置门禁

在 OC 完成上述契约并经过一轮 TEST 验证前，Relay 不提供遗留订单终态化操作。后续人工
`orphaned` 处置至少要求：`environment=test`、`counter_mode=huaxin_7x24_test`、原订单和拒撤均有
非空会话 ID、两者不同、完整分页证据、操作员及原因、幂等处置 ID。生产环境即使观察到会话
ID 变化，也始终不得仅凭 `ORDER_NOT_FOUND` 自动改变订单状态。

## 验收样例

1. 同一会话内 OC 重启后，订单查询/事件/撤单结果的 `counter_session_id` 不变。
2. TEST 某次结算确实清理映射后 heartbeat 使用新 ID，同一自然日上一周期订单仍保留旧 ID。
3. 对上一结算周期订单拒撤时，撤单尝试携带当前会话 ID；Relay 可证明其与原订单会话不同。
4. 当前会话 ID 缺失或相同，Relay 的人工处置必须失败关闭。
5. TEST 连续两个结算周期若 OC 保留同一订单命名空间，ID 保持不变，Relay 不制造切换事实。
6. 生产账户即使携带该字段，正常 A 股下单准入和订单状态机行为与升级前一致。
7. `ORDER_NOT_FOUND` 的 reply/事件完整携带六个撤单语义字段，且不会改变原订单状态。
8. 当前会话撤单成功、业务拒绝和订单不存在三类结果均能由结构化字段区分，不依赖文本解析。
9. 全量检查实时事件和主动订单查询，不存在 `status=filled` 但数量未满、残量非零或非终态的记录。
10. OC 重启后，新订单的会话身份、撤单语义和成交终态约束仍保持一致。
