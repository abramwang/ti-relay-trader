# OC 成交查询终态被订单业务状态污染（2026-08-26）

## 1. 结论

生产账户 `314000045768`（富盈13号）的 `fill.list.query` 被 OC 稳定返回为查询级失败：

```text
status=failed
result_type=error_result
code=QUERY_FAILED
message=20046:市价单不能满足成交条件撤单
chunk.is_last=true
```

`20046` 实际属于一笔已经正常落账的 `300750` 卖单业务状态，不是成交查询基础设施错误。该订单已部分成交后撤余，最终状态为 `cancelled`。订单业务状态不应污染 `fill.list.query` 的终态。

这个现象与 `RELAY_COMPATIBILITY_NOTICE_20260805.md` 中已修复的 `order.list.query` 终止帧污染同源，但本次发生在成交查询链路。当天同账户的 `order.list.query` 已正常完成，说明 8 月 5 日的订单查询修复仍然有效；需要 OC 将相同的业务错误隔离原则覆盖到 `fill.list.query` 及其内部依赖查询。

Relay 没有把失败终态降级为成功，也没有使用实时账本里的已有成交绕过查询门禁。因此该账户未写入 `broker_close/close`，其余五个账户正常完成盘后结算。

## 2. 影响范围

- 交易日：`2026-08-26`
- Relay 标准账户：`314000045768`
- OC/柜台原始账户：`31400004576801`
- 受影响 action：`fill.list.query`
- 未受影响 action：
  - `account.asset.query`：唯一成功终态
  - `account.positions.query`：176 个 reply，唯一成功终态
  - `order.list.query`：202 个 reply，唯一成功终态
  - `fee.list.query`：本地账本已取得 201 条完整费用记录；本轮因成交查询先失败，任务未继续等待其查询终态
- Relay 当时可见账本摘要：
  - 资金更新时间：`2026-08-26 15:01:01.973 Asia/Shanghai`
  - 持仓更新时间：`2026-08-26 15:01:02.687 Asia/Shanghai`
  - 正持仓：`29`
  - 当日订单：`201`
  - 当日已有成交：`482`
  - 完整订单费用：`201`

已有 `482` 条成交只能证明实时事件已经落入 Relay，不能证明盘后权威查询完整，因此不能据此绕过 `fill.list.query` 成功终态。

## 3. 首次自动任务证据

15:01 盘后流水线发布的成交查询：

| 字段 | 值 |
| --- | --- |
| action | `fill.list.query` |
| origin message ID | `msg-fills-query-1787727661989152387-161` |
| request ID | `relay-1787727661989136063-9246` |
| command stream | `relay:prod:v1:huaxin:314000045768:cmd.query` |
| command stream ID | `1787727661992-0` |
| 发布时刻 | `2026-08-26 15:01:01.992 Asia/Shanghai` 附近 |

OC 返回的唯一终态：

| 字段 | 值 |
| --- | --- |
| reply message ID | `reply-1787727665349-10562` |
| reply stream | `relay:prod:v1:huaxin:314000045768:reply` |
| reply stream ID | `1787727665349-0` |
| received_at | `2026-08-26 15:01:06.449114 Asia/Shanghai` |
| status | `failed` |
| result_type | `error_result` |
| code | `QUERY_FAILED` |
| message | `20046:市价单不能满足成交条件撤单` |
| chunk.is_last | `true` |
| reply_count | `1` |
| terminal_count | `1` |

精简后的 Relay 查询终态：

```json
{
  "origin_message_id": "msg-fills-query-1787727661989152387-161",
  "account_id": "314000045768",
  "action": "fill.list.query",
  "expected_result_type": "fill_page",
  "state": "failed",
  "terminal": true,
  "success": false,
  "contradictory": false,
  "reply_count": 1,
  "terminal_count": 1,
  "final_reply": {
    "message_id": "reply-1787727665349-10562",
    "status": "failed",
    "code": "QUERY_FAILED",
    "message": "20046:市价单不能满足成交条件撤单",
    "result_type": "error_result",
    "is_last": true
  }
}
```

## 4. 人工补查稳定复现

