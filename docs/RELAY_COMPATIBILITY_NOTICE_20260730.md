# OC 华鑫网关 v1.2 Relay 兼容通知

版本：`v1.2`  
日期：`2026-07-30`  
适用程序：`oc_trader_commander_huaxin`  
协议：`relay.stream.v1`  
代码基线：`cb2ac8f`

## 1. 结论

本次更新不修改 Redis Stream 名称，也不修改现有 `order.event`、`fill.event`、`transfer.event` 和查询 page 的核心字段。

Relay 需要处理一项新增业务事件：

1. 识别 `event_name=order.cancel.rejected`。
2. 撤单被拒绝只表示本次撤单动作失败，不能把原订单状态改成 `rejected`。
3. 原订单继续保持已有状态，后续仍可能成交、再次撤单或由查询修正。

其余变化均为增量字段、输入校验或 OC 内部可靠性增强。Relay 应保持“忽略未知增量字段”的前向兼容策略。

## 2. Relay 必须兼容的变化

### 2.1 撤单拒绝事件

`order.cancel` 的初始 `reply.status=accepted` 仍然只表示撤单请求已提交到华鑫接口，不表示撤单成功。

华鑫明确拒绝撤单时，OC 会向 event stream 发布：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "event",
  "event_type": "order.cancel.event",
  "event_name": "order.cancel.rejected",
  "action": "order.cancel",
  "origin_message_id": "msg-cancel-001",
  "request_id": "req-cancel-001",
  "correlation_id": "msg-cancel-001",
  "gateway_order_id": "gw-order-001",
  "payload": {
    "gateway_order_id": "gw-order-001",
    "account_id": "501000114077",
    "order_id": 123,
    "order_stream_id": "12001A180000123",
    "cancel_status": "rejected",
    "code": "BROKER_CANCEL_REJECTED",
    "message": "当前状态禁止此项操作",
    "order_state_changed": false,
    "retry_safe": false,
    "occurred_at": "2026-07-30T10:00:00.123Z"
  },
  "adapter_context": {
    "broker_error_id": 1,
    "error_text": "当前状态禁止此项操作",
    "broker_account_id": "50100011407701"
  }
}
```

Relay 处理规则：

1. 以 `origin_message_id/request_id/gateway_order_id` 关联撤单请求。
2. 将撤单尝试记录为失败。
3. 不修改订单主表的 `gateway_status/atlas_status`。
4. 不把 `payload.message` 写入原订单的下单拒绝原因。
5. 是否允许再次撤单由 Relay 风控策略决定，不能根据 `retry_safe=false` 自动重试。
6. 真正撤单成功仍以 `order.event.payload.gateway_status=cancelled` 为准。

### 2.2 未知事件的前向兼容

Relay event dispatcher 不应因新增 `event_type/event_name` 导致整条 event stream 消费失败。

推荐策略：

1. 已知事件按对应 schema 入库。
2. 未知事件保留 raw 消息并告警。
3. 未知事件不能阻塞同一 stream 后续订单和成交事件。

## 3. 向后兼容的增量变化

### 3.1 Heartbeat 就绪状态

OC 不再在华鑫尚未就绪时固定报告 `UP`。

状态规则：

| state | 含义 |
| --- | --- |
| `UP` | 华鑫登录、股东账户初始化和初始订单快照均已完成 |
| `DEGRADED` | Redis 可发布心跳，但华鑫未 ready 或初始订单同步未完成 |

新增字段：

```json
{
  "state": "DEGRADED",
  "state_text": "initial_order_sync_pending",
  "redis_ready": true,
  "broker_ready": true,
  "order_snapshot_ready": false,
  "accepting_trade_commands": true,
  "accepting_cancel_commands": false
}
```

Relay 建议：

1. 只有 `state=UP` 时自动开放全部交易和撤单。
2. `accepting_trade_commands=true` 只表示普通交易命令可提交。
3. `accepting_cancel_commands=false` 时不要自动发送撤单。
4. `state_text=broker_not_ready` 时暂停交易并告警。

### 3.2 Batch 部分失败明细

`order.batch.submit` reply 的 payload 新增：

```json
{
  "accepted_count": 1,
  "failed_count": 1,
  "accepted_orders": [
    {
      "index": 0,
      "gateway_order_id": "gw-001",
      "adapter_request_id": 201
    }
  ],
  "failed_orders": [
    {
      "index": 1,
      "gateway_order_id": "gw-002",
      "code": "ORDER_SUBMIT_REJECTED",
      "message": "exchange must be SH or SZ"
    }
  ]
}
```

旧消费者可以忽略 `failed_orders`。新消费者应按 `index` 对应原始 `payload.orders[]`。

### 3.3 新错误码

| code | 含义 | Relay 建议 |
| --- | --- | --- |
| `ACTION_STREAM_MISMATCH` | trade/query action 写错命令 stream | 修正 stream 后使用新 `message_id` 重发 |
| `MESSAGE_ID_CONFLICT` | 同一 `message_id` 的 action、payload 或幂等键发生变化 | 视为生产者 bug，禁止自动重试 |
| `ACCOUNT_MISMATCH` | payload 账户与当前 gateway stream 账户不一致 | 修正路由或账户配置 |
| `BROKER_CANCEL_REJECTED` | 华鑫明确拒绝撤单动作 | 保留原订单状态 |
| `CANCEL_RESPONSE_TIMEOUT` | 华鑫未在超时内返回撤单动作响应 | 查询订单对账，不自动重撤 |
| `COMMAND_OUTCOME_UNKNOWN` | OC 重启时发现未完成交易命令 | 先查订单，再人工决定是否重试 |
| `QUERY_INTERRUPTED` | OC 重启中断查询 | 使用新 `message_id` 重试查询 |

`CANCEL_RESPONSE_TIMEOUT` 当前写入 DLQ，payload 中带：

```json
{
  "gateway_order_id": "...",
  "origin_message_id": "...",
  "request_id": "...",
  "adapter_request_id": 123,
  "reconciliation_required": true
}
```

## 4. 命令输入规则收紧

### 4.1 Stream 角色

| Stream | 允许 action |
| --- | --- |
| `cmd.trade` | `order.submit`、`order.batch.submit`、`order.cancel` |
| `cmd.query` | `account.asset.query`、`account.positions.query`、`order.list.query`、`fill.list.query` |

投错 stream 不会调用华鑫柜台。

### 4.2 下单字段

下单必须满足：

1. `account_id` 与 stream 中的 `gateway_id` 一致。
2. `exchange` 为 `SH` 或 `SZ`，小写输入会规范化为大写。
3. 普通股票业务使用 `business_type=S` 且 `trade_side=B/S`。
4. ETF 申购赎回使用 `business_type=E` 且 `trade_side=P/R`。
5. `price` 为有限正数，不能是 `NaN/Infinity/0/负数`。
6. `qty` 为 int 范围内的正整数。
7. `gateway_order_id` 在账户范围内不可复用。

OC 不再把缺失方向默认成买入，也不再把未知业务类型默认成普通股票。

## 5. 幂等和 Redis PEL

### 5.1 执行中的重复消息

相同 `message_id` 在原查询尚未完成时：

1. OC 不会重复调用华鑫查询。
2. 重复 Redis entry 等待原命令终态。
3. 原命令终态 reply 成功写入后，OC ACK 原 entry 和等待中的重复 entry。

### 5.2 已完成查询的重复消息

多页查询会按原顺序重放完整的 `partial + completed` reply 序列，不再只重放最后一页。

内存 reply 缓存默认保存最近 `256` 个命令。交易动作另外有 Redis 持久幂等记录，不依赖该内存缓存。

### 5.3 重启恢复

OC 不会自动重放重启前 outcome 未知的交易命令：

1. 交易命令返回 `COMMAND_OUTCOME_UNKNOWN`，要求先对账。
2. 查询命令返回 `QUERY_INTERRUPTED`，可以使用新 `message_id` 重试。
3. 输出成功写入 reply/DLQ 后才 ACK pending entry。

## 6. gateway_order_id 跨重启稳定性

### 6.1 Relay 可见规则

对新版本上线后由 Relay 提交的订单：

1. Relay 提交的完整 `gateway_order_id` 保持不变。
2. 实时 `order.event/fill.event` 使用该 ID。
3. OC 重启后的 `order.list.query/fill.list.query` 仍使用同一个 ID。
4. Relay 不需要识别或生成 OC 内部 token。

OC 内部向华鑫 `SInfo` 写入约 19 字节的 token：

```text
oc#<fnv1a-64-hex>
```

完整映射保存在 Redis string key，默认 TTL 与交易幂等 TTL 一致，为 7 天。

### 6.2 Redis ACL 要求

除 Stream 命令外，OC Redis 账号需要允许：

1. `GET`
2. `SET ... NX EX`

键模式：

```text
relay:{env}:v1:huaxin:{gateway_id}:cmd.trade:idempotency:v1:*
relay:{env}:v1:huaxin:{gateway_id}:cmd.trade:order-identity:v1:*
```

如果 ACL 不允许写订单身份映射，OC 会在调用华鑫前拒绝新订单，不会降级为可能跨重启换 ID 的不安全模式。

### 6.3 升级边界

升级前已存在的订单没有 `oc#...` token 映射，继续使用既有 external canonical ID 规则。新映射只对升级后新提交的订单生效，不回写历史订单。

