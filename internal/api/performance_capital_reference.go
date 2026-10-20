package api

import (
	"math"

	"ti-relay-trader/internal/ledger"
)

func overlayPerformanceCapitalReferences(series []ledger.DailyPerformance, navs []ledger.PerformanceNAV, prior *ledger.PerformanceNAV) []ledger.DailyPerformance {
	byDate := make(map[string]ledger.PerformanceNAV, len(navs))
	for _, nav := range navs {
		byDate[nav.TradeDate] = nav
	}
	for i := range series {
		series[i].CapitalReference = nil
		nav, ok := byDate[series[i].TradeDate]
		if !ok || !isOfficialPerformanceNAVFormula(nav.FormulaVersion) {
			prior = nil
			continue
		}
		series[i].CapitalReference = performanceCapitalReference(nav, prior)
		copy := nav
		prior = &copy
	}
	return series
}

func performanceCapitalReference(nav ledger.PerformanceNAV, prior *ledger.PerformanceNAV) *ledger.PerformanceCapitalReference {
	ref := &ledger.PerformanceCapitalReference{
		Basis: "daily_open_economic_nav", Capital: nav.OpenEconomicNAV,
		FormalReturnDenominator: nav.ReturnDenominator, PositionValueSource: "unavailable",
	}
	if nav.Status == "blocked" || !finiteCapital(nav.OpenEconomicNAV) || nav.OpenEconomicNAV <= 0 || !finiteCapital(nav.AccountDayPnL) {
		return ref
	}
	rate := nav.AccountDayPnL / nav.OpenEconomicNAV
	ref.DailyReturn = &rate
	strategyCapital, _ := floatFromAny(nav.PnLComponents["strategy_capital_net_flow"])
	after := math.Round((nav.OpenEconomicNAV+nav.ExternalNetFlow+strategyCapital)*100) / 100
	if finiteCapital(after) && after > 0 {
		ref.CapitalAfterNetFlow = &after
	}

	valuation, _ := nav.PnLComponents["market_valuation"].(map[string]any)
	position, available := floatFromAny(valuation["open_position_value"])
	if available && finiteCapital(position) && position >= 0 {
		ref.PositionValueSource = "open_position_value"
		ref.PositionValueDate = nav.TradeDate
	} else if prior != nil && prior.AccountID == nav.AccountID && prior.TradeDate < nav.TradeDate &&
		prior.Status != "blocked" && isOfficialPerformanceNAVFormula(prior.FormulaVersion) &&
		(prior.FormulaVersion == "broker_cash_repo_carveout.v1") == (nav.FormulaVersion == "broker_cash_repo_carveout.v1") &&
		finiteCapital(prior.CloseEconomicNAV) && math.Abs(prior.CloseEconomicNAV-nav.OpenEconomicNAV) < 0.011 {
		previousValuation, _ := prior.PnLComponents["market_valuation"].(map[string]any)
		position, available = floatFromAny(previousValuation["close_position_value"])
		if available && finiteCapital(position) && position >= 0 {
			ref.PositionValueSource = "previous_published_close_position_value"
			ref.PositionValueDate = prior.TradeDate
		}
	}
	if ref.PositionValueSource == "unavailable" {
		return ref
	}
	ref.PositionValue = &position
	beforeRatio := position / nav.OpenEconomicNAV
	ref.PositionRatioBefore = &beforeRatio
	if ref.CapitalAfterNetFlow != nil {
		afterRatio := position / *ref.CapitalAfterNetFlow
		ref.PositionRatioAfter = &afterRatio
	}
	return ref
}

func finiteCapital(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func performanceCapitalReferenceCSV(ref *ledger.PerformanceCapitalReference) []string {
	if ref == nil {
		return make([]string, 9)
	}
	return []string{
		formatCSVFloat(ref.Capital), formatCSVFloat(ref.FormalReturnDenominator),
		formatCSVOptionalFloat(ref.DailyReturn), formatCSVOptionalFloat(ref.CapitalAfterNetFlow),
		formatCSVOptionalFloat(ref.PositionValue), ref.PositionValueSource, ref.PositionValueDate,
		formatCSVOptionalFloat(ref.PositionRatioBefore), formatCSVOptionalFloat(ref.PositionRatioAfter),
	}
}
