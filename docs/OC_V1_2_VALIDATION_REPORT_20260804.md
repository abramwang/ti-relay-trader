# OC 华鑫 v1.2 生产复测报告（2026-08-04）

## 结论

`2026-08-03` 发现并由 OC 修正的资金查询矛盾终态、持仓成本字段缺失问题，已在 `2026-08-04` 真实交易日通过生产复测。Relay 无需再向 OC 提交新的兼容修改。

截至 `2026-08-04 15:26 Asia/Shanghai`：

- 六账户 gateway 均为 `online`、`broker_ready=true`；
- 12 个 `cmd.trade/cmd.query` consumer group 全部 `pending=0, lag=0`；
- 24 条 output stream healthy，输出流总 lag 为 0；
- 今日没有 `QUERY_EMPTY_RESULT`、矛盾查询终态、解析错误或新增 DLQ；
- 生产账户仍全部 `trading_enabled=false`，本次只发送查询命令。

## 查询终态

09:01 自动盘前任务在 3.967 秒内完成六账户订单、成交、资金和持仓查询，共 24 条命令。全部命令满足：

- `state=completed`；
- `success=true`；
- `contradictory=false`；
- `terminal_count=1`；
- 最后一条 reply 为对应 `result_type` 的 `status=completed, chunk.is_last=true`。

六条资金查询都只返回一条 `completed asset_page`。昨天的 `asset_page partial -> QUERY_EMPTY_RESULT failed` 未再出现。多持仓账户的 `position_page` 正常返回若干 `partial` 后，以唯一 `completed final` 收口。

12:54 午休窗口又独立发送六账户资金、持仓查询，共 12 条命令；12 条再次全部通过上述终态条件。随后补发六账户订单、成交、费用查询，共 18 条命令，也全部以唯一 completed final reply 完成。两轮结果证明修复不是盘前偶然通过。

## 持仓成本

午休刷新后的 144 个正持仓全部满足：

| 账户 | 正持仓 | `cost_complete=true` | `avg_cost_source` |
| --- | ---: | ---: | --- |
| `314000046830` | 60 | 60 | `broker_total_position_cost` |
| `314000045768` | 28 | 28 | `broker_total_position_cost` |
| `307000051388` | 28 | 28 | `broker_total_position_cost` |
| `307000051387` | 28 | 28 | `broker_total_position_cost` |

每条记录同时满足 `avg_cost>0`、`total_cost>0`。两个空仓账户没有伪造成本记录。昨天 OC 原始持仓出现零成本的情况没有复现。

## 实时账本

截至午休后的权威查询完成，今天已有：

| 数据 | 数量 | 验收结果 |
| --- | ---: | --- |
| 订单 | 2,191 | 全部终态，全部有实时 `order.event` |
| 普通成交 | 2,129 | 全部来自实时 event stream |
| ETF 成分划转 | 49 | 与普通成交隔离，语义合法 |
| 订单实际费用 | 2,191 | 完整率和订单关联率均为 100% |

四个有交易账户的订单、费用数量分别为 `143/143`、`732/732`、`644/644`、`672/672`。费用合计 `3,950.892437` 元。

本轮执行 26 项交叉检查，异常均为 0：稳定 ID 重复、孤立成交/划转/费用、订单事件覆盖缺失、非实时成交、订单/成交核心字段缺失、订单成交字段错配、普通订单成交数量差、非终态订单、费用不完整或未关联、费用分项不闭合、普通订单 turnover 不闭合、有成交订单缺费用、ETF 划转非法语义、transfer 混入普通成交、订单/成交日期时间错位、`QUERY_EMPTY_RESULT`、矛盾查询终态、解析错误和今日新增 DLQ。

## 盘后最终验收

15:01 盘后结算于 `15:05:11` 成功完成，耗时 `248.916` 秒。六账户资产、持仓、订单、成交和费用查询全部完成，固化 6 条 close 资产和 147 条正持仓；147 条持仓均为 `cost_complete=true`、`avg_cost>0`、`total_cost>0`。当日非终态订单和对账差异均为 0。

结算成功后自动触发 `performance_daily`，于 `15:06:03` 完成，耗时 `47.478` 秒：

- `314000046830`、`307000051388`、`307000051387` 为 `ready`，成本账均为 calculated、费用覆盖完整、数量桥为 0；用户已确认三账户结果与券商终端一致。
- 空账户 `307000051389` 为 `not_applicable`，没有被误报为批次失败。
- 绩效批次 `attention=0`、`blocked=0`、`errors=[]`。

最终 `/v1/status=ok`，六个 gateway online，24 条 output stream healthy、lag=0；历史 DLQ 已审核为 50 acknowledged、6 ignored、0 pending。生产六账户仍全部 `trading_enabled=false`。

复核绩效长区间时另发现 `314000046830/2026-07-14` 一笔 `512760.SH` 历史买单停在 working。原始流证明该订单只收到 accepted/working，旧 OC 15:05 查询仍返回 queued，完整交割单没有对应买入成交；该当日委托已按日终失效恢复为 cancelled，并将原对账断点标记 resolved。这个历史修复不属于 `2026-08-03` OC 升级回归，原始消息保持不变。

## 边界

- 今日没有产生撤单拒绝、batch 部分失败、`COMMAND_OUTCOME_UNKNOWN` 或 OC 重启恢复样本；这些低频路径不能用本轮数据重复验收，但 `2026-08-03` 已有的通过结论不受影响。
- 历史区间仍可能存在升级前的证券/成交一致性提示；它们按原始日期独立治理，不改变本轮新协议当日 26 项检查全部通过的结论。
