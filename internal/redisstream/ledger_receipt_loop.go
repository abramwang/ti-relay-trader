package redisstream

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

// Owners are local receiver capabilities, not public account-baseline fields.
type LedgerReceiptManifest struct {
	Version string                      `json:"version"`
	Streams []LedgerReceiptRegistration `json:"streams"`
}

type LedgerReceiptRegistration = ledger.LedgerStreamRegistration

func loadLedgerReceiptManifest(cfg config.Config) (LedgerReceiptManifest, error) {
	var out LedgerReceiptManifest
	if err := ReadLedgerReceiptDocument(cfg.Worker.LedgerReceiptManifest, 64<<10, &out); err != nil {
		return out, err
	}
	return out, validateLedgerReceiptManifest(cfg, out)
}

// ReadLedgerReceiptDocument rejects ambiguous JSON and exposed local owners.
func ReadLedgerReceiptDocument(path string, limit int64, out any) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute private ledger receipt document required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("ledger receipt document must be a regular file, not a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, st) || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || limit < 1 || limit > 4<<20 || st.Size() > limit {
		return errors.New("ledger receipt document is exposed, changed, or exceeds the size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit || ledger.ValidateCurrentBaselineSourceJSON(raw) != nil {
		return errors.New("invalid ledger receipt document JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

func ValidateLedgerReceiptManifest(cfg config.Config, m LedgerReceiptManifest) error {
	return validateLedgerReceiptManifest(cfg, m)
}

func validateLedgerReceiptManifest(cfg config.Config, m LedgerReceiptManifest) error {
	// This opt-in path has not had production or consumer acceptance.
	if cfg.Service.Environment != config.EnvironmentTest || cfg.Redis.Env != "test" || cfg.Service.Mode != config.ModeWorker || cfg.EmbeddedLedgerSyncEnabled() {
		return errors.New("ledger receipts currently require TEST and an external worker with embedded sync disabled")
	}
	if m.Version != "ledger.receipt-worker.v1" || len(m.Streams) == 0 || len(m.Streams) > 128 {
		return ledger.ErrLedgerCaptureBinding
	}
	ro, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return err
	}
	accounts := map[string]config.AccountRouteConfig{}
	for _, a := range cfg.Accounts {
		prefix := NewStreams(a.StreamPrefix).Prefix
		if prefix == "" || a.BrokerID != "huaxin" || a.AccountID == "" || a.GatewayID == "" {
			return ledger.ErrLedgerCaptureBinding
		}
		if _, exists := accounts[prefix]; exists {
			return ledger.ErrLedgerCaptureBinding
		}
		accounts[prefix] = a
	}
	expected := map[string]config.AccountRouteConfig{}
	for _, prefix := range PrefixesFromConfig(cfg) {
		a, ok := accounts[prefix]
		if !ok {
			return ledger.ErrLedgerCaptureBinding
		}
		for _, role := range []string{"reply", "event", "hb", "dlq"} {
			expected[prefix+":"+role] = a
		}
	}
	if len(expected) != len(m.Streams) {
		return ledger.ErrLedgerCaptureBinding
	}
	var source, run string
	for _, entry := range m.Streams {
		b := entry.Binding
		key, err := b.Stream()
		if err != nil {
			return err
		}
		a, ok := expected[key]
		if !ok || b.Environment != "test" || b.AccountID != a.AccountID || b.BrokerID != a.BrokerID || b.GatewayID != a.GatewayID || b.RedisDB != ro.DB {
			return ledger.ErrLedgerCaptureBinding
		}
		owner, err := hex.DecodeString(entry.Owner)
		if err != nil || len(owner) != 32 || strings.ToLower(entry.Owner) != entry.Owner {
			return ledger.ErrLedgerCaptureOwner
		}
		if source != "" && (source != b.SourceID || run != b.ServerRunID) {
			return ledger.ErrLedgerCaptureBinding
		}
		source, run = b.SourceID, b.ServerRunID
		delete(expected, key)
	}
	return nil
}

// RunLedgerReceiptLoop never registers, resets, migrates or rotates a receiver.
// Every stream must have an explicitly reviewed, persisted owner beforehand.
func RunLedgerReceiptLoop(ctx context.Context, cfg config.Config, repo *ledger.Repository, opts LedgerSyncLoopOptions, logger *slog.Logger, onReady func()) error {
	m, err := loadLedgerReceiptManifest(cfg)
	if err != nil {
		return err
	}
	if repo == nil {
		return ledger.ErrInvalidLedgerInput
	}
	keys := make([]string, len(m.Streams))
	for i, entry := range m.Streams {
		keys[i], _ = entry.Binding.Stream()
	}
	lease, err := repo.AcquireStreamWriter(ctx, keys, true)
	if err != nil {
		return err
	}
	defer lease.Close()
	store := lease.Receipts()
	handles := make([]ledger.LedgerStreamHandle, len(m.Streams))
	for i, entry := range m.Streams {
		handles[i], err = store.Attach(ctx, entry.Binding, entry.Owner)
		if err != nil {
			return fmt.Errorf("attach ledger stream %s: %w", keys[i], err)
		}
	}
	ro, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return err
	}
	ro.Protocol = 2
	ro.MaxRetries = -1
	client := redis.NewClient(ro)
	defer client.Close()
	reader, err := NewRedisLedgerReceiptReader(client, m.Streams[0].Binding.SourceID)
	if err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return runLedgerReceiptReads(ctx, store, handles, reader, opts, logger, onReady)
}

func runLedgerReceiptReads(ctx context.Context, store *ledger.LedgerReceiptStore, handles []ledger.LedgerStreamHandle, reader LedgerReceiptReader, opts LedgerSyncLoopOptions, logger *slog.Logger, onReady func()) error {
	if len(handles) == 0 {
		return ledger.ErrInvalidLedgerInput
	}
	count := opts.Count
	if count == 0 {
		count = 100
	}
	if count < 1 || count > 256 {
		return ledger.ErrCurrentBaselineLimit
	}
	ready := false
	nextRead := make([]time.Time, len(handles))
	for ctx.Err() == nil {
		for i, h := range handles {
			if ctx.Err() != nil {
				return nil
			}
			if time.Now().Before(nextRead[i]) {
				continue
			}
			r, err := ReceiveLedgerStreamOnce(ctx, store, h, reader, int(count))
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("ledger receipt stream %s stopped: %w", h.Stream(), err)
			}
			var report LedgerStreamReport
			if err = json.Unmarshal(r.ApplicationReport, &report); err != nil {
				return err
			}
			// Notifications are deliberately after the receipt/business COMMIT.
			// They are hints, not durable delivery or source-coverage evidence.
			notifyLedgerChanges(ctx, opts, report)
			delay := 5 * time.Second
			if len(r.Entries) != 0 {
				delay = 250 * time.Millisecond
			}
			nextRead[i] = time.Now().Add(delay)
		}
		if !ready {
			ready = true
			logger.Info("relay_ledger_receipt_worker_started", "streams", len(handles), "continuity_verified", false)
			if onReady != nil {
				onReady()
			}
		}
		delay := 5 * time.Second
		for _, next := range nextRead {
			if until := time.Until(next); until < delay {
				delay = until
			}
		}
		if delay < 0 {
			delay = 0
		}
		if !waitForLedgerSyncRetry(ctx, delay) {
			break
		}
	}
	return nil
}
