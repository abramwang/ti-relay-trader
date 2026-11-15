package performance

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// Trading evidence contains only the acquisition used by the redemption, not
// CORE inventory or unredeemed T0 residuals. Parent fees belong to signed cash.
type ETFCycleTradingEvidence struct {
	AccountID             string   `json:"account_id"`
	CycleID               string   `json:"cycle_id"`
	AcquisitionDate       string   `json:"acquisition_date"`
	AcquisitionQuantity   int64    `json:"acquisition_quantity"`
	RedemptionQuantity    int64    `json:"redemption_quantity"`
	AcquisitionGrossCents int64    `json:"acquisition_gross_cents"`
	ComponentSaleCents    int64    `json:"component_sale_cents"`
	LaterComponentSales   int64    `json:"later_component_sale_cents"`
	OrdinaryFeeCents      int64    `json:"ordinary_execution_fee_cents"`
	FeeScope              string   `json:"fee_scope"`
	FeeBasis              string   `json:"fee_basis"`
	EvidenceRefs          []string `json:"evidence_refs"`
	QualityFlags          []string `json:"quality_flags"`
}

type ETFCycleProfit struct {
	CycleID               string                  `json:"cycle_id"`
	Trading               ETFCycleTradingEvidence `json:"trading"`
	ObservedNetCashCents  int64                   `json:"observed_net_cash_cents"`
	NetCyclePnLCents      int64                   `json:"net_cycle_pnl_cents"`
	Status                string                  `json:"status"`
	QualityFlags          []string                `json:"quality_flags"`
	AccountNAVPublishable bool                    `json:"account_nav_publishable"`
	FinalSettlement       bool                    `json:"final_settlement"`
}

// CalculateETFCycleProfits replaces IOPV/friction with observed cash and scoped
// execution cost. It does not assert private settlement completeness or daily
// attribution. Every cash cohort must have exactly one trading evidence set.
func CalculateETFCycleProfits(cash ETFCashCycleReview, trades []ETFCycleTradingEvidence) ([]ETFCycleProfit, error) {
	fail := func() ([]ETFCycleProfit, error) { return nil, errors.New("invalid ETF cycle profit evidence") }
	if cash.AccountID == "" || cash.EconomicNAVPublishable || len(cash.Cycles) == 0 || len(cash.Cycles) != len(trades) || len(trades) > 1000 {
		return fail()
	}
	byID := map[string]ETFCycleTradingEvidence{}
	for _, t := range trades {
		if t.AccountID != cash.AccountID || t.CycleID == "" || byID[t.CycleID].CycleID != "" || t.AcquisitionQuantity <= 0 || t.AcquisitionQuantity > 1000000000000 || t.AcquisitionQuantity != t.RedemptionQuantity || t.AcquisitionGrossCents <= 0 || t.FeeScope != "ordinary_buys_and_component_sales_only" || len(t.EvidenceRefs) == 0 {
			return fail()
		}
		if t.FeeBasis != "broker_cash_reconciled" && t.FeeBasis != "oc_fee_snapshot_not_final" {
			return fail()
		}
		for _, ref := range t.EvidenceRefs {
			if strings.TrimSpace(ref) == "" {
				return fail()
			}
		}
		for _, v := range []int64{t.AcquisitionGrossCents, t.ComponentSaleCents, t.LaterComponentSales, t.OrdinaryFeeCents} {
			if v < 0 || v > 100000000000000 {
				return fail()
			}
		}
		byID[t.CycleID] = t
	}
	seenCash, seenCycles := map[string]bool{}, map[string]bool{}
	result := []ETFCycleProfit{}
	for _, c := range cash.Cycles {
		t, ok := byID[c.CycleID]
		if !ok || seenCycles[c.CycleID] || c.CycleID != c.SourceDate+"/"+c.SecurityID || !cashCycleSecurity.MatchString(c.SecurityID) || c.CompleteSettlement || len(c.FlowIDs) == 0 || c.KnownSourceLegs < 0 || c.InferredSourceLegs < 0 || len(c.FlowIDs) != c.KnownSourceLegs+c.InferredSourceLegs {
			return fail()
		}
		for _, date := range []string{t.AcquisitionDate, c.SourceDate, c.LastObservedPosting, cash.ObservedThrough} {
			parsed, err := time.Parse("2006-01-02", date)
			if err != nil || parsed.Format("2006-01-02") != date {
				return fail()
			}
		}
		if t.AcquisitionDate > c.SourceDate || c.LastObservedPosting < c.SourceDate || c.LastObservedPosting > cash.ObservedThrough || c.ReceivedCents < 0 || c.PaidCents < 0 || c.ReceivedCents > 100000000000000000 || c.PaidCents > 100000000000000000 || c.NetCashCents != c.ReceivedCents-c.PaidCents {
			return fail()
		}
		seenCycles[c.CycleID] = true
		for _, id := range c.FlowIDs {
			if strings.TrimSpace(id) == "" || seenCash[id] {
				return fail()
			}
			seenCash[id] = true
		}
		t.QualityFlags = appendUnique([]string{}, t.QualityFlags...)
		t.EvidenceRefs = append([]string(nil), t.EvidenceRefs...)
		flags := append([]string(nil), t.QualityFlags...)
		flags = appendUnique(flags, "observed_cycle_profit_not_account_daily_pnl", "private_settlement_coverage_unverified")
		if c.InferredSourceLegs > 0 {
			flags = appendUnique(flags, "cash_source_date_inferred")
		}
		if t.FeeBasis == "oc_fee_snapshot_not_final" {
			flags = appendUnique(flags, "execution_fee_finality_unverified")
		}
		status := "estimated"
		if t.FeeBasis == "broker_cash_reconciled" && c.InferredSourceLegs == 0 && len(t.QualityFlags) == 0 {
			status = "observed_cash_reconciled"
		}
		result = append(result, ETFCycleProfit{CycleID: c.CycleID, Trading: t, ObservedNetCashCents: c.NetCashCents,
			NetCyclePnLCents: t.ComponentSaleCents + t.LaterComponentSales + c.NetCashCents - t.AcquisitionGrossCents - t.OrdinaryFeeCents, Status: status, QualityFlags: flags})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CycleID < result[j].CycleID })
	return result, nil
}
