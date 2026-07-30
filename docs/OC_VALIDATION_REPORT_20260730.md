# OC v1.1 次交易日数据质量验收报告

验收日期：`2026-07-30 Asia/Shanghai`

涉及版本：OC `THIRD_PARTY_INTEGRATION_GUIDE.md v1.1`、Relay migration `000012-000016`

验收方式：只读检查生产 PostgreSQL 账本、`raw_stream_messages`、DLQ、交易质量接口和 15:05 盘后结算结果；未触发交易命令。

## 结论

OC v1.1 的防护和分流机制已经生效，但实时订单上下文错位仍可复现，因此本轮结论为 **部分通过，不能关闭 P0 问题**。

- 通过：空 `order_stream_id` 的订单已使用单笔稳定 ID，今天 32 笔此类订单对应 32 个不同订单身份，没有再次发生篮子级 ID 覆盖。
- 通过：OC 发布的 869 条普通成交在实时 `fill.event` 与盘后 `fill_page` 中逐笔一致，真实成交与账本订单的证券、方向、业务类型错配为 0，孤立成交组为 0。
- 通过：124 条 ETF 成分划转全部进入 `etf_component_transfers`，其中申购 49 条、赎回 75 条；0 价/无估值成分记录没有进入普通 `fills`。
- 通过：终态时间缺失、终态跨交易日、终态早于委托超过 5 秒和非拒单错误残留均为 0。
- 未通过：账户 `307000051387` 的一个篮子中，14 个实时 `order.event` 与盘后 `order_page` 对同一 `gateway_order_id` 给出了不同证券。
- 防护生效：上述错位进一步导致 16 个真实成交无法与 OC 内存中的订单上下文闭合；OC 没有发布错误 `fill.event`，而是生成 32 条 `FILL_ORDER_CONTEXT_MISMATCH` DLQ（16 个不同 `fill_id` 在实时和盘后查询各出现一次）。
- Relay 次生影响：缺少真实成交后，14 条历史兼容用 `relay-summary:*` 合成成交保留了错误实时订单证券；盘后权威订单查询修正订单主表后，这些 summary 与订单产生证券错配。真实成交表本身没有被错误数据污染。

## 当日样本规模

| 项目 | 数量 |
| --- | ---: |
| 订单 | 682 |
| 普通真实成交 | 869 |
| Relay 合成成交 | 14 |
| ETF 成分划转 | 124 |
| OC data-quality DLQ 原始消息 | 32 |
| OC data-quality DLQ 不同成交 | 16 |
| 盘后结算对账差异 | 15 |

15 条盘后差异由 9 条订单/成交数量不一致和 6 条收盘后仍为非终态的订单组成。其中 3 条数量不一致是 `P/R + business_type=E` 的 ETF 主订单没有普通成交明细，属于 Relay 普通成交检查口径需要单独排除的项目；其余 6 条来自本报告的 OC 实时上下文错位。

## P0 实时与查询身份错位

账户：`307000051387`

篮子：`BTR.83224303|J.0`

下单时段：约 `09:16:35-09:18:08 +08:00`

共同特征：`order_id` 和 `order_stream_id` 在实时与查询中一致，`gateway_order_id` 也一致，但实时 `order.event.payload.symbol/exchange` 与盘后 `order_page` 不同。盘后查询证券与实际成交证券一致，因此错误位于 OC 实时订单上下文，不是 Relay 字段解析。

