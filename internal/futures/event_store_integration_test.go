package futures

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func eventStream(scope TradeScope) string {
	r := scope.Route
	return fmt.Sprintf("relay:%s:v1:%s:%s:event", r.Env, r.BrokerID, r.GatewayID)
}
func eventTestDB(t *testing.T) (*sql.DB, *EventStore, *TradeStore, context.Context, string) {
	t.Helper()
	db, trades, ctx := tradeTestDB(t)
	events, err := NewEventStore(db)
	if err != nil {
		t.Fatal(err)
	}
	key, err := events.RegisterScope(ctx, tradeScope(), "FIXTURE_USER", eventStream(tradeScope()))
	if err != nil {
		t.Fatal(err)
	}
	return db, events, trades, ctx, key
}
func receiveEvent(t *testing.T, s *EventStore, ctx context.Context, key, entry string, raw []byte) EventReceipt {
	t.Helper()
	r, err := s.Receive(ctx, key, QuerySource{eventStream(tradeScope()), entry}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func processEvent(t *testing.T, s *EventStore, ctx context.Context, id int64, state string) EventReceipt {
	t.Helper()
	r, err := s.Process(ctx, id)
	if err != nil || r.State != state {
		t.Fatal(r, err)
	}
	return r
}
func registerSubmit(t *testing.T, s *EventStore, ctx context.Context, key string) {
	t.Helper()
	if replay, err := registerEventCommand(t, s, ctx, key, fixtureBody(t, "messages.json", "submit")); err != nil || replay {
		t.Fatal(replay, err)
	}
}

func registerEventCommand(t *testing.T, s *EventStore, ctx context.Context, key string, raw []byte) (bool, error) {
	t.Helper()
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return false, err
	}
	bound, err := lockEventScope(ctx, tx, key)
	_ = tx.Rollback()
	if err != nil {
		return false, err
	}
	scope := CommandScope{bound.scope.Route, bound.scope.NativeBrokerID, bound.scope.InvestorID, bound.userID}
	c, err := DecodeCommand(raw, scope, "cmd.trade")
	if err != nil {
		return false, err
	}
	e := RegistrationEvidence{CurrentTradeDate: bound.scope.TradeDate, Mappings: map[string]CatalogBinding{}}
	for _, row := range c.Orders {
		e.Mappings[row.GatewayOrderID] = CatalogBinding{"SYNTHETIC-ONLY", row.Symbol, row.Exchange, "synthetic-publication", "synthetic-checksum"}
	}
	store, _ := NewCommandStore(s.db)
	out, err := store.Register(ctx, scope, "cmd.trade", raw, e)
	if err != nil {
		return false, err
	}
	if !oneOf(out.Outcome, "registered", "replayed") {
		return false, ErrCommandIdentity
	}
	return out.Outcome == "replayed", nil
}

func TestEventStoreDurableFillFirstAndReplay(t *testing.T) {
	db, events, trades, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	if storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("intent fabricated order")
	}
	raw := fixtureBody(t, "messages.json", "fill")
	received := receiveEvent(t, events, ctx, key, "1-0", raw)
	report, err := trades.Read(ctx, tradeScope())
	if err != nil || !report.Blocked || len(report.Executions) != 0 {
		t.Fatal(report, err)
	}
	events, _ = NewEventStore(db)
	pending, err := events.Pending(ctx, key, 0, 10)
	if err != nil || len(pending) != 1 || pending[0].EventID != received.EventID {
		t.Fatal(pending, err)
	}
	processEvent(t, events, ctx, received.EventID, "applied")
	report, err = trades.Read(ctx, tradeScope())
	if err != nil || report.PendingCount != 1 || len(report.Orders) != 0 {
		t.Fatal(report, err)
	}
	order := receiveEvent(t, events, ctx, key, "2-0", fixtureBody(t, "messages.json", "order"))
	processEvent(t, events, ctx, order.EventID, "applied")
	report, err = trades.Read(ctx, tradeScope())
	if err != nil || report.Blocked || report.PendingCount != 0 || len(report.Executions) != 1 {
		t.Fatal(report, err)
	}
	again := receiveEvent(t, events, ctx, key, "1-0", raw)
	if !again.Replayed || again.EventID != received.EventID {
		t.Fatal(again)
	}
	repeated := receiveEvent(t, events, ctx, key, "3-0", raw)
	processEvent(t, events, ctx, repeated.EventID, "applied")
	if storedCount(t, db, "futures_event_inbox") != 3 || storedCount(t, db, "futures_trade_observations") != 2 {
		t.Fatal("exact message duplicated facts")
	}
	var saved []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_event_inbox WHERE event_id=$1`, received.EventID).Scan(&saved); err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("full envelope not preserved", err)
	}
	pending, err = events.Pending(ctx, key, 0, 10)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
}

func TestEventStoreWaitingIdentityThenRegistration(t *testing.T) {
	db, events, trades, ctx, key := eventTestDB(t)
	received := receiveEvent(t, events, ctx, key, "1-0", fixtureBody(t, "messages.json", "order"))
	processEvent(t, events, ctx, received.EventID, "waiting_identity")
	if storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("unknown local order treated as external")
	}
	pending, err := events.Pending(ctx, key, 0, 1)
	if err != nil || len(pending) != 1 || pending[0].State != "waiting_identity" {
		t.Fatal(pending, err)
	}
	second := receiveEvent(t, events, ctx, key, "2-0", fixtureBody(t, "messages.json", "order"))
	next, err := events.Pending(ctx, key, received.EventID, 1)
	if err != nil || len(next) != 1 || next[0].EventID != second.EventID {
		t.Fatal("waiting row starved later inbox work", next, err)
	}
	registerSubmit(t, events, ctx, key)
	events, _ = NewEventStore(db)
	processEvent(t, events, ctx, received.EventID, "applied")
	processEvent(t, events, ctx, second.EventID, "applied")
	report, err := trades.Read(ctx, tradeScope())
	if err != nil || report.Blocked || len(report.Orders) != 1 {
		t.Fatal(report, err)
	}
}

func TestEventStoreRejectsMismatchedLocalIntent(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(m map[string]any) { delete(m, "origin_message_id") },
		func(m map[string]any) { m["origin_message_id"] = "wrong" },
		func(m map[string]any) { m["request_id"] = "wrong" },
		func(m map[string]any) { m["correlation_id"] = "wrong" },
		func(m map[string]any) { m["idempotency_key"] = "wrong" },
		func(m map[string]any) { m["payload"].(map[string]any)["client_order_id"] = "wrong" },
		func(m map[string]any) { m["payload"].(map[string]any)["symbol"] = "rb2701" },
		func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = "3601" },
		func(m map[string]any) { p := m["payload"].(map[string]any); p["order_qty"] = 2; p["leaves_qty"] = 2 },
	} {
		db, events, trades, ctx, key := eventTestDB(t)
		registerSubmit(t, events, ctx, key)
		r := receiveEvent(t, events, ctx, key, "1-0", editBody(t, fixtureBody(t, "messages.json", "order"), change))
		processEvent(t, events, ctx, r.EventID, "rejected")
		report, err := trades.Read(ctx, tradeScope())
		if err != nil || !report.Blocked || len(report.Orders) != 0 {
			t.Fatal(report, err)
		}
		if storedCount(t, db, "futures_trade_observations") != 0 {
			t.Fatal("rejected event reached ledger")
		}
	}
}

func TestEventStoreConflictingSourceOrMessage(t *testing.T) {
	for _, entry := range []string{"1-0", "2-0", "different-message-same-entry"} {
		db, events, trades, ctx, key := eventTestDB(t)
		registerSubmit(t, events, ctx, key)
		raw := fixtureBody(t, "messages.json", "order")
		first := receiveEvent(t, events, ctx, key, "1-0", raw)
		processEvent(t, events, ctx, first.EventID, "applied")
		copyBeforeConflict := receiveEvent(t, events, ctx, key, "4-0", raw)
		changed := editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = "3601" })
		if entry == "different-message-same-entry" {
			entry = "1-0"
			changed = editBody(t, changed, func(m map[string]any) { m["message_id"] = "another-message" })
		}
		second := receiveEvent(t, events, ctx, key, entry, changed)
		if second.State != "conflict" {
			t.Fatal(second)
		}
		processEvent(t, events, ctx, first.EventID, "conflict")
		processEvent(t, events, ctx, second.EventID, "conflict")
		processEvent(t, events, ctx, copyBeforeConflict.EventID, "conflict")
		report, err := trades.Read(ctx, tradeScope())
		if err != nil || !report.Blocked || report.Orders["fixture-order-1"].Order.LimitPrice != "3600" {
			t.Fatal(report, err)
		}
		if storedCount(t, db, "futures_event_inbox") != 3 || storedCount(t, db, "futures_trade_observations") != 1 {
			t.Fatal("conflict erased audit or applied changed bytes")
		}
		r := receiveEvent(t, events, ctx, key, "3-0", raw)
		if r.State != "conflict" {
			t.Fatal("original bytes cleared conflict", r)
		}
	}
}

func TestEventStoreMalformedAndForeignAudit(t *testing.T) {
	db, events, trades, ctx, key := eventTestDB(t)
	raws := [][]byte{{0xff, 0, '{'}, nil, editBody(t, fixtureBody(t, "messages.json", "order"), func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" })}
	for i, raw := range raws {
		r := receiveEvent(t, events, ctx, key, fmt.Sprintf("%d-0", i+1), raw)
		processEvent(t, events, ctx, r.EventID, "rejected")
		var saved []byte
		if err := db.QueryRowContext(ctx, `SELECT body FROM futures_event_inbox WHERE event_id=$1`, r.EventID).Scan(&saved); err != nil || !bytes.Equal(saved, raw) {
			t.Fatal(err)
		}
	}
	report, err := trades.Read(ctx, tradeScope())
	if err != nil || !report.Blocked || len(report.Orders) != 0 {
		t.Fatal(report, err)
	}
	if _, err := events.Receive(ctx, key, QuerySource{"other:event", "1-0"}, []byte("{}")); err == nil {
		t.Fatal("unregistered stream accepted")
	}
	if _, err := events.Receive(ctx, key, QuerySource{eventStream(tradeScope()), "not-id"}, []byte("{}")); err == nil {
		t.Fatal("bad entry accepted")
	}
	if storedCount(t, db, "futures_event_inbox") != 3 {
		t.Fatal("unbound input persisted")
	}
}

func TestEventStoreCommandRegistryAtomicAndImmutable(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	raw := fixtureBody(t, "messages.json", "batch")
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_second_intent() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.identity='fixture-batch-child-2' THEN RAISE EXCEPTION 'synthetic child failure'; END IF; RETURN NEW; END $$;
        CREATE TRIGGER fail_second_intent BEFORE INSERT ON futures_command_reservations FOR EACH ROW EXECUTE FUNCTION fail_second_intent()`); err != nil {
		t.Fatal(err)
	}
	if _, err := registerEventCommand(t, events, ctx, key, raw); err == nil {
		t.Fatal("partial intent registration succeeded")
	}
	if storedCount(t, db, "futures_commands") != 0 || storedCount(t, db, "futures_command_reservations") != 0 {
		t.Fatal("partial batch identity persisted")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_second_intent ON futures_command_reservations`); err != nil {
		t.Fatal(err)
	}
	if replay, err := registerEventCommand(t, events, ctx, key, raw); err != nil || replay {
		t.Fatal(replay, err)
	}
	if replay, err := registerEventCommand(t, events, ctx, key, raw); err != nil || !replay {
		t.Fatal(replay, err)
	}
	for _, edit := range []func(map[string]any){
		func(m map[string]any) { m["idempotency_key"] = "new" },
		func(m map[string]any) { m["message_id"] = "new"; m["idempotency_key"] = "new" },
		func(m map[string]any) {
			p := m["payload"].(map[string]any)
			p["orders"].([]any)[0].(map[string]any)["limit_price"] = "3601"
		},
	} {
		if _, err := registerEventCommand(t, events, ctx, key, editBody(t, raw, edit)); !errors.Is(err, ErrCommandIdentity) {
			t.Fatal(err)
		}
	}
	if storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_reservations") != 4 {
		t.Fatal("registry partially written")
	}
	// Batch child binds to the parent message, never another sibling's identity.
	event := editBody(t, fixtureBody(t, "messages.json", "order"), func(m map[string]any) {
		m["origin_message_id"] = "fixture-batch-1"
		m["payload"].(map[string]any)["gateway_order_id"] = "fixture-batch-child-1"
	})
	r := receiveEvent(t, events, ctx, key, "1-0", event)
	processEvent(t, events, ctx, r.EventID, "applied")
}

func TestEventStoreProcessingRollbackRetainsInbox(t *testing.T) {
	db, events, trades, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	r := receiveEvent(t, events, ctx, key, "1-0", fixtureBody(t, "messages.json", "order"))
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_event_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='applied' THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$;
    CREATE TRIGGER fail_event_apply BEFORE UPDATE ON futures_event_inbox FOR EACH ROW EXECUTE FUNCTION fail_event_apply()`); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Process(ctx, r.EventID); err == nil {
		t.Fatal("failed transaction succeeded")
	}
	if storedCount(t, db, "futures_event_inbox") != 1 || storedCount(t, db, "futures_trade_observations") != 0 || storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("inbox lost or partial ledger persisted")
	}
	pending, err := events.Pending(ctx, key, 0, 10)
	if err != nil || len(pending) != 1 || pending[0].State != "pending" {
		t.Fatal(pending, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_event_apply ON futures_event_inbox`); err != nil {
		t.Fatal(err)
	}
	events, _ = NewEventStore(db)
	processEvent(t, events, ctx, r.EventID, "applied")
	report, err := trades.Read(ctx, tradeScope())
	if err != nil || report.Blocked || len(report.Orders) != 1 {
		t.Fatal(report, err)
	}
}

func TestEventStoreConcurrentReplay(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	raw := fixtureBody(t, "messages.json", "order")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := events.Receive(ctx, key, QuerySource{eventStream(tradeScope()), "1-0"}, raw)
			if err == nil {
				_, err = events.Process(ctx, r.EventID)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if storedCount(t, db, "futures_event_inbox") != 1 || storedCount(t, db, "futures_trade_observations") != 1 || storedCount(t, db, "futures_order_states") != 1 {
		t.Fatal("concurrent delivery duplicated application")
	}
}

func TestEventStoreExternalAndNativeScope(t *testing.T) {
	db, events, trades, ctx, key := eventTestDB(t)
	raw := editBody(t, fixtureBody(t, "messages.json", "order"), func(m map[string]any) {
		delete(m, "origin_message_id")
		m["payload"].(map[string]any)["gateway_order_id"] = nativeIdentity("order", tradeScope(), "SHFE", "00001234")
	})
	r := receiveEvent(t, events, ctx, key, "1-0", raw)
	processEvent(t, events, ctx, r.EventID, "applied")
	report, err := trades.Read(ctx, tradeScope())
	if err != nil || report.Blocked || len(report.Orders) != 1 {
		t.Fatal(report, err)
	}
	wrong := tradeScope()
	wrong.InvestorID = "OTHER"
	if _, err := events.RegisterScope(ctx, wrong, "FIXTURE_USER", eventStream(wrong)); err == nil {
		t.Fatal("native binding replaced")
	}
	if _, err := events.RegisterScope(ctx, tradeScope(), "FIXTURE_USER", "wrong:event"); err == nil {
		t.Fatal("stream binding replaced")
	}
	prod := tradeScope()
	prod.Route.Env = "prod"
	prodKey, err := events.RegisterScope(ctx, prod, "FIXTURE_USER", eventStream(prod))
	if err != nil || prodKey == key {
		t.Fatal(prodKey, err)
	}
	if storedCount(t, db, "futures_event_scopes") != 2 {
		t.Fatal("scope isolation")
	}
	prodReport, err := trades.Read(ctx, prod)
	if err != nil || len(prodReport.Orders) != 0 {
		t.Fatal(prodReport, err)
	}
}

func TestEventStoreCapacityAndIntegrity(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	raw := fixtureBody(t, "messages.json", "order")
	r := receiveEvent(t, events, ctx, key, "1-0", raw)
	if _, err := db.ExecContext(ctx, `UPDATE futures_event_inbox SET body='broken'::bytea WHERE event_id=$1`, r.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Process(ctx, r.EventID); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal(err)
	}
	if _, err := events.Receive(ctx, key, QuerySource{eventStream(tradeScope()), "1-0"}, raw); err == nil {
		t.Fatal("corrupt raw replay accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_event_scopes SET event_count=$2 WHERE scope_key=$1`, key, maxTradeAuditCount); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Receive(ctx, key, QuerySource{eventStream(tradeScope()), "2-0"}, raw); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal(err)
	}
	if storedCount(t, db, "futures_event_inbox") != 1 {
		t.Fatal("capacity ignored")
	}
	if _, err := events.Pending(ctx, key, 0, 0); err == nil {
		t.Fatal("unbounded pending list")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_commands SET body='broken'::bytea`); err != nil {
		t.Fatal(err)
	}
	if _, err := registerEventCommand(t, events, ctx, key, fixtureBody(t, "messages.json", "submit")); err == nil {
		t.Fatal(err)
	}
}
