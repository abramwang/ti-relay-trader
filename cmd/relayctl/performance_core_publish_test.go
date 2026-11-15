package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestCoreOriginBookGate(t *testing.T) {
	makeDay := func() researchQueryDay {
		d := researchQueryDay{Date: "2026-11-10", Attempts: []researchQueryAttempt{{LinkedToClose: true}}}
		for _, step := range []string{"orders", "fills"} {
			d.Attempts[0].Queries = append(d.Attempts[0].Queries, researchQueryProof{Step: step, State: "terminal_archive_consistent", BookCoverage: &researchBookCoverage{RequestState: "account_day_unfiltered_request", Rows: researchBookComparison{State: "exact_projection_match"}}})
		}
		return d
	}
	if err := verifyCoreOriginBooks(makeDay()); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*researchQueryDay){
		"missing":   func(d *researchQueryDay) { d.Attempts[0].Queries = d.Attempts[0].Queries[:1] },
		"ambiguous": func(d *researchQueryDay) { d.Attempts = append(d.Attempts, d.Attempts[0]) },
		"partial":   func(d *researchQueryDay) { d.Attempts[0].Queries[0].State = "not_proven" },
		"filtered":  func(d *researchQueryDay) { d.Attempts[0].Queries[0].BookCoverage.RequestState = "filtered" },
		"parse error": func(d *researchQueryDay) {
			d.Attempts[0].Queries[0].BookCoverage.DecodeIssues = []researchDecodeIssue{{Error: "bad"}}
		},
		"unproven extra": func(d *researchQueryDay) {
			d.Attempts[0].Queries[0].BookCoverage.Rows.Differences = []researchBookDifference{{Kind: "book_only_not_in_this_query"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := makeDay()
			change(&d)
			if verifyCoreOriginBooks(d) == nil {
				t.Fatal("unsafe query evidence accepted")
			}
		})
	}
}

func TestCorePublishArgumentGuards(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--config", "config/relay.prod.yaml", "--plan", "p", "--plan-sha256", strings.Repeat("a", 64), "--output", "x", "--apply"}} {
		if runCoreCostPublish(args) == nil {
			t.Fatal("missing backup/scope accepted")
		}
	}
}

func TestFuyingResearchCorePublishPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_RESEARCH_COST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	all, _, err := loadResearchMigration()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbmigrations.NewRunner(db).Up(ctx, all[:32]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts(account_id,broker_id) VALUES('a','huaxin')`); err != nil {
		t.Fatal(err)
	}
	c := performance.CostLedgerResult{AccountID: "a", TradeDate: "2026-11-10", Status: "calculated", Positions: []ledger.PositionCostState{{AccountID: "a", TradeDate: "2026-11-10", Symbol: "510300", Exchange: "SH", CostBucket: "CORE", Status: "calculated", FormulaVersion: "performance_position_cost.v3.3", BuyQuantity: 100, BuyAmount: 400, BuyFee: 2, CloseQuantity: 100, CloseTotalCost: 402, AverageCost: 4.02, BrokerCloseQuantity: 100, ClosePrice: 4, CloseMarketValue: 400, UnrealizedPnL: -2, CorporateActionType: "none", FeeSource: "actual", OpeningSource: "relay_previous_close_cost"}}}
	sha := strings.Repeat("a", 64)
	begin := func(ro bool) *sql.Tx {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: ro})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx
	}
	tx := begin(true)
	if _, err := insertCoreRecovery(ctx, tx, "a", sha, []performance.CostLedgerResult{c}); err == nil {
		t.Fatal("read-only write succeeded")
	}
	tx.Rollback()
	tx = begin(false)
	if n, err := insertCoreRecovery(ctx, tx, "a", sha, []performance.CostLedgerResult{c}); err != nil || n != 1 {
		t.Fatalf("insert %d %v", n, err)
	}
	tx.Rollback()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM performance_position_cost_states`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback failed", err, count)
	}
	tx = begin(false)
	if _, err := insertCoreRecovery(ctx, tx, "a", sha, []performance.CostLedgerResult{c}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx = begin(false)
	if _, err := insertCoreRecovery(ctx, tx, "a", sha, []performance.CostLedgerResult{c}); err == nil {
		t.Fatal("existing target overwritten")
	}
	tx.Rollback()
	rows, err := ledger.NewRepository(db).ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: "a", TradeDate: c.TradeDate})
	if err != nil || len(rows) != 1 || rows[0].Source != "relay.core_cost_recovery:"+sha || rows[0].CloseTotalCost != 402 {
		t.Fatal("readback", err, rows)
	}
	second := c
	second.TradeDate = "2026-11-11"
	second.Positions = append([]ledger.PositionCostState(nil), c.Positions...)
	second.Positions[0].TradeDate = second.TradeDate
	tx = begin(false)
	if _, err := insertCoreRecovery(ctx, tx, "a", sha, []performance.CostLedgerResult{second, c}); err == nil {
		t.Fatal("partial target conflict accepted")
	}
	tx.Rollback()
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM performance_position_cost_states`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial conflict inserted rows", err, count)
	}
	first := begin(false)
	if _, err := insertCoreRecovery(ctx, first, "a", sha, []performance.CostLedgerResult{second}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			result <- err
			return
		}
		defer tx.Rollback()
		_, err = insertCoreRecovery(ctx, tx, "a", sha, []performance.CostLedgerResult{second})
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("second writer did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("concurrent target overwritten")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
