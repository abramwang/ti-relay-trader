package futures

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func storageMetric(t *testing.T, metrics []StorageMetric, name string) StorageMetric {
	t.Helper()
	for _, m := range metrics {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("missing metric %s: %+v", name, metrics)
	return StorageMetric{}
}

func TestStorageInspectionThresholdsAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		used, limit int64
		state       string
	}{{0, 100, "ok"}, {79, 100, "ok"}, {80, 100, "warning"}, {89, 100, "warning"}, {90, 100, "critical"},
		{101, 100, "critical"}, {math.MaxInt64, math.MaxInt64, "critical"}, {0, 0, "unknown"}, {-1, 100, "unknown"}, {4, 5, "warning"}} {
		if m := storageCapacity("test", tc.used, tc.limit); m.State != tc.state {
			t.Fatal(tc, m)
		}
	}
	stat := syscall.Statfs_t{Bsize: 4096, Blocks: 100, Bavail: 10, Files: 100, Ffree: 20}
	metrics := localStorageMetrics("/explicit", stat)
	if metrics[0].State != "critical" || *metrics[0].Value != 40960 || metrics[1].State != "warning" || *metrics[1].Value != 20 {
		t.Fatal(metrics)
	}
	stat.Blocks = math.MaxUint64
	if localStorageMetrics("/explicit", stat)[0].State != "unknown" {
		t.Fatal("overflow accepted")
	}
	stat.Blocks, stat.Files = 100, 0
	if localStorageMetrics("/explicit", stat)[1].Value != nil {
		t.Fatal("missing inode statistics became zero")
	}
	for _, p := range [][]string{{"relative"}, {"/a/../b"}, {"/a", "/a"}, {"/bad\npath"}, {"/1", "/2", "/3", "/4", "/5", "/6", "/7", "/8", "/9"}} {
		if ValidateStoragePaths(p) == nil {
			t.Fatal("unsafe path list accepted", p)
		}
	}
}

func TestStorageInspectionRedisSemantics(t *testing.T) {
	info := "# Server\r\nrun_id:expected\r\nused_memory:90\r\nmaxmemory:100\r\nused_memory_rss:110\r\nmaxmemory_policy:noeviction\r\naof_enabled:1\r\naof_last_write_status:ok\r\nevicted_keys:12326\r\nexpired_keys:307\r\n"
	metrics := redisStorageMetrics(info, "expected")
	if storageMetric(t, metrics, "redis.used_memory_bytes").State != "critical" || storageMetric(t, metrics, "redis.eviction_policy").State != "ok" {
		t.Fatal(metrics)
	}
	if m := storageMetric(t, metrics, "redis.evicted_keys_total"); m.State != "warning" || !strings.Contains(m.Detail, "not_current_eviction_rate") {
		t.Fatal(m)
	}
	for _, tc := range []struct{ old, new, metric, state string }{
		{"maxmemory:100", "maxmemory:0", "redis.used_memory_bytes", "unknown"},
		{"used_memory:90", "used_memory:-1", "redis.used_memory_bytes", "unknown"},
		{"used_memory:90", "used_memory:9223372036854775808", "redis.used_memory_bytes", "unknown"},
		{"used_memory:90\r\n", "", "redis.used_memory_bytes", "unknown"},
		{"maxmemory_policy:noeviction", "maxmemory_policy:allkeys-lru", "redis.eviction_policy", "critical"},
		{"aof_last_write_status:ok", "aof_last_write_status:err", "redis.aof", "critical"},
		{"aof_enabled:1", "aof_enabled:0", "redis.aof", "warning"},
		{"aof_last_write_status:ok\r\n", "", "redis.aof", "unknown"},
	} {
		m := storageMetric(t, redisStorageMetrics(strings.ReplaceAll(info, tc.old, tc.new), "expected"), tc.metric)
		if m.State != tc.state {
			t.Fatal(tc, m)
		}
	}
	for _, bad := range []string{strings.ReplaceAll(info, "expected", "other"), info + "run_id:expected\r\n"} {
		m := redisStorageMetrics(bad, "expected")
		if len(m) != 1 || m[0].State != "unknown" || m[0].Value != nil {
			t.Fatal(m)
		}
	}
}

