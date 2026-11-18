package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

type cancelRejectionFixture struct {
	db             *sql.DB
	ctx            context.Context
	commands       *CommandStore
	events         *EventStore
	scope          TradeScope
	key            string
	commandContext CancelRejectionContext
	target         TestQueryTarget
	raw            []byte
}

func cancelRejectionDB(t *testing.T) cancelRejectionFixture {
	t.Helper()
	db, commands, ctx := assessmentDB(t)
	c := responsibilityCancelContext(t)
	scope := TradeScope{Route: c.Scope.Route, NativeBrokerID: c.Scope.NativeBrokerID, InvestorID: c.Scope.InvestorID, TradeDate: "20261105"}
	events, _ := NewEventStore(db)
	key, err := events.RegisterScope(ctx, scope, c.Scope.UserID, eventStream(scope))
	if err != nil {
		t.Fatal(err)
	}
	target := publicationTarget()
	target.CounterSessionID, target.TradeDate = c.CounterSessionID, scope.TradeDate
	return cancelRejectionFixture{db, ctx, commands, events, scope, key, c, target, responsibilityRejections(t)[1]}
}

func (f cancelRejectionFixture) register(t *testing.T) CommandRegistration {
	t.Helper()
	e := RegistrationEvidence{CancelTarget: &CancelTarget{f.commandContext.Scope, "fixture-order", f.scope.TradeDate}}
	r, err := f.commands.Register(f.ctx, f.commandContext.Scope, "cmd.trade", f.commandContext.CommandRaw, e)
	if err != nil || !oneOf(r.Outcome, "registered", "replayed") {
		t.Fatal(r, err)
	}
	return r
}

// Synthetic publication records isolate receipt processing. No publisher or
// SDK is called; real publication safety has separate integration coverage.
func (f cancelRejectionFixture) publishFixture(t *testing.T, id int64, state string) {
	t.Helper()
	binding, _ := json.Marshal(map[string]any{"target": f.target, "approval": map[string]any{"assessment_id": "synthetic-cancel-review"}})
	_, err := f.db.ExecContext(f.ctx, `INSERT INTO futures_test_trade_publications(command_id,assessment_key,action,target_json,proof_json,state,entry_id,finished_at)
VALUES($1,'synthetic-cancel-review','order.cancel',$2,'{}',$3,CASE WHEN $3='published' THEN '1-0' END,CASE WHEN $3<>'attempted' THEN clock_timestamp() END)`, id, binding, state)
	if err != nil {
		t.Fatal(err)
	}
}

func (f cancelRejectionFixture) receive(t *testing.T, entry string, raw []byte) EventReceipt {
	t.Helper()
	r, err := f.events.Receive(f.ctx, f.key, QuerySource{eventStream(f.scope), entry}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCancelRejectionDurableOutcomeAndOrderUnchanged(t *testing.T) {
	f := cancelRejectionDB(t)
	c := f.register(t)
	f.publishFixture(t, c.CommandID, "published")
	obj, _ := parseReply(responsibilityRejections(t)[0])
	order := editBody(t, obj["payload"], func(m map[string]any) {
		m["gateway_status"], m["reject_code"], m["reject_message"] = "working", "", ""
		m["native_ids"].(map[string]any)["order_sys_id"] = "0000123"
	})
	trades, _ := NewTradeStore(f.db)
	if _, err := trades.Observe(f.ctx, f.scope, "synthetic-order-before-rejection", "order", order); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT record FROM futures_order_states WHERE scope_key=$1 AND gateway_order_id='fixture-order'`, f.key).Scan(&before); err != nil {
		t.Fatal(err)
	}
	event := f.receive(t, "10-0", f.raw)
	processEvent(t, f.events, f.ctx, event.EventID, "applied")
	out, err := f.events.CancelRejection(f.ctx, f.key, c.CommandID)
	if err != nil || out.State != "rejected" || out.CancelID != "fixture-cancel" || out.NativeErrorID != 42 || out.RetrySafe || out.OrderStateChanged || !out.ReconciliationRequired {
		t.Fatal(out, err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT record FROM futures_order_states WHERE scope_key=$1 AND gateway_order_id='fixture-order'`, f.key).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("order altered by rejection", err)
	}
	copy := f.receive(t, "11-0", f.raw)
	processEvent(t, f.events, f.ctx, copy.EventID, "applied")
	if storedCount(t, f.db, "futures_cancel_rejections") != 1 || storedCount(t, f.db, "futures_trade_observations") != 1 || storedCount(t, f.db, "futures_test_trade_publications") != 1 {
		t.Fatal("rejection replay changed economic facts/publication")
	}
	restarted, _ := NewEventStore(f.db)
	if got, err := restarted.CancelRejection(f.ctx, f.key, c.CommandID); err != nil || got != out {
		t.Fatal("restart outcome changed", got, err)
	}
	if _, err := restarted.CancelRejection(f.ctx, "another-scope", c.CommandID); err == nil {
		t.Fatal("foreign scope read")
	}
}

