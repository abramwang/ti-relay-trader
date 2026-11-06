package futures

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
)

func commandTestDB(t *testing.T) (*sql.DB, *CommandStore, context.Context) {
	t.Helper()
	db, _, ctx := queryTestDB(t)
	s, err := NewCommandStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, s, ctx
}

// Purely synthetic directory evidence, not a claimed SHFE publication.
func registrationEvidence(t *testing.T, v intentVector) RegistrationEvidence {
	t.Helper()
	c, err := DecodeCommand([]byte(v.Raw), v.scope(), v.Stream)
	if err != nil {
		t.Fatal(err)
	}
	e := RegistrationEvidence{}
	if len(c.Orders) > 0 {
		e.CurrentTradeDate = c.Orders[0].TradeDate
		e.Mappings = map[string]CatalogBinding{}
		for _, row := range c.Orders {
			e.Mappings[row.GatewayOrderID] = CatalogBinding{"SYNTHETIC-ONLY", row.Symbol, row.Exchange, "synthetic-publication", "synthetic-checksum"}
		}
	}
	if c.Cancel != nil {
		e.CancelTarget = &CancelTarget{v.scope(), c.Cancel.GatewayOrderID, c.Cancel.TradeDate}
	}
	return e
}

func registerVector(t *testing.T, s *CommandStore, ctx context.Context, v intentVector, e RegistrationEvidence, want string) CommandRegistration {
	t.Helper()
	out, err := s.Register(ctx, v.scope(), v.Stream, []byte(v.Raw), e)
	if err != nil || out.Outcome != want {
		t.Fatal(out.Outcome, out.Reason, err)
	}
	return out
}

func TestCommandStoreCrossDayReplayAndAliasRestart(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	vs := intentVectors(t)
	v := vs["submit"]
	first := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	s, _ = NewCommandStore(db)
	replay := registerVector(t, s, ctx, v, RegistrationEvidence{CurrentTradeDate: "20261201"}, "replayed")
	alias := vs["same_intent_new_transport"]
	again := registerVector(t, s, ctx, alias, RegistrationEvidence{}, "replayed")
	registerVector(t, s, ctx, alias, RegistrationEvidence{}, "replayed")
	if replay.CommandID != first.CommandID || again.CommandID != first.CommandID || !bytes.Equal(again.Original.Raw, []byte(v.Raw)) || again.Original.MessageID != first.Original.MessageID || again.Original.RequestID != first.Original.RequestID || len(again.Evidence.Mappings) != 1 {
		t.Fatal("lost original identity/mapping")
	}
	if storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_messages") != 2 || storedCount(t, db, "futures_command_reservations") != 2 || storedCount(t, db, "futures_command_audit") != 3 {
		t.Fatal("duplicate command or missing audit")
	}
	alias.Raw += " "
	registerVector(t, s, ctx, alias, RegistrationEvidence{}, "conflict")
	if storedCount(t, db, "futures_command_audit") != 4 {
		t.Fatal("alias conflict not retained")
	}
}

func TestCommandStoreMessageBytesAndIntentConflicts(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	vs := intentVectors(t)
	v := vs["submit"]
	registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	for _, name := range []string{"equal_price_trailing_zero", "same_lookup_changed_native_identity", "account.asset.query", "fill.list.query"} {
		out := registerVector(t, s, ctx, vs[name], RegistrationEvidence{}, "conflict")
		if out.Reason == "" {
			t.Fatal("no reason")
		}
	}
	alias := vs["same_intent_new_transport"]
	alias.Raw = string(editBody(t, []byte(alias.Raw), func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = "20261106" }))
	registerVector(t, s, ctx, alias, RegistrationEvidence{CurrentTradeDate: "20261106"}, "conflict")
	if storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_audit") != 6 {
		t.Fatal("conflicts were not isolated")
	}
}

func TestCommandStoreNewIntentDateMappingAndReservations(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	registerVector(t, s, ctx, v, RegistrationEvidence{}, "rejected")
	e := registrationEvidence(t, v)
	bad := e
	bad.CurrentTradeDate = "20261106"
	registerVector(t, s, ctx, v, bad, "rejected")
	registerVector(t, s, ctx, v, RegistrationEvidence{CurrentTradeDate: e.CurrentTradeDate}, "rejected")
	registerVector(t, s, ctx, v, e, "registered")
	w := v
	w.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) { m["message_id"] = "new"; m["idempotency_key"] = "new" }))
	registerVector(t, s, ctx, w, e, "conflict")
	w.Raw = string(editBody(t, []byte(w.Raw), func(m map[string]any) { m["payload"].(map[string]any)["gateway_order_id"] = "new-gateway" }))
	registerVector(t, s, ctx, w, registrationEvidence(t, w), "conflict")
	w.Raw = string(editBody(t, []byte(w.Raw), func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = "20261106" }))
	registerVector(t, s, ctx, w, registrationEvidence(t, w), "registered")
	if storedCount(t, db, "futures_commands") != 2 {
		t.Fatal("day-scoped reservations wrong")
	}
}

