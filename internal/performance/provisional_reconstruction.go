package performance

import (
	"errors"
	"fmt"
	"math"

	"ti-relay-trader/internal/ledger"
)

const ProvisionalReconstructionFormula = "broker_reconstructed_provisional_nav.v1"
const ProvisionalReconstructionFlag = "user_authorized_provisional_reconstruction"

// ReconstructionAssetDay is an explicitly reviewed asset observation, not a
// final-clearing assertion. External cash flows require a separate timing review.
type ReconstructionAssetDay struct {
	Date                    string         `json:"trade_date"`
	ReportedClose           float64        `json:"reported_close"`
	ObservedOpen            float64        `json:"observed_open"`
	ReceivableCents         int64          `json:"known_receivable_cents"`
	PayableCents            int64          `json:"known_payable_cents"`
	InferredReceivableCents int64          `json:"inferred_receivable_cents"`
	InferredPayableCents    int64          `json:"inferred_payable_cents"`
	OtherCents              int64          `json:"unclassified_component_cents"`
	UnresolvedFlows         []string       `json:"unresolved_cash_source_ids"`
	Source                  string         `json:"source"`
	InputSHA                string         `json:"input_sha256"`
	Evidence                map[string]any `json:"evidence"`
}

// BuildProvisionalReconstruction deliberately keeps unverified inclusion and
// coverage visible. It never sets account_ledger_verified or finalized status.
func BuildProvisionalReconstruction(anchor ledger.PerformanceNAV, dates []string, days []ReconstructionAssetDay, authorization map[string]any) ([]ledger.PerformanceNAV, error) {
	if anchor.AccountID == "" || anchor.Status == "blocked" || anchor.CloseEconomicNAV <= 0 || anchor.CumulativeNAV <= 0 || len(days) == 0 || len(days) != len(dates) || authorization["approved"] != true || authorization["scope"] != "provisional_only" {
		return nil, errors.New("reviewed anchor, calendar and explicit provisional authorization required")
	}
	previous, unit, previousDate := anchor.CloseEconomicNAV, anchor.CumulativeNAV, anchor.TradeDate
	var result []ledger.PerformanceNAV
	for i, d := range days {
		if _, _, err := parseTradeDate(d.Date); err != nil || d.Date != dates[i] || d.Date <= previousDate || d.Source == "" || len(d.InputSHA) != 64 || d.ReceivableCents < 0 || d.PayableCents < 0 || d.InferredReceivableCents < 0 || d.InferredPayableCents < 0 {
			return nil, fmt.Errorf("invalid reconstruction observation on %s", d.Date)
		}
		if (d.InferredReceivableCents != 0 || d.InferredPayableCents != 0) && authorization["cash_source_policy"] != "same_etf_prior_redemption_and_unique_pcf_match" {
			return nil, errors.New("inferred cash timing requires explicit matching policy")
		}
		close := roundMoney(d.ReportedClose + float64(d.ReceivableCents-d.PayableCents+d.InferredReceivableCents-d.InferredPayableCents)/100)
		for _, n := range []float64{previous, unit, close, d.ReportedClose, d.ObservedOpen} {
			if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n >= 1e12 {
				return nil, errors.New("invalid reconstruction financial value")
			}
		}
		pnl := roundMoney(close - previous)
		rate := roundRatio(pnl / previous)
		unit = roundRatio(unit * (1 + rate))
		flags := []string{ProvisionalReconstructionFlag, "etf_asset_inclusion_unverified", "private_settlement_coverage_unverified", "strategy_attribution_pending"}
		if d.OtherCents != 0 {
			flags = append(flags, "broker_asset_components_unclassified")
		}
		if len(d.UnresolvedFlows) > 0 {
			flags = append(flags, "cash_source_date_unresolved")
		}
		if d.InferredReceivableCents != 0 || d.InferredPayableCents != 0 {
			flags = append(flags, "cash_source_date_inferred")
		}
		if d.Source == "oc_saved_close_and_meridian" && math.Abs(d.ObservedOpen-previous) > .000001 {
			flags = append(flags, "overnight_asset_difference_unclassified")
		}
		result = append(result, ledger.PerformanceNAV{
			AccountID: anchor.AccountID, TradeDate: d.Date, IsCurrent: true, Status: "provisional", FormulaVersion: ProvisionalReconstructionFormula,
			OpenEconomicNAV: previous, CloseEconomicNAV: close, AccountDayPnL: pnl, ReturnDenominator: previous, DailyReturn: rate, CumulativeNAV: unit,
			Source: "relay.provisional_reconstruction", QualityFlags: flags,
			PnLComponents: map[string]any{
				"account_ledger_verified": false, "continuity_verified": true, "authorization": authorization,
				"reconstruction": map[string]any{"reported_close_preserved": d.ReportedClose, "known_receivable": float64(d.ReceivableCents) / 100, "known_payable": float64(d.PayableCents) / 100,
					"inferred_receivable": float64(d.InferredReceivableCents) / 100, "inferred_payable": float64(d.InferredPayableCents) / 100,
					"unclassified_broker_component": float64(d.OtherCents) / 100, "source": d.Source, "input_sha256": d.InputSHA, "source_evidence": d.Evidence,
					"asset_inclusion_verified": false, "final_settlement_verified": false, "unresolved_cash_source_ids": d.UnresolvedFlows,
					"observed_open_asset": d.ObservedOpen, "observed_open_minus_continuous_open": roundMoney(d.ObservedOpen - previous),
					"external_flow_scope": "reviewed_no_external_flows", "recurring_csv_import": false},
				"market_valuation": map[string]any{"price_source": d.Source, "broker_cost_usage": "excluded"},
			},
		})
		if fees, ok := d.Evidence["broker_statement_fees"]; ok {
			result[len(result)-1].PnLComponents["broker_statement_fees"] = fees
		}
		previous, previousDate = close, d.Date
	}
	return result, nil
}
