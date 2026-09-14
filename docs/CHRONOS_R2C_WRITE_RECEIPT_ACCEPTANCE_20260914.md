# Chronos R2c 写回执动作验收

日期：`2026-09-14`  
环境：Relay `test`，账户及业务 ID 已脱敏  
协议：`relay.trading.v1alpha1` / `relay.stream.v1`  
SDK：`relay-sdk==0.1.34`

## 结论

Chronos 文档 `RELAY-R2C-WRITE-RECEIPT-ACTION-REQUIREMENT-20260914.md` 中的 Relay 修复项已完成：

| SDK / HTTP 动作 | 首次成功回执 | 相同命令重放 |
| --- | --- | --- |
| `submit_order()` / `POST /v1/orders` | `action=order.submit` | 动作不变，`replayed=true` |
| `submit_orders()` / `POST /v1/orders/batch` | `action=order.batch.submit` | 动作及子单身份不变，`replayed=true` |
| `cancel_order()` / `POST /v1/orders/{id}/cancel` | `action=order.cancel` | 动作不变，`replayed=true` |

三类响应均显式返回顶层 `account_id` 和 `action`。重放恢复首次发布的 `message_id`、`stream_key/stream_id`、`request_id` 和幂等键；订单及批量子单继续保留 gateway/client/idempotency 身份，撤单继续保留 `cancel_id`。

## 幂等修正

1. Relay 以 PostgreSQL 原始命令归档作为首次 payload 和发布回执的不可变事实，不再用可能被后续 OC 状态回报补全或省略字段的当前订单行判断原请求。
2. 订单回报缺少 `offset_type` 时不再覆盖首次报单值。
3. 撤单在订单终态校验前先检查已归档幂等命令，所以一次成功撤单在订单变成 `cancelled` 后仍可安全重放原回执。
4. 同一 API 进程内的撤单检查与发布串行化；进程重启后由 PostgreSQL 命令归档继续识别重放。
5. 同一幂等键但 payload 不同仍返回 `409 IDEMPOTENCY_CONFLICT`。

SDK 对成功 HTTP 响应执行动作校验。动作缺失或错误时抛出 `RelayCommandOutcomeUnknownError(code="WRITE_RECEIPT_ACTION_MISMATCH")`，重试建议要求先对账且禁止自动重报。

## 测试环境证据

使用全新业务 ID 完成真实 HTTP -> Redis -> OC -> PostgreSQL 链路验证：

测试环境 OC 在交易时段使用实时行情模拟撮合。真实撤单验收以 Meridian Level1 快照的最新盘口和证券主数据 `price_tick` 选择被动限价，并记录行情时间；不使用文档示例中的固定价格。提交前要求 OC 心跳的交易、撤单和订单快照 readiness 全部为 true。

| 场景 | HTTP / SDK 结果 | Redis 结果 |
| --- | --- | --- |
| 单笔首次提交 | `202`，`order.submit`，`replayed=false` | `cmd.trade +1` |
| 单笔相同命令重放 | `200`，动作及三类发布 ID 与首次一致 | `cmd.trade +0` |
| 两子单批量首次提交 | `202`，`order.batch.submit`，两子单 ID 完整 | `cmd.trade +1` |
| 批量相同命令重放 | `200`，动作、发布 ID、子单 ID 均不漂移 | `cmd.trade +0` |
| working 订单首次撤单 | `202`，`order.cancel`，cancel/message/stream/request ID 完整 | `cmd.trade +1` |
| 同一撤单重放 | `200`，动作及全部撤单身份与首次一致，`replayed=true` | `cmd.trade +0` |
| 单笔 payload 冲突 | `409 IDEMPOTENCY_CONFLICT` | `cmd.trade +0` |
| 撤单 cancel ID 冲突 | `409 IDEMPOTENCY_CONFLICT` | `cmd.trade +0` |

第一阶段验收中，`cmd.trade` 长度从 `9` 变为 `11`，只包含一次单笔和一次批量新发布。午间测试柜台对新订单异步返回“结算组数据没有同步”，因此等待下午 OC readiness 恢复后补做真实撤单。

`13:20:29.123+08:00` Meridian Level1 实时快照显示 `600000.SH` 买一/卖一为 `9.36/9.37`；证券主数据给出 `price_tick=0.01`、`round_lot=100`。买入被动单被测试柜台以“当前状态禁止此项操作”拒绝；随后以卖五 `9.42` 上方一档 `9.43` 提交 100 股被动卖单，订单进入 `working`，0 成交、剩余 100。首次撤单返回 `action=order.cancel`，相同撤单重放完整恢复原 `cancel_id/message_id/stream_id/request_id`，最终订单进入 `cancelled`、剩余 0。

下午窗口 `cmd.trade` 从 `11` 变为 `14`，三条新命令分别是被拒买单首次、working 卖单首次和撤单首次；下单及撤单重放均未增加 Stream 长度。working 卖单的下单、撤单幂等键在 `raw_stream_messages` 的 `cmd.trade` 角色中各有且仅有一条归档。consumer group 最终 `pending=0, lag=0`，四条运行 Stream 健康，DLQ 为 0。

非阻断审计项：该 OC 测试环境的 working 回报曾把 `13:21` 创建订单的 `accepted_at/last_updated_at` 带成 `09:15:02`，撤单终态又恢复为 `13:22:18`。标准交易日、订单 ID、命令关联和终态均正确；异常来自 OC/测试柜台时间字段，不影响本次写回执动作与幂等结论，但后续应单独验证测试回报时间单调性。

## 自动化结果

- `go test ./...`：通过。
- `go test -race ./internal/orderflow ./internal/api`：通过。
- Python SDK：`50/50` 通过，覆盖三类动作、首次/重放、批量子单和错误动作失败关闭。
- `scripts/check-python-sdk-release.py`：通过。
- `relay-sdk-0.1.34.tar.gz`：已在全新虚拟环境安装并对真实测试 API 完成安全重放。

## 发布物

```text
http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.34.tar.gz
http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.34.tar.gz.sha256
SHA-256: e12337be8c6522f18fff60ae5ce329e8b254b3bca85d02b55832b7d5ca82c4a4
```

实现提交：`592fe75`、`babef2b`。Redis Stream、OC wire schema、请求路径和订单状态机均未修改。
