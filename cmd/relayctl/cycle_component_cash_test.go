package main

import (
	"encoding/json"
	"testing"

	"ti-relay-trader/internal/performance"
)

func componentCashFixture(t *testing.T) ([]performance.CycleComponentReview, performance.ETFCashCycleReview, componentCashProof, []componentCashRow) {
	t.Helper()
	var p componentCashProof
	err := json.Unmarshal([]byte(`{
 "schema":"fuying13_component_cash_audit.v1","account_id":"a",
 "summary":{"days":1,"cash_legs":1,"amount":"123.45"},
 "days":[{"trade_date":"2026-10-14","inventory_state":"aggregate_quantity_matched","cash_legs":[{
 "trade_date":"2026-10-14","gateway_order_id":"parent","fill_id":"fill","amount":"123.45",
 "state":"specific_historical_cash_leg_proven","additional_broker_eod_receivable":"0.00",
 "original_transfer":{"account_id":"a","trade_date":"2026-10-14","gateway_order_id":"parent","fill_id":"fill","order_id":1,"order_stream_id":"order","component_symbol":"159900","component_exchange":"SZ","component_qty":1,"price":"123.45"},
 "parent_order":{"account_id":"a","trade_date":"2026-10-14","gateway_order_id":"parent","order_id":1,"order_stream_id":"order","symbol":"159869","exchange":"SZ","trade_side":"R","business_type":"E","is_terminal":true},
 "cash_row":{"account_id":"a","broker_flow_id":"flow","actual_amount":"123.45","cash_posted_at":"2026-10-14T18:00:00+08:00","raw_fields":{"test":"synthetic"}}
 }]}]}`), &p)
	if err != nil {
		t.Fatal(err)
	}
	r := []performance.CycleComponentReview{{CycleID: "2026-10-14/159869.SZ", Gaps: []performance.CycleComponentGap{{ParentID: "parent", SecurityID: "159900.SZ", TransferQty: 1, RemainingQty: 1}}}}
	c := performance.ETFCashCycleReview{AccountID: "a", Cycles: []performance.ETFCashCycle{{CycleID: r[0].CycleID, FlowIDs: []string{"flow"}}}}
	m := p.Days[0].Legs[0].Cash
	m.SecurityID, m.SourceDate = "159869.SZ", "20261014"
	return r, c, p, []componentCashRow{m}
}

func TestCycleComponentCashExplanation(t *testing.T) {
	r, c, p, m := componentCashFixture(t)
	if err := attachCycleComponentCash(r, c, p, m); err != nil {
		t.Fatal(err)
	}
	x := r[0].Gaps[0].Cash
	if x == nil || x.AmountCents != 12345 || x.FlowID != "flow" || !x.AlreadyIncluded {
		t.Fatal(x)
	}
	for _, kind := range []string{"account", "date", "parent", "transfer", "order", "amount", "raw", "posting", "scope", "cycle-flow", "duplicate", "coverage", "physical", "later", "quantity", "final", "intraday", "reapply"} {
		t.Run(kind, func(t *testing.T) {
			r, c, p, m := componentCashFixture(t)
			l := &p.Days[0].Legs[0]
			switch kind {
			case "account":
				l.Cash.AccountID = "other"
			case "date":
				l.Date = "2026-10-15"
			case "parent":
				l.Order.Parent = "other"
			case "transfer":
				l.Transfer.Fill = "other"
			case "order":
				l.Transfer.StreamID = "other"
			case "amount":
				l.Amount = "123.46"
			case "raw":
				m[0].Raw = map[string]string{"test": "different"}
			case "posting":
				m[0].PostedAt = "2026-10-14T19:00:00+08:00"
			case "scope":
				m[0].SourceDate = "20261015"
			case "cycle-flow":
				c.Cycles[0].FlowIDs = nil
			case "duplicate":
				p.Days[0].Legs = append(p.Days[0].Legs, *l)
			case "coverage":
				p.Summary.Legs++
			case "physical":
				r[0].Gaps[0].CloseQty = 1
			case "later":
				r[0].Gaps[0].LaterSaleDate = "2026-10-15"
			case "quantity":
				r[0].Gaps[0].TransferQty++
			case "final":
				l.Final = true
			case "intraday":
				l.Intraday = true
			case "reapply":
				r[0].Gaps[0].Cash = &performance.CycleComponentCash{}
			}
			if err := attachCycleComponentCash(r, c, p, m); err == nil {
				t.Fatal("invalid explanation accepted")
			}
			if kind != "reapply" && r[0].Gaps[0].Cash != nil {
				t.Fatal("partial attachment on failure")
			}
		})
	}
}
