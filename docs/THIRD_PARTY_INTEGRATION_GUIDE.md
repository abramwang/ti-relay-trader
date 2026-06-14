# 华鑫托管交易网关第三方对接文档

版本：`v1.0`  
日期：`2026-06-13`  
适用程序：`oc_trader_commander_huaxin`  
协议：`relay.stream.v1`  
对接方向：第三方交易系统 / Relay 通过 Redis Stream 与华鑫托管交易网关对接

## 1. 文档目标

本文档面向第三方开发团队，目标是让第三方在不了解 Titans 内部实现和华鑫原生接口细节的情况下，也可以独立完成与 `oc_trader_commander_huaxin` 的对接。

第三方只需要实现 Redis Stream 协议侧逻辑：

1. 向命令流写入交易命令和查询命令。
2. 从回包流读取命令处理结果。
3. 从事件流读取订单状态和成交事件。
4. 从心跳流监控托管交易网关状态。
5. 从死信流排查坏消息和未知命令。

华鑫证券柜台登录、交易前置连接、FENS 注册、订单号映射、订单回报解析、成交回报解析由 `oc_trader_commander_huaxin` 负责。

## 2. 系统边界

### 2.1 部署边界

`oc_trader_commander_huaxin` 是华鑫证券柜台交易接口适配服务，部署在可访问华鑫柜台的托管机房环境。

第三方系统通常部署在托管机房外部，通过可访问的 Redis 端口与该程序交互。

```text
第三方交易系统 / Relay
        |
        | Redis Stream
        v
外部可访问 Redis
        |
        | Redis Stream consumer
        v
oc_trader_commander_huaxin
        |
        | 华鑫交易接口
        v
华鑫证券柜台 / 交易前置
```

### 2.2 第三方不需要处理的内容

第三方不需要直接调用华鑫 SDK，也不需要理解华鑫原生结构体。以下内容由 OC 程序屏蔽：

1. 华鑫登录、认证、FENS、前置连接。
2. `TiReqOrderInsert`、`TiReqOrderDelete` 等原生结构体构造。
3. 华鑫 `nOrderId`、`szOrderStreamId`、`nReqId` 与北向订单号的映射。
4. 订单回报、成交回报去重。
5. 查询分页 final chunk 收口。

第三方只按本文档规定的 Redis JSON 消息开发。

## 3. 环境与凭据

### 3.1 Redis 连接

Redis 连接参数由甲方或部署方通过安全渠道提供。本文档不固化密码。

建议第三方实现支持以下环境变量：

```bash
HX_REDIS_HOST=192.168.3.100
HX_REDIS_PORT=6379
HX_REDIS_PASSWORD=<由授权人员提供>
HX_REDIS_DB=0
HX_RELAY_ENV=prod
HX_RELAY_BROKER_ID=huaxin
HX_RELAY_GATEWAY_ID=00030484
HX_ACCOUNT_ID=00030484
```

当前测试环境常用逻辑标识：

| 字段 | 当前值 | 说明 |
| --- | --- | --- |
| `env` | `prod` | Redis stream 命名环境，不等同于真实生产资金环境 |
| `broker_id` | `huaxin` | 券商标识 |
| `gateway_id` | `00030484` | 托管交易网关实例标识 |
| `account_id` | `00030484` | 测试账户 |

### 3.2 安全要求

1. 不要把 Redis 密码、账户密码、柜台密码写入代码仓库。
2. 第三方日志中不要打印完整密码或账户密码。
3. Redis 连接建议启用白名单、防火墙或专线隔离。
4. 交易测试前必须确认使用券商测试环境账户或其他明确隔离的测试账户。

## 4. Redis Stream 命名

所有 stream 使用统一前缀：

```text
relay:{env}:v1:{broker_id}:{gateway_id}
```

当前华鑫测试网关对应前缀：

```text
relay:prod:v1:huaxin:00030484
```

完整 stream 列表：

| Stream | 方向 | 用途 |
| --- | --- | --- |
| `relay:{env}:v1:{broker_id}:{gateway_id}:cmd.trade` | 第三方 -> OC | 交易命令：下单、批量下单、撤单 |
| `relay:{env}:v1:{broker_id}:{gateway_id}:cmd.query` | 第三方 -> OC | 查询命令：资金、持仓、订单、成交 |
| `relay:{env}:v1:{broker_id}:{gateway_id}:reply` | OC -> 第三方 | 命令级回包 |
| `relay:{env}:v1:{broker_id}:{gateway_id}:event` | OC -> 第三方 | 订单状态事件、成交事件 |
| `relay:{env}:v1:{broker_id}:{gateway_id}:hb` | OC -> 第三方 | 心跳和健康状态 |
| `relay:{env}:v1:{broker_id}:{gateway_id}:dlq` | OC -> 第三方 | 死信消息 |

