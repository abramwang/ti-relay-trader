package futures

import (
	"bytes"
	"context"
	"crypto/rand"
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

func TestReplyReceiveCLIRealRedis(t *testing.T) {
	admin, _, h, client, _, ctx := realStreamRole(t, "reply")
	pg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil || !strings.HasPrefix(pg.Host, "/tmp/relay-futures-pg.") {
		t.Fatal("CLI requires disposable local PG")
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "relay_reply_" + hex.EncodeToString(nonce[:]) + "_test"
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	pg.Database = name
	db := stdlib.OpenDB(*pg)
	t.Cleanup(func() {
		db.Close()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(c, "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	})
	for _, migration := range futuresTestMigrations {
		migrateFuturesTest(t, db, ctx, migration, "up")
	}
	dest := url.URL{Scheme: "postgres", User: url.User(pg.User), Path: "/" + name, RawQuery: url.Values{"host": {pg.Host}, "port": {strconv.Itoa(int(pg.Port))}, "sslmode": {"disable"}}.Encode()}
	cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
	if err != nil {
		t.Fatal("TEST config unavailable")
	}
	deployment := "reply-cli-synthetic"
	source, err := NewTestStreamSource(TestSourceConfig{Scope: h.config.Scope, DeploymentID: deployment, RedisURL: cfg.Redis.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := publicationTarget()
	target.SourceID = source.String()
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "run_id:") {
			target.ServerRunID = strings.TrimPrefix(line, "run_id:")
		}
	}
	prefix := strings.TrimSuffix(h.stream, ":reply")
	cmdKey, epochKey := prefix+":cmd.query", prefix+":cmd.trade:transport-epoch"
	if n, err := client.Exists(ctx, cmdKey, epochKey).Result(); err != nil || n != 0 {
		t.Fatal("occupied fixture keys", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(c, cmdKey, epochKey).Err(); err != nil {
			t.Error(err)
		}
	})
	if err = client.XGroupCreateMkStream(ctx, cmdKey, target.Group, "0-0").Err(); err != nil {
		t.Fatal(err)
	}
	if err = client.Expire(ctx, cmdKey, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := client.SetNX(ctx, epochKey, target.Epoch, time.Hour).Result(); err != nil || !ok {
		t.Fatal(err)
	}
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "target.json")
	raw, _ := json.Marshal(target)
	if err = os.WriteFile(targetFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "relay-ctp-replies")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "../../cmd/relay-ctp-replies")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	scope := h.config.Scope
	r := scope.Route
	args := []string{"--env", "test", "--broker", r.BrokerID, "--gateway", r.GatewayID, "--account", r.AccountID, "--native-broker", scope.NativeBrokerID, "--user", scope.UserID, "--investor", scope.InvestorID, "--database", name, "--deployment-id", deployment, "--target-file", targetFile, "--duration", "10s"}
	call := func(extra ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, append(append([]string{}, args...), extra...)...)
		cmd.Env = append(os.Environ(), "RELAY_CTP_REPLY_DATABASE_URL="+dest.String(), "RELAY_CTP_REPLY_REDIS_URL="+cfg.Redis.URL)
		var out, diag bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diag
		err := cmd.Run()
		if strings.Contains(out.String()+diag.String(), cfg.Redis.URL) || strings.Contains(out.String()+diag.String(), dest.String()) {
			t.Fatal("connection string leaked")
		}
		return out.Bytes(), err
	}
	status := func() HeartbeatCheckpoint {
		t.Helper()
		raw, err := call("--status")
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Checkpoint HeartbeatCheckpoint `json:"checkpoint"`
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v.Checkpoint
	}
	if cp := status(); cp.Registered {
		t.Fatal("status created receiver")
	}
	if _, err = call("--capture", "--persist-test-replies"); err != nil {
		t.Fatal("capture failed", err)
	}
	cp := status()
	if cp.Records != 1 || cp.Cursor != "1-0" {
		t.Fatal(cp)
	}
	if _, err = call("--capture", "--persist-test-replies"); err == nil {
		t.Fatal("implicit takeover allowed")
	}
	if status() != cp {
		t.Fatal("rejected takeover changed checkpoint")
	}
	if _, err = call("--capture", "--persist-test-replies", "--previous-owner", cp.Owner, "--cursor", cp.Cursor, "--confirm-stopped"); err != nil {
		t.Fatal(err)
	}
	last := status()
	if last.Records != 1 || last.Cursor != cp.Cursor || last.Owner == cp.Owner {
		t.Fatal("continuation duplicated", last)
	}
	if n, err := client.XLen(ctx, cmdKey).Result(); err != nil || n != 0 {
		t.Fatal("CLI sent commands", err)
	}
	if storedCount(t, db, "futures_query_batches") != 0 {
		t.Fatal("raw mode invented query")
	}
}
