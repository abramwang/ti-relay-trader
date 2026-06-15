# Redis Stream 只读探测

更新时间：`2026-06-13`

## 当前状态

已新增 `relayctl redis-probe` 只读探测命令，用于查看前置测试环境 Redis Stream 的存在性、长度、最新 ID 和最近消息摘要。

已新增 `relayctl redis-scan` 只读扫描命令，用于在生产或测试 Redis 中发现 `relay:<env>:v1:<broker>:<gateway>:<role>` 账户前缀。它只执行 `PING`、`SCAN`、`TYPE` 和 `XLEN`，不读取消息正文，不执行 `XADD`，不会创建 consumer group，不会确认或移动任何消费位点。

该命令只执行：

1. `PING`
2. `XINFO STREAM`
3. `XREVRANGE`

不会执行 `XADD`，不会创建 consumer group，不会确认或移动任何消费位点。

当前已在真实 Redis 上完成 `reply/event` 小批量读取和 PostgreSQL raw 归档。`redis-probe` 仍保持只读探测定位；需要写入账本时使用 `relayctl ledger-sync`。

## 命令入口

发现 Redis 中有哪些账户前缀：

```bash
go run ./cmd/relayctl redis-scan -config config/relay.prod.yaml
```

限定扫描 pattern：

```bash
go run ./cmd/relayctl redis-scan \
  -config config/relay.prod.yaml \
  -pattern 'relay:prod:v1:huaxin:*'
```

```bash
go run ./cmd/relayctl redis-probe -config config/relay.local.yaml
```

使用 `RELAY_CONFIG_PATH`：

```bash
RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml go run ./cmd/relayctl redis-probe
```

指定 stream prefix：

```bash
go run ./cmd/relayctl redis-probe \
  -config config/relay.local.yaml \
  -stream-prefix relay:prod:v1:huaxin:00030484 \
  -samples 2
```

## 环境变量兼容

除 relay 配置文件外，探测命令也兼容前置文档里的环境变量：

| 变量 | 说明 |
| --- | --- |
| `REDIS_URL` | 完整 Redis URL，优先级最高 |
| `HX_REDIS_HOST` | Redis host |
| `HX_REDIS_PORT` | Redis port，默认 `6379` |
| `HX_REDIS_PASSWORD` | Redis password |
| `HX_REDIS_DB` | Redis DB，默认 `0` |
| `HX_RELAY_ENV` | stream env，例如 `prod` |
| `HX_RELAY_BROKER_ID` | broker，例如 `huaxin` |
| `HX_RELAY_GATEWAY_ID` | gateway，例如 `00030484` |
| `HX_ACCOUNT_ID` | 测试账户 |

凭据不要提交到 Git。建议写入本地未跟踪配置文件或进程环境。

## 探测范围

对每个 prefix 会探测以下 stream：

| Stream | 用途 |
| --- | --- |
| `cmd.trade` | 交易命令输入 |
| `cmd.query` | 查询命令输入 |
| `reply` | 命令回包 |
| `event` | 订单和成交事件 |
| `hb` | 心跳 |
| `dlq` | 死信 |

prefix 来源优先级：

1. `-stream-prefix` 参数。
2. 配置文件 `accounts[].stream_prefix`。
3. 配置文件或环境变量中的 `redis.env + redis.broker_id + redis.gateway_id`。

`redis-scan` 不依赖 `accounts[]` 路由；默认根据 `redis.env` 扫描 `relay:<env>:v1:*:*`，并按 prefix 汇总出候选账户。发现新账户后，仍需要人工确认并写入本地未跟踪配置 `accounts[]`，默认保持 `trading_enabled=false`。

## 输出说明

输出为 JSON，敏感 Redis URL 会脱敏：

```json
{
  "protocol": "relay.stream.v1",
  "redis_addr": "redis://:***@host:6379/0",
  "prefixes": ["relay:prod:v1:huaxin:00030484"],
  "streams": [
    {
      "name": "relay:prod:v1:huaxin:00030484:hb",
      "exists": true,
      "length": 12,
      "last_generated_id": "1777103459926-0",
      "latest": [
        {
          "message_type": "heartbeat",
          "message_id": "hb-...",
          "body_bytes": 512,
          "payload_keys": ["component_id", "state"]
        }
      ]
    }
  ]
}
```

消息摘要只解析 `body` 里的关键路由字段、状态字段和 payload key 列表，不打印完整 body。

## 后续联调顺序

1. 用 `redis-probe` 只读确认 `hb` 是否持续增长。
2. 只读观察 `reply/event/dlq` 是否有历史消息。
3. 用 `ledger-sync` 将 `reply/event` 小批量归档到 `raw_stream_messages`。
4. 实现查询命令 client，只联调 `account.asset.query`、`account.positions.query`、`order.list.query`、`fill.list.query`。
5. 查询链路通过后，再在明确测试账户和风险边界后联调 `order.submit`、`order.batch.submit`、`order.cancel`。

账本同步说明见 [docs/REDIS_LEDGER_SYNC.md](/home/ti-relay-trader/docs/REDIS_LEDGER_SYNC.md:1)。
