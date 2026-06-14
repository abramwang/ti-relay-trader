package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/redisstream"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		return errors.New("database.dsn is required for worker mode")
	}
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return errors.New("redis.url is required for worker mode")
	}

	logger.Info("relay_worker_started",
		"mode", cfg.Service.Mode,
		"accounts_configured", len(cfg.Accounts),
		"jobs_configured", len(cfg.Jobs),
	)

	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)

	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	if err := db.PingContext(pingCtx); err != nil {
		cancelPing()
		return err
	}
	cancelPing()

	repo := ledger.NewRepository(db)
	publisher, err := redisstream.OpenRedisCommandPublisher(cfg.Redis)
	if err != nil {
		return err
	}
	defer publisher.Close()

	orders, err := orderflow.New(orderflow.Options{
		Config:    cfg,
		Ledger:    repo,
		Publisher: publisher,
	})
	if err != nil {
		return err
	}

	var autoRefresh *orderflow.AutoRefreshScheduler
	if cfg.AutoRefreshEnabled() {
		autoRefresh = orderflow.NewAutoRefreshScheduler(orderflow.AutoRefreshSchedulerOptions{
			Refresher: orders,
			Logger:    logger.With("component", "worker-auto-refresh"),
			Debounce:  time.Duration(cfg.AutoRefresh.DebounceSeconds) * time.Second,
			Cooldown:  time.Duration(cfg.AutoRefresh.CooldownSeconds) * time.Second,
			Timeout:   time.Duration(cfg.AutoRefresh.TimeoutSeconds) * time.Second,
		})
		defer autoRefresh.Stop()
		logger.Info("relay_worker_auto_refresh_enabled",
			"debounce", fmt.Sprintf("%ds", cfg.AutoRefresh.DebounceSeconds),
			"cooldown", fmt.Sprintf("%ds", cfg.AutoRefresh.CooldownSeconds),
			"timeout", fmt.Sprintf("%ds", cfg.AutoRefresh.TimeoutSeconds),
		)
	}

	err = redisstream.RunLedgerSyncLoop(ctx, cfg, repo, redisstream.LedgerSyncLoopOptions{
		StartID:     "0",
		Count:       200,
		Block:       time.Second,
		Roles:       []string{redisstream.SuffixReply, redisstream.SuffixEvent, redisstream.SuffixHB, redisstream.SuffixDLQ},
		Checkpoints: repo,
		OnTradeChange: func(_ context.Context, change redisstream.LedgerTradeChange) {
			if autoRefresh == nil {
				return
			}
			reason := fmt.Sprintf("worker-ledger:%s order_events=%d fills=%d", change.LastStreamID, change.OrderEvents, change.Fills)
			autoRefresh.RequestAccounts(change.AccountIDs, reason)
		},
	}, logger.With("component", "worker-ledger-sync"))
	if err != nil {
		return err
	}
	logger.Info("relay_worker_stopped", "reason", ctx.Err())
	return nil
}
