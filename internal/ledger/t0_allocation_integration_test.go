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
	t.Run("atomic batch and replay", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts VALUES ('batch-account')`); err != nil {
			t.Fatal(err)
		}
		var batch []T0Allocation
		for _, id := range []string{"batch-a", "batch-b", "batch-c"} {
			item := first
			item.AccountID, item.LinkID, item.GatewayOrderID = "batch-account", id, id
			if id == "batch-c" {
				item.ConfirmedAt, item.ConfirmedDate = time.Time{}, "2026-11-11"
			}
			batch = append(batch, item)
		}
		for attempt := 0; attempt < 3; attempt++ {
			tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for i, item := range batch {
				if attempt == 0 && i == 2 {
					item.Quantity = 0
				}
				_, err := NewRepository(tx).AppendT0Allocation(ctx, item)
				if attempt == 0 && i == 2 {
					if err == nil {
						t.Fatal("invalid third row accepted")
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			want := 3
			if attempt == 0 {
				want = 0
				err = tx.Rollback()
			} else {
				err = tx.Commit()
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := repo.ListT0AllocationHistory(ctx, "batch-account", first.TradeDate)
			if err != nil || len(rows) != want {
				t.Fatalf("attempt %d: rows=%d want=%d error=%v", attempt, len(rows), want, err)
			}
		}
	})
	t.Run("date precision storage and legacy isolation", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts VALUES ('date-account')`); err != nil {
			t.Fatal(err)
		}
		item := first
		item.AccountID, item.LinkID, item.GatewayOrderID = "date-account", "date-link", "date-order"
		item.ConfirmedAt, item.ConfirmedDate = time.Time{}, "2026-11-11"
		for i := 0; i < 2; i++ {
			if _, err := repo.AppendT0Allocation(ctx, item); err != nil {
				t.Fatal(err)
			}
		}
		history, err := repo.ListT0AllocationHistory(ctx, item.AccountID, item.TradeDate)
		if err != nil || len(history) != 1 || history[0].ConfirmedDate != item.ConfirmedDate || !history[0].ConfirmedAt.IsZero() {
			t.Fatal(history, err)
		}
		var source string
		var hasTime bool
		if err := db.QueryRowContext(ctx, `SELECT source, source_payload ? 'confirmed_at' FROM performance_attribution_links WHERE link_id=$1`, item.LinkID).Scan(&source, &hasTime); err != nil || source != t0AllocationDateSource || hasTime {
			t.Fatal(source, hasTime, err)
		}
		var legacyRows int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM performance_attribution_links WHERE account_id=$1 AND source=$2`, item.AccountID, t0AllocationSource).Scan(&legacyRows); err != nil || legacyRows != 0 {
			t.Fatal("legacy reader exposed to date-only rows", legacyRows, err)
		}
		changed := item
		changed.ConfirmedDate = "2026-11-12"
		if _, err := repo.AppendT0Allocation(ctx, changed); err == nil {
			t.Fatal("same-id evidence overwritten")
		}
		if _, err := db.ExecContext(ctx, `UPDATE performance_attribution_links SET source=$1 WHERE link_id=$2`, t0AllocationSource, item.LinkID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ListT0AllocationHistory(ctx, item.AccountID, item.TradeDate); err == nil {
			t.Fatal("precision envelope corruption accepted")
		}
	})
}
