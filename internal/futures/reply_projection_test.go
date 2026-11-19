package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

type replyFixture struct {
	ctx    context.Context
	db     *sql.DB
	store  *StreamStore
	h      StreamHandle
	p      *ReplyProjector
	c      CommandRegistration
	target TestQueryTarget
	raw    []byte
	next   int
}

func projectionReplyFixture(t *testing.T, kind string) *replyFixture {
	t.Helper()
	db, _, ctx := queryTestDB(t)
	c, target, raw := resultQueryFixture(t, kind)
	commands, _ := NewCommandStore(db)
	registered, err := commands.Register(ctx, c.Scope, "cmd.query", c.Original.Raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	c = registered
	raw = boundReply(t, raw, c)
	seedPublishedReview(t, db, ctx, c, target)
	store, _ := NewStreamStore(db)
	h, err := store.OpenReplyCapture(ctx, StreamConfig{Scope: c.Scope, Role: "reply", SourceID: target.SourceID}, HeartbeatCheckpoint{Cursor: "0-0"}, target)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewReplyProjector(store, c.Scope, target)
	if err != nil {
		t.Fatal(err)
	}
	return &replyFixture{ctx: ctx, db: db, store: store, h: h, p: p, c: c, target: target, raw: raw}
}

func (f *replyFixture) append(t *testing.T, raw []byte) int64 {
	t.Helper()
	status, err := f.store.Status(f.ctx, f.h)
	if err != nil {
		t.Fatal(err)
	}
	f.next++
	got := captureStream(t, f.store, f.ctx, f.h, status.Cursor, StreamInput{EntryID: fmt.Sprintf("%d-0", f.next), Raw: raw})
	return got.ReceiptIDs[0]
}

func (f *replyFixture) result(t *testing.T) *ReplyResult {
	t.Helper()
	tx, err := f.db.BeginTx(f.ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r, _, _, err := readReplyResult(f.ctx, tx, f.c.CommandID, f.target)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReplyProjectionQueriesPersistAndDisplay(t *testing.T) {
	for _, kind := range []string{"asset", "positions", "order", "fill"} {
		t.Run(kind, func(t *testing.T) {
			f := projectionReplyFixture(t, kind)
			id := f.append(t, f.raw)
			page, err := f.p.Step(f.ctx, f.h, 32)
			if err != nil || page.Delivered != 1 || page.Invalid != 0 {
				t.Fatal(page, err)
			}
			stored := f.result(t)
			if stored == nil || stored.State != "ready" || stored.Snapshot == nil || stored.NativeAuditVerified {
				t.Fatal(stored)
			}
			row, err := f.store.Record(f.ctx, f.h, id)
			if err != nil || row.State != "delivered" || row.ReplyCommandID.Int64 != f.c.CommandID || row.BatchID.Valid || row.EventID.Valid {
				t.Fatal(row, err)
			}
			reader := &TerminalReader{db: f.db, reviews: map[int64]QueryRequestReview{}}
			views, err := reader.queries(f.ctx, TerminalAccountConfig{Scope: f.c.Scope})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, v := range views {
				if v.Action == f.c.Original.Action {
					found = true
					if v.State != "response_ready" || !v.ResponseValidated || !v.ResponsePersisted || v.Reviewed || v.BusinessPublished || v.RowCount == nil || *v.RowCount != 1 {
						t.Fatal(v)
					}
				}
			}
			if !found {
				t.Fatal("missing query")
			}
			// These are response snapshots, not second copies of economic facts.
			if storedCount(t, f.db, "futures_trade_observations") != 0 || storedCount(t, f.db, "futures_test_query_reviews") != 0 {
				t.Fatal("invented independent audit or economic entries")
			}
		})
	}
}

func TestReplyProjectionRestartPartialAndLateConflict(t *testing.T) {
	f := projectionReplyFixture(t, "positions")
	first := editBody(t, f.raw, func(m map[string]any) {
		m["message_id"] = "partial-first"
		m["status"] = "partial"
		m["chunk"] = map[string]any{"index": 0, "is_last": false}
	})
	last := editBody(t, f.raw, func(m map[string]any) {
		m["message_id"] = "partial-last"
		m["chunk"] = map[string]any{"index": 1, "is_last": true}
		m["payload"].(map[string]any)["items"] = []any{}
	})
	f.append(t, last)
	if _, err := f.p.Step(f.ctx, f.h, 32); err != nil {
		t.Fatal(err)
	}
	if got := f.result(t); got.State != "collecting" || got.Snapshot != nil {
		t.Fatal(got)
	}
	cp, err := f.store.ReplyCheckpoint(f.ctx, f.h.config)
	if err != nil {
		t.Fatal(err)
	}
	old := f.h
	f.h, err = f.store.OpenReplyCapture(f.ctx, f.h.config, cp, f.target)
	if err != nil {
		t.Fatal(err)
	}
	f.p, _ = NewReplyProjector(f.store, f.c.Scope, f.target)
	if _, err = f.p.Step(f.ctx, old, 32); !errors.Is(err, ErrStreamOwner) {
		t.Fatal(err)
	}
	f.append(t, first)
	f.append(t, last)
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.Delivered != 2 {
		t.Fatal(page, err)
	}
	if got := f.result(t); got.State != "ready" || len(got.Snapshot.Positions) != 1 {
		t.Fatal(got)
	}
	bad := editBody(t, last, func(m map[string]any) { m["message"] = "same message changed" })
	f.append(t, bad)
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.Invalid != 1 {
		t.Fatal(page, err)
	}
	if got := f.result(t); got.State != "invalid" || got.Snapshot != nil {
		t.Fatal(got)
	}
	f.p, _ = NewReplyProjector(f.store, f.c.Scope, f.target)
	f.append(t, first)
	if _, err := f.p.Step(f.ctx, f.h, 32); err != nil {
		t.Fatal(err)
	}
	if got := f.result(t); got.State != "invalid" || got.Snapshot != nil {
		t.Fatal("conflict reset by restart", got)
	}
}

func TestReplyProjectionFailureEmptyAndForeign(t *testing.T) {
	for _, scenario := range []string{"failed", "empty", "unknown_origin", "wrong_session", "wrong_request", "publication_unknown", "source_changed", "reviewed_batch"} {
		t.Run(scenario, func(t *testing.T) {
			f := projectionReplyFixture(t, "positions")
			raw := f.raw
			switch scenario {
			case "failed":
				raw = failureBody(t, raw)
			case "empty":
				raw = editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} })
			case "unknown_origin":
				raw = editBody(t, raw, func(m map[string]any) { m["origin_message_id"] = "unknown" })
			case "wrong_session":
				raw = editBody(t, raw, func(m map[string]any) { evidence(m)["counter_session_id"] = "other" })
			case "wrong_request":
				raw = editBody(t, raw, func(m map[string]any) { m["request_id"] = "other" })
			case "publication_unknown":
				if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_test_query_publications SET state='attempted',entry_id=NULL,finished_at=NULL`); err != nil {
					t.Fatal(err)
				}
			case "source_changed":
				if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_test_reply_sources SET transport_json=jsonb_set(transport_json,'{Epoch}','"different"')`); err != nil {
					t.Fatal(err)
				}
			case "reviewed_batch":
				q, _ := NewQueryStore(f.db)
				c, err := reportedQueryContext(f.c, f.target, raw)
				if err != nil {
					t.Fatal(err)
				}
				c.Command = nil
				bindQuery(t, q, f.ctx, "review-owned", f.c, c)
			}
			id := f.append(t, raw)
			page, err := f.p.Step(f.ctx, f.h, 32)
			if scenario == "source_changed" {
				if !errors.Is(err, ErrStreamIdentity) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if oneOf(scenario, "unknown_origin", "publication_unknown", "reviewed_batch") {
				row, err := f.store.Record(f.ctx, f.h, id)
				if err != nil || row.State != "pending" || page.Delivered != 0 || len(page.Deferred) != 1 || f.result(t) != nil {
					t.Fatal(row, page, err)
				}
				return
			}
			got := f.result(t)
			want := map[string]string{"failed": "failed", "empty": "ready", "wrong_session": "invalid", "wrong_request": "invalid"}[scenario]
			if got.State != want {
				t.Fatal(got)
			}
			if scenario == "empty" && (got.Snapshot == nil || got.Snapshot.Positions == nil || len(got.Snapshot.Positions) != 0) {
				t.Fatal(got)
			}
			if scenario != "empty" && got.Snapshot != nil {
				t.Fatal(got)
			}
		})
	}
}

