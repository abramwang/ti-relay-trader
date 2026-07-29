# OC 交易账表数据质量修改请求

提交日期：2026-07-29 Asia/Shanghai

优先级：P0

涉及链路：华鑫柜台订单实时推送、成交实时推送、订单查询、成交查询、ETF 申购赎回及篮子子单

## 修改目标

OC 输出的每笔订单和成交必须能够由 Relay 无歧义、可重复地关联。Relay 将 `gateway_order_id` 视为不透明业务键，不会根据证券、数量或报文到达顺序猜测、拆分或重写 OC 的订单归属。

必须满足：

1. 一个 `account_id + trade_date + gateway_order_id` 只能表示一笔订单。
2. 同一订单的实时 `order.event`、`fill.event`、`order.list.query` 和 `fill.list.query` 必须使用相同 `gateway_order_id`。
3. 不同证券、不同柜台 `order_id` 的篮子子单不能共用一个 `gateway_order_id`。
4. 成交的证券、方向、业务类型和订单编号必须与关联订单一致。

## P0-1 空 order_stream_id 导致子单 ID 冲突

### 现象 A：2026-07-29 实时冲突

账户：`307000051388`

Redis Stream：`relay:prod:v1:huaxin:307000051388:event`

| stream_id | message_id | gateway_order_id | symbol | order_id | order_stream_id | reject_message |
| --- | --- | --- | --- | ---: | --- | --- |
| `1785288336652-0` | `event-1785288336692-649` | `BTR.90322777\|J.0` | `517180` | 3 | 空 | `VIP:找不到持仓` |
| `1785288336741-0` | `event-1785288336781-655` | `BTR.90322777\|J.0` | `159623` | 10 | 空 | `VIP:价格超过涨跌停板限制` |

两条事件间隔约 89ms，证券和 `order_id` 均不同，但共用同一个 `gateway_order_id`。Relay 的订单主表只能保留其中一笔，属于源头不可逆信息冲突。

### 现象 B：2026-07-28 ETF 篮子冲突

账户：`314000045768`

Redis Stream：`relay:prod:v1:huaxin:314000045768:event`

- `gateway_order_id=etfarb#588200.SSE#100338#s`
- 32 个不同证券、32 个不同 `order_id`
- `order_stream_id` 全部为空
- stream ID 范围：`1785204222989-0` 至 `1785204223387-0`
- 到达时间范围：10:03:59.225 至 10:03:59.539
- 示例：`688002/order_id=477`、`688120/order_id=503`、`688313/order_id=529`、`688981/order_id=571`

### OC 修改要求

1. 有 `order_stream_id` 时继续使用单笔委托级 ID：

   `external-huaxin-{broker_account_id}-{order_stream_id}`

2. `order_stream_id` 为空时，必须使用 OC 可以稳定重建的单笔子单 ID。该 ID 至少需要区分：

   - 交易日
   - 柜台 `order_id`
   - 篮子 ID
   - `symbol + exchange`
   - 必要时增加方向和业务类型

3. 具体字符串格式由 OC 决定，Relay 只要求它稳定且唯一。实时推送和查询回包必须使用同一生成函数，不能各自拼接。
4. 篮子 ID 应单独放在 `basket_id` 或 adapter context 中，不能直接作为多笔子单共用的 `gateway_order_id`。
5. 如果 OC 无法为某条记录确定单笔订单身份，不要将其作为普通 `order.event/fill.event` 推送；应进入 OC 错误处理或 DLQ，避免覆盖另一笔订单。
6. 拒单同样是一笔独立订单，不能因为没有 `order_stream_id` 就复用篮子级或旧订单 ID。

## P0-2 订单与成交关联字段错配

完整历史重放后发现 17 条真实成交的证券与同日关联订单不同，全部发生在 2026-07-06 以前。

| trade_date | gateway_order_id 后缀 | 订单证券/数量 | 成交证券/数量 | fill_id | Redis stream_id |
| --- | --- | --- | --- | --- | --- |
| 2026-06-15 | `110010180008903` | `204001.SH / 4,550` | `601728.SH / 100` | `0000000006109599` | `1781510830179-0` 查询页 |
| 2026-06-15 | `12001A180000592` | `301150.SZ / 60` | `000620.SZ / 100` | `01050000073993970000` | `1781510830179-0` 查询页 |
| 2026-06-17 | `110010180005032` | `300870.SZ / 100` | `204001.SH / 2,140` | `1200801003109815` | `1781665479331-0` |
| 2026-06-18 | `110010180000354` | `603296.SH / 80` | `688800.SH / 200` | `0000000000729605` | `1781746213886-0` |
| 2026-06-18 | `110010180000354` | `603296.SH / 80` | `688800.SH / 80` | `0000000000729608` | `1781746213903-0` |
| 2026-06-18 | `110010180000361` | `000601.SZ / 100` | `603290.SH / 100` | `0000000000794241` | `1781746216537-0` |
| 2026-06-18 | `110010180001828` | `002979.SZ / 200` | `603993.SH / 200` | `0000000015419903` | `1781747135020-0` |
| 2026-06-18 | `110010180001829` | `300655.SZ / 200` | `600166.SH / 100` | `0000000015407863` | `1781747133979-0` |
| 2026-06-18 | `12001A180000082` | `300397.SZ / 200` | `300031.SZ / 100` | `01050000022233820000` | `1781746228193-0` |
| 2026-06-18 | `12001A180001295` | `688676.SH / 200` | `300438.SZ / 100` | `01010000177756300000` | `1781747106146-0` |
| 2026-06-18 | `12001A180001296` | `002929.SZ / 100` | `002263.SZ / 100` | `01040000164142100000` | `1781747106240-0` |
| 2026-06-22 | `110010180014746` | `000408.SZ / 100` | `204001.SH / 4,450` | `1200801006661140` | `1782111424439-0` |
| 2026-06-23 | `110010180014642` | `000062.SZ / 100` | `204001.SH / 3,800` | `1200801006188148` | `1782197948696-0` |
| 2026-06-26 | `12001A180003654` | `159326.SZ / 1,000,000` | `159900.SZ / 1` | `03010000135805540026` | `1782441446942-0` |
| 2026-07-01 | `12001A180003120` | `159566.SZ / 1,000,000` | `159900.SZ / 1` | `03010000001058860016` | `1782870863761-0` |
| 2026-07-02 | `110010180013892` | `688469.SH / 300` | `204001.SH / 8,180` | `1200801007746176` | `1782975969890-0` |
| 2026-07-06 | `12001A180003719` | `159326.SZ / 1,000,000` | `159900.SZ / 1` | `03010000004866600026` | `1783305343339-0` |

