# relay PostgreSQL Migration

更新时间：`2026-06-14`

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
7. `relay_schema_migrations` 已记录版本 `1:init_ledger` 到 `6:research_performance_views`。

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

1. `UpsertAccount`
2. `UpsertOrder`
3. `AppendOrderEvent`
4. `InsertFill`
5. `ArchiveRawStreamMessage`
6. `GetStreamCheckpoint`
7. `UpsertStreamCheckpoint`
8. `UpsertJobRun`
9. `LatestJobRuns`

这些入口会把标准交易结构体、stream key、stream id、source/correlation 信息和原始 payload 写入 PostgreSQL。重复消费场景使用唯一约束和 `ON CONFLICT` 做幂等处理。

可选集成测试：

```bash
RELAY_LEDGER_TEST_DATABASE_URL="$RELAY_DATABASE_URL" go test ./internal/ledger -run TestRepositoryWritesToPostgres -count=1 -v
```

该测试默认跳过；设置测试库 DSN 后会写入一组临时账户、订单、事件、成交和原始 stream 消息，并在测试清理阶段删除。

## 覆盖表

配置与路由：

1. `gateways`
2. `accounts`
3. `account_gateway_routes`

交易账本：

1. `orders`
2. `order_events`
3. `fills`
4. `raw_stream_messages`

账户账表：

1. `positions`
2. `position_snapshots`
3. `asset_snapshots`
4. `cash_ledger`

盘后对账：

1. `reconciliation_runs`
2. `reconciliation_inputs`
3. `reconciliation_breaks`

运行位点：

1. `stream_checkpoints`

日流程任务：

1. `job_runs`

研究导出 view：

1. `research_account_daily_performance_v1`
2. `research_order_fill_export_v1`

## 关键约束

1. `orders(account_id, gateway_order_id)` 唯一，用作订单跨系统主键。
2. `fills(account_id, gateway_order_id, fill_id)` 在 `fill_id` 存在时唯一；前置/柜台的 `fill_id` 不能假设为账户级全局唯一。
3. 如果 `fill_id` 缺失，`fills` 使用 `account_id + order_stream_id + match_timestamp + qty + price` 作为 fallback 去重。
4. `order_events` 和 `fills` 对 `stream_key + stream_id` 做唯一约束，避免重复消费写入。
5. `raw_stream_messages` 归档每条 Redis Stream 原始消息，保留 `body`、`body_text` 和 `parse_error`。
6. 金额和价格字段使用 `numeric(20, 6)`，避免浮点误差进入最终账本。
7. 时间字段统一使用 `timestamptz`，原始柜台时间戳保留在 raw 或 adapter 字段。
8. `stream_checkpoints(stream_key)` 唯一记录每条 output stream 的最后消费 ID；worker 重启后从该 ID 继续 `XREAD`。
9. `job_runs(run_id)` 唯一记录每次盘前初始化、盘后结算或后续后台任务运行，完整报告保存在 `report_json`，`/v1/status` 只返回摘要。

## 手动执行示例

以下命令仅用于本地或部署机执行，不要把真实 DSN 写入仓库：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000002_stream_checkpoints.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000003_job_runs.up.sql
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000006_research_performance_views.up.sql
```

使用 relayctl：

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" go run ./cmd/relayctl migrate up
```

回滚：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.down.sql
```

使用 relayctl 回滚最近一步：

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" go run ./cmd/relayctl migrate down -steps 1
```

## 后续工作

1. 增加基于临时 PostgreSQL 的 CI 集成测试。
2. 补充盘后对账、任务状态和研究导出后续 migration。
3. 后续如需严格数据库级下单幂等，可在清理历史重复数据后补充 `orders(account_id, idempotency_key)` 部分唯一约束。
4. 内置模拟柜台 migration 暂缓；历史行情模拟撮合由回测引擎负责，外部模拟柜台如需接入应复用前置/Redis Stream 协议。
