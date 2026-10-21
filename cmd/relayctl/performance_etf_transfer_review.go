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

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

func runETFTransferReview(args []string) error {
	flags := flag.NewFlagSet("performance-etf-transfer-review", flag.ContinueOnError)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay config path")
	account := flags.String("account", "", "account id")
	dateFrom := flags.String("date-from", "", "first date, YYYYMMDD or YYYY-MM-DD")
	dateTo := flags.String("date-to", "", "last date, defaults to date-from; at most 31 calendar days")
	output := flags.String("output", "", "new private JSON file, defaults to stdout")
	timeout := flags.Duration("timeout", 2*time.Minute, "read-only database review timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*account) == "" || *dateFrom == "" {
		return errors.New("-account and -date-from are required; positional arguments are not supported")
	}
	if *timeout <= 0 || *timeout > 10*time.Minute {
		return errors.New("timeout must be positive and no more than 10 minutes")
	}
	if *dateTo == "" {
		*dateTo = *dateFrom
	}
	from, err := parseCLITradeDate(*dateFrom)
	if err != nil {
		return err
	}
	to, err := parseCLITradeDate(*dateTo)
	if err != nil {
		return err
	}
	if to.Before(from) || !to.Before(from.AddDate(0, 0, 31)) {
		return errors.New("review must cover 1 to 31 calendar days in ascending order")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	repo := ledger.NewRepository(tx)
	service, err := performance.New(performance.Options{Store: repo})
	if err != nil {
		return err
	}
	reviews := make([]performance.ETFTransferReview, 0)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		review, err := service.ReviewETFComponentTransfers(ctx, *account, day.Format("2006-01-02"))
		if err != nil {
			return fmt.Errorf("review %s: %w", day.Format("2006-01-02"), err)
		}
		reviews = append(reviews, review)
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	report := struct {
		Environment      config.Environment              `json:"environment"`
		GeneratedAt      time.Time                       `json:"generated_at"`
		DatabaseReadOnly bool                            `json:"database_read_only"`
		DatabaseWrites   int                             `json:"database_writes"`
		BrokerCommands   int                             `json:"broker_commands"`
		MarketRequests   int                             `json:"market_requests"`
		Reviews          []performance.ETFTransferReview `json:"reviews"`
	}{Environment: cfg.Service.Environment, GeneratedAt: time.Now().In(timeutil.Location()), DatabaseReadOnly: true, Reviews: reviews}
	if *output == "" {
		return writeJSON(report)
	}
	return writeETFTransferReviewFile(*output, report)
}

func writeETFTransferReviewFile(path string, report any) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	return nil
}
