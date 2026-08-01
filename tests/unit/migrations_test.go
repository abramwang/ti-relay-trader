package unit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitialLedgerMigrationContainsRequiredTables(t *testing.T) {
	upSQL := readMigration(t, "000001_init_ledger.up.sql")
	requiredTables := []string{
		"gateways",
		"accounts",
		"account_gateway_routes",
		"orders",
		"order_events",
		"fills",
		"raw_stream_messages",
		"positions",
		"position_snapshots",
		"asset_snapshots",
		"cash_ledger",
		"reconciliation_runs",
		"reconciliation_inputs",
		"reconciliation_breaks",
	}

	for _, table := range requiredTables {
		if !strings.Contains(upSQL, "CREATE TABLE "+table+" ") && !strings.Contains(upSQL, "CREATE TABLE "+table+"\n") {
			t.Fatalf("migration missing CREATE TABLE for %s", table)
		}
	}
}

func TestInitialLedgerMigrationHasCriticalConstraints(t *testing.T) {
	upSQL := readMigration(t, "000001_init_ledger.up.sql")
	requiredSnippets := []string{
		"CONSTRAINT orders_gateway_order_unique UNIQUE (account_id, gateway_order_id)",
		"CREATE UNIQUE INDEX fills_fill_id_unique",
		"CREATE UNIQUE INDEX fills_fallback_unique",
		"CONSTRAINT raw_stream_messages_unique UNIQUE (stream_key, stream_id)",
		"NUMERIC(20, 6)",
		"JSONB NOT NULL DEFAULT '{}'::jsonb",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("migration missing critical snippet: %s", snippet)
		}
	}
}

func TestInitialLedgerRollbackDropsRequiredTables(t *testing.T) {
	downSQL := readMigration(t, "000001_init_ledger.down.sql")
	for _, table := range []string{"orders", "fills", "accounts", "gateways"} {
		if !strings.Contains(downSQL, "DROP TABLE IF EXISTS "+table) {
			t.Fatalf("rollback missing DROP TABLE for %s", table)
		}
	}
}

func TestStreamCheckpointMigrationContainsCursorTable(t *testing.T) {
	upSQL := readMigration(t, "000002_stream_checkpoints.up.sql")
	requiredSnippets := []string{
		"CREATE TABLE stream_checkpoints",
		"stream_key TEXT PRIMARY KEY",
		"last_stream_id TEXT NOT NULL DEFAULT '0'",
		"processed_count BIGINT NOT NULL DEFAULT 0",
		"CONSTRAINT stream_checkpoints_role_check CHECK",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("stream checkpoint migration missing snippet: %s", snippet)
		}
	}
}

func TestStreamCheckpointRollbackDropsCursorTable(t *testing.T) {
	downSQL := readMigration(t, "000002_stream_checkpoints.down.sql")
	if !strings.Contains(downSQL, "DROP TABLE IF EXISTS stream_checkpoints") {
		t.Fatalf("stream checkpoint rollback missing DROP TABLE")
	}
}

func TestStreamOperationsMigrationAddsAuditedDLQReviews(t *testing.T) {
	upSQL := readMigration(t, "000015_stream_operations.up.sql")
	for _, snippet := range []string{
		"CREATE TABLE stream_dlq_reviews",
		"FOREIGN KEY (stream_key, stream_id)",
		"REFERENCES raw_stream_messages(stream_key, stream_id)",
		"CHECK (status IN ('acknowledged', 'ignored', 'replayed'))",
		"CREATE INDEX stream_dlq_reviews_message_idx",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("stream operations migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000015_stream_operations.down.sql")
	if !strings.Contains(downSQL, "DROP TABLE IF EXISTS stream_dlq_reviews") {
		t.Fatal("stream operations rollback missing review table")
	}
}

func TestStreamOperationsIndexesCoverRuntimeFilters(t *testing.T) {
	upSQL := readMigration(t, "000016_stream_operations_indexes.up.sql")
	for _, snippet := range []string{
		"CREATE INDEX raw_stream_messages_dlq_operations_idx",
		"WHERE stream_role = 'dlq'",
		"CREATE INDEX raw_stream_messages_broker_not_ready_idx",
		"WHERE code = 'BROKER_NOT_READY'",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("stream operations index migration missing snippet: %s", snippet)
		}
	}
}

