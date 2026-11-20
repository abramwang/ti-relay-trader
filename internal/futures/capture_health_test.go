package futures

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Synthetic running receiver evidence for service tests; no Redis/broker calls.
func seedServiceCapture(t *testing.T, s *CommandStore, scope CommandScope, target TestQueryTarget) {
	t.Helper()
	key := string(commandRouteBytes(scope.Route))
	prefix := strings.TrimSuffix(queryReplyStream(scope.Route), "reply")
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO futures_capture_health(route_key,owner_id,binding_json,state,reply_checked_at,event_checked_at,reply_owner,event_owner) VALUES($1,$2,$3,'running',clock_timestamp(),clock_timestamp(),COALESCE((SELECT owner_id FROM futures_stream_receivers WHERE stream_key=$4),''),COALESCE((SELECT owner_id FROM futures_stream_receivers WHERE stream_key=$5),'')) ON CONFLICT(route_key) DO UPDATE SET owner_id=excluded.owner_id,binding_json=excluded.binding_json,state='running',reply_checked_at=excluded.reply_checked_at,event_checked_at=excluded.event_checked_at,reply_owner=excluded.reply_owner,event_owner=excluded.event_owner`, key, strings.Repeat("a", 64), captureBinding(scope, target), prefix+"reply", prefix+"event")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCaptureHealthLifecycle(t *testing.T) {
	ctx, source, store, p, _ := residentFixture(t)
	w, err := startCaptureHealth(ctx, store.db, source, p.target)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	if other, err := startCaptureHealth(ctx, store.db, source, p.target); err == nil {
		other.close()
		t.Fatal("two live owners accepted")
	}
	if v, _, err := readCaptureHealth(ctx, store.db, source.scope, p.target); err == nil || v.Ready {
		t.Fatal(v, err)
	}
	for _, role := range []string{"reply", "event"} {
		key, _ := source.StreamConfig(role).stream()
		if err := w.observe(ctx, ResidentCaptureProgress{Stream: key, Phase: "waiting_stream", CheckpointVerified: true, Checkpoint: HeartbeatCheckpoint{Cursor: "0-0"}}); err != nil {
			t.Fatal(err)
		}
		v, _, err := readCaptureHealth(ctx, store.db, source.scope, p.target)
		if role == "reply" && (err == nil || v.Ready) {
			t.Fatal("one lane granted readiness", v)
		}
		if role == "event" && (err != nil || !v.Ready || time.Until(v.ValidUntil) > captureHealthTTL) {
			t.Fatal(v, err)
		}
	}
	if err := w.stop(); err != nil {
		t.Fatal(err)
	}
	if v, _, err := readCaptureHealth(ctx, store.db, source.scope, p.target); err == nil || v.State != "stopped" {
		t.Fatal(v, err)
	}
	key, _ := source.StreamConfig("reply").stream()
	if err := w.observe(ctx, ResidentCaptureProgress{Stream: key, Phase: "idle", CheckpointVerified: true}); err == nil {
		t.Fatal("stopped receiver reopened")
	}
}

func TestCaptureHealthCancellationKeepsLeaseUntilStop(t *testing.T) {
	ctx, source, store, p, _ := residentFixture(t)
	w, err := startCaptureHealth(ctx, store.db, source, p.target)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM futures_capture_health FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	key, _ := source.StreamConfig("reply").stream()
	go func() {
		done <- w.observe(run, ResidentCaptureProgress{Stream: key, Phase: "waiting_stream", CheckpointVerified: true})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		var waiting bool
		if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'UPDATE futures_capture_health SET reply_checked_at=%' AND wait_event_type='Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("health write did not wait on lock")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("shutdown destroyed lease connection", err)
	}
	if other, err := startCaptureHealth(ctx, store.db, source, p.target); err == nil {
		other.close()
		t.Fatal("lease released before stop")
	}
	if err := w.stop(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM futures_capture_health`).Scan(&state); err != nil || state != "stopped" {
		t.Fatal(state, err)
	}
}