每条 stream entry 只使用一个 field：

```text
body = "<JSON string>"
```

第三方不要依赖其他 Redis field。

## 5. Redis 读写约定

### 5.1 写命令

第三方使用 `XADD` 写入命令：

```bash
XADD relay:prod:v1:huaxin:00030484:cmd.trade * body '<json>'
XADD relay:prod:v1:huaxin:00030484:cmd.query * body '<json>'
```

交易类 action 写入 `cmd.trade`：

1. `order.submit`
2. `order.batch.submit`
3. `order.cancel`

查询类 action 写入 `cmd.query`：

1. `account.asset.query`
2. `account.positions.query`
3. `order.list.query`
4. `fill.list.query`

### 5.2 读回包和事件

第三方建议按自己的消费模型读取 `reply / event / hb / dlq`。

如果第三方只做请求响应式测试，可以使用 `XREAD` 从调用前的最新 stream id 后开始读。

如果第三方做生产级接入，建议：

1. 自己维护每条输出 stream 的消费位点。
2. 通过 `origin_message_id` 匹配本次命令回包。
3. 通过 `gateway_order_id` 匹配订单事件和成交事件。
4. 不要并行使用同一组 stream、同一账户和同一 gateway 跑多个验收脚本，除非输出侧做了强过滤。

### 5.3 并行测试注意事项

`reply` 和 `event` 是共享输出流。多个 runner 同时跑同一账户、同一 stream prefix 时，输出会交错。

第三方必须按以下字段过滤：

| 消息类型 | 推荐过滤字段 |
| --- | --- |
| `reply` | `origin_message_id` |
| `order.event` | `payload.gateway_order_id` 或 top-level `gateway_order_id` |
| `fill.event` | `payload.gateway_order_id` |
| `deadletter` | `payload.original_message_id` 或 top-level `origin_message_id` |

生产接入建议每个 gateway/account 由一个状态机消费，测试接入建议串行执行验收脚本。

## 6. 通用命令 Envelope

第三方写入 `cmd.trade` 或 `cmd.query` 的 JSON 顶层结构如下：

```json
{
  "message_type": "command",
  "message_id": "msg-uuid",
  "request_id": "req-uuid",
  "correlation_id": "req-uuid",
  "idempotency_key": "idem-business-key",
  "action": "order.submit",
  "payload": {},
  "sent_at": "2026-06-13 10:00:00"
}
```

字段说明：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `message_type` | 建议 | 固定 `command` |
| `message_id` | 是 | 命令唯一 ID。OC 使用它做重复消息保护和回包关联 |
| `request_id` | 建议 | 请求追踪 ID。OC 会透传到 reply/event |
| `correlation_id` | 建议 | 第三方自己的链路关联 ID。OC 会在 `request_correlation_id` 中保留 |
| `idempotency_key` | 交易必填 | 交易命令幂等键。同 key 同 payload 回放首次结果；同 key 不同 payload 返回冲突 |
| `action` | 是 | 命令动作 |
| `payload` | 是 | 业务参数 |
| `sent_at` | 建议 | 第三方发送时间，字符串即可 |

兼容说明：

1. 当前 OC 支持从 top-level、`header`、`meta` 中读取 `message_id / request_id / idempotency_key / correlation_id`。
2. 第三方新开发时统一使用 top-level 字段，不建议再嵌套到 `header` 或 `meta`。
3. `message_id` 缺失会进入 DLQ，并返回 `MISSING_MESSAGE_ID`。

## 7. 交易命令

### 7.1 单笔下单：`order.submit`

写入 stream：

```text
relay:{env}:v1:{broker_id}:{gateway_id}:cmd.trade
```

请求示例：

```json
{
  "message_type": "command",
  "message_id": "msg-submit-0001",
  "request_id": "req-submit-0001",
  "correlation_id": "req-submit-0001",
  "idempotency_key": "idem-submit-0001",
  "action": "order.submit",
  "payload": {
    "account_id": "00030484",
    "client_order_id": "cli-0001",
    "gateway_order_id": "gw-cli-0001",
    "symbol": "600000",
    "exchange": "SH",
    "trade_side": "B",
    "business_type": "S",
    "offset_type": "C",
    "price": 9.54,
    "qty": 100
  },
  "sent_at": "2026-06-13 10:00:00"
}
```

payload 字段：

