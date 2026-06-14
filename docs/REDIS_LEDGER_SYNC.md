# Redis Stream 到 PostgreSQL 账本同步

更新时间：`2026-06-14`

## 当前状态

已新增 `relayctl ledger-sync`，用于把前置服务输出的 Redis Stream 消息写入 PostgreSQL 账本。

`2026-06-14` 起，9092 docs/api 模式也会在本地配置包含 PostgreSQL 和 Redis 时启动轻量后台同步循环，持续消费测试 Redis `reply/event` 并更新本地账本。同步循环现在支持 PostgreSQL `stream_checkpoints` 位点表：如果存在 checkpoint，就从对应 stream 的 `last_stream_id` 继续读取；如果不存在，则按配置起点从 `0` 追赶历史。

同日正式 `worker` 模式已接入同一套同步循环，可持续消费 `reply/event/hb/dlq`，并将每条 output stream 的消费位点、处理计数和最近错误摘要写入 `stream_checkpoints`。生产化建议将持续同步放到 worker 进程，9092 API 进程专注处理 HTTP 请求。

同日新增自动资金持仓刷新：当同步循环处理到 `order.event` 或 `fill.event` 后，会按账户调度 `account.asset.query` 和 `account.positions.query`。调度器默认 2 秒合并、20 秒冷却，只向前置写入查询命令，后续仍由 `asset_page/position_page` reply 合并到 PostgreSQL。

当前 reply 合并范围已覆盖资金、持仓、订单和成交查询结果：`asset_page` 写入 `asset_snapshots`，`position_page` 写入 `positions`，`order_page` upsert `orders`，`fill_page` 幂等写入 `fills`。下单类 `rejected/failed` reply 会更新对应草稿订单为 `rejected`，并把前置/柜台错误抽取到 `reject_code`、`reject_message` 和 `adapter_context.relay_error_message`，便于 `/trade` 和策略端排查拒单原因。

首批同步范围：

1. `reply`：完整归档到 `raw_stream_messages`，并合并 `asset_page/position_page/order_page/fill_page`；下单类 `rejected/failed` reply 会回写订单终态和错误信息。
2. `event`：完整归档到 `raw_stream_messages`。
3. `order.event`：payload 字段足够时写入 `accounts`、`orders`、`order_events`。
4. `fill.event`：payload 字段足够时写入 `accounts`、`fills`。

`relayctl ledger-sync` 命令是受控批处理入口，不会写入 `cmd.trade` 或 `cmd.query`，也不会移动 consumer group 位点。自动资金持仓刷新在 9092 docs/api 轻量后台同步循环和正式 worker 中均可启用。

`hb/dlq` 当前由 worker 原始归档；心跳状态合并到 `gateways`、DLQ 告警和处置状态仍是后续任务。

## Stream 命名与方向

协议版本固定为 `relay.stream.v1`。每个账户路由必须解析到唯一 `stream_prefix`：

```text
relay:{env}:v1:{broker_id}:{gateway_id}
```

当前测试账户示例：

```text
relay:prod:v1:huaxin:00030484
```

relay 不从 stream key 推断真实生产资金环境，`env=prod` 只是前置协议命名的一部分。真实实盘和测试环境隔离以部署配置、Redis 连接和账户路由为准。

完整 stream：

| Role | Stream | 方向 | 是否写账本 | 说明 |
| --- | --- | --- | --- | --- |
| `cmd.trade` | `{prefix}:cmd.trade` | relay -> 前置 | 归档 command raw | 下单、批量下单、撤单 |
| `cmd.query` | `{prefix}:cmd.query` | relay -> 前置 | 归档 command raw | 资金、持仓、订单、成交刷新查询 |
| `reply` | `{prefix}:reply` | 前置 -> relay | 是 | command 回包和分页查询结果 |
| `event` | `{prefix}:event` | 前置 -> relay | 是 | 订单/成交状态持续推送 |
| `hb` | `{prefix}:hb` | 前置 -> relay | 原始归档 | 心跳状态，后续合并到 gateway 状态 |
| `dlq` | `{prefix}:dlq` | 前置 -> relay | 原始归档 | 死信消息，后续做告警和处置状态 |

