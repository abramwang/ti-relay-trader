package futures

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestResidentCaptureBounds(t *testing.T) {
	o := DefaultResidentCaptureOptions()
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ResidentCaptureOptions){
		func(o *ResidentCaptureOptions) { o.Role = "hb" },
		func(o *ResidentCaptureOptions) { o.MaxRecords = 0 },
		func(o *ResidentCaptureOptions) { o.MaxRecords = 50001 },
		func(o *ResidentCaptureOptions) { o.PageSize = 257 },
		func(o *ResidentCaptureOptions) { o.StepTimeout = 0 },
		func(o *ResidentCaptureOptions) { o.MinDelay = 0 },
		func(o *ResidentCaptureOptions) { o.MaxDelay = 2 * time.Minute },
		func(o *ResidentCaptureOptions) { o.ReportInterval = 0 },
		func(o *ResidentCaptureOptions) { o.Checkpoint.Cursor = "1-0" },
	} {
		bad := o
		change(&bad)
		if bad.validate() == nil {
			t.Fatal("unsafe options", bad)
		}
	}
	if err := FollowTestRawStream(context.Background(), nil, nil, nil, o, nil); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal(err)
	}
}

func residentFixture(t *testing.T) (context.Context, *TestStreamSource, *StreamStore, *TestQueryPublisher, ResidentCaptureOptions) {
	t.Helper()
	db, _, ctx := queryTestDB(t)
	config := sourceFixture()
	r := newLocalStorageRedis(t)
	config.RedisURL = "unix://" + r.socket
	source, err := NewTestStreamSource(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { source.Close() })
	store, _ := NewStreamStore(db)
	target := publicationTarget()
	target.SourceID = source.String()
	info, err := source.reader.client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	target.ServerRunID = redisStorageField(info, "run_id")
	publisher, err := NewTestQueryPublisher(config, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { publisher.Close() })
	client := source.reader.client
	if err := client.Set(ctx, publisher.prefix()+":cmd.trade:transport-epoch", target.Epoch, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if err := client.XGroupCreateMkStream(ctx, publisher.prefix()+":"+suffix, target.Group, "0-0").Err(); err != nil {
			t.Fatal(err)
		}
	}
	o := DefaultResidentCaptureOptions()
	o.PageSize, o.MaxRecords = 1, 2
	o.MinDelay, o.MaxDelay = 10*time.Millisecond, 20*time.Millisecond
	o.StepTimeout, o.ReportInterval = time.Second, time.Second
	return ctx, source, store, publisher, o
}

