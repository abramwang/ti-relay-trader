# OC v1.2 次交易日联合验收报告

验收日期：`2026-07-31 Asia/Shanghai`

验收方式：只读检查生产 Redis Stream、consumer group、PostgreSQL 原始归档和标准账本；未发送交易命令。

## 结论

OC v1.2 的订单、成交、ETF 划转、撤单拒绝和 heartbeat 主链路达到预期，7 月 30 日的实时订单上下文错位没有复现。但是 Redis 查询命令 ACK/PEL 恢复链路未达到协议完成标准，因此本轮结论为 **主链路通过，恢复链路不通过**。

## 已通过

1. 六账户 heartbeat 均包含 `redis_ready/broker_ready/order_snapshot_ready/accepting_trade_commands/accepting_cancel_commands`，最后一条样本五项均为 `true`，业务 pending 均为 0。
2. 当日实时 `order.event` 与盘后 `order_page` 有 6,898 个共同订单身份，`symbol/exchange/trade_side/business_type` 错配为 0。
3. 当日实时 `fill.event` 与盘后 `fill_page` 有 8,318 个共同成交身份，`gateway_order_id/symbol/exchange/trade_side/business_type` 错配为 0。
4. 问题账户 `307000051387` 当日 2,147 笔订单、2,444 笔成交，交易质量异常、孤立成交和 `FILL_ORDER_CONTEXT_MISMATCH` 新增均为 0。
5. 8,318 条普通成交没有零价记录，也没有 transfer 混入；297 条 ETF 成分划转独立进入 `etf_component_transfers`，其中 294 条价格为 0，分类符合协议。
6. 当日 7 条 `BROKER_CANCEL_REJECTED` 均为 `retry_safe=false`、`order_state_changed=false`，没有把原订单改成 rejected。
7. 当日没有新增其它数据质量 DLQ。

## P0：查询 PEL 未收口

OC 15:29 关停前，六个 `cmd.query` consumer group 的 `lag` 均为 0，但合计仍有 18 条 pending：

| 账户 | cmd.query pending | cmd.query lag |
| --- | ---: | ---: |
| `501000114077` | 4 | 0 |
| `314000046830` | 4 | 0 |
| `314000045768` | 1 | 0 |
| `307000051388` | 4 | 0 |
| `307000051389` | 1 | 0 |
| `307000051387` | 4 | 0 |

这些 pending 均是当日盘后资金、持仓、订单或成交查询，Relay 已收到对应的 `partial + completed` reply。以 `307000051387` 为例，四条 15:10 查询全部有最终回包，但仍停留在 PEL；订单查询已被投递两次，其余三条投递一次。

15:26:36 OC 又为六账户各发布一条 `BAD_RECOVERED_COMMAND`。DLQ 元数据存在一致的错位：

- `original_stream` 被写成 `cmd.trade`，但实际 pending 位于 `cmd.query`。
- `original_entry_id` 被写成完整的 `cmd.query` stream key，不是 Redis entry ID。
- `original_body/raw` 被写成字面量 `body`，实际 pending entry 有合法 JSON body。
- 错误 DLQ 发布后，真实 query PEL 没有被 ACK。

该现象符合 recovery/XAUTOCLAIM/XREADGROUP 返回结构解析错位，不是 Relay 查询命令格式错误。

### OC 修改要求

1. 查询输出成功写入最终 `completed + chunk.is_last=true` 后，必须 ACK 原 command entry。
2. 重启恢复时必须同时保留真实 `stream key + entry id + fields.body`，不能把 Redis 返回数组的相邻元素错当字段。
3. 已完成查询应按缓存完整重放 `partial + completed` 后 ACK；无法恢复的查询应发布关联真实 entry 的 `QUERY_INTERRUPTED` 后 ACK。
4. 只有真实 command body 无法解析时才发布 `BAD_RECOVERED_COMMAND`，DLQ 必须携带实际 entry ID 和原始 body。
5. 修复后验收六账户 `cmd.trade/cmd.query` consumer group 均为 `pending=0, lag=0`，且不再新增伪 `BAD_RECOVERED_COMMAND`。

## P1：撤单事件账户字段

当日 7 条撤单拒绝事件中：

- `routing.account_id` 是 Relay 标准账户，例如 `314000045768`。
- `payload.account_id` 是券商账户，例如 `31400004576801`。
- `adapter_context.broker_account_id` 也是券商账户。

兼容通知示例约定 `payload.account_id` 使用 Relay 标准账户，券商账户只放 `adapter_context.broker_account_id`。建议 OC 按该约定修正，避免第三方把券商账户误建为新的业务账户。

Relay 已增加防御：撤单审计优先使用 `routing.account_id`。migration `000018_normalize_cancel_attempt_accounts` 已应用到生产库，7 条当日撤单拒绝审计已迁回标准账户，其中 `314000045768` 4 条、`501000114077` 3 条。

## 尚未取得实盘样本

以下是 v1.2 既有验收项，不是本轮新增需求：

1. `CANCEL_RESPONSE_TIMEOUT` 进入需对账审计且不自动重撤。
2. `order.batch.submit.payload.failed_orders[]` 部分失败。
3. `COMMAND_OUTCOME_UNKNOWN` 跨重启保护。
4. Relay 提交的长 `gateway_order_id` 在 OC 重启后实时与查询保持一致。
5. 同 `message_id` 并发重复查询只调用一次柜台并完整重放多页 reply。

生产交易权限仍关闭，上述写链路等待券商测试环境或受控交易机会继续验收。
