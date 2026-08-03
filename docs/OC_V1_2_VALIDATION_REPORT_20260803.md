# OC v1.2 交易日全日及收盘联合验收报告

验收日期：`2026-08-03 Asia/Shanghai`

验收窗口：`09:00-15:30`

验收方式：先保留自然实时推送现场，只读检查生产 Redis Stream、consumer group、PostgreSQL 标准账本和 DLQ；确认自然链路后，仅对有订单的四个账户发送 `fee.list.query`。15:01 使用生产交易日任务发起六账户资金、持仓、订单、成交和费用权威查询，收盘后核对 Redis PEL、最终账本、close 快照和 reconciliation。未通过 Relay 发送下单或撤单，生产账户交易权限保持关闭。

## 结论

OC `70c966d/7110a76/701d5af` 三项定向修正均已通过真实交易日验收：查询命令 PEL 全部清零且没有恢复字段错位；12 条真实撤单拒绝全部使用 Relay 标准账户，柜台账户只保留在 `adapter_context.broker_account_id`。当日最终 `15,756` 笔订单、`15,235` 笔普通成交和 `99` 条 ETF 成分划转的唯一性、关联关系、业务字段和时间字段没有复现历史问题；`15,754` 条订单费用全部完整并关联到唯一订单。

收盘复核新发现一个独立 P1：每次 `account.asset.query` 都先返回一条含有效资金的 `asset_page status=partial`，约 2 毫秒后又为同一 `origin_message_id` 返回 `QUERY_EMPTY_RESULT status=failed`。全天 18 次真实资金查询全部稳定复现。资金数据已经落账，但查询终态与对接文档冲突，需要 OC 修正；Relay 日任务也应增加命令终态关联门禁，避免只依据资金时间戳把矛盾终态视为成功。

15:01 权威盘后结算已经恢复完成。首轮失败来自 Relay 的 45 秒新鲜度门限和 10 秒快照 HTTP 超时，均已修复，不属于 OC 数据质量失败。17:45 绩效计算不在本报告窗口内，仍需按交易日流程继续验收。

## OC 恢复链路

OC 于 09:00 启动后，把 7 月 31 日遗留的 18 条 query PEL 全部转为真实关联的 `QUERY_INTERRUPTED` 并完成 ACK：

| 账户 | 恢复条数 |
| --- | ---: |
| `501000114077` | 4 |
| `314000046830` | 4 |
| `314000045768` | 1 |
| `307000051388` | 4 |
| `307000051389` | 1 |
| `307000051387` | 4 |

动作分布为资金查询 4 条、持仓查询 6 条、订单查询 4 条、成交查询 4 条。18 条记录均满足：

- `original_stream` 是真实账户 `cmd.query` key；
- `original_entry_id` 是真实 Redis entry ID；
- `original_body.message_id == origin_message_id`；
- 没有新增 `BAD_RECOVERED_COMMAND`。

六账户 `cmd.trade/cmd.query` consumer group 均为 `pending=0, lag=0`。这说明完成后非阻塞 XACK、失败重试和多 Stream 嵌套响应解析已按预期生效。

## 实时交易账本

09:01 盘前订单/成交查询发生在当日交易之前；本报告中的盘中订单、成交和划转来自自然实时事件，不依赖盘中手工订单/成交刷新。

| 账户 | 订单 | 普通成交 | ETF 划转 | 订单费用 | 实际费用合计 |
| --- | ---: | ---: | ---: | ---: | ---: |
| `501000114077` | 0 | 0 | 0 | 0 | 0.0000 |
| `314000046830` | 2,157 | 2,171 | 0 | 2,157 | 2,697.6771 |
| `314000045768` | 688 | 600 | 99 | 688 | 3,063.7331 |
| `307000051388` | 500 | 510 | 0 | 499 | 453.8654 |
| `307000051389` | 0 | 0 | 0 | 0 | 0.0000 |
| `307000051387` | 641 | 588 | 0 | 640 | 480.8589 |
| **合计** | **3,986** | **3,869** | **99** | **3,984** | **6,696.1344** |

订单终态为 filled 3,048 条、cancelled 936 条、rejected 2 条。全量交叉检查结果：

- 重复 `gateway_order_id`：0；
- 重复 `(gateway_order_id, fill_id)`：0；
- 孤立成交：0；
- 订单/成交 `symbol/exchange/trade_side/business_type` 错配：0；
- 普通订单累计成交量与逐笔成交量不一致：0；
- 普通成交零价或非正数量：0；
- 普通成交混入 transfer：0；
- 非东八区或非当日订单/成交时间：0。

