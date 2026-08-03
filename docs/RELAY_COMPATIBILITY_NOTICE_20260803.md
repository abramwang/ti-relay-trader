# OC 华鑫 2026-08-03 修正兼容通知

适用程序：`oc_trader_commander_huaxin`

## 1. 资金查询终态修正

旧版本收到华鑫资金数据帧后立即发布：

```text
asset_page partial -> QUERY_EMPTY_RESULT failed
```

原因是华鑫在有效资金行之后还会发送一个空的 `isLast=true` 终止帧，OC 将该终止帧错误地解释为整次查询无数据。

修正后，OC 会缓存资金行并等待查询结束，只发布一个终态响应：

```json
{
  "action": "account.asset.query",
  "status": "completed",
  "result_type": "asset_page",
  "payload": {
    "account": {}
  },
  "chunk": {
    "is_last": true
  }
}
```

只有整次查询直到终止帧都没有资金行时，才返回：

```text
status=failed, result_type=error_result, code=QUERY_EMPTY_RESULT
```

Relay 应继续按 `origin_message_id` 关联查询终态，并要求 `status=completed`、`result_type=asset_page`、`chunk.is_last=true` 后再把命令标记为成功。数据新鲜度不能替代命令终态检查。

## 2. 持仓成本增强

`position_page.items[]` 新增三个可选字段：

```json
{
  "avg_cost": 9.54,
  "total_cost": 954.0,
  "avg_cost_source": "broker_total_position_cost",
  "cost_complete": true
}
```

字段规则：

- `avg_cost` 优先按华鑫 `TotalPosCost / CurrentPosition` 计算。
- 柜台没有有效总成本时，回退到 `HistoryPosPrice`，此时 `avg_cost_source=broker_history_position_price`。
- 两种成本都不可用时，`avg_cost_source=unavailable`、`cost_complete=false`。
- `total_cost` 直接来自华鑫 `TotalPosCost`。
- 原有字段未删除，Relay 可以兼容旧 schema 后逐步采用新增字段。

注意：历史 `market_value` 字段在华鑫实现中承载的也是 `TotalPosCost`，并非按最新行情计算的市值。Relay 计算 NAV 时应使用行情价格计算市值，不应直接使用该字段。

## 3. 复测方式

部署新版 OC 并等待 heartbeat 中 `broker_ready=true` 后，执行只读资金查询：

```bash
cd /home/Titian_Cpp/oceanus
HX_ACCOUNT_ID=314000046830 HX_RELAY_GATEWAY_ID=314000046830 \
python3 test/trade.py asset --timeout 30
```

通过条件：

- 只出现一条该 `origin_message_id` 的终态 reply；
- `status=completed`；
- `result_type=asset_page`；
- `chunk.is_last=true`；
- `payload.account` 非空；
- 对应 `cmd.query` consumer group 最终 `pending=0, lag=0`。

持仓复测使用：

```bash
python3 test/trade.py queries --timeout 60
```

对当日新建且仍持有的仓位，检查 `avg_cost`、`total_cost`、`avg_cost_source` 和 `cost_complete`，并与华鑫客户端持仓成本核对。
