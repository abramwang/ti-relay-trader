package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"ti-relay-trader/internal/config"
	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	relayperformance "ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "ledger-sync":
		if err := runLedgerSync(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl ledger-sync: %v\n", err)
			os.Exit(1)
		}
	case "ledger-replay":
		if err := runLedgerReplay(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl ledger-replay: %v\n", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl migrate: %v\n", err)
			os.Exit(1)
		}
	case "performance-rebuild":
		if err := runPerformanceRebuild(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl performance-rebuild: %v\n", err)
			os.Exit(1)
		}
	case "redis-probe":
		if err := runRedisProbe(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl redis-probe: %v\n", err)
			os.Exit(1)
		}
	case "redis-scan":
		if err := runRedisScan(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl redis-scan: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

type performanceRebuildItem struct {
	AccountID string                             `json:"account_id"`
	TradeDate string                             `json:"trade_date"`
	Cost      relayperformance.CostLedgerResult  `json:"cost_ledger"`
	NAV       relayperformance.EconomicNAVResult `json:"economic_nav"`
	Error     string                             `json:"error,omitempty"`
}

type performanceRebuildReport struct {
	DateFrom    string                   `json:"date_from"`
	DateTo      string                   `json:"date_to"`
	Persist     bool                     `json:"persist"`
	Items       []performanceRebuildItem `json:"items"`
	SkippedDays []string                 `json:"skipped_days,omitempty"`
}

func runPerformanceRebuild(args []string) error {
	flags := flag.NewFlagSet("performance-rebuild", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountsValue := flags.String("accounts", "", "comma-separated account ids")
	dateFromValue := flags.String("date-from", "", "first trade date, YYYYMMDD or YYYY-MM-DD")
	dateToValue := flags.String("date-to", "", "last trade date, defaults to date-from")
	persist := flags.Bool("persist", false, "persist non-blocked cost states and provisional v2 NAVs")
	timeout := flags.Duration("timeout", 5*time.Minute, "rebuild timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	accounts := splitCSV(*accountsValue)
	if len(accounts) == 0 {
		return fmt.Errorf("-accounts is required")
	}
	dateFrom, err := parseCLITradeDate(*dateFromValue)
	if err != nil {
		return fmt.Errorf("invalid date-from: %w", err)
	}
	dateTo := dateFrom
	if strings.TrimSpace(*dateToValue) != "" {
		dateTo, err = parseCLITradeDate(*dateToValue)
		if err != nil {
			return fmt.Errorf("invalid date-to: %w", err)
		}
	}
	if dateTo.Before(dateFrom) {
		return fmt.Errorf("date-to must not be before date-from")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	marketClient, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := relayperformance.New(relayperformance.Options{
		Store:               ledger.NewRepository(db),
		Calendar:            marketClient,
		Market:              marketClient,
		FormulaVersion:      cfg.Performance.FormulaVersion,
		ETFT0FrictionRate:   cfg.Performance.ETFT0FrictionRate,
		AutoToleranceCNY:    cfg.Performance.AutoToleranceCNY,
		AutoToleranceBP:     cfg.Performance.AutoToleranceBP,
		WarningToleranceCNY: cfg.Performance.WarningToleranceCNY,
		WarningToleranceBP:  cfg.Performance.WarningToleranceBP,
	})
	if err != nil {
		return err
	}
	report := performanceRebuildReport{
		DateFrom: dateFrom.Format("2006-01-02"),
		DateTo:   dateTo.Format("2006-01-02"),
		Persist:  *persist,
	}
	for date := dateFrom; !date.After(dateTo); date = date.AddDate(0, 0, 1) {
		dateText := date.Format("2006-01-02")
		calendar, calendarErr := marketClient.TradingDayStatus(ctx, date.Format("20060102"))
		if calendarErr != nil {
			return fmt.Errorf("trading calendar %s: %w", dateText, calendarErr)
		}
		if calendar.IsTradingDayKnown && !calendar.IsTradingDay {
			report.SkippedDays = append(report.SkippedDays, dateText)
			continue
		}
		for _, accountID := range accounts {
			item := performanceRebuildItem{AccountID: accountID, TradeDate: dateText}
			cost, costErr := service.CalculateCostLedger(ctx, accountID, dateText, relayperformance.CostLedgerOptions{})
			item.Cost = cost
			if costErr != nil {
				item.Error = "cost ledger: " + costErr.Error()
				report.Items = append(report.Items, item)
				continue
			}
			if *persist && cost.Status != "blocked" {
				cost, costErr = service.CalculateCostLedger(ctx, accountID, dateText, relayperformance.CostLedgerOptions{Persist: true})
				item.Cost = cost
				if costErr != nil {
					item.Error = "persist cost ledger: " + costErr.Error()
					report.Items = append(report.Items, item)
					continue
				}
			}
			nav, navErr := service.CalculateEconomicNAV(ctx, accountID, dateText, relayperformance.EconomicNAVOptions{})
			item.NAV = nav
			if navErr != nil {
				item.Error = "economic nav: " + navErr.Error()
				report.Items = append(report.Items, item)
				continue
			}
			if *persist {
				nav, navErr = service.CalculateEconomicNAV(ctx, accountID, dateText, relayperformance.EconomicNAVOptions{Persist: true, Status: "provisional"})
				item.NAV = nav
				if navErr != nil {
					item.Error = "persist economic nav: " + navErr.Error()
				}
			}
			report.Items = append(report.Items, item)
		}
	}
	return writeJSON(report)
}

func parseCLITradeDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if parsed, err := time.ParseInLocation(layout, value, timeutil.Location()); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("expected YYYYMMDD or YYYY-MM-DD")
}

func runLedgerSync(args []string) error {
	flags := flag.NewFlagSet("ledger-sync", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	databaseURL := flags.String("database-url", os.Getenv("RELAY_DATABASE_URL"), "PostgreSQL DSN override")
	prefix := flags.String("stream-prefix", "", "override stream prefix, for example relay:prod:v1:huaxin:00030484")
	startID := flags.String("from", "0", "Redis Stream ID to read after; use 0 for backfill or $ for new messages")
	count := flags.Int64("count", 100, "maximum messages to read per stream")
	block := flags.Duration("block", 0, "optional XREAD block duration")
	roles := flags.String("roles", "reply,event", "comma-separated output stream roles to sync")
	timeout := flags.Duration("timeout", 30*time.Second, "sync timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	*cfg = redisstream.ApplyProbeEnv(*cfg)

	dsn := strings.TrimSpace(*databaseURL)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Database.DSN)
	}
	if dsn == "" {
		return fmt.Errorf("database DSN is required; set -database-url, RELAY_DATABASE_URL, or config.database.dsn")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}

	var prefixes []string
	if strings.TrimSpace(*prefix) != "" {
		prefixes = []string{strings.TrimSpace(*prefix)}
	}

	repo := ledger.NewRepository(db)
	report, err := redisstream.SyncLedger(ctx, *cfg, repo, redisstream.LedgerSyncOptions{
		Prefixes: prefixes,
		StartID:  *startID,
		Count:    *count,
		Block:    *block,
		Roles:    splitCSV(*roles),
	})
	if err != nil {
		return err
	}
	return writeJSON(report)
}

func runLedgerReplay(args []string) error {
	flags := flag.NewFlagSet("ledger-replay", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	databaseURL := flags.String("database-url", os.Getenv("RELAY_DATABASE_URL"), "PostgreSQL DSN override")
	accountID := flags.String("account-id", "", "optional account filter")
	dateFrom := flags.String("date-from", "", "optional received-date lower bound, YYYY-MM-DD in Asia/Shanghai")
	dateTo := flags.String("date-to", "", "optional received-date upper bound, YYYY-MM-DD in Asia/Shanghai")
	stages := flags.String("stages", "orders,fills,transfers", "comma-separated replay stages: orders,fills,transfers")
	timeout := flags.Duration("timeout", 15*time.Minute, "replay timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	dsn := strings.TrimSpace(*databaseURL)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Database.DSN)
	}
	if dsn == "" {
		return fmt.Errorf("database DSN is required; set -database-url, RELAY_DATABASE_URL, or config.database.dsn")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}

	repo := ledger.NewRepository(db)
	report, err := redisstream.ReplayArchivedLedger(ctx, db, repo, redisstream.ArchiveReplayOptions{
		AccountID: *accountID,
		DateFrom:  *dateFrom,
		DateTo:    *dateTo,
		Stages:    splitCSV(*stages),
	})
	if err != nil {
		return err
	}
	if err := writeJSON(report); err != nil {
		return err
	}
	for _, stage := range report.Stages {
		if stage.ErrorCount > 0 {
			return fmt.Errorf("%s replay completed with %d message errors", stage.Name, stage.ErrorCount)
		}
	}
	return nil
}

func runMigrate(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("missing migrate action: status, up, or down")
	}
	action := args[0]
	flags := flag.NewFlagSet("migrate "+action, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	databaseURL := flags.String("database-url", os.Getenv("RELAY_DATABASE_URL"), "PostgreSQL DSN override")
	dir := flags.String("dir", "migrations/postgres", "migration directory")
	steps := flags.Int("steps", 1, "rollback steps for down")
	timeout := flags.Duration("timeout", 30*time.Second, "database operation timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	dsn := strings.TrimSpace(*databaseURL)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Database.DSN)
	}
	if dsn == "" {
		return fmt.Errorf("database DSN is required; set -database-url, RELAY_DATABASE_URL, or config.database.dsn")
	}

	migrations, err := dbmigrations.LoadDir(*dir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	runner, err := dbmigrations.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer runner.Close()

	switch action {
	case "status":
		statuses, err := runner.Status(ctx, migrations)
		if err != nil {
			return err
		}
		return writeJSON(statuses)
	case "up":
		applied, err := runner.Up(ctx, migrations)
		if err != nil {
			return err
		}
		return writeJSON(applied)
	case "down":
		rolledBack, err := runner.Down(ctx, migrations, *steps)
		if err != nil {
			return err
		}
		return writeJSON(rolledBack)
	default:
		return fmt.Errorf("unknown migrate action %q", action)
	}
}

func runRedisProbe(args []string) error {
	flags := flag.NewFlagSet("redis-probe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	prefix := flags.String("stream-prefix", "", "override stream prefix, for example relay:prod:v1:huaxin:00030484")
	samples := flags.Int("samples", 3, "latest message summaries per stream")
	timeout := flags.Duration("timeout", 5*time.Second, "probe timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	*cfg = redisstream.ApplyProbeEnv(*cfg)

	var prefixes []string
	if strings.TrimSpace(*prefix) != "" {
		prefixes = []string{strings.TrimSpace(*prefix)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report, err := redisstream.Probe(ctx, *cfg, redisstream.ProbeOptions{
		SamplesPerStream: *samples,
		Prefixes:         prefixes,
	})
	if err != nil {
		return err
	}

	return writeJSON(report)
}

func runRedisScan(args []string) error {
	flags := flag.NewFlagSet("redis-scan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	pattern := flags.String("pattern", "", "Redis key pattern, defaults to relay:<redis.env>:v1:*:*")
	count := flags.Int64("count", 200, "SCAN count hint")
	timeout := flags.Duration("timeout", 10*time.Second, "scan timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	*cfg = redisstream.ApplyProbeEnv(*cfg)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report, err := redisstream.ScanStreams(ctx, *cfg, redisstream.StreamScanOptions{
		Pattern: *pattern,
		Count:   *count,
	})
	if err != nil {
		return err
	}
	return writeJSON(report)
}

func loadConfig(path string) (*config.Config, error) {
	if strings.TrimSpace(path) == "" {
		cfg := config.Default()
		return &cfg, nil
	}
	return config.Load(path)
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, `relayctl commands:
  ledger-sync    Archive Redis reply/event streams into PostgreSQL ledger
  ledger-replay  Rebuild order/fill/transfer ledgers from archived raw stream messages
  migrate        Run PostgreSQL migration status/up/down
  performance-rebuild  Rebuild trusted position costs and v2 economic NAVs
  redis-probe    Read-only Redis Stream probe using relay config
  redis-scan     Read-only Redis key scan for relay stream accounts

Examples:
  RELAY_DATABASE_URL=postgres://... REDIS_URL=redis://... go run ./cmd/relayctl ledger-sync -stream-prefix relay:prod:v1:huaxin:00030484 -count 20
  go run ./cmd/relayctl ledger-replay -config config/relay.prod.yaml -date-from 2026-07-01
  go run ./cmd/relayctl ledger-replay -config config/relay.prod.yaml -stages orders
  RELAY_DATABASE_URL=postgres://... go run ./cmd/relayctl migrate status
  go run ./cmd/relayctl migrate up -config config/relay.local.yaml
  go run ./cmd/relayctl migrate down -config config/relay.local.yaml -steps 1
  go run ./cmd/relayctl performance-rebuild -config config/relay.prod.yaml -accounts 307000051387 -date-from 20260729 -date-to 20260731 -persist
  RELAY_CONFIG_PATH=config/relay.local.yaml go run ./cmd/relayctl redis-probe
  go run ./cmd/relayctl redis-probe -config config/relay.local.yaml -samples 2
  go run ./cmd/relayctl redis-probe -config config/relay.local.yaml -stream-prefix relay:prod:v1:huaxin:00030484
  go run ./cmd/relayctl redis-scan -config config/relay.prod.yaml`)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
