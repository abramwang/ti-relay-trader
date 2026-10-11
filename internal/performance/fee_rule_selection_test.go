package performance

import (
	"math"
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func TestContributionFeeRulesFailClosedOnConflict(t *testing.T) {
	fill := trading.Fill{AccountID: "a", Exchange: trading.ExchangeSH, BusinessType: trading.BusinessTypeStock, TradeSide: trading.TradeSideBuy, Price: 10, Qty: 100}
	instrument := contributionInstrument{Exchange: "SH", InstrumentType: "stock"}
	rule := ledger.FeeRule{RuleID: "one", AccountID: "a", Market: "SH", InstrumentType: "stock", BusinessType: "S", TradeSide: "B", CommissionRate: 0.001}
	duplicate := rule
	duplicate.RuleID = "two"
	duplicate.Version = 2
	for _, rules := range [][]ledger.FeeRule{{rule, duplicate}, {duplicate, rule}} {
		fee := calculateContributionFillFee(fill, instrument, rules)
		if fee.source != "missing" || !containsStringValue(fee.flags, "fee_rule_conflict") {
			t.Fatalf("conflict must not pick by version or order: %#v", fee)
		}
	}
	broader := rule
	broader.Market = "*"
	fee := calculateContributionFillFee(fill, instrument, []ledger.FeeRule{broader, broader, rule})
	if fee.source != "estimated" || fee.effective != 1 {
		t.Fatalf("a unique more specific rule must win: %#v", fee)
	}
	rule.AccountID = "b"
	fee = calculateContributionFillFee(fill, instrument, []ledger.FeeRule{rule})
	if fee.source != "missing" {
		t.Fatalf("another account rule must not match: %#v", fee)
	}
}

func TestContributionBudgetUsesOrderMinimumAndFixedFee(t *testing.T) {
	for _, side := range []trading.TradeSide{trading.TradeSideBuy, trading.TradeSideSell} {
		t.Run(string(side), func(t *testing.T) {
			instrument := contributionInstrument{Exchange: "SH", InstrumentType: "etf"}
			rule := ledger.FeeRule{RuleID: "etf", AccountID: "a", Market: "SH", InstrumentType: "etf", BusinessType: "S", TradeSide: string(side), CommissionRate: 0.00005, MinimumCommission: 0.1, FixedFee: 0.02}
			state := make(map[string]contributionOrderFeeState)
			fill := trading.Fill{AccountID: "a", GatewayOrderID: "order-1", Symbol: "510300", TradeDate: "2026-10-12", BusinessType: "S", TradeSide: side, Price: 1, Qty: 1000}
			var total float64
			for range 4 {
				fee := contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
				if fee.source != "estimated" {
					t.Fatalf("not estimated: %#v", fee)
				}
				total += fee.effective
			}
			if math.Abs(total-0.22) > 1e-9 {
				t.Fatalf("one order/four fills fee = %f want 0.22", total)
			}
			fill.GatewayOrderID = "order-2"
			fee := contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
			if fee.effective != 0.12 {
				t.Fatalf("separate order minimum = %#v", fee)
			}
			fill.TradeDate = "2026-10-13"
			fee = contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
			if fee.effective != 0.12 {
				t.Fatalf("separate date minimum = %#v", fee)
			}
			fill.AccountID, rule.AccountID = "b", "b"
			fee = contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
			if fee.effective != 0.12 {
				t.Fatalf("separate account minimum = %#v", fee)
			}
		})
	}
}

func TestContributionBudgetSampleExamplesAndBoundaries(t *testing.T) {
	for _, example := range []struct {
		kind, market     string
		side             trading.TradeSide
		amount, expected float64
	}{
		{"etf", "SH", trading.TradeSideSell, 75.2, 0.1},
		{"etf", "SH", trading.TradeSideBuy, 3771000, 188.55},
		{"stock", "SH", trading.TradeSideBuy, 10000, 1.454},
		{"stock", "SH", trading.TradeSideSell, 10000, 6.454},
		{"stock", "SZ", trading.TradeSideSell, 10000, 6.354},
	} {
		rule := ledger.FeeRule{RuleID: "budget", AccountID: "a", Market: example.market, InstrumentType: example.kind, BusinessType: "S", TradeSide: string(example.side), RawPayload: map[string]any{"authority": "sample_verified_budget"}, CommissionRate: 0.0001354}
		if example.kind == "etf" {
			rule.CommissionRate, rule.MinimumCommission = 0.00005, 0.1
		} else {
			if example.market == "SH" {
				rule.TransferFeeRate = 0.00001
			}
			if example.side == trading.TradeSideSell {
				rule.StampDutyRate = 0.0005
			}
		}
		instrument := contributionInstrument{Exchange: example.market, InstrumentType: example.kind}
		fill := trading.Fill{AccountID: "a", GatewayOrderID: "order", TradeDate: "2026-10-12", BusinessType: "S", TradeSide: example.side, Price: example.amount, Qty: 1}
		fee := contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, make(map[string]contributionOrderFeeState))
		if math.Abs(fee.effective-example.expected) > 1e-9 || !containsStringValue(fee.flags, "sample_verified_budget_not_broker_settlement") {
			t.Fatalf("%+v: fee=%#v", example, fee)
		}
		if example.kind == "stock" && !containsStringValue(fee.flags, "stock_minimum_commission_unverified") {
			t.Fatal("stock minimum boundary lost")
		}
		fill.BusinessType = "E"
		fee = contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, make(map[string]contributionOrderFeeState))
		if fee.source != "missing" {
			t.Fatal("primary ETF must not use ordinary budget")
		}
	}
}

