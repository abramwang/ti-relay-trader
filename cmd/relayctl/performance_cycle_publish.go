package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"time"
)

func runCycleReviewPublish(args []string) error {
	f := flag.NewFlagSet("performance-cycle-review-publish", flag.ContinueOnError)
	configPath := f.String("config", "", "explicit config")
	env := f.String("environment", "", "expected environment")
	input := f.String("input", "", "sealed report")
	sha := f.String("sha256", "", "report digest")
	version := f.String("version", "", "immutable version")
	output := f.String("output", "", "new private receipt")
	apply := f.Bool("apply", false, "publish review only; never NAV")
	migrate := f.Bool("allow-cycle-migration", false, "allow only migration 43")
	backup := f.String("backup", "", "fresh full backup")
	backupSHA := f.String("backup-sha256", "", "backup digest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *env == "" || *input == "" || len(*sha) != 64 || *version == "" || *output == "" || (*migrate && !*apply) {
		return errors.New("explicit config/environment/input/sha256/version/output required")
	}
	for _, p := range []string{*output, *output + ".commit.json"} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return errors.New("output exists")
		}
	}
	var source performance.CycleReviewSource
	if err := readPinnedJSON(*input, *sha, &source); err != nil {
		return err
	}
	v, err := performance.BuildCycleReviewVersion(source, *version, *sha)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if string(cfg.Service.Environment) != *env {
		return errors.New("environment mismatch")
	}
	if _, ok := cfg.AccountRoute(v.AccountID); !ok {
		return errors.New("review account outside selected environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if *apply {
		if err := validateResearchBackup(ctx, *backup, *backupSHA); err != nil {
			return err
		}
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*apply, Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil || name != cfg.Database.ExpectedName {
		return errors.New("database mismatch")
	}
	if *apply {
		if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('relay.cycle_review_publication'))`); err != nil {
			return err
		}
	}
	var exists, registered bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.performance_cycle_reviews') IS NOT NULL,EXISTS(SELECT 1 FROM relay_schema_migrations WHERE version=43 AND name='performance_cycle_reviews')`).Scan(&exists, &registered); err != nil {
		return err
	}
	if exists != registered {
		return errors.New("cycle review schema/registry mismatch")
	}
	if *apply && !exists {
		if !*migrate {
			return errors.New("migration 43 explicitly required")
		}
		all, err := dbmigrations.LoadDir("migrations/postgres")
		if err != nil {
			return err
		}
		m, ok := dbmigrations.FindByVersion(all, 43)
		if !ok || m.Name != "performance_cycle_reviews" || fmt.Sprintf("%x", sha256.Sum256([]byte(m.UpSQL))) != "2c96999ece040312e28e64ef1d7baffd2905b3132884c41992518f85d426e424" {
			return errors.New("migration 43 unavailable")
		}
		if _, err := tx.ExecContext(ctx, m.UpSQL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO relay_schema_migrations(version,name) VALUES(43,$1)`, m.Name); err != nil {
			return err
		}
	}
	if *apply {
		if _, err := ledger.NewRepository(tx).AppendCycleReviewVersion(ctx, v); err != nil {
			return err
		}
	}
	report := map[string]any{"account_id": v.AccountID, "version_id": v.VersionID, "environment": *env, "database": name, "source_sha256": *sha, "payload_sha256": v.PayloadSHA256, "migration_needed": !exists, "cycles": len(source.Profit.Profits), "applied": false, "nav_writes": 0, "broker_commands": 0}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	if *apply {
		if err := tx.Commit(); err != nil {
			return err
		}
		saved, err := ledger.NewRepository(db).GetCycleReviewVersion(ctx, v.AccountID, v.VersionID)
		if err != nil {
			return err
		}
		if saved.PayloadSHA256 != v.PayloadSHA256 || fmt.Sprintf("%x", sha256.Sum256(saved.Payload)) != v.PayloadSHA256 {
			return errors.New("publication readback differs")
		}
		report["applied"] = true
		report["published_at"] = saved.CreatedAt
		if err := writeETFTransferReviewFile(*output+".commit.json", report); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
