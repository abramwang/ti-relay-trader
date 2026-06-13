package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/redisstream"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "migrate":
		if err := runMigrate(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl migrate: %v\n", err)
			os.Exit(1)
		}
	case "redis-probe":
		if err := runRedisProbe(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl redis-probe: %v\n", err)
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

func loadConfig(path string) (*config.Config, error) {
	if strings.TrimSpace(path) == "" {
		cfg := config.Default()
		return &cfg, nil
	}
	return config.Load(path)
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, `relayctl commands:
  migrate      Run PostgreSQL migration status/up/down
  redis-probe  Read-only Redis Stream probe using relay config

Examples:
  RELAY_DATABASE_URL=postgres://... go run ./cmd/relayctl migrate status
  go run ./cmd/relayctl migrate up -config config/relay.local.yaml
  go run ./cmd/relayctl migrate down -config config/relay.local.yaml -steps 1
  RELAY_CONFIG_PATH=config/relay.local.yaml go run ./cmd/relayctl redis-probe
  go run ./cmd/relayctl redis-probe -config config/relay.local.yaml -samples 2
  go run ./cmd/relayctl redis-probe -config config/relay.local.yaml -stream-prefix relay:prod:v1:huaxin:00030484`)
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
