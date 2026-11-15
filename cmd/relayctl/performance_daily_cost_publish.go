package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"os"
	"reflect"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
)

type dailyCostPlan struct {
	Schema      string                         `json:"schema"`
	Account     string                         `json:"account_id"`
	Environment string                         `json:"environment"`
	Database    string                         `json:"database"`
	ReadOnly    bool                           `json:"database_read_only"`
	Complete    bool                           `json:"cost_chain_complete"`
	Writes      int                            `json:"database_writes"`
	Commands    int                            `json:"broker_commands"`
	Publishable bool                           `json:"economic_nav_publishable"`
	Error       string                         `json:"error"`
	Formal      []ledger.PositionCostState     `json:"formal_costs"`
	Costs       []performance.CostLedgerResult `json:"costs"`
}

func dailyCostCandidate(p dailyCostPlan) (performance.CostLedgerResult, []ledger.PositionCostState, error) {
	bad := errors.New("outside reviewed daily cost continuation scope")
	if p.Schema != "relay.formal_daily_review.v1" || p.Account != fuying13Account || p.Environment != "production" || p.Database != "relay_trader" || !p.ReadOnly || !p.Complete || p.Writes != 0 || p.Commands != 0 || p.Publishable || p.Error != "" || len(p.Costs) != 3 {
		return performance.CostLedgerResult{}, nil, bad
	}
	var previous []ledger.PositionCostState
	for _, row := range p.Formal {
		if row.TradeDate == "2026-11-13" {
			return performance.CostLedgerResult{}, nil, bad
		}
		if row.TradeDate == "2026-11-12" {
			previous = append(previous, row)
		}
	}
	for i, date := range []string{"2026-11-11", "2026-11-12", "2026-11-13"} {
		if p.Costs[i].TradeDate != date || cleanContinuationCost(p.Costs[i], p.Account, date) != nil {
			return performance.CostLedgerResult{}, nil, bad
		}
	}
	if verifyDailyFormalCost(p.Costs[1], previous) != nil {
		return performance.CostLedgerResult{}, nil, bad
	}
	return p.Costs[2], previous, nil
}

func runDailyCostPublish(args []string) error {
	f := flag.NewFlagSet("performance-daily-cost-publish", flag.ContinueOnError)
	configPath := f.String("config", "", "explicit production configuration")
	plan := f.String("plan", "", "sealed read-only daily review")
	sha := f.String("plan-sha256", "", "reviewed digest")
	output := f.String("output", "", "new private receipt")
	apply := f.Bool("apply", false, "insert missing November 13 costs only; never NAV")
	backup := f.String("backup", "", "fresh full PostgreSQL backup")
	backupSHA := f.String("backup-sha256", "", "backup digest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *plan == "" || len(*sha) != 64 || *output == "" {
		return errors.New("config, pinned plan and new output required")
	}
	for _, path := range []string{*output, *output + ".commit.json"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("receipt already exists")
		}
	}
	var p dailyCostPlan
	if err := readPinnedJSON(*plan, *sha, &p); err != nil {
		return err
	}
	wanted, previous, err := dailyCostCandidate(p)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if string(cfg.Service.Environment) != p.Environment || cfg.Database.ExpectedName != p.Database {
		return errors.New("environment/database mismatch")
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
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil || name != p.Database {
		return errors.New("connected database mismatch")
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	if *apply {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE performance_position_cost_states IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
	}
	repo := ledger.NewRepository(tx)
	actual, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: p.Account, TradeDate: "2026-11-12"})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(normalizedCoreRows(actual), normalizedCoreRows(previous)) {
		return errors.New("formal previous-day evidence changed")
	}
	existing, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: p.Account, TradeDate: wanted.TradeDate})
	if err != nil {
		return err
	}
	if len(existing) != 0 {
		return errors.New("target costs already exist; refusing overwrite")
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := settlement20261110Service(cfg, repo, client)
	if err != nil {
		return err
	}
	fresh, err := service.CalculateCostLedger(ctx, p.Account, wanted.TradeDate, performance.CostLedgerOptions{})
	if err != nil {
		return err
	}
	if cleanContinuationCost(fresh, p.Account, wanted.TradeDate) != nil || !reflect.DeepEqual(normalizedCoreRows(fresh.Positions), normalizedCoreRows(wanted.Positions)) {
		return errors.New("current inputs differ from reviewed cost plan")
	}
	n := 0
	if *apply {
		n, err = insertCoreRecoveryRows(ctx, tx, p.Account, *sha, []performance.CostLedgerResult{fresh}, false)
		if err != nil {
			return err
		}
	}
	report := map[string]any{"account_id": p.Account, "trade_date": wanted.TradeDate, "plan_sha256": *sha, "candidate_rows": len(fresh.Positions), "cost_rows_written": n, "nav_writes": 0, "cash_writes": 0, "broker_commands": 0, "committed": false, "preserves_previous_cost_origin": true}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	if *apply {
		if err := tx.Commit(); err != nil {
			return err
		}
		rows, err := ledger.NewRepository(db).ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: p.Account, TradeDate: wanted.TradeDate})
		if err != nil {
			return err
		}
		expected := append([]ledger.PositionCostState(nil), fresh.Positions...)
		for i := range expected {
			expected[i].Source = "relay.core_cost_recovery:" + *sha
		}
		if !reflect.DeepEqual(normalizedCoreRows(rows), normalizedCoreRows(expected)) {
			return errors.New("commit succeeded but readback differs")
		}
		report["committed"] = true
		if err := writeETFTransferReviewFile(*output+".commit.json", report); err != nil {
			return err
		}
	}
	return writeJSON(report)
}