每条 entry 只使用一个 field：

```text
body = <JSON string>
```

relay 的探测、同步和归档逻辑都假设 `body` 是唯一业务载荷字段。额外 Redis field 不会作为标准字段入账。

## Command Envelope

relay 写入 `cmd.trade` 或 `cmd.query` 前会生成 command envelope：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "command",
  "message_id": "msg-order-submit-...",
  "request_id": "relay-...",
  "correlation_id": "relay-...",
  "idempotency_key": "order:00030484:gw-...",
  "action": "order.submit",
  "payload": {},
  "sent_at": "2026-06-14T10:30:00.123+08:00"
}
```

字段口径：

| 字段 | 生成方 | 说明 |
| --- | --- | --- |
| `message_id` | relay | 单条 Redis command 消息 ID，和 Redis `stream_id` 不是一回事 |
| `request_id` | HTTP middleware 或 orderflow | 本次 HTTP/API 请求 ID，用于接口排查 |
| `correlation_id` | relay | 当前与 `request_id` 相同，供前置 reply/event 关联 |
| `idempotency_key` | 调用方或 relay | 交易命令业务幂等键；查询刷新也会生成查询幂等键方便追踪 |
| `action` | relay | 决定写入 `cmd.trade` 或 `cmd.query` |
| `payload` | relay | 标准交易请求或查询请求 |
| `sent_at` | relay | `Asia/Shanghai` RFC3339Nano 字符串 |

action 到 stream 的映射：

| Action | Stream role |
| --- | --- |
| `order.submit` | `cmd.trade` |
| `order.batch.submit` | `cmd.trade` |
| `order.cancel` | `cmd.trade` |
| `account.asset.query` | `cmd.query` |
| `account.positions.query` | `cmd.query` |
| `order.list.query` | `cmd.query` |
| `fill.list.query` | `cmd.query` |

## Reply/Event 合并规则

所有 output entry 的处理顺序相同：

1. 解析 `body`。解析失败时仍归档 `body_text` 和 `parse_error`。
2. 写入 `raw_stream_messages`，唯一键为 `stream_key + stream_id`。
3. 根据 `message_type/action/result_type/event_type` 合并标准账表。
4. 成功处理后更新 `stream_checkpoints`。

`reply` 合并：

| Reply 类型 | 判定字段 | 落盘目标 |
| --- | --- | --- |
| 资金页 | `action=account.asset.query` 或 `result_type=asset_page` | `asset_snapshots(snapshot_type=intraday)` |
| 持仓页 | `action=account.positions.query` 或 `result_type=position_page` | `positions` 当前持仓 |
| 委托页 | `action=order.list.query` 或 `result_type=order_page` | `orders`，并在需要时补汇总成交 |
| 成交页 | `action=fill.list.query` 或 `result_type=fill_page` | `fills` |
| 下单拒绝/失败 | `status=rejected/failed` 且 action 为 `order.submit/order.batch.submit` | 更新草稿订单为 `rejected`，保留柜台错误 |

`event` 合并：

| Event | 必需字段 | 落盘目标 |
| --- | --- | --- |
| `order.event` | `account_id`、`gateway_order_id`、数量/状态字段；若没有本地草稿，还需要 `trade_side/business_type` | `orders` upsert 或状态更新、`order_events` 追加 |
| `fill.event` | `account_id`、`gateway_order_id`、`fill_id` 或 `adapter_context.match_stream_id`、价格、数量、标的、方向 | `fills` |

订单事件按整单快照处理。前置如果同一订单状态变化，会推送完整订单信息，relay 对 `orders(account_id, gateway_order_id)` 做 upsert。终态保护在 SQL 层实现：已 `filled/cancelled/rejected` 的订单不会被后续非终态事件回退，`terminal_at` 也不会被非终态覆盖。

如果订单事件显示已全成，但缺少完整逐笔成交，relay 会生成一条 `fill_id=relay-summary:<gateway_order_id>` 的汇总成交，`adapter_context.relay_synthesized=true`。这只用于保持订单/成交数量口径一致，不代表柜台真实逐笔成交。

## Checkpoint 与重放

`stream_checkpoints` 每条 output stream 一行：

| 字段 | 说明 |
| --- | --- |
| `stream_key` | Redis stream 完整 key |
| `stream_role` | `reply/event/hb/dlq` |
| `last_stream_id` | 最近已处理 Redis Stream ID |
| `last_seen_at` | 最近看到消息的时间 |
| `last_processed_at` | 最近成功推进处理的时间 |
| `processed_count` | 累计处理数 |
| `error_count` | 累计错误数 |
| `last_error` | 最近错误摘要，不包含敏感连接串 |

同步循环读取 checkpoint 的规则：

1. 表中有 `stream_key` 时，从 `last_stream_id` 继续 `XREAD`。
2. 没有 checkpoint 时，从配置起点读取；当前默认从 `0` 追赶历史。
3. 重新消费同一段消息不会重复入账，因为 `raw_stream_messages`、`order_events`、`fills` 等表都有唯一约束。
4. 如果需要手动回放，可用 `relayctl ledger-sync -from <stream_id>` 读取指定区间；不要手工改 Redis 消费组位点。

当前没有使用 Redis consumer group。位点以 PostgreSQL 为准，这样 API/docs 轻量同步循环和 worker 可以使用同一套恢复逻辑。

## 命令入口

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" \
REDIS_URL="$REDIS_URL" \
go run ./cmd/relayctl ledger-sync \
  -stream-prefix relay:prod:v1:huaxin:00030484 \
  -count 20 \
  -timeout 30s
```

