package redisstream

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/ledger"
)

func receiptTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("RELAY_POSITION_BASELINE_ISOLATED_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL only")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Host, "/tmp/relay-position-baseline-pg.") || cfg.User != "relay_baseline_test" {
		t.Fatal("non-isolated PG refused")
	}
	admin := stdlib.OpenDB(*cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	schema := fmt.Sprintf("receipts_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		cancel()
	})
	files, err := os.ReadDir("../../migrations/postgres")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".up.sql") || f.Name() > "000055_ledger_stream_receipts.up.sql" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../../migrations/postgres", f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(f.Name(), err)
		}
	}
	return db, ctx
}

func receiverBinding() ledger.LedgerStreamBinding {
	return ledger.LedgerStreamBinding{Environment: "test", AccountID: "00030484", BrokerID: "huaxin", GatewayID: "00030484", Role: "hb", SourceID: "local-fixture", ServerRunID: strings.Repeat("a", 40)}
}

func receiverBody(role string) []byte {
	w := map[string]any{"protocol": Protocol, "message_id": "fixture-" + role, "produced_at": time.Now().UTC().Format(time.RFC3339Nano), "message_type": "heartbeat",
		"routing": Routing{Env: "test", BrokerID: "huaxin", GatewayID: "00030484", AccountID: "00030484"}, "payload": map[string]any{"broker_ready": true}}
	if role == "reply" {
		w["message_type"] = "reply"
		w["action"] = "account.asset.query"
		w["result_type"] = "asset_page"
		w["status"] = "completed"
		w["chunk"] = map[string]any{"is_last": true}
		w["payload"] = map[string]any{"account": map[string]any{"account_id": "00030484", "cash_available": 100, "cash_total": 100, "net_asset": 100}}
	}
	b, _ := json.Marshal(w)
	return append(append([]byte(" \n"), b...), byte('\n'))
}

type receiptReaderStub struct {
	observations  []LedgerSourceObservation
	response      any
	err           error
	reads, probes int
}

func (s *receiptReaderStub) Observe(context.Context, string, string) (LedgerSourceObservation, error) {
	i := s.probes
	s.probes++
	if i >= len(s.observations) {
		i = len(s.observations) - 1
	}
	return s.observations[i], nil
}
func (s *receiptReaderStub) Read(context.Context, string, string, int) (any, error) {
	s.reads++
	return s.response, s.err
}
func receiptObservation(b ledger.LedgerStreamBinding) LedgerSourceObservation {
	return LedgerSourceObservation{SourceID: b.SourceID, ServerRunID: b.ServerRunID, RedisDB: b.RedisDB, ObservedAt: time.Now().UTC(), Length: 1, EntriesAdded: 1, FirstID: "1-0", LastGeneratedID: "1-0", MaxDeletedID: "0-0"}
}
func receiptResponse(key, id string, body []byte) any {
	return []any{[]any{key, []any{[]any{id, []any{"body", string(body)}}}}}
}

