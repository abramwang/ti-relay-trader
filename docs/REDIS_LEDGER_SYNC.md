# Redis Stream 到 PostgreSQL 账本同步

更新时间：`2026-06-13`

## 当前状态

已新增 `relayctl ledger-sync`，用于把前置服务输出的 Redis Stream 消息写入 PostgreSQL 账本。

首批同步范围：

1. `reply`：完整归档到 `raw_stream_messages`。
2. `event`：完整归档到 `raw_stream_messages`。
3. `order.event`：payload 字段足够时写入 `accounts`、`orders`、`order_events`。
4. `fill.event`：payload 字段足够时写入 `accounts`、`fills`。

当前命令是受控批处理入口，不会写入 `cmd.trade` 或 `cmd.query`，也不会移动 consumer group 位点。

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
4. `fills` 以 `account_id + fill_id` 或 fallback 唯一键去重。

重复执行同一批 `ledger-sync` 不会重复插入原始消息。

## 后续工作

1. 实现撤单 API，写入 `order.cancel` 并归档命令。
2. 增加 worker 常驻模式，持续消费 `reply/event/hb/dlq`。
3. 引入消费位点表或 consumer group，避免每次从 `0` 回放。
4. 让前置 `order.event.payload` 补齐 `trade_side` 和 `business_type`，支持历史事件重建。
