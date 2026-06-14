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

func readMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", "postgres", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}
