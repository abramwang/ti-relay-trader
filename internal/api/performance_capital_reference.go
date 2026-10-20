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
	populateRetainedCapitalReference(ref, nav)
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

func populateRetainedCapitalReference(ref *ledger.PerformanceCapitalReference, nav ledger.PerformanceNAV) {
	ref.RetainedCapitalStatus = "unavailable"
	denominator := nav.ReturnDenominator
	if denominator == 0 && boolFromMap(nav.PnLComponents, "observed_opening_anchor") && nav.AccountDayPnL == 0 && finiteCapital(nav.OpenEconomicNAV) && nav.OpenEconomicNAV > 0 {
		denominator = nav.OpenEconomicNAV
	}
	if nav.Status == "blocked" || !finiteCapital(nav.AccountDayPnL) || !finiteCapital(denominator) || denominator <= 0 {
		return
	}
	rows, _ := performanceExternalFlowDetails(nav)
	weightedWithdrawals, net, withdrawals := 0.0, 0.0, 0
	for _, row := range rows {
		amount, ok := floatFromAny(row["amount"])
		if !ok || !finiteCapital(amount) {
			ref.RetainedCapitalStatus = "incomplete_flow_evidence"
			return
		}
		net += amount
		if amount >= 0 {
			continue
		}
		withdrawals++
		weight, ok := floatFromAny(row["weight"])
		if !ok || !finiteCapital(weight) || weight < 0 || weight > 1 {
			ref.RetainedCapitalStatus = "missing_withdrawal_weights"
			return
		}
		weightedWithdrawals -= amount * weight
	}
	if withdrawals > 0 && math.Abs(net-nav.ExternalNetFlow) > .011 {
		ref.RetainedCapitalStatus = "incomplete_flow_evidence"
		return
	}
	if withdrawals == 0 && nav.ExternalNetFlow < 0 {
		// Approved early history explicitly applies all bank cash at day end.
		// Its withdrawal weight is zero; do not invent an intraday timestamp.
		if stringFromMap(nav.PnLComponents, "return_policy") != "daily_end_net_flow_convention" && nav.FormulaVersion != "broker_cash_repo_carveout.v1" {
			ref.RetainedCapitalStatus = "missing_withdrawal_weights"
			return
		}
	}
	capital := math.Round((denominator+weightedWithdrawals)*1e6) / 1e6
	rate := nav.AccountDayPnL / capital
	if !finiteCapital(capital) || capital <= 0 || !finiteCapital(rate) {
		return
	}
	ref.RetainedCapital, ref.RetainedCapitalReturn = &capital, &rate
	ref.RetainedCapitalStatus = "available"
}

// This selected-range diagnostic index is deliberately separate from official
// NAV compounding and summaries. A missing day invalidates the remaining chain.
func buildCapitalReferenceSeries(series []ledger.DailyPerformance) {
	index, peak := 1.0, 1.0
	interrupted := false
	for i := range series {
		item := &series[i]
		ref := item.CapitalReference
		if ref == nil {
			interrupted = true
			continue
		}
		ref.Index, ref.Drawdown = nil, nil
		ref.SeriesPolicy = "retained_withdrawal_capital_chain.v1"
		ref.SeriesStatus = "interrupted"
		if item.PerformanceStatus == "blocked" || !isOfficialPerformanceNAVFormula(item.FormulaVersion) ||
			ref.RetainedCapitalReturn == nil || !finiteCapital(*ref.RetainedCapitalReturn) || *ref.RetainedCapitalReturn <= -1 {
			interrupted = true
		}
		if interrupted {
			continue
		}
		index *= 1 + *ref.RetainedCapitalReturn
		if !finiteCapital(index) || index <= 0 {
			interrupted = true
			continue
		}
		if index > peak {
			peak = index
		}
		point, drawdown := index, index/peak-1
		ref.Index, ref.Drawdown = &point, &drawdown
		ref.SeriesStatus = "available"
	}
}

func performanceCapitalReferenceCSV(ref *ledger.PerformanceCapitalReference) []string {
	if ref == nil {
		return make([]string, 16)
	}
	return []string{
		formatCSVFloat(ref.Capital), formatCSVFloat(ref.FormalReturnDenominator),
		formatCSVOptionalFloat(ref.DailyReturn), formatCSVOptionalFloat(ref.CapitalAfterNetFlow),
		formatCSVOptionalFloat(ref.PositionValue), ref.PositionValueSource, ref.PositionValueDate,
		formatCSVOptionalFloat(ref.PositionRatioBefore), formatCSVOptionalFloat(ref.PositionRatioAfter),
		formatCSVOptionalFloat(ref.RetainedCapital), formatCSVOptionalFloat(ref.RetainedCapitalReturn), ref.RetainedCapitalStatus,
		formatCSVOptionalFloat(ref.Index), formatCSVOptionalFloat(ref.Drawdown), ref.SeriesPolicy, ref.SeriesStatus,
	}
}