func TestReplyProjectionAtomicRollbackIntegrityAndDownGuard(t *testing.T) {
	f := projectionReplyFixture(t, "positions")
	id := f.append(t, f.raw)
	_, err := f.db.ExecContext(f.ctx, `CREATE FUNCTION reject_response_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic handoff failure'; END $$; CREATE TRIGGER fail_response_handoff BEFORE UPDATE ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION reject_response_handoff()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.p.Step(f.ctx, f.h, 32); err == nil {
		t.Fatal("failure hidden")
	}
	if f.result(t) != nil {
		t.Fatal("partly committed result")
	}
	row, err := f.store.Record(f.ctx, f.h, id)
	if err != nil || row.State != "pending" {
		t.Fatal(row, err)
	}
	if _, err = f.db.ExecContext(f.ctx, `DROP TRIGGER fail_response_handoff ON futures_stream_inbox`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.p.Step(f.ctx, f.h, 32); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/postgres/000050_futures_reply_results.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, string(raw)); err == nil {
		t.Fatal("erased response responsibility")
	}
	if f.result(t) == nil {
		t.Fatal("lost result")
	}
	f.append(t, f.raw)
	if _, err = f.db.ExecContext(f.ctx, `UPDATE futures_stream_inbox SET body='{}'::bytea WHERE receipt_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.p.Step(f.ctx, f.h, 32); err == nil {
		t.Fatal("overwrote corrupted previous evidence")
	}
}

func TestReplyProjectionTradeHistoryAndNoOrderMutation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			db, s, ctx := assessmentDB(t)
			v, e, target, _ := assessmentFixture(t)
			if cancel {
				v = intentVectors(t)["cancel_target_day"]
				e = registrationEvidence(t, v)
			}
			r := registerVector(t, s, ctx, v, e, "registered")
			if cancel {
				target.TradeDate = r.Original.Cancel.TradeDate
			}
			binding, _ := json.Marshal(map[string]any{"target": target})
			if _, err := db.ExecContext(ctx, `INSERT INTO futures_test_trade_publications(command_id,assessment_key,action,target_json,proof_json,state,entry_id,finished_at) VALUES($1,'reply-test',$2,$3,'{}','published','1-0',clock_timestamp())`, r.CommandID, r.Original.Action, binding); err != nil {
				t.Fatal(err)
			}
			store, _ := NewStreamStore(db)
			h, err := store.OpenReplyCapture(ctx, StreamConfig{Scope: r.Scope, Role: "reply", SourceID: target.SourceID}, HeartbeatCheckpoint{Cursor: "0-0"}, target)
			if err != nil {
				t.Fatal(err)
			}
			captureStream(t, store, ctx, h, "0-0", StreamInput{"1-0", resultTradeBody(t, r, target)})
			p, _ := NewReplyProjector(store, r.Scope, target)
			if page, err := p.Step(ctx, h, 32); err != nil || page.Delivered != 1 || page.Invalid != 0 {
				t.Fatal(page, err)
			}
			reader := &TerminalReader{db: db, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: r.Scope}}}}
			page, err := reader.Commands(ctx, r.Scope.Route.AccountID, CommandHistoryOptions{Limit: 10})
			if err != nil || len(page.Items) != 1 {
				t.Fatal(page, err)
			}
			item := page.Items[0]
			if item.Response == nil || item.Response.State != "accepted" || item.BrokerStateKnown || item.RetrySafe || item.Publication.BrokerAccepted || item.Response.OrderStateChanged {
				t.Fatal(item)
			}
			if storedCount(t, db, "futures_order_states") != 0 || storedCount(t, db, "futures_execution_facts") != 0 || storedCount(t, db, "futures_event_inbox") != 0 {
				t.Fatal("receipt became economic order state")
			}
		})
	}
}

func TestCaptureServiceReplyProjection(t *testing.T) {
	ctx, source, store, verifier, c := captureServiceFixture(t)
	r := residentProjectionCommand(t, ctx, store.db, source.scope, verifier.target)
	raw := resultTradeBody(t, r, verifier.target)
	key, _ := source.StreamConfig("reply").stream()
	residentAppend(t, ctx, source, key, "1-0", string(raw))
	c.ProjectReplies = true
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	saw := false
	err := RunTestCaptureService(run, source, store, verifier, c, func(v ResidentCaptureProgress) error {
		if v.ReplyProjection != nil && v.ReplyProjection.Delivered == 1 {
			saw = true
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || !saw || storedCount(t, store.db, "futures_reply_results") != 1 {
		t.Fatal(saw, err)
	}
	assertServiceStopped(t, ctx, store, source, verifier)
}