使用本地配置文件：

```bash
go run ./cmd/relayctl ledger-sync -config config/relay.local.yaml -count 20
```

常用参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-config` | `RELAY_CONFIG_PATH` | relay YAML 配置文件 |
| `-database-url` | `RELAY_DATABASE_URL` | PostgreSQL DSN 覆盖值 |
| `-stream-prefix` | 空 | 指定单个 stream prefix |
| `-from` | `0` | Redis Stream 起始 ID，`0` 表示从历史开始读取 |
| `-count` | `100` | 每条 stream 最多读取消息数 |
| `-roles` | `reply,event` | 要同步的输出 stream role |
| `-block` | `0` | 可选 XREAD block 时长 |
| `-timeout` | `30s` | 本次同步整体超时 |

## 输出说明

输出为 JSON，Redis URL 会脱敏，PostgreSQL DSN 不会打印。

核心计数字段：

| 字段 | 说明 |
| --- | --- |
| `seen` | 本次读取到的消息数 |
| `archived` | 已归档到 `raw_stream_messages` 的消息数 |
| `accounts` | 写入或更新账户数 |
| `orders` | 写入或更新订单数 |
| `order_events` | 追加订单事件数 |
| `fills` | 写入成交流水数 |
| `replies` | 处理的 reply 数 |
| `skipped` | 原始消息已归档但业务账本未写入的消息数 |
| `parse_errors` | body 无法解析为 JSON 的消息数 |
| `ledger_errors` | PostgreSQL 写入失败数 |
| `unsupported` | 暂不支持的消息类型数 |

## 真实联调结果

已使用内网 Redis 和 PostgreSQL 跑通真实同步：

1. 从 `relay:prod:v1:huaxin:00030484:reply` 读取 10 条并归档。
2. 从 `relay:prod:v1:huaxin:00030484:event` 读取 10 条并归档。
3. PostgreSQL `raw_stream_messages` 当前可看到 `reply` 和 `event` 归档记录。
4. 通过 API 模式 `POST /v1/orders` 发送一笔测试单后，回流同步读取到 1 条 reply、3 条 order.event 和 1 条 fill.event。
5. 该测试单已从订单草稿更新到 `filled/filled`，并落盘 3 条订单事件、1 条成交和 6 条原始 stream 消息。
6. 通过 API 模式 `POST /v1/accounts/00030484/orders/refresh` 和 `/fills/refresh` 向测试前置发送 `order.list.query/fill.list.query`，Redis reply 返回 `completed` 且 `payload.items` 为空；非空 `order_page/fill_page` 合并逻辑已由单元测试覆盖，等待测试前置产生可查询记录后继续做实盘样例校验。

## 字段缺口

当前观察到的 `order.event` payload 已包含：

1. `gateway_order_id`
2. `account_id`
3. `symbol`
4. `exchange`
5. `order_qty`
6. `cum_filled_qty`
7. `leaves_qty`
8. `limit_price`
9. `gateway_status`
10. `is_terminal`

但缺少：

1. `trade_side`
2. `business_type`

建议前置程序在 `order.event.payload` 中补充这两个字段。这样 relay 可以在没有本地订单草稿的情况下，仅凭事件流重建订单账本。

relay 的正式下单 API 已在写入 Redis `cmd.trade` 前先写入订单草稿；当事件回流时，即使前置事件字段不全，也可以基于本地草稿完成状态更新并追加 `order_events`。历史无草稿事件仍需要前置补字段后才能单独重建订单主表。

## 幂等策略

1. `raw_stream_messages` 以 `stream_key + stream_id` 去重。
2. `orders` 以 `account_id + gateway_order_id` upsert。
3. `order_events` 以 `account_id + event_id` 或 `stream_key + stream_id` 去重。
4. `fills` 以 `account_id + gateway_order_id + fill_id` 或 fallback 唯一键去重；`fill_id/match_stream_id` 只要求在订单作用域内稳定。
5. `stream_checkpoints` 以 `stream_key` 去重，记录最后已读 Redis Stream ID。
6. API 下单在发布 Redis 前先查 `gateway_order_id` 和 `idempotency_key`，命中相同 payload 时返回 `replayed=true`，冲突时不发布 Redis 命令。

重复执行同一批 `ledger-sync` 不会重复插入原始消息。

## 2026-06-14 成交去重排查结论

`tmp/relay_sdk_017_feedback_20260614.md` 中两笔订单表现为订单已 `filled` 但成交明细为空。排查 Redis `event` stream 后确认前置程序已经发送对应 `fill.event`，问题不在 stream 丢消息，而是旧 DDL 将 `fills(account_id, fill_id)` 当作唯一键；测试前置在不同订单间复用了 `fill_id/match_stream_id`，`ON CONFLICT DO NOTHING` 静默丢弃了后到的合法成交。

修复方式：

1. `000005_fill_id_order_scope` 将成交唯一键调整为 `account_id + gateway_order_id + fill_id`。
2. Redis Stream 原始消息仍以 `stream_key + stream_id` 做重复消费幂等。
3. 前置 `fill.event` 必须尽量携带 `gateway_order_id`、`order_id`、`order_stream_id` 和 `fill_id/match_stream_id`；`fill_id` 只要求在订单作用域内稳定。
4. Relay 回放该区间后，`fh-sdk017-writepress-20260614T125055Z-400597-011-300750` 和 `...-012-600000` 已补入逐笔成交。

## 后续工作

1. 将 `hb` 合并为 gateway 心跳状态，并在 `/v1/status` 中暴露摘要。
2. 增加 `dlq` 告警、处置状态和页面入口。
3. 将自动资金持仓刷新调度状态和最近触发时间落盘。
4. 让前置 `order.event.payload` 补齐 `trade_side` 和 `business_type`，支持历史事件重建。
5. 在测试前置返回非空 `order_page/fill_page` 后补充一组真实样例归档和重放记录。