### OC 修改要求

1. 生成 `fill.event` 和 `fill.list.query` 项目时，必须从成交实际归属的单笔订单上下文取得：

   - `gateway_order_id`
   - `order_id`
   - `order_stream_id`
   - `symbol`
   - `exchange`
   - `trade_side`
   - `business_type`

2. 不要使用当前篮子、最近订单或客户端级 ID 覆盖成交本身携带的订单关联字段。
3. 同一 `fill_id` 的实时推送和查询回包必须使用相同订单关联字段。
4. OC 输出前增加一致性检查：

   `fill.symbol/exchange/trade_side/business_type == referenced_order.*`

5. 无法闭合时不要发布普通成交；记录原始柜台数据和关联候选，进入错误处理或 DLQ。
6. 上表三条 `159900.SZ / 1` 需要确认是普通成交还是 ETF 业务划转。若属于申赎成分或现金替代划转，应按 `transfer.event / etf_component_transfer.event` 输出，不应进入普通 `fill.event`。

## P0-3 ETF 划转与普通成交必须分离

1. ETF 申购赎回成分证券划转、现金替代或 0 价记录使用：

   `transfer.event` 或 `etf_component_transfer.event`

2. 普通 `fill.event` 必须满足：

   - `price > 0`
   - `qty > 0`
   - 有稳定 `fill_id`
   - 能关联到唯一单笔订单

3. ETF 申购/赎回主订单使用业务方向 `P/R`；成分股划转不伪装成普通股票买卖成交。
4. 查询链路与实时链路使用相同分类逻辑，避免实时是 transfer、盘后查询又变回 fill。

## P1 字段和时间语义

### order.event / order_page

每笔订单应完整携带：

- `account_id`
- `gateway_order_id`
- `order_id`
- `order_stream_id`，柜台没有时允许空，但 `gateway_order_id` 仍必须唯一
- `trade_date`
- `symbol + exchange`
- `trade_side + business_type`
- 委托价格和数量
- 累计成交量、撤单量、废单量、剩余量
- 标准状态、柜台状态和 `is_terminal`
- `created_at`
- `last_updated_at`
- 终态时提供真实 `terminal_at`

### fill.event / fill_page

每笔成交应完整携带：

- `fill_id`
- 与订单一致的 `gateway_order_id/order_id/order_stream_id`
- `trade_date`
- `symbol + exchange`
- `trade_side + business_type`
- `price + qty`
- `matched_at`

### 时间要求

1. 时间统一使用 ISO 8601，并显式携带 `+08:00`。
2. `created_at` 是柜台委托创建时间，不是 OC 查询回包时间。
3. `terminal_at` 是订单成交、撤单或拒绝进入终态的时间，不是盘后查询时间。
4. `last_updated_at` 是柜台最后状态变化时间，不是 `order.list.query` 的响应生成时间。
5. `produced_at` 是 OC 生成当前 Redis 报文的时间，可以晚于业务时间。

## 联合验收标准

OC 更新后需要在一个真实 ETF 申购/赎回篮子、一个普通股票篮子和一笔普通单上复测：

1. 同一账户、交易日内，每个 `gateway_order_id` 只对应一个：

   `symbol + exchange + order_id`

2. `order.event` 与 `order.list.query` 对同一订单使用相同 `gateway_order_id`。
3. `fill.event` 与 `fill.list.query` 对同一成交使用相同订单关联字段。
4. 每笔普通成交的证券、方向和业务类型与订单一致。
5. 每笔订单满足：

   `sum(real fill.qty) = cum_filled_qty <= order_qty`

6. ETF 成分划转不进入普通 `fills`，0 价记录只出现在 transfer 链路。
7. 盘后查询后，已成交/已撤订单不残留拒单原因，实际业务拒单保留完整错误码和错误信息。
8. 终态时间属于对应交易日；超过 5 秒的 `terminal_at < created_at` 视为异常。
9. Relay 质量检查结果：

   - `orphan_fill_groups = 0`
   - `fill_order_security_mismatch = 0`
   - `fill_order_side_mismatch = 0`
   - `fill_order_business_type_mismatch = 0`
   - `order_fill_quantity_mismatch = 0`
   - `terminal_time_missing = 0`
   - `terminal_trade_date_mismatch = 0`

## OC 回传信息

请 OC 修改完成后回传：

1. 空 `order_stream_id` 时的新 `gateway_order_id` 生成规则。
2. 实时和查询是否调用同一个 ID 生成函数。
3. 成交关联订单上下文的取值来源。
4. ETF transfer/fill 分类条件。
5. 修改后的对接文档位置和版本。
6. 可复测账户、交易日、篮子 ID 和预计交易时间。
