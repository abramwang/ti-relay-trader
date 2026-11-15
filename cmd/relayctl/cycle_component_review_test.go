package main

import (
	"testing"

	"ti-relay-trader/internal/performance"
)

func TestCycleComponentLaterSale(t *testing.T) {
	for _, kind := range []string{"valid", "wrong_account", "quantity", "same_day", "duplicate", "unmatched", "open_inventory"} {
		t.Run(kind, func(t *testing.T) {
			r := []performance.CycleComponentReview{{CycleID: "2026-10-15/510310.SH", Gaps: []performance.CycleComponentGap{{ParentID: "parent", SecurityID: "600026.SH", TransferQty: 300, RemainingQty: 300, CloseQty: 300}}}}
			c := cycleCostEvidence{Account: "a", Contributions: []performance.ContributionResult{{AccountID: "a", TradeDate: "2026-10-16", Contributions: []performance.SecurityContribution{{StrategyID: "ETF_T0_COMPONENT:2026-10-15:parent", SecurityID: "600026.SH", OpenQuantity: 300, SellQuantity: 300, SellAmount: 6216}}}}}
			switch kind {
			case "wrong_account":
				c.Contributions[0].AccountID = "b"
			case "quantity":
				r[0].Gaps[0].RemainingQty--
			case "same_day":
				c.Contributions[0].TradeDate = "2026-10-15"
			case "duplicate":
				c.Contributions = append(c.Contributions, c.Contributions[0])
			case "unmatched":
				r[0].Gaps[0].ParentID = "other"
			case "open_inventory":
				r[0].Gaps[0].OpenQty = 1
			}
			err := attachCycleLaterSales(r, c)
			if kind == "valid" {
				if err != nil || r[0].Gaps[0].LaterSaleCents != 621600 || r[0].Gaps[0].LaterSaleDate != "2026-10-16" {
					t.Fatal(r, err)
				}
			} else if err == nil {
				t.Fatal("invalid continuation accepted")
			}
		})
	}
}
