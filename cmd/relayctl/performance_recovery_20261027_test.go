package main

import (
	"fmt"
	"math"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func TestRecovery20261027ExecutionRoundingAndSplitOrders(t *testing.T) {
	trade := recovery20261027Trade{Code: "300346", Qty: 300, GrossCents: 1288300, Orders: 1}
	flow := brokerCashFlowRow{FlowID: "a", Summary: "broker卖南大光电(300346)300股*42.943", InCents: 1287481}
	fee, ids, err := recovery20261027ExecutionFee(trade, []brokerCashFlowRow{flow}, false)
	if err != nil || fee != 819 || len(ids) != 1 {
		t.Fatalf("rounded average must not replace gross: %d %v", fee, err)
	}
	trade = recovery20261027Trade{Code: "300033", Qty: 200, GrossCents: 3926400, Orders: 2}
	flows := []brokerCashFlowRow{
		{FlowID: "a", Summary: "broker卖同花顺(300033)100股*196.330", InCents: 1962052},
		{FlowID: "b", Summary: "broker卖同花顺(300033)100股*196.310", InCents: 1961852},
	}
	fee, _, err = recovery20261027ExecutionFee(trade, flows, false)
	if err != nil || fee != 2496 {
		t.Fatalf("split orders: %d %v", fee, err)
	}
	for _, mutate := range []func(*recovery20261027Trade, []brokerCashFlowRow){
		func(tr *recovery20261027Trade, _ []brokerCashFlowRow) { tr.Qty++ },
		func(tr *recovery20261027Trade, _ []brokerCashFlowRow) { tr.Orders++ },
		func(tr *recovery20261027Trade, _ []brokerCashFlowRow) { tr.GrossCents += 10000 },
		func(_ *recovery20261027Trade, f []brokerCashFlowRow) { f[0].OutCents = 1 },
		func(_ *recovery20261027Trade, f []brokerCashFlowRow) { f[0].Summary = "missing trade" },
		func(_ *recovery20261027Trade, f []brokerCashFlowRow) { f[0].InCents += 10000 },
	} {
		tr := trade
		f := append([]brokerCashFlowRow(nil), flows...)
		mutate(&tr, f)
		if _, _, err := recovery20261027ExecutionFee(tr, f, false); err == nil {
			t.Fatal("accepted invalid execution evidence")
		}
	}
}

func TestRecovery20261027BuyFee(t *testing.T) {
	tr := recovery20261027Trade{Code: "159915", Qty: 1000000, GrossCents: 275600000, Orders: 1}
	f := brokerCashFlowRow{FlowID: "buy", Summary: "broker买创业板ETF易方达(159915)1000000股*2.756", OutCents: 275613780}
	fee, _, err := recovery20261027ExecutionFee(tr, []brokerCashFlowRow{f}, true)
	if err != nil || fee != 13780 {
		t.Fatalf("buy fee: %d %v", fee, err)
	}
}

func recovery20261027PCFFixture() (map[string]any, map[string]any) {
	rows := make([]any, 100)
	for i := range rows {
		qty := "100"
		status := "security_quantity"
		exclude := false
		if i >= 98 {
			qty = "0"
			status = "zero_economic_exposure"
			exclude = true
		}
		rows[i] = map[string]any{"security_id": "159915.SZ", "trade_date": float64(20261021), "schema_version": "etf_component.v2", "component_security_id": fmt.Sprintf("%06d.SZ", 300001+i), "stock_amount": qty, "fixed_cash_substitute": "0.0", "redeem_cash_substitute": "0.0", "economic_exposure_status": status, "exclude_from_security_exposure": exclude}
	}
	return map[string]any{"data": rows}, map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "schema_version": "etf_cash_component.v1", "trade_date": float64(20261022), "pre_trade_date": float64(20261021), "unit_subscribe_redeem": "1000000", "cash_component": "-652.34"}}}
}

func TestRecovery20261027PCFProof(t *testing.T) {
	pcf, cash := recovery20261027PCFFixture()
	got, err := verifyRecovery20261027PCF(pcf, cash)
	if err != nil || len(got) != 98 {
		t.Fatalf("complete proof: %v", err)
	}
	for _, tc := range []struct {
		row   int
		key   string
		value any
	}{
		{99, "redeem_cash_substitute", "100"}, {99, "economic_exposure_status", "unknown"},
		{0, "stock_amount", "NaN"}, {0, "stock_amount", "1.5"}, {0, "stock_amount", "-1"},
		{0, "component_security_id", "600000.SH"}, {1, "component_security_id", "300001.SZ"},
		{0, "trade_date", 20261022}, {0, "exclude_from_security_exposure", true},
	} {
		p, c := recovery20261027PCFFixture()
		p["data"].([]any)[tc.row].(map[string]any)[tc.key] = tc.value
		if _, err := verifyRecovery20261027PCF(p, c); err == nil {
			t.Fatalf("accepted %s=%v", tc.key, tc.value)
		}
	}
	cash["data"].([]any)[0].(map[string]any)["pre_trade_date"] = 20261020
	if _, err := verifyRecovery20261027PCF(pcf, cash); err == nil {
		t.Fatal("accepted wrong source day")
	}
}

func TestRecovery20261027PayablesAreReleasedWithoutNewLoss(t *testing.T) {
	previous := 0.0
	sum := 0.0
	for _, date := range []string{"20261021", "20261022", "20261023", "20261026"} {
		open, close := recovery20261027Carry(date)
		if open != previous {
			t.Fatal("discontinuous carry")
		}
		sum += close - open
		previous = close
	}
	if math.Abs(sum) > 1e-9 || previous != 0 {
		t.Fatal("payables did not fully release")
	}
}

func TestRecovery20261027CannotWriteOrExpandScope(t *testing.T) {
	for _, args := range [][]string{{"-persist"}, {"-account", "314000045768"}, {"-date-from", "20261009"}, {"unexpected"}} {
		if err := runPerformanceRecovery20261027(args); err == nil {
			t.Fatalf("accepted unsupported scope/write: %v", args)
		}
	}
}

func TestRecovery20261027ApplyRequiresReviewedBackupAndCompleteScope(t *testing.T) {
	if err := runPerformanceRecovery20261027Apply(nil); err == nil {
		t.Fatal("apply accepted missing evidence/backup")
	}
	if err := validateRecovery20261027Reviewed(recovery20261027Reviewed{QualityGatePassed: true}); err == nil {
		t.Fatal("partial plan accepted")
	}
	nav := ledger.PerformanceNAV{OpenEconomicNAV: 1000, CloseEconomicNAV: 990, AccountDayPnL: -10, ReturnDenominator: 1000, CumulativeNAV: 0.99}
	if err := compareRecovery20261027NAV(nav, nav); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ledger.PerformanceNAV){
		func(n *ledger.PerformanceNAV) { n.OpenEconomicNAV++ },
		func(n *ledger.PerformanceNAV) { n.ExternalNetFlow++ },
		func(n *ledger.PerformanceNAV) { n.AccountDayPnL = math.NaN() },
		func(n *ledger.PerformanceNAV) { n.CumulativeNAV = math.NaN() },
		func(n *ledger.PerformanceNAV) { n.CumulativeNAV += 0.001 },
	} {
		n := nav
		mutate(&n)
		if err := compareRecovery20261027NAV(n, nav); err == nil {
			t.Fatal("changed/nonfinite NAV accepted")
		}
	}
}
