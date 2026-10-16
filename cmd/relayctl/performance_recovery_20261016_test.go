package main

import (
	"math"
	"testing"
	"time"
)

func TestRecovery20261016SeparateLegsCloseToSourceCarry(t *testing.T) {
	final := recovery20261016Finalization(time.Now())
	entries := recovery20261016CashEntries()
	if math.Abs(entries[0].Amount+entries[1].Amount-final.SourceCloseSettlementCarry) > 0.000001 {
		t.Fatal("signed settlement legs do not equal source outstanding carry")
	}
	if entries[0].Amount <= final.SourceCloseSettlementCarry || entries[1].Amount >= 0 {
		t.Fatal("regression sample must cover gross refund larger than net carry")
	}
	if final.ActualTotalFee != 1740.18 || final.NetContribution != 9781.23 {
		t.Fatal("reviewed actual fee and contribution changed")
	}
	if got := entries[2].EffectiveAt.Format(time.RFC3339); got != "2026-10-16T09:55:53+08:00" || entries[2].Amount != 33000000 {
		t.Fatalf("confirmed external flow changed: %s / %v", got, entries[2].Amount)
	}
}

func TestVerifyRecovery20261016PCF(t *testing.T) {
	row := map[string]any{
		"security_id": "510310.SH", "schema_version": "etf_cash_component.v1",
		"pre_trade_date": float64(20261009), "trade_date": float64(20261012),
		"unit_subscribe_redeem": "900000", "cash_component": "-1817.21",
	}
	payload := map[string]any{"data": []any{row}}
	if err := verifyRecovery20261016PCF(payload); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{"-1708.21", "NaN", nil} {
		row["cash_component"] = bad
		if err := verifyRecovery20261016PCF(payload); err == nil {
			t.Fatalf("accepted invalid final cash component %v", bad)
		}
	}
}
