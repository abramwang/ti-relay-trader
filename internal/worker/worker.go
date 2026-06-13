package worker

import (
	"context"
	"log/slog"

	"ti-relay-trader/internal/config"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("relay_worker_started",
		"mode", cfg.Service.Mode,
		"accounts_configured", len(cfg.Accounts),
		"jobs_configured", len(cfg.Jobs),
	)
	<-ctx.Done()
	logger.Info("relay_worker_stopped", "reason", ctx.Err())
	return nil
}