func TestCancelRejectionWaitsForKnownPublication(t *testing.T) {
	f := cancelRejectionDB(t)
	e := f.receive(t, "1-0", f.raw)
	processEvent(t, f.events, f.ctx, e.EventID, "waiting_identity")
	c := f.register(t)
	processEvent(t, f.events, f.ctx, e.EventID, "waiting_identity")
	f.publishFixture(t, c.CommandID, "attempted")
	processEvent(t, f.events, f.ctx, e.EventID, "waiting_identity")
	if storedCount(t, f.db, "futures_cancel_rejections") != 0 {
		t.Fatal("unknown publication promoted")
	}
	var state string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state FROM futures_test_trade_publications WHERE command_id=$1`, c.CommandID).Scan(&state); err != nil || state != "attempted" {
		t.Fatal("publication repaired from event", err)
	}
	// Simulate separately verified publication recovery, not automatic repair.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_test_trade_publications SET state='published',entry_id='1-0',finished_at=clock_timestamp() WHERE command_id=$1`, c.CommandID); err != nil {
		t.Fatal(err)
	}
	processEvent(t, f.events, f.ctx, e.EventID, "applied")
}

func TestCancelRejectionMismatchAndNoPublication(t *testing.T) {
	for _, scenario := range []string{"not_published", "session", "generation", "origin_alias", "order", "day", "route", "retry_safe"} {
		t.Run(scenario, func(t *testing.T) {
			f := cancelRejectionDB(t)
			c := f.register(t)
			state := "published"
			if scenario == "not_published" {
				state = "not_published"
			}
			f.publishFixture(t, c.CommandID, state)
			raw := editBody(t, f.raw, func(m map[string]any) {
				p := m["payload"].(map[string]any)
				switch scenario {
				case "session":
					p["counter_session_id"] = "other"
				case "generation":
					m["adapter_context"].(map[string]any)["connection_generation"] = 2
				case "order":
					p["gateway_order_id"] = "another-order"
				case "day":
					p["trade_date"] = "20261106"
				case "route":
					m["routing"].(map[string]any)["env"] = "prod"
				case "retry_safe":
					p["retry_safe"] = true
				case "origin_alias":
					m["origin_message_id"] = "alias"
				}
			})
			if scenario == "origin_alias" {
				alias := editBody(t, f.commandContext.CommandRaw, func(m map[string]any) { m["message_id"] = "alias" })
				r, err := f.commands.Register(f.ctx, f.commandContext.Scope, "cmd.trade", alias, RegistrationEvidence{})
				if err != nil || r.Outcome != "replayed" {
					t.Fatal(r, err)
				}
			}
			e := f.receive(t, "1-0", raw)
			processEvent(t, f.events, f.ctx, e.EventID, "rejected")
			if storedCount(t, f.db, "futures_cancel_rejections") != 0 {
				t.Fatal("invalid rejection applied")
			}
		})
	}
}

func TestCancelRejectionConflictCannotRestoreCleanOutcome(t *testing.T) {
	for _, sameMessage := range []bool{false, true} {
		t.Run(map[bool]string{false: "new_message", true: "same_message"}[sameMessage], func(t *testing.T) {
			f := cancelRejectionDB(t)
			c := f.register(t)
			f.publishFixture(t, c.CommandID, "published")
			first := f.receive(t, "1-0", f.raw)
			processEvent(t, f.events, f.ctx, first.EventID, "applied")
			other := editBody(t, f.raw, func(m map[string]any) {
				if !sameMessage {
					m["message_id"] = "another-rejection"
				}
				m["payload"].(map[string]any)["message"] = "different broker text"
			})
			second := f.receive(t, "2-0", other)
			processEvent(t, f.events, f.ctx, second.EventID, "conflict")
			out, err := f.events.CancelRejection(f.ctx, f.key, c.CommandID)
			if err != nil || out.State != "conflict" || out.RetrySafe {
				t.Fatal(out, err)
			}
			third := f.receive(t, "3-0", f.raw)
			processEvent(t, f.events, f.ctx, third.EventID, "conflict")
			if got, err := f.events.CancelRejection(f.ctx, f.key, c.CommandID); err != nil || got.State != "conflict" {
				t.Fatal("clean replay erased conflict", err)
			}
		})
	}
}

