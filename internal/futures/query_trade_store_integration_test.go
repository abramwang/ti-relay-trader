package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func readyTradeQuery(t *testing.T, s *QueryStore, ctx context.Context, id, kind string, items ...[]byte) []byte {
	t.Helper()
	c := tradeQueryContext(kind)
	c.OriginMessageID = id
	registerQuery(t, s, ctx, id, c)
	raw := editBody(t, tradePage(t, kind, id, 0, true, items...), func(m map[string]any) { m["origin_message_id"] = id })
	if out, err := s.Ingest(ctx, id, source(id), raw, 0); err != nil || out.State != BatchReady {
		t.Fatal(out, err)
	}
	return raw
}

func TestTradeQueryApplicationRecoveryAndAssociation(t *testing.T) {
	db, trades, ctx := tradeTestDB(t)
	queries, _ := NewQueryStore(db)
	raw := readyTradeQuery(t, queries, ctx, "fills", "fill", tradeFixture(t, "fill"))
	if storedCount(t, db, "futures_execution_facts") != 0 {
		t.Fatal("assembly applied facts implicitly")
	}
	queries, _ = NewQueryStore(db)
	ids, err := queries.ListUnappliedTradeQueries(ctx, 10)
	if err != nil || len(ids) != 1 || ids[0] != "fills" {
		t.Fatal(ids, err)
	}
	if _, err := queries.Recover(ctx, "fills"); err != nil {
		t.Fatal(err)
	}
	applied, err := queries.ApplyTradeQuery(ctx, "fills")
	if err != nil || !applied.Applied || applied.Replayed || applied.RowCount != 1 {
		t.Fatal(applied, err)
	}
	if _, err := queries.Current(ctx, tradeQueryContext("fill")); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("history promoted readiness", err)
	}
	r, err := trades.Read(ctx, tradeScope())
	if err != nil || !r.Blocked || r.PendingCount != 1 || len(r.Orders) != 0 {
		t.Fatal(r, err)
	}
	// Row bytes come from the original page, never a reserialized typed row.
	var p struct {
		Payload struct {
			Items []json.RawMessage `json:"items"`
		} `json:"payload"`
	}
	json.Unmarshal(raw, &p)
	var saved []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_trade_observations`).Scan(&saved); err != nil || !bytes.Equal(saved, p.Payload.Items[0]) {
		t.Fatal("row audit changed", err)
	}
	readyTradeQuery(t, queries, ctx, "orders", "order", tradeFixture(t, "order"))
	if _, err := queries.ApplyTradeQuery(ctx, "orders"); err != nil {
		t.Fatal(err)
	}
	r, err = trades.Read(ctx, tradeScope())
	if err != nil || r.Blocked || r.PendingCount != 0 || len(r.Executions) != 1 || len(r.Orders) != 1 {
		t.Fatal(r, err)
	}
	queries, _ = NewQueryStore(db)
	applied, err = queries.ApplyTradeQuery(ctx, "fills")
	if err != nil || !applied.Replayed {
		t.Fatal(applied, err)
	}
	if storedCount(t, db, "futures_trade_observations") != 2 {
		t.Fatal("retry duplicated row")
	}
	ids, err = queries.ListUnappliedTradeQueries(ctx, 10)
	if err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
}

func TestTradeQueryPartialDuplicateAndEmptyNeverClear(t *testing.T) {
	db, trades, ctx := tradeTestDB(t)
	q, _ := NewQueryStore(db)
	registerQuery(t, q, ctx, "partial", tradeQueryContext("order"))
	if _, err := q.Ingest(ctx, "partial", source("last"), tradePage(t, "order", "last", 1, true, tradeFixture(t, "order")), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ApplyTradeQuery(ctx, "partial"); err == nil {
		t.Fatal("missing page applied")
	}
	if storedCount(t, db, "futures_trade_scopes") != 0 {
		t.Fatal("partial created ledger")
	}
	if out, err := q.Ingest(ctx, "partial", source("first"), tradePage(t, "order", "first", 0, false, tradeFixture(t, "order")), 0); err != nil || out.State != BatchInvalid {
		t.Fatal(out, err)
	}
	if _, err := q.ApplyTradeQuery(ctx, "partial"); err == nil {
		t.Fatal("duplicate query applied")
	}
	readyTradeQuery(t, q, ctx, "valid", "order", tradeFixture(t, "order"))
	if _, err := q.ApplyTradeQuery(ctx, "valid"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"order", "fill"} {
		id := "empty-" + kind
		readyTradeQuery(t, q, ctx, id, kind)
		result, err := q.ApplyTradeQuery(ctx, id)
		if err != nil || result.RowCount != 0 {
			t.Fatal(result, err)
		}
		snapshot, err := q.Snapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "order" && snapshot.Candidate.Orders == nil || kind == "fill" && snapshot.Candidate.Fills == nil {
			t.Fatal("empty result not explicit")
		}
	}
	r, err := trades.Read(ctx, tradeScope())
	if err != nil || len(r.Orders) != 1 {
		t.Fatal("empty snapshot cleared facts", r, err)
	}
	if _, err := q.ListUnappliedTradeQueries(ctx, 0); err == nil {
		t.Fatal("unbounded listing")
	}
}

func TestTradeQueryLateConflictBlocksAppliedFacts(t *testing.T) {
	db, trades, ctx := tradeTestDB(t)
	q, _ := NewQueryStore(db)
	raw := readyTradeQuery(t, q, ctx, "orders", "order", tradeFixture(t, "order"))
	if _, err := q.ApplyTradeQuery(ctx, "orders"); err != nil {
		t.Fatal(err)
	}
	r, err := trades.Read(ctx, tradeScope())
	if err != nil || r.Blocked {
		t.Fatal(r, err)
	}
	conflict := editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} })
	if out, err := q.Ingest(ctx, "orders", source("different"), conflict, 0); err != nil || out.State != BatchInvalid {
		t.Fatal(out, err)
	}
	r, err = trades.Read(ctx, tradeScope())
	if err != nil || !r.Blocked || len(r.Orders) != 1 || !strings.Contains(strings.Join(r.Conflicts, ","), "applied query invalidated: orders") {
		t.Fatal(r, err)
	}
	if _, err := q.ApplyTradeQuery(ctx, "orders"); err == nil {
		t.Fatal("invalid application replay reported success")
	}
	// New observations cannot hide a contradiction in an already applied query.
	if out, err := trades.Observe(ctx, tradeScope(), "event/new", "order", tradeFixture(t, "order")); err != nil || !out.Blocked {
		t.Fatal(out, err)
	}
	r, err = trades.Read(ctx, tradeScope())
	if err != nil || !r.Blocked {
		t.Fatal(r, err)
	}
}

func TestTradeQueryApplicationRollbackAndRetry(t *testing.T) {
	db, trades, ctx := tradeTestDB(t)
	q, _ := NewQueryStore(db)
	second := editBody(t, tradeFixture(t, "order"), func(m map[string]any) {
		m["gateway_order_id"] = "second"
		m["native_ids"].(map[string]any)["order_sys_id"] = "00001235"
	})
	readyTradeQuery(t, q, ctx, "orders", "order", tradeFixture(t, "order"), second)
	// Force failure after row/projection writes, before the application marker.
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_application() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$;
        CREATE TRIGGER fail_application BEFORE INSERT ON futures_query_trade_applications FOR EACH ROW EXECUTE FUNCTION fail_application()`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ApplyTradeQuery(ctx, "orders"); err == nil {
		t.Fatal("failure ignored")
	}
	for _, table := range []string{"futures_trade_scopes", "futures_trade_observations", "futures_order_states", "futures_query_trade_applications"} {
		if storedCount(t, db, table) != 0 {
			t.Fatal("partial transaction persisted", table)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_application ON futures_query_trade_applications`); err != nil {
		t.Fatal(err)
	}
	q, _ = NewQueryStore(db)
	out, err := q.ApplyTradeQuery(ctx, "orders")
	if err != nil || out.RowCount != 2 {
		t.Fatal(out, err)
	}
	r, err := trades.Read(ctx, tradeScope())
	if err != nil || len(r.Orders) != 2 || r.Blocked {
		t.Fatal(r, err)
	}
}

func TestTradeQueryApplicationConcurrentReplay(t *testing.T) {
	db, _, ctx := tradeTestDB(t)
	q, _ := NewQueryStore(db)
	readyTradeQuery(t, q, ctx, "orders", "order", tradeFixture(t, "order"))
	var wg sync.WaitGroup
	results := make(chan TradeQueryApplication, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out, err := q.ApplyTradeQuery(ctx, "orders"); results <- out; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	for out := range results {
		if !out.Replayed {
			applied++
		}
	}
	if applied != 1 || storedCount(t, db, "futures_trade_observations") != 1 || storedCount(t, db, "futures_query_trade_applications") != 1 {
		t.Fatal("concurrent replay duplicated application", applied)
	}
}

func TestTradeQueryNativeAuthorizationAndHistoricalFacts(t *testing.T) {
	db, _, ctx := tradeTestDB(t)
	q, _ := NewQueryStore(db)
	readyTradeQuery(t, q, ctx, "orders", "order", tradeFixture(t, "order"))
	c := tradeQueryContext("order")
	c.NativeInvestorID = "OTHER"
	if _, err := q.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("wrong native scope returned", err)
	}
	readyTradeQuery(t, q, ctx, "later-empty", "order")
	if _, err := q.ApplyTradeQuery(ctx, "orders"); err != nil {
		t.Fatal("complete historical query rejected", err)
	}
	current, err := q.Current(ctx, tradeQueryContext("order"))
	if err != nil || current.BatchID != "later-empty" {
		t.Fatal("old apply changed current pointer", current, err)
	}
	if _, err := q.ApplyTradeQuery(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestTradeQueryApplicationCapacityNoPartialCommit(t *testing.T) {
	db, _, ctx := tradeTestDB(t)
	q, _ := NewQueryStore(db)
	items := make([][]byte, 0, maxAuditCount+1)
	base := tradeFixture(t, "order")
	for i := 0; i <= maxAuditCount; i++ {
		items = append(items, editBody(t, base, func(m map[string]any) {
			m["gateway_order_id"] = fmt.Sprint("order-", i)
			m["native_ids"].(map[string]any)["order_sys_id"] = fmt.Sprint(100000 + i)
		}))
	}
	readyTradeQuery(t, q, ctx, "too-many", "order", items...)
	if _, err := q.ApplyTradeQuery(ctx, "too-many"); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal(err)
	}
	if storedCount(t, db, "futures_trade_scopes") != 0 || storedCount(t, db, "futures_query_trade_applications") != 0 {
		t.Fatal("capacity partially applied")
	}
}
