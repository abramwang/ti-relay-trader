# OC 实际交易费用数据需求

日期：`2026-08-01`  
范围：华鑫生产账户委托、成交、账户汇总及盘后交割数据  
优先级：P1（阻断 Relay 绩效从 provisional 升级为 finalized）

## 现象

Relay 已完整接收并落库 OC 当前提供的费用字段，但生产数据没有实际费用值：

- `orders.fee`：所有生产账户均为 `0`；债享5号 `314000046830` 共 `14,986` 笔订单，非零费用 `0` 笔。
- `fills.fee`：所有生产账户均为 `0`；债享5号共 `14,094` 笔成交，非零费用 `0` 笔。
- Redis 原始归档中债享5号的 `adapter_context.fee/nFee` 各出现 `56,207` 次，取值全部为 `0.0`。
- 债享5号 `99` 条资产快照的 `commission` 全部为 `0`；其他生产账户也没有非零账户手续费汇总。

Relay 对接文档和数据结构已经支持订单/成交 `fee`，不存在字段落库遗漏。`/home/Titans/resource/include/ti_trader_struct.h` 也已经预留：

- `TiRtnOrderStatus.nFee`：订单手续费。
- `TiRtnOrderMatch.nFee`：成交手续费。
- `TiRspAccountInfo.nCommission`：账户手续费汇总。

因此当前问题位于 OC/华鑫数据源：字段存在，但查询和回报链路没有给出实际值，或实际费用只存在于券商客户端使用的交割单、资金明细等其他接口。

## 债享5号连续净值阻断样本

`314000046830/2026-07-23` 已提供一组可复现的费用缺失证据：

- `performance_economic_nav.v2.1` 的日末资产和当日盈利与用户人工金标均只差 `0.004028` 元，账户级资金和估值方程已经闭合。
- 当日 155 个股票卖出贡献项全部为 `missing_fee_rule`，股票卖出金额 `1,567,206.34` 元。
- 不含逆回购预估利息的股票贡献为 `-1,558.66` 元，账户当日盈利为 `-2,634.545972` 元，正式归因残差为 `-1,075.885972` 元，即股票卖出金额的 `6.864992 bp`。
- 该量级符合卖出印花税、佣金和过户费组合，但 Relay 不会把人工净值残差反写成实际费用。
- 前一交易样本 `2026-07-22` 同样缺 155 项费用，但股票卖出金额仅 `51,611.95` 元，归因残差 `-277.892645` 元，低于 `537.812274` 元警告阈值；23 日随成交额放大后残差超过 `537.564516` 元阈值而 blocked。
- 逆回购预估净息 `618.493151` 元已经从正式 NAV/PnL 排除，不是这次残差来源。

因此 7 月 23 日当前不需要修改订单/成交数量，也不应通过放宽阈值发布。只要 OC 返回成交/订单/交割单实际费用，或用户提供该账户独立费率合同参数，Relay 即可重算该日并继续连续区间验收。

## 期望方案

### 方案 A：复用现有订单和成交查询

优先检查华鑫 `OnRspQryOrder`、`OnRspQryTrade` 及盘后查询结构是否提供实际费用。若可取得：

1. 订单级实际费用写入 `order_page/orders[].fee` 和 `adapter_context.nFee`。
2. 成交级实际费用写入 `fill_page/fills[].fee` 和 `adapter_context.nFee`。
3. 同时增加：
   - `fee_complete`：`true` 表示柜台确认费用完整；`false` 表示暂未结算或接口不提供。
   - `fee_source`：建议为 `broker_order_query`、`broker_trade_query`、`delivery_statement` 或 `unavailable`。
   - `fee_as_of`：费用数据在东八区的确认时间。
4. 未返回费用时不得只用 `fee=0` 表达；必须同时返回 `fee_complete=false`，区分真实零费用和字段缺失。

### 方案 B：新增交割单/资金明细查询

若华鑫订单和成交查询不提供实际费用，请新增 `fee.list.query` 或 `delivery_statement.list.query`。建议每条记录至少包含：

```json
{
  "account_id": "314000046830",
  "trade_date": "2026-07-29",
  "order_id": 123,
  "order_stream_id": "110010180000001",
  "fill_id": "000000000000001",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "business_type": "S",
  "commission": 3.21,
  "stamp_tax": 0.0,
  "transfer_fee": 0.03,
  "handling_fee": 0.17,
  "regulatory_fee": 0.0,
  "other_fee": 0.0,
  "total_fee": 3.41,
  "currency": "CNY",
  "fee_complete": true,
  "fee_source": "delivery_statement",
  "settled_at": "2026-07-29T16:30:00+08:00"
}
```

要求：

1. 尽量提供 `order_stream_id/fill_id`，使费用可精确关联 Relay 订单和成交。
2. 只有订单级总费用时允许 `fill_id` 为空，但同一订单只能返回一份总费用，避免按多个成交重复计费。
3. 只提供账户日汇总时，返回 `trade_date + account_id + total_fee`，仅用于总额对账；Relay 不会无依据地分摊到证券贡献。
4. 重复查询结果必须幂等；盘后费用更新允许覆盖同一稳定记录，并保留 `fee_as_of/settled_at`。
5. 股票、ETF 二级市场、ETF 申赎和逆回购需要保留不同 `business_type/trade_side`，不能混用费率语义。

## Relay 使用优先级

Relay 后续按以下顺序选择费用，且同一费用只计算一次：

```text
交割单或成交级实际费用
> 订单级实际费用
> 账户日汇总（只用于对账，不做证券分摊）
> Relay 账户费用规则估算
> missing_fee
```

在 `fee_complete=false` 或只有账户日汇总时，绩效继续保持 `provisional`，不会宣称为正式净收益。

## 联合验收

1. 使用 `314000046830` 的 `2026-07-29`、`2026-07-30` 订单和成交复测。
2. 至少一笔普通股票买入、一笔普通股票卖出和一笔逆回购返回非零实际费用或明确的 `fee_complete=false`。
3. 订单/成交费用汇总与券商官方客户端或交割单当日总费用一致，允许货币舍入误差不超过 `0.01 CNY`。
4. 同一查询重复执行两次，记录标识和费用总额稳定，不产生重复费用。
5. 实时回报暂时没有最终费用时可以返回 `fee_complete=false`；盘后权威查询必须能覆盖补全，且不得修改订单、成交的稳定身份字段。
