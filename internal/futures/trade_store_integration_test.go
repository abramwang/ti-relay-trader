package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"testing"
)

func tradeTestDB(t *testing.T) (*sql.DB, *TradeStore, context.Context) {
	t.Helper()
	db, _, ctx := queryTestDB(t)
	s, err := NewTradeStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, s, ctx
}

func TestTradeStoreFillBeforeOrderSurvivesRestart(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	fill := tradeFixture(t, "fill")
	out, err := s.Observe(ctx, tradeScope(), "event/1/0", "fill", fill)
	if err != nil || out.PendingCount != 1 || !out.Blocked {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_execution_facts") != 1 || storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("pending fill fabricated order")
	}
	s, err = NewTradeStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "event/2/0", "order", tradeFixture(t, "order")); err != nil {
		t.Fatal(err)
	}
	r, err := s.Read(ctx, tradeScope())
	if err != nil || r.Blocked || r.PendingCount != 0 || len(r.Executions) != 1 {
		t.Fatal(r, err)
	}
	for _, record := range r.Executions {
		if record.State != "linked" || record.LinkedGateway != "fixture-order-1" {
			t.Fatal(record)
		}
	}
	out, err = s.Observe(ctx, tradeScope(), "event/1/0", "fill", fill)
	if err != nil || !out.Replayed || out.Blocked {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_trade_observations") != 2 {
		t.Fatal("replay repeated fact")
	}
	var saved []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_trade_observations WHERE source_id='event/1/0'`).Scan(&saved); err != nil || !bytes.Equal(saved, fill) {
		t.Fatal("raw evidence changed", err)
	}
}

func TestTradeStoreFeeVersionsNeverDuplicateExecution(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	if _, err := s.Observe(ctx, tradeScope(), "o", "order", tradeFixture(t, "order")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "f1", "fill", tradeFixture(t, "fill")); err != nil {
		t.Fatal(err)
	}
	raw := editBody(t, tradeFixture(t, "fill"), func(m map[string]any) {
		m["fee"] = "1.23000000000001"
		m["fee_complete"] = true
		m["counter_session_id"] = "new-process"
		m["gateway_order_id"] = nativeIdentity("order", tradeScope(), "SHFE", "00001234")
	})
	if out, err := s.Observe(ctx, tradeScope(), "f2", "fill", raw); err != nil || out.Blocked {
		t.Fatal(out, err)
	}
	r, err := s.Read(ctx, tradeScope())
	if err != nil || len(r.Executions) != 1 {
		t.Fatal(r, err)
	}
	for _, record := range r.Executions {
		if record.Fill.Qty != 1 || record.Fill.Fee != nil || len(record.FeeEvidence) != 2 || *record.FeeEvidence[1].Fee != "1.23000000000001" {
			t.Fatal("fee revisions lost or became final", record)
		}
	}
	if storedCount(t, db, "futures_execution_facts") != 1 {
		t.Fatal("duplicate execution")
	}
}

func TestTradeStoreConflictRetainsOriginalFacts(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	raw := tradeFixture(t, "fill")
	if _, err := s.Observe(ctx, tradeScope(), "f1", "fill", raw); err != nil {
		t.Fatal(err)
	}
	changed := editBody(t, raw, func(m map[string]any) { m["qty"] = 2 })
	out, err := s.Observe(ctx, tradeScope(), "f2", "fill", changed)
	if err != nil || !out.Blocked {
		t.Fatal(out, err)
	}
	r, err := s.Read(ctx, tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range r.Executions {
		if record.Fill.Qty != 1 || record.State != "conflict" {
			t.Fatal("conflicting fact overwrote original", record)
		}
	}
	if storedCount(t, db, "futures_trade_observations") != 2 || storedCount(t, db, "futures_execution_facts") != 1 {
		t.Fatal("conflict evidence lost")
	}
	if out, err := s.Observe(ctx, tradeScope(), "f1", "order", raw); err != nil || !out.Blocked {
		t.Fatal("source identity conflict not retained", out, err)
	}
	if storedCount(t, db, "futures_trade_observations") != 3 || storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("source conflict manufactured order")
	}
	if out, err := s.Observe(ctx, tradeScope(), "f1", "order", raw); err != nil || !out.Replayed || !out.Blocked {
		t.Fatal(out, err)
	}
}

func TestTradeStoreAliasCollisionRevokesLinks(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	if _, err := s.Observe(ctx, tradeScope(), "o1", "order", tradeFixture(t, "order")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "f1", "fill", tradeFixture(t, "fill")); err != nil {
		t.Fatal(err)
	}
	raw := editBody(t, tradeFixture(t, "order"), func(m map[string]any) { m["gateway_order_id"] = "other-local" })
	if out, err := s.Observe(ctx, tradeScope(), "o2", "order", raw); err != nil || !out.Blocked {
		t.Fatal(out, err)
	}
	var state string
	var link sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT link_status,linked_gateway FROM futures_execution_facts`).Scan(&state, &link); err != nil || state != "conflict" || link.Valid {
		t.Fatal("ambiguous link remained published", state, link, err)
	}
	if storedCount(t, db, "futures_order_states") != 2 {
		t.Fatal("real reported order discarded")
	}
}

