package futures

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type projectionFixture struct {
	db      *sql.DB
	ctx     context.Context
	store   *StreamStore
	handle  StreamHandle
	project *LocalEventProjector
	command CommandRegistration
	target  TestQueryTarget
	raw     []byte
}

func newProjectionFixture(t *testing.T) projectionFixture {
	t.Helper()
	db, commands, ctx := assessmentDB(t)
	v, e, target, a := assessmentFixture(t)
	r := registerVector(t, commands, ctx, v, e, "registered")
	if _, err := commands.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, &fakeQueryPublisher{}); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStreamStore(db)
	h, err := store.OpenEventCapture(ctx, StreamConfig{Scope: v.scope(), Role: "event", SourceID: target.SourceID}, HeartbeatCheckpoint{Cursor: "0-0"}, target)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewLocalEventProjector(ctx, store, v.scope(), target)
	if err != nil {
		t.Fatal(err)
	}
	return projectionFixture{db, ctx, store, h, p, r, target, assessmentOrderBody(t, r, target, nil)}
}

func (f projectionFixture) capture(t *testing.T, before, entry string, raw []byte) int64 {
	t.Helper()
	r := captureStream(t, f.store, f.ctx, f.handle, before, StreamInput{entry, raw})
	return r.ReceiptIDs[0]
}