func TestCaptureHealthBackpressureLanesAndMigration(t *testing.T) {
	ctx, source, store, p, _ := residentFixture(t)
	w, err := startCaptureHealth(ctx, store.db, source, p.target)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	observe := func(role, phase string) {
		t.Helper()
		key, _ := source.StreamConfig(role).stream()
		if err := w.observe(ctx, ResidentCaptureProgress{Stream: key, Phase: phase, CheckpointVerified: true}); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(state string, ready bool) {
		t.Helper()
		v, _, err := readCaptureHealth(ctx, store.db, source.scope, p.target)
		if v.State != state || v.Ready != ready || (err == nil) != ready {
			t.Fatal(v, err)
		}
	}
	observe("reply", "waiting_stream")
	observe("event", "waiting_stream")
	assert("running", true)
	for _, role := range []string{"reply", "event"} {
		other := "event"
		if role == "event" {
			other = "reply"
		}
		observe(role, "backpressured")
		observe(other, "idle")
		assert("backpressured", false)
		observe(other, "backpressured")
		observe(role, "captured")
		assert("backpressured", false)
		observe(other, "captured")
		assert("running", true)
	}
	// Downgrade must not erase an active backpressure diagnosis or reopen it.
	observe("event", "backpressured")
	raw, err := os.ReadFile("../../migrations/postgres/000060_futures_capture_backpressure.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, string(raw)); err == nil {
		t.Fatal("downgraded an active worker")
	}
	assert("backpressured", false)
	if err := w.stop(); err != nil {
		t.Fatal(err)
	}
	migrateFuturesTest(t, store.db, ctx, "000060_futures_capture_backpressure", "down")
	migrateFuturesTest(t, store.db, ctx, "000060_futures_capture_backpressure", "up")
	assert("stopped", false)
	key, _ := source.StreamConfig("reply").stream()
	if err := w.observe(ctx, ResidentCaptureProgress{Stream: key, Phase: "backpressured", CheckpointVerified: true}); !errors.Is(err, ErrCaptureUnavailable) {
		t.Fatal("late pressure observation revived stopped worker", err)
	}
}

func TestCaptureGuardRejectsLostEvidence(t *testing.T) {
	for _, scenario := range []string{"missing", "missing_table", "starting", "backpressured", "missing_lane", "stopped", "stale_reply", "stale_event", "future", "wrong_binding", "changed_owner", "receiver_takeover", "healthy"} {
		t.Run(scenario, func(t *testing.T) {
			db, s, ctx := assessmentDB(t)
			v, _, target, _, _ := executionTradeFixture(t, time.Now())
			seedServiceCapture(t, s, v.scope(), target)
			inner := &fakeQueryPublisher{}
			g := &captureGuardTransport{inner: inner, db: db, scope: v.scope(), target: target}
			proof, err := g.inspect(ctx)
			if err != nil || proof.Capture == nil || time.Until(proof.ExpiresAt) > captureHealthTTL {
				t.Fatal(proof, err)
			}
			q := ""
			switch scenario {
			case "missing":
				q = `DELETE FROM futures_capture_health`
			case "missing_table":
				q = `DROP TABLE futures_capture_health`
			case "starting":
				q = `UPDATE futures_capture_health SET state='starting'`
			case "backpressured":
				q = `UPDATE futures_capture_health SET state='backpressured',reply_backpressured=true,reply_checked_at=NULL`
			case "missing_lane":
				q = `UPDATE futures_capture_health SET event_checked_at=NULL`
			case "stopped":
				q = `UPDATE futures_capture_health SET state='stopped'`
			case "stale_reply":
				q = `UPDATE futures_capture_health SET reply_checked_at=clock_timestamp()-interval '16 seconds'`
			case "stale_event":
				q = `UPDATE futures_capture_health SET event_checked_at=clock_timestamp()-interval '16 seconds'`
			case "future":
				q = `UPDATE futures_capture_health SET reply_checked_at=clock_timestamp()+interval '1 minute'`
			case "wrong_binding":
				q = `UPDATE futures_capture_health SET binding_json='{}'`
			case "changed_owner":
				q = `UPDATE futures_capture_health SET owner_id='new-owner'`
			case "receiver_takeover":
				q = `UPDATE futures_capture_health SET event_owner='different'`
			}
			if q != "" {
				if _, err := db.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			_, rejected, err := g.appendQuery(ctx, proof, []byte("synthetic"))
			if err != nil || rejected != (scenario != "healthy") || inner.appends.Load() != map[bool]int32{true: 1, false: 0}[scenario == "healthy"] {
				t.Fatal(rejected, err, inner.appends.Load())
			}
		})
	}
}

type captureStopOnInspect struct {
	inner *fakeQueryPublisher
	stop  func()
}

func (f *captureStopOnInspect) inspect(ctx context.Context) (queryPublishProof, error) {
	p, e := f.inner.inspect(ctx)
	f.stop()
	return p, e
}
func (f *captureStopOnInspect) appendQuery(ctx context.Context, p queryPublishProof, b []byte) (string, bool, error) {
	return f.inner.appendQuery(ctx, p, b)
}

func TestOrderServiceCaptureGateAndReplay(t *testing.T) {
	for _, phase := range []string{"before_registration", "before_append", "after_publication"} {
		t.Run(phase, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			inner := &fakeQueryPublisher{}
			s, market, _ := serviceFixture(t, store, inner)
			stop := func() {
				if _, err := db.ExecContext(ctx, `UPDATE futures_capture_health SET state='stopped'`); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "before_registration" {
				stop()
			}
			if phase == "before_append" {
				s.transport = func(TestSourceConfig, TestQueryTarget, TestTradeApproval) (queryPublicationTransport, func(), error) {
					return &captureStopOnInspect{inner, stop}, func() {}, nil
				}
			}
			r := orderServiceRequest()
			result, err := s.Submit(ctx, r)
			if phase == "before_registration" {
				if !errors.Is(err, ErrCaptureUnavailable) || storedCount(t, db, "futures_commands") != 0 || market.calls.Load() != 0 || inner.appends.Load() != 0 {
					t.Fatal(result, err)
				}
				return
			}
			want := "published"
			if phase == "before_append" {
				// The transaction-level check rejects before an attempt exists.
				// Keep the registered intention, not a fabricated transport result.
				want = "registered"
				if !errors.Is(err, ErrCaptureUnavailable) || storedCount(t, db, "futures_test_trade_publications") != 0 {
					t.Fatal("capture stop left a publication attempt", result, err)
				}
			}
			if result.Publication.State != want || (err != nil) != (phase == "before_append") {
				t.Fatal(result, err)
			}
			stop()
			before := inner.appends.Load()
			again, err := s.Submit(ctx, r)
			if err != nil || !again.Publication.Replayed || again.Publication.State != want || inner.appends.Load() != before {
				t.Fatal(again, err)
			}
			status, err := s.Lookup(ctx, r.Order.AccountID, r.IdempotencyKey)
			if err != nil || status.MessageID != result.MessageID {
				t.Fatal(status, err)
			}
			if phase == "before_append" && before != 0 {
				t.Fatal("called transport after stop")
			}
		})
	}
}

func TestCancelServiceCaptureGateKeepsLookup(t *testing.T) {
	db, s, f, _, ctx, r, _ := cancelServiceFixture(t)
	if _, err := db.ExecContext(ctx, `UPDATE futures_capture_health SET state='stopped'`); err != nil {
		t.Fatal(err)
	}
	before := f.appends.Load()
	if _, err := s.Cancel(ctx, r); !errors.Is(err, ErrCaptureUnavailable) {
		t.Fatal(err)
	}
	if f.appends.Load() != before || storedCount(t, db, "futures_commands") != 1 {
		t.Fatal("cancel registered while capture stopped")
	}
	parent, err := s.Lookup(ctx, r.AccountID, orderServiceRequest().IdempotencyKey)
	if err != nil || parent.Publication.State != "published" {
		t.Fatal(parent, err)
	}
}
