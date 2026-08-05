# OC 订单查询矛盾终态问题（2026-08-05）

## 结论

生产账户 `314000046830` 的 `order.list.query` 会先返回完整的 `order_page/partial` 数据，随后将某一笔市价单的业务撤单原因错误地作为整项查询的 `QUERY_FAILED` 终态返回。Relay 因此得到 `contradictory=true`，按账表门禁阻止该账户盘后 close 快照。

业务拒单或撤单原因应落在对应订单的状态和错误字段中；只要柜台查询本身已正常遍历完成，查询命令必须以唯一的 `order_page/completed/is_last=true` 结束，不能把单笔订单状态映射成查询基础设施失败。

## 首次样本

- 账户：`314000046830`
- action：`order.list.query`
- origin message：`msg-orders-query-1785913261881498585-6`
- command stream ID：`1785913261885-0`
- 请求时间：`2026-08-05 15:01:01 Asia/Shanghai`
- 回复总数：`523`
- 前置数据：`522` 条有效 `order_page/partial/is_last=false`
- 最终回复：`reply-1785913267177-11279`
- 最终 stream ID：`1785913267176-0`
- 最终时间：`2026-08-05 15:01:07.208105 Asia/Shanghai`
- 最终状态：`failed / error_result / is_last=true`
- 错误码：`QUERY_FAILED`
- 错误信息：`20046:市价单不能满足成交条件撤单`

## 稳定复现

`2026-08-05 15:09` 再次发起同账户查询：

- origin message：`msg-orders-query-1785913785310434402-31`
- command stream ID：`1785913785312-0`
- 回复总数：`523`
- 最终回复：`reply-1785913789559-12179`
- 最终 stream ID：`1785913789560-0`
- 最终时间：`2026-08-05 15:09:49.606928 Asia/Shanghai`
- 结果仍为同一 `QUERY_FAILED / 20046`，且 `contradictory=true`

## OC 修正要求

1. 区分“单笔订单业务状态/拒撤原因”和“订单查询命令执行失败”。
2. 单笔 `20046` 必须附着在对应订单回报，不得终止或污染整个 `order.list.query`。
3. 查询正常遍历后只发送一个 `status=completed/result_type=order_page/is_last=true` 终态。
4. 同一查询不能同时具有有效 partial page 和 failed terminal；若底层查询确实失败，应明确是否已返回不完整数据，并保持可审计错误上下文。

## 联合验收

对该账户连续执行两次 `order.list.query`：

- 两次均为 `state=completed`、`success=true`、`contradictory=false`；
- `terminal_count=1`；
- `reply_count` 可随分页变化，但最终唯一回复必须为 completed final page；
- `20046` 对应订单仍保留正确业务终态和错误原因；
- Relay 可为该账户完成 close 资产/持仓快照，任务不再出现 `query terminal validation failed`。

## Relay 侧处置

Relay 不把该矛盾终态降级为成功，也不绕过债享5号的 close 快照门禁。任务报告改为保留 reply 总数、首条和最终/错误证据，完整分页继续保存在 raw archive；生产任务回写改走本机 9092。`2026-08-05 15:12` 补跑后，任务报告由约 3 MB 降至 83 KB 并成功落库，其余五账户正常完成 close 快照，债享5号保持账户级 blocked，等待 OC 修正后再单户补跑。

## 修复验收

OC 更新并于 `2026-08-05 15:51 Asia/Shanghai` 临时恢复后，Relay 连续执行两次只读 `order.list.query`：

| 轮次 | origin message | reply count | terminal count | state | contradictory |
| --- | --- | ---: | ---: | --- | --- |
| 1 | `msg-orders-query-1785916281731657865-1` | 524 | 1 | completed | false |
| 2 | `msg-orders-query-1785916297583061410-2` | 524 | 1 | completed | false |

两次最后一页均为 `status=completed/result_type=order_page/chunk.is_last=true`，没有查询级 `QUERY_FAILED`。对应订单 `external-huaxin-31400004683001-12001A180004501` 为 `cancelled/cancelled/is_terminal=true`，`reject_code/reject_message` 为空；`20046:市价单不能满足成交条件撤单` 同时保存在订单 `adapter_context.status_message/cancel_reason/broker_status_text`。

随后使用正式 `scripts/run-post-close-pipeline.sh` 补跑 `20260805`：

- `post_close_settlement-20260805-1785916378018731000` 于 `15:52:58..15:54:25` 成功；
- 六账户查询终态全部通过，账户错误为 0；
- 写入 6 份 close 资产、233 条 close 持仓；
- 结算覆盖 4,710 笔订单、5,496 笔成交，未终态订单和 reconciliation break 均为 0；
- 债享5号写入 1 份 close 资产和 146 条 close 持仓，523 笔订单全部纳入；
- 下游 `performance_daily` 正常触发，债享5号为 ready；另两个账户的 ETF T0 成本质量阻断属于独立绩效问题；
- 完成后 24 条 output stream lag/pending 为 0，六账户命令 pending 为 0，DLQ pending 为 0。

本事故关闭，OC 订单查询终态修复通过生产实盘验收。
