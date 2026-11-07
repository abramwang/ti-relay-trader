package futures

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
)

func captureConfig() StreamConfig {
	c := streamTestConfig("hb")
	c.Scope = CommandScope{Route: c.Scope.Route}
	return c
}

func TestHeartbeatCaptureCASAndUntrustedNativeScope(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, _ := NewStreamStore(db)
	c := captureConfig()
	initial, err := s.HeartbeatCheckpoint(ctx, c)
	if err != nil || initial.Registered || initial.Cursor != "0-0" || storedCount(t, db, "futures_stream_receivers") != 0 {
		t.Fatal(initial, err)
	}
	h, err := s.OpenHeartbeatCapture(ctx, c, initial)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.HeartbeatCheckpoint(ctx, c)
	if err != nil || !before.Registered || before.Owner != h.owner {
		t.Fatal(before, err)
	}
	if _, err := s.OpenHeartbeatCapture(ctx, c, initial); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("default takeover allowed", err)
	}
	raw := []byte{0xff, 0, '{'}
	captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw})
	if _, err := s.OpenHeartbeatCapture(ctx, c, before); !errors.Is(err, ErrStreamCursor) {
		t.Fatal("stale checkpoint takeover", err)
	}
	now, _ := s.HeartbeatCheckpoint(ctx, c)
	newHandle, err := s.OpenHeartbeatCapture(ctx, c, now)
	if err != nil || newHandle.owner == h.owner {
		t.Fatal(err)
	}
	if _, err := s.Cursor(ctx, h); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("old writer remains live", err)
	}
	page, err := s.HeartbeatAudit(ctx, newHandle, 0, 1)
	if err != nil || len(page) != 1 || !bytes.Equal(page[0].Receipt.Raw, raw) || page[0].DecodeError == "" {
		t.Fatal(page, err)
	}
	for _, mutate := range []func(*StreamConfig){
		func(c *StreamConfig) { c.Scope.Route.Env = "prod" },
		func(c *StreamConfig) { c.Scope.Route.BrokerID = "huaxin" },
		func(c *StreamConfig) { c.Role = "event" },
		func(c *StreamConfig) { c.Role = "reply" },
		func(c *StreamConfig) { c.Scope.NativeBrokerID = "4900" },
		func(c *StreamConfig) { c.SourceID += "-changed" },
	} {
		other := c
		mutate(&other)
		if _, err := s.OpenHeartbeatCapture(ctx, other, now); err == nil {
			t.Fatal("capture scope mutation accepted")
		}
	}
	full := streamTestConfig("hb")
	if _, err := s.Open(ctx, full); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("transport promoted into native binding", err)
	}
}

