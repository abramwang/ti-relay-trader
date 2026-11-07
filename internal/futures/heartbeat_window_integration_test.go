package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func heartbeatWindowFixture(t *testing.T) (*TestHeartbeatWindow, *sql.DB, context.Context) {
	t.Helper()
	db, _, ctx := queryTestDB(t)
	s, err := NewTestStreamSource(sourceFixture())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	store, h := openStream(t, db, ctx, s.StreamConfig("hb"))
	expected := DeliveryQualityContext{tradeScope(), "ctpproc-fixture-1", 1}
	expected.Scope.Route = s.scope.Route
	w, err := NewTestHeartbeatWindow(ctx, store, s, h, expected, DefaultTestHeartbeatPolicy(), HeartbeatBoundary{"0-0", 0, "synthetic-review"})
	if err != nil {
		t.Fatal(err)
	}
	return w, db, ctx
}

func observeWindow(w *TestHeartbeatWindow, ctx context.Context, limit int) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return false, w.failure
	}
	caughtUp, err := w.observe(ctx, limit)
	if err != nil {
		return false, w.fail(err)
	}
	return caughtUp, nil
}

func windowBody(t *testing.T, w *TestHeartbeatWindow, id string, offset time.Duration) []byte {
	t.Helper()
	return guardBody(t, w.guard, id, w.startedAt.Add(time.Millisecond+offset))
}

func TestHeartbeatWindowOrderedDecisionNeverSkipsDisconnect(t *testing.T) {
	w, db, ctx := heartbeatWindowFixture(t)
	good := windowBody(t, w, "good", 0)
	down := editBody(t, windowBody(t, w, "down", time.Millisecond), func(m map[string]any) { m["payload"].(map[string]any)["broker_ready"] = false })
	r := captureStream(t, w.store, ctx, w.handle, "0-0", StreamInput{"1-0", good}, StreamInput{"2-0", down}, StreamInput{"3-0", windowBody(t, w, "later", 2*time.Millisecond)})
	if _, err := observeWindow(w, ctx, 10); !errors.Is(err, ErrTestSessionStopped) {
		t.Fatal(err)
	}
	var state, reason string
	var after int64
	if err := db.QueryRowContext(ctx, `SELECT state,reason,after_receipt FROM futures_heartbeat_windows WHERE window_id=$1`, w.id).Scan(&state, &reason, &after); err != nil || state != "stopped" || !strings.Contains(reason, "disconnected") || after != r.ReceiptIDs[1] {
		t.Fatal(state, reason, after, err)
	}
	if storedCount(t, db, "futures_heartbeat_observations") != 2 || storedCount(t, db, "futures_stream_inbox") != 3 {
		t.Fatal("disconnect skipped or later raw evidence lost")
	}
	if _, err := observeWindow(w, ctx, 10); !errors.Is(err, ErrTestSessionStopped) {
		t.Fatal("window resumed", err)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal("close not idempotent", err)
	}
}

