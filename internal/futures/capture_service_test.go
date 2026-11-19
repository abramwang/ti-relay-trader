package futures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func captureServiceFixture(t *testing.T) (context.Context, *TestStreamSource, *StreamStore, *TestQueryPublisher, CaptureServiceConfig) {
	t.Helper()
	ctx, source, existing, verifier, _ := residentFixture(t)
	pg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil || !strings.HasPrefix(pg.Host, "/tmp/relay-futures-pg.") {
		t.Fatal("service tests require disposable local PG")
	}
	name := "relay_service_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_test"
	if _, err := existing.db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	pg.Database = name
	db := stdlib.OpenDB(*pg)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := existing.db.ExecContext(cleanup, "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	})
	for _, m := range futuresTestMigrations {
		migrateFuturesTest(t, db, ctx, m, "up")
	}
	store, _ := NewStreamStore(db)
	c := CaptureServiceConfig{Version: "ctp.capture.service.v1", ServiceID: "isolated-capture-service", Database: name,
		DeploymentID: sourceFixture().DeploymentID, Scope: source.scope, Target: verifier.target,
		InitialCheckpoints: ResidentCheckpoints{Reply: HeartbeatCheckpoint{Cursor: "0-0"}, Event: HeartbeatCheckpoint{Cursor: "0-0"}}}
	return ctx, source, store, verifier, c
}

func serviceKeys(source *TestStreamSource) map[string]string {
	keys := map[string]string{}
	for _, role := range []string{"reply", "event"} {
		keys[role], _ = source.StreamConfig(role).stream()
	}
	return keys
}

func serviceOwners(t *testing.T, ctx context.Context, store *StreamStore, source *TestStreamSource) ResidentCheckpoints {
	t.Helper()
	var out ResidentCheckpoints
	var reply, event string
	err := store.db.QueryRowContext(ctx, `SELECT reply_owner,event_owner FROM futures_capture_services WHERE route_key=$1`, string(commandRouteBytes(source.scope.Route))).Scan(&reply, &event)
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range []struct {
		role, owner string
		cp          *HeartbeatCheckpoint
	}{{"reply", reply, &out.Reply}, {"event", event, &out.Event}} {
		cp, err := store.captureCheckpoint(ctx, source.StreamConfig(lane.role))
		if err != nil || cp.Owner != lane.owner {
			t.Fatal("owner commit was not atomic", cp, lane.owner, err)
		}
		*lane.cp = cp
	}
	return out
}

func assertServiceStopped(t *testing.T, ctx context.Context, store *StreamStore, source *TestStreamSource, p *TestQueryPublisher) {
	t.Helper()
	health, _, err := readCaptureHealth(ctx, store.db, source.scope, p.target)
	if err == nil || health.Ready {
		t.Fatal(health, err)
	}
	var state string
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM futures_capture_health WHERE route_key=$1`, string(commandRouteBytes(source.scope.Route))).Scan(&state); err != nil || state != "stopped" {
		t.Fatal(state, err)
	}
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if n, err := source.reader.client.XLen(ctx, p.prefix()+":"+suffix).Result(); err != nil || n != 0 {
			t.Fatal("service published command", n, err)
		}
	}
}

func TestCaptureServicePersistentResumeAndSingleton(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	keys := serviceKeys(source)
	for _, key := range keys {
		residentAppend(t, ctx, source, key, "1-0", `{"synthetic":true}`)
	}
	var first ResidentCheckpoints
	for round := 1; round <= 3; round++ {
		if round == 2 {
			for _, key := range keys {
				residentAppend(t, ctx, source, key, "2-0", `{"late":true}`)
			}
		}
		// Deliberately no deadline: normal service lifetime is SIGTERM/cancel.
		run, cancel := context.WithCancel(context.Background())
		watch := time.AfterFunc(8*time.Second, cancel)
		seen := map[string]bool{}
		secondRejected := false
		err := RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
			if v.Mode != "test_capture_service_raw" || v.BusinessAuthorized {
				t.Error(v)
			}
			if v.Phase == "stopped" {
				return nil
			}
			if !secondRejected {
				secondRejected = true
				other := RunTestCaptureService(run, source, store, p, c, func(ResidentCaptureProgress) error { return nil })
				if !errors.Is(other, ErrCaptureUnavailable) {
					t.Error("second service accepted", other)
				}
			}
			seen[v.Stream] = true
			if len(seen) == 2 {
				cancel()
			}
			return nil
		})
		watch.Stop()
		cancel()
		if !errors.Is(err, context.Canceled) || len(seen) != 2 {
			t.Fatal(err, seen)
		}
		cp := serviceOwners(t, ctx, store, source)
		want := "2-0"
		if round == 1 {
			want = "1-0"
			first = cp
		}
		if cp.Reply.Cursor != want || cp.Event.Cursor != want {
			t.Fatal(cp)
		}
		if round > 1 && (cp.Reply.Owner == first.Reply.Owner || cp.Event.Owner == first.Event.Owner) {
			t.Fatal("owner was reused")
		}
		assertServiceStopped(t, ctx, store, source, p)
	}
	if n := storedCount(t, store.db, "futures_stream_inbox"); n != 4 {
		t.Fatal("recounted history", n)
	}
	if _, err := store.openBound(ctx, source.StreamConfig("event"), &first.Event, &p.target); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("stale CLI stole service", err)
	}
}

func TestCaptureServiceLazyStreamsAndForeignOwner(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	run, cancel := context.WithCancel(ctx)
	seen := map[string]bool{}
	err := RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "waiting_stream" {
			seen[v.Stream] = true
			if len(seen) == 2 {
				cancel()
			}
		}
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) || len(seen) != 2 || storedCount(t, store.db, "futures_stream_receivers") != 0 {
		t.Fatal(err, seen)
	}
	cp := serviceOwners(t, ctx, store, source)
	if cp.Reply.Registered || cp.Event.Registered {
		t.Fatal(cp)
	}
	for _, key := range serviceKeys(source) {
		residentAppend(t, ctx, source, key, "1-0", `{"synthetic":true}`)
	}
	run, cancel = context.WithCancel(ctx)
	seen = map[string]bool{}
	err = RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "captured" {
			seen[v.Stream] = true
			if len(seen) == 2 {
				cancel()
			}
		}
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cp = serviceOwners(t, ctx, store, source)
	if _, err = store.openBound(ctx, source.StreamConfig("event"), &cp.Event, &p.target); err != nil {
		t.Fatal(err)
	}
	if err = RunTestCaptureService(ctx, source, store, p, c, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("silently adopted another receiver", err)
	}
	assertServiceStopped(t, ctx, store, source, p)
}

func TestCaptureServiceAtomicOwnershipRollback(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	for _, key := range serviceKeys(source) {
		residentAppend(t, ctx, source, key, "1-0", `{"synthetic":true}`)
	}
	_, err := store.db.ExecContext(ctx, `CREATE FUNCTION fail_service_owner() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic owner failure'; END $$; CREATE TRIGGER fail_owner BEFORE UPDATE ON futures_capture_services FOR EACH ROW EXECUTE FUNCTION fail_service_owner()`)
	if err != nil {
		t.Fatal(err)
	}
	err = RunTestCaptureService(ctx, source, store, p, c, func(ResidentCaptureProgress) error { return nil })
	if err == nil {
		t.Fatal("write failure ignored")
	}
	serviceOwners(t, ctx, store, source)
	if n := storedCount(t, store.db, "futures_stream_receivers"); n != 0 {
		t.Fatal("untracked owner survived rollback", n)
	}
	if _, err = store.db.ExecContext(ctx, `DROP TRIGGER fail_owner ON futures_capture_services`); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	seen := map[string]bool{}
	err = RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "captured" {
			seen[v.Stream] = true
			if len(seen) == 2 {
				cancel()
			}
		}
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) || len(seen) != 2 {
		t.Fatal(err, seen)
	}
	serviceOwners(t, ctx, store, source)
}

