package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func registeredQueryCommand(t *testing.T, db *sql.DB, ctx context.Context, c QueryContext) (CommandRegistration, QueryContext) {
	t.Helper()
	c.NativeInvestorID = "FIXTURE_INV"
	c.NativeUserID = "FIXTURE_USER"
	c.Command = nil
	raw, err := json.Marshal(map[string]any{
		"protocol": "relay.stream.v1", "message_type": "command", "message_id": c.OriginMessageID,
		"request_id": "request-" + c.OriginMessageID, "correlation_id": "correlation-" + c.OriginMessageID, "idempotency_key": "idempotency-" + c.OriginMessageID,
		"action": c.Action, "sent_at": "2026-11-04T13:00:00+08:00",
		"payload": map[string]any{"account_id": c.Route.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := NewCommandStore(db)
	out, err := s.Register(ctx, c.commandScope(), "cmd.query", raw, RegistrationEvidence{})
	if err != nil || !oneOf(out.Outcome, "registered", "replayed") {
		t.Fatal(out, err)
	}
	return out, c
}

func boundReply(t *testing.T, raw []byte, r CommandRegistration) []byte {
	t.Helper()
	return editBody(t, raw, func(m map[string]any) {
		m["origin_message_id"] = r.Original.MessageID
		m["request_id"] = r.Original.RequestID
		m["correlation_id"] = r.Original.CorrelationID
		m["idempotency_key"] = r.Original.IdempotencyKey
	})
}

func bindQuery(t *testing.T, s *QueryStore, ctx context.Context, id string, r CommandRegistration, c QueryContext) {
	t.Helper()
	if err := s.RegisterCommand(ctx, id, r.CommandID, c, time.Minute, DefaultBatchLimits()); err != nil {
		t.Fatal(err)
	}
}

func TestBoundQueryFourActionsAndSource(t *testing.T) {
	for _, kind := range []string{"asset", "positions", "order", "fill"} {
		t.Run(kind, func(t *testing.T) {
			db, s, ctx := queryTestDB(t)
			c := batchContext(kind)
			var raw []byte
			if kind == "order" || kind == "fill" {
				c = tradeQueryContext(kind)
				raw = tradePage(t, kind, "bound-page", 0, true, tradeFixture(t, kind))
			} else {
				raw = fixtureBody(t, "messages.json", kind)
			}
			r, c := registeredQueryCommand(t, db, ctx, c)
			bindQuery(t, s, ctx, "bound", r, c)
			raw = boundReply(t, raw, r)
			if _, err := s.Ingest(ctx, "bound", source("1-0"), raw, 0); err == nil {
				t.Fatal("foreign stream accepted")
			}
			if storedCount(t, db, "futures_query_observations") != 0 {
				t.Fatal("foreign source consumed")
			}
			out, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0)
			if err != nil || out.State != BatchReady || !out.Published {
				t.Fatal(out, err)
			}
			snap, err := s.Snapshot(ctx, "bound")
			if err != nil || snap.Candidate.Context.Command == nil || snap.Candidate.Context.Command.CommandID != r.CommandID {
				t.Fatal(snap, err)
			}
			if _, err := s.Current(ctx, c); err != nil {
				t.Fatal(err)
			}
			c.NativeUserID = "OTHER"
			if _, err := s.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
				t.Fatal("native user ignored", err)
			}
		})
	}
}

func TestBoundQueryOriginalIDsRejectAliasAndMissingFields(t *testing.T) {
	for _, field := range []string{"request_id", "correlation_id", "idempotency_key"} {
		for _, remove := range []bool{false, true} {
			t.Run(fmt.Sprint(field, remove), func(t *testing.T) {
				db, s, ctx := queryTestDB(t)
				r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
				bindQuery(t, s, ctx, "bound", r, c)
				raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
				raw = editBody(t, raw, func(m map[string]any) {
					if remove {
						delete(m, field)
					} else {
						m[field] = "alias"
					}
				})
				out, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0)
				if err != nil || out.State != BatchInvalid || out.Disposition != "rejected" {
					t.Fatal(out, err)
				}
				if storedCount(t, db, "futures_query_snapshots") != 0 {
					t.Fatal("mismatched correlation published")
				}
				var saved []byte
				if err = db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations`).Scan(&saved); err != nil || !bytes.Equal(saved, raw) {
					t.Fatal("raw mismatch", err)
				}
			})
		}
	}
}

func TestBoundQueryAliasNeverResamplesOrChangesOrigin(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, s, ctx, "bound", r, c)
	alias := editBody(t, r.Original.Raw, func(m map[string]any) { m["message_id"] = "alias"; m["request_id"] = "alias-request" })
	commands, _ := NewCommandStore(db)
	again, err := commands.Register(ctx, c.commandScope(), "cmd.query", alias, RegistrationEvidence{})
	if err != nil || again.Outcome != "replayed" || again.CommandID != r.CommandID {
		t.Fatal(again, err)
	}
	bindQuery(t, s, ctx, "bound", again, c)
	if err := s.RegisterCommand(ctx, "new-batch", again.CommandID, c, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal(err)
	}
	other := c
	other.OriginMessageID = "alias"
	if err := s.RegisterCommand(ctx, "bound", again.CommandID, other, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal("alias accepted as original", err)
	}
	raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
	foreign := editBody(t, raw, func(m map[string]any) { m["origin_message_id"] = "alias" })
	out, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "1-0"}, foreign, 0)
	if err != nil || out.Disposition != "foreign" || out.State != BatchCollecting {
		t.Fatal(out, err)
	}
	out, err = s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "2-0"}, raw, 0)
	if err != nil || out.State != BatchReady {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_query_batches") != 1 || storedCount(t, db, "futures_query_commands") != 1 {
		t.Fatal("resampled alias")
	}
}

func TestBoundQueryScopeAndTicketImmutable(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, s, ctx, "bound", r, c)
	for _, change := range []func(*QueryContext){
		func(c *QueryContext) { c.NativeUserID = "OTHER" }, func(c *QueryContext) { c.NativeInvestorID = "OTHER" }, func(c *QueryContext) { c.NativeBrokerID = "9999" },
		func(c *QueryContext) { c.Action = "account.asset.query" }, func(c *QueryContext) { c.Ticket = "another-ticket" },
		func(c *QueryContext) { c.CounterSessionID = "restarted" }, func(c *QueryContext) { c.TradeDate = "20261105" },
		func(c *QueryContext) { c.Route.Env = "prod" },
	} {
		other := copyContext(c)
		change(&other)
		if err := s.RegisterCommand(ctx, "bound", r.CommandID, other, time.Minute, DefaultBatchLimits()); err == nil {
			t.Fatal("changed binding accepted", other)
		}
	}
	if storedCount(t, db, "futures_query_batches") != 1 {
		t.Fatal("partial registration")
	}
}

func TestBoundQueryLinkFailureAtomicAndConcurrentRetry(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_query_link() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic binding failure'; END $$; CREATE TRIGGER fail_link BEFORE INSERT ON futures_query_commands FOR EACH ROW EXECUTE FUNCTION fail_query_link()`); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterCommand(ctx, "bound", r.CommandID, c, time.Minute, DefaultBatchLimits()); err == nil {
		t.Fatal("link failure hidden")
	}
	if storedCount(t, db, "futures_query_batches") != 0 || storedCount(t, db, "futures_query_heads") != 0 {
		t.Fatal("orphan batch/target")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_link ON futures_query_commands`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.RegisterCommand(ctx, "bound", r.CommandID, c, time.Minute, DefaultBatchLimits())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if storedCount(t, db, "futures_query_batches") != 1 || storedCount(t, db, "futures_query_commands") != 1 {
		t.Fatal("duplicate request")
	}
}

func TestBoundQueryRestartRetainsHistoryAndFencesOldRequest(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, s, ctx, "bound", r, c)
	raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
	if _, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0); err != nil {
		t.Fatal(err)
	}
	newStore, _ := NewQueryStore(db)
	if _, err := newStore.Recover(ctx, "bound"); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore.Snapshot(ctx, "bound"); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal(err)
	}
	if err := newStore.RegisterCommand(ctx, "another", r.CommandID, c, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal("resumed old query", err)
	}
	if _, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "2-0"}, raw, time.Second); !errors.Is(err, ErrBatchOwner) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_commands SET body=body || ' '::bytea WHERE command_id=$1`, r.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore.Snapshot(ctx, "bound"); err == nil {
		t.Fatal("corrupt command history served")
	}
}

func TestBoundTradeQueryWaitsForSubmitIdentity(t *testing.T) {
	db, events, trades, ctx, key := eventTestDB(t)
	s, _ := NewQueryStore(db)
	r, c := registeredQueryCommand(t, db, ctx, tradeQueryContext("fill"))
	bindQuery(t, s, ctx, "fills", r, c)
	raw := boundReply(t, tradePage(t, "fill", "fill-page", 0, true, tradeFixture(t, "fill")), r)
	if out, err := s.Ingest(ctx, "fills", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0); err != nil || out.State != BatchReady {
		t.Fatal(out, err)
	}
	if _, err := s.ApplyTradeQuery(ctx, "fills"); err == nil {
		t.Fatal("query bypassed missing submit identity")
	}
	if storedCount(t, db, "futures_execution_facts") != 0 || storedCount(t, db, "futures_query_trade_applications") != 0 {
		t.Fatal("partial application")
	}
	registerSubmit(t, events, ctx, key)
	if out, err := s.ApplyTradeQuery(ctx, "fills"); err != nil || !out.Applied || out.RowCount != 1 {
		t.Fatal(out, err)
	}
	report, err := trades.Read(ctx, tradeScope())
	if err != nil || report.PendingCount != 1 || len(report.Orders) != 0 {
		t.Fatal(report, err)
	}
	if storedCount(t, db, "futures_submit_commands") != 0 {
		t.Fatal("legacy registry used")
	}
}

func TestBoundTradeQueryEconomicMismatchRejectsWholeBatch(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	s, _ := NewQueryStore(db)
	r, c := registeredQueryCommand(t, db, ctx, tradeQueryContext("order"))
	bindQuery(t, s, ctx, "orders", r, c)
	row := editBody(t, tradeFixture(t, "order"), func(m map[string]any) { m["limit_price"] = "3601" })
	raw := boundReply(t, tradePage(t, "order", "order-page", 0, true, row), r)
	if out, err := s.Ingest(ctx, "orders", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0); err != nil || out.State != BatchReady {
		t.Fatal(out, err)
	}
	if _, err := s.ApplyTradeQuery(ctx, "orders"); err == nil {
		t.Fatal("query economic mismatch accepted")
	}
	if storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("invalid order written")
	}
}

func TestEventUnifiedOriginAliasAndNativeUser(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	alias := editBody(t, fixtureBody(t, "messages.json", "submit"), func(m map[string]any) { m["message_id"] = "submit-alias" })
	if replay, err := registerEventCommand(t, events, ctx, key, alias); err != nil || !replay {
		t.Fatal(replay, err)
	}
	for i, change := range []func(map[string]any){
		func(m map[string]any) { m["origin_message_id"] = "submit-alias" },
		func(m map[string]any) { m["request_id"] = "wrong-request" },
		func(m map[string]any) {
			m["payload"].(map[string]any)["native_ids"].(map[string]any)["investor_id"] = "OTHER"
		},
	} {
		raw := editBody(t, fixtureBody(t, "messages.json", "order"), func(m map[string]any) { m["message_id"] = fmt.Sprint("bad", i); change(m) })
		received := receiveEvent(t, events, ctx, key, fmt.Sprintf("%d-0", i+1), raw)
		processEvent(t, events, ctx, received.EventID, "rejected")
	}
	correct := receiveEvent(t, events, ctx, key, "10-0", fixtureBody(t, "messages.json", "order"))
	processEvent(t, events, ctx, correct.EventID, "applied")
	if storedCount(t, db, "futures_order_states") != 1 || storedCount(t, db, "futures_submit_commands") != 0 {
		t.Fatal("wrong ledger/registry")
	}
	if _, err := events.RegisterScope(ctx, tradeScope(), "OTHER", eventStream(tradeScope())); err == nil {
		t.Fatal("event user rebound")
	}
}

func TestEventUnifiedConfiguredUserDriftFailsClosed(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	registerSubmit(t, events, ctx, key)
	if _, err := db.ExecContext(ctx, `UPDATE futures_event_scopes SET native_user_id='OTHER' WHERE scope_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	r := receiveEvent(t, events, ctx, key, "1-0", fixtureBody(t, "messages.json", "order"))
	processEvent(t, events, ctx, r.EventID, "rejected")
	if storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("native identity drift accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_event_scopes SET native_user_id=NULL WHERE scope_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Receive(ctx, key, QuerySource{eventStream(tradeScope()), "2-0"}, fixtureBody(t, "messages.json", "order")); err == nil {
		t.Fatal("unbound old scope silently upgraded")
	}
}

func TestBoundQueryPartialRestartNeverResumes(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, s, ctx, "partial", r, c)
	raw := boundReply(t, page(t, "positions", "first", 0, false, "FIRST"), r)
	if out, err := s.Ingest(ctx, "partial", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0); err != nil || out.State != BatchCollecting {
		t.Fatal(out, err)
	}
	restarted, _ := NewQueryStore(db)
	if state, err := restarted.Recover(ctx, "partial"); err != nil || state != BatchInterrupted {
		t.Fatal(state, err)
	}
	if _, err := restarted.Snapshot(ctx, "partial"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal(err)
	}
	if err := restarted.RegisterCommand(ctx, "partial", r.CommandID, c, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal("old monotonic deadline reused", err)
	}
	last := boundReply(t, page(t, "positions", "last", 1, true, "LAST"), r)
	if _, err := s.Ingest(ctx, "partial", QuerySource{queryReplyStream(c.Route), "2-0"}, last, time.Second); !errors.Is(err, ErrBatchOwner) {
		t.Fatal("old writer resumed", err)
	}
	if storedCount(t, db, "futures_query_observations") != 1 || storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("partial pages combined after restart")
	}
}

func TestBoundQueryNoImplicitUpgradeOrDetachedBinding(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	registerQuery(t, s, ctx, "prototype", c)
	if err := s.RegisterCommand(ctx, "prototype", r.CommandID, c, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal("unbound upgraded", err)
	}
	c.OriginMessageID = "new-real-request"
	r, c = registeredQueryCommand(t, db, ctx, c)
	bindQuery(t, s, ctx, "bound", r, c)
	raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
	if out, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0); err != nil || out.State != BatchReady {
		t.Fatal(out, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_query_batches SET context_json=context_json-'Command' WHERE batch_id='bound'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(ctx, "bound"); err == nil {
		t.Fatal("detached snapshot published")
	}
	if _, err := s.Current(ctx, c); err == nil {
		t.Fatal("detached current snapshot published")
	}
	if _, err := s.Ingest(ctx, "bound", QuerySource{queryReplyStream(c.Route), "2-0"}, raw, time.Second); err == nil {
		t.Fatal("detached binding silently unbound")
	}
}
