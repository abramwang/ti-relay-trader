package redisstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

func receiptLoopConfig() config.Config {
	embedded := false
	return config.Config{
		Service:  config.ServiceConfig{Environment: config.EnvironmentTest, Mode: config.ModeWorker},
		Redis:    config.RedisConfig{URL: "redis://127.0.0.1:1/0", Env: "test", BrokerID: "huaxin", GatewayID: "00030484"},
		Worker:   config.WorkerConfig{EmbeddedLedgerSync: &embedded},
		Accounts: []config.AccountRouteConfig{{AccountID: "00030484", BrokerID: "huaxin", GatewayID: "00030484", StreamPrefix: "relay:test:v1:huaxin:00030484", Enabled: true}},
	}
}

func receiptLoopManifest() LedgerReceiptManifest {
	m := LedgerReceiptManifest{Version: "ledger.receipt-worker.v1"}
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		b := receiverBinding()
		b.Role = role
		m.Streams = append(m.Streams, LedgerReceiptRegistration{Binding: b, Owner: strings.Repeat("a", 64)})
	}
	return m
}

func writeReceiptManifest(t *testing.T, m LedgerReceiptManifest) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt-manifest.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCurrentAccountBaselineReceiptManifest(t *testing.T) {
	cfg := receiptLoopConfig()
	m := receiptLoopManifest()
	cfg.Worker.LedgerReceiptManifest = writeReceiptManifest(t, m)
	if _, err := loadLedgerReceiptManifest(cfg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config, *LedgerReceiptManifest)
	}{
		{"production", func(c *config.Config, m *LedgerReceiptManifest) { c.Service.Environment = config.EnvironmentProduction }},
		{"api", func(c *config.Config, m *LedgerReceiptManifest) { c.Service.Mode = config.ModeAPI }},
		{"embedded", func(c *config.Config, m *LedgerReceiptManifest) { c.Worker.EmbeddedLedgerSync = nil }},
		{"missing_stream", func(c *config.Config, m *LedgerReceiptManifest) { m.Streams = m.Streams[:3] }},
		{"duplicate_stream", func(c *config.Config, m *LedgerReceiptManifest) { m.Streams[1] = m.Streams[0] }},
		{"wrong_account", func(c *config.Config, m *LedgerReceiptManifest) { m.Streams[0].Binding.AccountID = "other" }},
		{"wrong_db", func(c *config.Config, m *LedgerReceiptManifest) { m.Streams[0].Binding.RedisDB = 1 }},
		{"wrong_source", func(c *config.Config, m *LedgerReceiptManifest) { m.Streams[0].Binding.SourceID = "other" }},
		{"wrong_run", func(c *config.Config, m *LedgerReceiptManifest) {
			m.Streams[0].Binding.ServerRunID = strings.Repeat("b", 40)
		}},
		{"bad_owner", func(c *config.Config, m *LedgerReceiptManifest) { m.Streams[0].Owner = "old" }},
		{"unknown_version", func(c *config.Config, m *LedgerReceiptManifest) { m.Version = "future" }},
		{"unmapped_prefix", func(c *config.Config, m *LedgerReceiptManifest) { c.Redis.GatewayID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, m := receiptLoopConfig(), receiptLoopManifest()
			tc.mutate(&c, &m)
			if err := validateLedgerReceiptManifest(c, m); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	if err := os.Chmod(cfg.Worker.LedgerReceiptManifest, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLedgerReceiptManifest(cfg); err == nil {
		t.Fatal("owner file exposed")
	}
	for _, raw := range []string{`{"version":"ledger.receipt-worker.v1","version":"ledger.receipt-worker.v1","streams":[]}`, `{"version":"ledger.receipt-worker.v1","streams":[],"override":true}`, `{} {}`} {
		if err := os.WriteFile(cfg.Worker.LedgerReceiptManifest, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(cfg.Worker.LedgerReceiptManifest, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadLedgerReceiptManifest(cfg); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

func TestCurrentAccountBaselineLegacyEntrypointsGuarded(t *testing.T) {
	db, ctx := receiptTestDB(t)
	repo := ledger.NewRepository(db)
	store, _ := ledger.NewLedgerReceiptStore(db)
	b := receiverBinding()
	if _, err := store.Open(ctx, b, "0-0"); err != nil {
		t.Fatal(err)
	}
	cfg := receiptLoopConfig()
	// Port 1 must never be contacted: registration rejects both paths first.
	if err := RunLedgerSyncLoop(ctx, cfg, repo, LedgerSyncLoopOptions{Roles: []string{"hb"}}, nil); !errors.Is(err, ledger.ErrLedgerCaptureBinding) {
		t.Fatal(err)
	}
	if _, err := SyncLedger(ctx, cfg, repo, LedgerSyncOptions{Roles: []string{"hb"}}); !errors.Is(err, ledger.ErrLedgerCaptureBinding) {
		t.Fatal(err)
	}
}

func TestCurrentAccountBaselineReceiptWorkerLocalRedis(t *testing.T) {
	db, ctx := receiptTestDB(t)
	client := localReceiptRedis(t)
	cfg := receiptLoopConfig()
	cfg.Redis.URL = "unix://" + client.Options().Addr + "?db=0"
	m := receiptLoopManifest()
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	var run string
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "run_id:") {
			run = strings.TrimPrefix(line, "run_id:")
		}
	}
	store, _ := ledger.NewLedgerReceiptStore(db)
	for i := range m.Streams {
		b := &m.Streams[i].Binding
		b.ServerRunID = run
		key, _ := b.Stream()
		if b.Role == "hb" || b.Role == "reply" {
			if err = client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: "1-0", Values: []any{"body", string(receiverBody(b.Role))}}).Err(); err != nil {
				t.Fatal(err)
			}
		} else if err = client.XGroupCreateMkStream(ctx, key, "fixture-only", "0").Err(); err != nil {
			t.Fatal(err)
		}
		h, e := store.Open(ctx, *b, "0-0")
		if e != nil {
			t.Fatal(e)
		}
		m.Streams[i].Owner = h.Owner()
	}
	cfg.Worker.LedgerReceiptManifest = writeReceiptManifest(t, m)
	repo := ledger.NewRepository(db)
	callbacks := 0
	for round := 0; round < 2; round++ {
		loopCtx, cancel := context.WithCancel(ctx)
		ready := false
		opts := LedgerSyncLoopOptions{Count: 100, OnLedgerChange: func(callbackCtx context.Context, change LedgerChange) {
			callbacks++
			if change.Assets != 1 || change.LastStreamID != "1-0" {
				t.Error(change)
			}
			var n int
			if e := db.QueryRowContext(callbackCtx, `SELECT count(*) FROM ledger_stream_read_entries`).Scan(&n); e != nil || n != 1 {
				t.Errorf("notification before commit: %d %v", n, e)
			}
		}}
		err = RunLedgerReceiptLoop(loopCtx, cfg, repo, opts, nil, func() {
			ready = true
			if _, e := repo.AcquireStreamWriter(ctx, []string{"relay:test:v1:huaxin:00030484:reply"}, true); !errors.Is(e, ledger.ErrLedgerWriterBusy) {
				t.Error("concurrent receipt worker", e)
			}
			cancel()
		})
		cancel()
		if err != nil || !ready {
			t.Fatal("worker", round, ready, err)
		}
	}
	if callbacks != 1 {
		t.Fatal("restart replayed business notification", callbacks)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_read_entries`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	var retained []byte
	if err = db.QueryRowContext(ctx, `SELECT body FROM ledger_stream_read_entries WHERE stream_key LIKE '%:reply'`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err = json.Unmarshal(retained, &envelope); err != nil || !bytes.HasPrefix(retained, []byte(" \n")) {
		t.Fatal("body changed", err)
	}
	for _, entry := range m.Streams {
		key, _ := entry.Binding.Stream()
		groups, e := client.XInfoGroups(ctx, key).Result()
		want := 0
		if entry.Binding.Role == "event" || entry.Binding.Role == "dlq" {
			want = 1
		}
		if e != nil || len(groups) != want {
			t.Fatal("receiver changed groups", groups, e)
		}
		if _, e = repo.AcquireStreamWriter(ctx, []string{key}, false); !errors.Is(e, ledger.ErrLedgerCaptureBinding) {
			t.Fatal("legacy restart allowed", e)
		}
	}
	// A source switch does not trigger fallback or auto-registration.
	m.Streams[0].Binding.ServerRunID = strings.Repeat("b", 40)
	cfg.Worker.LedgerReceiptManifest = writeReceiptManifest(t, m)
	if err = RunLedgerReceiptLoop(ctx, cfg, repo, LedgerSyncLoopOptions{}, nil, nil); err == nil {
		t.Fatal("source mismatch accepted")
	}
}

func TestCurrentAccountBaselineReceiptLoopFailureStops(t *testing.T) {
	db, ctx := receiptTestDB(t)
	store, _ := ledger.NewLedgerReceiptStore(db)
	b := receiverBinding()
	b.Role = "reply"
	h, err := store.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	obs := receiptObservation(b)
	reader := &receiptReaderStub{observations: []LedgerSourceObservation{obs}, response: receiptResponse(h.Stream(), "1-0", []byte(`{}`))}
	notified, ready := false, false
	err = runLedgerReceiptReads(ctx, store, []ledger.LedgerStreamHandle{h}, reader, LedgerSyncLoopOptions{OnLedgerChange: func(context.Context, LedgerChange) { notified = true }}, slog.Default(), func() { ready = true })
	if err == nil || notified || ready || reader.reads != 1 {
		t.Fatal("failed batch continued", err, notified, ready, reader.reads)
	}
	st, err := store.Status(ctx, h)
	if err != nil || st.Cursor != "0-0" || st.BlockedReason == "" || st.FaultCount != 1 {
		t.Fatal(st, err)
	}
	if _, err = store.Attach(ctx, b, h.Owner()); !errors.Is(err, ledger.ErrLedgerCaptureBlocked) {
		t.Fatal("restart cleared failed projection", err)
	}
}

func TestCurrentAccountBaselineLegacyToReceiptHandoff(t *testing.T) {
	db, ctx := receiptTestDB(t)
	client := localReceiptRedis(t)
	cfg := receiptLoopConfig()
	cfg.Redis.URL = "unix://" + client.Options().Addr + "?db=0"
	b := receiverBinding()
	b.Role = "reply"
	key, _ := b.Stream()
	body := receiverBody("reply")
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: "1-0", Values: []any{"body", string(body)}}).Err(); err != nil {
		t.Fatal(err)
	}
	repo := ledger.NewRepository(db)
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	called := false
	err := RunLedgerSyncLoop(loopCtx, cfg, repo, LedgerSyncLoopOptions{
		Roles: []string{"reply"}, Checkpoints: repo,
		OnLedgerChange: func(context.Context, LedgerChange) { called = true; cancel() },
	}, nil)
	if err != nil || !called {
		t.Fatal("legacy processing", called, err)
	}
	checkpoint, err := repo.GetStreamCheckpoint(ctx, key)
	if err != nil || checkpoint.LastStreamID != "1-0" || checkpoint.ErrorCount != 0 {
		t.Fatal(checkpoint, err)
	}
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "run_id:") {
			b.ServerRunID = strings.TrimPrefix(line, "run_id:")
		}
	}
	store, _ := ledger.NewLedgerReceiptStore(db)
	h, err := store.Open(ctx, b, checkpoint.LastStreamID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.AcquireStreamWriter(ctx, []string{key}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	attached, err := lease.Receipts().Attach(ctx, b, h.Owner())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewRedisLedgerReceiptReader(client, b.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ReceiveLedgerStreamOnce(ctx, lease.Receipts(), attached, reader, 10)
	if err != nil || len(r.Entries) != 0 || r.EndCursor != "1-0" {
		t.Fatal(r, err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM asset_snapshots`).Scan(&n); err != nil || n != 1 {
		t.Fatal("legacy facts replayed", n, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_read_entries`).Scan(&n); err != nil || n != 0 {
		t.Fatal("legacy raw upgraded to receipt", n, err)
	}
}