func TestCaptureServiceLostProgressAndSourceChange(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	for _, key := range serviceKeys(source) {
		residentAppend(t, ctx, source, key, "1-0", `{"synthetic":true}`)
	}
	err := RunTestCaptureService(ctx, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "captured" {
			return errors.New("lost stdout after commit")
		}
		return nil
	})
	if err == nil {
		t.Fatal("output failure ignored")
	}
	serviceOwners(t, ctx, store, source)
	run, cancel := context.WithCancel(ctx)
	seen := map[string]bool{}
	err = RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "idle" || v.Phase == "captured" {
			seen[v.Stream] = true
			if len(seen) == 2 {
				cancel()
			}
		}
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) || storedCount(t, store.db, "futures_stream_inbox") != 2 {
		t.Fatal(err)
	}
	before := serviceOwners(t, ctx, store, source)
	changed := c
	changed.ServiceID = "different-service"
	if err = RunTestCaptureService(ctx, source, store, p, changed, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal(err)
	}
	if after := serviceOwners(t, ctx, store, source); after != before {
		t.Fatal("changed service adopted old state")
	}
	if err = source.reader.client.Set(ctx, p.prefix()+":cmd.trade:transport-epoch", "changed-epoch", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err = RunTestCaptureService(ctx, source, store, p, c, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal(err)
	}
	assertServiceStopped(t, ctx, store, source, p)
}