func TestHeartbeatCaptureConcurrentCreateAndResume(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, _ := NewStreamStore(db)
	c := captureConfig()
	expected := HeartbeatCheckpoint{Cursor: "0-0"}
	for round := 0; round < 2; round++ {
		var success atomic.Int32
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.OpenHeartbeatCapture(ctx, c, expected)
				if err == nil {
					success.Add(1)
				} else if !errors.Is(err, ErrStreamOwner) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if success.Load() != 1 {
			t.Fatal("concurrent claim winners", success.Load())
		}
		var err error
		expected, err = s.HeartbeatCheckpoint(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHeartbeatCaptureRegistrationFailurePreservesPreviousOwner(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, _ := NewStreamStore(db)
	c := captureConfig()
	h, err := s.OpenHeartbeatCapture(ctx, c, HeartbeatCheckpoint{Cursor: "0-0"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.HeartbeatCheckpoint(ctx, c)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_capture_owner() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$; CREATE CONSTRAINT TRIGGER fail_capture_owner AFTER UPDATE ON futures_stream_receivers DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_capture_owner()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenHeartbeatCapture(ctx, c, before); err == nil {
		t.Fatal("commit failure ignored")
	}
	after, err := s.HeartbeatCheckpoint(ctx, c)
	if err != nil || after != before {
		t.Fatal("failed takeover changed checkpoint", after, err)
	}
	if _, err := s.Cursor(ctx, h); err != nil {
		t.Fatal("old owner lost on rollback", err)
	}
}

func realHeartbeatCapture(t *testing.T) (*sql.DB, *StreamStore, *TestStreamSource, StreamHandle, *redis.Client, context.Context) {
	t.Helper()
	_, _, h, client, _, _ := realStreamRole(t, "hb")
	// Separate temporary schema models an independent raw collector, not the
	// native-bound receiver used to seed this random Redis fixture.
	db, _, ctx := queryTestDB(t)
	cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
	if err != nil {
		t.Fatal("cannot load test Redis configuration")
	}
	source, err := NewTestHeartbeatSource(TestSourceConfig{Scope: CommandScope{Route: h.config.Scope.Route}, DeploymentID: "synthetic-capture-test", RedisURL: cfg.Redis.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { source.Close() })
	store, _ := NewStreamStore(db)
	return db, store, source, h, client, ctx
}

func captureWindow(t *testing.T, parent context.Context) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHeartbeatCaptureRealRedisBoundedRestartAndRawEvidence(t *testing.T) {
	db, store, source, h, client, ctx := realHeartbeatCapture(t)
	body := []byte(runtimeHeartbeats(t)[0].Body)
	appendRealStream(t, ctx, client, h, "2-0", "body", string(body))
	appendRealStream(t, ctx, client, h, "3-0", "body", string([]byte{0xff, 0}))
	reports := []HeartbeatCaptureProgress{}
	emit := func(p HeartbeatCaptureProgress) error { reports = append(reports, p); return nil }
	if err := CaptureTestHeartbeats(captureWindow(t, ctx), source, store, HeartbeatCheckpoint{Cursor: "0-0"}, HeartbeatCaptureOptions{1, 1}, emit); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || reports[1].Phase != "page_budget" || reports[1].Cursor != "1-0" || reports[1].BusinessAuthorized {
		t.Fatal(reports)
	}
	checkpoint, err := store.HeartbeatCheckpoint(ctx, source.StreamConfig("hb"))
	if err != nil || checkpoint.Records != 1 {
		t.Fatal(checkpoint, err)
	}
	if err := CaptureTestHeartbeats(captureWindow(t, ctx), source, store, checkpoint, HeartbeatCaptureOptions{1, 8}, emit); err != nil {
		t.Fatal(err)
	}
	last := reports[len(reports)-1]
	if last.Phase != "idle" || last.Cursor != "3-0" || last.Captured != 2 || last.Owner == checkpoint.Owner {
		t.Fatal(last)
	}
	latest, _ := store.HeartbeatCheckpoint(ctx, source.StreamConfig("hb"))
	if latest.Records != 3 {
		t.Fatal("restart duplicated evidence", latest)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_stream_inbox WHERE entry_id='2-0'`).Scan(&raw); err != nil || !bytes.Equal(raw, body) {
		t.Fatal("raw changed", err)
	}
	for _, table := range []string{"futures_query_batches", "futures_event_inbox", "futures_heartbeat_windows"} {
		if storedCount(t, db, table) != 0 {
			t.Fatal("capture entered business processing", table)
		}
	}
	groups, err := client.XInfoGroups(ctx, h.stream).Result()
	if err != nil || len(groups) != 0 {
		t.Fatal("consumer groups changed", err)
	}
	full := tradeScope()
	full.Route = h.config.Scope.Route
	if _, err := NewTestSessionGuard(source, DeliveryQualityContext{full, "fixture-session", 1}, DefaultTestHeartbeatPolicy()); err == nil {
		t.Fatal("transport-only source activated business guard")
	}
}

func TestHeartbeatCaptureRealRedisOutputFailureAndRecovery(t *testing.T) {
	_, store, source, _, _, ctx := realHeartbeatCapture(t)
	fail := errors.New("synthetic sink unavailable")
	err := CaptureTestHeartbeats(captureWindow(t, ctx), source, store, HeartbeatCheckpoint{Cursor: "0-0"}, HeartbeatCaptureOptions{1, 3}, func(p HeartbeatCaptureProgress) error { return fail })
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	checkpoint, err := store.HeartbeatCheckpoint(ctx, source.StreamConfig("hb"))
	if err != nil || checkpoint.Cursor != "0-0" || checkpoint.Records != 0 || !checkpoint.Registered {
		t.Fatal(checkpoint, err)
	}
	err = CaptureTestHeartbeats(captureWindow(t, ctx), source, store, checkpoint, HeartbeatCaptureOptions{1, 3}, func(p HeartbeatCaptureProgress) error {
		if p.Phase != "opened" {
			return fail
		}
		return nil
	})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	checkpoint, err = store.HeartbeatCheckpoint(ctx, source.StreamConfig("hb"))
	if err != nil || checkpoint.Cursor != "1-0" || checkpoint.Records != 1 {
		t.Fatal("output loss discarded committed page", checkpoint, err)
	}
	if err := CaptureTestHeartbeats(captureWindow(t, ctx), source, store, checkpoint, HeartbeatCaptureOptions{1, 3}, func(p HeartbeatCaptureProgress) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, _ := store.HeartbeatCheckpoint(ctx, source.StreamConfig("hb"))
	if after.Records != 1 {
		t.Fatal("output recovery duplicated raw")
	}
}
