# OC 华鑫实际费用查询对接说明

日期：`2026-08-01`

## 1. 实现结论

OC 已接入华鑫现货交易 API 的 `ReqQryOrderFundDetail` / `OnRspQryOrderFundDetail`，新增 Relay 查询动作：

- action：`fee.list.query`
- result_type：`fee_page`
- 数据粒度：订单级
- 数据来源：`broker_order_fund_detail`
- 可查询范围：当前柜台交易日

华鑫当前 SDK 的订单和成交查询结构不包含实际费用字段，因此 `order.list.query`、`fill.list.query` 以及实时订单/成交事件中的原 `fee=0` 仍不能作为实际零费用使用。OC 已在这些数据中明确返回：

```json
{
  "fee": 0.0,
  "fee_complete": false,
  "fee_source": "unavailable",
  "fee_as_of": "2026-08-01T15:00:00.000+08:00"
}
```

## 2. 查询请求

请求写入对应账户的 `cmd.query` Stream，例如：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "command",
  "message_id": "msg-fee-query-20260801-001",
  "request_id": "req-fee-query-20260801-001",
  "correlation_id": "req-fee-query-20260801-001",
  "idempotency_key": "idem-fee-query-20260801-001",
  "action": "fee.list.query",
  "payload": {
    "account_id": "314000046830",
    "trade_date": "2026-07-31"
  }
}
```

`payload.trade_date` 可省略。传入时支持 `YYYY-MM-DD` 或 `YYYYMMDD`，但必须等于华鑫登录返回的当前交易日。

## 3. 成功响应

OC 在 `reply` Stream 返回一条终态 `fee_page`：

```json
{
  "protocol": "relay.stream.v1",
  "message_type": "reply",
  "origin_message_id": "msg-fee-query-20260801-001",
  "action": "fee.list.query",
  "status": "completed",
  "result_type": "fee_page",
  "payload": {
    "items": [
      {
        "fee_record_id": "huaxin-order-fee-20260731-external_huaxin_31400004683001_12001A180000001",
        "record_scope": "order",
        "account_id": "314000046830",
        "trade_date": "2026-07-31",
        "gateway_order_id": "external-huaxin-31400004683001-12001A180000001",
        "order_id": 123,
        "order_stream_id": "12001A180000001",
        "fill_id": "",
        "symbol": "600000",
        "exchange": "SH",
        "trade_side": "B",
        "business_type": "S",
        "order_amount": 954.0,
        "turnover": 954.0,
        "commission": 3.21,
        "stamp_tax": 0.0,
        "transfer_fee": 0.03,
        "handling_fee": 0.17,
        "regulatory_fee": 0.0,
        "settlement_fee": 0.0,
        "other_fee": 0.0,
        "total_fee": 3.41,
        "currency": "CNY",
        "fee_complete": true,
        "fee_source": "broker_order_fund_detail",
        "fee_as_of": "2026-08-01T15:00:00.000+08:00",
        "settled_at": "",
        "association_complete": true,
        "adapter_context": {
          "broker_account_id": "31400004683001",
          "order_local_id": "000001",
          "component_fee_total": 3.41,
          "total_fee_delta": 0.0
        }
      }
    ],
    "item_count": 1,
    "account_total_fee": 3.41,
    "currency": "CNY",
    "fee_complete": true,
    "fee_source": "broker_order_fund_detail",
    "fee_as_of": "2026-08-01T15:00:00.000+08:00",
    "association_complete": true,
    "record_scope": "order"
  },
  "chunk": {
    "is_last": true
  }
}
```

## 4. 字段语义

| 字段 | 说明 |
| --- | --- |
| `fee_record_id` | 稳定费用记录 ID；Relay 应按 `account_id + fee_record_id` 幂等覆盖。 |
| `record_scope` | 当前固定为 `order`，表示一笔订单只有一份总费用。 |
| `gateway_order_id` | 与 `order_page` 中同一柜台委托一致的订单 ID。 |
| `fill_id` | 当前固定为空；不得把订单总费用重复分摊到多笔成交。 |
| `commission` | 华鑫 `BrokerageFee`。 |
| `stamp_tax` | 华鑫 `StampTaxFee`。 |
| `transfer_fee` | 华鑫 `TransferFee`。 |
| `handling_fee` | 华鑫 `HandlingFee`。 |
| `regulatory_fee` | 华鑫 `RegulateFee`。 |
| `settlement_fee` | 华鑫 `SettlementFee`。 |
| `other_fee` | `TotalFee` 与已知费用分项之和的差额，通常为 `0`。 |
| `total_fee` | 华鑫 `TotalFee`，为权威订单总费用。 |
| `fee_complete` | 订单已到终态且成功关联订单快照时为 `true`；盘中未终态订单为 `false`。 |
| `association_complete` | 是否成功关联到 OC 的订单身份和业务枚举。 |
| `fee_as_of` | 本次从柜台获取费用的时间，不等同于券商结算时间。 |
| `settled_at` | 当前 SDK 不提供结算时间，因此返回空字符串。 |

`account_total_fee` 是本页所有去重后 `items[].total_fee` 的直接求和。Relay 应优先使用每条订单费用入账，并只将该汇总用于对账，不能再次计费。

## 5. Relay 接入规则

1. 以 `account_id + fee_record_id` 为唯一键执行 upsert。
2. `record_scope=order` 的费用只关联到对应订单，不按 `fill_id` 或成交笔数重复计算。
3. 只有 `fee_complete=true` 且 `association_complete=true` 时，才可将该订单费用标记为 finalized。
4. `fee_complete=false` 时可保存最新快照，但绩效仍保持 provisional，后续查询允许覆盖费用金额和完整性状态。
5. `order.list.query`、`fill.list.query` 和实时事件中 `fee_source=unavailable` 的 `fee` 不参与实际费用优先级。
6. `other_fee` 是柜台总费用与公开分项的残差；总账以 `total_fee` 为准，不应再把 `other_fee` 之外的分项重复相加计费。
7. 同一请求因 Redis 重投而回放时，按 `origin_message_id` 去重；不同请求查询同一交易日时，按稳定 `fee_record_id` 覆盖。

## 6. 失败码

| code | 含义 | Relay 建议 |
| --- | --- | --- |
| `BROKER_NOT_READY` | 华鑫尚未登录完成。 | 稍后用新 `message_id` 重试。 |
| `BROKER_ORDER_SYNC_IN_PROGRESS` | 初始订单快照尚未完成，暂时无法可靠关联费用。 | 等 heartbeat 的 `order_snapshot_ready=true` 后重试。 |
| `BROKER_TRADING_DAY_UNAVAILABLE` | 尚未获得当前柜台交易日。 | 稍后重试。 |
| `HISTORICAL_FEE_QUERY_UNSUPPORTED` | 请求日期不是当前柜台交易日。 | 不要把失败解释为零费用；切换历史交割数据源。 |
| `QUERY_SUBMIT_FAILED` | 华鑫拒绝提交费用查询。 | 记录告警并按查询退避策略重试。 |
| `QUERY_FAILED` | 华鑫异步返回查询错误。 | 保存错误信息，不更新已有费用。 |
| `QUERY_TIMEOUT` | 查询在配置超时时间内未完成。 | 使用新 `message_id` 重试。 |

## 7. 当前能力边界

华鑫 `CTORATstpQryOrderFundDetailField` 没有 `TradingDay` 查询条件，只能查询当前柜台交易日。因此本版本不能直接完成需求中对 `2026-07-29`、`2026-07-30` 的历史回补验收，也不会用空数组伪装成功。

要完成历史费用验收，还需要至少一种数据源：

1. 华鑫历史交割单 API；
2. 华鑫客户端导出的交割单文件；
3. 柜台提供的历史资金流水或历史订单资金明细接口。

取得历史数据源后可继续沿用相同 `fee_page` schema，建议将 `fee_source` 改为 `delivery_statement`，并补充真实 `settled_at`。

## 8. 验收命令

只读查询，不会下单：

```bash
cd /home/Titian_Cpp/oceanus
HX_ACCOUNT_ID=314000046830 HX_RELAY_GATEWAY_ID=314000046830 \
python3 test/trade.py fee --require-fee-records --timeout 60
```

默认不传 `--trade-date`，由 OC 使用华鑫登录返回的当前交易日。需要显式校验日期时，应传实际柜台交易日，而不是文档生成日期或自然日。

测试脚本会连续查询两次，检查：

- `fee_record_id` 完整且无重复；
- `item_count` 与数组长度一致；
- 明细费用之和与 `account_total_fee` 的误差不超过 `0.01 CNY`；
- 两次查询的记录 ID 集合和费用总额稳定。