func TestCaptureServiceExplicitLegacyAdoption(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	keys := serviceKeys(source)
	for _, role := range []string{"reply", "event"} {
		residentAppend(t, ctx, source, keys[role], "1-0", `{"legacy":true}`)
		h, err := store.openBound(ctx, source.StreamConfig(role), &HeartbeatCheckpoint{Cursor: "0-0"}, &p.target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.ReadOnce(ctx, store, h, 10); err != nil {
			t.Fatal(err)
		}
	}
	err := RunTestCaptureService(ctx, source, store, p, c, func(ResidentCaptureProgress) error { return nil })
	if !errors.Is(err, ErrStreamOwner) || storedCount(t, store.db, "futures_capture_services") != 0 {
		t.Fatal("adopted legacy without checkpoint", err)
	}
	c.InitialCheckpoints.Reply, _ = store.captureCheckpoint(ctx, source.StreamConfig("reply"))
	c.InitialCheckpoints.Event, _ = store.captureCheckpoint(ctx, source.StreamConfig("event"))
	bad := c
	bad.InitialCheckpoints.Event.Cursor = "0-0"
	if err = RunTestCaptureService(ctx, source, store, p, bad, func(ResidentCaptureProgress) error { return nil }); !errors.Is(err, ErrStreamCursor) {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	seen := map[string]bool{}
	err = RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.Phase == "idle" {
			seen[v.Stream] = true
			if len(seen) == 2 {
				cancel()
			}
		}
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) || len(seen) != 2 {
		t.Fatal(err, seen)
	}
	if storedCount(t, store.db, "futures_stream_inbox") != 2 {
		t.Fatal("legacy replay counted again")
	}
}

func TestCaptureServiceProjectsOnlyLocalEvents(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	r := residentProjectionCommand(t, ctx, store.db, source.scope, p.target)
	keys := serviceKeys(source)
	residentAppend(t, ctx, source, keys["event"], "1-0", string(assessmentOrderBody(t, r, p.target, nil)))
	c.ProjectLocalEvents = true
	run, cancel := context.WithCancel(ctx)
	saw := false
	err := RunTestCaptureService(run, source, store, p, c, func(v ResidentCaptureProgress) error {
		if v.LocalProjection != nil && v.LocalProjection.Applied == 1 {
			saw = true
			cancel()
		}
		return nil
	})
	cancel()
	if !saw || !errors.Is(err, context.Canceled) || storedCount(t, store.db, "futures_order_states") != 1 {
		t.Fatal(err, saw)
	}
	serviceOwners(t, ctx, store, source)
	assertServiceStopped(t, ctx, store, source, p)
}

func TestCaptureServiceCLICrashResume(t *testing.T) {
	ctx, source, store, p, c := captureServiceFixture(t)
	dir := t.TempDir()
	bin, config := filepath.Join(dir, "ctp-worker"), filepath.Join(dir, "capture.json")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "../../cmd/relay-ctp-worker")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("worker build: %v %s", err, out)
	}
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	pg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dsn := "host=" + pg.Host + " port=" + strconv.Itoa(int(pg.Port)) + " user=" + pg.User + " dbname=" + c.Database + " sslmode=disable"
	keys := serviceKeys(source)
	for _, key := range keys {
		residentAppend(t, ctx, source, key, "1-0", `{"synthetic":true}`)
	}
	for round := 1; round <= 2; round++ {
		if round == 2 {
			for _, key := range keys {
				residentAppend(t, ctx, source, key, "2-0", `{"synthetic_late":true}`)
			}
		}
		cmd := exec.Command(bin, "--config", config, "--run")
		cmd.Env = append(os.Environ(), "RELAY_CTP_CAPTURE_DATABASE_URL="+dsn, "RELAY_CTP_CAPTURE_REDIS_URL=unix://"+source.reader.client.Options().Addr)
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		cmd.Stdout = io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		waited := false
		t.Cleanup(func() {
			if !waited {
				_ = cmd.Process.Kill()
				<-done
			}
		})
		want := strconv.Itoa(round) + "-0"
		for {
			select {
			case err := <-done:
				waited = true
				t.Fatalf("worker exited early: %v %s", err, diagnostics.String())
			default:
			}
			var n int
			err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM futures_stream_receivers WHERE cursor_id=$1`, want).Scan(&n)
			if err != nil {
				t.Fatal(err)
			}
			if n == 2 {
				break
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			time.Sleep(20 * time.Millisecond)
		}
		if round == 1 {
			err = cmd.Process.Kill()
		} else {
			err = cmd.Process.Signal(syscall.SIGTERM)
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-done:
			waited = true
		case <-ctx.Done():
			t.Fatal("worker did not stop")
		}
		if round == 2 && err != nil {
			t.Fatalf("SIGTERM: %v %s", err, diagnostics.String())
		}
		serviceOwners(t, ctx, store, source)
	}
	if n := storedCount(t, store.db, "futures_stream_inbox"); n != 4 {
		t.Fatal(n)
	}
	assertServiceStopped(t, ctx, store, source, p)
}
