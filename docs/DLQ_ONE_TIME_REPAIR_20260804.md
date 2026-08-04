# 历史 DLQ 一次性修复报告

执行日期：`2026-08-04`

业务时区：`Asia/Shanghai`

范围：生产账户 `307000051387/2026-07-30`；追加复核 `314000046830/2026-07-14`

## 边界

本次是针对已确认 OC 历史故障的一次性数据修复，不建立交割单导入 API、数据库标准流程、定时任务或自动重建能力。每日 OC 实时推送和当日权威查询继续作为标准交易账本的最终来源。

原始 Redis 消息和 56 条 DLQ 均未删除。修复对象限定为旧 Relay 兼容逻辑生成的 14 条错误 `relay-summary:*` 衍生成交、当日 29 个已成交稳定订单的实际费用，以及两个已有稳定身份但缺终态的订单。所有更正均引用已有订单身份，不生成柜台订单号、成交号或 Redis Stream 身份。

## 权威证据

来源文件：本机 `reference/307000051387_20260804.csv`

SHA-256：`e2685215739cd4cd3aa9806af046190993b7be249edb32a6eabcb23f37cd3945`

只读 dry-run 结果：

- 原 32 条 `FILL_ORDER_CONTEXT_MISMATCH` 对应 16 个不同成交、14 个稳定订单。
- 目标订单 14 个、错误 summary 14 条、交割单匹配行 14 条，全部一一对应。
- 当日交割单 28 条普通买卖全部能唯一匹配 28 个已成交订单。
- `204001.SH` 逆回购的交割单方向为“融券”、数量为 4,326，而 OC 订单数量为 43,260。成交记录不修改；订单费用按已确认的 10 倍业务单位规则唯一关联。
- 交割单成交时间通过无时区本地时间加 `Asia/Shanghai` 构造，目标时间范围为 09:25:00..09:50:12。
- 两个未成交稳定订单的交割单均无成交。`159566.SZ` 原稳定事件已有 `invalid_qty=100` 和柜台文本 `VIP:找不到股东账户`，同 basket/order identity 的旧 shadow 订单为明确 rejected；`515880.SH` 已 accepted/working、无拒绝证据且整日零成交，只能按交易日结束失效为 cancelled。

## 事务修改

第一阶段单个 PostgreSQL 事务执行以下操作，任一数量断言不满足会整体回滚：

1. 将 14 条错误 `relay-summary:*` 衍生成交就地修正为 `statement-correction:20260804:*`，写入正确订单、证券、方向、数量、价格、时间和费用。
2. 每条修正记录在 `adapter_context.relay_one_time_correction` 保存修正 ID、原因、原 fill ID、原证券/方向/数量/价格、交割单文件哈希和原始行号。
3. 同步 14 个订单的 `avg_fill_price` 和实际费用，并写入同一修正审计上下文。
4. 将 6 个已经闭合的 `order_fill_qty_mismatch` 对账差异标记为 resolved；两条没有终态证据的 `non_terminal_order` 保持 open。
5. 32 条成交上下文错配和 18 条查询中断 DLQ 标记 `acknowledged`；6 条旧多 Stream 解析错误标记 `ignored`。审核只追加 `stream_dlq_reviews`，不重放、不删除原始 DLQ。

用户复核后发现费用和两个终态仍有充分证据可恢复，第二阶段先完成整笔回滚演练，再以单个事务提交：

1. 从交割单精确筛选 29 条交易流水，按账户、交易日、证券、方向和数量关联 28 个普通买卖订单；逆回购使用独立 10 倍订单单位规则。29 条来源行和 29 个稳定订单均唯一。
2. 新增 29 条 `order_fee_records`，保存交割单文件哈希、原始行号、方向、数量单位规则及费用分项；同步订单均价和费用。实际费用合计 55.05 元。
3. 为 `159566.SZ` 追加一次性 rejected 更正事件，终态时间为 09:18:08，保留原稳定事件和 shadow 拒绝证据；为 `515880.SH` 追加日终 cancelled 更正事件，终态时间为 15:00:00，并明确该时间是交易日失效推断而非柜台回报。
4. 将对应两条 `non_terminal_order` 对账差异标记 resolved。第二阶段不修改成交、Redis raw 或 DLQ 审核状态。

## 事务后验证

| 检查 | 结果 |
| --- | --- |
| 修正成交/订单 | 14 / 14 |
| 修正成交时间 | 09:25:00..09:50:12 CST |
| 修正成交数量 | 116,500 |
| 修正成交金额 | 98,741.50 CNY |
| 修正成交费用 | 5.44 CNY |
| 28 个普通买卖汇总组数量/金额差异 | 0 |
| 已成交订单成交量差异 | 0 |
| `order_fill_qty_mismatch` | 6 resolved，0 open |
| 订单实际费用 | 29/29，55.05 CNY |
| 费用时间 | 09:25:00..15:03:49 CST |
| `non_terminal_order` | 2 resolved，0 open |
| 缺终态订单 | `159566.SZ` rejected；`515880.SH` cancelled |
| DLQ 审核 | 50 acknowledged，6 ignored，0 pending |
| Redis output stream | 24 healthy，lag 0 |
| `/v1/status` | `ok` |

只读绩效复核：

