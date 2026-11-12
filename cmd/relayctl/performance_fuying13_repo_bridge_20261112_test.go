package main

import (
	"math"
	"testing"

	"ti-relay-trader/internal/performance"
)

func TestFuyingRepoBridgeMoney(t *testing.T) {
	for _, x := range []string{"", "--", "-1", "NaN", "1e2", "1.001", "1,000", "1000000000000"} {
		if _, err := fuyingRepoCashCents(x); err == nil {
			t.Fatalf("accepted %q", x)
		}
	}
	if n, err := fuyingRepoCashCents("3376.95"); err != nil || n != 337695 {
		t.Fatal(n, err)
	}
	if n, err := fuyingRepoScaled(3960.818630, 1000000); err != nil || n != 3960818630 {
		t.Fatal(n, err)
	}
	for _, x := range []float64{math.NaN(), math.Inf(1), 1.0000001, 1e12} {
		if _, err := fuyingRepoScaled(x, 1000000); err == nil {
			t.Fatal("invalid model precision")
		}
	}
}

func TestFuyingRepoBridgeCurveDoesNotAddPrincipalOrRemoveUnknownAsset(t *testing.T) {
	s := fuyingNAVScenario{Days: []fuyingNAVScenarioDay{
		{Date: "2026-10-12", ConditionalOpen: 1000, ConditionalClose: 1010, PnL: 10, Unclassified: 8},
		{Date: "2026-10-13", ConditionalOpen: 1010, ConditionalClose: 1020, PnL: 10, Unclassified: 9},
	}}
	b := performance.RepoInterestBridge{Days: []performance.RepoInterestBridgeDay{
		{TradeDate: "2026-10-12", OpenReceivableMicros: 1000000, CloseReceivableMicros: 2000000, AssetAdjustmentMicros: 1000000},
		{TradeDate: "2026-10-13", OpenReceivableMicros: 2000000, CloseReceivableMicros: 0, AssetAdjustmentMicros: -2000000},
	}}
	r, err := fuyingRepoAdjustedCurve(s, b)
	if err != nil || r[0].Open != 1001 || r[0].Close != 1012 || r[0].PnL != 11 || r[1].PnL != 8 || r[1].Unclassified != 9 || math.Abs(r[1].Index-1020.0/1001) > 1e-12 {
		t.Fatal(r, err)
	}
	b.Days[1].OpenReceivableMicros = 1
	if _, err := fuyingRepoAdjustedCurve(s, b); err == nil {
		t.Fatal("broken bridge accepted")
	}
}

func TestFuyingRepoBridgeNoWriteMode(t *testing.T) {
	if err := runFuyingRepoBridge([]string{"-persist", "-output", "unused"}); err == nil {
		t.Fatal("write mode")
	}
	if err := runFuyingRepoBridge(nil); err == nil {
		t.Fatal("missing output")
	}
}