func TestHeartbeatWindowCommitFailureCannotRenew(t *testing.T) {
	w, db, ctx := heartbeatWindowFixture(t)
	captureStream(t, w.store, ctx, w.handle, "0-0", StreamInput{"1-0", windowBody(t, w, "ready", 0)})
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_window() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic observer commit failure'; END $$; CREATE CONSTRAINT TRIGGER fail_window AFTER UPDATE ON futures_heartbeat_windows DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_window()`); err != nil {
		t.Fatal(err)
	}
	if _, err := observeWindow(w, ctx, 10); err == nil {
		t.Fatal("commit failure hidden")
	}
	if storedCount(t, db, "futures_heartbeat_observations") != 0 || !w.guard.expiresAt.IsZero() || !errors.Is(w.guard.Check(), ErrTestSessionStopped) || w.after != 0 {
		t.Fatal("failed transaction installed freshness or partial observation")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_window ON futures_heartbeat_windows`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatWindowRestartRequiresReviewedBoundaryAndNewEvidence(t *testing.T) {
	w, db, ctx := heartbeatWindowFixture(t)
	r := captureStream(t, w.store, ctx, w.handle, "0-0", StreamInput{"1-0", windowBody(t, w, "ready", 0)})
	if done, err := observeWindow(w, ctx, 10); err != nil || !done || w.guard.Check() != nil {
		t.Fatal(done, err)
	}
	newHandle, err := w.store.Open(ctx, w.handle.config)
	if err != nil {
		t.Fatal(err)
	}
	boundary := HeartbeatBoundary{"1-0", r.ReceiptIDs[0], "independent-reverified-fixture"}
	newWindow, err := NewTestHeartbeatWindow(ctx, w.store, w.source, newHandle, w.guard.expected, w.guard.policy, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(newWindow.guard.Check(), ErrTestSessionMissing) {
		t.Fatal("restored historical validity")
	}
	if _, err := observeWindow(w, ctx, 10); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("old owner not fenced", err)
	}
	var reason string
	if err := db.QueryRowContext(ctx, `SELECT reason FROM futures_heartbeat_windows WHERE window_id=$1`, w.id).Scan(&reason); err != nil || reason != "receiver_replaced" {
		t.Fatal(reason, err)
	}
	// Even a timestamp within MaxAge is ineligible if it predates this window.
	oldBody := guardBody(t, newWindow.guard, "unread-old", newWindow.startedAt.Add(-time.Millisecond))
	captureStream(t, w.store, ctx, newHandle, "1-0", StreamInput{"2-0", oldBody}, StreamInput{"3-0", windowBody(t, newWindow, "new-ready", 0)})
	if _, err := observeWindow(newWindow, ctx, 10); err == nil || !strings.Contains(err.Error(), "precedes_window") {
		t.Fatal("old source heartbeat revived window", err)
	}
	if storedCount(t, db, "futures_heartbeat_observations") != 2 {
		t.Fatal("later ready skipped rejection")
	}
}

func TestHeartbeatWindowIntegrityAndQuarantine(t *testing.T) {
	for _, mode := range []string{"body", "binding", "quarantined", "expired", "duplicate", "recovery_only"} {
		t.Run(mode, func(t *testing.T) {
			w, db, ctx := heartbeatWindowFixture(t)
			raw := windowBody(t, w, "hb", 0)
			if mode == "recovery_only" {
				raw = editBody(t, []byte(runtimeHeartbeats(t)[0].Body), func(m map[string]any) {
					m["routing"] = w.handle.config.Scope.Route
					m["produced_at"] = w.startedAt.Add(time.Millisecond).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339Nano)
				})
			}
			r := captureStream(t, w.store, ctx, w.handle, "0-0", StreamInput{"1-0", raw})
			switch mode {
			case "body":
				if _, err := db.ExecContext(ctx, `UPDATE futures_stream_inbox SET body='changed' WHERE receipt_id=$1`, r.ReceiptIDs[0]); err != nil {
					t.Fatal(err)
				}
			case "binding":
				if _, err := db.ExecContext(ctx, `UPDATE futures_heartbeat_windows SET context_json='{}' WHERE window_id=$1`, w.id); err != nil {
					t.Fatal(err)
				}
			case "quarantined":
				if err := w.store.Quarantine(ctx, w.handle, r.ReceiptIDs[0], "fixture-review"); err != nil {
					t.Fatal(err)
				}
			case "expired":
				w.deadline = time.Now().Add(-time.Millisecond)
			case "duplicate":
				if _, err := observeWindow(w, ctx, 1); err != nil {
					t.Fatal(err)
				}
				expires := w.guard.expiresAt
				captureStream(t, w.store, ctx, w.handle, "1-0", StreamInput{"2-0", raw})
				if _, err := observeWindow(w, ctx, 1); err != nil || !expires.Equal(w.guard.expiresAt) {
					t.Fatal("redelivery renewed freshness", err)
				}
				return
			}
			if _, err := observeWindow(w, ctx, 10); err == nil || !errors.Is(w.guard.Check(), ErrTestSessionStopped) {
				t.Fatal("invalid durable context continued", err)
			}
		})
	}
}

func TestHeartbeatWindowSlowDatabaseCannotBlockExpiry(t *testing.T) {
	w, db, ctx := heartbeatWindowFixture(t)
	w.guard.policy.MaxAge = 200 * time.Millisecond
	captureStream(t, w.store, ctx, w.handle, "0-0", StreamInput{"1-0", windowBody(t, w, "ready", 0)})
	if _, err := observeWindow(w, ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION slow_window() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(2); RETURN NEW; END $$; CREATE TRIGGER slow_window BEFORE UPDATE ON futures_heartbeat_windows FOR EACH ROW EXECUTE FUNCTION slow_window()`); err != nil {
		t.Fatal(err)
	}
	captureStream(t, w.store, ctx, w.handle, "1-0", StreamInput{"2-0", guardBody(t, w.guard, "renew", time.Now())})
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); w.guard.watch(run, cancel) }()
	done := make(chan error, 1)
	go func() { _, err := observeWindow(w, run, 10); done <- err }()
	select {
	case <-run.Done():
		if !errors.Is(context.Cause(run), ErrTestSessionStopped) {
			t.Error(context.Cause(run))
		}
	case <-time.After(time.Second):
		t.Error("slow DB prevented heartbeat expiry")
		cancel(context.DeadlineExceeded)
	}
	<-watchDone
	if err := <-done; err == nil {
		t.Fatal("expired observer committed renewal")
	}
	if storedCount(t, db, "futures_heartbeat_observations") != 1 {
		t.Fatal("late renewal became durable")
	}
}

