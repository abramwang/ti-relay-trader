# OC 华鑫测试环境订单回报验收

日期：`2026-09-06`  
环境：Relay `test`、账户 `00030484`、协议 `relay.stream.v1`  
对应 OC 提交：`f4a683a`、`5eaea72`

## 结论

OC 的交易日稳定和原命令关联修复已通过在线测试。Relay 无需修改 Redis Stream、命令、回报终态或订单主键协议，当前通用异常日期保护继续保留。验证期间 raw Stream 报文未被 Relay 改写。

已通过：

1. 两组 `order.batch.submit` 均只收到一条 `accepted/order_action_receipt`，`accepted_count=2`，与两个子单一致。
2. 第二组两个子单均形成 `accepted -> working -> cancelled` 生命周期；每条 `order.event.origin_message_id` 都持续等于原批量命令 `message_id`。
3. 每个子单的标准 `payload.trade_date` 全程为 `20260906`。
4. 柜台继续返回 `20450624` 时，标准日期未变化；raw 报文只在 `adapter_context.broker_trade_date=20450624` 和 `broker_trade_date_outlier=true` 保留异常原值。
5. `gateway_order_id`、`order_id`、`order_stream_id` 在 accepted、working 和 cancelled 事件中保持稳定。
6. 主动执行 `order.list.query` 后，两个子单仍为 `trade_date=2026-09-06`，订单 ID 与实时事件一致，账本中的原始提交 `origin_message_id/request_id` 未被查询回包覆盖。
7. `cmd.trade` 和 `cmd.query` consumer group 最终均为 `pending=0, lag=0`。
8. 本轮三笔 working 测试委托均已撤成 `cancelled`，没有遗留可执行委托。

## 样本

主验收批次：

- `message_id=msg-order-batch-submit-1788703678645392438-5`
- 子单 C：`gateway_order_id=ocfix-gateway-c-20260906220758-679484`，`order_id=1680003`，`order_stream_id=110018000000068`
- 子单 D：`gateway_order_id=ocfix-gateway-d-20260906220758-679484`，`order_id=1680004`，`order_stream_id=110018000000069`
- 查询命令：`msg-orders-query-1788703715239828431-8`，唯一终态为 `completed`，`success=true`

补充批次 `msg-order-batch-submit-1788703572706620407-1` 中，一笔子单进入 working，另一笔被测试柜台正常拒绝。该批次同样验证了 accepted 回执、标准日期和命令关联；拒绝子单只有一条订单事件，因此不作为“两条生命周期事件”主样本。

## 未覆盖项

尚未执行“新订单建立后重启 OC，再查询或接收后续事件”的恢复验收。OC 运行在 Relay 主机之外，本轮不主动中断外部进程。当前结果已经证明正常日切异常日期路径不会再清空命令上下文；进程重启后的 `order-identity:v1` 持久化恢复仍需在下一次可协调的测试 OC 重启窗口复核。

## Relay 后续口径

1. 保留通用 7 天异常日期归一化和 raw 审计，不针对 `20450624` 写死补丁。
2. 持续告警同一账户/订单标准交易日变化、本地下单事件缺失 `origin_message_id`、标准日期与订单时间相差超过 7 天。
3. `accepted/order_action_receipt` 继续作为交易命令接收终态；订单业务状态继续完全由 `order.event` 驱动，不等待额外 final chunk。

