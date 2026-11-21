package futures

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func eventDispatcher(t *testing.T, s *StreamStore, h StreamHandle, ctx context.Context, key string) *StreamDispatcher {
	t.Helper()
	d, err := NewStreamDispatcher(ctx, s, h, nil, []EventDispatchBinding{{key, "ctpproc-fixture-1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStreamDispatchBoundedScanDoesNotLearnOrStarve(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("event"))
	unknown := editBody(t, fixtureBody(t, "messages.json", "order"), func(m map[string]any) {
		m["payload"].(map[string]any)["counter_session_id"] = "unregistered-session"
	})
	in := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", unknown}, StreamInput{"2-0", fixtureBody(t, "messages.json", "fill")})
	d := eventDispatcher(t, s, h, ctx, key)
	first, err := d.Dispatch(ctx, 0, 1)
	if err != nil || first.Scanned != 1 || first.Delivered != 0 || len(first.Deferred) != 1 || first.NextAfterID != in.ReceiptIDs[0] || first.CycleComplete {
		t.Fatal(first, err)
	}
	second, err := d.Dispatch(ctx, first.NextAfterID, 1)
	if err != nil || second.Delivered != 1 || len(second.Deferred) != 0 {
		t.Fatal(second, err)
	}
	end, err := d.Dispatch(ctx, second.NextAfterID, 1)
	if err != nil || !end.CycleComplete || end.NextAfterID != 0 {
		t.Fatal(end, err)
	}
	if storedCount(t, db, "futures_commands") != 0 || storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("dispatcher invented local intent/order")
	}
	if r, err := s.Record(ctx, h, in.ReceiptIDs[0]); err != nil || r.State != "pending" || !bytes.Equal(r.Raw, unknown) {
		t.Fatal(r, err)
	}
	work, err := events.Pending(ctx, key, 0, 10)
	if err != nil || len(work) != 1 || work[0].State != "pending" {
		t.Fatal(work, err)
	}
	processEvent(t, events, ctx, work[0].EventID, "waiting_identity")
	registerSubmit(t, events, ctx, key)
	processEvent(t, events, ctx, work[0].EventID, "applied")
	if page, err := d.Dispatch(ctx, 0, 10); err != nil || page.Delivered != 0 || len(page.Deferred) != 1 {
		t.Fatal(page, err)
	}
}

func TestStreamDispatchBindingsAreExplicitAndImmutable(t *testing.T) {
	db, _, _, ctx, key := eventTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("event"))
	bindings := []EventDispatchBinding{{key, "ctpproc-fixture-1"}}
	d, err := NewStreamDispatcher(ctx, s, h, nil, bindings, nil)
	if err != nil {
		t.Fatal(err)
	}
	bindings[0].CounterSessionID = "changed"
	captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", fixtureBody(t, "messages.json", "order")})
	if page, err := d.Dispatch(ctx, 0, 10); err != nil || page.Delivered != 1 {
		t.Fatal(page, err)
	}
	for _, bad := range [][]EventDispatchBinding{nil, {{key, ""}}, {{key, "a"}, {key, "a"}}, {{"missing", "a"}}} {
		if _, err := NewStreamDispatcher(ctx, s, h, nil, bad, nil); err == nil {
			t.Fatal("bad binding accepted")
		}
	}
	foreign := streamTestConfig("event")
	foreign.Scope.Route.Env = "prod"
	_, other := openStream(t, db, ctx, foreign)
	if _, err := NewStreamDispatcher(ctx, s, other, nil, []EventDispatchBinding{{key, "a"}}, nil); err == nil {
		t.Fatal("cross environment scope accepted")
	}
	if _, err := s.Open(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Dispatch(ctx, 0, 10); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("retired reader accepted", err)
	}
}

func TestStreamDispatchFailureRetainsRetryCursor(t *testing.T) {
	db, _, _, ctx, key := eventTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("event"))
	in := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", []byte("bad JSON")}, StreamInput{"2-0", fixtureBody(t, "messages.json", "order")})
	d := eventDispatcher(t, s, h, ctx, key)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic handoff failure'; END $$; CREATE TRIGGER fail_dispatch BEFORE UPDATE ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION fail_dispatch()`); err != nil {
		t.Fatal(err)
	}
	page, err := d.Dispatch(ctx, 0, 10)
	if err == nil || page.Scanned != 1 || page.NextAfterID != in.ReceiptIDs[0] || page.CycleComplete || storedCount(t, db, "futures_event_inbox") != 0 {
		t.Fatal(page, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_dispatch ON futures_stream_inbox`); err != nil {
		t.Fatal(err)
	}
	if retry, err := d.Dispatch(ctx, page.NextAfterID, 10); err != nil || retry.Delivered != 1 || !retry.CycleComplete {
		t.Fatal(retry, err)
	}
	for _, limit := range []int{0, 257} {
		if _, err := d.Dispatch(ctx, 0, limit); err == nil {
			t.Fatal("unbounded scan")
		}
	}
}

func TestStreamDispatchQueriesDeadlineAndRetirement(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "active"
		if expired {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
			bindQuery(t, q, ctx, "query", r, c)
			s, h := openStream(t, db, ctx, StreamConfig{c.commandScope(), "reply", "fixture"})
			unknown := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
			unknown = editBody(t, unknown, func(m map[string]any) { m["origin_message_id"] = "unregistered-query" })
			captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", unknown}, StreamInput{"2-0", boundReply(t, fixtureBody(t, "messages.json", "positions"), r)})
			start := time.Now()
			if expired {
				start = start.Add(-2 * time.Minute)
			}
			for _, badStart := range []time.Time{{}, time.Now().Add(time.Hour)} {
				if _, err := NewStreamDispatcher(ctx, s, h, q, nil, []QueryDispatchBinding{{"query", badStart}}); err == nil {
					t.Fatal("invalid request clock accepted")
				}
			}
			d, err := NewStreamDispatcher(ctx, s, h, q, nil, []QueryDispatchBinding{{"query", start}})
			if err != nil {
				t.Fatal(err)
			}
			page, err := d.Dispatch(ctx, 0, 10)
			if err != nil || page.Delivered != 1 || len(page.Deferred) != 1 || storedCount(t, db, "futures_commands") != 1 {
				t.Fatal(page, err)
			}
			var state string
			if err := db.QueryRowContext(ctx, `SELECT state FROM futures_query_batches WHERE batch_id='query'`).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if (state == string(BatchExpired)) != expired || (storedCount(t, db, "futures_query_snapshots") == 0) != expired {
				t.Fatal("elapsed time was reset", state)
			}
			next, _ := NewQueryStore(db)
			if _, err := next.Recover(ctx, "query"); err != nil {
				t.Fatal(err)
			}
			if _, err := NewStreamDispatcher(ctx, s, h, q, nil, []QueryDispatchBinding{{"query", start}}); !errors.Is(err, ErrBatchOwner) {
				t.Fatal("retired query rebound", err)
			}
		})
	}
}

func readyDelivery(t *testing.T) (*sql.DB, *StreamStore, StreamHandle, StreamHandle, *QueryStore, DeliveryQualityContext, context.Context) {
	t.Helper()
	db, q, ctx := queryTestDB(t)
	s, event := openStream(t, db, ctx, streamTestConfig("event"))
	_, reply := openStream(t, db, ctx, streamTestConfig("reply"))
	for _, kind := range []string{"asset", "positions", "order", "fill"} {
		c := batchContext(kind)
		raw := fixtureBody(t, "messages.json", kind)
		if kind == "order" || kind == "fill" {
			c = tradeQueryContext(kind)
			raw = tradePage(t, kind, "empty-"+kind, 0, true)
		}
		r, c := registeredQueryCommand(t, db, ctx, c)
		bindQuery(t, q, ctx, kind, r, c)
		if out, err := q.Ingest(ctx, kind, QuerySource{reply.stream, "1-0"}, boundReply(t, raw, r), 0); err != nil || !out.Published {
			t.Fatal(out, err)
		}
		if isTradeQuery(c.Action) {
			if _, err := q.ApplyTradeQuery(ctx, kind); err != nil {
				t.Fatal(err)
			}
		}
	}
	expected := DeliveryQualityContext{tradeScope(), "ctpproc-fixture-1", 1}
	if quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected); err != nil || quality.Blocked || len(quality.Snapshots) != 4 {
		t.Fatal(quality, err)
	}
	return db, s, event, reply, q, expected, ctx
}

func TestStreamQualityIncludesPendingAndDownstreamExecution(t *testing.T) {
	db, s, event, reply, q, expected, ctx := readyDelivery(t)
	events, _ := NewEventStore(db)
	key, err := events.RegisterScope(ctx, tradeScope(), "FIXTURE_USER", event.stream)
	if err != nil {
		t.Fatal(err)
	}
	registerSubmit(t, events, ctx, key)
	in := captureStream(t, s, ctx, event, "0-0", StreamInput{"1-0", fixtureBody(t, "messages.json", "fill")})
	if quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected); err != nil || !quality.Blocked || quality.TransportPending != 1 {
		t.Fatal(quality, err)
	}
	forward, err := s.ForwardEvent(ctx, event, in.ReceiptIDs[0], key)
	if err != nil {
		t.Fatal(err)
	}
	if quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected); err != nil || !quality.Blocked || quality.EventsPending != 1 || quality.TransportPending != 0 {
		t.Fatal("delivered was mistaken for applied", quality, err)
	}
	processEvent(t, events, ctx, forward.EventID, "applied")
	if quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected); err != nil || !quality.Blocked || quality.LedgerPending != 1 {
		t.Fatal("pending fill was ignored", quality, err)
	}
	in = captureStream(t, s, ctx, event, "1-0", StreamInput{"2-0", fixtureBody(t, "messages.json", "order")})
	forward, err = s.ForwardEvent(ctx, event, in.ReceiptIDs[0], key)
	if err != nil {
		t.Fatal(err)
	}
	processEvent(t, events, ctx, forward.EventID, "applied")
	if quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected); err != nil || quality.Blocked {
		t.Fatal(quality, err)
	}
	in = captureStream(t, s, ctx, event, "2-0", StreamInput{"3-0", []byte("broken")})
	if err := s.Quarantine(ctx, event, in.ReceiptIDs[0], "retain evidence"); err != nil {
		t.Fatal(err)
	}
	if quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected); err != nil || !quality.Blocked || quality.TransportQuarantined != 1 {
		t.Fatal("quarantine disappeared from quality", quality, err)
	}
}

func TestStreamQualityRejectsStaleIncompleteAndCorruptEvidence(t *testing.T) {
	for _, mutation := range []string{"session", "generation", "day", "retired", "latest_collecting", "unapplied", "snapshot_hash", "ledger_hash", "receiver", "source"} {
		t.Run(mutation, func(t *testing.T) {
			db, s, event, reply, q, expected, ctx := readyDelivery(t)
			var err error
			switch mutation {
			case "session":
				expected.CounterSessionID = "other-session"
			case "generation":
				expected.ConnectionGeneration++
			case "day":
				expected.Scope.TradeDate = "20261105"
			case "retired":
				next, _ := NewQueryStore(db)
				_, err = next.Recover(ctx, "asset")
			case "latest_collecting":
				c := batchContext("asset")
				c.OriginMessageID = "new-asset-query"
				r, c := registeredQueryCommand(t, db, ctx, c)
				bindQuery(t, q, ctx, "new-asset", r, c)
			case "unapplied":
				_, err = db.ExecContext(ctx, `DELETE FROM futures_query_trade_applications WHERE batch_id='order'`)
			case "snapshot_hash":
				_, err = db.ExecContext(ctx, `UPDATE futures_query_snapshots SET candidate='corrupt'::bytea WHERE batch_id='asset'`)
			case "ledger_hash":
				_, err = db.ExecContext(ctx, `UPDATE futures_trade_scopes SET summary='corrupt'::bytea`)
			case "receiver":
				_, err = s.Open(ctx, event.config)
			case "source":
				reply.config.SourceID = "different-redis"
			}
			if err != nil {
				t.Fatal(err)
			}
			quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected)
			if !quality.Blocked {
				t.Fatal("unsafe quality cleared", mutation, quality)
			}
			wantError := mutation == "snapshot_hash" || mutation == "ledger_hash" || mutation == "receiver" || mutation == "source"
			if (err != nil) != wantError {
				t.Fatal("unexpected inspection error", mutation, err)
			}
		})
	}
}

func TestStreamQualityKeepsRejectedEventsAndOldQueryInvalidation(t *testing.T) {
	for _, kind := range []string{"event", "query"} {
		t.Run(kind, func(t *testing.T) {
			db, s, event, reply, q, expected, ctx := readyDelivery(t)
			if kind == "event" {
				events, _ := NewEventStore(db)
				key, err := events.RegisterScope(ctx, tradeScope(), "FIXTURE_USER", event.stream)
				if err != nil {
					t.Fatal(err)
				}
				r, err := events.Receive(ctx, key, QuerySource{event.stream, "1-0"}, []byte("bad JSON retained"))
				if err != nil {
					t.Fatal(err)
				}
				processEvent(t, events, ctx, r.EventID, "rejected")
			} else {
				c := tradeQueryContext("order")
				r, c := registeredQueryCommand(t, db, ctx, c)
				out, err := q.Ingest(ctx, "order", QuerySource{reply.stream, "2-0"}, boundReply(t, tradePage(t, "order", "contradictory-page", 0, true), r), time.Second)
				if err != nil || out.State != BatchInvalid {
					t.Fatal(out, err)
				}
				c.OriginMessageID = "fresh-order-query"
				r, c = registeredQueryCommand(t, db, ctx, c)
				bindQuery(t, q, ctx, "fresh-order", r, c)
				if out, err := q.Ingest(ctx, "fresh-order", QuerySource{reply.stream, "3-0"}, boundReply(t, tradePage(t, "order", "fresh-page", 0, true), r), 0); err != nil || !out.Published {
					t.Fatal(out, err)
				}
				if _, err := q.ApplyTradeQuery(ctx, "fresh-order"); err != nil {
					t.Fatal(err)
				}
			}
			quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected)
			if err != nil || !quality.Blocked || (kind == "event" && quality.EventsRejected != 1) || (kind == "query" && quality.InvalidAppliedQueries != 1) {
				t.Fatal(quality, err)
			}
			for _, snapshot := range quality.Snapshots {
				if !snapshot.Usable {
					t.Fatal("expected new snapshots usable without clearing old errors", snapshot)
				}
			}
		})
	}
}

func TestStreamQualityNeverTreatsEmptyStorageAsReady(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	s, event := openStream(t, db, ctx, streamTestConfig("event"))
	_, reply := openStream(t, db, ctx, streamTestConfig("reply"))
	expected := DeliveryQualityContext{tradeScope(), "ctpproc-fixture-1", 1}
	quality, err := s.InspectDeliveryQuality(ctx, event, reply, q, expected)
	if err != nil || !quality.Blocked || !quality.LedgerMissing || len(quality.Snapshots) != 4 {
		t.Fatal(quality, err)
	}
	for _, item := range quality.Snapshots {
		if item.Usable || item.Reason != "snapshot_missing" {
			t.Fatal(item)
		}
	}
}