func TestStorageInspectionReadOnlyPGAndRedis(t *testing.T) {
	ctx, source, store, _, c := captureServiceFixture(t)
	db := store.db
	_, h := openStream(t, db, ctx, source.StreamConfig("reply"))
	captureStream(t, store, ctx, h, "0-0", StreamInput{"1-0", []byte(`{"audit":"unprocessed"}`)})
	// Synthetic capacity counters exercise warning semantics without allocating
	// 50,000 raw observations. Inspection does not certify their body integrity.
	scope := TradeScope{Route: c.Scope.Route, NativeBrokerID: c.Scope.NativeBrokerID, InvestorID: c.Scope.InvestorID, TradeDate: c.Target.TradeDate}
	raw, _ := json.Marshal(scope)
	if _, err := db.ExecContext(ctx, `INSERT INTO futures_trade_scopes(scope_key,context_json,audit_count,audit_bytes) VALUES($1,$2,40000,0)`, tradeScopeKey(scope), raw); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		t.Helper()
		var parts []string
		for _, table := range []string{"futures_commands", "futures_command_work", "futures_stream_receivers", "futures_stream_inbox", "futures_trade_scopes", "futures_capture_services", "futures_capture_health"} {
			var value string
			if err := db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM `+table+` t`).Scan(&value); err != nil {
				t.Fatal(err)
			}
			parts = append(parts, table+":"+value)
		}
		keys, err := source.reader.client.Keys(ctx, "*").Result()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, err := source.reader.client.Dump(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, key+":"+value)
		}
		return strings.Join(parts, "\n")
	}
	before := fingerprint()
	path := t.TempDir()
	report, err := InspectTestStorage(ctx, db, source, c, []string{path, filepath.Join(path, "missing")})
	if err != nil || report.BusinessAuthorized || report.AtomicSnapshot || report.ConfiguredTradeDate != c.Target.TradeDate {
		t.Fatal(report, err)
	}
	if m := storageMetric(t, report.Metrics, "reply.backlog_count"); m.Value == nil || *m.Value != 1 {
		t.Fatal(m)
	}
	if storageMetric(t, report.Metrics, "event.backlog").State != "not_observed" || storageMetric(t, report.Metrics, "day_ledger.audit_count").State != "warning" {
		t.Fatal(report)
	}
	for _, name := range []string{"postgres.volume_available_bytes", "redis.volume_available_bytes", "local_volume:" + filepath.Join(path, "missing")} {
		if m := storageMetric(t, report.Metrics, name); m.State != "unknown" || m.Value != nil {
			t.Fatal(m)
		}
	}
	if fingerprint() != before {
		t.Fatal("inspection changed evidence, owner, cursor or Redis keys")
	}
	wrong := c
	wrong.Database = "another_test"
	report, err = InspectTestStorage(ctx, db, source, wrong, nil)
	if err != nil || storageMetric(t, report.Metrics, "postgres").Detail != "scope_or_database_mismatch" {
		t.Fatal(report, err)
	}
	wrong = c
	wrong.Target.ServerRunID = strings.Repeat("e", 40)
	report, err = InspectTestStorage(ctx, db, source, wrong, nil)
	if err != nil || storageMetric(t, report.Metrics, "redis").Detail != "server_run_id_mismatch" {
		t.Fatal(report, err)
	}
	wrong = c
	wrong.Target.TradeDate = "20261130"
	report, err = InspectTestStorage(ctx, db, source, wrong, nil)
	if err != nil || storageMetric(t, report.Metrics, "day_ledger").State != "not_observed" {
		t.Fatal("used another day's ledger", report, err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE futures_command_work RENAME TO missing_migration`); err != nil {
		t.Fatal(err)
	}
	report, err = InspectTestStorage(ctx, db, source, c, nil)
	if err != nil || storageMetric(t, report.Metrics, "postgres").State != "unknown" {
		t.Fatal("missing schema reported zero/healthy", report, err)
	}
	closed, cancel := context.WithCancel(ctx)
	cancel()
	report, err = InspectTestStorage(closed, db, source, c, nil)
	if err != nil || storageMetric(t, report.Metrics, "redis").State != "unknown" || storageMetric(t, report.Metrics, "postgres").State != "unknown" {
		t.Fatal(report, err)
	}
}

func TestStorageInspectionWorkerCLI(t *testing.T) {
	ctx, source, store, _, c := captureServiceFixture(t)
	dir := t.TempDir()
	raw, _ := json.Marshal(c)
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "worker")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/relay-ctp-worker")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	// The fixture database uses the disposable PG's endpoint and credentials.
	dsn := os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL")
	command := exec.CommandContext(ctx, binary, "--config", configPath, "--storage-check", "--storage-path", dir)
	command.Env = append(os.Environ(), "RELAY_CTP_CAPTURE_REDIS_URL=unix://"+source.reader.client.Options().Addr,
		"RELAY_CTP_CAPTURE_DATABASE_URL="+dsn+" dbname="+c.Database)
	out, err := command.CombinedOutput()
	var report StorageInspection
	if err != nil || json.Unmarshal(out, &report) != nil || report.Mode != "read_only_storage_inspection" {
		t.Fatalf("CLI failed: %v %s", err, out)
	}
	if storageMetric(t, report.Metrics, "postgres.database_bytes").Value == nil || storedCount(t, store.db, "futures_capture_services") != 0 || storedCount(t, store.db, "futures_stream_receivers") != 0 {
		t.Fatal("inspection failed or acquired receiver", report)
	}
	if report.CompletedAt.Before(report.StartedAt) || time.Since(report.CompletedAt) > time.Minute {
		t.Fatal("missing sample times", report)
	}
}

func TestStorageInspectionBacklogVersusHistory(t *testing.T) {
	f := projectionReplyFixture(t, "positions")
	c := CaptureServiceConfig{Scope: f.c.Scope, Target: f.p.target}
	if err := f.db.QueryRowContext(f.ctx, `SELECT current_database()`).Scan(&c.Database); err != nil {
		t.Fatal(err)
	}
	f.append(t, f.raw)
	metrics, err := inspectPostgresStorage(f.ctx, f.db, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"commands.pending_count", "reply.backlog_count", "reply.retained_count"} {
		if m := storageMetric(t, metrics, name); m.Value == nil || *m.Value != 1 {
			t.Fatal(m)
		}
	}
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.Delivered != 1 {
		t.Fatal(page, err)
	}
	metrics, err = inspectPostgresStorage(f.ctx, f.db, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"commands.pending_count", "reply.backlog_count"} {
		if m := storageMetric(t, metrics, name); m.Value == nil || *m.Value != 0 {
			t.Fatal("history still consumes work capacity", m)
		}
	}
	if m := storageMetric(t, metrics, "reply.retained_count"); *m.Value != 1 || m.Limit != nil {
		t.Fatal("lost history or gave history a quota", m)
	}
	wrong := c
	wrong.Target.SourceID = "other-source"
	if _, err := inspectPostgresStorage(f.ctx, f.db, wrong); err != ErrStreamIdentity {
		t.Fatal("accepted a different receiver scope", err)
	}
}
