package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/trading"
)

func TestFrozenFeeEvidenceLookup(t *testing.T) {
	record := func(method, body string) researchInputRecord {
		return researchInputRecord{Method: method, Request: json.RawMessage(`{"date":"2026-10-12"}`), Response: json.RawMessage(body)}
	}
	first := record("market.MarketETFCashComponents", `{"StatusCode":200,"Payload":{"data":[{"amount":"123.45"}],"meta":{"request_id":"a","next_cursor":"1"}}}`)
	second := record(first.Method, `{"StatusCode":200,"Payload":{"data":[{"amount":"123.45"}],"meta":{"request_id":"b","next_cursor":"1"}}}`)
	l := &researchEvidenceLookup{records: []researchInputRecord{first, second}}
	var got json.RawMessage
	if err := l.read(first.Method, map[string]string{"date": "2026-10-12"}, &got); err != nil {
		t.Fatal(err)
	}
	if string(first.Response) != `{"StatusCode":200,"Payload":{"data":[{"amount":"123.45"}],"meta":{"request_id":"a","next_cursor":"1"}}}` {
		t.Fatal("raw evidence mutated")
	}
	for _, body := range []string{
		`{"StatusCode":200,"Payload":{"data":[{"amount":"123.46"}],"meta":{"request_id":"b","next_cursor":"1"}}}`,
		`{"StatusCode":200,"Payload":{"data":[{"amount":"123.45"}],"meta":{"request_id":"b","next_cursor":"2"}}}`,
		`{"StatusCode":500,"Payload":{"data":[{"amount":"123.45"}],"meta":{"request_id":"b","next_cursor":"1"}}}`,
	} {
		l = &researchEvidenceLookup{records: []researchInputRecord{first, record(first.Method, body)}}
		if err := l.read(first.Method, map[string]string{"date": "2026-10-12"}, &got); err == nil {
			t.Fatal("changed economic evidence accepted")
		}
	}
	l = &researchEvidenceLookup{records: []researchInputRecord{first}}
	if l.read(first.Method, map[string]string{"date": "2026-10-13"}, &got) == nil {
		t.Fatal("missing request accepted")
	}
	second.Error = "timeout"
	l = &researchEvidenceLookup{records: []researchInputRecord{first, second}}
	if l.read(first.Method, map[string]string{"date": "2026-10-12"}, &got) == nil {
		t.Fatal("failed occurrence ignored")
	}
	if sameFrozenEvidence("store.ListOrders", first.Response, second.Response) {
		t.Fatal("store evidence normalized")
	}
}

func TestFrozenMemberFillConservation(t *testing.T) {
	f := trading.Fill{AccountID: fuying13Account, TradeDate: "2026-10-12", GatewayOrderID: "buy", FillID: "fill-1", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Qty: 100, Price: 1}
	q := trading.FillQuery{AccountID: fuying13Account, TradeDate: f.TradeDate, History: true, Limit: 500, Cursor: "0"}
	request, _ := json.Marshal(q)
	response, _ := json.Marshal([]trading.Fill{f})
	lookup := func() *researchEvidenceLookup {
		return &researchEvidenceLookup{records: []researchInputRecord{{Method: "store.ListFills", Request: request, Response: response}}}
	}
	m := cycleFeeMember{Date: f.TradeDate, Order: f.GatewayOrderID, Security: "510310.SH", Side: "B", FillID: f.FillID, Quantity: 50, Gross: 50000000}
	if err := verifyFrozenFeeFills(lookup(), []cycleFeeMember{m, m}); err != nil {
		t.Fatal(err)
	}
	if err := verifyFrozenFeeFills(lookup(), []cycleFeeMember{m, m, m}); err == nil {
		t.Fatal("same fill used beyond original quantity")
	}
	for _, kind := range []string{"id", "order", "price", "side", "security"} {
		bad := m
		switch kind {
		case "id":
			bad.FillID = "other"
		case "order":
			bad.Order = "other"
		case "price":
			bad.Gross++
		case "side":
			bad.Side = "S"
		case "security":
			bad.Security = "510300.SH"
		}
		if err := verifyFrozenFeeFills(lookup(), []cycleFeeMember{bad}); err == nil {
			t.Fatal("changed source fill accepted", kind)
		}
	}
	duplicated, _ := json.Marshal([]trading.Fill{f, f})
	l := lookup()
	l.records[0].Response = duplicated
	if err := verifyFrozenFeeFills(l, []cycleFeeMember{m}); err == nil {
		t.Fatal("duplicate original fill accepted")
	}
}

