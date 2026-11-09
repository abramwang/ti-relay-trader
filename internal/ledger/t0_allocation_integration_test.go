package ledger

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestT0AllocationPostgresAppendOnly(t *testing.T) {
	dsn := os.Getenv("RELAY_T0_ALLOCATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated T0 allocation test database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The runner supplies a disposable cluster, never an application DSN.
	ddl, err := os.ReadFile("../../migrations/postgres/000010_strategy_attribution_keys.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(ddl), "CREATE TABLE performance_attribution_links (")
	if start < 0 {
		t.Fatal("missing schema")
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE accounts(account_id TEXT PRIMARY KEY); INSERT INTO accounts VALUES ('audit-account');`+string(ddl[start:])); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	first := allocationFixture()
	if _, err = repo.AppendT0Allocation(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AppendT0Allocation(ctx, first); err != nil {
		t.Fatal("idempotent replay", err)
	}
	conflict := first
	conflict.Quantity++
	if _, err = repo.AppendT0Allocation(ctx, conflict); err == nil {
		t.Fatal("accepted same-id different payload")
	}
	second := first
	second.Revision = 2
	second.Supersedes = first.LinkID
	second.LinkID = "audit-2"
	second.GroupID = "new-group"
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, id := range []string{"audit-2a", "audit-2b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			candidate := second
			candidate.LinkID = id
			_, err := repo.AppendT0Allocation(ctx, candidate)
			errors <- err
		}(id)
	}
	wg.Wait()
	close(errors)
	successes := 0
	for err := range errors {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent revision successes=%d", successes)
	}
	history, err := repo.ListT0AllocationHistory(ctx, first.AccountID, first.TradeDate)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v error=%v", history, err)
	}
	current, err := ResolveT0Allocations(history)
	if err != nil {
		t.Fatal(err)
	}
	revoked := current[0]
	revoked.Revision++
	revoked.Supersedes = revoked.LinkID
	revoked.LinkID = "audit-revoked"
	revoked.Status = "revoked"
	if _, err = repo.AppendT0Allocation(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	history, err = repo.ListT0AllocationHistory(ctx, first.AccountID, first.TradeDate)
	if err != nil {
		t.Fatal(err)
	}
	current, err = ResolveT0Allocations(history)
	if err != nil || len(history) != 3 || len(current) != 0 || history[0].GroupID != "residual" {
		t.Fatalf("append-only history=%+v %v", history, err)
	}
	other, err := repo.ListT0AllocationHistory(ctx, "other", first.TradeDate)
	if err != nil || len(other) != 0 {
		t.Fatal("account isolation", err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewRepository(tx).AppendT0Allocation(ctx, first); err == nil {
		t.Fatal("unsafe revision visibility")
	}
	tx.Rollback()
	if _, err = db.ExecContext(ctx, `UPDATE performance_attribution_links SET quantity=5900.5 WHERE link_id='audit-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ListT0AllocationHistory(ctx, first.AccountID, first.TradeDate); err == nil {
		t.Fatal("accepted fractional/tampered envelope")
	}
}
