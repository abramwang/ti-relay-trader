package main

import (
	"os"
	"path/filepath"
	"testing"

	"ti-relay-trader/internal/performance"
)

func TestCycleProfitCLIRequiresCyclePlan(t *testing.T) {
	if err := runFuyingCashTiming([]string{"--cycle-profit", "--output", filepath.Join(t.TempDir(), "x.json")}); err == nil {
		t.Fatal("profit run without pinned publication accepted")
	}
}

func TestCycleProfitMoneyPrecision(t *testing.T) {
	for _, v := range []float64{-1, 0.0000001, 1e20} {
		if _, err := cycleMoney(v); err == nil {
			t.Fatal("invalid money accepted", v)
		}
	}
	for _, v := range []struct {
		value float64
		cents int64
	}{{1.004999, 100}, {1.005, 101}, {0, 0}} {
		n, err := cycleMoney(v.value)
		if err != nil || n != v.cents {
			t.Fatal("micro to cent rounding", n, err)
		}
	}
}

func TestCycleProfitReviewedEvidence(t *testing.T) {
	if _, err := os.Stat(filepath.Join("../..", cycleCostEvidencePath)); os.IsNotExist(err) {
		t.Skip("private reviewed broker evidence not in source repository")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Error(err)
		}
	})
	var matrix fuyingCashMatrix
	if err := readPinnedJSON(fuyingCashMatrixPath, fuyingCashMatrixHash, &matrix); err != nil {
		t.Fatal(err)
	}
	var plan provisionalRecoveryPlan
	if err := readProvisionalPlan("outputs/recovery/fuying13_broker_refresh_20261115/plan.json", "dc38a0f24aea64c40da7735e76eb32cdf6790a2a9fdb3a501b8fe49c7ecfe8f7", &plan); err != nil {
		t.Fatal(err)
	}
	legs, err := fuyingCycleLegs(matrix)
	if err != nil {
		t.Fatal(err)
	}
	cash, err := performance.ReviewETFCashCycles(plan.Account, plan.Anchor.TradeDate, plan.Dates, legs)
	if err != nil {
		t.Fatal(err)
	}
	r, err := loadFuyingCycleProfit(cash)
	if err != nil {
		t.Fatal(err)
	}
	profits := r["profits"].([]performance.ETFCycleProfit)
	if len(profits) != 14 {
		t.Fatal("lost cycles")
	}
	known := 0
	for _, p := range profits {
		if p.AccountNAVPublishable || p.FinalSettlement {
			t.Fatal("finality promoted")
		}
		if p.Status == "observed_cash_reconciled" {
			known++
		}
		switch p.CycleID {
		case "2026-10-12/510310.SH":
			if p.Trading.AcquisitionQuantity != 2700000 || p.Trading.AcquisitionGrossCents != 1148040000 {
				t.Fatal("5900 residual mixed into redemption")
			}
		case "2026-10-15/510310.SH":
			if p.Trading.LaterComponentSales != 1566600 || p.Status != "estimated" {
				t.Fatal("overnight components lost or fees invented")
			}
		case "2026-10-16/159915.SZ":
			if p.Trading.AcquisitionQuantity != 6000000 || p.Trading.AcquisitionGrossCents != 1687600000 {
				t.Fatal("day pool cost changed")
			}
		case "2026-11-04/159915.SZ":
			if p.Trading.AcquisitionDate != "2026-11-03" || p.Trading.AcquisitionGrossCents != 245200000 || p.NetCyclePnLCents != 113473 || p.Trading.OrdinaryFeeCents != 160828 {
				t.Fatal("overnight acquisition/fees wrong")
			}
		}
	}
	if known != 1 {
		t.Fatal("unverified groups promoted")
	}
}