func feeAllocationFixture() ([]performance.ETFCycleTradingEvidence, []cycleFeeMember, cycleExecutionFeeEvidence) {
	fee := int64(123)
	trade := performance.ETFCycleTradingEvidence{AccountID: fuying13Account, CycleID: "2026-10-12/510310.SH", AcquisitionQuantity: 100, AcquisitionGrossCents: 10000, OrdinaryFeeCents: 999, FeeBasis: "oc_fee_snapshot_not_final", QualityFlags: []string{"historical_t0_order_group_inferred"}}
	m := cycleFeeMember{Cycle: trade.CycleID, Date: "2026-10-12", Order: "buy", Security: "510310.SH", Side: "B", Quantity: 100, Gross: 100000000}
	e := cycleExecutionFeeEvidence{Schema: "relay.broker_execution_fee_association.v1", Account: fuying13Account}
	e.OC.Orders = []cycleExecutionFeeOrder{{Date: m.Date, Order: m.Order, Security: m.Security, Side: m.Side, Quantity: 100, Gross: 100000000, Fee: &fee, Status: "matched"}}
	return []performance.ETFCycleTradingEvidence{trade}, []cycleFeeMember{m}, e
}

func TestCycleWholeOrderFeeOnce(t *testing.T) {
	trades, m, e := feeAllocationFixture()
	m = append(m, m[0])
	for i := range m {
		m[i].Quantity = 50
		m[i].Gross = 50000000
	}
	r, review, err := applyCycleOrdinaryFees(trades, m, e)
	if err != nil || r[0].OrdinaryFeeCents != 123 || r[0].FeeBasis != "broker_cash_reconciled" || review[0].Orders != 1 || !review[0].Applied {
		t.Fatal(r, review, err)
	}
	if trades[0].OrdinaryFeeCents != 999 || !reflect.DeepEqual(r[0].QualityFlags, trades[0].QualityFlags) {
		t.Fatal("mutated old fees or ownership uncertainty")
	}
}

func TestCycleSharedOrderKeepsUncertainty(t *testing.T) {
	for _, partial := range []bool{false, true} {
		trades, m, e := feeAllocationFixture()
		trades[0].AcquisitionQuantity = 50
		trades[0].AcquisitionGrossCents = 5000
		m[0].Quantity = 50
		m[0].Gross = 50000000
		if !partial {
			two := trades[0]
			two.CycleID = "2026-10-13/510310.SH"
			trades = append(trades, two)
			n := m[0]
			n.Cycle = two.CycleID
			m = append(m, n)
		}
		r, review, err := applyCycleOrdinaryFees(trades, m, e)
		if err != nil {
			t.Fatal(err)
		}
		for i := range r {
			if review[i].Applied || r[i].OrdinaryFeeCents != 999 || r[i].FeeBasis != "oc_fee_snapshot_not_final" || len(review[i].Unresolved) != 1 {
				t.Fatal("shared fee silently allocated", r, review)
			}
		}
	}
}

func TestCycleFeeAllocationFailClosed(t *testing.T) {
	for _, name := range []string{"unknown_cycle", "account", "quantity", "gross", "negative", "extra_member", "duplicate_fee", "wrong_security", "wrong_side", "missing_fee_order"} {
		t.Run(name, func(t *testing.T) {
			trades, m, e := feeAllocationFixture()
			switch name {
			case "unknown_cycle":
				m[0].Cycle = "unknown"
			case "account":
				e.Account = "other"
			case "quantity":
				m[0].Quantity++
			case "gross":
				m[0].Gross++
			case "negative":
				m[0].Gross = -1
			case "extra_member":
				m = append(m, m[0])
			case "duplicate_fee":
				e.OC.Orders = append(e.OC.Orders, e.OC.Orders[0])
			case "wrong_security":
				e.OC.Orders[0].Security = "159915.SZ"
			case "wrong_side":
				e.OC.Orders[0].Side = "S"
			case "missing_fee_order":
				e.OC.Orders = nil
			}
			if _, _, err := applyCycleOrdinaryFees(trades, m, e); err == nil {
				t.Fatal("invalid allocation accepted")
			}
		})
	}
	for _, status := range []string{"gross_mismatch", "fee_ambiguous"} {
		trades, m, e := feeAllocationFixture()
		e.OC.Orders[0].Status = status
		e.OC.Orders[0].Fee = nil
		r, review, err := applyCycleOrdinaryFees(trades, m, e)
		if err != nil || review[0].Applied || r[0].OrdinaryFeeCents != 999 {
			t.Fatal(r, review, err)
		}
	}
}
