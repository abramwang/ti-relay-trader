package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

func readResearchCostComparison(ctx context.Context, tx *sql.Tx, account, version string) (performance.ResearchCostComparison, error) {
	return performance.ReadResearchCostComparison(ctx, tx, account, version)
}

func runResearchCostCompare(args []string) error {
	f := flag.NewFlagSet("performance-research-cost-compare", flag.ContinueOnError)
	configPath := f.String("config", "", "environment config; read-only, no switching")
	account := f.String("account", "", "explicit account")
	version := f.String("version", "", "explicit stored research version; no latest fallback")
	output := f.String("output", "", "new private comparison JSON")
	csvOutput := f.String("csv", "", "optional new private row comparison CSV")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" {
		return errors.New("config, account, version and output are required")
	}
	for _, s := range []string{*account, *version} {
		if s == "" || strings.TrimSpace(s) != s || len(s) > 160 || strings.ContainsRune(s, 0) {
			return errors.New("explicit account and version are required")
		}
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	if *csvOutput != "" {
		a, e1 := filepath.Abs(*output)
		b, e2 := filepath.Abs(*csvOutput)
		if e1 != nil || e2 != nil || a == b {
			return errors.New("JSON and CSV paths must differ")
		}
		if _, err := os.Lstat(*csvOutput); !errors.Is(err, os.ErrNotExist) {
			return errors.New("CSV output must not exist")
		}
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	parsed, err := pgconn.ParseConfig(cfg.Database.DSN)
	if err != nil || cfg.Database.ExpectedName == "" || parsed.Database != cfg.Database.ExpectedName {
		return errors.New("configured database identity mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil || name != cfg.Database.ExpectedName {
		return errors.New("connected database identity mismatch")
	}
	start := time.Now()
	r, err := readResearchCostComparison(ctx, tx, *account, *version)
	if err != nil {
		return err
	}
	if *csvOutput != "" {
		if err := writeResearchCostCSV(*csvOutput, r); err != nil {
			return err
		}
	}
	if err := writeETFTransferReviewFile(*output, map[string]any{"generated_at": timeutil.Now(), "database": name, "environment": cfg.Service.Environment, "database_read_only": true, "database_writes": 0, "broker_commands": 0, "elapsed_ms": time.Since(start).Milliseconds(), "comparison": r}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish read-only comparison: %w", err)
	}
	return writeJSON(map[string]any{"account_id": r.AccountID, "version_id": r.VersionID, "days": len(r.Days), "counts": r.Counts, "anchor_and_allocations_match": r.AnchorAndAllocationsMatch, "evidence_changes": len(r.EvidenceChanges), "economic_nav_publishable": false, "database_writes": 0, "broker_commands": 0})
}
