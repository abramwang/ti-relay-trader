package performance

import (
	"encoding/json"
	"reflect"
	"testing"
)

func cycleProfitFixture(t *testing.T) (ETFCashCycleReview, []ETFCycleTradingEvidence) {
	t.Helper()
	cash, err := ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, []ETFCycleCashLeg{
		cycleLeg("receipt", "510310.SH", "2026-10-12", "2026-10-13", 3000, false),
		cycleLeg("parent-fee", "510310.SH", "2026-10-12", "2026-10-12", -200, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	return cash, []ETFCycleTradingEvidence{{AccountID: "a", CycleID: "2026-10-12/510310.SH", AcquisitionDate: "2026-10-09", AcquisitionQuantity: 1000, RedemptionQuantity: 1000, AcquisitionGrossCents: 10000, ComponentSaleCents: 9000, LaterComponentSales: 1000, OrdinaryFeeCents: 100, FeeScope: "ordinary_buys_and_component_sales_only", FeeBasis: "broker_cash_reconciled", EvidenceRefs: []string{"reviewed:fixture"}}}
}

func TestETFCycleProfitUsesAcquisitionCostAndCashOnlyOnce(t *testing.T) {
	c, tr := cycleProfitFixture(t)
	before, _ := json.Marshal(tr)
	r, err := CalculateETFCycleProfits(c, tr)
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 1 || r[0].NetCyclePnLCents != 2700 || r[0].ObservedNetCashCents != 2800 || r[0].Status != "observed_cash_reconciled" || r[0].FinalSettlement || r[0].AccountNAVPublishable {
		t.Fatal("cost/parent fee/cash formula changed", r)
	}
	after, _ := json.Marshal(tr)
	if string(before) != string(after) {
		t.Fatal("input mutated")
	}
	again, err := CalculateETFCycleProfits(c, tr)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("repeat calculation changed result")
	}
}

func TestETFCycleProfitRetainsEstimatedBoundaries(t *testing.T) {
	for _, kind := range []string{"candidate", "oc-fee", "inventory"} {
		t.Run(kind, func(t *testing.T) {
			c, tr := cycleProfitFixture(t)
			switch kind {
			case "candidate":
				c.Cycles[0].KnownSourceLegs--
				c.Cycles[0].InferredSourceLegs++
			case "oc-fee":
				tr[0].FeeBasis = "oc_fee_snapshot_not_final"
			case "inventory":
				tr[0].QualityFlags = []string{"component_transfer_sell_quantity_mismatch", "component_transfer_sell_quantity_mismatch"}
			}
			r, err := CalculateETFCycleProfits(c, tr)
			if err != nil || r[0].Status != "estimated" || r[0].FinalSettlement {
				t.Fatal("estimate promoted", err)
			}
			if kind == "inventory" && len(r[0].Trading.QualityFlags) != 1 {
				t.Fatal("duplicate warnings")
			}
		})
	}
}

func TestETFCycleProfitRejectsInvalidAccounting(t *testing.T) {
	for name, change := range map[string]func(*ETFCashCycleReview, []ETFCycleTradingEvidence){
		"core mixed": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].AcquisitionQuantity += 5900 },
		"zero cost":  func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].AcquisitionGrossCents = 0 },
		"cash duplicate": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) {
			c.Cycles[0].FlowIDs[1] = c.Cycles[0].FlowIDs[0]
		},
		"cash sign":       func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { c.Cycles[0].NetCashCents++ },
		"future purchase": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].AcquisitionDate = "2026-10-14" },
		"bad date":        func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].AcquisitionDate = "2026-02-30" },
		"cross account":   func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].AccountID = "b" },
		"parent charged twice": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) {
			tr[0].FeeScope = "all_order_fees_including_redemption"
		},
		"unpriced fee":   func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].FeeBasis = "" },
		"negative fee":   func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].OrdinaryFeeCents = -100 },
		"overflow":       func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].LaterComponentSales = 1 << 62 },
		"no evidence":    func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].EvidenceRefs = nil },
		"cycle mismatch": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { tr[0].CycleID = "2026-10-12/159915.SZ" },
		"unobserved future receipt": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) {
			c.Cycles[0].LastObservedPosting = "2026-10-14"
		},
		"asserted final": func(c *ETFCashCycleReview, tr []ETFCycleTradingEvidence) { c.Cycles[0].CompleteSettlement = true },
	} {
		t.Run(name, func(t *testing.T) {
			c, tr := cycleProfitFixture(t)
			change(&c, tr)
			if _, err := CalculateETFCycleProfits(c, tr); err == nil {
				t.Fatal("invalid accounting accepted")
			}
		})
	}
	c, tr := cycleProfitFixture(t)
	if _, err := CalculateETFCycleProfits(c, append(tr, tr[0])); err == nil {
		t.Fatal("extra cost set accepted")
	}
	if _, err := CalculateETFCycleProfits(ETFCashCycleReview{}, nil); err == nil {
		t.Fatal("empty proof accepted")
	}
}

func TestETFCycleProfitRejectsFlowReuseAcrossCohorts(t *testing.T) {
	c, tr := cycleProfitFixture(t)
	c2 := c.Cycles[0]
	c2.SecurityID = "159915.SZ"
	c2.CycleID = "2026-10-12/159915.SZ"
	c.Cycles = append(c.Cycles, c2)
	t2 := tr[0]
	t2.CycleID = c2.CycleID
	tr = append(tr, t2)
	if _, err := CalculateETFCycleProfits(c, tr); err == nil {
		t.Fatal("same cash allocated twice")
	}
}
