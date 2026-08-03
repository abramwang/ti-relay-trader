# relay PostgreSQL Migration

更新时间：`2026-08-01`

## 当前状态

已新增 PostgreSQL 账本和位点 migration：

```text
migrations/postgres/000001_init_ledger.up.sql
migrations/postgres/000001_init_ledger.down.sql
migrations/postgres/000002_stream_checkpoints.up.sql
migrations/postgres/000002_stream_checkpoints.down.sql
migrations/postgres/000003_job_runs.up.sql
migrations/postgres/000003_job_runs.down.sql
migrations/postgres/000004_reconciliation_idempotency.up.sql
migrations/postgres/000004_reconciliation_idempotency.down.sql
migrations/postgres/000005_fill_id_order_scope.up.sql
migrations/postgres/000005_fill_id_order_scope.down.sql
migrations/postgres/000006_research_performance_views.up.sql
migrations/postgres/000006_research_performance_views.down.sql
migrations/postgres/000007_open_asset_snapshots.up.sql
migrations/postgres/000007_open_asset_snapshots.down.sql
migrations/postgres/000008_position_day_pnl.up.sql
migrations/postgres/000008_position_day_pnl.down.sql
migrations/postgres/000009_performance_accounting.up.sql
migrations/postgres/000009_performance_accounting.down.sql
migrations/postgres/000010_strategy_attribution_keys.up.sql
migrations/postgres/000010_strategy_attribution_keys.down.sql
migrations/postgres/000011_position_snapshot_types.up.sql
migrations/postgres/000011_position_snapshot_types.down.sql
migrations/postgres/000012_trade_date_order_scope.up.sql
migrations/postgres/000012_trade_date_order_scope.down.sql
migrations/postgres/000013_oc_v1_1_component_transfers.up.sql
migrations/postgres/000013_oc_v1_1_component_transfers.down.sql
migrations/postgres/000014_remove_etf_transfer_summary_fills.up.sql
migrations/postgres/000014_remove_etf_transfer_summary_fills.down.sql
migrations/postgres/000015_stream_operations.up.sql
migrations/postgres/000015_stream_operations.down.sql
migrations/postgres/000016_stream_operations_indexes.up.sql
migrations/postgres/000016_stream_operations_indexes.down.sql
migrations/postgres/000017_oc_v1_2_cancel_attempts.up.sql
migrations/postgres/000017_oc_v1_2_cancel_attempts.down.sql
migrations/postgres/000018_normalize_cancel_attempt_accounts.up.sql
migrations/postgres/000018_normalize_cancel_attempt_accounts.down.sql
migrations/postgres/000019_order_idempotency_unique.up.sql
migrations/postgres/000019_order_idempotency_unique.down.sql
migrations/postgres/000020_performance_cost_ledger.up.sql
migrations/postgres/000020_performance_cost_ledger.down.sql
migrations/postgres/000021_performance_nav_gold.up.sql
migrations/postgres/000021_performance_nav_gold.down.sql
migrations/postgres/000022_order_fee_records.up.sql
migrations/postgres/000022_order_fee_records.down.sql
migrations/postgres/000023_position_cost_corporate_actions.up.sql
migrations/postgres/000023_position_cost_corporate_actions.down.sql
```

文件命名采用 `golang-migrate` / `goose` 常见的 `version_name.up.sql`、`version_name.down.sql` 形式，但 SQL 本身保持工具无关。部署阶段可以用 `psql`、`golang-migrate`、`goose` 或内部发布脚本执行。

当前仓库不保存真实 PostgreSQL DSN。连接方式仍从部署机本地配置或 `http://doc.quantstage.com` 获取。

已在内网 PostgreSQL 上创建专用数据库 `relay_trader`，并通过 `relayctl migrate status/up/status` 验证 migration 已应用。验证结果：