- `performance_position_cost.v3` 为 32 个 calculated、0 estimated、0 blocked，数量差异和缺费用项均为 0。
- 当日贡献实际费用为 55.05 元，订单费用覆盖为 29/29，归因残差降至 `-3.108480` 元。
- `trade_quality.v2` 优先使用完整订单费用且同订单只计一次；摘要费用为 55.05 元，未终态订单为 0。
- 该日仍受绩效起算基线、逆回购本金处理和策略归因等独立质量标记约束；本次修复不自动发布 finalized 绩效。

## 本地回滚证据

修复前快照保存在忽略 Git 的 `outputs/backups/relay-dlq-repair-20260804/`：

| 文件 | 行数 | SHA-256 |
| --- | ---: | --- |
| `affected_fills_before.csv` | 14 | `036b6e2fffb73c7034cae6f3548a8d57a8f57d670ca2a94c5ef074bcdd55b958` |
| `affected_orders_before.csv` | 14 | `ad20b725a393b12bddfeb466c33ea686262d87e2e6e0ebb8f77719b4e462a527` |
| `dlq_before.csv` | 56 | `46e29fc4c1d08a220bc35f0132d9cad40437bd328e670c99ca301797ec7f16d9` |
| `dlq_reviews_before.csv` | 0 | `7b338ce5b5aad99e48a23852276813bb65f6c1537e5d47c36d44334f0d29d88b` |
| `reconciliation_breaks_before.csv` | 8 | `866321611c7c579f545341a7130493ba8c4d65f1f9ac156675d845b6fb8ab1e5` |

这些文件含生产账户数据，只保存在本机，不提交 Git。

第二阶段修复前快照保存在 `outputs/backups/relay-dlq-repair-20260804-followup/`：

| 文件 | 行数 | SHA-256 |
| --- | ---: | --- |
| `orders_before.csv` | 31 | `0aaf83108c90b13ddec3f48ed52366e710352d32adb9701e9427009c51d1acf3` |
| `terminal_events_before.csv` | 4 | `138822a6364c0928a39cfe797af31f670048677fdcfc2d592e8732735d05b9b9` |
| `fee_records_before.csv` | 0 | `7986752591bc00a2a896ce310b7945db4150dba67e00d4b9dde8f7a50271401e` |
| `reconciliation_breaks_before.csv` | 8 | `46033da650944953221efcf092acfc8b3db1e09493913c083a75a3de258e48f1` |

同目录 `repair_followup.sql` 为实际执行事务，只保存在本机忽略目录。正式执行前使用相同 SQL 将 `COMMIT` 替换为 `ROLLBACK` 完成全量断言演练。

## 债享5号历史终态追加修复

绩效页面查询覆盖 7 月上旬的长区间时，`314000046830` 的“订单与成交账本”仍有一项阻断。定位结果为 `2026-07-14` 的稳定订单 `external-huaxin-31400004683001-110010180010003`：`512760.SH` 买入 1,000,000 份、限价 1.36，OC 原始流只有 accepted/working 两条事件，15:05 的旧查询仍回报 queued，订单没有任何成交。

完整交割单 `reference/314000046830_20260804.csv` 的 SHA-256 为 `3342452559335c41b47afa737bb4fe4c1f7fcf4f639ba9ed083753177d1eaea9`。当日交割单有 `512760.SH` 卖出和手工冻结记录，但没有对应买入成交；结合 A 股当日委托不跨日有效，足以确认该零成交订单在交易日结束时已失效，无法仅凭现有证据区分主动撤单与交易所日终失效，因此标准终态统一恢复为 cancelled，并将 `terminal_time_basis` 明确记录为 `A_share_trading_day_close`。

修复先使用同一事务完整 ROLLBACK 演练，再正式提交：追加一条带审计上下文的 cancelled 事件，订单改为 `cum_filled_qty=0/leaves_qty=0/cancelled_qty=1,000,000/is_terminal=true`，对应 `post_close_settlement-20260714` 对账断点由 open 改为 resolved。原始 3 条 Redis 消息和原 accepted/working 事件没有修改。修复后债享5号 `2026-07-01..2026-08-04` 的未终态订单为 0，页面不再显示该阻断。

修复前快照位于忽略 Git 的 `outputs/backups/relay-debt5-terminal-repair-20260804/`：订单 1 条、事件 2 条、对账断点 1 条、原始证据 3 条；对应 SHA-256 分别为 `ff168740...609`、`d5d03974...0d2`、`cb37d797...5bf` 和 `a693a9d6...1aa`。

## 债享5号全区间交易账表修复

用户进一步确认：最近历史区间内，券商交割单能够证明正确且 Relay 已有 OC 稳定订单身份的交易日，可作为历史账表基准。债享5号 30 个 OC 交易日的 15,551 个有成交订单与交割单逐笔闭合；由此定位并受控修正 11 个旧上下文订单外壳、2 条重复 canonical fill，补齐 13,455 条历史实际费用 `21,398.66 CNY`。修复后未终态、孤立成交、订单/成交上下文错配和成交数量差异均为 0。

本次仍不建设交割单导入 API、批次或定时任务。完整匹配规则、事务边界、绩效重建和备份哈希见 `docs/DEBT5_HISTORICAL_LEDGER_RECONCILIATION_20260804.md`。