func TestHeartbeatWindowBoundariesAndNonDestructiveDown(t *testing.T) {
	w, db, ctx := heartbeatWindowFixture(t)
	var binding struct{ Started, Deadline string }
	if err := json.Unmarshal(w.binding, &binding); err != nil || !strings.HasSuffix(binding.Started, "+08:00") || !strings.HasSuffix(binding.Deadline, "+08:00") {
		t.Fatal("window audit timestamps not in Shanghai time", err)
	}
	for _, boundary := range []HeartbeatBoundary{{"1-0", 0, "review"}, {"0-0", 1, "review"}, {"0-0", 0, ""}, {"0-0", 0, "second-window"}} {
		if _, err := NewTestHeartbeatWindow(ctx, w.store, w.source, w.handle, w.guard.expected, w.guard.policy, boundary); err == nil {
			t.Fatal("stale/unreviewed/duplicate window accepted")
		}
	}
	if storedCount(t, db, "futures_heartbeat_windows") != 1 {
		t.Fatal("rejected begin changed windows")
	}
	raw, err := os.ReadFile("../../migrations/postgres/000041_futures_heartbeat_windows.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(raw)); err == nil {
		t.Fatal("downgrade erased evidence")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO futures_heartbeat_windows SELECT 'limit-'||n,stream_key,receiver_owner,context_json,review_id,boundary_cursor,boundary_receipt,after_receipt,started_at,state,reason,stopped_at FROM futures_heartbeat_windows CROSS JOIN generate_series(1,127) n WHERE window_id=$1`, w.id); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTestHeartbeatWindow(ctx, w.store, w.source, w.handle, w.guard.expected, w.guard.policy, HeartbeatBoundary{"0-0", 0, "review"}); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal("window capacity ignored", err)
	}
}

func realHeartbeatWindow(t *testing.T, c *TestConsumer, ctx context.Context, maxAge time.Duration) (*TestHeartbeatWindow, *redis.Client) {
	t.Helper()
	client := redis.NewClient(c.source.reader.client.Options())
	t.Cleanup(func() { _ = client.Close() })
	h, err := c.event.store.Open(ctx, c.source.StreamConfig("hb"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := client.Exists(ctx, h.stream).Result(); err != nil || n != 0 {
		t.Fatal("development heartbeat already exists")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(cleanup, h.stream).Err(); err != nil {
			t.Error("development heartbeat cleanup failed", err)
		}
	})
	appendRealStream(t, ctx, client, h, "1-0", "body", "reviewed fixture history")
	if err := client.Expire(ctx, h.stream, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	r, err := c.source.ReadOnce(ctx, c.event.store, h, 1)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultTestHeartbeatPolicy()
	policy.MaxAge = maxAge
	w, err := NewTestHeartbeatWindow(ctx, c.event.store, c.source, h, c.expected, policy, HeartbeatBoundary{r.Cursor, r.ReceiptIDs[0], "reviewed-fixture-only"})
	if err != nil {
		t.Fatal(err)
	}
	return w, client
}

func TestHeartbeatWindowConsumerBootstrapDisconnectIsNotSkipped(t *testing.T) {
	c, db, ctx := realConsumer(t, 1)
	w, client := realHeartbeatWindow(t, c, ctx, time.Second)
	appendRealStream(t, ctx, client, w.handle, "2-0", "body", windowBody(t, w, "ready", 0))
	down := editBody(t, windowBody(t, w, "down", time.Millisecond), func(m map[string]any) { m["payload"].(map[string]any)["broker_ready"] = false })
	appendRealStream(t, ctx, client, w.handle, "3-0", "body", down)
	appendRealStream(t, ctx, client, w.handle, "4-0", "body", windowBody(t, w, "later", 2*time.Millisecond))
	run, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cycles := 0
	err := c.RunHeartbeatWindow(run, w, 10*time.Millisecond, func(cycle ConsumerCycle) {
		if !cycle.Stopped {
			cycles++
		}
	})
	if !errors.Is(err, ErrTestSessionStopped) || cycles != 0 {
		t.Fatal(cycles, err)
	}
	var business, retired int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM futures_stream_inbox WHERE stream_key<>$1`, w.handle.stream).Scan(&business); err != nil || business != 0 {
		t.Fatal("business consumption started during bootstrap", business, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM futures_query_batches WHERE retired`).Scan(&retired); err != nil || retired != 4 {
		t.Fatal(retired, err)
	}
}

func TestHeartbeatWindowConsumerStopsOnNewDisconnectOrExpiry(t *testing.T) {
	for _, mode := range []string{"disconnect", "expire", "missing"} {
		t.Run(mode, func(t *testing.T) {
			c, db, ctx := realConsumer(t, 4)
			c.options.MinDelay, c.options.MaxDelay = 2*time.Second, 2*time.Second
			w, client := realHeartbeatWindow(t, c, ctx, 800*time.Millisecond)
			if mode != "missing" {
				appendRealStream(t, ctx, client, w.handle, "2-0", "body", windowBody(t, w, "ready", 0))
			}
			run, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			cycles := 0
			err := c.RunHeartbeatWindow(run, w, 20*time.Millisecond, func(cycle ConsumerCycle) {
				if cycle.Stopped {
					return
				}
				cycles++
				if mode == "disconnect" {
					down := editBody(t, guardBody(t, w.guard, "down", time.Now()), func(m map[string]any) { m["payload"].(map[string]any)["broker_ready"] = false })
					appendRealStream(t, ctx, client, w.handle, "3-0", "body", down)
				}
			})
			if mode == "missing" {
				if !errors.Is(err, context.DeadlineExceeded) || cycles != 0 {
					t.Fatal(cycles, err)
				}
			} else if !errors.Is(err, ErrTestSessionStopped) || cycles != 1 {
				t.Fatal(cycles, err)
			}
			var ready, retired int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='ready'), count(*) FILTER(WHERE retired) FROM futures_query_batches`).Scan(&ready, &retired); err != nil || retired != 4 {
				t.Fatal(ready, retired, err)
			}
			if mode != "missing" && ready != 4 {
				t.Fatal("completed history lost", ready)
			}
			var state string
			if err := db.QueryRowContext(ctx, `SELECT state FROM futures_heartbeat_windows WHERE window_id=$1`, w.id).Scan(&state); err != nil || state != "stopped" {
				t.Fatal(state, err)
			}
			if storedCount(t, db, "futures_commands") != 5 {
				t.Fatal("heartbeat poller created commands")
			}
		})
	}
}
