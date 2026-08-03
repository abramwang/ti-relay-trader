# OC v1.2 交易日盘中联合验收报告

验收日期：`2026-08-03 Asia/Shanghai`

验收窗口：`09:00-10:25`

验收方式：先保留自然实时推送现场，只读检查生产 Redis Stream、consumer group、PostgreSQL 标准账本和 DLQ；确认自然链路后，仅对有订单的四个账户发送 `fee.list.query`。未发送下单、撤单、订单刷新或成交刷新命令，生产账户交易权限保持关闭。

## 结论

OC `70c966d/7110a76` 的查询 ACK 与多 Stream 恢复修正已经通过真实重启验收；当日订单、普通成交和 ETF 成分划转的唯一性、关联关系、业务字段和时间字段没有复现历史错位。OC 订单实际费用接口首次取得完整生产样本，四账户 `3,984` 条费用记录全部完整并关联到唯一订单。

本轮没有发现需要 OC 继续修改的 P0/P1 数据质量问题。Relay 自身发现并修复 heartbeat 被账本 worker 误计为 unsupported 的运维误报；修复后 24 条 output stream 全部健康。

15:01 权威盘后结算和 17:45 绩效计算不在本报告窗口内，仍需按交易日流程继续验收。

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

1. 15:01 `post_close_settlement` 的资金、持仓、订单、成交、费用权威刷新和 close 快照。
2. 17:45 `performance_daily` 对四个可信成本账户的费用覆盖、成本池、数量桥和 economic NAV 结果。
3. 本日没有自然产生撤单拒绝、撤单超时、batch 部分失败或 `COMMAND_OUTCOME_UNKNOWN` 样本，这些低频写链路继续等待受控交易机会。