func residentAppend(t *testing.T, ctx context.Context, s *TestStreamSource, key, id, raw string) {
	t.Helper()
	if err := s.reader.client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: id, Values: []any{"body", raw}}).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestResidentCaptureLazyTailAndResume(t *testing.T) {
	for _, role := range []string{"reply", "event"} {
		t.Run(role, func(t *testing.T) {
			ctx, s, store, p, o := residentFixture(t)
			o.Role = role
			key, _ := s.StreamConfig(role).stream()
			var progress []ResidentCaptureProgress
			waited := false
			err := FollowTestRawStream(ctx, s, store, p, o, func(v ResidentCaptureProgress) error {
				progress = append(progress, v)
				if v.Phase == "waiting_stream" && !waited {
					waited = true
					if v.Checkpoint.Registered {
						t.Fatal("lazy wait claimed receiver")
					}
					residentAppend(t, ctx, s, key, "1-0", `{"state":"stopping"}`)
				}
				if v.Phase == "captured" && v.Captured == 1 {
					// No heartbeat/login/trade permission exists, yet late originals
					// must survive without acquiring business authority.
					residentAppend(t, ctx, s, key, "2-0", `{"late":true,"session":"next"}`)
				}
				return nil
			})
			last := progress[len(progress)-1]
			if !errors.Is(err, ErrResidentCaptureBudget) || !waited || !last.CheckpointVerified || last.Checkpoint.Cursor != "2-0" || last.StopReason != "record_budget" || last.BusinessAuthorized {
				t.Fatal(last, err)
			}
			if n := storedCount(t, store.db, "futures_stream_inbox"); n != 2 {
				t.Fatal(n)
			}
			var pending int
			if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM futures_stream_inbox WHERE state='pending'`).Scan(&pending); err != nil || pending != 2 {
				t.Fatal(pending, err)
			}
			old := last.Checkpoint
			o.Checkpoint, o.MaxRecords = old, 1
			residentAppend(t, ctx, s, key, "3-0", `{"resumed":true}`)
			err = FollowTestRawStream(ctx, s, store, p, o, func(v ResidentCaptureProgress) error { last = v; return nil })
			if !errors.Is(err, ErrResidentCaptureBudget) || last.Checkpoint.Cursor != "3-0" || last.Checkpoint.Owner == old.Owner || last.Captured != 1 {
				t.Fatal(last, err)
			}
			if n := storedCount(t, store.db, "futures_stream_inbox"); n != 3 {
				t.Fatal(n)
			}
			if err := FollowTestRawStream(ctx, s, store, p, o, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrStreamOwner) {
				t.Fatal("stale owner accepted", err)
			}
			for _, suffix := range []string{"cmd.query", "cmd.trade"} {
				if n, err := s.reader.client.XLen(ctx, p.prefix()+":"+suffix).Result(); err != nil || n != 0 {
					t.Fatal("receiver wrote command", n, err)
				}
			}
		})
	}
}

func TestResidentCaptureTransportAndHistoryFailures(t *testing.T) {
	for _, scenario := range []string{"run_id", "epoch", "group", "delete_stream", "rewrite_tail", "output_failure", "db_failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, s, store, p, o := residentFixture(t)
			key, _ := s.StreamConfig(o.Role).stream()
			residentAppend(t, ctx, s, key, "1-0", `{"original":true}`)
			var last ResidentCaptureProgress
			outputFailure := errors.New("synthetic output failure")
			err := FollowTestRawStream(ctx, s, store, p, o, func(v ResidentCaptureProgress) error {
				last = v
				if v.Phase != "captured" {
					return nil
				}
				client := s.reader.client
				switch scenario {
				case "run_id":
					p.target.ServerRunID = strings.Repeat("0", 40)
				case "epoch":
					if err := client.Set(ctx, p.prefix()+":cmd.trade:transport-epoch", "changed", 0).Err(); err != nil {
						t.Fatal(err)
					}
				case "group":
					if err := client.XGroupDestroy(ctx, p.prefix()+":cmd.query", p.target.Group).Err(); err != nil {
						t.Fatal(err)
					}
				case "delete_stream", "rewrite_tail":
					if err := client.Del(ctx, key).Err(); err != nil {
						t.Fatal(err)
					}
					if scenario == "rewrite_tail" {
						residentAppend(t, ctx, s, key, "1-0", `{"rewritten":true}`)
					}
				case "output_failure":
					return outputFailure
				case "db_failure":
					_, err := store.db.ExecContext(ctx, `CREATE FUNCTION reject_resident_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER reject_resident BEFORE INSERT ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION reject_resident_insert()`)
					if err != nil {
						t.Fatal(err)
					}
					residentAppend(t, ctx, s, key, "2-0", `{"not_committed":true}`)
				}
				return nil
			})
			if err == nil || errors.Is(err, ErrResidentCaptureBudget) || last.StopReason != "capture_failed" || !last.CheckpointVerified || last.Checkpoint.Cursor != "1-0" {
				t.Fatal(last, err)
			}
			if scenario == "rewrite_tail" && !errors.Is(err, ErrStreamHistory) {
				t.Fatal(err)
			}
			if scenario == "output_failure" && !errors.Is(err, outputFailure) {
				t.Fatal(err)
			}
			var body []byte
			if err := store.db.QueryRowContext(ctx, `SELECT body FROM futures_stream_inbox WHERE entry_id='1-0'`).Scan(&body); err != nil || string(body) != `{"original":true}` {
				t.Fatal(string(body), err)
			}
			if n := storedCount(t, store.db, "futures_stream_inbox"); n != 1 {
				t.Fatal(n)
			}
		})
	}
}

func TestResidentCaptureCancellationAndLazyDeadline(t *testing.T) {
	ctx, s, store, p, o := residentFixture(t)
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	var last ResidentCaptureProgress
	err := FollowTestRawStream(short, s, store, p, o, func(v ResidentCaptureProgress) error { last = v; return nil })
	cancel()
	if err != context.DeadlineExceeded || last.StopReason != "duration_elapsed" || last.Checkpoint.Registered {
		t.Fatal(last, err)
	}
	key, _ := s.StreamConfig(o.Role).stream()
	residentAppend(t, ctx, s, key, "1-0", `{"durable":true}`)
	stop, cancel := context.WithCancel(ctx)
	err = FollowTestRawStream(stop, s, store, p, o, func(v ResidentCaptureProgress) error {
		last = v
		if v.Phase == "captured" {
			cancel()
		}
		return nil
	})
	cancel()
	if err != context.Canceled || last.StopReason != "cancelled" || !last.CheckpointVerified || last.Checkpoint.Cursor != "1-0" {
		t.Fatal(last, err)
	}
	// A previously captured stream disappearing must fail, not wait as if new.
	o.Checkpoint = last.Checkpoint
	if err := s.reader.client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := FollowTestRawStream(ctx, s, store, p, o, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrStreamHistory) {
		t.Fatal(err)
	}
}

func TestResidentCaptureIdleReceivesLaterAndFinalOutputError(t *testing.T) {
	ctx, s, store, p, o := residentFixture(t)
	key, _ := s.StreamConfig(o.Role).stream()
	residentAppend(t, ctx, s, key, "1-0", `{"first":true}`)
	waited := false
	var last ResidentCaptureProgress
	err := FollowTestRawStream(ctx, s, store, p, o, func(v ResidentCaptureProgress) error {
		last = v
		if v.Phase == "idle" && !waited {
			waited = true
			residentAppend(t, ctx, s, key, "2-0", `{"after_idle":true}`)
		}
		return nil
	})
	if err != ErrResidentCaptureBudget || !waited || last.Checkpoint.Cursor != "2-0" {
		t.Fatal(last, err)
	}
	o.Checkpoint = last.Checkpoint
	stop, cancel := context.WithCancel(ctx)
	outputErr := errors.New("final progress output failed")
	err = FollowTestRawStream(stop, s, store, p, o, func(v ResidentCaptureProgress) error {
		if v.Phase == "idle" {
			cancel()
		}
		if v.Phase == "stopped" {
			return outputErr
		}
		return nil
	})
	cancel()
	if err == context.Canceled || !errors.Is(err, outputErr) {
		t.Fatal("output failure hidden by normal shutdown", err)
	}
}
