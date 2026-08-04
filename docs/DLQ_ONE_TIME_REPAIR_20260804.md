# 历史 DLQ 一次性修复报告

执行日期：`2026-08-04`

业务时区：`Asia/Shanghai`

范围：生产账户 `307000051387`，交易日 `2026-07-30`

## 边界

本次是针对已确认 OC 历史故障的一次性数据修复，不建立交割单导入 API、数据库标准流程、定时任务或自动重建能力。每日 OC 实时推送和当日权威查询继续作为标准交易账本的最终来源。

原始 Redis 消息和 56 条 DLQ 均未删除。修复对象只有旧 Relay 兼容逻辑生成的 14 条错误 `relay-summary:*` 衍生成交，以及与其直接关联的订单展示字段和对账审核状态。

## 权威证据

来源文件：本机 `reference/307000051387_20260804.csv`

SHA-256：`e2685215739cd4cd3aa9806af046190993b7be249edb32a6eabcb23f37cd3945`

只读 dry-run 结果：

- 原 32 条 `FILL_ORDER_CONTEXT_MISMATCH` 对应 16 个不同成交、14 个稳定订单。
- 目标订单 14 个、错误 summary 14 条、交割单匹配行 14 条，全部一一对应。
- 当日交割单 28 条普通买卖全部能唯一匹配 28 个已成交订单。
- 模拟修复后唯一额外差异是 `204001.SH` 逆回购的数量单位和方向语义；它不属于本批普通买卖 DLQ，也未被修改。
- 交割单成交时间通过无时区本地时间加 `Asia/Shanghai` 构造，目标时间范围为 09:25:00..09:50:12。

## 事务修改

单个 PostgreSQL 事务执行以下操作，任一数量断言不满足会整体回滚：

1. 将 14 条错误 `relay-summary:*` 衍生成交就地修正为 `statement-correction:20260804:*`，写入正确订单、证券、方向、数量、价格、时间和费用。
2. 每条修正记录在 `adapter_context.relay_one_time_correction` 保存修正 ID、原因、原 fill ID、原证券/方向/数量/价格、交割单文件哈希和原始行号。
3. 同步 14 个订单的 `avg_fill_price` 和实际费用，并写入同一修正审计上下文。
4. 将 6 个已经闭合的 `order_fill_qty_mismatch` 对账差异标记为 resolved；两条没有终态证据的 `non_terminal_order` 保持 open。
5. 32 条成交上下文错配和 18 条查询中断 DLQ 标记 `acknowledged`；6 条旧多 Stream 解析错误标记 `ignored`。审核只追加 `stream_dlq_reviews`，不重放、不删除原始 DLQ。

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
| `non_terminal_order` | 2 open |
| DLQ 审核 | 50 acknowledged，6 ignored，0 pending |
| Redis output stream | 24 healthy，lag 0 |
| `/v1/status` | `ok` |

只读绩效复核：

- `performance_position_cost.v3` 数量差异为 0、blocked items 为 0，结果为 estimated。
- 当日贡献归因残差降至 `-52.718480` 元。
- 订单费用覆盖为 14/29；本次没有用交割单补齐其余正常订单费用。
- 两个工作订单仍缺 OC 历史终态，因此当日不自动发布 finalized 绩效。

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
