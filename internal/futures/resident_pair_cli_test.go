package futures

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestResidentPairCLIIsolatedRedisAndPG(t *testing.T) {
	ctx, source, existing, verifier, _ := residentFixture(t)
	pg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil || !strings.HasPrefix(pg.Host, "/tmp/relay-futures-pg.") {
		t.Fatal("CLI requires disposable local PG")
	}
	name := "relay_pair_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_test"
	if _, err := existing.db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	pg.Database = name
	db := stdlib.OpenDB(*pg)
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
	dir := t.TempDir()
	targetPath, cpPath := filepath.Join(dir, "target.json"), filepath.Join(dir, "checkpoints.json")
	raw, _ := json.Marshal(verifier.target)
	if err := os.WriteFile(targetPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cp := ResidentCheckpoints{Reply: HeartbeatCheckpoint{Cursor: "0-0"}, Event: HeartbeatCheckpoint{Cursor: "0-0"}}
	writeCP := func() {
		t.Helper()
		raw, _ := json.Marshal(cp)
		if err := os.WriteFile(cpPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeCP()
	binary := filepath.Join(dir, "relay-ctp-replies")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "../../cmd/relay-ctp-replies")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	scope := source.scope
	r := scope.Route
	fixture := sourceFixture()
	args := []string{"--env", "test", "--broker", r.BrokerID, "--gateway", r.GatewayID, "--account", r.AccountID, "--native-broker", scope.NativeBrokerID, "--user", scope.UserID, "--investor", scope.InvestorID, "--database", name, "--deployment-id", fixture.DeploymentID, "--target-file", targetPath, "--duration", "1s", "--stream-role", "both"}
	redisURL := "unix://" + source.reader.client.Options().Addr
	dsn := "host=" + pg.Host + " port=" + strconv.Itoa(int(pg.Port)) + " user=" + pg.User + " dbname=" + name + " sslmode=disable"
	call := func(extra ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, append(append([]string{}, args...), extra...)...)
		cmd.Env = append(os.Environ(), "RELAY_CTP_REPLY_DATABASE_URL="+dsn, "RELAY_CTP_REPLY_REDIS_URL="+redisURL)
		var out, diag bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diag
		err := cmd.Run()
		if strings.Contains(out.String()+diag.String(), dsn) || strings.Contains(out.String()+diag.String(), redisURL) {
			t.Fatal("connection string leaked")
		}
		if err != nil {
			t.Log("CLI stopped:", diag.String())
		}
		return out.Bytes(), err
	}
	status := func() ResidentCheckpoints {
		t.Helper()
		raw, err := call("--status")
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Checkpoints ResidentCheckpoints `json:"checkpoints"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v.Checkpoints
	}
	if got := status(); got.Reply.Registered || got.Event.Registered {
		t.Fatal(got)
	}
	for _, role := range []string{"reply", "event"} {
		key, _ := source.StreamConfig(role).stream()
		residentAppend(t, ctx, source, key, "1-0", `{"synthetic":true,"role":"`+role+`"}`)
	}
	runArgs := []string{"--follow", "--capture", "--persist-test-replies", "--persist-test-events", "--checkpoints-file", cpPath}
	expectedMode := "test_resident_raw_capture"
	checkProgress := func(raw []byte) {
		t.Helper()
		d := json.NewDecoder(bytes.NewReader(raw))
		stopped := map[string]bool{}
		for {
			var p ResidentCaptureProgress
			err := d.Decode(&p)
			if err == io.EOF {
				break
			}
			if err != nil || p.BusinessAuthorized || p.Mode != expectedMode {
				t.Fatal("invalid or interleaved progress", string(raw), err)
			}
			if p.Phase == "stopped" {
				if !p.CheckpointVerified || p.Checkpoint.Owner == "" || stopped[p.Stream] {
					t.Fatal(p)
				}
				stopped[p.Stream] = true
			}
		}
		if len(stopped) != 2 {
			t.Fatal("missing final checkpoints", string(raw))
		}
	}
	out, err := call(runArgs...)
	if err != nil {
		t.Fatal(string(out), err)
	}
	checkProgress(out)
	got := status()
	if !got.Reply.Registered || !got.Event.Registered || got.Reply.Records != 1 || got.Event.Records != 1 {
		t.Fatal(got, string(out))
	}
	if _, err := call(runArgs...); err == nil {
		t.Fatal("implicit takeover accepted")
	}
	if again := status(); again != got {
		t.Fatal("failed preflight changed ownership", again, got)
	}
	cp = got
	writeCP()
	for _, role := range []string{"reply", "event"} {
		key, _ := source.StreamConfig(role).stream()
		residentAppend(t, ctx, source, key, "2-0", `{"synthetic":true,"resumed":true}`)
	}
	out, err = call(append(runArgs, "--confirm-stopped")...)
	if err != nil {
		t.Fatal(string(out), err)
	}
	checkProgress(out)
	got = status()
	if got.Reply.Records != 2 || got.Event.Records != 2 || got.Reply.Owner == cp.Reply.Owner || got.Event.Owner == cp.Event.Owner {
		t.Fatal(got)
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM futures_stream_inbox WHERE state='pending'`).Scan(&pending); err != nil || pending != 4 {
		t.Fatal(pending, err)
	}
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if n, err := source.reader.client.XLen(ctx, verifier.prefix()+":"+suffix).Result(); err != nil || n != 0 {
			t.Fatal("receiver sent commands", n, err)
		}
	}
	// The same executable explicitly projects only a confirmed local event.
	registration := residentProjectionCommand(t, ctx, db, scope, verifier.target)
	key, _ := source.StreamConfig("event").stream()
	residentAppend(t, ctx, source, key, "3-0", string(assessmentOrderBody(t, registration, verifier.target, nil)))
	cp = got
	writeCP()
	expectedMode = "test_resident_local_event_projection"
	out, err = call(append(runArgs, "--confirm-stopped", "--project-local-events")...)
	if err != nil {
		t.Fatal(string(out), err)
	}
	checkProgress(out)
	if storedCount(t, db, "futures_order_states") != 1 {
		t.Fatal("CLI failed to project local order", string(out))
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM futures_stream_inbox WHERE state='pending'`).Scan(&pending); err != nil || pending != 4 {
		t.Fatal("unrelated raw records were claimed", pending, err)
	}
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if n, err := source.reader.client.XLen(ctx, verifier.prefix()+":"+suffix).Result(); err != nil || n != 0 {
			t.Fatal("projection sent command", n, err)
		}
	}
}
