package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

// Accept only an ordinary CORE endpoint. This does not promote the historical
// research version or enable the special T0 preview paths in the normal engine.
func cleanContinuationCost(c performance.CostLedgerResult, account, date string) error {
	if c.AccountID != account || c.TradeDate != date || c.Status != "calculated" || c.Persisted || len(c.Positions) == 0 ||
		c.Summary.QuantityBreaks != 0 || c.Summary.BlockedItems != 0 || c.Summary.EstimatedItems != 0 || c.Summary.MissingFeeItems != 0 ||
		c.Summary.T0CostBuckets != 0 || c.Summary.T0ResidualBuckets != 0 {
		return errors.New("cost endpoint is not an eligible clean CORE anchor")
	}
	for _, flag := range c.QualityFlags {
		if flag != "reverse_repo_excluded_from_position_cost" {
			return fmt.Errorf("unreviewed continuation quality flag: %s", flag)
		}
	}
	for _, row := range c.Positions {
		if row.AccountID != account || row.TradeDate != date || row.CostBucket != "CORE" || row.Status != "calculated" || row.QuantityResidual != 0 || len(row.QualityFlags) != 0 {
			return errors.New("cost row is not a clean CORE position")
		}
	}
	return nil
}

type costContinuationDay struct {
	Date         string                            `json:"trade_date"`
	Cost         performance.CostLedgerResult      `json:"cost"`
	NAV          performance.EconomicNAVResult     `json:"native_nav_diagnostic"`
	Observations []ledger.AssetPositionObservation `json:"asset_observations"`
	Cash         []ledger.CashLedgerEntry          `json:"cash_entries"`
}

func runCostContinuation(args []string) error {
	f := flag.NewFlagSet("performance-cost-continue", flag.ContinueOnError)
	configPath := f.String("config", "", "explicit environment config, no switching")
	account := f.String("account", "", "explicit account")
	version := f.String("version", "", "stored research version, no latest fallback")
	through := f.String("through", "", "last date YYYYMMDD, at most 31 days after stored anchor")
	output := f.String("output", "", "new private JSON report, no apply option")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *account == "" || *version == "" || *output == "" || strings.TrimSpace(*account) != *account || strings.TrimSpace(*version) != *version {
		return errors.New("config, account, version, through and output required")
	}
	end, err := time.ParseInLocation("20060102", *through, timeutil.Location())
	now := timeutil.Now().In(timeutil.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, timeutil.Location())
	if err != nil || end.Format("20060102") != *through || !end.Before(today) {
		return errors.New("through must be a completed historical date YYYYMMDD")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	parsed, err := pgconn.ParseConfig(cfg.Database.DSN)
	if err != nil || cfg.Database.ExpectedName == "" || parsed.Database != cfg.Database.ExpectedName {
		return errors.New("configured database identity mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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
	var dbName, readOnly string
	if err := tx.QueryRowContext(ctx, `SELECT current_database(),current_setting('transaction_read_only')`).Scan(&dbName, &readOnly); err != nil || dbName != cfg.Database.ExpectedName || readOnly != "on" {
		return errors.New("connected database identity/read-only guard failed")
	}
	repo := ledger.NewRepository(tx)
	v, err := repo.GetResearchCostVersion(ctx, *account, *version)
	if err != nil {
		return err
	}
	b, err := performance.DecodeResearchCostVersion(v)
	if err != nil {
		return err
	}
	comparison, err := performance.ReadResearchCostComparison(ctx, tx, *account, *version)
	if err != nil {
		return err
	}
	if !comparison.AnchorAndAllocationsMatch {
		return errors.New("stored anchor or active attribution audits changed")
	}
	last := b.Costs[len(b.Costs)-1]
	if err := cleanContinuationCost(last, *account, v.DateTo); err != nil {
		return err
	}
	anchor, err := time.ParseInLocation("2006-01-02", v.DateTo, timeutil.Location())
	if err != nil || !end.After(anchor) || end.Sub(anchor) > 31*24*time.Hour {
		return errors.New("continuation must be after anchor and within 31 calendar days")
	}
	store := &performancePreviewStore{Repository: repo,
		costs: map[string]map[string][]ledger.PositionCostState{*account: {v.DateTo: last.Positions}},
		navs:  map[string]map[string]ledger.PerformanceNAV{}}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := settlement20261110Service(cfg, store, client)
	if err != nil {
		return err
	}
	var days []costContinuationDay
	var dates []string
	var costs []performance.CostLedgerResult
	for d := anchor.AddDate(0, 0, 1); !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		calendar, err := client.TradingDayStatus(ctx, d.Format("20060102"))
		if err != nil || !calendar.IsTradingDayKnown {
			return fmt.Errorf("unknown trading day %s: %v", day, err)
		}
		if !calendar.IsTradingDay {
			continue
		}
		item := costContinuationDay{Date: day}
		for _, kind := range []string{"open", "close"} {
			observation, err := repo.GetAssetPositionObservation(ctx, *account, day, kind)
			if err != nil {
				return err
			}
			item.Observations = append(item.Observations, observation)
		}
		item.Cash, err = repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: *account, TradeDate: day, Limit: 1000})
		if err != nil || len(item.Cash) >= 1000 {
			return fmt.Errorf("cash evidence unavailable or exceeds limit on %s: %v", day, err)
		}
		item.Cost, err = service.CalculateCostLedger(ctx, *account, day, performance.CostLedgerOptions{})
		if err != nil {
			return err
		}
		if err := cleanContinuationCost(item.Cost, *account, day); err != nil {
			return fmt.Errorf("continuation stopped on %s: %w", day, err)
		}
		store.costs[*account][day] = item.Cost.Positions
		item.NAV, err = service.CalculateEconomicNAV(ctx, *account, day, performance.EconomicNAVOptions{Persist: false})
		if err != nil {
			return err
		}
		days, dates, costs = append(days, item), append(dates, day), append(costs, item.Cost)
	}
	if len(days) == 0 {
		return errors.New("no completed trading days in requested continuation")
	}
	continuity, err := performance.ReviewCostContinuity(*account, v.DateTo, last.Positions, dates, costs)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	report := map[string]any{"schema": "relay.stored_cost_continuation.v1", "account_id": *account,
		"generated_at": timeutil.Now(), "environment": cfg.Service.Environment, "database": dbName,
		"anchor_version": v.VersionID, "anchor_sha256": v.PayloadSHA256, "anchor_date": v.DateTo,
		"date_to": end.Format("2006-01-02"), "days": days, "continuity": continuity,
		"database_read_only": true, "database_writes": 0, "broker_commands": 0, "csv_reads": 0,
		"economic_nav_publishable": false, "cost_publishable": false,
		"scope": "PG stored research anchor plus current OC ledger and Meridian; clean CORE continuation only; historical source freshness and formal publication remain separate reviews; no NAV propagation"}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	summary := make([]map[string]any, 0, len(days))
	for _, day := range days {
		summary = append(summary, map[string]any{"trade_date": day.Date, "cost_status": day.Cost.Status,
			"cost_rows": len(day.Cost.Positions), "nav_status": day.NAV.Status, "nav_pnl_diagnostic": day.NAV.NAV.AccountDayPnL, "nav_flags": day.NAV.QualityFlags})
	}
	return writeJSON(map[string]any{"database_writes": 0, "csv_reads": 0, "economic_nav_publishable": false, "days": summary})
}