| 字段 | 必填 | 类型 | 说明 |
| --- | --- | --- | --- |
| `account_id` | 是 | string | 资金账户 |
| `client_order_id` | 建议 | string | 第三方客户端订单号 |
| `gateway_order_id` | 强烈建议 | string | 北向订单主键，后续撤单和事件匹配都用它 |
| `symbol` | 是 | string | 证券代码，如 `600000` |
| `exchange` | 是 | string | 交易所，如 `SH`、`SZ` |
| `trade_side` | 是 | string | `B` 买入，`S` 卖出。也兼容 `buy/BUY/sell/SELL` |
| `business_type` | 是 | string | `S` 股票，`E` ETF。也兼容 `ETF/etf` |
| `offset_type` | 建议 | string | 股票通常为 `C` |
| `price` | 是 | number | 委托价格 |
| `qty` | 是 | integer | 委托数量 |

`reply.status=accepted` 只表示 OC 已接受命令并成功调用柜台下单接口，不表示交易所最终接单或成交。

订单最终状态必须以 `order.event` 为准。

### 7.2 批量下单：`order.batch.submit`

写入 stream：

```text
relay:{env}:v1:{broker_id}:{gateway_id}:cmd.trade
```

请求示例：

```json
{
  "message_type": "command",
  "message_id": "msg-batch-0001",
  "request_id": "req-batch-0001",
  "correlation_id": "req-batch-0001",
  "idempotency_key": "idem-batch-0001",
  "action": "order.batch.submit",
  "payload": {
    "account_id": "00030484",
    "orders": [
      {
        "account_id": "00030484",
        "client_order_id": "cli-batch-0001-1",
        "gateway_order_id": "gw-cli-batch-0001-1",
        "symbol": "600000",
        "exchange": "SH",
        "trade_side": "B",
        "business_type": "S",
        "offset_type": "C",
        "price": 9.54,
        "qty": 100
      },
      {
        "account_id": "00030484",
        "client_order_id": "cli-batch-0001-2",
        "gateway_order_id": "gw-cli-batch-0001-2",
        "symbol": "600000",
        "exchange": "SH",
        "trade_side": "B",
        "business_type": "S",
        "offset_type": "C",
        "price": 9.54,
        "qty": 100
      }
    ]
  },
  "sent_at": "2026-06-13 10:00:00"
}
```

批量下单的回包只表示这一批提交到 OC 的接收结果。每一笔子订单仍会独立产生自己的 `order.event` 和 `fill.event`。

### 7.3 撤单：`order.cancel`

写入 stream：

```text
relay:{env}:v1:{broker_id}:{gateway_id}:cmd.trade
```

请求示例：

```json
{
  "message_type": "command",
  "message_id": "msg-cancel-0001",
  "request_id": "req-cancel-0001",
  "correlation_id": "req-cancel-0001",
  "idempotency_key": "idem-cancel-0001",
  "action": "order.cancel",
  "payload": {
    "account_id": "00030484",
    "gateway_order_id": "gw-cli-0001"
  },
  "sent_at": "2026-06-13 10:00:00"
}
```

撤单字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `account_id` | 建议 | 资金账户 |
| `gateway_order_id` | 是 | 需要撤销的北向订单主键 |

撤单语义：

1. `reply.status=accepted` 表示 OC 已提交撤单请求。
2. 是否撤成以之后的 `order.event.payload.gateway_status` 为准。
3. 如果订单已成交、已撤、已拒等终态，OC 返回 `ORDER_TERMINAL_NOT_CANCELABLE`。
4. 如果 OC 尚未拿到华鑫 `order_id / order_stream_id`，返回 `ORDER_NOT_READY_FOR_CANCEL`。

## 8. 查询命令

查询命令写入：

```text
relay:{env}:v1:{broker_id}:{gateway_id}:cmd.query
```

统一请求格式：

```json
{
  "message_type": "command",
  "message_id": "msg-query-0001",
  "request_id": "req-query-0001",
  "correlation_id": "req-query-0001",
  "idempotency_key": "idem-query-0001",
  "action": "account.asset.query",
  "payload": {
    "account_id": "00030484"
  },
  "sent_at": "2026-06-13 10:00:00"
}
```

支持的查询：

| action | result_type | payload 说明 |
| --- | --- | --- |
| `account.asset.query` | `asset_page` | `payload.account` |
| `account.positions.query` | `position_page` | `payload.items[]` |
| `order.list.query` | `order_page` | `payload.items[]` |
| `fill.list.query` | `fill_page` | `payload.items[]` |

查询分页规则：

1. 如果只有一页，直接返回 `status=completed`，`chunk.is_last=true`。
2. 如果有多页，中间页返回 `status=partial`，`chunk.is_last=false`。
3. 最后一页必须返回 `status=completed`，`chunk.is_last=true`。
4. 华鑫接口可能先推数据页，再推空的 final 回调；第三方必须以 `completed + is_last=true` 判断查询结束。

## 9. Reply 回包格式