## 7. ETF 成交与划转

分类规则保持：

1. ETF 申购赎回成分划转进入 `transfer.event` 或查询的 `component_transfers[]`。
2. 普通成交进入 `fill.event` 或查询的 `items[]`。
3. 普通业务的 `price<=0` 不再自动伪装为 ETF 划转，而是进入 `INVALID_ORDINARY_FILL` DLQ。
4. 普通成交的证券、市场、方向、业务类型必须与关联订单一致，否则进入 `FILL_ORDER_CONTEXT_MISMATCH` DLQ。

Relay 不应把 `transfer.event` 写入普通成交表。

## 8. 建议验收用例

上线前至少执行：

1. 正常单笔下单、订单事件、成交事件、订单查询、成交查询。
2. 一个包含沪深证券的 batch，确认订单身份不串位。
3. 同 `message_id` 的查询并发重复投递，确认柜台只查询一次且 PEL 为 0。
4. 查询完成后重复同 `message_id`，确认完整 reply 序列可重放。
5. 同 `message_id` 改 payload，确认返回 `MESSAGE_ID_CONFLICT`。
6. 把 `order.submit` 写入 `cmd.query`，确认返回 `ACTION_STREAM_MISMATCH` 且没有下单。
7. 使用错误 `account_id`，确认返回 `ACCOUNT_MISMATCH`。
8. 使用重复 `gateway_order_id` 和新幂等键，确认 OC 在柜台前拒绝。
9. 提交一个长度超过 32 字节的 `gateway_order_id`，等待首个订单事件后重启 OC，再查订单，确认 ID 不变。
10. 验证可撤订单最终进入 `gateway_status=cancelled`。
11. 验证不可撤订单产生 `order.cancel.rejected`，且原订单状态不变。
12. 验证 ETF 成分划转不进入普通 fills。
13. 验证 `cmd.trade/cmd.query` consumer group 均为 `pending=0, lag=0`。

## 9. 当前验证状态

本地已完成：

1. CMake 全目标编译通过。
2. 每个逻辑修正均独立提交。
3. `git diff --check` 通过。

尚未完成：

1. 未在当前会话连接生产华鑫柜台执行交易测试。
2. 新增撤单拒绝事件、跨重启订单身份和 Redis ACL 需要 Relay/部署环境联合验收。

在联合验收通过前，建议保持生产交易开关由 Relay 人工控制。
