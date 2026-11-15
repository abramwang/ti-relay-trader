package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

type corePublishPlan struct {
	Schema      string                `json:"schema"`
	Account     string                `json:"account_id"`
	Environment string                `json:"environment"`
	Database    string                `json:"database"`
	Version     string                `json:"anchor_version"`
	SHA         string                `json:"anchor_sha256"`
	Anchor      string                `json:"anchor_date"`
	Through     string                `json:"date_to"`
	ReadOnly    bool                  `json:"database_read_only"`
	Writes      int                   `json:"database_writes"`
	Commands    int                   `json:"broker_commands"`
	CSVReads    int                   `json:"csv_reads"`
	Origin      *coreOriginReview     `json:"zero_origin_review"`
	Days        []costContinuationDay `json:"days"`
}

func normalizedCoreRows(rows []ledger.PositionCostState) []ledger.PositionCostState {
	rows = append([]ledger.PositionCostState(nil), rows...)
	for i := range rows {
		rows[i].CalculatedAt = time.Time{}
		rows[i].CreatedAt = time.Time{}
		rows[i].UpdatedAt = time.Time{}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Symbol+"."+rows[i].Exchange < rows[j].Symbol+"."+rows[j].Exchange })
	return rows
}

// Existing rows are never overwritten, including partial prior recoveries.
// Lock before checking the full target set, and leave commit/rollback to caller.
func insertCoreRecovery(ctx context.Context, tx *sql.Tx, account, proofSHA string, costs []performance.CostLedgerResult) (int, error) {
	return insertCoreRecoveryRows(ctx, tx, account, proofSHA, costs, true)
}

func insertCoreRecoveryRows(ctx context.Context, tx *sql.Tx, account, proofSHA string, costs []performance.CostLedgerResult, zeroOrigin bool) (int, error) {
	if len(proofSHA) != 64 || len(costs) == 0 {
		return 0, errors.New("missing recovery proof or costs")
	}
	seen := map[string]bool{}
	for _, c := range costs {
		if err := cleanContinuationCost(c, account, c.TradeDate); err != nil {
			return 0, err
		}
		if seen[c.TradeDate] {
			return 0, errors.New("duplicate recovery day")
		}
		seen[c.TradeDate] = true
		rows := map[string]bool{}
		for _, p := range c.Positions {
			key := p.Symbol + "." + p.Exchange
			if rows[key] {
				return 0, errors.New("duplicate cost row")
			}
			rows[key] = true
		}
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE performance_position_cost_states IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	repo := ledger.NewRepository(tx)
	for _, c := range costs {
		existing, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: account, TradeDate: c.TradeDate})
		if err != nil {
			return 0, err
		}
		if len(existing) > 0 {
			return 0, fmt.Errorf("formal costs already exist for %s; refusing overwrite", c.TradeDate)
		}
	}
	n := 0
	for i, c := range costs {
		for _, row := range c.Positions {
			row.Source = "relay.core_cost_recovery:" + proofSHA
			if i == 0 && zeroOrigin {
				row.OpeningSource = "verified_zero_origin_replay"
			}
			saved, err := repo.UpsertPositionCostState(ctx, row)
			if err != nil {
				return 0, err
			}
			if !reflect.DeepEqual(normalizedCoreRows([]ledger.PositionCostState{row}), normalizedCoreRows([]ledger.PositionCostState{saved})) {
				return 0, errors.New("stored cost differs from verified recovery")
			}
			n++
		}
	}
	return n, nil
}