所有命令都有 `reply`。第三方必须用 `origin_message_id` 匹配原始命令。

通用结构：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "reply",
  "message_id": "reply-...",
  "produced_at": "2026-06-13T10:00:00.123Z",
  "timestamp": "2026-06-13 18:00:00.000123",
  "producer": {
    "system": "colo-trader",
    "role": "colo-execution-consumer",
    "instance_id": "oc_trader_commander_huaxin.00030484"
  },
  "routing": {
    "env": "prod",
    "broker_id": "huaxin",
    "gateway_id": "00030484",
    "account_id": "00030484"
  },
  "status": "accepted",
  "action": "order.submit",
  "result_type": "order_action_receipt",
  "origin_message_id": "msg-submit-0001",
  "request_id": "req-submit-0001",
  "correlation_id": "msg-submit-0001",
  "request_correlation_id": "req-submit-0001",
  "idempotency_key": "idem-submit-0001",
  "code": "OK",
  "message": "order accepted",
  "payload": {}
}
```

### 9.1 Reply status

| status | 语义 |
| --- | --- |
| `accepted` | OC 已接受交易命令并提交到适配层或柜台接口 |
| `partial` | 查询中间页 |
| `completed` | 查询已结束 |
| `rejected` | 业务拒绝或参数拒绝 |
| `failed` | 技术失败或适配层失败 |

### 9.2 result_type

| action/status | result_type |
| --- | --- |
| `order.submit` 成功 | `order_action_receipt` |
| `order.batch.submit` 成功 | `order_action_receipt` |
| `order.cancel` 成功 | `order_action_receipt` |
| `account.asset.query` | `asset_page` |
| `account.positions.query` | `position_page` |
| `order.list.query` | `order_page` |
| `fill.list.query` | `fill_page` |
| `rejected / failed` | `error_result` |

### 9.3 下单成功 reply payload

```json
{
  "gateway_order_id": "gw-cli-0001",
  "adapter_request_id": 106,
  "request_id": "req-submit-0001",
  "gateway_status": "accepted",
  "atlas_status": "new",
  "accepted_at": "2026-06-13T10:00:00.123Z"
}
```

### 9.4 批量下单成功 reply payload

```json
{
  "accepted_count": 2,
  "failed_count": 0,
  "accepted_orders": [
    {
      "index": 0,
      "gateway_order_id": "gw-cli-batch-0001-1",
      "adapter_request_id": 112
    },
    {
      "index": 1,
      "gateway_order_id": "gw-cli-batch-0001-2",
      "adapter_request_id": 113
    }
  ],
  "request_id": "req-batch-0001",
  "gateway_status": "accepted",
  "atlas_status": "new",
  "accepted_at": "2026-06-13T10:00:00.123Z"
}
```

### 9.5 错误 reply payload

```json
{
  "code": "ORDER_NOT_FOUND",
  "message": "gateway_order_id not found",
  "correlation_id": "msg-cancel-0001",
  "gateway_order_id": "gw-cli-0001"
}
```

错误信息同时会出现在 top-level `code / message` 和 `payload.code / payload.message`。

## 10. 订单事件：`order.event`

订单状态变化由 `event` stream 推送。第三方必须以 `gateway_order_id` 匹配订单。

示例：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "event",
  "message_id": "event-...",
  "event_type": "order.event",
  "event_name": "order.event",
  "origin_message_id": "msg-submit-0001",
  "request_id": "req-submit-0001",
  "correlation_id": "msg-submit-0001",
  "gateway_order_id": "gw-cli-0001",
  "payload": {
    "gateway_order_id": "gw-cli-0001",
    "order_id": 1680001,
    "order_stream_id": "110018100000001",
    "account_id": "00030484",
    "symbol": "600000",
    "exchange": "SH",
    "order_qty": 100,
    "cum_filled_qty": 0,
    "leaves_qty": 100,
    "limit_price": 9.54,
    "gateway_status": "working",
    "atlas_status": "routed",
    "is_terminal": false
  },
  "adapter_context": {
    "order_status_code": 2,
    "order_status_name": "queued",
    "order_id": 1680001,
    "order_stream_id": "110018100000001",
    "dealt_vol": 0,
    "withdrawn_vol": 0,
    "invalid_vol": 0,
    "fee": 0.0,
    "nOrderId": 1680001,
    "szOrderStreamId": "110018100000001",
    "nDealtVol": 0,
    "nTotalWithDrawnVol": 0,
    "nInValid": 0,
    "nFee": 0.0,
    "error_text": "",
    "broker_status_text": "VIP:正确"
  }
}
```

### 10.1 gateway_status