func TestContributionBudgetKeepsActualFeesAndRejectsUnknownIdentity(t *testing.T) {
	fill := trading.Fill{AccountID: "a", GatewayOrderID: "order", Price: 1, Qty: 100}
	rule := ledger.FeeRule{RuleID: "r", AccountID: "a", CommissionRate: 0.00005, MinimumCommission: 0.1}
	instrument := contributionInstrument{}
	state := make(map[string]contributionOrderFeeState)
	fill.Fee = 0.07
	actual := contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
	if actual.actual != 0.07 || actual.estimated != 0 {
		t.Fatal("actual fee replaced by budget")
	}
	fill.Fee = 0
	missing := contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
	if missing.source != "missing" || !containsStringValue(missing.flags, "mixed_actual_and_budget_order_fee") {
		t.Fatal("mixed fee evidence must not silently charge another minimum")
	}
	fill.GatewayOrderID = ""
	missing = contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
	if missing.source != "missing" || !containsStringValue(missing.flags, "missing_fee_order_identity") {
		t.Fatal("missing order identity must fail closed")
	}
	fill.GatewayOrderID, fill.AdapterContext = "zero", map[string]any{"fee_complete": true}
	actual = contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, state)
	if actual.source != "actual" || actual.effective != 0 {
		t.Fatal("verified zero actual fee must remain zero")
	}
	fill.AdapterContext, fill.Qty = nil, 0
	missing = contributionFeeForFill(fill, instrument, []ledger.FeeRule{rule}, nil, make(map[string]contributionOrderFeeState))
	if missing.source != "missing" || !containsStringValue(missing.flags, "invalid_fee_turnover") {
		t.Fatal("invalid turnover must not accrue a minimum or fixed fee")
	}
}

func TestContributionFeeRuleLegacyZeroRateMinimumSemantics(t *testing.T) {
	fill := trading.Fill{AccountID: "a", TradeSide: trading.TradeSideBuy, Price: 10, Qty: 100}
	rule := ledger.FeeRule{AccountID: "a", MinimumCommission: 5}
	fee := calculateContributionFillFee(fill, contributionInstrument{}, []ledger.FeeRule{rule})
	if fee.source != "estimated" || fee.effective != 0 {
		t.Fatalf("do not silently change existing zero-rate semantics: %#v", fee)
	}
}