用户确认 OC 尚未关闭后，Relay 于 15:04 再次只读发布同账户成交查询，没有下单或撤单：

| 字段 | 值 |
| --- | --- |
| origin message ID | `msg-fills-query-1787727879395227301-178` |
| request ID | `relay-1787727879395169257-9735` |
| command stream ID | `1787727879403-0` |
| 发布时刻 | `2026-08-26 15:04:39.431423 Asia/Shanghai` |
| reply message ID | `reply-1787727879428-10648` |
| reply stream ID | `1787727879429-0` |
| received_at | `2026-08-26 15:04:39.433861 Asia/Shanghai` |
| 结果 | `failed / error_result / QUERY_FAILED / is_last=true` |
| message | `20046:市价单不能满足成交条件撤单` |

第二次查询在约 30 毫秒内直接失败，仍没有任何 `fill_page`。这排除了 Relay 180 秒等待门限、HTTP 超时、Meridian 行情、PostgreSQL 快照写入和偶发分页迟到等原因，问题稳定存在于 OC/柜台成交查询终态生成链路。

## 5. 被错误引用的订单

`20046` 在 Relay 订单账本中有明确归属：

```json
{
  "account_id": "314000045768",
  "gateway_order_id": "external-huaxin-31400004576801-12001A180003653",
  "order_stream_id": "12001A180003653",
  "symbol": "300750",
  "trade_side": "S",
  "business_type": "S",
  "limit_price": 301.38,
  "order_qty": 2600,
  "cum_filled_qty": 1600,
  "leaves_qty": 0,
  "status": "cancelled",
  "gateway_status": "cancelled",
  "is_terminal": true,
  "adapter_status_code": -9,
  "adapter_status_name": "removed",
  "status_message": "20046:市价单不能满足成交条件撤单",
  "cancel_reason": "20046:市价单不能满足成交条件撤单",
  "created_at": "2026-08-26 10:06:36 Asia/Shanghai"
}
```

订单本身的落账语义是合理的：成交 `1,600` 股、剩余数量撤销、最终 `cancelled`，并保留券商业务原因。错误仅在于该订单原因又被用于结束完全独立的 `fill.list.query`。

## 6. 预期 wire 行为

### 6.1 成交查询正常完成

账户当日有成交时，OC 应发送零到多条分页 reply，最后只有一个成功终态：

```json
{
  "action": "fill.list.query",
  "status": "completed",
  "result_type": "fill_page",
  "payload": {
    "items": [],
    "component_transfers": []
  },
  "chunk": {
    "is_last": true
  }
}
```

中间页面使用 `status=partial/result_type=fill_page/chunk.is_last=false`。账户没有成交时也应返回一个空的 `fill_page/completed/is_last=true`，不能因为空结果或其他订单的业务状态返回查询失败。

### 6.2 订单业务原因重复出现在终止回调

如果华鑫成交查询、成交查询的订单映射前置步骤，或共享回调上下文在空终止帧再次携带 `20046`：

1. 该错误应继续保存在对应订单的 `status_message/cancel_reason/adapter_context`；
2. 不得写入 `fill.list.query` 的 `error_result`；
3. 如果成交遍历已经完整结束，应发送正常 `fill_page/completed/is_last=true`；
4. 可以在 final page 的可选 `adapter_context` 中保留忽略原因及来源回调，供审计使用；
5. 不得吞掉已经返回的普通成交或 ETF `component_transfers`。

### 6.3 真实成交查询失败

如果华鑫 `QueryMatches/OnRspQryTrade` 确实失败，仍必须返回查询级 `QUERY_FAILED`，Relay 会继续阻断。建议错误 payload 至少明确：

```json
{
  "query_incomplete": true,
  "partial_data_returned": false,
  "broker_callback": "OnRspQryTrade",
  "broker_error_id": -1,
  "broker_error_text": "..."
}
```

关键点是不能仅按 `ErrorID != 0` 判断查询失败；需要结合错误所属业务对象、来源回调、当前 pending action、是否已有查询数据，以及 `is_last` 判断。不要为了修复本样本而无条件忽略所有成交查询错误。

## 7. OC 建议排查点