| gateway_status | atlas_status | 是否终态 | 说明 |
| --- | --- | --- | --- |
| `accepted` | `new` | 否 | OC/柜台初始接受 |
| `working` | `routed` | 否 | 已报入或排队中，可继续等待成交/撤单 |
| `filled` | `filled` | 是 | 全部成交 |
| `cancelled` | `cancelled` | 是 | 已撤 |
| `rejected` | `rejected` | 是 | 被拒 |

当前 OC 把华鑫状态映射如下：

| 华鑫状态名 | gateway_status | adapter_context.order_status_name |
| --- | --- | --- |
| `unAccept` | `accepted` | `unAccept` |
| `accepted` | `working` | `accepted` |
| `queued` | `working` | `queued` |
| `toRemove` | `working` | `toRemove` |
| `removing` | `working` | `removing` |
| `dealt` | `filled` | `dealt` |
| `removed` | `cancelled` | `removed` |
| `fail` | `rejected` | `fail` |

### 10.2 拒单事件

拒单事件会带：

```json
{
  "payload": {
    "gateway_status": "rejected",
    "atlas_status": "rejected",
    "is_terminal": true,
    "reject_code": "BROKER_REJECTED",
    "reject_message": "VIP:价格超过涨跌停板限制"
  },
  "adapter_context": {
    "error_text": "VIP:价格超过涨跌停板限制"
  }
}
```

正常事件中 `adapter_context.error_text` 为空字符串。如果券商正常状态文本存在，会放在 `adapter_context.broker_status_text`。

## 11. 成交事件：`fill.event`

成交事件来源于华鑫成交回报。

示例：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "event",
  "message_id": "event-...",
  "event_type": "fill.event",
  "event_name": "fill.event",
  "origin_message_id": "msg-submit-0001",
  "request_id": "req-submit-0001",
  "correlation_id": "msg-submit-0001",
  "gateway_order_id": "gw-cli-0001",
  "payload": {
    "gateway_order_id": "gw-cli-0001",
    "fill_id": "match-stream-id",
    "order_id": 1680001,
    "order_stream_id": "110018100000001",
    "account_id": "00030484",
    "symbol": "600000",
    "exchange": "SH",
    "price": 9.54,
    "qty": 100,
    "match_timestamp": 1777103459957,
    "trade_side": "B"
  },
  "adapter_context": {
    "order_id": 1680001,
    "order_stream_id": "110018100000001",
    "match_stream_id": "match-stream-id",
    "fee": 0.0,
    "nOrderId": 1680001,
    "szOrderStreamId": "110018100000001",
    "szStreamId": "match-stream-id",
    "nFee": 0.0
  }
}
```

成交去重规则：

1. 优先使用 `account_id + gateway_order_id + adapter_context.match_stream_id` 或 `account_id + gateway_order_id + payload.fill_id` 去重。
2. 如果柜台没有稳定成交流号，可以使用 `order_stream_id + match_timestamp + qty + price` 组合去重。
3. 不要从订单累计成交差分反推成交明细；成交事实以 `fill.event` 为准。

前置程序发送 `fill.event` 时应尽量携带 `gateway_order_id`、`order_id` 和 `order_stream_id`。Relay 支持 `fill_id/match_stream_id` 在不同订单间复用，但同一订单内的成交编号应保持稳定。

## 12. Heartbeat 心跳

心跳写入：

```text
relay:{env}:v1:{broker_id}:{gateway_id}:hb
```

示例：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "heartbeat",
  "message_id": "hb-...",
  "produced_at": "2026-06-13T10:00:00.123Z",
  "component_id": "oc_trader_commander_huaxin.00030484",
  "component_role": "broker_trader_gateway",
  "state": "UP",
  "state_text": "running",
  "last_command_rx_at": "2026-06-13T09:59:58.001Z",
  "pending_trade_count": 0,
  "pending_query_count": 0,
  "payload": {
    "component_id": "oc_trader_commander_huaxin.00030484",
    "component_role": "broker_trader_gateway",
    "state": "UP",
    "state_text": "running",
    "last_command_rx_at": "2026-06-13T09:59:58.001Z",
    "pending_trade_count": 0,
    "pending_query_count": 0
  }
}
```

监控建议：

1. `state=UP` 表示 OC 主循环和 Redis 发布正常。
2. `pending_trade_count` 长时间不为 0，说明有订单还未到终态。
3. `pending_query_count` 长时间不为 0，说明查询 final chunk 未收口，应告警。
4. 第三方可按 `produced_at` 判断心跳是否超时。

## 13. Deadletter 死信

OC 遇到坏 JSON、缺少 `message_id`、未知 action 等问题时，会写 `dlq`。

