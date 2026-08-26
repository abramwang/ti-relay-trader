# OC 华鑫成交查询终态修复通知

日期：`2026-08-26`

适用程序：`oc_trader_commander_huaxin`

关联事故：`OC_FILL_QUERY_TERMINAL_INCIDENT_20260826.md`

## 1. 问题根因

华鑫 `OnRspQryTrade` 回调可能在成交查询期间重复携带单笔订单业务状态：

```text
20046:市价单不能满足成交条件撤单
```

该状态属于具体订单，不代表 `QueryMatches` 查询失败。旧版驱动只要看到
`pRspInfoField.ErrorID != 0` 就丢弃当前成交行，并生成查询错误；OC 随后把该错误直接发布为
`fill.list.query / QUERY_FAILED`。因此真实成交未进入 `fill_page.items`，盘后权威成交查询被错误阻断。

## 2. 修复后行为

### 2.1 业务状态与成交行同行

当 `ErrorID=20046` 且回调携带有效 `pTradeField` 时，驱动继续解析并上送成交行，不再丢弃成交数据。
订单的 `cancelled` 状态、部分成交数量及 `status_message/cancel_reason` 仍由订单链路保留，不受本次修复影响。

### 2.2 空回调重复携带 20046

OC 仅将明确的 `20046` 分类为 `single_order_business_status`：

- 非末帧只忽略该业务状态并继续收集后续成交；
- 末帧发送唯一成功终态，并包含此前累计的普通成交和 ETF 成分股划转；
- 当日没有成交时也返回空的成功 `fill_page`，不会伪造查询失败。

带审计信息的成功终态示例：

```json
{
  "action": "fill.list.query",
  "status": "completed",
  "result_type": "fill_page",
  "payload": {
    "items": [],
    "item_count": 0,
    "component_transfers": [],
    "component_transfer_count": 0,
    "broker_terminal_status_ignored": true,
    "adapter_context": {
      "broker_callback": "OnRspQryTrade",
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

`broker_terminal_status_ignored` 及 `payload.adapter_context` 是可选审计字段。正常末帧未携带
`20046` 时不会出现这两个字段。

### 2.3 真正的成交查询失败

除 `20046` 外的华鑫成交查询错误仍返回 `QUERY_FAILED`。错误 payload 现在保留来源回调、券商错误及
是否已经收到部分数据：

```json
{
  "query_incomplete": true,
  "partial_data_returned": true,
  "broker_callback": "OnRspQryTrade",
  "broker_error_id": -1,
  "broker_error_text": "..."
}
```

即使 `partial_data_returned=true`，该终态仍表示本次快照不完整，Relay 不得降级为成功或用于生成
`broker_close`。

## 3. Relay 兼容要求

1. 现有主协议和 Stream key 不变，Relay 不需要迁移数据库 schema。
2. 成功条件保持为 `status=completed`、`result_type=fill_page`、`chunk.is_last=true`，每个查询只能有一个终态。
3. `payload.items[]` 仍是普通成交；`payload.component_transfers[]` 仍是 ETF 成分股划转，两者不得混合入账。
4. `broker_terminal_status_ignored` 和新增审计字段可直接归档或忽略，不应生成查询失败 attention。
5. Relay 不需要也不应增加 `20046` 白名单；分类责任由 OC 在华鑫适配层完成。
6. 真实 `QUERY_FAILED` 必须继续阻断盘后快照，不能用实时成交账本替代权威查询结果。
7. `20046` 对应订单仍应保持 `gateway_status=cancelled`、已成交 `1600/2600` 及原始业务原因。

## 4. 部署与只读复测

代码提交：

- `fa43056 fix(huaxin): isolate order status from fill queries`
- `fd0b89b test(huaxin): add repeated fill query incident retest`

重新编译并部署 OC 后，在仓库根目录执行：

```bash
cd /home/Titian_Cpp/oceanus
HX_ACCOUNT_ID=314000045768 \
HX_RELAY_GATEWAY_ID=314000045768 \
python3 test/trade.py fill --timeout 180
```

`fill` 模式只发送两次 `fill.list.query`，不会下单或撤单。脚本自动检查：

- 两次查询均返回唯一的 `completed/fill_page/is_last=true` 终态；
- final page 的普通成交数和 ETF 划转数与对应 count 字段一致；
- 每条记录均有 `fill_id/gateway_order_id/order_stream_id`；
- 单页内没有重复成交身份；
- 两次查询返回稳定、完整的成交和划转身份集合；
- 终态之后两秒内没有同一 `origin_message_id` 的第二个终态。

生产复测还应确认：

- 当日真实成交数量符合柜台结果，不能只看到实时账本已有数据；
- `300750` 订单仍为部分成交后撤余，业务原因未丢失；
- 同账户 `order.list.query` 继续成功；
- 六账户 `cmd.query` 最终均为 `pending=0, lag=0`；
- 没有新增 parser/ledger DLQ、上下文错配或矛盾终态。

## 5. 发布前验证结果

本次修改已通过：

- `git diff --check`；
- `python3 -m py_compile oceanus/test/trade.py`；
- `python3 oceanus/test/trade.py --help`；
- `cmake --build oceanus/src/oc_trader_commander_huaxin/build -j2`。

C++ 构建仅出现项目原有的 `strncpy` 截断告警，没有新增编译错误。