| gateway_order_id 后缀 | order_id | 实时 order.event | 盘后 order_page/实际成交 |
| --- | ---: | --- | --- |
| `110010180000014` | 12 | `159851.SZ` | `510810.SH` |
| `110010180000016` | 14 | `159869.SZ` | `515210.SH` |
| `110010180000018` | 16 | `159915.SZ` | `515790.SH` |
| `110010180000020` | 18 | `510720.SH` | `516220.SH` |
| `110010180000022` | 20 | `510810.SH` | `517180.SH` |
| `110010180000024` | 22 | `512400.SH` | `561580.SH` |
| `110010180000026` | 24 | `515210.SH` | `562990.SH` |
| `110010180000027` | 28 | `515790.SH` | `515000.SH` |
| `110010180000029` | 30 | `515880.SH` | `560660.SH` |
| `110010180000031` | 32 | `516220.SH` | `588200.SH` |
| `12001A180000028` | 6 | `159623.SZ` | `159731.SZ` |
| `12001A180000030` | 8 | `159731.SZ` | `159851.SZ` |
| `12001A180000032` | 10 | `159732.SZ` | `159915.SZ` |
| `12001A180000034` | 26 | `515220.SH` | `159819.SZ` |

证券序列呈明显错位，符合实时回调仍从篮子迭代位置、上一笔订单或共享上下文取证券，而 `order_id/order_stream_id` 已来自当前柜台订单的特征。

## 原始流证据

以 `gateway_order_id=external-huaxin-30700005138701-110010180000014` 为例：

1. `order.event` stream `1785374287823-0` 和 `1785374287845-0`：`order_id=12`、`order_stream_id=110010180000014`，但证券为 `159851.SZ`。
2. 终态 `order.event` stream `1785376211739-0`：同一 ID 仍为 `159851.SZ`，累计成交量为 `76400`。
3. 盘后 `order.list.query` reply stream `1785395323217-0`：同一 ID、同一 `order_id/order_stream_id` 的权威证券为 `510810.SH`，累计成交量为 `76400`。
4. 实际成交 `fill_id=0000000016238224` 的证券为 `510810.SH`。OC 分别在 stream `1785376211731-0` 和 `1785395324518-0` 生成 `FILL_ORDER_CONTEXT_MISMATCH / symbol mismatch` DLQ，payload 明确记录 `fill_symbol=510810`、`order_symbol=159851`。

## OC 修改请求

1. 修正实时 `order.event` 的订单上下文来源。`symbol/exchange/trade_side/business_type` 必须与当前柜台回调的 `order_id/order_stream_id` 属于同一记录，不能读取篮子当前索引、上一条记录或共享的最近订单上下文。
2. 稳定 ID 生成函数已经工作，但同一个函数只能保证 ID 一致，不能替代身份字段一致性。发布前应校验：

   `gateway_order_id -> order_id + order_stream_id + symbol + exchange + trade_side + business_type`

3. 保留现有 `FILL_ORDER_CONTEXT_MISMATCH` 隔离逻辑。它成功阻止了错误真实成交进入 Relay 普通成交账本。
4. 建议对同一 `account_id + trade_date + fill_id + code` 的实时/查询重复 DLQ使用稳定去重键，避免同一问题在运维页面形成两条原始告警；原始来源可保留在 details 中。
5. 修复后用一个包含沪深 ETF 的普通调仓篮子复测，验收实时 `order.event`、实时 `fill.event`、`order_page` 和 `fill_page` 四条链路身份完全一致。

## Relay 后续修正

OC 修复不需要等待以下 Relay 改进，但两项都应在下一轮完成：

1. 权威 `order_page` 改写订单身份时，删除或重建身份不一致的 `relay-summary:*` 合成成交，避免兼容数据在真实成交被 DLQ 后形成次生证券错配。
2. 交易质量与盘后对账对 `trade_side in (P,R) AND business_type=E` 的 ETF 主订单使用申赎/transfer 口径，不按普通 `fills` 数量闭合，消除 3 条假阳性数量差异。

## 复验标准

- 当日实时 `order.event` 与 `order_page` 身份错配为 0。
- `FILL_ORDER_CONTEXT_MISMATCH` 新增不同 `fill_id` 为 0。
- 真实 `fill.event` 与 `fill_page` 交集身份错配为 0，且不因 DLQ 缺失应有成交。
- 空 `order_stream_id` 的不同子单仍保持一单一 ID。
- ETF 0 价/无估值成分记录只进入 transfer 链路。
- Relay 清理后 `fill_order_security_mismatch=0`、普通订单 `order_fill_quantity_mismatch=0`。