示例：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "deadletter",
  "message_id": "dlq-...",
  "stream_key": "relay:prod:v1:huaxin:00030484:cmd.query",
  "stream_id": "1777103459926-0",
  "origin_message_id": "msg-unknown-0001",
  "request_id": "req-unknown-0001",
  "correlation_id": "msg-unknown-0001",
  "action": "unknown.action",
  "code": "UNKNOWN_ACTION",
  "message": "unknown action",
  "payload": {
    "original_stream": "relay:prod:v1:huaxin:00030484:cmd.query",
    "original_entry_id": "1777103459926-0",
    "original_message_id": "msg-unknown-0001",
    "delivery_count": 1,
    "reason_code": "UNKNOWN_ACTION",
    "reason_message": "unknown action",
    "moved_at": "2026-06-13T10:00:00.123Z",
    "original_body": {}
  }
}
```

如果原始 `body` 不是合法 JSON，`payload.original_body` 形如：

```json
{
  "_raw": "{bad-json"
}
```

## 14. 幂等与去重

### 14.1 message_id

`message_id` 是命令唯一标识。

OC 行为：

1. 如果同一个 `message_id` 在首条命令处理完成后再次出现，OC 会识别为重复消息并忽略，不会再次下单。
2. 如果同一个 `message_id` 在处理中的窗口内再次出现，OC 会忽略重复 in-flight 消息。

第三方要求：

1. 每条新命令必须使用新的 `message_id`。
2. 不要用重复 `message_id` 期待 OC 再次返回一条 reply。
3. 等待 reply 时用 `origin_message_id == 原 message_id` 匹配。

### 14.2 idempotency_key

`idempotency_key` 用于交易命令业务幂等，只对以下 action 生效：

1. `order.submit`
2. `order.batch.submit`
3. `order.cancel`

OC 行为：

| 场景 | 结果 |
| --- | --- |
| 同 `idempotency_key` + 同 payload | 回放第一次 reply，不重复调用柜台 |
| 同 `idempotency_key` + 不同 payload | 返回 `IDEMPOTENCY_CONFLICT` |
| 不同 `idempotency_key` | 按新命令处理 |

第三方建议：

1. 下单类命令使用业务唯一键作为 `idempotency_key`，例如 `account_id + strategy_order_id`。
2. 批量下单的 `idempotency_key` 对整批 payload 生效。
3. 撤单的 `idempotency_key` 建议使用 `account_id + gateway_order_id + cancel_attempt_id`。

## 15. 错误码

当前 OC 可能返回以下错误码：

| code | 出现场景 | 建议处理 |
| --- | --- | --- |
| `OK` | 命令接受成功 | 继续等待事件或查询结果 |
| `BAD_COMMAND_BODY` | `body` 不是合法 JSON | 修复消息序列化 |
| `MISSING_MESSAGE_ID` | 缺少 `message_id` | 修复命令 envelope |
| `UNKNOWN_ACTION` | 未知 action | 检查 action 拼写和 stream |
| `ORDER_SUBMIT_REJECTED` | 本地字段校验失败或下单接口调用失败 | 检查 symbol/exchange/price/qty |
| `INVALID_BATCH` | 批量请求缺少 `payload.orders[]` | 修复 payload |
| `BATCH_REJECTED` | 批量订单全部失败 | 检查每笔订单参数 |
| `INVALID_ARGUMENT` | 缺少必要参数 | 按 message 修复 |
| `ORDER_NOT_FOUND` | 撤单找不到 `gateway_order_id` | 确认订单是否由本 OC 实例提交 |
| `ORDER_TERMINAL_NOT_CANCELABLE` | 订单已终态，不可撤 | 按终态处理，不再重试撤单 |
| `ORDER_NOT_READY_FOR_CANCEL` | 订单映射未就绪 | 稍后重试，或等待首个 order.event |
| `CANCEL_SUBMIT_FAILED` | 撤单接口调用失败 | 进入人工/重试策略 |
| `QUERY_SUBMIT_FAILED` | 查询接口调用失败 | 稍后重试 |
| `QUERY_FAILED` | 柜台查询回调失败 | 稍后重试或告警 |
| `IDEMPOTENCY_CONFLICT` | 同幂等键不同 payload | 第三方必须修正幂等键或 payload |
| `BROKER_REJECTED` | 订单事件中的券商拒单 | 使用 `reject_message` 展示原因 |

## 16. 状态机建议

第三方每笔订单建议维护以下状态机：

```text
created
  |
  | reply.status=accepted
  v
accepted
  |
  | order.event.gateway_status=working
  v
working
  | \
  |  \ fill.event
  |   v
  | partially_filled
  |
  | order.event.gateway_status in terminal states
  v
