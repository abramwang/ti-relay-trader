package futures

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// These fault tests never accept an external Redis URL or read application
// configuration. Only a freshly spawned process on a private Unix socket is used.
type localStorageRedis struct {
	binary, dir, socket string
	client              *redis.Client
	cmd                 *exec.Cmd
	done                chan error
}

func newLocalStorageRedis(t *testing.T) *localStorageRedis {
	t.Helper()
	binary := os.Getenv("RELAY_FUTURES_LOCAL_REDIS_SERVER")
	if binary == "" {
		t.Skip("set RELAY_FUTURES_LOCAL_REDIS_SERVER for isolated Redis process tests")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("Redis binary must be an explicit absolute path")
	}
	dir, err := os.MkdirTemp("", "relay-redis-")
	if err != nil {
		t.Fatal(err)
	}
	r := &localStorageRedis{binary: binary, dir: dir, socket: filepath.Join(dir, "r.sock")}
	t.Cleanup(func() {
		r.stop(t)
		if r.client != nil {
			r.client.Close()
		}
		os.RemoveAll(dir)
	})
	r.client = redis.NewClient(&redis.Options{Network: "unix", Addr: r.socket, Protocol: 2, MaxRetries: -1, DialerRetries: 1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, ContextTimeoutEnabled: true})
	r.start(t)
	info, err := r.client.Info(context.Background(), "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	t.Log("isolated Redis version", redisStorageField(info, "redis_version"))
	return r
}

func (r *localStorageRedis) start(t *testing.T) {
	t.Helper()
	if r.cmd != nil {
		t.Fatal("local Redis already running")
	}
	r.cmd = exec.Command(r.binary, "--port", "0", "--unixsocket", r.socket, "--unixsocketperm", "700",
		"--dir", r.dir, "--appendonly", "yes", "--appendfsync", "everysec", "--save", "",
		"--maxmemory-policy", "noeviction", "--daemonize", "no", "--logfile", filepath.Join(r.dir, "redis.log"))
	if err := r.cmd.Start(); err != nil {
		r.cmd = nil
		t.Fatal(err)
	}
	r.done = make(chan error, 1)
	cmd := r.cmd
	go func() { r.done <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			r.cmd = nil
			log, _ := os.ReadFile(filepath.Join(r.dir, "redis.log"))
			t.Fatalf("isolated Redis exited: %v\n%s", err, log)
		default:
		}
		_, socketErr := os.Stat(r.socket)
		if socketErr == nil && r.client.Ping(context.Background()).Err() == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("isolated Redis did not become ready")
}

func (r *localStorageRedis) stop(t *testing.T) {
	t.Helper()
	if r.cmd == nil {
		return
	}
	if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Error(err)
	}
	select {
	case err := <-r.done:
		if err != nil {
			t.Error("isolated Redis shutdown", err)
		}
	case <-time.After(5 * time.Second):
		r.cmd.Process.Kill()
		<-r.done
		t.Error("isolated Redis forced shutdown")
	}
	r.cmd = nil
}

func redisStorageField(info, key string) string {
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimPrefix(line, key+":")
		}
	}
	return ""
}

func localStorageLimit(t *testing.T, c *redis.Client, limit string) {
	t.Helper()
	if err := c.ConfigSet(context.Background(), "maxmemory", limit).Err(); err != nil {
		t.Fatal(err)
	}
}

