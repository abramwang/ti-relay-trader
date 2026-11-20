package futures

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type countCaptureReads struct{ reads atomic.Int64 }

func (h *countCaptureReads) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *countCaptureReads) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *countCaptureReads) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "xread" {
			h.reads.Add(1)
		}
		return next(ctx, cmd)
	}
}

// Actual raw bytes fill the configured quota. Quarantined synthetic filler is
// unresolved, never a fabricated delivered record or a modified usage counter.
func seedResidentBacklog(t *testing.T, ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, role string, raw []byte) HeartbeatCheckpoint {
	t.Helper()
	h, err := store.openBound(ctx, source.StreamConfig(role), &HeartbeatCheckpoint{Cursor: "0-0"}, &verifier.target)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), maxSnapshotBytes)
	cursor := "0-0"
	for i := 1; i <= 9; i++ {
		body := chunk
		if i == 1 {
			body = raw
		} else if i == 9 {
			body = chunk[:maxSnapshotBytes-len(raw)]
		}
		id := fmt.Sprintf("%d-0", i)
		residentAppend(t, ctx, source, h.stream, id, string(body))
		r := captureStream(t, store, ctx, h, cursor, StreamInput{id, body})
		if i > 1 {
			if err := store.Quarantine(ctx, h, r.ReceiptIDs[0], "synthetic unresolved filler"); err != nil {
				t.Fatal(err)
			}
		}
		cursor = id
	}
	if s := checkStreamUsage(t, store.db, ctx, store, h); s.Pending != 1 || s.Quarantined != 8 || s.Bytes != maxStreamBacklogBytes {
		t.Fatal(s)
	}
	residentAppend(t, ctx, source, h.stream, "10-0", `{}`)
	cp, err := store.captureCheckpoint(ctx, source.StreamConfig(role))
	if err != nil {
		t.Fatal(err)
	}
	return cp
}

