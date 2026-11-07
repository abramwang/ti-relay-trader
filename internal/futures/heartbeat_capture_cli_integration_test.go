package futures

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"ti-relay-trader/internal/config"
)

func TestHeartbeatCaptureCLIRealRedisAndDisposableDatabase(t *testing.T) {
	admin, _, h, _, _, ctx := realStreamRole(t, "hb")
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "relay_capture_" + hex.EncodeToString(nonce[:]) + "_test"
	pg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("bad temporary DSN")
	}
	pg.Database = name
	// pgx.ConnString retains the original text even when Config.Database changes.
	// Build the subprocess DSN structurally for this disposable socket database.
	if !strings.HasPrefix(pg.Host, "/tmp/relay-futures-pg.") {
		t.Fatal("CLI test requires the disposable local PostgreSQL harness")
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	destination := url.URL{Scheme: "postgres", User: url.User(pg.User), Path: "/" + name}
	query := url.Values{"host": {pg.Host}, "port": {strconv.Itoa(int(pg.Port))}, "sslmode": {"disable"}}
	destination.RawQuery = query.Encode()
	cliDSN := destination.String()
	db := stdlib.OpenDB(*pg)
	t.Cleanup(func() {
		db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP DATABASE "+name); err != nil {
			t.Error("temporary capture database cleanup failed", err)
		}
	})
	for _, migration := range futuresTestMigrations {
		migrateFuturesTest(t, db, ctx, migration, "up")
	}
	cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
	if err != nil {
		t.Fatal("bad test config")
	}
	binary := filepath.Join(t.TempDir(), "relay-ctp-capture")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "../../cmd/relay-ctp-capture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	route := h.config.Scope.Route
	args := []string{"--env", "test", "--broker", route.BrokerID, "--gateway", route.GatewayID, "--account", route.AccountID, "--database", name, "--deployment-id", "synthetic-cli-integration", "--timeout", "10s"}
	redisURL := cfg.Redis.URL
	call := func(extra ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, append(append([]string{}, args...), extra...)...)
		cmd.Env = append(os.Environ(), "RELAY_CTP_CAPTURE_REDIS_URL="+redisURL, "RELAY_CTP_CAPTURE_DATABASE_URL="+cliDSN)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if strings.Contains(stderr.String()+stdout.String(), cfg.Redis.URL) || strings.Contains(stderr.String()+stdout.String(), cliDSN) {
			t.Fatal("CLI exposed connection strings")
		}
		if err != nil {
			t.Log("capture command diagnostic:", stderr.String())
		}
		return stdout.Bytes(), err
	}
	readStatus := func() HeartbeatCheckpoint {
		t.Helper()
		raw, err := call("--status")
		if err != nil {
			t.Fatal("status command failed", err)
		}
		var status struct {
			Checkpoint HeartbeatCheckpoint `json:"checkpoint"`
			Authorized bool                `json:"business_authorized"`
		}
		if err := json.Unmarshal(raw, &status); err != nil || status.Authorized {
			t.Fatal("bad status output", err)
		}
		return status.Checkpoint
	}
	redisURL = "redis://127.0.0.1:0/0"
	if v := readStatus(); v.Registered || storedCount(t, db, "futures_stream_receivers") != 0 {
		t.Fatal("status changed receiver")
	}
	redisURL = cfg.Redis.URL
	raw, err := call("--run", "--persist-test-audit")
	if err != nil {
		t.Fatal("capture failed", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var opened, complete HeartbeatCaptureProgress
	if decoder.Decode(&opened) != nil || decoder.Decode(&complete) != nil || opened.Phase != "opened" || complete.Phase != "idle" || complete.Captured != 1 || complete.BusinessAuthorized {
		t.Fatal("bad progress report")
	}
	first := readStatus()
	if first.Records != 1 || first.Cursor != "1-0" {
		t.Fatal(first)
	}
	auditArgs := []string{"--audit", "--audit-owner", first.Owner, "--audit-cursor", first.Cursor}
	auditRaw, err := call(auditArgs...)
	if err != nil {
		t.Fatal("audit failed", err)
	}
	var audit HeartbeatEvidencePage
	if err := json.Unmarshal(auditRaw, &audit); err != nil || len(audit.Records) != 1 || audit.Records[0].BodyBase64 != nil || audit.BusinessAuthorized || audit.Checkpoint != first {
		t.Fatal("bad audit output", err)
	}
	auditRaw, err = call(append(auditArgs, "--include-raw")...)
	if err != nil {
		t.Fatal("raw audit failed", err)
	}
	if err := json.Unmarshal(auditRaw, &audit); err != nil || audit.Records[0].BodyBase64 == nil {
		t.Fatal("raw audit missing", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(*audit.Records[0].BodyBase64)
	if err != nil || string(decoded) != "synthetic seed" {
		t.Fatal("raw bytes changed", err)
	}
	if current := readStatus(); current != first {
		t.Fatal("audit altered checkpoint")
	}
	if _, err := call("--run", "--persist-test-audit"); err == nil {
		t.Fatal("implicit takeover accepted")
	}
	if current := readStatus(); current != first {
		t.Fatal("failed startup changed owner")
	}
	if _, err := call("--run", "--persist-test-audit", "--resume-owner", first.Owner, "--resume-cursor", first.Cursor, "--confirm-stopped"); err != nil {
		t.Fatal("explicit resume failed", err)
	}
	last := readStatus()
	if last.Owner == first.Owner || last.Records != first.Records || last.Cursor != first.Cursor {
		t.Fatal("resume duplicated evidence", last)
	}
	if _, err := call(auditArgs...); err == nil {
		t.Fatal("old audit anchor accepted after takeover")
	}
	if storedCount(t, db, "futures_event_inbox") != 0 || storedCount(t, db, "futures_query_batches") != 0 {
		t.Fatal("CLI entered business processing")
	}
}