func localStoragePublisher(t *testing.T, r *localStorageRedis) *TestQueryPublisher {
	t.Helper()
	ctx := context.Background()
	scope := intentVectors(t)["account.asset.query"].scope()
	target := publicationTarget()
	info, err := r.client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	target.ServerRunID = redisStorageField(info, "run_id")
	cfg := TestSourceConfig{Scope: scope, DeploymentID: "isolated-storage-test", RedisURL: "unix://" + r.socket}
	source, err := NewTestStreamSource(cfg)
	if err != nil {
		t.Fatal(err)
	}
	target.SourceID = source.String()
	source.Close()
	p, err := NewTestQueryPublisher(cfg, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if err := r.client.XGroupCreateMkStream(ctx, p.prefix()+":"+suffix, target.Group, "0-0").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.client.Set(ctx, p.prefix()+":cmd.trade:transport-epoch", target.Epoch, 0).Err(); err != nil {
		t.Fatal(err)
	}
	body := editBody(t, readOnlyScenarios(t)[0].Heartbeats[1], func(m map[string]any) {
		m["routing"], m["counter_session_id"], m["produced_at"] = scope.Route, target.CounterSessionID, assessmentTime(time.Now())
		payload := m["payload"].(map[string]any)
		payload["counter_session_id"], payload["trade_date"] = target.CounterSessionID, target.TradeDate
	})
	if err := r.client.XAdd(ctx, &redis.XAddArgs{Stream: p.prefix() + ":hb", Values: map[string]any{"body": string(body)}}).Err(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocalRedisNoEvictionPreservesDataAndRejectsPublication(t *testing.T) {
	r := newLocalStorageRedis(t)
	p := localStoragePublisher(t, r)
	ctx := context.Background()
	proof, err := p.inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := p.prefix() + ":retained-audit"
	raw := "original-audit\x00\xff"
	if err := r.client.Set(ctx, key, raw, 0).Err(); err != nil {
		t.Fatal(err)
	}
	// Deterministically put only this disposable instance over its limit,
	// without allocating pressure into the shared server or this host.
	localStorageLimit(t, r.client, "1")
	if err := r.client.Set(ctx, p.prefix()+":must-not-exist", "new", 0).Err(); !redis.HasErrorPrefix(err, "OOM") {
		t.Fatal("expected real Redis OOM", err)
	}
	if err := r.client.XAdd(ctx, &redis.XAddArgs{Stream: p.prefix() + ":cmd.trade", Values: map[string]any{"body": "must-not-publish"}}).Err(); !redis.HasErrorPrefix(err, "OOM") {
		t.Fatal("XADD OOM", err)
	}
	_, evalErr := r.client.Eval(ctx, testQueryAppendScript,
		[]string{p.prefix() + ":cmd.trade:transport-epoch", p.prefix() + ":hb", p.prefix() + ":cmd.query"},
		p.target.ServerRunID, p.target.Epoch, p.target.Group, proof.EntryID, string(proof.Body), proof.ExpiresAt.UnixMilli(), proof.ProducedAt.UnixMilli(), "isolated OOM probe").Result()
	if !redis.HasErrorPrefix(evalErr, "OOM") {
		t.Fatal("expected original EVAL OOM before testing error mapping", evalErr)
	}
	id, rejected, err := p.appendQuery(ctx, proof, []byte("must-not-publish"))
	if !errors.Is(err, ErrQueryPublishUnknown) || rejected || id != "" {
		t.Fatal("OOM incorrectly reported as known publication outcome", id, rejected, err)
	}
	if value, err := r.client.Get(ctx, key).Result(); err != nil || value != raw {
		t.Fatal("old evidence lost", err)
	}
	if ttl, err := r.client.PTTL(ctx, key).Result(); err != nil || ttl != -1 {
		t.Fatal("evidence expiration changed", ttl, err)
	}
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if n, err := r.client.XLen(ctx, p.prefix()+":"+suffix).Result(); err != nil || n != 0 {
			t.Fatal("unexpected append", n, err)
		}
	}
	stats, err := r.client.Info(ctx, "stats").Result()
	if err != nil || redisStorageField(stats, "evicted_keys") != "0" {
		t.Fatal("unexpected eviction", err)
	}
	localStorageLimit(t, r.client, "0")
	if err := r.client.Set(ctx, p.prefix()+":new-after-capacity-restored", "probe", 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Log("SET/XADD/EVAL rejected at limit; old bytes retained; evicted=0; publisher returned unknown")
}

type localOOMPublication struct {
	p     *TestQueryPublisher
	t     *testing.T
	calls int
}

func (p *localOOMPublication) inspect(ctx context.Context) (queryPublishProof, error) {
	return p.p.inspect(ctx)
}
func (p *localOOMPublication) appendQuery(ctx context.Context, proof queryPublishProof, raw []byte) (string, bool, error) {
	p.calls++
	localStorageLimit(p.t, p.p.client, "1")
	return p.p.appendQuery(ctx, proof, raw)
}

func TestLocalRedisOOMKeepsDurableAttemptAndNeverResends(t *testing.T) {
	r := newLocalStorageRedis(t)
	p := localStoragePublisher(t, r)
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["account.asset.query"]
	registered := registerVector(t, s, ctx, v, RegistrationEvidence{}, "registered")
	transport := &localOOMPublication{p: p, t: t}
	result, err := s.publishTestQuery(ctx, v.scope(), registered.CommandID, registered.Original.MessageID, p.target, transport)
	if !errors.Is(err, ErrQueryPublishUnknown) || result.State != "attempted" || result.Published != nil || transport.calls != 1 {
		t.Fatal(result, err, transport.calls)
	}
	localStorageLimit(t, r.client, "0")
	fresh, _ := NewCommandStore(db)
	again, err := fresh.publishTestQuery(ctx, v.scope(), registered.CommandID, registered.Original.MessageID, p.target, transport)
	if !errors.Is(err, ErrQueryPublishUnknown) || !again.Replayed || again.Published != nil || transport.calls != 1 {
		t.Fatal("capacity recovery resent command", again, err, transport.calls)
	}
	if n, err := r.client.XLen(ctx, p.prefix()+":cmd.query").Result(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	t.Log("real OOM leaves PG attempted; fresh store/capacity recovery never retries")
}

func TestLocalRedisTradePublicationOOMRemainsUnknown(t *testing.T) {
	r := newLocalStorageRedis(t)
	base := localStoragePublisher(t, r)
	ctx, now := context.Background(), time.Now()
	v, _, _, _ := assessmentFixture(t)
	row, _, _, approval := freshOCTradeHeartbeat(t, now, false)
	row.Raw = editBody(t, row.Raw, func(m map[string]any) {
		m["routing"], m["counter_session_id"] = base.scope.Route, base.target.CounterSessionID
		p := m["payload"].(map[string]any)
		p["counter_session_id"], p["trade_date"] = base.target.CounterSessionID, base.target.TradeDate
	})
	id, err := r.client.XAdd(ctx, &redis.XAddArgs{Stream: base.prefix() + ":hb", Values: map[string]any{"body": string(row.Raw)}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	row.EntryID = id
	approval.CommandSHA256 = commandDigest([]byte(v.Raw))
	publisher := &TestTradePublisher{base: base, approval: approval}
	proof, err := publisher.inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	localStorageLimit(t, r.client, "1")
	entry, rejected, err := publisher.appendQuery(ctx, proof, []byte(v.Raw))
	if !errors.Is(err, ErrQueryPublishUnknown) || entry != "" || rejected {
		t.Fatal("trade OOM reported as known result", entry, rejected, err)
	}
	if count, err := r.client.XLen(ctx, base.prefix()+":cmd.trade").Result(); err != nil || count != 0 {
		t.Fatal("OOM published trade", count, err)
	}
	localStorageLimit(t, r.client, "0")
	t.Log("real TEST trade publisher OOM remains unknown, no trade stream append; no broker involved")
}

func TestLocalRedisAOFRestartPreservesPELAndFencesOldRun(t *testing.T) {
	r := newLocalStorageRedis(t)
	p := localStoragePublisher(t, r)
	ctx := context.Background()
	proof, err := p.inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commandKey := p.prefix() + ":cmd.query"
	raw := "synthetic-persisted-body\x00\xff"
	id, err := r.client.XAdd(ctx, &redis.XAddArgs{Stream: commandKey, Values: map[string]any{"body": raw}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: p.target.Group, Consumer: "local-test-owner", Streams: []string{commandKey, ">"}, Count: 1, Block: -1}).Result(); err != nil {
		t.Fatal(err)
	}
	if err := r.client.Set(ctx, p.prefix()+":receiver-cursor", id, 0).Err(); err != nil {
		t.Fatal(err)
	}
	// Wait for AOF fsync evidence, then perform a controlled process restart.
	// This proves neither power-loss durability nor the shared server's mount.
	values, err := r.client.Do(ctx, "WAITAOF", 1, 0, 3000).Slice()
	if err != nil || len(values) != 2 || values[0] != int64(1) {
		t.Fatal("AOF sync not confirmed", values, err)
	}
	r.stop(t)
	r.start(t)
	info, err := r.client.Info(ctx, "server").Result()
	if err != nil || redisStorageField(info, "run_id") == p.target.ServerRunID {
		t.Fatal("restart identity did not change", err)
	}
	rows, err := r.client.XRange(ctx, commandKey, "-", "+").Result()
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Values["body"] != raw {
		t.Fatal("AOF body loss", err)
	}
	for key, want := range map[string]string{p.prefix() + ":cmd.trade:transport-epoch": p.target.Epoch, p.prefix() + ":receiver-cursor": id} {
		if value, err := r.client.Get(ctx, key).Result(); err != nil || value != want {
			t.Fatal("binding/cursor loss", key, err)
		}
	}
	pending, err := r.client.XPending(ctx, commandKey, p.target.Group).Result()
	if err != nil || pending.Count != 1 || pending.Consumers["local-test-owner"] != 1 {
		t.Fatal("PEL lost", pending, err)
	}
	details, err := r.client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: commandKey, Group: p.target.Group, Start: "-", End: "+", Count: 10}).Result()
	if err != nil || len(details) != 1 || details[0].ID != id || details[0].Consumer != "local-test-owner" {
		t.Fatal("pending identity changed", details, err)
	}
	tail, err := r.client.XRevRangeN(ctx, p.prefix()+":hb", "+", "-", 1).Result()
	if err != nil || len(tail) != 1 || tail[0].ID != proof.EntryID || tail[0].Values["body"] != string(proof.Body) {
		t.Fatal("heartbeat evidence changed", err)
	}
	// Establish a new connection before checking the old binding, so a stale
	// socket error cannot masquerade as a successful run_id fencing test.
	if p.client.Ping(ctx).Err() != nil {
		if err := p.client.Ping(ctx).Err(); err != nil {
			t.Fatal(err)
		}
	}
	live, err := p.client.Info(ctx, "server").Result()
	if err != nil || redisStorageField(live, "run_id") != redisStorageField(info, "run_id") {
		t.Fatal("publisher did not reconnect to new process", err)
	}
	if _, err := p.inspect(ctx); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal("old run admitted", err)
	}
	if id, rejected, err := p.appendQuery(ctx, proof, []byte("must-not-publish")); err != nil || !rejected || id != "" {
		t.Fatal("atomic old-run gate", id, rejected, err)
	}
	stats, err := r.client.Info(ctx, "stats").Result()
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(redisStorageField(stats, "evicted_keys"))
	if err != nil || n != 0 {
		t.Fatal("unexpected eviction")
	}
	t.Log("controlled AOF restart kept bytes/IDs/epoch/cursor/PEL; old run rejected without replay")
}
