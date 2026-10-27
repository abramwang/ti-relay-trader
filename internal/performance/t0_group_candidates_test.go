package performance

import (
	"testing"
	"time"

	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func TestT0CandidateOrderUnits(t *testing.T) {
	type buy struct {
		id              string
		ordered, filled int64
		group           string
	}
	for _, tc := range []struct {
		name         string
		unit, target int64
		group        string
		buys         []buy
		wantIDs      []string
		wantFlag     string
	}{
		{name: "core buys excluded", unit: 1_000_000, target: 1_000_000,
			buys: []buy{{"core-1", 53_700, 53_700, ""}, {"core-2", 53_800, 53_800, ""}, {"t0", 1_000_000, 1_000_000, ""}}, wantIDs: []string{"t0"}},
		{name: "multiple whole units retained", unit: 500_000, target: 1_500_000,
			buys: []buy{{"one", 500_000, 500_000, ""}, {"two", 1_000_000, 1_000_000, ""}}, wantIDs: []string{"two", "one"}},
		{name: "competing whole units stay ambiguous", unit: 1_000_000, target: 1_000_000,
			buys: []buy{{"first", 1_000_000, 1_000_000, ""}, {"second", 1_000_000, 1_000_000, ""}}, wantIDs: []string{"second"}, wantFlag: "ambiguous_t0_order_group"},
		{name: "quantity is order quantity not fill quantity", unit: 1_000_000, target: 1_000_000,
			buys: []buy{{"core-partial", 1_050_000, 1_000_000, ""}}, wantFlag: "incomplete_t0_order_group"},
		{name: "partial whole order remains incomplete", unit: 1_000_000, target: 1_000_000,
			buys: []buy{{"partial", 1_000_000, 400_000, ""}}, wantFlag: "incomplete_t0_order_group"},
		{name: "missing order quantity not inferred", unit: 1_000_000, target: 1_000_000,
			buys: []buy{{"unknown", 0, 1_000_000, ""}}, wantFlag: "incomplete_t0_order_group"},
		{name: "missing PCF stays blocked", target: 1_000_000,
			buys: []buy{{"t0", 1_000_000, 1_000_000, ""}}, wantFlag: "missing_meridian_etf_redemption_unit"},
		{name: "explicit split group preserved", unit: 1_000_000, target: 1_000_000, group: "explicit",
			buys: []buy{{"split-a", 400_000, 400_000, "explicit"}, {"split-b", 600_000, 600_000, "explicit"}, {"other", 1_000_000, 1_000_000, "other"}}, wantIDs: []string{"split-b", "split-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 10, 21, 10, 0, 0, 0, timeutil.Location())
			orders := []trading.Order{{GatewayOrderID: "redeem", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, OrderQty: tc.target, T0OrderGroupID: tc.group, AcceptedAt: at.Add(time.Hour)}}
			fills := []trading.Fill{{FillID: "redemption", GatewayOrderID: "redeem", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, Qty: tc.target, MatchedAt: at.Add(time.Hour)}}
			for i, b := range tc.buys {
				when := at.Add(time.Duration(i) * time.Minute)
				orders = append(orders, trading.Order{GatewayOrderID: b.id, Symbol: "159915", Exchange: trading.ExchangeSZ,
					TradeSide: trading.TradeSideBuy, OrderQty: b.ordered, T0OrderGroupID: b.group, AcceptedAt: when})
				fills = append(fills, trading.Fill{FillID: b.id, GatewayOrderID: b.id, Symbol: "159915", Exchange: trading.ExchangeSZ,
					TradeSide: trading.TradeSideBuy, Qty: b.filled, Price: 2.75, MatchedAt: when})
			}
			groups, consumed, consumedFills := (&Service{}).buildT0Groups(orders, fills,
				map[string]contributionInstrument{"159915.SZ": {SecurityID: "159915.SZ", InstrumentType: "etf"}},
				map[string]int64{"159915.SZ": tc.unit})
			if len(groups) != 1 || len(groups[0].orders) != len(tc.wantIDs) {
				t.Fatalf("unexpected selection: %#v", groups)
			}
			selected := map[string]bool{}
			for i, id := range tc.wantIDs {
				selected[id] = true
				if groups[0].orders[i].GatewayOrderID != id {
					t.Fatalf("wrong selection: %#v", groups[0].orders)
				}
			}
			for i, b := range tc.buys {
				if consumed[b.id] != selected[b.id] || consumedFills[contributionFillKey(fills[i+1])] != selected[b.id] {
					t.Fatalf("unexpected consumption for %s: %v / %v", b.id, consumed, consumedFills)
				}
			}
			if tc.wantFlag != "" && !containsString(groups[0].flags, tc.wantFlag) {
				t.Fatalf("missing %s: %v", tc.wantFlag, groups[0].flags)
			}
			bucket := buildT0CostBuckets("account", "2026-10-21", groups).states[0]
			if (bucket.item.Status == "blocked") != (tc.wantFlag != "") {
				t.Fatalf("unexpected status: %#v", bucket.item)
			}
		})
	}
}
