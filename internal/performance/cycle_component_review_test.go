package performance

import (
	"strings"
	"testing"

	"ti-relay-trader/internal/trading"
)

func TestCycleComponentQuantityGaps(t *testing.T) {
	links := componentSaleLinks{groups: map[string]*componentTransferGroup{"parent": {
		expectedBySecurity: map[string]int64{"600026.SH": 300, "159900.SZ": 1, "600000.SH": 100},
		linkedBySecurity:   map[string]int64{"600000.SH": 100},
	}}}
	redemptions := []trading.Fill{{GatewayOrderID: "parent"}, {GatewayOrderID: "parent"}}
	close := []trading.Position{{Symbol: "600026", Exchange: "SH", Quantity: 300}}
	rows, err := componentQuantityGaps(redemptions, links, nil, close)
	if err != nil || len(rows) != 2 || rows[0].SecurityID != "159900.SZ" || rows[0].RemainingQty != 1 || rows[0].CloseQty != 0 || rows[1].CloseQty != 300 || rows[1].LaterSaleDate != "" {
		t.Fatalf("gaps=%+v error=%v", rows, err)
	}
	// No security-specific filtering and no automatic later sale/settlement.
	links.groups["parent"].linkedBySecurity["159900.SZ"] = 2
	if _, err := componentQuantityGaps(redemptions, links, nil, close); err == nil {
		t.Fatal("over-allocation accepted")
	}
	if _, err := componentQuantityGaps([]trading.Fill{{GatewayOrderID: "missing"}}, links, nil, nil); err == nil {
		t.Fatal("missing transfer group accepted")
	}
}

func TestCycleComponentPublication(t *testing.T) {
	s, _ := cyclePublicationFixture(t)
	s.Profit.Profits[0].Trading.QualityFlags = []string{"component_transfer_sell_quantity_mismatch"}
	s.Profit.Profits[0].Trading.LaterComponentSales = 200
	p, err := CalculateETFCycleProfits(s.Cycles, []ETFCycleTradingEvidence{s.Profit.Profits[0].Trading})
	if err != nil {
		t.Fatal(err)
	}
	s.Profit.Profits = p
	s.Profit.Components = []CycleComponentReview{{CycleID: p[0].CycleID, Groups: 1, InputSHA256: strings.Repeat("a", 64), CostSHA256: strings.Repeat("b", 64), Gaps: []CycleComponentGap{{ParentID: "parent", SecurityID: "600026.SH", TransferQty: 100, RemainingQty: 100, CloseQty: 100, LaterSaleDate: "2026-10-13", LaterSaleQty: 100, LaterSaleCents: 200}}}}
	if _, err := BuildCycleReviewVersion(s, "v2", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"scope", "date", "quantity", "amount", "duplicate", "hash", "opening", "closing", "coverage", "flag"} {
		t.Run(kind, func(t *testing.T) {
			c := s.Profit.Components[0]
			c.Gaps = append([]CycleComponentGap(nil), c.Gaps...)
			x := s
			x.Profit.Components = []CycleComponentReview{c}
			g := &x.Profit.Components[0].Gaps[0]
			switch kind {
			case "scope":
				x.Profit.Components[0].CycleID = "2026-10-12/159915.SZ"
			case "date":
				g.LaterSaleDate = "2026-10-12"
			case "quantity":
				g.LaterSaleQty--
			case "amount":
				g.LaterSaleCents++
			case "duplicate":
				x.Profit.Components[0].Gaps = append(x.Profit.Components[0].Gaps, *g)
			case "hash":
				x.Profit.Components[0].InputSHA256 = "invalid"
			case "opening":
				g.OpenQty = 1
			case "closing":
				g.CloseQty = 0
			case "coverage":
				x.Profit.Components = append(x.Profit.Components, c)
			case "flag":
				x.Profit.Components[0].Gaps = nil
			}
			if _, err := BuildCycleReviewVersion(x, "v3", strings.Repeat("c", 64)); err == nil {
				t.Fatal("invalid diagnostic accepted")
			}
		})
	}
}