func TestCaptureServiceBackpressureDrainsAndResumes(t *testing.T) {
	ctx, source, store, verifier, c := captureServiceFixture(t)
	r := residentProjectionCommand(t, ctx, store.db, source.scope, verifier.target)
	raw := resultTradeBody(t, r, verifier.target)
	c.InitialCheckpoints.Reply = seedResidentBacklog(t, ctx, source, store, verifier, "reply", raw)
	c.ProjectReplies = true
	if _, err := store.db.ExecContext(ctx, `CREATE FUNCTION require_backpressure_health() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='delivered' AND NOT EXISTS(SELECT 1 FROM futures_capture_health WHERE state='backpressured') THEN RAISE EXCEPTION 'handoff ran before readiness closed'; END IF; RETURN NEW; END $$; CREATE TRIGGER require_backpressure_health BEFORE UPDATE ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION require_backpressure_health()`); err != nil {
		t.Fatal(err)
	}
	hook := &countCaptureReads{}
	source.reader.client.AddHook(hook)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	watch := time.AfterFunc(8*time.Second, cancel)
	defer watch.Stop()
	paused, resumed := false, false
	err := RunTestCaptureService(run, source, store, verifier, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "backpressured" {
			paused = true
			if v.Checkpoint.Cursor != "9-0" || !v.CheckpointVerified || v.Captured != 0 || v.ReplyProjection == nil || v.ReplyProjection.Delivered != 1 {
				t.Error("did not drain the real reply at capacity", v)
			}
			view, _, err := readCaptureHealth(ctx, store.db, source.scope, verifier.target)
			if !errors.Is(err, ErrCaptureUnavailable) || view.Ready || view.State != "backpressured" {
				t.Error("projection renewed health before a successful read", view, err)
			}
		}
		if paused && v.Phase == "captured" && v.Checkpoint.Cursor == "10-0" {
			resumed = true
		}
		if resumed && v.Phase != "stopped" {
			view, _, err := readCaptureHealth(ctx, store.db, source.scope, verifier.target)
			if err == nil && view.Ready {
				cancel()
			}
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || !paused || !resumed || hook.reads.Load() < 2 {
		t.Fatal(paused, resumed, hook.reads.Load(), err)
	}
	cp := serviceOwners(t, ctx, store, source)
	h := StreamHandle{config: source.StreamConfig("reply"), stream: serviceKeys(source)["reply"], owner: cp.Reply.Owner}
	s := checkStreamUsage(t, store.db, ctx, store, h)
	if s.Cursor != "10-0" || s.Records != 10 || s.Bytes != maxStreamBacklogBytes+2 || s.Delivered != 1 || s.Quarantined != 8 || s.Pending != 1 {
		t.Fatal("lost evidence or invented delivery", s)
	}
	var original []byte
	if err := store.db.QueryRowContext(ctx, `SELECT body FROM futures_stream_inbox WHERE stream_key=$1 AND entry_id='1-0'`, h.stream).Scan(&original); err != nil || !bytes.Equal(raw, original) {
		t.Fatal("changed original", err)
	}
	if storedCount(t, store.db, "futures_reply_results") != 1 || storedCount(t, store.db, "futures_order_states") != 0 {
		t.Fatal("receipt was not retained separately from order state")
	}
	assertServiceStopped(t, ctx, store, source, verifier)
}

func TestResidentBackpressureWaitAndFaults(t *testing.T) {
	for _, scenario := range []string{"cancel_resume", "epoch", "tail_loss", "unread_loss", "counters"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, source, store, verifier, o := residentFixture(t)
			cp := seedResidentBacklog(t, ctx, source, store, verifier, "reply", []byte(`{"unregistered":true}`))
			checkpoints := ResidentCheckpoints{Reply: cp, Event: HeartbeatCheckpoint{Cursor: "0-0"}}
			hook := &countCaptureReads{}
			source.reader.client.AddHook(hook)
			key := serviceKeys(source)["reply"]
			run, cancel := context.WithCancel(ctx)
			defer cancel()
			watch := time.AfterFunc(5*time.Second, cancel)
			defer watch.Stop()
			paused := 0
			var last HeartbeatCheckpoint
			err := FollowTestRawPair(run, source, store, verifier, o, checkpoints, func(v ResidentCaptureProgress) error {
				if v.Stream == key && v.Phase == "stopped" {
					last = v.Checkpoint
				}
				if v.Phase != "backpressured" {
					return nil
				}
				paused++
				if v.Checkpoint.Cursor != "9-0" || v.Captured != 0 {
					t.Error("advanced paused cursor", v)
				}
				if paused != 1 {
					if scenario == "cancel_resume" {
						cancel()
					}
					return nil
				}
				switch scenario {
				case "epoch":
					return source.reader.client.Set(ctx, verifier.prefix()+":cmd.trade:transport-epoch", "changed", 0).Err()
				case "tail_loss":
					return source.reader.client.XDel(ctx, key, "9-0").Err()
				case "unread_loss":
					return source.reader.client.XDel(ctx, key, "10-0").Err()
				case "counters":
					_, err := store.db.ExecContext(ctx, `UPDATE futures_stream_receivers SET pending_count=pending_count+1 WHERE stream_key=$1`, key)
					return err
				}
				return nil
			})
			if paused == 0 || hook.reads.Load() != 1 || last.Cursor != "9-0" {
				t.Fatal("paused worker reread or skipped input", paused, hook.reads.Load(), last, err)
			}
			if scenario != "cancel_resume" {
				if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("integrity failure treated as retryable pressure", err)
				}
				return
			}
			if paused < 2 || !errors.Is(err, context.Canceled) {
				t.Fatal("did not remain paused until cancellation", paused, err)
			}
			checkpoints.Reply = last
			run, stop := context.WithCancel(ctx)
			defer stop()
			err = FollowTestRawPair(run, source, store, verifier, o, checkpoints, func(v ResidentCaptureProgress) error {
				if v.Phase == "backpressured" {
					if v.Checkpoint.Cursor != last.Cursor || v.Checkpoint.Owner == last.Owner || v.Captured != 0 {
						t.Error("restart lost paused responsibility", v)
					}
					stop()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || hook.reads.Load() != 2 || storedCount(t, store.db, "futures_stream_inbox") != 9 {
				t.Fatal("restart cleared or bypassed backlog", err, hook.reads.Load())
			}
		})
	}
}

func TestCaptureServiceBackpressureProjectionFailureStops(t *testing.T) {
	ctx, source, store, verifier, c := captureServiceFixture(t)
	r := residentProjectionCommand(t, ctx, store.db, source.scope, verifier.target)
	c.InitialCheckpoints.Reply = seedResidentBacklog(t, ctx, source, store, verifier, "reply", resultTradeBody(t, r, verifier.target))
	c.ProjectReplies = true
	if _, err := store.db.ExecContext(ctx, `CREATE FUNCTION fail_backlog_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic handoff failure'; END $$; CREATE TRIGGER fail_backlog_handoff BEFORE UPDATE ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION fail_backlog_handoff()`); err != nil {
		t.Fatal(err)
	}
	err := RunTestCaptureService(ctx, source, store, verifier, c, func(ResidentCaptureProgress) error { return nil })
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrStreamBacklog) {
		t.Fatal("write failure treated as recoverable backlog", err)
	}
	cp := serviceOwners(t, ctx, store, source)
	h := StreamHandle{config: source.StreamConfig("reply"), stream: serviceKeys(source)["reply"], owner: cp.Reply.Owner}
	if s := checkStreamUsage(t, store.db, ctx, store, h); s.Cursor != "9-0" || s.Delivered != 0 || s.Pending != 1 || s.Bytes != maxStreamBacklogBytes {
		t.Fatal("partially committed handoff", s)
	}
	if storedCount(t, store.db, "futures_reply_results") != 0 {
		t.Fatal("failed handoff left a result")
	}
	assertServiceStopped(t, ctx, store, source, verifier)
}

func TestCaptureServiceBackpressureLocalEvents(t *testing.T) {
	ctx, source, store, verifier, c := captureServiceFixture(t)
	r := residentProjectionCommand(t, ctx, store.db, source.scope, verifier.target)
	c.InitialCheckpoints.Event = seedResidentBacklog(t, ctx, source, store, verifier, "event", assessmentOrderBody(t, r, verifier.target, nil))
	c.ProjectLocalEvents = true
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	watch := time.AfterFunc(8*time.Second, cancel)
	defer watch.Stop()
	paused, resumed := false, false
	err := RunTestCaptureService(run, source, store, verifier, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "backpressured" {
			paused = true
			view, _, err := readCaptureHealth(ctx, store.db, source.scope, verifier.target)
			if err == nil || view.Ready || view.State != "backpressured" || v.LocalProjection == nil || v.LocalProjection.Applied != 1 {
				t.Error("event projection did not drain with ingress closed", v, view, err)
			}
		}
		if v.Phase == "captured" && v.Checkpoint.Cursor == "10-0" {
			resumed = true
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || !paused || !resumed || storedCount(t, store.db, "futures_order_states") != 1 {
		t.Fatal(paused, resumed, err)
	}
	cp := serviceOwners(t, ctx, store, source)
	h := StreamHandle{config: source.StreamConfig("event"), stream: serviceKeys(source)["event"], owner: cp.Event.Owner}
	if s := checkStreamUsage(t, store.db, ctx, store, h); s.Delivered != 1 || s.Quarantined != 8 || s.Pending != 1 || s.Records != 10 {
		t.Fatal(s)
	}
	assertServiceStopped(t, ctx, store, source, verifier)
}