## ETF 赎回语义

`314000045768` 有一笔 `159915.SZ` 赎回：

- `trade_side=R`、`business_type=E`、委托和完成数量均为 `1,000,000`；
- 稳定 `gateway_order_id/order_stream_id` 完整；
- 不生成普通 `fill.event`；
- 独立生成 99 条 `etf_redemption_component_transfer`，其中 1 条 ETF 本体记录、98 条零价成分划转，99 个 `fill_id` 均唯一；
- 订单实际费用为 `181.164042` 元，其中 commission `168.364042` 元、transfer fee `12.8` 元，费用记录与赎回订单完整关联。

赎回订单的 `cum_filled_qty=1,000,000` 而普通成交数量为 0 是协议设计结果，不属于订单/成交数量缺口。

## 柜台前拒单

`307000051388` 和 `307000051387` 各有一笔 `517180.SH` 篮子卖单在生成柜台委托流号前被拒绝，错误均为 `VIP:数量不足`：

- `order_stream_id` 为空，符合尚未进入柜台委托流的事实；
- Relay 使用交易日、ref、basket、证券和方向构造稳定且互不冲突的 `gateway_order_id`；
- 两笔订单均为 terminal rejected，`invalid_qty=order_qty`；
- OC 费用页不返回这两笔未进入柜台的拒单，因此费用记录总数比订单总数少 2，属于预期结果。

## 实际费用页

对四个有订单账户显式发送一次 `fee.list.query` 后：

- 3,984 条记录全部 `fee_complete=true`；
- 3,984 条记录全部 `association_complete=true`；
- `fee_record_id` 重复 0 条，孤立费用 0 条；
- 每条 `total_fee` 与 commission、stamp tax、transfer fee 等分项之和一致；
- 普通订单 `turnover` 与该订单逐笔成交金额之和逐单一致；
- 来源全部为 `broker_order_fund_detail`；
- cancelled 订单也有稳定的零费用记录，柜台前 rejected 订单没有伪费用记录。

费用接口只支持柜台当前交易日。本次结果证明新完整交易日可以使用实际费用进入成本和净绩效；不能据此回填 7 月历史费用。

## 收盘最终账本

15:01 权威查询完成后的最终账本如下：

| 账户 | 订单 | 普通成交 | ETF 划转 | 订单费用 | 实际费用合计 |
| --- | ---: | ---: | ---: | ---: | ---: |
| `501000114077` | 0 | 0 | 0 | 0 | 0.000000 |
| `314000046830` | 2,158 | 2,172 | 0 | 2,158 | 2,714.827768 |
| `314000045768` | 5,465 | 5,184 | 99 | 5,465 | 3,524.675540 |
| `307000051388` | 4,063 | 3,956 | 0 | 4,062 | 800.050235 |
| `307000051389` | 0 | 0 | 0 | 0 | 0.000000 |
| `307000051387` | 4,070 | 3,923 | 0 | 4,069 | 815.515445 |
| **合计** | **15,756** | **15,235** | **99** | **15,754** | **7,855.068988** |

订单终态为 filled 14,151 条、cancelled 1,603 条、rejected 2 条，未终态为 0。两条费用缺口仍是柜台前 `VIP:数量不足` 拒单，没有 `order_stream_id`，属于预期无费用记录。

收盘全量交叉检查共 23 项，异常计数全部为 0：订单、成交、划转和费用稳定 ID 重复；孤立成交、划转和费用；订单成交证券、交易所、方向和业务类型错配；普通订单累计成交量差异；非终态订单；核心字段缺失；异常委托流号缺失；普通成交非法价格/数量；ETF 划转非法语义；transfer 混入普通成交；申赎业务语义错配；订单/成交交易日与东八区时间错配；费用不完整、费用分项不闭合、普通订单 turnover 不闭合及应有费用缺失。

实时覆盖率也通过：15,756 笔订单全部存在至少一条 `order.event`；15,235 笔普通成交的最终来源全部为 event stream；99 条 ETF 划转的最终来源也全部为 event stream。盘后查询没有补出此前实时链路遗漏的订单、成交或划转。

## 撤单拒绝标准账户

当日自然产生 12 条 `BROKER_CANCEL_REJECTED`：`314000045768` 9 条、`307000051388` 2 条、`307000051387` 1 条。全部满足：

- `order_cancel_attempts.account_id == raw_payload.account_id`，且均为 Relay 标准账户；
- `adapter_context.broker_account_id` 全部存在，分别保留带柜台后缀的原始账户；
- 标准账户错配 0；
- `reconciliation_required=false`，没有把撤单拒绝错误改写为订单终态。

