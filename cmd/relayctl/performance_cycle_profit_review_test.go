package main

import (
	"os"
	"path/filepath"
	"reflect"
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
			if p.Trading.LaterComponentSales != 1566600 || p.Status != "estimated" || p.NetCyclePnLCents != 55909 {
				t.Fatal("overnight components lost or fees invented")
			}
			for _, flag := range p.QualityFlags {
				if flag == "later_component_sale_fee_allocation_unverified" {
					t.Fatal("matched fee still marked unverified")
				}
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

func TestCycleLaterFeeAssociation(t *testing.T) {
	fee := int64(383)
	newInputs := func() ([]performance.ETFCycleTradingEvidence, cycleCostEvidence, map[string]string, cycleExecutionFeeEvidence) {
		trades := []performance.ETFCycleTradingEvidence{{AccountID: "a", CycleID: "2026-10-15/510310.SH", OrdinaryFeeCents: 100, LaterComponentSales: 621600, QualityFlags: []string{"later_component_sale_fee_allocation_unverified", "historical_t0_order_group_inferred"}, EvidenceRefs: []string{"old"}}}
		cost := cycleCostEvidence{Account: "a", Contributions: []performance.ContributionResult{{AccountID: "a", TradeDate: "2026-10-16", Contributions: []performance.SecurityContribution{{SecurityID: "600026.SH", StrategyID: "ETF_T0_COMPONENT:2026-10-15:parent", SellQuantity: 300, OpenQuantity: 300, SellAmount: 6216}}}}}
		parents := map[string]string{"2026-10-15/parent": "2026-10-15/510310.SH"}
		var evidence cycleExecutionFeeEvidence
		evidence.Schema, evidence.Account = "relay.broker_execution_fee_association.v1", "a"
		evidence.OC.Orders = []cycleExecutionFeeOrder{{Date: "2026-10-16", Order: "order", Security: "600026.SH", Side: "S", Quantity: 300, Gross: 6216000000, Fee: &fee, Status: "matched"}}
		return trades, cost, parents, evidence
	}
	t.Run("matched preserves other boundaries and input", func(t *testing.T) {
		trades, cost, parents, evidence := newInputs()
		result, err := applyCycleLaterExecutionFees(trades, cost, parents, evidence)
		if err != nil || result[0].OrdinaryFeeCents != 483 || !reflect.DeepEqual(result[0].QualityFlags, []string{"historical_t0_order_group_inferred"}) || result[0].FeeBasis != trades[0].FeeBasis {
			t.Fatal(result, err)
		}
		if trades[0].OrdinaryFeeCents != 100 || len(trades[0].QualityFlags) != 2 || len(trades[0].EvidenceRefs) != 1 {
			t.Fatal("input mutated")
		}
		if _, err := applyCycleLaterExecutionFees(result, cost, parents, evidence); err == nil {
			t.Fatal("fee applied twice")
		}
	})
	for _, name := range []string{"scope", "quantity", "gross", "fee_missing", "gross_mismatch", "wrong_side", "missing", "duplicate", "shared", "partial", "already_charged", "cycle_total", "wrong_parent"} {
		t.Run(name, func(t *testing.T) {
			trades, cost, parents, evidence := newInputs()
			switch name {
			case "scope":
				evidence.Account = "other"
			case "quantity":
				evidence.OC.Orders[0].Quantity++
			case "gross":
				evidence.OC.Orders[0].Gross++
			case "fee_missing":
				evidence.OC.Orders[0].Fee = nil
			case "gross_mismatch":
				evidence.OC.Orders[0].Status = "gross_mismatch"
			case "wrong_side":
				evidence.OC.Orders[0].Side = "B"
			case "missing":
				evidence.OC.Orders = nil
			case "duplicate":
				evidence.OC.Orders = append(evidence.OC.Orders, evidence.OC.Orders[0])
			case "shared":
				cost.Contributions[0].Contributions = append(cost.Contributions[0].Contributions, cost.Contributions[0].Contributions[0])
			case "partial":
				cost.Contributions[0].Contributions[0].CloseQuantity = 1
			case "already_charged":
				cost.Contributions[0].Contributions[0].ActualFee = 3.83
			case "cycle_total":
				trades[0].LaterComponentSales++
			case "wrong_parent":
				delete(parents, "2026-10-15/parent")
			}
			if _, err := applyCycleLaterExecutionFees(trades, cost, parents, evidence); err == nil {
				t.Fatal("invalid later fee association accepted")
			}
		})
	}
}
