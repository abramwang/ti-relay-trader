package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func verifyDailyFormalCost(cost performance.CostLedgerResult, stored []ledger.PositionCostState) error {
	if err := cleanContinuationCost(cost, fuying13Account, cost.TradeDate); err != nil {
		return fmt.Errorf("%s: %w; status=%s persisted=%t summary=%+v flags=%v", cost.TradeDate, err, cost.Status, cost.Persisted, cost.Summary, cost.QualityFlags)
	}
	if len(stored) != len(cost.Positions) {
		return fmt.Errorf("formal cost row coverage differs %s: stored=%d replayed=%d", cost.TradeDate, len(stored), len(cost.Positions))
	}
	byKey := map[string]ledger.PositionCostState{}
	for _, p := range stored {
		key := p.Symbol + "." + p.Exchange + "/" + p.CostBucket
		if _, ok := byKey[key]; ok {
			return errors.New("duplicate formal cost")
		}
		byKey[key] = p
	}
	for _, p := range cost.Positions {
		key := p.Symbol + "." + p.Exchange + "/" + p.CostBucket
		s, ok := byKey[key]
		if !ok || s.AccountID != fuying13Account || s.TradeDate != cost.TradeDate || s.Status != "calculated" || s.QuantityResidual != 0 || len(s.QualityFlags) != 0 || !reflect.DeepEqual(coreCostFacts(p), coreCostFacts(s)) {
			return fmt.Errorf("formal cost differs: %s %s", cost.TradeDate, key)
		}
		delete(byKey, key)
	}
	return nil
}

// Deliberately bounded to the final historical gap. No preview allocations,
// synthetic cash flows, persistence option or trading client are available.
func runFuyingDailyReview(args []string) error {
	f := flag.NewFlagSet("performance-fuying13-daily-review", flag.ContinueOnError)
	configPath := f.String("config", "", "explicit production config; read-only transaction")
	output := f.String("output", "", "new private report")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" {
		return errors.New("config and new output required")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	parsed, err := pgconn.ParseConfig(cfg.Database.DSN)
	if err != nil || string(cfg.Service.Environment) != "production" || cfg.Database.ExpectedName == "" || parsed.Database != cfg.Database.ExpectedName {
		return errors.New("explicit production database identity required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	var name, readOnly string
	if err := tx.QueryRowContext(ctx, `SELECT current_database(), current_setting('transaction_read_only')`).Scan(&name, &readOnly); err != nil || name != cfg.Database.ExpectedName || readOnly != "on" {
		return errors.New("database identity/read-only guard failed")
	}
	repo := ledger.NewRepository(tx)
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := settlement20261110Service(cfg, repo, client)
	if err != nil {
		return err
	}
	var contributions []performance.ContributionResult
	var costs []performance.CostLedgerResult
	var formal []ledger.PositionCostState
	var orders []trading.Order
	var fills []trading.Fill
	costEvidence := map[string]string{}
	for _, day := range []string{"2026-11-09", "2026-11-10", "2026-11-11", "2026-11-12", "2026-11-13"} {
		// Keep all orders/fills in the bounded dates for independent receipt linkage.
		for offset := 0; ; offset += 500 {
			if offset >= 20000 {
				return errors.New("order evidence budget exceeded")
			}
			rows, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: day, History: true, Limit: 500, Cursor: strconv.Itoa(offset)})
			if err != nil {
				return err
			}
			orders = append(orders, rows...)
			if len(rows) < 500 {
				break
			}
		}
		for offset := 0; ; offset += 500 {
			if offset >= 20000 {
				return errors.New("fill evidence budget exceeded")
			}
			rows, err := repo.ListFills(ctx, trading.FillQuery{AccountID: fuying13Account, TradeDate: day, History: true, Limit: 500, Cursor: strconv.Itoa(offset)})
			if err != nil {
				return err
			}
			fills = append(fills, rows...)
			if len(rows) < 500 {
				break
			}
		}
		if day == "2026-11-09" {
			continue
		}
		stored, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: fuying13Account, TradeDate: day})
		if err != nil {
			return err
		}
		if day == "2026-11-10" {
			// November 10 is the independently published zero-origin anchor;
			// earlier research costs are intentionally not formal cost states.
			if len(stored) == 0 {
				return errors.New("formal November 10 anchor is missing")
			}
			formal = append(formal, stored...)
			costEvidence[day] = "stored_zero_origin_anchor"
			continue
		}
		cost, err := service.CalculateCostLedger(ctx, fuying13Account, day, performance.CostLedgerOptions{})
		if err != nil {
			return err
		}
		if day == "2026-11-13" && len(stored) == 0 {
			if err := cleanContinuationCost(cost, fuying13Account, day); err != nil {
				return err
			}
			costEvidence[day] = "read_only_replay_from_formal_previous_day_not_persisted"
		} else {
			if err := verifyDailyFormalCost(cost, stored); err != nil {
				return err
			}
			costEvidence[day] = "replay_matches_formal_cost"
		}
		c, err := service.CalculateContributions(ctx, fuying13Account, day)
		if err != nil {
			return err
		}
		if c.AccountID != fuying13Account || c.TradeDate != day || c.Summary.MissingItems != 0 || c.Summary.MissingFeeItems != 0 {
			return fmt.Errorf("incomplete contribution evidence: %s", day)
		}
		costs, formal, contributions = append(costs, cost), append(formal, stored...), append(contributions, c)
		fmt.Fprintf(os.Stderr, "read-only daily review %s: stored_costs=%d replayed_costs=%d contributions=%d\n", day, len(stored), len(cost.Positions), len(c.Contributions))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return writeETFTransferReviewFile(*output, map[string]any{
		"schema": "relay.formal_daily_review.v1", "account_id": fuying13Account, "generated_at": timeutil.Now(),
		"environment": cfg.Service.Environment, "database": name, "database_read_only": true,
		"database_writes": 0, "broker_commands": 0, "economic_nav_publishable": false,
		"cost_chain_complete": true, "error": "", "formal_costs": formal, "costs": costs,
		"cost_evidence": costEvidence,
		"contributions": contributions, "orders": orders, "fills": fills,
		"scope": "Formal PG costs independently replayed; current read-only attribution and repo identity evidence, not final settlement; market reads are not atomic with PG",
	})
}
