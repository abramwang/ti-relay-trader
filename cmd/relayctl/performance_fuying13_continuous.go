package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

const fuyingContinuousFormula = "broker_reconstructed_economic_nav.v1"
const fuyingContinuousSource = "broker_fuying13_continuous_ledgers_20261020"
const fuyingContinuousPlanSHA = "2aaeabcb3b65fa9201bf5ddc0c038b61c1dddf42253d6cbe0bfa3a2d212b40cd"
const fuyingContinuousBaselineSHA = "2ab348c9709d81634f84e27e1c8f4a0cc1fd12dc4705ecd66825ca62ed17882c"

func readPinnedJSON(path, digest string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != digest {
		return errors.New("reviewed recovery artifact fingerprint mismatch")
	}
	return json.Unmarshal(data, target)
}

func validateFuyingContinuousNAVs(navs []ledger.PerformanceNAV) error {
	if len(navs) != 375 || navs[0].TradeDate != "2025-04-01" || navs[len(navs)-1].TradeDate != "2026-10-19" {
		return errors.New("recovery must cover the reviewed 375 funds dates")
	}
	unit := 1.0
	for i, nav := range navs {
		for _, value := range []float64{nav.OpenEconomicNAV, nav.CloseEconomicNAV, nav.ExternalNetFlow, nav.AccountDayPnL, nav.ReturnDenominator, nav.DailyReturn, nav.CumulativeNAV} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return errors.New("nonfinite recovery financial value")
			}
		}
		if nav.AccountID != fuying13Account || nav.FormulaVersion != fuyingContinuousFormula || nav.Source != fuyingContinuousSource ||
			(nav.Status != "provisional" && nav.Status != "finalized") || nav.OpenEconomicNAV <= 0 || nav.CloseEconomicNAV <= 0 ||
			nav.PnLComponents["account_ledger_verified"] != true {
			return fmt.Errorf("unverified recovery row on %s", nav.TradeDate)
		}
		if _, err := time.Parse("2006-01-02", nav.TradeDate); err != nil {
			return err
		}
		if i > 0 && (nav.TradeDate <= navs[i-1].TradeDate || math.Abs(nav.OpenEconomicNAV-navs[i-1].CloseEconomicNAV) > .000001) {
			return fmt.Errorf("noncontinuous recovery asset chain on %s", nav.TradeDate)
		}
		strategy, ok := nav.PnLComponents["strategy_capital_net_flow"].(float64)
		if !ok {
			return errors.New("missing explicit strategy capital flow")
		}
		if math.Abs(nav.CloseEconomicNAV-nav.OpenEconomicNAV-nav.ExternalNetFlow-strategy-nav.AccountDayPnL) > .00001 {
			return fmt.Errorf("recovery capital identity failed on %s", nav.TradeDate)
		}
		if i == 0 {
			if nav.CloseEconomicNAV != 218030.91 || nav.CumulativeNAV != 1 || nav.AccountDayPnL != 0 || nav.ReturnDenominator != 0 {
				return errors.New("approved opening anchor changed")
			}
		} else if nav.ReturnDenominator <= 0 || math.Abs(nav.AccountDayPnL/nav.ReturnDenominator-nav.DailyReturn) > 1e-12 {
			return fmt.Errorf("invalid capital-weighted return on %s", nav.TradeDate)
		}
		unit *= 1 + nav.DailyReturn
		if nav.DailyReturn <= -1 || math.IsNaN(unit) || math.IsInf(unit, 0) || math.Abs(unit-nav.CumulativeNAV) > 1e-9 {
			return errors.New("recovery unit NAV chain failed")
		}
	}
	return nil
}

func sameContinuousNAV(left, right ledger.PerformanceNAV) bool {
	if left.FormulaVersion != right.FormulaVersion || left.Source != right.Source || left.Status != right.Status {
		return false
	}
	a := []float64{left.OpenEconomicNAV, left.CloseEconomicNAV, left.ExternalNetFlow, left.AccountDayPnL, left.ReturnDenominator, left.DailyReturn, left.CumulativeNAV, left.SettlementAdjustment}
	b := []float64{right.OpenEconomicNAV, right.CloseEconomicNAV, right.ExternalNetFlow, right.AccountDayPnL, right.ReturnDenominator, right.DailyReturn, right.CumulativeNAV, right.SettlementAdjustment}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-9 {
			return false
		}
	}
	aJSON, errA := json.Marshal([]any{left.PnLComponents, left.QualityFlags})
	bJSON, errB := json.Marshal([]any{right.PnLComponents, right.QualityFlags})
	return errA == nil && errB == nil && bytes.Equal(aJSON, bJSON)
}

