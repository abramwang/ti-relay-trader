# OC 华鑫订单查询终态修复通知

日期：`2026-08-05`

适用程序：`oc_trader_commander_huaxin`

## 1. 问题根因

华鑫 `OnRspQryOrder` 可能用两种方式返回单笔订单的业务状态：

1. `pOrderField` 携带具体订单，同时 `pRspInfoField.ErrorID` 或 `StatusMsg` 携带业务原因；
2. 具体订单已在前面的数据帧返回，空的 `isLast=true` 终止帧再次携带同一个业务原因。

旧版驱动只要看到 `ErrorID != 0` 就立即生成查询级 `QUERY_FAILED`，OC 又把任何 `TiRspQryOrder.szErr` 都解释为查询基础设施错误。因此单笔市价单的 `20046:市价单不能满足成交条件撤单` 污染了整个 `order.list.query` 终态。

## 2. 修复后行为

### 2.1 业务状态与具体订单同行

OC 正常返回该订单的 `order_page` 数据，不终止查询。新增可选字段：

```json
{
  "gateway_order_id": "external-huaxin-...",
  "gateway_status": "cancelled",
  "status_message": "20046:市价单不能满足成交条件撤单",
  "cancel_reason": "20046:市价单不能满足成交条件撤单",
  "adapter_context": {
    "broker_status_text": "20046:市价单不能满足成交条件撤单",
    "error_text": ""
  }
}
```

如果订单状态为 rejected，则使用：

```json
{
  "reject_code": "BROKER_REJECTED",
  "reject_message": "..."
}
```

### 2.2 空终止帧重复携带 20046

当本次查询已经成功发布订单数据后，OC 将该已知单笔业务状态识别为重复终止信息，并发送正常 final page：

```json
{
  "status": "completed",
  "result_type": "order_page",
  "payload": {
    "items": [],
    "broker_terminal_status_ignored": true,
    "adapter_context": {
      "error_id": 20046,
      "error_text": "20046:市价单不能满足成交条件撤单",
      "classification": "single_order_business_status"
    }
  },
  "chunk": {
    "is_last": true
  }
}
```

`broker_terminal_status_ignored` 仅是审计信息，不代表丢弃对应订单。具体订单及其业务原因已在前面的 `order_page` 数据帧返回。

### 2.3 真正的查询失败

不携带订单数据、且不属于上述已知重复业务状态的错误仍返回 `QUERY_FAILED`。错误 payload 新增：

```json
{
  "query_incomplete": true,
  "partial_data_returned": true
}
```

Relay 应继续将这种终态视为失败。`partial_data_returned=true` 只表示失败前曾收到部分页面，不能把查询降级为成功。

## 3. Relay 兼容建议

1. 保持现有查询门禁：同一 `origin_message_id` 必须只有一个终态，且成功条件为 `status=completed`、`result_type=order_page`、`chunk.is_last=true`。
2. `status_message`、`cancel_reason`、`broker_terminal_status_ignored` 和新增 `adapter_context` 均为可选增量字段，未知字段可安全忽略。
3. 单笔 `20046` 应保留在订单业务状态中，不生成查询级 attention。
4. 真正的 `QUERY_FAILED` 即使已经收到 partial 页面也必须保持账户级 attention，等待重新查询。
5. `completed` 空 final page 只表示分页结束，不能覆盖或删除前面已经归档的订单页面。

## 4. 联合复测

部署新版 OC 后执行只读测试，不会下单或撤单：

```bash
cd /home/Titian_Cpp/oceanus
HX_ACCOUNT_ID=314000046830 HX_RELAY_GATEWAY_ID=314000046830 \
python3 test/trade.py order --timeout 90
```

通过条件：

- 连续两次查询均为 `completed`；
- 两次 `result_type` 均为 `order_page`；
- 两次 final reply 均为 `chunk.is_last=true`；
- 每次 `terminal_count=1`、`contradictory=false`；
- `20046` 对应订单仍保留 cancelled 终态及业务原因；
- `cmd.query` 最终 `pending=0, lag=0`；
- Relay 可以重新完成账户 `314000046830` 的 close 快照。