func TestLocalProjectionDeferredAndReplayed(t *testing.T) {
	f := newProjectionFixture(t)
	bad := editBody(t, f.raw, func(m map[string]any) { m["payload"].(map[string]any)["counter_session_id"] = "other-session" })
	first := f.capture(t, "0-0", "1-0", bad)
	f.capture(t, "1-0", "2-0", f.raw)
	p, err := f.project.Step(f.ctx, f.handle, 1)
	if err != nil || p.Applied != 0 || len(p.Deferred) != 1 {
		t.Fatal(p, err)
	}
	p, err = f.project.Step(f.ctx, f.handle, 1)
	if err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	if storedCount(t, f.db, "futures_order_states") != 1 {
		t.Fatal("order not projected")
	}
	if row, err := f.store.Record(f.ctx, f.handle, first); err != nil || row.State != "pending" {
		t.Fatal(row, err)
	}
	// A new process re-scans durable responsibility, not a reconstructed order.
	f.project, err = NewLocalEventProjector(f.ctx, f.store, f.command.Scope, f.target)
	if err != nil {
		t.Fatal(err)
	}
	f.capture(t, "2-0", "3-0", f.raw)
	p, err = f.project.Step(f.ctx, f.handle, 32)
	if err != nil || p.Applied != 1 || len(p.Deferred) != 1 {
		t.Fatal(p, err)
	}
	if storedCount(t, f.db, "futures_order_states") != 1 || storedCount(t, f.db, "futures_trade_observations") != 1 || storedCount(t, f.db, "futures_commands") != 1 {
		t.Fatal("duplicate or invented business fact")
	}
	// Exact same message identity with changed native facts must remain a conflict.
	changed := editBody(t, f.raw, func(m map[string]any) {
		m["payload"].(map[string]any)["native_ids"].(map[string]any)["order_sys_id"] = "00999"
	})
	f.capture(t, "3-0", "4-0", changed)
	p, err = f.project.Step(f.ctx, f.handle, 32)
	if err != nil || p.Conflicts != 1 || storedCount(t, f.db, "futures_trade_observations") != 1 {
		t.Fatal(p, err)
	}
	fresh, err := NewLocalEventProjector(f.ctx, f.store, f.command.Scope, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Step(f.ctx, f.handle, 32); !errors.Is(err, ErrLocalProjectionConflict) {
		t.Fatal("restart cleared conflict", err)
	}
}

func TestLocalProjectionRequiresPublishedIntent(t *testing.T) {
	for _, mode := range []string{"unknown", "not_published", "no_publication", "publication_target", "origin", "session", "day", "scope", "external", "intent", "binding", "owner", "raw_corruption", "report_corruption"} {
		t.Run(mode, func(t *testing.T) {
			f := newProjectionFixture(t)
			raw := f.raw
			q := ""
			switch mode {
			case "unknown":
				q = `UPDATE futures_test_trade_publications SET state='attempted',entry_id=NULL,finished_at=NULL`
			case "not_published":
				q = `UPDATE futures_test_trade_publications SET state='not_published',entry_id=NULL`
			case "no_publication":
				q = `DELETE FROM futures_test_trade_publications`
			case "publication_target":
				q = `UPDATE futures_test_trade_publications SET target_json=jsonb_set(target_json,'{target,source_id}','"other-source"')`
			case "report_corruption":
				q = `UPDATE futures_trade_scopes SET report=report||' '::bytea`
			case "binding":
				q = `UPDATE futures_test_reply_sources SET transport_json='{}'`
			case "owner":
				q = `UPDATE futures_stream_receivers SET owner_id='other-owner'`
			default:
				raw = editBody(t, raw, func(m map[string]any) {
					p := m["payload"].(map[string]any)
					switch mode {
					case "origin":
						m["origin_message_id"] = "unknown-origin"
					case "session":
						p["counter_session_id"] = "another-session"
					case "day":
						p["trade_date"] = "20260101"
					case "scope":
						m["routing"].(map[string]any)["account_id"] = "other-account"
					case "external":
						p["gateway_order_id"] = "external-ctp-v1-unknown"
						delete(m, "origin_message_id")
					case "intent":
						p["limit_price"] = "9999"
					}
				})
			}
			f.capture(t, "0-0", "1-0", raw)
			if mode == "raw_corruption" {
				q = `UPDATE futures_stream_inbox SET body=body||' '::bytea`
			}
			if q != "" {
				if _, err := f.db.ExecContext(f.ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			p, err := f.project.Step(f.ctx, f.handle, 32)
			if mode == "binding" || mode == "owner" || mode == "raw_corruption" || mode == "report_corruption" {
				if err == nil {
					t.Fatal("corrupt authority accepted", p)
				}
			} else if err != nil || len(p.Deferred) != 1 {
				t.Fatal(p, err)
			}
			if storedCount(t, f.db, "futures_order_states") != 0 || storedCount(t, f.db, "futures_event_inbox") != 0 {
				t.Fatal("unbound event projected")
			}
		})
	}
}

func TestLocalProjectionAtomicFailureAndRetry(t *testing.T) {
	f := newProjectionFixture(t)
	id := f.capture(t, "0-0", "1-0", f.raw)
	_, err := f.db.ExecContext(f.ctx, `CREATE FUNCTION fail_projection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic projection error'; END $$; CREATE TRIGGER fail_projection BEFORE INSERT ON futures_trade_observations FOR EACH ROW EXECUTE FUNCTION fail_projection()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.project.Step(f.ctx, f.handle, 32); err == nil {
		t.Fatal("failed business insert accepted")
	}
	row, err := f.store.Record(f.ctx, f.handle, id)
	if err != nil || row.State != "pending" || storedCount(t, f.db, "futures_event_inbox") != 0 {
		t.Fatal("partial handoff", row, err)
	}
	if _, err := f.db.ExecContext(f.ctx, `DROP TRIGGER fail_projection ON futures_trade_observations`); err != nil {
		t.Fatal(err)
	}
	p, err := f.project.Step(f.ctx, f.handle, 32)
	if err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
}

func projectionFill(t *testing.T, f projectionFixture) []byte {
	t.Helper()
	return editBody(t, f.raw, func(m map[string]any) {
		m["message_id"], m["event_type"] = "synthetic-fill", "fill.event"
		p := m["payload"].(map[string]any)
		for _, key := range []string{"order_qty", "cum_filled_qty", "leaves_qty", "limit_price", "gateway_status", "submitted_at", "reject_code", "reject_message"} {
			delete(p, key)
		}
		p["qty"], p["price"], p["matched_at"], p["fee"], p["fee_complete"] = 1, "2964", nil, nil, false
		ids := p["native_ids"].(map[string]any)
		ids["trade_id"] = "12345"
		p["fill_id"] = nativeFillIdentityV2(TradeScope{f.command.Scope.Route, f.command.Scope.NativeBrokerID, f.command.Scope.InvestorID, f.target.TradeDate}, "SHFE", "12345", ids["order_sys_id"].(string), "B")
	})
}

func TestLocalProjectionFillBeforeOrder(t *testing.T) {
	f := newProjectionFixture(t)
	f.capture(t, "0-0", "1-0", projectionFill(t, f))
	p, err := f.project.Step(f.ctx, f.handle, 32)
	if err != nil || p.Applied != 1 || storedCount(t, f.db, "futures_order_states") != 0 {
		t.Fatal(p, err)
	}
	f.capture(t, "1-0", "2-0", f.raw)
	p, err = f.project.Step(f.ctx, f.handle, 32)
	if err != nil || p.Applied != 1 || storedCount(t, f.db, "futures_order_states") != 1 {
		t.Fatal(p, err)
	}
}

func TestLocalProjectionEconomicConflictSurvivesRestart(t *testing.T) {
	f := newProjectionFixture(t)
	fill := projectionFill(t, f)
	f.capture(t, "0-0", "1-0", fill)
	if page, err := f.project.Step(f.ctx, f.handle, 32); err != nil || page.Applied != 1 {
		t.Fatal(page, err)
	}
	changed := editBody(t, fill, func(m map[string]any) {
		m["message_id"] = "second-message-same-fill"
		m["payload"].(map[string]any)["price"] = "2963"
	})
	f.capture(t, "1-0", "2-0", changed)
	if page, err := f.project.Step(f.ctx, f.handle, 32); err != nil || page.Conflicts != 1 {
		t.Fatal(page, err)
	}
	fresh, err := NewLocalEventProjector(f.ctx, f.store, f.command.Scope, f.target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Step(f.ctx, f.handle, 32); !errors.Is(err, ErrLocalProjectionConflict) {
		t.Fatal(err)
	}
	if storedCount(t, f.db, "futures_trade_observations") != 2 {
		t.Fatal("conflicting raw observation lost")
	}
}

func TestLocalProjectionCancelRejection(t *testing.T) {
	f := cancelRejectionDB(t)
	c := f.register(t)
	f.publishFixture(t, c.CommandID, "published")
	store, _ := NewStreamStore(f.db)
	h, err := store.OpenEventCapture(f.ctx, StreamConfig{Scope: c.Scope, Role: "event", SourceID: f.target.SourceID}, HeartbeatCheckpoint{Cursor: "0-0"}, f.target)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewLocalEventProjector(f.ctx, store, c.Scope, f.target)
	if err != nil {
		t.Fatal(err)
	}
	captureStream(t, store, f.ctx, h, "0-0", StreamInput{"1-0", f.raw})
	page, err := p.Step(f.ctx, h, 32)
	if err != nil || page.Applied != 1 {
		t.Fatal(page, err)
	}
	status, err := f.events.CancelRejection(f.ctx, f.key, c.CommandID)
	if err != nil || status.State != "rejected" || status.OrderStateChanged || storedCount(t, f.db, "futures_order_states") != 0 {
		t.Fatal(status, err)
	}
	// Retired transport owners cannot continue business application either.
	if _, err := store.Open(f.ctx, h.config); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Step(f.ctx, h, 32); !errors.Is(err, ErrStreamOwner) {
		t.Fatal(err)
	}
}
