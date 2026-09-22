package main

import (
	"math"
	"testing"
)

func TestParseBrokerMoneyCentsPreservesSubYuanNegativeValues(t *testing.T) {
	value, err := parseBrokerMoneyCents("-0.79")
	if err != nil {
		t.Fatalf("parseBrokerMoneyCents() error = %v", err)
	}
	if value != -79 || formatCents(value) != "-0.79" {
		t.Fatalf("value = %d, formatted = %q", value, formatCents(value))
	}
}

func TestBrokerReturnDenominatorUsesModifiedDietzTiming(t *testing.T) {
	denominator, details := brokerReturnDenominator(1_000_00, []brokerCashFlowRow{{TradeTime: "12:15:00", FlowID: "flow-1", OutCents: 100_00}})
	if math.Abs(denominator-950) > 0.000001 {
		t.Fatalf("denominator = %.6f, want 950", denominator)
	}
	if len(details) != 1 || details[0]["weight"] != 0.5 {
		t.Fatalf("details = %#v", details)
	}
}