filled / cancelled / rejected
```

终态集合：

```text
filled
cancelled
rejected
```

注意：

1. `reply.status=accepted` 不是订单终态。
2. `order.cancel` 的 `reply.status=accepted` 不是撤单成功，撤单成功必须等 `order.event.gateway_status=cancelled`。
3. 撤单和成交可能竞态，撤单 accepted 后订单也可能最终 `filled`。
4. 成交事实以 `fill.event` 为准；订单累计成交以 `order.event.payload.cum_filled_qty` 为准。

## 17. 查询 payload 结构

### 17.1 资金查询 `asset_page`

```json
{
  "payload": {
    "account": {
      "account_id": "00030484",
      "cash_available": 50000000.0,
      "cash_total": 50000000.0,
      "net_asset": 50000000.0,
      "market_value": 0.0
    }
  },
  "status": "completed",
  "chunk": {
    "is_last": true
  }
}
```

### 17.2 持仓查询 `position_page`

```json
{
  "payload": {
    "items": [
      {
        "account_id": "00030484",
        "symbol": "600000",
        "exchange": "SH",
        "quantity": 100,
        "sellable_qty": 100,
        "avg_cost": 9.54,
        "market_value": 954.0,
        "shareholder_id": "A00030484"
      }
    ]
  },
  "status": "partial",
  "chunk": {
    "is_last": false
  }
}
```

最后一页可能为空：

```json
{
  "payload": {
    "items": []
  },
  "status": "completed",
  "chunk": {
    "is_last": true
  }
}
```

第三方必须把 final chunk 作为查询结束信号。

### 17.3 订单查询 `order_page`

`payload.items[]` 与 `order.event.payload` 接近，额外包含 `adapter_status`、`adapter_status_code`、`update_time`。

### 17.4 成交查询 `fill_page`

`payload.items[]` 与 `fill.event.payload` 接近。

## 18. 验收脚本

仓库提供 canonical 验收入口：

```bash
python3 /home/Titian_Cpp/reference/trade.py --help
```

该入口实际调用：

```text
/home/Titian_Cpp/oceanus/test/trade.py
```

第三方或 Relay 环境建议把同一份脚本同步到自己的仓库 `reference/trade.py`，避免双方跑不同版本脚本。

### 18.1 基础查询验收

```bash
python3 /home/Titian_Cpp/reference/trade.py queries --timeout 15
```

验收点：

1. 四类查询均返回 reply。
2. 每类查询最终有 `status=completed`。
3. 每类查询最终有 `chunk.is_last=true`。
4. 心跳 `pending_query_count=0`。

### 18.2 主流程验收

```bash
python3 /home/Titian_Cpp/reference/trade.py full \
  --yes-trade \
  --timeout 15 \
  --order-event-timeout 30
```

覆盖：

1. 查询。
2. 单笔下单。
3. 自动撤单或终态收口。
4. 批量下单。
5. 批量子单自清理。
6. DLQ。

说明：如果订单在撤单前已经成交，最终 `filled` 是合法结果。

### 18.3 一键验收

```bash
python3 /home/Titian_Cpp/reference/trade.py acceptance \
  --yes-trade \
  --timeout 15 \
  --order-event-timeout 30
```

覆盖：

1. 查询。
2. 成功撤单样例。
3. 单笔下单。
4. 批量下单。
5. `idem-submit`。
6. `idem-batch`。
7. `idem-cancel`。
8. DLQ。

如果测试环境暂时无法产生可撤订单，可跳过成功撤单强校验：

```bash
python3 /home/Titian_Cpp/reference/trade.py acceptance \
  --yes-trade \
  --timeout 15 \
  --order-event-timeout 30 \
  --skip-cancel-success
```

如果要强制验证成功撤单：

```bash
python3 /home/Titian_Cpp/reference/trade.py acceptance \
  --yes-trade \
  --timeout 15 \
  --order-event-timeout 30 \
  --require-cancel-success
```

### 18.4 幂等专项

```bash
python3 /home/Titian_Cpp/reference/trade.py idem-submit --yes-trade --timeout 15 --order-event-timeout 30
python3 /home/Titian_Cpp/reference/trade.py idem-batch  --yes-trade --timeout 15 --order-event-timeout 30
python3 /home/Titian_Cpp/reference/trade.py idem-cancel --timeout 15
```

验收点：

1. 同 key 同 payload 回放首次 reply。
2. 同 key 不同 payload 返回 `IDEMPOTENCY_CONFLICT`。
3. 不重复产生新的柜台下单。

### 18.5 成功撤单专项

```bash
python3 /home/Titian_Cpp/reference/trade.py cancel-success \
  --yes-trade \
  --timeout 15 \
  --order-event-timeout 40 \
  --cancel-success-price 8.59
