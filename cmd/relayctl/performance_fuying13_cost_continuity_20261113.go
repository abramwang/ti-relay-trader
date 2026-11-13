package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"os"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

const fuyingCostChainPath = "outputs/recovery/fuying13_settlement_20261110/cost_chain_cash_v12.json"
const fuyingCostChainHash = "aacd65f5560e0ca503d8410d06497494901b7f0d44e5dd6f5179978218dcf2ad"

func runFuyingCostContinuity(args []string) error {
	f := flag.NewFlagSet("performance-fuying13-cost-continuity-20261113", flag.ContinueOnError)
	configPath := f.String("config", "", "production config; read-only transaction")
	output := f.String("output", "", "new private continuity report")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	var frozen struct {
		Account     string                         `json:"account_id"`
		ReadOnly    bool                           `json:"database_read_only"`
		Writes      int                            `json:"database_writes"`
		Commands    int                            `json:"broker_commands"`
		Publishable bool                           `json:"economic_nav_publishable"`
		Complete    bool                           `json:"cost_chain_complete"`
		Costs       []performance.CostLedgerResult `json:"costs"`
	}
	if err := readPinnedJSON(fuyingCostChainPath, fuyingCostChainHash, &frozen); err != nil {
		return err
	}
	if frozen.Account != fuying13Account || !frozen.ReadOnly || frozen.Writes != 0 || frozen.Commands != 0 || frozen.Publishable || !frozen.Complete {
		return errors.New("frozen cost evidence scope changed")
	}
	// Frozen historical calendar coverage, not a new calendar service or weekend heuristic.
	dates := strings.Fields("2026-10-12 2026-10-13 2026-10-14 2026-10-15 2026-10-16 2026-10-19 2026-10-20 2026-10-21 2026-10-22 2026-10-23 2026-10-26 2026-10-27 2026-10-28 2026-10-29 2026-10-30 2026-11-02 2026-11-03 2026-11-04 2026-11-05 2026-11-06 2026-11-09 2026-11-10")
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("production ledger required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	repo := ledger.NewRepository(tx)
	anchor, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: fuying13Account, BeforeDate: dates[0]})
	if err != nil {
		return err
	}
	review, err := performance.ReviewCostContinuity(fuying13Account, "2026-10-09", anchor, dates, frozen.Costs)
	if err != nil {
		return err
	}
	var audit []ledger.T0Allocation
	var missing []string
	for _, day := range frozen.Costs {
		history, err := repo.ListT0AllocationHistory(ctx, fuying13Account, day.TradeDate)
		if err != nil {
			return err
		}
		active, err := ledger.ResolveT0Allocations(history)
		if err != nil {
			return err
		}
		audit = append(audit, active...)
		stored := map[string]bool{}
		for _, link := range active {
			stored[link.LinkID] = true
		}
		for _, id := range day.AttributionLinkIDs {
			if !stored[id] {
				missing = append(missing, day.TradeDate+":"+id)
			}
		}
	}
	sort.Strings(missing)
	if err := writeETFTransferReviewFile(*output, map[string]any{
		"generated_at": timeutil.Now(), "account_id": fuying13Account, "source_sha256": fuyingCostChainHash,
		"review": review, "stored_opening_anchor": anchor, "active_attribution_audits": audit,
		"unpersisted_attribution_link_ids": missing, "cost_states_publishable": false,
		"economic_nav_publishable": false, "database_writes": 0, "broker_commands": 0,
		"scope": "frozen cost structure versus stored opening; not new market sampling or final economic verification",
	}); err != nil {
		return err
	}
	return writeJSON(map[string]any{"days": review.Days, "rows": review.Rows, "carried_lots": review.CarriedLots,
		"basis_movements": len(review.Movements), "unpersisted_attribution_links": len(missing),
		"rounding_differences": len(review.RoundingDifferences), "exact_cost_identities": review.ExactCostIdentities,
		"structural_continuity_verified": review.Verified, "cost_states_publishable": false, "economic_nav_publishable": false, "database_writes": 0})
}