func TestCurrentAccountBaselineReceiptRESPValidation(t *testing.T) {
	key := "relay:test:v1:huaxin:00030484:hb"
	for _, tc := range []struct {
		name     string
		response any
	}{
		{"other stream", receiptResponse("other", "1-0", []byte("raw"))},
		{"duplicate field", []any{[]any{key, []any{[]any{"1-0", []any{"body", "a", "body", "b"}}}}}},
		{"duplicate entry", []any{[]any{key, []any{[]any{"1-0", []any{"body", "a"}}, []any{"1-0", []any{"body", "b"}}}}}},
		{"old entry", receiptResponse(key, "0-0", []byte("raw"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeLedgerRead(key, "0-0", 2, tc.response); err == nil {
				t.Fatal("bad response accepted")
			}
		})
	}
	raw := []byte{0, 0xff, 'a'}
	rows, err := decodeLedgerRead(key, "0-0", 1, receiptResponse(key, "1-0", raw))
	if err != nil || !bytes.Equal(rows[0].Body, raw) {
		t.Fatal("raw bytes lost", rows, err)
	}
}

func TestCurrentAccountBaselineReceiptObservedSourceGuards(t *testing.T) {
	b := receiverBinding()
	good := receiptObservation(b)
	for _, tc := range []struct {
		name   string
		modify func(*LedgerSourceObservation)
	}{
		{"run", func(o *LedgerSourceObservation) { o.ServerRunID = strings.Repeat("b", 40) }},
		{"db", func(o *LedgerSourceObservation) { o.RedisDB = 1 }},
		{"source", func(o *LedgerSourceObservation) { o.SourceID = "other" }},
		{"deleted", func(o *LedgerSourceObservation) { o.MaxDeletedID = "1-0" }},
		{"trimmed", func(o *LedgerSourceObservation) { o.EntriesAdded = 3 }},
		{"cleared", func(o *LedgerSourceObservation) { o.Length = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := good
			tc.modify(&o)
			if err := checkLedgerSource(b, "0-0", o); err == nil {
				t.Fatal("source fault accepted")
			}
		})
	}
	if err := checkLedgerSource(b, "0-0", good); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentAccountBaselineReceiptExistingLedgerPipeline(t *testing.T) {
	db, ctx := receiptTestDB(t)
	store, _ := ledger.NewLedgerReceiptStore(db)
	b := receiverBinding()
	h, err := store.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	body := receiverBody("hb")
	obs := receiptObservation(b)
	reader := &receiptReaderStub{observations: []LedgerSourceObservation{obs}, response: receiptResponse(h.Stream(), "1-0", body)}
	r, err := ReceiveLedgerStreamOnce(ctx, store, h, reader, 10)
	if err != nil || r.Outcome != "applied" || r.EndCursor != "1-0" {
		t.Fatal(r, err)
	}
	var saved, cursor string
	// Legacy archive normalizes outer whitespace; only the new receipt claims
	// byte identity. Both are committed with the same ledger checkpoint.
	if err = db.QueryRowContext(ctx, `SELECT body_text FROM raw_stream_messages WHERE stream_key=$1 AND stream_id='1-0'`, h.Stream()).Scan(&saved); err != nil || saved != strings.TrimSpace(string(body)) {
		t.Fatal(saved, err)
	}
	retained, err := store.Receipt(ctx, h, r.AttemptID)
	if err != nil || len(retained.Entries) != 1 || !bytes.Equal(retained.Entries[0].Body, body) {
		t.Fatal("durable receipt lost original bytes", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT last_stream_id FROM stream_checkpoints WHERE stream_key=$1`, h.Stream()).Scan(&cursor); err != nil || cursor != "1-0" {
		t.Fatal(cursor, err)
	}
	st, _ := store.Status(ctx, h)
	obs.AnchorSHA256 = st.AnchorSHA256
	reader = &receiptReaderStub{observations: []LedgerSourceObservation{obs}, response: nil, err: redis.Nil}
	r, err = ReceiveLedgerStreamOnce(ctx, store, h, reader, 10)
	if err != nil || r.EndCursor != "1-0" || len(r.Entries) != 0 {
		t.Fatal(r, err)
	}
	obs.AnchorSHA256 = strings.Repeat("f", 64)
	reader = &receiptReaderStub{observations: []LedgerSourceObservation{obs}, response: nil}
	r, err = ReceiveLedgerStreamOnce(ctx, store, h, reader, 10)
	if err == nil || r.ErrorCode != "source_cursor_body_changed" || reader.reads != 0 {
		t.Fatal(r, err, reader.reads)
	}
	var ev map[string]any
	json.Unmarshal(r.Evidence, &ev)
	if ev["continuity_verified"] != false || ev["stream_epoch_verified"] != false {
		t.Fatal(ev)
	}
}

func TestCurrentAccountBaselineReceiptSourceChangesAfterRead(t *testing.T) {
	db, ctx := receiptTestDB(t)
	store, _ := ledger.NewLedgerReceiptStore(db)
	b := receiverBinding()
	h, err := store.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	before := receiptObservation(b)
	after := before
	after.ServerRunID = strings.Repeat("b", 40)
	body := receiverBody("hb")
	reader := &receiptReaderStub{observations: []LedgerSourceObservation{before, after}, response: receiptResponse(h.Stream(), "1-0", body)}
	r, err := ReceiveLedgerStreamOnce(ctx, store, h, reader, 1)
	if err == nil || r.Outcome != "read_failed" || len(r.Entries) != 1 || !bytes.Equal(r.Entries[0].Body, body) {
		t.Fatal(r, err)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM raw_stream_messages`).Scan(&n)
	if n != 0 {
		t.Fatal("unverified read applied", n)
	}
	st, _ := store.Status(ctx, h)
	if st.Cursor != "0-0" || st.FaultCount != 1 {
		t.Fatal(st)
	}
}

func TestCurrentAccountBaselineReceiptPartialProjectionRollback(t *testing.T) {
	db, ctx := receiptTestDB(t)
	store, _ := ledger.NewLedgerReceiptStore(db)
	b := receiverBinding()
	b.Role = "reply"
	h, err := store.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	body := receiverBody("reply")
	bad := bytes.ReplaceAll(body, []byte(`"cash_total":100`), []byte(`"cash_total":"invalid"`))
	obs := receiptObservation(b)
	obs.Length = 2
	obs.EntriesAdded = 2
	obs.LastGeneratedID = "2-0"
	reader := &receiptReaderStub{observations: []LedgerSourceObservation{obs}, response: []any{[]any{h.Stream(), []any{[]any{"1-0", []any{"body", string(body)}}, []any{"2-0", []any{"body", string(bad)}}}}}}
	r, err := ReceiveLedgerStreamOnce(ctx, store, h, reader, 10)
	if err == nil || r.Outcome != "application_failed" || len(r.Entries) != 2 {
		t.Fatalf("outcome=%s error=%v report=%s", r.Outcome, err, r.ApplicationReport)
	}
	for _, table := range []string{"accounts", "asset_snapshots", "raw_stream_messages", "stream_checkpoints"} {
		var n int
		db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n)
		if n != 0 {
			t.Fatal("partial write in", table, n)
		}
	}
	var report LedgerStreamReport
	if err = json.Unmarshal(r.ApplicationReport, &report); err != nil || report.Totals.Assets != 1 || len(report.Errors) == 0 {
		t.Fatalf("expected a partial business application before rollback: %s (%v)", r.ApplicationReport, err)
	}
	st, _ := store.Status(ctx, h)
	if st.Cursor != "0-0" || st.BlockedReason == "" {
		t.Fatal(st)
	}
	before := reader.reads
	if _, err = ReceiveLedgerStreamOnce(ctx, store, h, reader, 10); !errors.Is(err, ledger.ErrLedgerCaptureBlocked) || reader.reads != before {
		t.Fatal("blocked source read again", err)
	}
}

func localReceiptRedis(t *testing.T) *redis.Client {
	t.Helper()
	binary := os.Getenv("RELAY_BASELINE_LOCAL_REDIS_SERVER")
	if binary == "" {
		t.Skip("explicit local Redis binary required")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute local binary required")
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--unixsocketperm", "700", "--dir", dir, "--save", "", "--appendonly", "no", "--daemonize", "no", "--logfile", filepath.Join(dir, "redis.log"))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, Protocol: 2, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() {
		client.Close()
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "redis.log"))
	t.Fatalf("local Redis did not start; log=%s (check local binary runtime dependencies)", log)
	return nil
}

func TestCurrentAccountBaselineReceiptMissingAssetFields(t *testing.T) {
	for _, raw := range []string{`{}`, `{"account":null}`, `{"account":{"account_id":"00030484"}}`, `{"account":{"account_id":"00030484","cash_available":0,"cash_total":null,"net_asset":0}}`} {
		if err := checkReceiptAssetFields(json.RawMessage(raw)); err == nil {
			t.Fatal("incomplete asset could be treated as zero", raw)
		}
	}
	if err := checkReceiptAssetFields(json.RawMessage(`{"account":{"account_id":"00030484","cash_available":0,"cash_total":0,"net_asset":0}}`)); err != nil {
		t.Fatal("explicit zero rejected", err)
	}
}

func TestCurrentAccountBaselineReceiptLocalRedis(t *testing.T) {
	db, ctx := receiptTestDB(t)
	client := localReceiptRedis(t)
	store, _ := ledger.NewLedgerReceiptStore(db)
	b := receiverBinding()
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "run_id:") {
			b.ServerRunID = strings.TrimPrefix(line, "run_id:")
		}
	}
	key, _ := b.Stream()
	body := receiverBody("hb")
	if err = client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: "1-0", Values: []any{"body", string(body)}}).Err(); err != nil {
		t.Fatal(err)
	}
	h, err := store.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewRedisLedgerReceiptReader(client, b.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ReceiveLedgerStreamOnce(ctx, store, h, reader, 1)
	if err != nil || r.EndCursor != "1-0" {
		t.Fatal(r, err)
	}
	// A new reader resumes the retained cursor; no command/group/ACK is created.
	h2, err := store.Resume(ctx, b, h.Owner(), "1-0")
	if err != nil {
		t.Fatal(err)
	}
	r, err = ReceiveLedgerStreamOnce(ctx, store, h2, reader, 1)
	if err != nil || len(r.Entries) != 0 {
		t.Fatal(r, err)
	}
	if _, err = ReceiveLedgerStreamOnce(ctx, store, h, reader, 1); !errors.Is(err, ledger.ErrLedgerCaptureOwner) {
		t.Fatal(err)
	}
	if groups, e := client.XInfoGroups(ctx, key).Result(); e != nil || len(groups) != 0 {
		t.Fatal("receiver created a group", groups, e)
	}
	// Delete/recreate with a different anchor but the same server run_id.
	if err = client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err = client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: "1-0", Values: []any{"body", "changed"}}).Err(); err != nil {
		t.Fatal(err)
	}
	r, err = ReceiveLedgerStreamOnce(ctx, store, h2, reader, 1)
	if err == nil || r.ErrorCode != "source_cursor_body_changed" || r.EndCursor != "1-0" {
		t.Fatal(r, err)
	}
}
