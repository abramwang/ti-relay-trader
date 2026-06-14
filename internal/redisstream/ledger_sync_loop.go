package redisstream

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
)

type LedgerSyncLoopOptions struct {
	Prefixes       []string
	StartID        string
	Count          int64
	Block          time.Duration
	Roles          []string
	OnTradeChange  func(context.Context, LedgerTradeChange)
	OnLedgerChange func(context.Context, LedgerChange)
}

type LedgerTradeChange struct {
	Stream       string
	Role         string
	AccountIDs   []string
	OrderEvents  int
	Fills        int
	LastStreamID string
}

type LedgerChange struct {
	Stream       string
	Role         string
	AccountIDs   []string
	OrderEvents  int
	Fills        int
	Assets       int
	Positions    int
	LastStreamID string
}

type ledgerStreamCursor struct {
	name   string
	role   string
	lastID string
}

func RunLedgerSyncLoop(ctx context.Context, cfg config.Config, writer LedgerWriter, opts LedgerSyncLoopOptions, logger *slog.Logger) error {
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return errors.New("redis.url is required for ledger sync loop")
	}
	if writer == nil {
		return errors.New("ledger writer is required for ledger sync loop")
	}
	if logger == nil {
		logger = slog.Default()
	}

	redisOptions, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return err
	}
	client := redis.NewClient(redisOptions)
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return err
	}

	prefixes := opts.Prefixes
	if len(prefixes) == 0 {
		prefixes = PrefixesFromConfig(cfg)
	}
	if len(prefixes) == 0 {
		return errors.New("no stream prefixes configured")
	}

	startID := strings.TrimSpace(opts.StartID)
	if startID == "" {
		startID = "0"
	}
	count := opts.Count
	if count <= 0 {
		count = 100
	}
	block := opts.Block
	if block <= 0 {
		block = time.Second
	}

	cursors := make([]ledgerStreamCursor, 0, len(prefixes)*2)
	for _, prefix := range prefixes {
		streams := NewStreams(prefix)
		for _, role := range outputRoles(opts.Roles) {
			name := streamNameForRole(streams, role)
			if name == "" {
				continue
			}
			cursors = append(cursors, ledgerStreamCursor{name: name, role: role, lastID: startID})
		}
	}
	if len(cursors) == 0 {
		return errors.New("no stream roles configured")
	}

	logger.Info("relay_ledger_sync_loop_started",
		"streams", len(cursors),
		"start_id", startID,
		"count", count,
		"block", block.String(),
	)
	for ctx.Err() == nil {
		for i := range cursors {
			report := readAndProcessStream(ctx, client, writer, cursors[i].name, cursors[i].role, cursors[i].lastID, count, block)
			if ctx.Err() != nil {
				break
			}
			if report.Totals.LastStreamID != "" {
				cursors[i].lastID = report.Totals.LastStreamID
			}
			if report.Count > 0 || len(report.Errors) > 0 {
				logger.Info("relay_ledger_sync_stream_batch",
					"stream", report.Name,
					"role", report.Role,
					"count", report.Count,
					"last_stream_id", cursors[i].lastID,
					"archived", report.Totals.Archived,
					"orders", report.Totals.Orders,
					"order_events", report.Totals.OrderEvents,
					"fills", report.Totals.Fills,
					"assets", report.Totals.Assets,
					"positions", report.Totals.Positions,
					"skipped", report.Totals.Skipped,
					"ledger_errors", report.Totals.LedgerErrors,
					"parse_errors", report.Totals.ParseErrors,
				)
			}
			if opts.OnTradeChange != nil && (report.Totals.OrderEvents > 0 || report.Totals.Fills > 0) {
				accountIDs := accountIDsFromLedgerResult(report.Totals)
				if len(accountIDs) > 0 {
					opts.OnTradeChange(ctx, LedgerTradeChange{
						Stream:       report.Name,
						Role:         report.Role,
						AccountIDs:   accountIDs,
						OrderEvents:  report.Totals.OrderEvents,
						Fills:        report.Totals.Fills,
						LastStreamID: report.Totals.LastStreamID,
					})
				}
			}
			if opts.OnLedgerChange != nil && (report.Totals.OrderEvents > 0 || report.Totals.Fills > 0 || report.Totals.Assets > 0 || report.Totals.Positions > 0) {
				accountIDs := accountIDsFromLedgerResult(report.Totals)
				if len(accountIDs) > 0 {
					opts.OnLedgerChange(ctx, LedgerChange{
						Stream:       report.Name,
						Role:         report.Role,
						AccountIDs:   accountIDs,
						OrderEvents:  report.Totals.OrderEvents,
						Fills:        report.Totals.Fills,
						Assets:       report.Totals.Assets,
						Positions:    report.Totals.Positions,
						LastStreamID: report.Totals.LastStreamID,
					})
				}
			}
		}
	}
	logger.Info("relay_ledger_sync_loop_stopped", "reason", ctx.Err())
	return nil
}

func accountIDsFromLedgerResult(result LedgerProcessResult) []string {
	accountIDs := result.AccountIDs
	if len(accountIDs) == 0 && strings.TrimSpace(result.LastAccountID) != "" {
		accountIDs = []string{result.LastAccountID}
	}
	return accountIDs
}
