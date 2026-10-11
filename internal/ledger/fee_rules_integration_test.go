package ledger

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestFeeRuleLookaheadUsesIsolatedPostgresTable(t *testing.T) {
	dsn := os.Getenv("RELAY_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RELAY_LEDGER_TEST_DATABASE_URL for isolated fee-rule integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// A temporary shadow table and explicit primary keys avoid real rules and sequences.
	if _, err := tx.ExecContext(ctx, `
CREATE TEMP TABLE performance_fee_rules (LIKE public.performance_fee_rules INCLUDING DEFAULTS) ON COMMIT DROP;
INSERT INTO performance_fee_rules (fee_rule_pk, rule_id, account_id, version, status, effective_from)
SELECT n, 'isolated-fee-' || n, 'isolated-account', n, 'active', DATE '2026-10-10'
FROM generate_series(1, 501) AS n;
`); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(tx)
	query := FeeRuleQuery{AccountID: "isolated-account", Status: "active", EffectiveOn: "20261010", Limit: 500}
	rules, err := repo.ListFeeRules(ctx, query)
	if err != nil || len(rules) != 500 {
		t.Fatalf("normal list: rows=%d err=%v", len(rules), err)
	}
	query.FetchExtra = true
	rules, err = repo.ListFeeRules(ctx, query)
	if err != nil || len(rules) != 501 {
		t.Fatalf("lookahead: rows=%d err=%v", len(rules), err)
	}
	query.EffectiveOn = "20261009"
	rules, err = repo.ListFeeRules(ctx, query)
	if err != nil || len(rules) != 0 {
		t.Fatalf("effective-date filter: rows=%d err=%v", len(rules), err)
	}
	query.EffectiveOn = "20261010"
	query.AccountID = "other-account"
	rules, err = repo.ListFeeRules(ctx, query)
	if err != nil || len(rules) != 0 {
		t.Fatalf("account filter: rows=%d err=%v", len(rules), err)
	}
}
