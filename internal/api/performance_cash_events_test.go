package api

import (
	"encoding/json"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
)

func TestPerformanceCashEventsPublishedEvidenceShapes(t *testing.T) {
	for _, source := range []string{"reconstructed", "statement", "native"} {
		t.Run(source, func(t *testing.T) {
			rows := []map[string]any{
				{"flow_id": "in", "amount": 33000000.0, "effective_time": "09:55:53"},
				{"flow_id": "out", "amount": -33000000.0, "effective_time": "14:32:10"},
			}
			components := map[string]any{}
			switch source {
			case "reconstructed":
				components["bank_flow_details"] = rows
			case "statement":
				components["broker_statement"] = map[string]any{"external_flow_details": rows}
			case "native":
				rows[0] = map[string]any{"entry_id": "in", "amount": 33000000.0, "effective_at": time.Date(2026, 10, 16, 1, 55, 53, 0, time.UTC)}
				components["cash_bridge"] = map[string]any{"weighted_external_flow_details": rows}
			}
			nav := ledger.PerformanceNAV{TradeDate: "2026-10-16", PnLComponents: components}
			// Match the JSON representation read from PostgreSQL, too.
			for _, persisted := range []bool{false, true} {
				if persisted {
					body, err := json.Marshal(nav)
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(body, &nav); err != nil {
						t.Fatal(err)
					}
				}
				events := performanceCashEvents(nav)
				if len(events) != 2 || events[0].Amount != 33000000 || events[1].Amount != -33000000 {
					t.Fatalf("same-day gross flows lost: %+v", events)
				}
				if events[0].EffectiveAt != "2026-10-16T09:55:53+08:00" || events[0].TimePrecision != "second" || events[0].EntryID != "in" {
					t.Fatalf("Shanghai time/identity = %+v", events[0])
				}
			}
		})
	}
}

func TestPerformanceCashEventsDateOnlyAndStrategyCapitalAreDistinct(t *testing.T) {
	nav := ledger.PerformanceNAV{TradeDate: "2025-06-24", ExternalNetFlow: 20000000,
		PnLComponents: map[string]any{"bank_flow_details": []any{}, "strategy_capital_net_flow": -18090000.0}}
	events := performanceCashEvents(nav)
	if len(events) != 2 || events[0].Kind != "external" || events[1].Kind != "strategy_capital" || events[0].Amount != 20000000 {
		t.Fatalf("cash scopes mixed: %+v", events)
	}
	for _, event := range events {
		if event.EffectiveAt != "" || event.TimePrecision != "date" || event.Granularity != "daily_net" {
			t.Fatalf("fabricated transaction or time: %+v", event)
		}
	}
	projected := overlayPerformanceNAVSeries([]ledger.DailyPerformance{{TradeDate: nav.TradeDate}}, []ledger.PerformanceNAV{
		{TradeDate: nav.TradeDate, ExternalNetFlow: nav.ExternalNetFlow, PnLComponents: nav.PnLComponents, FormulaVersion: "broker_reconstructed_economic_nav.v1", Status: "provisional"},
	})
	if projected[0].ExternalNetFlow != 20000000 || len(projected[0].CashFlowEvents) != 2 {
		t.Fatalf("series projection = %+v", projected)
	}
}

func TestPerformanceCashEventsMissingPrecisionAndPartialDetails(t *testing.T) {
	nav := ledger.PerformanceNAV{TradeDate: "2026-10-16", ExternalNetFlow: 100,
		PnLComponents: map[string]any{"cash_bridge": map[string]any{"weighted_external_flow_details": []any{
			map[string]any{"amount": 20.0, "effective_at": "2026-10-16T00:00:00+08:00"},
			map[string]any{"amount": 30.0, "effective_at": "2026-10-16T15:00:00+08:00", "effective_time_precision": "date"},
			map[string]any{"amount": "invalid"},
		}}}}
	events := performanceCashEvents(nav)
	if len(events) != 3 || events[2].Amount != 50 || events[2].Granularity != "daily_net" {
		t.Fatalf("partial details = %+v", events)
	}
	for _, event := range events {
		if event.TimePrecision != "date" || event.EffectiveAt != "" {
			t.Fatalf("invented exact time: %+v", event)
		}
	}
	if events := performanceCashEvents(ledger.PerformanceNAV{}); len(events) != 0 {
		t.Fatalf("invented cash: %+v", events)
	}
	projected := overlayPerformanceNAVSeries([]ledger.DailyPerformance{{TradeDate: "2026-10-16", ExternalNetFlow: 999, CashFlowEvents: events}}, nil)
	if projected[0].ExternalNetFlow != 0 || len(projected[0].CashFlowEvents) != 0 {
		t.Fatalf("unpublished snapshot cash leaked: %+v", projected)
	}
}

func TestPerformanceCashEventsLegacyEstimatedTimeIsNotPrecise(t *testing.T) {
	nav := ledger.PerformanceNAV{TradeDate: "2026-10-16", ExternalNetFlow: 100,
		QualityFlags: []string{"external_flow_time_estimated_mid_session"},
		PnLComponents: map[string]any{"cash_bridge": map[string]any{"weighted_external_flow_details": []any{
			map[string]any{"amount": 100.0, "effective_at": "2026-10-16T15:00:00+08:00", "weight": 0.5},
		}}}}
	events := performanceCashEvents(nav)
	if len(events) != 1 || events[0].TimePrecision != "date" || events[0].EffectiveAt != "" {
		t.Fatalf("legacy estimated time presented as exact: %+v", events)
	}
}
