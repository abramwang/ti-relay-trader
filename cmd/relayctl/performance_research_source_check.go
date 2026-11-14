package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func readResearchSourceBaseline(path string) (*performance.ResearchSourcePreflight, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, errors.New("invalid baseline file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	var r performance.ResearchSourcePreflight
	if err := decoder.Decode(&r); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing baseline data")
	}
	hash, err := performance.SourceObservationHash(r.Observation)
	if err != nil || hash != r.ObservationSHA256 || r.Schema != "relay.research_source_preflight.v1" {
		return nil, errors.New("baseline integrity failure")
	}
	return &r, nil
}

func runResearchSourceCheck(args []string) error {
	f := flag.NewFlagSet("performance-research-source-check", flag.ContinueOnError)
	configPath := f.String("config", "", "explicit environment config; read-only")
	account := f.String("account", "", "explicit configured account")
	version := f.String("version", "", "explicit archived research version")
	output := f.String("output", "", "new private source preflight JSON")
	baseline := f.String("baseline", "", "optional previous observation JSON; not an original calculation manifest")
	publication := f.Bool("publication-review", false, "include read-only cost semantics and publication gates; never publish")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" {
		return errors.New("config, account, version and output required")
	}
	for _, value := range []string{*account, *version} {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 160 || strings.ContainsRune(value, 0) {
			return errors.New("explicit account and version required")
		}
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	var prior *performance.ResearchSourcePreflight
	var err error
	if *baseline != "" {
		prior, err = readResearchSourceBaseline(*baseline)
		if err != nil {
			return err
		}
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if _, ok := cfg.AccountRoute(*account); !ok {
		return errors.New("account route not configured")
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
	var database string
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != cfg.Database.ExpectedName {
		return errors.New("connected database identity mismatch")
	}
	comparison, err := performance.ReadResearchCostComparison(ctx, tx, *account, *version)
	if err != nil {
		return err
	}
	archived, err := ledger.NewRepository(tx).GetResearchCostVersion(ctx, *account, *version)
	if err != nil {
		return err
	}
	observation, err := performance.ReadResearchSourceObservation(ctx, tx, performance.ResearchSourceObservation{Environment: string(cfg.Service.Environment), Database: database, AccountID: *account, VersionID: *version, PayloadSHA256: archived.PayloadSHA256, DateFrom: archived.AnchorDate, DateTo: archived.DateTo})
	if err != nil {
		return err
	}
	report, err := performance.ReviewResearchSources(observation, prior, comparison)
	if err != nil {
		return err
	}
	var result any = report
	summary := report.Summary()
	if *publication {
		review, err := performance.ReviewResearchCostPublication(archived, comparison, report)
		if err != nil {
			return err
		}
		result, summary = review, review.Summary()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := writeETFTransferReviewFile(*output, result); err != nil {
		return err
	}
	return writeJSON(summary)
}
