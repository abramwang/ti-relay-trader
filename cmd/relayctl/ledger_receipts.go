package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

type ledgerReceiptRequest struct {
	Version string                      `json:"version"`
	Action  string                      `json:"action"`
	Streams []ledger.LedgerHandoffInput `json:"streams"`
}

func runLedgerReceipts(args []string) error {
	f := flag.NewFlagSet("ledger-receipts", flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	path := f.String("config", "", "explicit TEST YAML config; no environment overrides")
	mode := f.String("mode", "plan", "plan (read-only) or apply")
	input := f.String("input", "", "private request JSON for plan, or saved plan JSON for apply")
	output := f.String("output", "", "new private plan or worker manifest path; never overwritten")
	digest := f.String("plan-sha256", "", "exact plan digest required for apply")
	stopped := f.Bool("legacy-writers-stopped", false, "operator confirmation that pre-guard and current writers are stopped")
	timeout := f.Duration("timeout", 30*time.Second, "bounded preflight/apply timeout, at most two minutes")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !filepath.IsAbs(*path) || !filepath.IsAbs(*input) || !filepath.IsAbs(*output) || *timeout <= 0 || *timeout > 2*time.Minute {
		return errors.New("absolute config/input/output and timeout in (0,2m] required")
	}
	if (*mode != "plan" && *mode != "apply") || (*mode == "apply" && (!*stopped || len(*digest) != 64)) || (*mode == "plan" && (*stopped || *digest != "")) {
		return errors.New("apply requires -legacy-writers-stopped and -plan-sha256; plan is read-only")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not already exist")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	// relayctl is the external-worker operator, never an environment switch.
	cfg.Service.Mode = config.ModeWorker
	var request ledgerReceiptRequest
	var plan ledger.LedgerHandoffPlan
	var inputs []ledger.LedgerHandoffInput
	if *mode == "plan" {
		if err = redisstream.ReadLedgerReceiptDocument(*input, 64<<10, &request); err != nil {
			return err
		}
		if request.Version != "ledger.receipt-handoff-request.v1" || (request.Action != "register" && request.Action != "takeover") {
			return errors.New("unsupported handoff request")
		}
		inputs = request.Streams
	} else {
		if err = redisstream.ReadLedgerReceiptDocument(*input, 4<<20, &plan); err != nil {
			return err
		}
		for _, entry := range plan.Entries {
			inputs = append(inputs, entry.Input)
		}
		actual, e := ledger.LedgerHandoffDigest(plan)
		if e != nil || actual != *digest {
			return errors.New("plan digest mismatch")
		}
	}
	if err = validateLedgerHandoffConfig(*cfg, inputs); err != nil {
		return err
	}
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var name string
	if err = db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return err
	}
	if name != cfg.Database.ExpectedName {
		return errors.New("database identity mismatch")
	}
	ro, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return err
	}
	ro.Protocol = 2
	ro.MaxRetries = -1
	client := redis.NewClient(ro)
	defer client.Close()
	reader, err := redisstream.NewRedisLedgerReceiptReader(client, inputs[0].Binding.SourceID)
	if err != nil {
		return err
	}
	store, _ := ledger.NewLedgerReceiptStore(db)
	if *mode == "plan" {
		plan, err = store.PrepareHandoff(ctx, request.Action, inputs, reader.ProbeHandoff)
		if err != nil {
			return err
		}
		sha, err := ledger.LedgerHandoffDigest(plan)
		if err != nil {
			return err
		}
		if err = writeLedgerReceiptDocument(*output, plan, 4<<20); err != nil {
			return err
		}
		return writeJSON(map[string]any{"mode": "plan", "handoff_id": plan.HandoffID, "plan_sha256": sha, "stream_count": len(plan.Entries), "database_changed": false, "continuity_verified": false, "output": *output})
	}
	receipt, err := store.ApplyHandoff(ctx, plan, *digest, reader.ProbeHandoff)
	if err != nil {
		return fmt.Errorf("handoff failed or commit outcome unknown; retain original plan, do not reset state: %w", err)
	}
	manifest := redisstream.LedgerReceiptManifest{Version: "ledger.receipt-worker.v1", Streams: receipt.Registrations}
	if err = redisstream.ValidateLedgerReceiptManifest(*cfg, manifest); err != nil {
		return err
	}
	if err = writeLedgerReceiptDocument(*output, manifest, 64<<10); err != nil {
		return fmt.Errorf("handoff committed; repeat this same plan/digest with a new output path to recover manifest (do not prepare another handoff): %w", err)
	}
	return writeJSON(map[string]any{"mode": "apply", "handoff_id": receipt.HandoffID, "stream_count": len(receipt.Registrations), "replayed": receipt.Replayed, "worker_started": false, "continuity_verified": false, "output": *output})
}

func validateLedgerHandoffConfig(cfg config.Config, inputs []ledger.LedgerHandoffInput) error {
	p, err := pgx.ParseConfig(cfg.Database.DSN)
	if err != nil || cfg.Database.ExpectedName == "" || !strings.HasSuffix(cfg.Database.ExpectedName, "_test") || p.Database != cfg.Database.ExpectedName {
		return errors.New("handoff requires an explicitly named TEST database ending in _test")
	}
	m := redisstream.LedgerReceiptManifest{Version: "ledger.receipt-worker.v1"}
	for _, in := range inputs {
		m.Streams = append(m.Streams, ledger.LedgerStreamRegistration{Binding: in.Binding, Owner: strings.Repeat("0", 64)})
	}
	return redisstream.ValidateLedgerReceiptManifest(cfg, m)
}

func writeLedgerReceiptDocument(path string, value any, limit int) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > limit {
		return errors.New("receipt document exceeds size limit")
	}
	if !filepath.IsAbs(path) {
		return errors.New("absolute receipt document path required")
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	st, err := parent.Stat()
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("receipt output parent must be a private existing directory")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return parent.Sync()
}
