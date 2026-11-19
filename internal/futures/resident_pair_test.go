package futures

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResidentCheckpoints(t *testing.T) {
	raw := `{"reply":{"owner":"","cursor":"0-0"},"event":{"owner":"","cursor":"0-0"}}`
	c, err := DecodeResidentCheckpoints([]byte(raw))
	if err != nil || c.Reply.Cursor != "0-0" || c.Event.Cursor != "0-0" {
		t.Fatal(c, err)
	}
	for _, invalid := range []string{`null`, `{}`, `{"reply":{"cursor":"0-0"}}`, raw + `{}`, strings.Replace(raw, `"reply"`, `"unknown"`, 1), strings.Replace(raw, `"owner":""`, `"owner":"short"`, 1), strings.Replace(raw, `"cursor":"0-0"`, `"cursor":"1-0"`, 1), strings.Repeat(" ", 4097)} {
		if _, err := DecodeResidentCheckpoints([]byte(invalid)); err == nil {
			t.Fatal("unsafe checkpoints", invalid)
		}
	}
}

func TestResidentPairLifecycle(t *testing.T) {
	fault := errors.New("checkpoint persistence failed")
	for _, mode := range []string{"cancel", "deadline", "fault", "joined_cancel", "budget", "unexpected_return"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			var stopped atomic.Int32
			err := runCapturePair(ctx, func(run context.Context, role string) error {
				defer stopped.Add(1)
				if role == "reply" {
					switch mode {
					case "cancel":
						cancel()
					case "fault":
						return fault
					case "joined_cancel":
						return errors.Join(context.Canceled, fault)
					case "budget":
						return ErrResidentCaptureBudget
					case "unexpected_return":
						return nil
					}
				}
				<-run.Done()
				return run.Err()
			})
			if stopped.Load() != 2 || err == nil {
				t.Fatal("returned before both receivers stopped", stopped.Load(), err)
			}
			switch mode {
			case "cancel":
				if err != context.Canceled {
					t.Fatal(err)
				}
			case "deadline":
				if err != context.DeadlineExceeded {
					t.Fatal(err)
				}
			case "fault", "joined_cancel":
				if !errors.Is(err, fault) || err == context.Canceled {
					t.Fatal(err)
				}
			case "budget":
				if !errors.Is(err, ErrResidentCaptureBudget) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestResidentPairRawCaptureResumeAndPeerFailure(t *testing.T) {
	ctx, s, store, p, o := residentFixture(t)
	o.MaxRecords = 100
	checkpoints := ResidentCheckpoints{Reply: HeartbeatCheckpoint{Cursor: "0-0"}, Event: HeartbeatCheckpoint{Cursor: "0-0"}}
	keys := map[string]string{}
	for _, role := range []string{"reply", "event"} {
		keys[role], _ = s.StreamConfig(role).stream()
		residentAppend(t, ctx, s, keys[role], "1-0", `{"synthetic":true,"role":"`+role+`"}`)
	}
	last := map[string]ResidentCaptureProgress{}
	run, cancel := context.WithCancel(ctx)
	err := FollowTestRawPair(run, s, store, p, o, checkpoints, func(v ResidentCaptureProgress) error {
		last[v.Stream] = v
		if last[keys["reply"]].Captured == 1 && last[keys["event"]].Captured == 1 {
			if v.Phase != "stopped" {
				if health, _, e := readCaptureHealth(ctx, store.db, s.scope, p.target); e != nil || !health.Ready {
					return errors.New("both captured lanes failed to publish health")
				}
			}
			cancel()
		}
		return nil
	})
	cancel()
	if err != context.Canceled || len(last) != 2 {
		t.Fatal(last, err)
	}
	if health, _, err := readCaptureHealth(ctx, store.db, s.scope, p.target); err == nil || health.State != "stopped" {
		t.Fatal("pair exit did not close readiness", health, err)
	}
	for _, v := range last {
		if v.Phase != "stopped" || !v.CheckpointVerified || v.Checkpoint.Cursor != "1-0" || v.BusinessAuthorized {
			t.Fatal(v)
		}
	}
	checkpoints.Reply, checkpoints.Event = last[keys["reply"]].Checkpoint, last[keys["event"]].Checkpoint
	old := checkpoints
	// A stale event owner must not rotate the otherwise valid reply owner.
	bad := checkpoints
	bad.Event.Owner = strings.Repeat("a", 64)
	if err := FollowTestRawPair(ctx, s, store, p, o, bad, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrStreamOwner) {
		t.Fatal(err)
	}
	cp, err := store.ReplyCheckpoint(ctx, s.StreamConfig("reply"))
	if err != nil || cp.Owner != old.Reply.Owner {
		t.Fatal(cp, err)
	}
	for _, role := range []string{"reply", "event"} {
		residentAppend(t, ctx, s, keys[role], "2-0", `{"synthetic":true,"late":true}`)
	}
	last = map[string]ResidentCaptureProgress{}
	run, cancel = context.WithCancel(ctx)
	err = FollowTestRawPair(run, s, store, p, o, checkpoints, func(v ResidentCaptureProgress) error {
		last[v.Stream] = v
		if last[keys["reply"]].Captured == 1 && last[keys["event"]].Captured == 1 {
			cancel()
		}
		return nil
	})
	cancel()
	if err != context.Canceled || storedCount(t, store.db, "futures_stream_inbox") != 4 {
		t.Fatal(last, err)
	}
	checkpoints.Reply, checkpoints.Event = last[keys["reply"]].Checkpoint, last[keys["event"]].Checkpoint
	if checkpoints.Reply.Owner == old.Reply.Owner || checkpoints.Event.Owner == old.Event.Owner {
		t.Fatal("owners not rotated")
	}
	// Break one stream after both have been durably collected. Its peer must
	// stop even though it is idle; neither path may create commands or ACKs.
	if err := s.reader.client.Del(ctx, keys["event"]).Err(); err != nil {
		t.Fatal(err)
	}
	last = map[string]ResidentCaptureProgress{}
	err = FollowTestRawPair(ctx, s, store, p, o, checkpoints, func(v ResidentCaptureProgress) error { last[v.Stream] = v; return nil })
	if !errors.Is(err, ErrStreamHistory) || len(last) != 2 {
		t.Fatal(last, err)
	}
	for _, v := range last {
		if v.Phase != "stopped" {
			t.Fatal(v)
		}
	}
	var pending int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM futures_stream_inbox WHERE state='pending'`).Scan(&pending); err != nil || pending != 4 {
		t.Fatal(pending, err)
	}
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if n, err := s.reader.client.XLen(ctx, p.prefix()+":"+suffix).Result(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
}