func runCoreCostPublish(args []string) error {
	f := flag.NewFlagSet("performance-core-cost-publish", flag.ContinueOnError)
	configPath := f.String("config", "", "production config; no environment switching")
	planPath := f.String("plan", "", "completed zero-origin continuation JSON")
	planSHA := f.String("plan-sha256", "", "reviewed report SHA256")
	output := f.String("output", "", "new private execution report")
	apply := f.Bool("apply", false, "atomically insert verified CORE costs only; never publishes NAV")
	backup := f.String("backup", "", "fresh full PostgreSQL archive required for apply")
	backupSHA := f.String("backup-sha256", "", "archive SHA256")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *planPath == "" || len(*planSHA) != 64 || *output == "" || (*apply && (*backup == "" || len(*backupSHA) != 64)) {
		return errors.New("explicit config, pinned plan, output and apply backup required")
	}
	for _, path := range []string{*output, *output + ".commit.json"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("output and completion receipt must not exist")
		}
	}
	var p corePublishPlan
	if err := readPinnedJSON(*planPath, *planSHA, &p); err != nil {
		return err
	}
	if p.Schema != "relay.stored_cost_continuation.v1" || p.Account != fuying13Account || p.Environment != "production" || p.Database != "relay_trader" || p.Anchor != "2026-11-10" || p.Through != "2026-11-12" || len(p.Days) != 2 || p.Days[0].Date != "2026-11-11" || p.Days[1].Date != "2026-11-12" || !p.ReadOnly || p.Writes != 0 || p.Commands != 0 || p.CSVReads != 0 || p.Origin == nil || !p.Origin.Verified {
		return errors.New("outside reviewed cost-only recovery scope")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != p.Database {
		return errors.New("environment/database scope mismatch")
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
	iso := sql.LevelRepeatableRead
	if *apply {
		iso = sql.LevelSerializable
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*apply, Isolation: iso})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var database string
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != p.Database {
		return errors.New("connected database mismatch")
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	repo := ledger.NewRepository(tx)
	for _, date := range []string{p.Anchor, p.Days[0].Date, p.Days[1].Date} {
		rows, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: p.Account, TradeDate: date})
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			return fmt.Errorf("formal costs already exist for %s; refusing overwrite", date)
		}
	}
	v, err := repo.GetResearchCostVersion(ctx, p.Account, p.Version)
	if err != nil {
		return err
	}
	if v.PayloadSHA256 != p.SHA {
		return errors.New("stored anchor revision changed")
	}
	b, err := performance.DecodeResearchCostVersion(v)
	if err != nil {
		return err
	}
	if err := checkFuyingResearchEvidence(ctx, tx, b); err != nil {
		return err
	}
	fresh, err := readCoreOriginReview(ctx, tx, cfg, b)
	if err != nil {
		return err
	}
	if !fresh.Verified || !reflect.DeepEqual(fresh.SourceSHA, p.Origin.SourceSHA) || !reflect.DeepEqual(normalizedCoreRows(fresh.Rebuilt.Positions), normalizedCoreRows(p.Origin.Rebuilt.Positions)) {
		return errors.New("raw origin evidence or current anchor changed since reviewed plan")
	}
	store := &performancePreviewStore{Repository: repo, costs: map[string]map[string][]ledger.PositionCostState{p.Account: {p.Anchor: fresh.Rebuilt.Positions}}, navs: map[string]map[string]ledger.PerformanceNAV{}}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := settlement20261110Service(cfg, store, client)
	if err != nil {
		return err
	}
	costs := []performance.CostLedgerResult{fresh.Rebuilt}
	for _, day := range p.Days {
		cost, err := service.CalculateCostLedger(ctx, p.Account, day.Date, performance.CostLedgerOptions{})
		if err != nil {
			return err
		}
		if err := cleanContinuationCost(cost, p.Account, day.Date); err != nil {
			return err
		}
		if !reflect.DeepEqual(normalizedCoreRows(cost.Positions), normalizedCoreRows(day.Cost.Positions)) {
			return fmt.Errorf("current cost inputs differ from plan on %s", day.Date)
		}
		store.costs[p.Account][day.Date] = cost.Positions
		costs = append(costs, cost)
	}
	written := 0
	if *apply {
		written, err = insertCoreRecovery(ctx, tx, p.Account, *planSHA, costs)
		if err != nil {
			return err
		}
	}
	report := map[string]any{"account_id": p.Account, "generated_at": timeutil.Now(), "plan_sha256": *planSHA, "backup_sha256": *backupSHA, "source_sha256": fresh.SourceSHA, "costs": costs, "cost_rows_written": written, "nav_rows_written": 0, "cash_rows_written": 0, "broker_commands": 0, "csv_reads": 0, "economic_nav_publishable": false, "applied": *apply, "committed": false}
	// A failed receipt write cannot leave an unreported commit. The receipt says
	// committed=false; successful CLI output and DB readback prove the commit.
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	readBack := 0
	if *apply {
		for _, cost := range costs {
			rows, err := ledger.NewRepository(db).ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: p.Account, TradeDate: cost.TradeDate})
			if err != nil || len(rows) != len(cost.Positions) {
				return fmt.Errorf("commit succeeded but independent readback failed: %v", err)
			}
			for _, row := range rows {
				if row.Source != "relay.core_cost_recovery:"+*planSHA {
					return errors.New("commit succeeded but readback source differs")
				}
			}
			readBack += len(rows)
		}
	}
	completion := map[string]any{"committed": true, "applied": *apply, "plan_sha256": *planSHA, "cost_rows_written": written, "cost_rows_read_back": readBack, "nav_rows_written": 0, "cash_rows_written": 0, "economic_nav_publishable": false, "completed_at": timeutil.Now()}
	if err := writeETFTransferReviewFile(*output+".commit.json", completion); err != nil {
		return fmt.Errorf("commit succeeded; completion receipt failed: %w", err)
	}
	return writeJSON(completion)
}