```

验收点：

1. 下单 `reply.status=accepted`。
2. 订单进入 `gateway_status=accepted` 或 `working`。
3. 撤单 `reply.status=accepted`。
4. 最终 `order.event.payload.gateway_status=cancelled`。
5. `adapter_context.withdrawn_vol` 等于撤单数量。

当前测试环境里 `600000.SH` 常用价格说明：

| 用途 | 价格 |
| --- | --- |
| 默认下单价 | `9.54` |
| 成功撤单被动价 | `8.59` |
| 本地价格保护区间 | `8.59 - 10.49` |

如果测试环境行情变化，可使用：

```bash
--price <price>
--limit-low <low>
--limit-high <high>
--cancel-success-price <passive_price>
```

## 19. 第三方实现 checklist

上线前请确认：

1. 能连接 Redis，并能写入 `cmd.trade / cmd.query`。
2. 所有命令都包含唯一 `message_id`。
3. 交易命令都包含稳定 `idempotency_key`。
4. 单笔和批量订单都生成唯一 `gateway_order_id`。
5. reply 匹配使用 `origin_message_id`。
6. order/fill event 匹配使用 `gateway_order_id`。
7. 查询结束判断使用 `status=completed` 且 `chunk.is_last=true`。
8. 撤单结果以最终 `order.event` 为准，不以撤单 reply 为准。
9. 支持 `IDEMPOTENCY_CONFLICT`、`ORDER_NOT_FOUND`、`ORDER_TERMINAL_NOT_CANCELABLE` 等错误码。
10. 心跳监控 `pending_trade_count / pending_query_count`。
11. DLQ 进入告警或排障面板。
12. 验收脚本不要并行跑共享 stream，除非使用强过滤或独立 stream prefix。

## 20. 最小 Python 写入示例

```python
import json
import time
import uuid
import redis

r = redis.Redis(
    host="192.168.3.100",
    port=6379,
    password="<由授权人员提供>",
    db=0,
    decode_responses=True,
)

prefix = "relay:prod:v1:huaxin:00030484"
cmd_trade = f"{prefix}:cmd.trade"

message_id = f"msg-{uuid.uuid4()}"
request_id = f"req-{uuid.uuid4()}"

command = {
    "message_type": "command",
    "message_id": message_id,
    "request_id": request_id,
    "correlation_id": request_id,
    "idempotency_key": f"idem-submit-{uuid.uuid4()}",
    "action": "order.submit",
    "payload": {
        "account_id": "00030484",
        "client_order_id": f"cli-{int(time.time() * 1000)}",
        "gateway_order_id": f"gw-{int(time.time() * 1000)}",
        "symbol": "600000",
        "exchange": "SH",
        "trade_side": "B",
        "business_type": "S",
        "offset_type": "C",
        "price": 9.54,
        "qty": 100,
    },
    "sent_at": time.strftime("%Y-%m-%d %H:%M:%S"),
}

entry_id = r.xadd(cmd_trade, {"body": json.dumps(command, ensure_ascii=False)})
print("sent", entry_id, message_id)
```

## 21. 常见问题

### 21.1 为什么下单 accepted 后又 rejected？

`reply.status=accepted` 只表示 OC 接受命令并调用柜台。交易所或柜台后续仍可能因为价格、权限、涨跌停、状态等原因拒单。最终结果以 `order.event` 为准。

### 21.2 为什么撤单 accepted 后订单 filled？

撤单与成交存在竞态。撤单请求提交成功不等于撤单最终成功。如果撤单前订单已成交，最终状态会是 `filled`。

### 21.3 为什么查询有 partial 后还会收到空 completed？

这是分页收口。第三方必须等待 `completed + chunk.is_last=true`，不能只看第一条 partial。

### 21.4 为什么并行测试时日志串台？

`reply/event` 是共享 stream。并行 runner 需要按 `origin_message_id / gateway_order_id / idempotency_key` 过滤，或使用不同 gateway/account/stream prefix。

### 21.5 `error_text` 为空是否异常？

不是。正常事件 `adapter_context.error_text` 为空。券商正常状态文本会放在 `broker_status_text`。只有拒单/失败类事件才应关注 `error_text` 或 `payload.reject_message`。

## 22. 对接完成标准

第三方实现满足以下条件，即可认为 Redis Stream 协议侧对接完成：

1. `queries` 通过，所有查询 final chunk 收口。
2. `full` 通过，主交易链路、事件链路、DLQ 均可消费。
3. `idem-submit / idem-batch / idem-cancel` 通过。
4. 若环境支持可撤订单，`cancel-success` 通过。
5. 消费端状态机能正确处理 `accepted -> working -> filled/cancelled/rejected`。
6. 心跳和 Redis consumer group 均无长期 pending。
7. DLQ 能被监控并可追溯原始消息。
