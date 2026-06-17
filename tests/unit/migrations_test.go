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

func readMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", "postgres", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}
