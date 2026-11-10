package main

import (
	"math"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestSettlementCostComparesPerSecurityNotJustSummary(t *testing.T) {
	a := performance.CostLedgerResult{Positions: []ledger.PositionCostState{{Symbol: "159915", CloseQuantity: 100, CloseTotalCost: 200}, {Symbol: "510310", CloseQuantity: 100, CloseTotalCost: 300}}}
	b := a
	b.Positions = append([]ledger.PositionCostState(nil), a.Positions...)
	b.Positions[0].CalculatedAt = time.Now()
	if !sameSettlementCost(a, b) {
		t.Fatal("audit timestamp changed financial comparison")
	}
	b.Positions[0].CloseTotalCost += 10
	b.Positions[1].CloseTotalCost -= 10
	if sameSettlementCost(a, b) {
		t.Fatal("offsetting security-level changes escaped comparison")
	}
}

func TestActualSettlementSignedCarry(t *testing.T) {
	s := settlement20261110Spec()
	for day, want := range map[string][2]float64{
		"20261103": {0, 0}, "20261104": {0, 3363.91}, "20261105": {3363.91, 3376.95}, "20261106": {3376.95, 0}, "20261109": {0, 0},
	} {
		a, b := s.carry(day)
		if a != want[0] || b != want[1] {
			t.Fatalf("%s: %v %v", day, a, b)
		}
	}
	s.SourceDate = "20261021"
	s.FeeDate = "20261022"
	s.CashDate = "20261023"
	s.CashComponentCents = -65234
	s.DeferredFeeCents = 1306
	for _, d := range []string{"20261021", "20261022", "20261023", "20261026"} {
		a, b := s.carry(d)
		x, y := recovery20261027Carry(d)
		if a != x || b != y {
			t.Fatalf("old liability changed on %s", d)
		}
	}
}

func settlement20261110PCFFixture() (map[string]any, map[string]any) {
	p, c := recovery20261027PCFFixture()
	for _, v := range p["data"].([]any) {
		v.(map[string]any)["trade_date"] = float64(20261104)
	}
	r := c["data"].([]any)[0].(map[string]any)
	r["trade_date"] = float64(20261105)
	r["pre_trade_date"] = float64(20261104)
	r["cash_component"] = "3376.95"
	return p, c
}

func TestActualSettlementPCFPositiveCashAndSourceDay(t *testing.T) {
	p, c := settlement20261110PCFFixture()
	if rows, err := verifyActualSettlementPCF(p, c, 20261104, 20261105, 3376.95); err != nil || len(rows) != 98 {
		t.Fatal(rows, err)
	}
	if _, err := verifyRecovery20261027PCF(p, c); err == nil {
		t.Fatal("new evidence accepted as old source date")
	}
	for _, bad := range []any{"-3376.95", "3376.94", "NaN", "Inf"} {
		c["data"].([]any)[0].(map[string]any)["cash_component"] = bad
		if _, err := verifyActualSettlementPCF(p, c, 20261104, 20261105, 3376.95); err == nil {
			t.Fatal("accepted wrong actual cash", bad)
		}
	}
}

func TestSettlement20261110RejectsDoubleRecognition(t *testing.T) {
	p := recovery20261027Plan{Bases: []ledger.AssetPositionObservation{{TradeDate: "2026-11-06", RawPayload: map[string]any{
		"reported_daily_pnl": 1000.0, "open_outstanding_etf_settlement_asset": 3376.95, "close_outstanding_etf_settlement_asset": 0.0,
	}}}}
	item := performanceRebuildItem{TradeDate: "2026-11-06", NAV: performance.EconomicNAVResult{NAV: ledger.PerformanceNAV{AccountDayPnL: -2376.95}}}
	item.NAV.AssetBasis.Applied = true
	if err := verifySettlement20261110NAV(p, item); err != nil {
		t.Fatal(err)
	}
	bad := item
	bad.NAV.ETFSettlement.SettlementVariance = 3376.95
	if err := verifySettlement20261110NAV(p, bad); err == nil {
		t.Fatal("receipt recognized as new income")
	}
	bad = item
	bad.NAV.ETFSettlement.PostCloseReceiptAmount = 3376.95
	if err := verifySettlement20261110NAV(p, bad); err == nil {
		t.Fatal("cash added twice")
	}
	bad = item
	bad.NAV.NAV.AccountDayPnL = 1000
	if err := verifySettlement20261110NAV(p, bad); err == nil {
		t.Fatal("receivable not released")
	}
}

func TestSettlement20261110ReviewRequiresFullOrderedChain(t *testing.T) {
	r := recovery20261027Reviewed{DatabaseReadOnly: true, QualityGatePassed: true}
	for _, a := range settlement20261110Accounts {
		r.Plans = append(r.Plans, recovery20261027Plan{AccountID: a, Finalization: ledger.ETFSettlementFinalization{SourceTradeDate: "2026-11-04"}, CashEntries: make([]ledger.CashLedgerEntry, 99), Bases: make([]ledger.AssetPositionObservation, 4)})
		for _, d := range settlement20261110Dates {
			r.Preview.Items = append(r.Preview.Items, performanceRebuildItem{AccountID: a, TradeDate: d, Cost: performance.CostLedgerResult{Status: "calculated"}, NAV: performance.EconomicNAVResult{Status: "provisional"}})
		}
	}
	if err := validateSettlement20261110Reviewed(r); err != nil {
		t.Fatal(err)
	}
	r.Preview.Items[0].NAV.NAV.AccountDayPnL = math.NaN()
	if err := validateSettlement20261110Reviewed(r); err == nil {
		t.Fatal("accepted non-finite result")
	}
	r.Preview.Items[0].NAV.NAV.AccountDayPnL = 0
	r.Preview.Items[1].TradeDate = "2026-11-06"
	if err := validateSettlement20261110Reviewed(r); err == nil {
		t.Fatal("accepted missing or reordered date")
	}
}