1. `000001_init_ledger` 已应用。
2. `000002_stream_checkpoints` 已应用。
3. `000003_job_runs` 已应用。
4. `000004_reconciliation_idempotency` 已应用。
5. `000005_fill_id_order_scope` 已应用。
6. `000006_research_performance_views` 已应用。
7. `000007_open_asset_snapshots` 已应用。
8. `000008_position_day_pnl` 增加持仓当日浮盈字段，并更新研究侧绩效 view。
9. `000009_performance_accounting` 新增绩效输入层、版本化经济 NAV、T+1 对账和逆回购应计表，并扩展 `cash_ledger`。
10. `000010_strategy_attribution_keys` 新增订单/成交策略归因字段、订单交易日索引和 `performance_attribution_links`。
11. `000011_position_snapshot_types` 为 `position_snapshots` 增加 `snapshot_type`，支持盘前 open 持仓快照和盘后 close 持仓快照共存。
12. `000012_trade_date_order_scope` 将订单、事件和成交的业务键与外键统一为 `account_id + trade_date + gateway_order_id`。
13. `000013_oc_v1_1_component_transfers` 按 OC v1.1 扩展普通成交 fallback 唯一键，新增 ETF 成分划转账表和 `adapter.data_quality` DLQ 索引。
14. `000014_remove_etf_transfer_summary_fills` 清理 Relay 曾从 ETF 申赎订单错误派生的 summary fill；该数据不能安全逆向重建，因此 down migration 只保留说明。
15. `000015_stream_operations` 新增 Redis Stream checkpoint、Gateway 问题和 DLQ 人工审核能力。
16. `000016_stream_operations_indexes` 补充 DLQ 和 `BROKER_NOT_READY` 运维查询索引。
17. `000017_oc_v1_2_cancel_attempts` 新增撤单动作结果审计表；撤单被拒绝、超时或结果未知不再污染订单主状态。
18. `000018_normalize_cancel_attempt_accounts` 将撤单审计账户统一迁移到 Relay routing 标准账户。
19. `000019_order_idempotency_unique` 清理误写入订单的查询请求键和历史重复键，增加 `orders(account_id,idempotency_key)` 部分唯一索引。
20. `000020_performance_cost_ledger` 新增账户绩效起算配置和逐日移动加权持仓成本状态，支持数量对账、Meridian 重估、版本和质量标记。
21. `000021_performance_nav_gold` 新增版本化人工净值金标，保存确认审计、原始输入、派生日初/隔夜调整和内容哈希幂等；金标不参与 NAV 公式。
22. `000022_order_fee_records` 新增 OC 订单级实际费用账表，按账户和稳定 `fee_record_id` 幂等更新；完整且关联成功的费用同步回订单辅助字段，绩效仍以费用账表为权威来源。
23. `000023_position_cost_corporate_actions` 为成本状态新增上一 close、券商 open、公司行为类型/因子/数量差及 Meridian 原始上下文审计字段。
24. `relay_schema_migrations` 当前应记录版本 `1:init_ledger` 到 `23:position_cost_corporate_actions`。

当前环境已安装 PostgreSQL client：

```bash
psql --version
```

也已新增 Go 版 migration runner：

```bash
go run ./cmd/relayctl migrate status
go run ./cmd/relayctl migrate up
go run ./cmd/relayctl migrate down -steps 1
```

runner 会创建 `relay_schema_migrations` 表记录已应用版本。真实 DSN 可通过 `-database-url`、`RELAY_DATABASE_URL` 或 `config.database.dsn` 提供。

当前也已新增 Go 账本写入 repository：

```text
internal/ledger
```

Repository 当前覆盖：

- `UpsertAccount`、`CreateOrder`、`UpsertOrder`、`AppendOrderEvent`、`UpsertOrderCancelAttempt`
- `InsertFill`、`InsertComponentTransfer`
- `ListFills`、`ListComponentTransfers`
- `ArchiveRawStreamMessage`
- `GetStreamCheckpoint`、`UpsertStreamCheckpoint`
- `UpsertJobRun`、`LatestJobRuns`
- `UpsertPosition`、`UpsertAssetSnapshotForDate`、`UpsertPositionSnapshotWithType`
- `GetDailyPerformance`、`ListDailyPerformance`
- `UpsertReconciliationRun`、`UpsertReconciliationInput`、`UpsertReconciliationBreak`、`ListReconciliationBreaks`
- `RawStreamSummary`
- `CreateFeeRule`、`ListFeeRules`、`EffectiveRepoFeeRule`
- `CreateCashLedgerEntry`、`ListCashLedgerEntries`、`ConfirmCashLedgerEntry`、`VoidCashLedgerEntry`
- `CreateNavBaseline`、`ListNavBaselines`
- `UpsertPerformanceInception`、`GetPerformanceInception`
- `UpsertPositionCostState`、`ListPositionCostStates`
- `UpsertPerformanceNAVGold`、`ListPerformanceNAVGold`
- `UpsertReverseRepoAccrual`、`ListReverseRepoAccruals`
- `ListPerformanceNAVs`、`ListNAVReconciliations`

这些入口会把标准交易结构体、stream key、stream id、source/correlation 信息和原始 payload 写入 PostgreSQL。重复消费场景使用唯一约束和 `ON CONFLICT` 做幂等处理。

可选集成测试：

```bash
RELAY_LEDGER_TEST_DATABASE_URL="$RELAY_DATABASE_URL" go test ./internal/ledger -run TestRepositoryWritesToPostgres -count=1 -v
```

该测试默认跳过；设置测试库 DSN 后会写入一组临时账户、订单、事件、成交和原始 stream 消息，验证重复订单幂等键被数据库拒绝，并在测试清理阶段删除。

完整临时库测试：

```bash
scripts/test-postgres-integration.sh
```

脚本默认从未跟踪的 `config/relay.local.yaml` 读取管理员 DSN，也可通过 `RELAY_DATABASE_ADMIN_URL` 提供。它会创建唯一临时数据库、执行全部 migration、运行 repository 集成测试，并通过 trap 自动销毁临时库；日志不会打印 DSN。

## 覆盖表

配置与路由：