func runFuyingContinuousRecovery(args []string) error {
	flags := flag.NewFlagSet("performance-fuying13-continuous-recovery", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	planPath := flags.String("plan", "outputs/recovery/fuying13_continuous_20261020_verified/nav_plan.json", "reviewed immutable NAV plan")
	baselinePath := flags.String("baseline", "outputs/recovery/fuying13_april_publication_20261020/published_navs.json", "reviewed existing NAV snapshot")
	backup := flags.String("backup", "", "verified pre-write PostgreSQL dump")
	confirmedBy := flags.String("confirmed-by", "", "operator recording user-authorized one-time restoration")
	persist := flags.Bool("persist", false, "commit all reviewed NAV dates in one transaction")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var plan struct {
		Report map[string]any          `json:"report"`
		NAVs   []ledger.PerformanceNAV `json:"navs"`
	}
	if err := readPinnedJSON(*planPath, fuyingContinuousPlanSHA, &plan); err != nil {
		return err
	}
	if err := validateFuyingContinuousNAVs(plan.NAVs); err != nil {
		return err
	}
	var baseline struct {
		OK   bool `json:"ok"`
		Data struct {
			Count int                     `json:"count"`
			NAVs  []ledger.PerformanceNAV `json:"navs"`
		} `json:"data"`
	}
	if err := readPinnedJSON(*baselinePath, fuyingContinuousBaselineSHA, &baseline); err != nil {
		return err
	}
	if !baseline.OK || baseline.Data.Count != 82 || len(baseline.Data.NAVs) != 82 {
		return errors.New("reviewed pre-recovery 82-point snapshot is incomplete")
	}
	if *persist {
		if *confirmedBy == "" {
			return errors.New("-confirmed-by is required for persistence")
		}
		if info, err := os.Stat(*backup); err != nil || info.Size() == 0 {
			return errors.New("a verified pre-write -backup is required")
		}
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction {
		return errors.New("this reviewed recovery is restricted to production")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*persist, Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if *persist {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", fuyingContinuousSource); err != nil {
			return err
		}
	}
	repo := ledger.NewRepository(tx)
	current, err := repo.ListPerformanceNAVs(ctx, fuying13Account, "2025-04-01", "2026-10-19")
	if err != nil {
		return err
	}
	oldByDate := make(map[string]ledger.PerformanceNAV)
	for _, nav := range baseline.Data.NAVs {
		oldByDate[nav.TradeDate] = nav
	}
	byDate := make(map[string]ledger.PerformanceNAV)
	for _, nav := range current {
		if nav.FormulaVersion != fuyingContinuousFormula {
			old, ok := oldByDate[nav.TradeDate]
			if !ok || old.PerformanceNAVPK != nav.PerformanceNAVPK || old.Version != nav.Version || !old.UpdatedAt.Equal(nav.UpdatedAt) || !sameContinuousNAV(old, nav) {
				return fmt.Errorf("pre-recovery baseline changed on %s", nav.TradeDate)
			}
		}
		byDate[nav.TradeDate] = nav
	}
	for date := range oldByDate {
		if _, ok := byDate[date]; !ok {
			return fmt.Errorf("reviewed existing NAV disappeared on %s", date)
		}
	}
	saved, unchanged, replaced, inserted := 0, 0, 0, 0
	for _, next := range plan.NAVs {
		old, exists := byDate[next.TradeDate]
		if exists && old.FormulaVersion == fuyingContinuousFormula {
			if !sameContinuousNAV(old, next) {
				return fmt.Errorf("refusing to overwrite a different reconstructed NAV on %s", next.TradeDate)
			}
			unchanged++
			continue
		}
		if exists {
			replaced++
		} else {
			inserted++
		}
		if *persist {
			if _, err := repo.UpsertPerformanceNAV(ctx, next); err != nil {
				return err
			}
			saved++
		}
	}
	if *persist {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return writeJSON(map[string]any{"account_id": fuying13Account, "persist": *persist, "validated": len(plan.NAVs),
		"saved": saved, "unchanged": unchanged, "replace_current": replaced, "insert_missing": inserted,
		"confirmed_by": *confirmedBy, "backup": *backup, "plan_sha256": fuyingContinuousPlanSHA,
		"last_point": plan.NAVs[len(plan.NAVs)-1]})
}