这为 OC `701d5af` 提供了真实写链路验收样本，不再只是静态协议检查。

## 资金查询矛盾终态

全天 18 次非恢复类 `account.asset.query` 全部出现相同序列：

1. 同一命令先返回 `result_type=asset_page`、`status=partial`，`payload.account` 包含有效资金数据；
2. 约 2 毫秒后返回 `result_type=error_result`、`status=failed`、`code=QUERY_EMPTY_RESULT`，错误文本为 `broker asset query returned no account row`；
3. 两条 reply 的 `origin_message_id` 和 `request_id` 完全相同。

15:01 六账户样本：

| Relay 账户 | origin_message_id | 有效 asset_page 到达 | 错误终态到达 |
| --- | --- | --- | --- |
| `501000114077` | `msg-asset-query-1785740462955032312-10` | 15:01:03.164 | 15:01:03.167 |
| `314000046830` | `msg-asset-query-1785740463118273148-15` | 15:01:32.473 | 15:01:32.475 |
| `314000045768` | `msg-asset-query-1785740477451234586-20` | 15:02:39.831 | 15:02:39.833 |
| `307000051388` | `msg-asset-query-1785740488926538613-25` | 15:03:01.276 | 15:03:01.278 |
| `307000051389` | `msg-asset-query-1785740503200976001-30` | 15:01:44.896 | 15:01:44.899 |
| `307000051387` | `msg-asset-query-1785740514926629524-35` | 15:03:30.344 | 15:03:30.347 |

对接文档 17.1 的资金单页示例要求 `asset_page status=completed + chunk.is_last=true`，查询分页规则也要求最后一页必须是 completed final chunk。OC 应在已经收到并发出有效资金行后把该查询标记为成功完成，不应再发布 `QUERY_EMPTY_RESULT`。只有整个查询确实没有任何资金行时才应返回该错误。

当前影响：Relay 会先接收并落账有效资金，因此六账户 intraday 资金值均已更新；但命令协议终态是 failed。现有日任务只用资金/持仓 `updated_at` 做新鲜度门禁，没有关联检查 reply 终态，所以报告没有暴露这六条失败。修复建议分工：

- OC P1：记录本次查询是否已经收到资金行；有数据时只发布一条 `completed asset_page`，无数据时才发布 `QUERY_EMPTY_RESULT`。
- Relay P1：给日任务增加 `message_id/origin_message_id` 查询终态关联；发现“数据页后 failed”时保留已落账数据，但账户必须显示 attention，不能静默成功。

## Close 快照

15:15 恢复结算写入 6 份 close 资产和 150 条正持仓快照，`reconciliation_runs/post_close_settlement-20260803` 为 completed。六账户 close 资产字段与 15:01 最终 intraday 资金值一致；150 条持仓的证券、数量、可用量、日初量、当日量和股东号全部一致。

其中 23 条 OC 持仓 `avg_cost=0`，Relay 根据当日普通成交补出 close 成本：`314000046830` 17 条，其余三个有持仓账户各 2 条。该差异不改变托管数量闭合，但说明 close 成本是 Relay 衍生值，不能把这些 OC 原始零成本直接当作绩效成本来源。

## Relay 运维误报修复

worker 原先会归档 heartbeat，但随后进入 unsupported 分支，把每批合法 heartbeat 写成 checkpoint error，导致 `/operations` 的 6 条 heartbeat stream 常驻红色。修正后 heartbeat 被识别为“只归档、推进 checkpoint、不产生交易账本”的合法消息。

部署后状态：

- gateway online `6/6`；
- output stream healthy `24/24`；
- stream attention `0`；
- Redis lag `0`；
- heartbeat checkpoint error `0`。

`/v1/status` 仍为 degraded 的原因是 56 条待人工审核历史 DLQ，其中 18 条为本轮预期的 `QUERY_INTERRUPTED` 恢复审计，不代表当前链路异常。

## 尚待验证

1. OC 修复 `account.asset.query` 的矛盾终态后，再用一个真实账户确认只返回 `completed asset_page`，且 `cmd.query pending=0, lag=0`。
2. Relay 日任务增加查询终态关联门禁，避免有效数据页后的 failed 终态被时间戳门禁掩盖。
3. 17:45 `performance_daily` 对四个可信成本账户的费用覆盖、成本池、数量桥和 economic NAV 结果。
4. 本日没有自然产生撤单超时、batch 部分失败或 `COMMAND_OUTCOME_UNKNOWN` 样本，这些低频写链路继续等待受控交易机会。