1. `gateways`
2. `accounts`
3. `account_gateway_routes`

交易账本：

1. `orders`
2. `order_events`
3. `fills`
4. `etf_component_transfers`
5. `raw_stream_messages`

账户账表：

1. `positions`
2. `position_snapshots`
3. `asset_snapshots`
4. `cash_ledger`
5. `performance_fee_rules`
6. `performance_nav_baselines`
7. `performance_nav_versions`
8. `performance_nav_reconciliations`
9. `reverse_repo_accruals`
10. `performance_attribution_links`
11. `performance_account_inceptions`
12. `performance_position_cost_states`

盘后对账：

1. `reconciliation_runs`
2. `reconciliation_inputs`
3. `reconciliation_breaks`

运行位点：

1. `stream_checkpoints`
2. `stream_dlq_reviews`

日流程任务：

1. `job_runs`

研究导出 view：

1. `research_account_daily_performance_v1`
2. `research_order_fill_export_v1`

## 关键约束

1. `orders(account_id, trade_date, gateway_order_id)` 唯一；柜台订单号只要求账户内当日唯一。
2. `fills(account_id, trade_date, gateway_order_id, fill_id)` 在 `fill_id` 存在时唯一；前置/柜台的 `fill_id` 不能假设为跨日全局唯一。
3. 如果 `fill_id` 缺失，`fills` 使用包含 `account_id + trade_date + order_stream_id + order_id + symbol + exchange + match_timestamp + qty + price` 的 fallback 去重。
4. `order_events` 和 `fills` 对 `stream_key + stream_id` 做唯一约束，避免重复消费写入。
5. `raw_stream_messages` 归档每条 Redis Stream 原始消息，保留 `body`、`body_text` 和 `parse_error`。
6. 金额和价格字段使用 `numeric(20, 6)`，避免浮点误差进入最终账本。
7. 时间字段统一使用 `timestamptz`，原始柜台时间戳保留在 raw 或 adapter 字段。
8. `stream_checkpoints(stream_key)` 唯一记录每条 output stream 的最后消费 ID；worker 重启后从该 ID 继续 `XREAD`。
9. `job_runs(run_id)` 唯一记录每次盘前初始化、盘后结算或后续后台任务运行，完整报告保存在 `report_json`，`/v1/status` 只返回摘要。
10. `000012_trade_date_order_scope` 删除旧账户级订单唯一约束，将 `orders/fills/order_events` 的唯一键、外键和查询视图统一为交易日作用域；该迁移不可逆，避免回滚时丢弃合法跨日订单。
11. `etf_component_transfers` 与 `fills` 分表：普通成交只接受正价格、正数量和稳定订单标识，ETF 申购赎回成分划转保持独立 transfer 语义，空 `component_value` 必须保留为 `NULL`。
12. `raw_stream_messages` 对 `stream_role=dlq AND action=adapter.data_quality` 建部分索引，供后续质量告警和人工处置查询。
13. `stream_dlq_reviews` 通过 `(stream_key, stream_id)` 外键关联 raw DLQ，每次确认、忽略或标记已重放均追加不可变记录；当前状态取最新 `review_id`，未审核消息视为 `pending`。
14. `000024_oc_position_cost_quality` 为当前持仓和历史快照增加 `total_cost/avg_cost_source/cost_complete`；这些字段记录柜台成本质量，绝不能代替行情市值。

## 手动执行示例

以下命令仅用于本地或部署机执行，不要把真实 DSN 写入仓库：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000002_stream_checkpoints.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000003_job_runs.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000006_research_performance_views.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000007_open_asset_snapshots.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000008_position_day_pnl.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000009_performance_accounting.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000012_trade_date_order_scope.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000013_oc_v1_1_component_transfers.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000014_remove_etf_transfer_summary_fills.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000015_stream_operations.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000016_stream_operations_indexes.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000020_performance_cost_ledger.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000021_performance_nav_gold.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000022_order_fee_records.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000023_position_cost_corporate_actions.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000024_oc_position_cost_quality.up.sql
```

使用 relayctl：

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" go run ./cmd/relayctl migrate up
```

已有历史账表升级到 `000012` 后，先执行 `relayctl ledger-replay`，确认同日孤立订单事件和成交均为 0，再验证复合外键：

```sql
ALTER TABLE order_events VALIDATE CONSTRAINT order_events_order_fk;
ALTER TABLE fills VALIDATE CONSTRAINT fills_order_fk;
```

生产库两条约束均已完成验证。不要在重放前强制验证，否则旧二元订单键造成的历史孤立记录会使验证失败。

回滚：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.down.sql
```

使用 relayctl 回滚最近一步：

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" go run ./cmd/relayctl migrate down -steps 1
```

## 待增强项

1. 本机全量备份与临时恢复演练已完成；后续补异机副本、加密、保留周期和恢复时间告警。
2. 内置模拟柜台 migration 暂缓；历史行情模拟撮合由回测引擎负责，外部模拟柜台如需接入应复用前置/Redis Stream 协议。