1. 检查 `fill.list.query` 的 pending query 上下文是否只接收本次成交查询的回调和错误。
2. 检查 `QueryMatches/OnRspQryTrade` 之前是否为了订单 ID 映射调用了订单查询；若有，订单业务状态不得覆盖成交查询上下文的 `szErr/ErrorID`。
3. 检查订单查询和成交查询是否复用了同一错误缓存、最后错误字段或 commander pending entry。
4. 检查空 `is_last=true` 回调携带 `20046` 时，当前实现是否直接走统一 `QUERY_FAILED` 分支。
5. 将 `RELAY_COMPATIBILITY_NOTICE_20260805.md` 对 `OnRspQryOrder/order.list.query` 的分类规则推广到成交查询内部依赖，但保留真正 `OnRspQryTrade` 失败的审计语义。
6. 确保 reply 的 `origin_message_id/request_id/action/account_id` 始终来自当前 `fill.list.query`，不要从之前订单命令或其他账户 pending context 继承。
7. 修复后检查多账户并发查询，避免共享错误状态使一个账户的订单业务错误污染另一个账户。

上述第 2、3 点是根据 wire 现象提出的重点排查方向，不代表 Relay 已能确定 OC 内部具体代码根因。最终根因应以 OC 的华鑫回调日志和 pending context 跟踪结果为准。

## 8. 联合验收标准

OC 更新并重启后，Relay 将执行只读复测，不会下单或撤单。

### 8.1 单接口复测

对 `314000045768` 连续执行两次 `fill.list.query`：

- 两次均为 `state=completed`；
- `success=true`、`contradictory=false`；
- `terminal_count=1`；
- 唯一终态为 `status=completed/result_type=fill_page/chunk.is_last=true`；
- 当日成交和 ETF 划转分页完整，不丢失、不重复；
- `20046` 不再出现在成交查询 `error_result`；
- `300750` 订单仍为 `cancelled`，成交 `1,600/2,600`，业务原因保留；
- 同账户 `order.list.query` 继续保持成功，不能回归 8 月 5 日问题。

### 8.2 多账户与 Stream 验收

- 六账户 `cmd.query` 最终 `pending=0, lag=0`；
- 每条查询只有一个 terminal reply；
- 没有新增 `BAD_RECOVERED_COMMAND`、上下文错配或 parser/ledger DLQ；
- 其他账户查询不受富盈13号单笔 `20046` 影响。

### 8.3 盘后恢复验收

单接口通过后，Relay 将按以下顺序恢复 `2026-08-26`：

1. 重新执行富盈13号 `post_close_capture`，取得资金、持仓、订单、成交和费用全部成功终态；
2. 写入该账户不可变 `broker_close`；
3. 对当天全部六账户重新执行 `post_close_settlement --skip-refresh`，形成完整账户集合；
4. 重跑 `performance_daily`；
5. `/v1/reconciliations/review-report?trade_date=20260826` 不再出现富盈13号 `post_close snapshot is missing`；
6. 复核开放 reconciliation break、DLQ pending、Stream lag/pending 均为 0。

## 9. Relay 侧当前处置

- 保持查询终态严格门禁，不增加针对 `20046` 的 Relay 白名单。
- 不把实时事件已有的 482 条成交视为盘后查询已完成。
- 不用盘前、盘中或当前账本数据冒充富盈13号 `broker_close`。
- 不影响其余五账户已经完成的 `2026-08-26` 收盘快照和结算。
- 等待 OC 修复后再补查和补结算，原始 Redis 报文、查询状态和任务报告继续保留供审计。

## 10. OC 修复交付与待验收状态

OC 已交付并部署以下修复：

- `fa43056 fix(huaxin): isolate order status from fill queries`
- `fd0b89b test(huaxin): add repeated fill query incident retest`
- 兼容通知：`RELAY_COMPATIBILITY_NOTICE_20260826.md`

通知中的成功/失败分类、唯一终态和审计字段与 Relay 现有协议兼容，不需要 Relay schema 或解析代码改动。

Relay 于 `2026-08-26 15:37:19 Asia/Shanghai` 发起部署后第一轮只读复测：

