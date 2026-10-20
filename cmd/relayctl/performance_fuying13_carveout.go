package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

const fuyingCarveoutFormula = "broker_cash_repo_carveout.v1"
const fuyingCarveoutSource = "broker_fuying13_cash_repo_carveout_20261020"
const fuyingCarveoutCSVHash = "b7ad9c118c9db17e2524a83c665f0cac7ac9e6d1b641d4f7f411e6455a1a9af3"

// This publisher accepts only the immutable, reviewed 65-point package, not arbitrary broker exports.
func readFuyingCarveout(path string, confirmedAt time.Time) ([]ledger.PerformanceNAV, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != fuyingCarveoutCSVHash {
		return nil, errors.New("reviewed carve-out CSV fingerprint mismatch")
	}
	return buildFuyingCarveout(data, confirmedAt)
}

func buildFuyingCarveout(data []byte, confirmedAt time.Time) ([]ledger.PerformanceNAV, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	columns := make(map[string]int)
	for i, key := range header {
		columns[key] = i
	}
	for _, key := range []string{"trade_date", "close_asset", "bank_net_flow", "strategy_net_flow", "daily_pnl", "daily_end_net_flow_convention_denominator", "daily_end_net_flow_convention_unit_nav"} {
		if _, ok := columns[key]; !ok {
			return nil, fmt.Errorf("missing reviewed column %s", key)
		}
	}
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) != 65 {
		return nil, errors.New("reviewed segment must contain exactly 65 points")
	}
	result := make([]ledger.PerformanceNAV, 0, len(rows))
	var previousClose, pnlTotal int64
	previousUnit := 1.0
	for i, row := range rows {
		value := func(key string) string { return row[columns[key]] }
		day, err := time.Parse("20060102", value("trade_date"))
		if err != nil {
			return nil, err
		}
		date := day.Format("2006-01-02")
		if (i == 0 && date != "2025-04-01") || (i == 64 && date != "2025-07-07") ||
			(i > 0 && date <= result[i-1].TradeDate) {
			return nil, errors.New("unexpected carve-out date range or order")
		}
		amounts := make(map[string]int64)
		for _, key := range []string{"close_asset", "bank_net_flow", "strategy_net_flow", "daily_pnl"} {
			if i == 0 && key == "daily_pnl" && value(key) == "" {
				continue
			}
			amounts[key], err = parseBrokerMoneyCents(value(key))
			if err != nil {
				return nil, err
			}
		}
		closeCents, bankCents, strategyCents, pnlCents := amounts["close_asset"], amounts["bank_net_flow"], amounts["strategy_net_flow"], amounts["daily_pnl"]
		unit, err := strconv.ParseFloat(value("daily_end_net_flow_convention_unit_nav"), 64)
		if err != nil || math.IsNaN(unit) || math.IsInf(unit, 0) || unit <= 0 || closeCents <= 0 {
			return nil, errors.New("invalid reviewed asset or unit NAV")
		}
		flags := []string{"user_confirmed_cash_repo_carveout", "prior_bond_strategy_excluded", "cash_posting_income_basis", "daily_end_net_flow_convention", "not_full_account_accrual_nav"}
		open := previousClose
		denominator := open
		rate := 0.0
		if i == 0 {
			if closeCents != 21803091 || bankCents != 0 || strategyCents != 0 || unit != 1 {
				return nil, errors.New("unexpected observed opening anchor")
			}
			open = closeCents
			denominator = 0
			flags = append(flags, "observed_opening_anchor_no_daily_return")
		} else {
			denominator, err = parseBrokerMoneyCents(value("daily_end_net_flow_convention_denominator"))
			if err != nil || denominator != open || open <= 0 || closeCents != open+bankCents+strategyCents+pnlCents {
				return nil, fmt.Errorf("carve-out capital bridge failed on %s", date)
			}
			rate = float64(pnlCents) / float64(denominator)
			if math.Abs(unit-previousUnit*(1+rate)) > 1e-12 {
				return nil, fmt.Errorf("carve-out unit chain failed on %s", date)
			}
		}
		if strategyCents != 0 {
			flags = append(flags, "strategy_capital_transfer_time_date_only")
		}
		if date == "2025-06-23" {
			flags = append(flags, "interest_ownership_confirmed_accrual_cutoff_unverified")
		}
		pnlTotal += pnlCents
		result = append(result, ledger.PerformanceNAV{
			AccountID: fuying13Account, TradeDate: date, Status: "finalized", FormulaVersion: fuyingCarveoutFormula,
			OpenEconomicNAV: centsFloat(open), CloseEconomicNAV: centsFloat(closeCents),
			ExternalNetFlow: centsFloat(bankCents), AccountDayPnL: centsFloat(pnlCents),
			ReturnDenominator: centsFloat(denominator), DailyReturn: rate, CumulativeNAV: unit,
			Source: fuyingCarveoutSource, FinalizedAt: confirmedAt, QualityFlags: flags,
			PnLComponents: map[string]any{
				"publication_scope": "cash_repo_excluding_prior_bonds", "ownership_confirmed": true,
				"confirmed_by": "user_20261020", "confirmed_at": confirmedAt.Format(time.RFC3339),
				"source_csv_sha256": fuyingCarveoutCSVHash, "source_csv_row": i + 2,
				"source_ref":       "docs/FUYING13_APRIL_NAV_PUBLICATION_20261020.md",
				"recurring_import": false, "observed_opening_anchor": i == 0,
				"opening_daily_pnl_known": i != 0, "full_accrual_cutoff_verified": false,
				"strategy_capital_net_flow": centsFloat(strategyCents),
				"funding_net_flow":          centsFloat(bankCents + strategyCents),
				"market_valuation":          map[string]any{"price_source": "verified_broker_cash_and_actual_repo_principal", "close_position_value": 0},
				"cash_and_repo":             map[string]any{"open_total": centsFloat(open), "close_total": centsFloat(closeCents)},
			},
		})
		previousClose, previousUnit = closeCents, unit
	}
	if pnlTotal != 53228 || previousClose != 1000144242 {
		return nil, errors.New("reviewed segment totals mismatch")
	}
	return result, nil
}

