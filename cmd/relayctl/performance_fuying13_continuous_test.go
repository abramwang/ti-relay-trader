package main

import (
	"math"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
)

func syntheticContinuousNAVs() []ledger.PerformanceNAV {
	result := make([]ledger.PerformanceNAV, 375)
	start := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := range result {
		result[i] = ledger.PerformanceNAV{
			AccountID: fuying13Account, TradeDate: start.AddDate(0, 0, i).Format("2006-01-02"),
			Status: "provisional", FormulaVersion: fuyingContinuousFormula, Source: fuyingContinuousSource,
			OpenEconomicNAV: 218030.91, CloseEconomicNAV: 218030.91, ReturnDenominator: 218030.91, CumulativeNAV: 1,
			PnLComponents: map[string]any{"account_ledger_verified": true, "strategy_capital_net_flow": 0.0},
		}
	}
	result[0].ReturnDenominator = 0
	result[374].TradeDate = "2026-10-19"
	return result
}

func TestContinuousHistoryValidatesFullCapitalAndUnitChain(t *testing.T) {
	navs := syntheticContinuousNAVs()
	if err := validateFuyingContinuousNAVs(navs); err != nil {
		t.Fatal(err)
	}
	if !sameContinuousNAV(navs[1], navs[1]) {
		t.Fatal("identical reconstructed NAV must be idempotent")
	}
	changed := navs[1]
	changed.SettlementAdjustment = 1
	if sameContinuousNAV(navs[1], changed) {
		t.Fatal("changed settlement adjustment must fail idempotence guard")
	}
}

func TestContinuousHistoryRejectsChangedFinancialCoverage(t *testing.T) {
	mutations := []func([]ledger.PerformanceNAV){
		func(n []ledger.PerformanceNAV) { n[20].OpenEconomicNAV++ },
		func(n []ledger.PerformanceNAV) { n[20].ExternalNetFlow++ },
		func(n []ledger.PerformanceNAV) { n[20].CumulativeNAV = 2 },
		func(n []ledger.PerformanceNAV) { n[20].CloseEconomicNAV = math.NaN() },
		func(n []ledger.PerformanceNAV) { n[20].DailyReturn = math.Inf(1) },
		func(n []ledger.PerformanceNAV) { n[20].TradeDate = n[19].TradeDate },
		func(n []ledger.PerformanceNAV) { n[20].AccountID = "another" },
		func(n []ledger.PerformanceNAV) { delete(n[20].PnLComponents, "account_ledger_verified") },
	}
	for index, change := range mutations {
		navs := syntheticContinuousNAVs()
		change(navs)
		if err := validateFuyingContinuousNAVs(navs); err == nil {
			t.Fatalf("accepted invalid plan mutation %d", index)
		}
	}
	if err := validateFuyingContinuousNAVs(syntheticContinuousNAVs()[:374]); err == nil {
		t.Fatal("partial history must not be published as complete")
	}
}

func TestContinuousHistoryRejectsUnreviewedArtifact(t *testing.T) {
	var target any
	if err := readPinnedJSON("/nonexistent/recovery-plan.json", fuyingContinuousPlanSHA, &target); err == nil {
		t.Fatal("missing immutable artifact must fail closed")
	}
}
