package redisstream

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/ledger"
)

func TestCurrentAccountBaselineHandoffLocalRedis(t *testing.T) {
	db, ctx := receiptTestDB(t)
	migration, err := os.ReadFile("../../migrations/postgres/000056_ledger_stream_handoffs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	client := localReceiptRedis(t)
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
	var inputs []ledger.LedgerHandoffInput
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		b := receiverBinding()
		b.Role, b.ServerRunID = role, run
		key, _ := b.Stream()
		if role == "hb" || role == "reply" {
			err = client.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: "1-0", Values: []any{"body", string(receiverBody(role))}}).Err()
		} else {
			err = client.XGroupCreateMkStream(ctx, key, "fixture", "0").Err()
		}
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, ledger.LedgerHandoffInput{Binding: b, Cursor: "0-0"})
	}
	reader, err := NewRedisLedgerReceiptReader(client, "local-fixture")
	if err != nil {
		t.Fatal(err)
	}
	store, _ := ledger.NewLedgerReceiptStore(db)
	plan, err := store.PrepareHandoff(ctx, "register", inputs, reader.ProbeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_receivers`).Scan(&count); err != nil || count != 0 {
		t.Fatal("plan wrote receivers", count, err)
	}
	for _, entry := range plan.Entries {
		var evidence struct {
			Continuity bool `json:"continuity_verified"`
			Epoch      bool `json:"stream_epoch_verified"`
		}
		if err = json.Unmarshal(entry.Source.Evidence, &evidence); err != nil || evidence.Continuity || evidence.Epoch {
			t.Fatal("false coverage", err)
		}
	}
	sha, _ := ledger.LedgerHandoffDigest(plan)
	receipt, err := store.ApplyHandoff(ctx, plan, sha, reader.ProbeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	cfg := receiptLoopConfig()
	cfg.Redis.URL = "unix://" + client.Options().Addr + "?db=0"
	manifest := LedgerReceiptManifest{Version: "ledger.receipt-worker.v1", Streams: receipt.Registrations}
	cfg.Worker.LedgerReceiptManifest = writeReceiptManifest(t, manifest)
	runWorker := func() {
		t.Helper()
		loopCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		ready := false
		err := RunLedgerReceiptLoop(loopCtx, cfg, ledger.NewRepository(db), LedgerSyncLoopOptions{}, nil, func() { ready = true; cancel() })
		if err != nil || !ready {
			t.Fatal("worker did not attach", ready, err)
		}
	}
	runWorker()
	inputs = nil
	for _, reg := range receipt.Registrations {
		h, err := store.Attach(ctx, reg.Binding, reg.Owner)
		if err != nil {
			t.Fatal(err)
		}
		status, err := store.Status(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, ledger.LedgerHandoffInput{Binding: reg.Binding, Cursor: status.Cursor, PreviousOwner: reg.Owner})
	}
	plan, err = store.PrepareHandoff(ctx, "takeover", inputs, reader.ProbeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	sha, _ = ledger.LedgerHandoffDigest(plan)
	receipt, err = store.ApplyHandoff(ctx, plan, sha, reader.ProbeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Streams = receipt.Registrations
	cfg.Worker.LedgerReceiptManifest = writeReceiptManifest(t, manifest)
	runWorker()
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_read_entries`).Scan(&count); err != nil || count != 2 {
		t.Fatal("handoff replayed raw entries", count, err)
	}
	for _, in := range inputs {
		key, _ := in.Binding.Stream()
		groups, err := client.XInfoGroups(ctx, key).Result()
		want := 0
		if in.Binding.Role == "event" || in.Binding.Role == "dlq" {
			want = 1
		}
		if err != nil || len(groups) != want {
			t.Fatal("groups changed", err)
		}
	}
	// Same run_id is not proof of continuity: a vanished cursor must stop preflight.
	for _, reg := range receipt.Registrations {
		if reg.Binding.Role != "reply" {
			continue
		}
		key, _ := reg.Binding.Stream()
		if err = client.XDel(ctx, key, "1-0").Err(); err != nil {
			t.Fatal(err)
		}
		if _, err = reader.ProbeHandoff(ctx, reg.Binding, "1-0"); err == nil {
			t.Fatal("deleted anchor accepted")
		}
		wrong := reg.Binding
		wrong.ServerRunID = strings.Repeat("b", 40)
		if _, err = reader.ProbeHandoff(ctx, wrong, "0-0"); err == nil {
			t.Fatal("wrong source accepted")
		}
	}
}
