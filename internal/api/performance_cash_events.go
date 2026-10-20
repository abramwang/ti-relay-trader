package api

import (
	"math"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

// Project the evidence already used by the published NAV; never infer cash from
// asset changes, ETF estimates, or internal counter transfers.
func performanceCashEvents(nav ledger.PerformanceNAV) []ledger.PerformanceCashEvent {
	components := nav.PnLComponents
	details := components["bank_flow_details"]
	source := "bank_flow_details"
	if details == nil {
		if statement, ok := components["broker_statement"].(map[string]any); ok {
			details = statement["external_flow_details"]
			source = "broker_statement.external_flow_details"
		}
	}
	if details == nil {
		if bridge, ok := components["cash_bridge"].(map[string]any); ok {
			details = bridge["weighted_external_flow_details"]
			source = "cash_bridge.weighted_external_flow_details"
		}
	}
	var rows []map[string]any
	switch values := details.(type) {
	case []map[string]any:
		rows = values
	case []any:
		for _, value := range values {
			if row, ok := value.(map[string]any); ok {
				rows = append(rows, row)
			}
		}
	}
	events := make([]ledger.PerformanceCashEvent, 0, len(rows)+2)
	total := 0.0
	for _, row := range rows {
		amount, ok := floatFromAny(row["amount"])
		if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || amount == 0 {
			continue
		}
		event := ledger.PerformanceCashEvent{
			EntryID: stringFromMap(row, "entry_id"), Kind: "external", Amount: amount,
			TimePrecision: "date", Granularity: "transaction", Source: source,
		}
		if event.EntryID == "" {
			event.EntryID = stringFromMap(row, "flow_id")
		}
		precision := stringFromMap(row, "effective_time_precision")
		if precision == "" {
			if weight, ok := floatFromAny(row["weight"]); ok && weight == 0.5 {
				for _, flag := range nav.QualityFlags {
					if flag == "external_flow_time_estimated_mid_session" {
						precision = "date"
					}
				}
			}
		}
		if precision != "date" {
			var at time.Time
			switch value := row["effective_at"].(type) {
			case time.Time:
				at = value
			case string:
				at, _ = time.Parse(time.RFC3339Nano, value)
			}
			if at.IsZero() {
				at, _ = time.ParseInLocation("2006-01-02 15:04:05", nav.TradeDate+" "+stringFromMap(row, "effective_time"), timeutil.Location())
			}
			// Older NAVs did not persist precision. Midnight may be a date-only
			// placeholder, so do not present it as a known bank transaction time.
			local := at.In(timeutil.Location())
			if !at.IsZero() && (precision == "second" || local.Hour() != 0 || local.Minute() != 0 || local.Second() != 0) {
				event.EffectiveAt = local.Format(time.RFC3339)
				event.TimePrecision = "second"
			}
		}
		events = append(events, event)
		total += amount
	}
	// Missing or incomplete detail is a daily net amount, never a fabricated
	// transaction. Keep both gross directions when same-day details net to zero.
	if remainder := math.Round((nav.ExternalNetFlow-total)*100) / 100; remainder != 0 {
		events = append(events, ledger.PerformanceCashEvent{
			Kind: "external", Amount: remainder, TimePrecision: "date",
			Granularity: "daily_net", Source: "nav.external_net_flow",
		})
	}
	if amount, ok := floatFromAny(components["strategy_capital_net_flow"]); ok && amount != 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0) {
		events = append(events, ledger.PerformanceCashEvent{
			Kind: "strategy_capital", Amount: amount, TimePrecision: "date",
			Granularity: "daily_net", Source: "nav.strategy_capital_net_flow",
		})
	}
	return events
}