- origin message ID：`msg-fills-query-1787729839067062486-179`
- command stream ID：`1787729839043-0`
- 截至 `15:38:36`：`state=pending`、`reply_count=0`、`terminal_count=0`
- OC 最新 heartbeat：`2026-08-26 15:25:34.953 Asia/Shanghai`

当前 OC 已无持续心跳，疑似被 15:30 关停计划停止，因此本轮结果只能判定为“未消费/待验收”，不能判定修复通过或失败。本地自动等待循环已经停止，不再追加第二条命令；第一条只读命令保留在 `cmd.query`，OC 临时恢复后应先观察它是否被正常消费并产生唯一成功终态，再继续两轮稳定性和盘后恢复验收。

## 11. 部署后首次生产验收失败

OC 于 `2026-08-26 15:39 Asia/Shanghai` 临时恢复运行。第 10 节遗留命令在柜台登录完成前被消费并返回 `BROKER_NOT_READY`，该行为符合协议，不计入修复验收。

heartbeat 随后持续显示：

```text
state=UP
redis_ready=true
broker_ready=true
order_snapshot_ready=true
pending_query_count=0
```

Relay 在确认 ready 后发布全新的只读成交查询：

| 字段 | 值 |
| --- | --- |
| origin message ID | `msg-fills-query-1787730007090739891-180` |
| request ID | `relay-1787730007090708437-9966` |
| command stream ID | `1787730007065-0` |
| reply message ID | `reply-1787730007085-20` |
| reply stream ID | `1787730007090-0` |
| received_at | `2026-08-26 15:40:07.128657 Asia/Shanghai` |

结果仍为：

```text
state=failed
success=false
contradictory=false
reply_count=1
terminal_count=1
status=failed
result_type=error_result
code=QUERY_FAILED
message=20046:市价单不能满足成交条件撤单
chunk.is_last=true
```

新版 OC 已增加诊断字段，原始 `payload` 为：

```json
{
  "code": "QUERY_FAILED",
  "message": "20046:市价单不能满足成交条件撤单",
  "correlation_id": "msg-fills-query-1787730007090739891-180",
  "broker_callback": "OnRspQryTrade",
  "broker_error_id": 0,
  "query_incomplete": true,
  "broker_error_text": "20046:市价单不能满足成交条件撤单",
  "partial_data_returned": true
}
```

这里出现了本轮最关键的新证据：`broker_error_id=0`，业务码 `20046` 只存在于 `broker_error_text/message`。如果当前修复只判断数值 `ErrorID == 20046`，生产路径不会进入 `single_order_business_status` 分支，因此仍会生成 `QUERY_FAILED`。

OC 下一轮建议重点修正并测试：

1. 在驱动层确认原始 `pRspInfoField.ErrorID` 是否本来就是 `0`，以及 `20046` 来自 `ErrorMsg`、成交行状态还是内部 `szErr` 聚合字段。
2. 如果原始数值错误码在上送前丢失，应先修复结构体传递，保留真实错误码和来源，不要仅在 commander 末端猜测。
3. 如果华鑫生产接口确实返回 `ErrorID=0 + ErrorMsg=20046...`，分类器需要覆盖这一实际组合；文本识别应限定为规范化后的明确代码前缀 `20046:`，并同时检查回调来源、成交行/末帧和查询上下文，不能无条件忽略其他错误文本。
4. `broker_error_id=0` 时不应仅凭共享 `szErr` 生成查询失败；需要确认该文本属于当前成交查询，而非对应订单或之前回调的残留状态。
5. 新增与本次生产 payload 完全一致的回归测试：`OnRspQryTrade`、`broker_error_id=0`、`broker_error_text` 以 `20046:` 开头、`partial_data_returned=true`。预期唯一终态为 `fill_page/completed/is_last=true`，此前成交行全部保留。
6. 保留现有真正查询失败测试，确保非 `20046` 或无法明确归类的错误仍为 `QUERY_FAILED`。

由于第一条全新查询已经失败，Relay 停止后续重复查询和盘后恢复。本轮 OC 修复尚未通过生产验收。