func TestCommandStoreHistoricalCancelAndQueryRegistration(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["cancel_target_day"]
	registerVector(t, s, ctx, v, RegistrationEvidence{CurrentTradeDate: "20261201"}, "rejected")
	e := registrationEvidence(t, v)
	e.CurrentTradeDate = "20261201"
	first := registerVector(t, s, ctx, v, e, "registered")
	if first.Original.Cancel.TradeDate == e.CurrentTradeDate {
		t.Fatal("cancel date replaced")
	}
	registerVector(t, s, ctx, v, RegistrationEvidence{}, "replayed")
	for i, name := range []string{"account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"} {
		q := intentVectors(t)[name]
		q.Raw = string(editBody(t, []byte(q.Raw), func(m map[string]any) {
			m["message_id"] = fmt.Sprint("query", i)
			m["idempotency_key"] = fmt.Sprint("query", i)
		}))
		registerVector(t, s, ctx, q, RegistrationEvidence{}, "registered")
		registerVector(t, s, ctx, q, RegistrationEvidence{CurrentTradeDate: "20261201"}, "replayed")
	}
	if storedCount(t, db, "futures_commands") != 5 {
		t.Fatal("queries not registered")
	}
}

func TestCommandStoreMappingImmutableOnReplay(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	e := registrationEvidence(t, v)
	registerVector(t, s, ctx, v, e, "registered")
	for key, m := range e.Mappings {
		m.SecurityID = "OTHER-SYNTHETIC"
		e.Mappings[key] = m
	}
	out := registerVector(t, s, ctx, v, e, "conflict")
	if out.Reason != "mapping_binding_conflict" {
		t.Fatal(out.Reason)
	}
	replayed := registerVector(t, s, ctx, v, RegistrationEvidence{}, "replayed")
	for _, m := range replayed.Evidence.Mappings {
		if m.SecurityID != "SYNTHETIC-ONLY" {
			t.Fatal("mapping overwritten")
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_commands SET evidence_json=replace(evidence_json::text,'SYNTHETIC-ONLY','CORRUPTED-SYNTHETIC')::jsonb`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, v.scope(), v.Stream, []byte(v.Raw), RegistrationEvidence{}); err == nil {
		t.Fatal("persisted mapping corruption accepted")
	}
}

func TestCommandStoreConcurrentAliases(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	e := registrationEvidence(t, v)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		raw := editBody(t, []byte(v.Raw), func(m map[string]any) { m["message_id"] = fmt.Sprint("concurrent", i) })
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Register(ctx, v.scope(), v.Stream, raw, e)
			if err == nil && !oneOf(out.Outcome, "registered", "replayed") {
				err = fmt.Errorf("unexpected %s", out.Outcome)
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
	if storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_messages") != 16 || storedCount(t, db, "futures_command_reservations") != 2 || storedCount(t, db, "futures_command_audit") != 16 {
		t.Fatal("concurrent duplication")
	}
}

func TestCommandStoreRouteIsolation(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	for _, change := range []func(*CommandScope){func(s *CommandScope) {}, func(s *CommandScope) { s.Route.Env = "prod" }, func(s *CommandScope) { s.Route.BrokerID = "other" }, func(s *CommandScope) { s.Route.GatewayID = "other" }, func(s *CommandScope) { s.Route.AccountID = "990000000002" }} {
		scope := v.scope()
		change(&scope)
		raw := editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = scope.Route.AccountID })
		out, err := s.Register(ctx, scope, v.Stream, raw, registrationEvidence(t, v))
		if err != nil || out.Outcome != "registered" {
			t.Fatal(out, err)
		}
	}
	if storedCount(t, db, "futures_commands") != 5 {
		t.Fatal("route namespace collision")
	}
}

func TestCommandStoreAuditFailureRollsBackEverything(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_command_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$; CREATE TRIGGER fail_audit BEFORE INSERT ON futures_command_audit FOR EACH ROW EXECUTE FUNCTION fail_command_audit()`); err != nil {
		t.Fatal(err)
	}
	v := intentVectors(t)["submit"]
	if _, err := s.Register(ctx, v.scope(), v.Stream, []byte(v.Raw), registrationEvidence(t, v)); err == nil {
		t.Fatal("write failure hidden")
	}
	for _, table := range []string{"futures_command_routes", "futures_commands", "futures_command_messages", "futures_command_reservations", "futures_command_audit"} {
		if storedCount(t, db, table) != 0 {
			t.Fatal("partial transaction", table)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_audit ON futures_command_audit`); err != nil {
		t.Fatal(err)
	}
	registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
}

func TestCommandStoreCapacityAndCorruption(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	first := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	registerVector(t, s, ctx, v, RegistrationEvidence{}, "replayed")
	if _, err := db.ExecContext(ctx, `UPDATE futures_command_routes SET audit_count=4096`); err != nil {
		t.Fatal(err)
	}
	registerVector(t, s, ctx, v, RegistrationEvidence{}, "replayed")
	w := intentVectors(t)["same_intent_new_transport"]
	if _, err := s.Register(ctx, w.scope(), w.Stream, []byte(w.Raw), RegistrationEvidence{}); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal(err)
	}
	if storedCount(t, db, "futures_command_messages") != 1 {
		t.Fatal("capacity left alias behind")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_commands SET body=body || ' '::bytea WHERE command_id=$1`, first.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(ctx, v.scope(), v.Stream, []byte(v.Raw), RegistrationEvidence{}); err == nil {
		t.Fatal("corruption silently replayed")
	}
}

func TestCommandStoreMigrationDown(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	migrateFuturesTest(t, db, ctx, "000038_futures_command_bindings", "down")
	raw, err := os.ReadFile("../../migrations/postgres/000037_futures_command_registry.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND (table_name LIKE 'futures_command%' OR table_name='futures_commands')`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestCommandStoreIntentReplayAndCrossActionConflict(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	vs := intentVectors(t)
	v := vs["submit"]
	first := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	price := vs["equal_price_trailing_zero"]
	price.Raw = string(editBody(t, []byte(price.Raw), func(m map[string]any) { m["message_id"] = "normalized-price" }))
	out := registerVector(t, s, ctx, price, RegistrationEvidence{}, "replayed")
	if out.CommandID != first.CommandID {
		t.Fatal("same decimal caused duplicate intent")
	}
	q := vs["account.asset.query"]
	q.Raw = string(editBody(t, []byte(q.Raw), func(m map[string]any) { m["message_id"] = "different-action" }))
	out = registerVector(t, s, ctx, q, RegistrationEvidence{}, "conflict")
	if out.Reason != "idempotency_intent_conflict" {
		t.Fatal(out.Reason)
	}
	if storedCount(t, db, "futures_commands") != 1 {
		t.Fatal("action changed namespace")
	}
}

func TestCommandStoreBatchAtomicReservationAndOrder(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	vs := intentVectors(t)
	v := vs["ordered_batch"]
	registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	reversed := vs["reversed_batch"]
	reversed.Raw = string(editBody(t, []byte(reversed.Raw), func(m map[string]any) { m["message_id"] = "reversed" }))
	out := registerVector(t, s, ctx, reversed, RegistrationEvidence{}, "conflict")
	if out.Reason != "idempotency_intent_conflict" {
		t.Fatal(out.Reason)
	}
	w := v
	w.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
		m["message_id"], m["idempotency_key"] = "new-batch", "new-batch"
		first := m["payload"].(map[string]any)["orders"].([]any)[0].(map[string]any)
		first["gateway_order_id"], first["client_order_id"] = "new-child", "new-child"
	}))
	registerVector(t, s, ctx, w, registrationEvidence(t, w), "conflict")
	if storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_reservations") != 4 {
		t.Fatal("partial batch registered")
	}
}

func TestCommandStoreConcurrentDifferentIntents(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	e := registrationEvidence(t, v)
	var wg sync.WaitGroup
	results := make(chan CommandRegistration, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		raw := editBody(t, []byte(v.Raw), func(m map[string]any) {
			m["message_id"] = fmt.Sprint("different", i)
			m["payload"].(map[string]any)["order_qty"] = i + 1
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Register(ctx, v.scope(), v.Stream, raw, e)
			results <- out
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]int{}
	for out := range results {
		counts[out.Outcome]++
	}
	if counts["registered"] != 1 || counts["conflict"] != 7 || storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_audit") != 8 {
		t.Fatal(counts)
	}
}