func runFuyingCarveoutPublication(args []string) error {
	flags := flag.NewFlagSet("performance-fuying13-carveout-publish", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	input := flags.String("input", "outputs/recovery/fuying13_april_carveout_20261020_final/preview.csv", "immutable reviewed CSV")
	backup := flags.String("backup", "", "verified pre-write archive")
	confirmed := flags.Bool("user-confirmed-20261020", false, "record the explicit ownership and publication authorization")
	persist := flags.Bool("persist", false, "publish the reviewed segment in one transaction")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*confirmed {
		return errors.New("explicit -user-confirmed-20261020 is required")
	}
	navs, err := readFuyingCarveout(*input, timeutil.Now())
	if err != nil {
		return err
	}
	report := map[string]any{"account_id": fuying13Account, "formula_version": fuyingCarveoutFormula, "persist": *persist, "points": len(navs), "daily_pnl_sum": 532.28, "last_unit_nav": navs[len(navs)-1].CumulativeNAV, "source_csv_sha256": fuyingCarveoutCSVHash}
	if !*persist {
		return writeJSON(report)
	}
	info, err := os.Stat(*backup)
	if err != nil || info.Size() == 0 || filepath.Ext(*backup) != ".dump" {
		return errors.New("a verified nonempty PostgreSQL -backup .dump archive is required")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction {
		return errors.New("this authorized historical publication is restricted to production")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", fuyingCarveoutSource); err != nil {
		return err
	}
	repo := ledger.NewRepository(tx)
	existing, err := repo.ListPerformanceNAVs(ctx, fuying13Account, "2025-04-01", "2025-07-07")
	if err != nil {
		return err
	}
	byDate := make(map[string]ledger.PerformanceNAV)
	for _, nav := range existing {
		byDate[nav.TradeDate] = nav
	}
	saved, skipped := 0, 0
	for _, nav := range navs {
		if old, ok := byDate[nav.TradeDate]; ok {
			if !sameFuyingCarveout(old, nav) {
				return fmt.Errorf("refusing to overwrite a different NAV on %s", nav.TradeDate)
			}
			skipped++
			continue
		}
		if _, err := repo.UpsertPerformanceNAV(ctx, nav); err != nil {
			return err
		}
		saved++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	report["saved"], report["unchanged"], report["backup"] = saved, skipped, *backup
	return writeJSON(report)
}

func sameFuyingCarveout(old, next ledger.PerformanceNAV) bool {
	if old.FormulaVersion != next.FormulaVersion || old.Source != next.Source || old.Status != next.Status ||
		old.PnLComponents["source_csv_sha256"] != fuyingCarveoutCSVHash {
		return false
	}
	left := []float64{old.OpenEconomicNAV, old.CloseEconomicNAV, old.ExternalNetFlow, old.AccountDayPnL, old.ReturnDenominator, old.DailyReturn, old.CumulativeNAV, old.SettlementAdjustment}
	right := []float64{next.OpenEconomicNAV, next.CloseEconomicNAV, next.ExternalNetFlow, next.AccountDayPnL, next.ReturnDenominator, next.DailyReturn, next.CumulativeNAV, next.SettlementAdjustment}
	for i := range left {
		if math.Abs(left[i]-right[i]) > 1e-9 {
			return false
		}
	}
	canonical := func(nav ledger.PerformanceNAV) ([]byte, error) {
		components := make(map[string]any, len(nav.PnLComponents))
		for key, value := range nav.PnLComponents {
			if key != "confirmed_at" {
				components[key] = value
			}
		}
		return json.Marshal(map[string]any{"components": components, "quality_flags": nav.QualityFlags})
	}
	oldJSON, oldErr := canonical(old)
	nextJSON, nextErr := canonical(next)
	return oldErr == nil && nextErr == nil && bytes.Equal(oldJSON, nextJSON)
}