func TestTradeStoreRollbackOfFactAndLink(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	_, err := db.ExecContext(ctx, `CREATE FUNCTION reject_execution() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN RAISE EXCEPTION 'injected trade projection failure'; END $$;
        CREATE TRIGGER execution_fault BEFORE INSERT ON futures_execution_facts FOR EACH ROW EXECUTE FUNCTION reject_execution()`)
	if err != nil {
		t.Fatal(err)
	}
	raw := tradeFixture(t, "fill")
	if _, err := s.Observe(ctx, tradeScope(), "f1", "fill", raw); err == nil {
		t.Fatal("database failure ignored")
	}
	for _, table := range []string{"futures_trade_scopes", "futures_trade_observations", "futures_execution_facts"} {
		if storedCount(t, db, table) != 0 {
			t.Fatal("partial trade transaction committed", table)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER execution_fault ON futures_execution_facts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "f1", "fill", raw); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Observe(ctx, tradeScope(), "f1", "fill", raw); err != nil || !out.Replayed {
		t.Fatal(out, err)
	}
}

func TestTradeStoreConcurrentReplayAndNewEvidence(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	raw := tradeFixture(t, "fill")
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "same"
			if i >= 8 {
				id = "new-" + strconv.Itoa(i)
			}
			_, err := s.Observe(ctx, tradeScope(), id, "fill", raw)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if storedCount(t, db, "futures_trade_observations") != 9 || storedCount(t, db, "futures_execution_facts") != 1 {
		t.Fatal("concurrent execution counted twice")
	}
	r, err := s.Read(ctx, tradeScope())
	if err != nil || r.PendingCount != 1 {
		t.Fatal(r, err)
	}
}

func TestTradeStoreTradingDayAndEnvironmentIsolation(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	for i := 0; i < 3; i++ {
		scope := tradeScope()
		if i == 1 {
			scope.TradeDate = "20261105"
		}
		if i == 2 {
			scope.Route.Env = "prod"
		}
		raw := editBody(t, tradeFixture(t, "fill"), func(m map[string]any) {
			m["trade_date"] = scope.TradeDate
			m["fill_id"] = nativeIdentity("fill", scope, "SHFE", "00005678")
		})
		if out, err := s.Observe(ctx, scope, "same-source", "fill", raw); err != nil || out.Replayed {
			t.Fatal(out, err)
		}
		r, err := s.Read(ctx, scope)
		if err != nil || len(r.Executions) != 1 || r.PendingCount != 1 {
			t.Fatal(r, err)
		}
	}
	if storedCount(t, db, "futures_execution_facts") != 3 {
		t.Fatal("day/environment identities collapsed")
	}
	wrong := tradeScope()
	wrong.InvestorID = "OTHER"
	if _, err := s.Observe(ctx, wrong, "new", "fill", tradeFixture(t, "fill")); err == nil {
		t.Fatal("native authorization rebound silently")
	}
}

func TestTradeStoreMalformedAuditIntegrityAndDown(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	raw := []byte{0xff, 0, '{'}
	if out, err := s.Observe(ctx, tradeScope(), "bad", "fill", raw); err != nil || !out.Blocked {
		t.Fatal(out, err)
	}
	r, err := s.Read(ctx, tradeScope())
	if err != nil || !r.Blocked || len(r.Executions) != 0 {
		t.Fatal(r, err)
	}
	var saved []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_trade_observations`).Scan(&saved); err != nil || !bytes.Equal(raw, saved) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_trade_observations SET body='broken'::bytea`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "good", "fill", tradeFixture(t, "fill")); err == nil {
		t.Fatal("corrupted audit ignored")
	}
	migrateFuturesTest(t, db, ctx, "000038_futures_command_bindings", "down")
	migrateFuturesTest(t, db, ctx, "000037_futures_command_registry", "down")
	eventDown, err := os.ReadFile("../../migrations/postgres/000036_futures_event_inbox.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(eventDown)); err != nil {
		t.Fatal(err)
	}
	applicationDown, err := os.ReadFile("../../migrations/postgres/000035_futures_query_trade_applications.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(applicationDown)); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/postgres/000034_futures_trade_facts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema()`).Scan(&n); err != nil || n != 4 {
		t.Fatal("trade migration affected query tables", n, err)
	}
}

func TestTradeStoreNoOrderReactivation(t *testing.T) {
	_, s, ctx := tradeTestDB(t)
	if _, err := s.Observe(ctx, tradeScope(), "final", "order", stateRow(t, "filled", 3, 0)); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Observe(ctx, tradeScope(), "old", "order", stateRow(t, "working", 0, 3)); err != nil || out.Blocked {
		t.Fatal(out, err)
	}
	r, err := s.Read(ctx, tradeScope())
	if err != nil || r.Orders["fixture-order-1"].Order.GatewayStatus != "filled" {
		t.Fatal(r, err)
	}
	if out, err := s.Observe(ctx, tradeScope(), "conflict", "order", stateRow(t, "cancelled", 0, 0)); err != nil || !out.Blocked {
		t.Fatal(out, err)
	}
	// Inspecting a report must not turn a conflicting lifecycle into a new status.
	r, err = s.Read(ctx, tradeScope())
	if err != nil || !r.Blocked || r.Orders["fixture-order-1"].Order.GatewayStatus != "filled" {
		t.Fatal(r, err)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal(err)
	}
}