func TestCancelRejectionTransactionAndConcurrentReplay(t *testing.T) {
	f := cancelRejectionDB(t)
	c := f.register(t)
	f.publishFixture(t, c.CommandID, "published")
	e := f.receive(t, "1-0", f.raw)
	if _, err := f.db.ExecContext(f.ctx, `CREATE FUNCTION reject_cancel_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic storage failure'; END $$; CREATE TRIGGER reject_outcome BEFORE INSERT ON futures_cancel_rejections FOR EACH ROW EXECUTE FUNCTION reject_cancel_outcome()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.events.Process(f.ctx, e.EventID); err == nil {
		t.Fatal("failure ignored")
	}
	var state string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state FROM futures_event_inbox WHERE event_id=$1`, e.EventID).Scan(&state); err != nil || state != "pending" {
		t.Fatal("partial transaction applied", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `DROP TRIGGER reject_outcome ON futures_cancel_rejections`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.events.Process(f.ctx, e.EventID)
			if err != nil || r.State != "applied" {
				t.Error(r, err)
			}
		}()
	}
	wg.Wait()
	if storedCount(t, f.db, "futures_cancel_rejections") != 1 {
		t.Fatal("concurrent duplicate outcomes")
	}
}

func TestCancelRejectionStreamDispatchAndResume(t *testing.T) {
	f := cancelRejectionDB(t)
	c := f.register(t)
	s, h := openStream(t, f.db, f.ctx, StreamConfig{Scope: f.commandContext.Scope, Role: "event", SourceID: f.target.SourceID})
	in := captureStream(t, s, f.ctx, h, "0-0", StreamInput{"1-0", f.raw})
	d, err := NewStreamDispatcher(f.ctx, s, h, nil, []EventDispatchBinding{{f.key, f.target.CounterSessionID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Dispatch(f.ctx, 0, 10)
	if err != nil || page.Delivered != 0 || len(page.Deferred) != 1 {
		t.Fatal(page, err)
	}
	f.publishFixture(t, c.CommandID, "published")
	page, err = d.Dispatch(f.ctx, 0, 10)
	if err != nil || page.Delivered != 1 {
		t.Fatal(page, err)
	}
	record, err := s.Record(f.ctx, h, in.ReceiptIDs[0])
	if err != nil || record.State != "delivered" || !bytes.Equal(record.Raw, f.raw) || !record.EventID.Valid {
		t.Fatal(record, err)
	}
	processEvent(t, f.events, f.ctx, record.EventID.Int64, "applied")
	expected := DeliveryQualityContext{Scope: f.scope, CounterSessionID: f.target.CounterSessionID, ConnectionGeneration: 1}
	if current, err := currentConsumerEvent(record.Raw, expected); err != nil || !current {
		t.Fatal("consumer rejects delivered cancel outcome", err)
	}
	expected.CounterSessionID = "previous-session"
	if current, err := currentConsumerEvent(record.Raw, expected); err != nil || current {
		t.Fatal("consumer accepts wrong session", err)
	}
	s2, h2 := openStream(t, f.db, f.ctx, h.config)
	if _, err := s.ForwardEvent(f.ctx, h, in.ReceiptIDs[0], f.key); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("stale owner accepted", err)
	}
	if got, err := s2.ForwardEvent(f.ctx, h2, in.ReceiptIDs[0], f.key); err != nil || !got.Replayed {
		t.Fatal(got, err)
	}
}

func TestCancelRejectionStreamRejectsDifferentRedisSource(t *testing.T) {
	f := cancelRejectionDB(t)
	c := f.register(t)
	f.publishFixture(t, c.CommandID, "published")
	s, h := openStream(t, f.db, f.ctx, StreamConfig{Scope: f.commandContext.Scope, Role: "event", SourceID: "another-source"})
	in := captureStream(t, s, f.ctx, h, "0-0", StreamInput{"1-0", f.raw})
	if _, err := s.ForwardEvent(f.ctx, h, in.ReceiptIDs[0], f.key); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("foreign source forwarded", err)
	}
	if storedCount(t, f.db, "futures_event_inbox") != 0 {
		t.Fatal("foreign source delivered")
	}
}