func TestOCV12MigrationAddsCancelAttemptAudit(t *testing.T) {
	upSQL := readMigration(t, "000017_oc_v1_2_cancel_attempts.up.sql")
	for _, snippet := range []string{
		"CREATE TABLE order_cancel_attempts",
		"UNIQUE (account_id, attempt_id)",
		"reconciliation_required BOOLEAN NOT NULL DEFAULT FALSE",
		"CHECK (status IN ('accepted', 'rejected', 'timeout', 'outcome_unknown', 'not_ready', 'failed'))",
		"CREATE INDEX order_cancel_attempts_reconciliation_idx",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("OC v1.2 migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000017_oc_v1_2_cancel_attempts.down.sql")
	if !strings.Contains(downSQL, "DROP TABLE IF EXISTS order_cancel_attempts") {
		t.Fatal("OC v1.2 rollback missing cancel attempt table")
	}
}

func TestReconciliationIdempotencyMigrationContainsUniqueIndexes(t *testing.T) {
	upSQL := readMigration(t, "000004_reconciliation_idempotency.up.sql")
	for _, snippet := range []string{
		"CREATE UNIQUE INDEX reconciliation_inputs_unique",
		"CREATE UNIQUE INDEX reconciliation_breaks_unique",
		"COALESCE(account_id, '')",
		"COALESCE(object_id, '')",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("reconciliation idempotency migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000004_reconciliation_idempotency.down.sql")
	if !strings.Contains(downSQL, "DROP INDEX IF EXISTS reconciliation_breaks_unique") {
		t.Fatalf("reconciliation idempotency rollback missing drop index")
	}
}

func TestOrderIdempotencyMigrationCleansQueryKeysAndAddsUniqueIndex(t *testing.T) {
	upSQL := readMigration(t, "000019_order_idempotency_unique.up.sql")
	for _, snippet := range []string{
		"idempotency_key LIKE 'orders:query:%'",
		"historical_duplicate_key",
		"CREATE UNIQUE INDEX orders_idempotency_unique",
		"ON orders(account_id, idempotency_key)",
		"WHERE idempotency_key IS NOT NULL",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("order idempotency migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000019_order_idempotency_unique.down.sql")
	if !strings.Contains(downSQL, "DROP INDEX IF EXISTS orders_idempotency_unique") ||
		!strings.Contains(downSQL, "relay_idempotency_cleanup") {
		t.Fatal("order idempotency rollback must drop the unique index and restore audited keys")
	}
}

func TestFillIDOrderScopeMigrationReplacesAccountScopedIndex(t *testing.T) {
	upSQL := readMigration(t, "000005_fill_id_order_scope.up.sql")
	for _, snippet := range []string{
		"DROP INDEX IF EXISTS fills_fill_id_unique",
		"CREATE UNIQUE INDEX fills_fill_id_order_unique",
		"ON fills(account_id, gateway_order_id, fill_id)",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("fill id order scope migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000005_fill_id_order_scope.down.sql")
	for _, snippet := range []string{
		"DROP INDEX IF EXISTS fills_fill_id_order_unique",
		"CREATE UNIQUE INDEX fills_fill_id_unique",
		"ON fills(account_id, fill_id)",
	} {
		if !strings.Contains(downSQL, snippet) {
			t.Fatalf("fill id order scope rollback missing snippet: %s", snippet)
		}
	}
}

func TestOpenAssetSnapshotMigrationExtendsSnapshotType(t *testing.T) {
	upSQL := readMigration(t, "000007_open_asset_snapshots.up.sql")
	for _, snippet := range []string{
		"DROP CONSTRAINT IF EXISTS asset_snapshots_type_check",
		"CHECK (snapshot_type IN ('intraday', 'open', 'close', 'reconcile'))",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("open asset snapshot migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000007_open_asset_snapshots.down.sql")
	for _, snippet := range []string{
		"DROP CONSTRAINT IF EXISTS asset_snapshots_type_check",
		"CHECK (snapshot_type IN ('intraday', 'close', 'reconcile'))",
	} {
		if !strings.Contains(downSQL, snippet) {
			t.Fatalf("open asset snapshot rollback missing snippet: %s", snippet)
		}
	}
}

func TestPositionDayPnLMigrationAddsColumnsAndViewMetric(t *testing.T) {
	upSQL := readMigration(t, "000008_position_day_pnl.up.sql")
	for _, snippet := range []string{
		"ADD COLUMN IF NOT EXISTS day_unrealized_pnl",
		"DROP VIEW IF EXISTS research_account_daily_performance_v1",
		"COALESCE(sum(day_unrealized_pnl), 0) AS day_unrealized_pnl",
		"COALESCE(positions.settled_profit, 0) + COALESCE(positions.day_unrealized_pnl, 0) AS gross_pnl",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("position day pnl migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000008_position_day_pnl.down.sql")
	for _, snippet := range []string{
		"DROP COLUMN IF EXISTS day_unrealized_pnl",
		"COALESCE(positions.settled_profit, 0) + COALESCE(positions.unrealized_pnl, 0) AS gross_pnl",
	} {
		if !strings.Contains(downSQL, snippet) {
			t.Fatalf("position day pnl rollback missing snippet: %s", snippet)
		}
	}
}

func TestPerformanceAccountingMigrationAddsVersionedInputsAndOutputs(t *testing.T) {
	upSQL := readMigration(t, "000009_performance_accounting.up.sql")
	for _, snippet := range []string{
		"CREATE TABLE performance_fee_rules",
		"ADD COLUMN flow_class",
		"ADD COLUMN effective_at",
		"CREATE TABLE performance_nav_baselines",
		"CREATE TABLE performance_nav_versions",
		"CREATE TABLE performance_nav_reconciliations",
		"CREATE TABLE reverse_repo_accruals",
		"performance_nav_versions_current_unique",
		"cash_ledger_idempotency_unique",
		"repo_fee_rate",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("performance accounting migration missing snippet: %s", snippet)
		}
	}

	downSQL := readMigration(t, "000009_performance_accounting.down.sql")
	for _, snippet := range []string{
		"DROP TABLE IF EXISTS reverse_repo_accruals",
		"DROP TABLE IF EXISTS performance_nav_reconciliations",
		"DROP TABLE IF EXISTS performance_nav_versions",
		"DROP TABLE IF EXISTS performance_nav_baselines",
		"DROP TABLE IF EXISTS performance_fee_rules",
	} {
		if !strings.Contains(downSQL, snippet) {
			t.Fatalf("performance accounting rollback missing snippet: %s", snippet)
		}
	}
}

func TestStrategyAttributionKeysMigrationAddsTradeDateAndLinks(t *testing.T) {
	upSQL := readMigration(t, "000010_strategy_attribution_keys.up.sql")
	for _, snippet := range []string{
		"ADD COLUMN trade_date DATE",
		"ADD COLUMN strategy_type TEXT",
		"orders_account_trade_date_gateway_order_unique",
		"CREATE TABLE performance_attribution_links",
		"performance_attribution_links_strategy_idx",
		"fills_t0_group_idx",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("strategy attribution migration missing snippet: %s", snippet)
		}
	}

	downSQL := readMigration(t, "000010_strategy_attribution_keys.down.sql")
	for _, snippet := range []string{
		"DROP TABLE IF EXISTS performance_attribution_links",
		"DROP INDEX IF EXISTS orders_account_trade_date_gateway_order_unique",
		"DROP COLUMN IF EXISTS trade_date",
		"DROP COLUMN IF EXISTS strategy_type",
	} {
		if !strings.Contains(downSQL, snippet) {
			t.Fatalf("strategy attribution rollback missing snippet: %s", snippet)
		}
	}
}

func TestTradeDateOrderScopeMigrationReplacesCompatibilityKeys(t *testing.T) {
	upSQL := readMigration(t, "000012_trade_date_order_scope.up.sql")
	for _, snippet := range []string{
		"DROP CONSTRAINT orders_gateway_order_unique",
		"UNIQUE (account_id, trade_date, gateway_order_id)",
		"FOREIGN KEY (account_id, trade_date, gateway_order_id)",
		"ON fills(account_id, trade_date, gateway_order_id, fill_id)",
		"AND fills.trade_date = orders.trade_date",
		"NOT VALID",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("trade date order scope migration missing snippet: %s", snippet)
		}
	}

	downSQL := readMigration(t, "000012_trade_date_order_scope.down.sql")
	if !strings.Contains(downSQL, "intentionally irreversible") {
		t.Fatal("trade date order scope rollback must refuse destructive downgrade")
	}
}

func TestPerformanceNAVGoldMigrationAddsVersionedAuditedInput(t *testing.T) {
	upSQL := readMigration(t, "000021_performance_nav_gold.up.sql")
	for _, snippet := range []string{
		"CREATE TABLE performance_nav_gold_versions",
		"GENERATED ALWAYS AS (close_asset - daily_pnl) STORED",
		"GENERATED ALWAYS AS (close_asset - daily_pnl - carried_open_asset) STORED",
		"UNIQUE (account_id, trade_date, source, version)",
		"CREATE UNIQUE INDEX performance_nav_gold_versions_current_unique",
		"WHERE is_current",
		"status <> 'confirmed' OR (confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL)",
		"raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("performance NAV gold migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000021_performance_nav_gold.down.sql")
	if !strings.Contains(downSQL, "DROP TABLE IF EXISTS performance_nav_gold_versions") {
		t.Fatal("performance NAV gold rollback missing table drop")
	}
}

func TestOrderFeeRecordsMigrationAddsAuditedOrderFees(t *testing.T) {
	upSQL := readMigration(t, "000022_order_fee_records.up.sql")
	for _, snippet := range []string{
		"CREATE TABLE order_fee_records",
		"UNIQUE (account_id, fee_record_id)",
		"record_scope = 'order'",
		"fee_complete BOOLEAN NOT NULL DEFAULT FALSE",
		"association_complete BOOLEAN NOT NULL DEFAULT FALSE",
		"fee_as_of TIMESTAMPTZ NOT NULL",
		"raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb",
		"CREATE INDEX order_fee_records_order_idx",
	} {
		if !strings.Contains(upSQL, snippet) {
			t.Fatalf("order fee records migration missing snippet: %s", snippet)
		}
	}
	downSQL := readMigration(t, "000022_order_fee_records.down.sql")
	if !strings.Contains(downSQL, "DROP TABLE IF EXISTS order_fee_records") {
		t.Fatal("order fee records rollback missing table drop")
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", "postgres", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}
