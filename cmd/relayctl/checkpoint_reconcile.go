package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/redisstream"
)

func runCheckpointReconcile(args []string) error {
	flags := flag.NewFlagSet("checkpoint-reconcile", flag.ContinueOnError)
	path := flags.String("config", os.Getenv(config.EnvPath), "active TEST YAML config path")
	account := flags.String("account", "", "enabled TEST account")
	normalizeOnly := flags.Bool("normalize-only", false, "structure original error without publishing queries")
	timeout := flags.Duration("timeout", 2*time.Minute, "bounded read-only reconciliation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" || strings.TrimSpace(*account) == "" {
		return errors.New("explicit config and account required")
	}
	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	if cfg.Redis.Env != "test" || cfg.Service.Environment != config.EnvironmentTest {
		return errors.New("checkpoint-reconcile is restricted to TEST")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	repo := ledger.NewRepository(db)
	observability, err := redisstream.NewRuntimeObservability(*cfg, repo, nil)
	if err != nil {
		return err
	}
	defer observability.Close()
	publisher, err := redisstream.OpenRedisCommandPublisher(cfg.Redis)
	if err != nil {
		return err
	}
	defer publisher.Close()
	service, err := orderflow.New(orderflow.Options{Config: *cfg, Ledger: repo, Publisher: publisher})
	if err != nil {
		return err
	}
	query := func(ctx context.Context, action string) (string, error) {
		var result orderflow.RefreshQueryResult
		var err error
		switch action {
		case redisstream.ActionAccountAsset:
			result, err = service.RefreshAsset(ctx, *account, orderflow.RefreshOptions{})
		case redisstream.ActionAccountPositions:
			result, err = service.RefreshPositions(ctx, *account, orderflow.RefreshOptions{})
		case redisstream.ActionOrderList:
			result, err = service.RefreshOrders(ctx, *account, orderflow.RefreshOptions{})
		case redisstream.ActionFillList:
			result, err = service.RefreshFills(ctx, *account, orderflow.RefreshOptions{})
		default:
			return "", errors.New("non-query action prohibited")
		}
		return result.MessageID, err
	}
	report, reconcileErr := observability.ReconcileReplyCheckpoint(ctx, *account, *normalizeOnly, query)
	if err := writeJSON(report); err != nil {
		return err
	}
	return reconcileErr
}
