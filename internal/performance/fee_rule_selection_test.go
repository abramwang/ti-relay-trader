package performance

import (
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

func TestContributionFeeRuleLegacyZeroRateMinimumSemantics(t *testing.T) {
	fill := trading.Fill{AccountID: "a", TradeSide: trading.TradeSideBuy, Price: 10, Qty: 100}
	rule := ledger.FeeRule{AccountID: "a", MinimumCommission: 5}
	fee := calculateContributionFillFee(fill, contributionInstrument{}, []ledger.FeeRule{rule})
	if fee.source != "estimated" || fee.effective != 0 {
		t.Fatalf("do not silently change existing zero-rate semantics: %#v", fee)
	}
}
