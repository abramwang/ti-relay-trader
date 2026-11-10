package performance

import (
	"context"
	"reflect"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/trading"
)

// Synthetic identities, with the reviewed partial-buy/cross-round quantities.
func dayPoolFixture() ([]t0RedemptionGroup, []trading.Order, []trading.Fill) {
	var orders []trading.Order
	var fills []trading.Fill
	for _, r := range []struct {
		id, clock string
		qty       int64
		price     float64
		side      trading.TradeSide
	}{
		{"b1", "09:39:47", 431300, 2.82, trading.TradeSideBuy},
		{"b2", "09:39:47", 1000000, 2.821, trading.TradeSideBuy},
		{"b3", "09:47:08", 1000000, 2.811, trading.TradeSideBuy},
		{"b4", "09:49:54", 568700, 2.82, trading.TradeSideBuy},
		{"r1", "09:52:23", 1000000, 0, trading.TradeSideRedemption},
		{"b5", "10:01:54", 1000000, 2.812, trading.TradeSideBuy},
		{"b6", "10:09:12", 1000000, 2.803, trading.TradeSideBuy},
		{"r2", "10:25:11", 2000000, 0, trading.TradeSideRedemption},
		{"r3", "11:08:20", 2000000, 0, trading.TradeSideRedemption},
		{"b7", "13:22:54", 1000000, 2.809, trading.TradeSideBuy},
		{"r4", "13:42:17", 1000000, 0, trading.TradeSideRedemption},
	} {
		at, _ := time.Parse(time.RFC3339, "2026-10-16T"+r.clock+"+08:00")
		business := trading.BusinessTypeStock
		if r.side == trading.TradeSideRedemption {
			business = trading.BusinessTypeETF
		}
		q := r.qty
		if r.id == "b1" {
			q = 1000000
		}
		orders = append(orders, trading.Order{AccountID: "acct", TradeDate: "2026-10-16", GatewayOrderID: r.id, Symbol: "159915", Exchange: trading.ExchangeSZ,
			TradeSide: r.side, BusinessType: business, OrderQty: q, CumFilledQty: r.qty, IsTerminal: true, BasketID: "etfarb#159915", CreatedAt: at})
		fills = append(fills, trading.Fill{AccountID: "acct", TradeDate: "2026-10-16", GatewayOrderID: r.id, FillID: r.id, Symbol: "159915", Exchange: trading.ExchangeSZ,
			TradeSide: r.side, BusinessType: business, Qty: r.qty, Price: r.price, MatchedAt: at, BasketID: "etfarb#159915"})
	}
	groups, _, _ := (&Service{}).buildT0Groups(orders, fills, map[string]contributionInstrument{"159915.SZ": {SecurityID: "159915.SZ", Symbol: "159915", Exchange: "SZ", InstrumentType: "etf"}}, map[string]int64{"159915.SZ": 1000000})
	return groups, orders, fills
}

func TestClosedDayPoolConservesCostWithoutAllocatingRounds(t *testing.T) {
	groups, orders, fills := dayPoolFixture()
	beforeOrders, beforeFills := append([]trading.Order(nil), orders...), append([]trading.Fill(nil), fills...)
	pool, ok := closedDayPool("acct", "2026-10-16", "159915.SZ", groups, orders, fills, nil, nil)
	if !ok || len(pool.orders) != 7 || len(pool.redemptions) != 4 || pool.explicit || !containsString(pool.flags, "per_redemption_buy_allocation_unasserted") {
		t.Fatalf("%+v %v", pool, ok)
	}
	cost := buildT0CostBuckets("acct", "2026-10-16", []t0RedemptionGroup{pool})
	if len(cost.states) != 1 || cost.states[0].item.Status == "blocked" || len(cost.consumedFills) != 11 {
		t.Fatalf("%+v", cost)
	}
	row := cost.states[0].item
	assertClose(t, row.BuyAmount, 16876000)
	if row.BuyQuantity != 6000000 || row.SellQuantity != 6000000 || row.CloseQuantity != 0 || row.CloseTotalCost != 0 {
		t.Fatalf("%+v", row)
	}
	if !reflect.DeepEqual(beforeOrders, orders) || !reflect.DeepEqual(beforeFills, fills) {
		t.Fatal("changed original facts or per-order attribution")
	}
}

func TestClosedDayPoolRejectsAmbiguousOrIncompleteEvidence(t *testing.T) {
	for _, scenario := range []string{"opening", "closing", "ordinary_buy", "untagged", "mixed_strategy", "strategy_id", "fill_strategy_id", "fill_conflict", "missing_fill", "duplicate_fill", "duplicate_order", "not_terminal", "order_quantity", "explicit_group", "wrong_account", "wrong_date", "wrong_event_day", "before_buy", "same_time", "missing_time", "bad_price", "unknown_instrument", "missing_pcf", "missing_order", "ordinary_sale"} {
		t.Run(scenario, func(t *testing.T) {
			groups, orders, fills := dayPoolFixture()
			var open, close []trading.Position
			switch scenario {
			case "opening":
				open = []trading.Position{{Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 100}}
			case "closing":
				close = []trading.Position{{Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 100}}
			case "ordinary_buy":
				orders[0].BasketID = "myasset"
			case "untagged":
				orders[0].BasketID = ""
			case "mixed_strategy":
				orders[0].StrategyType = "etf_cross_section"
			case "strategy_id":
				orders[0].StrategyID = "another-t0-strategy"
			case "fill_strategy_id":
				fills[0].StrategyID = "another-t0-strategy"
			case "fill_conflict":
				fills[0].BasketID = "etfarb#510310"
			case "missing_fill":
				fills = fills[1:]
			case "duplicate_fill":
				fills = append(fills, fills[0])
			case "duplicate_order":
				orders = append(orders, orders[0])
			case "not_terminal":
				orders[0].IsTerminal = false
			case "order_quantity":
				orders[0].CumFilledQty++
			case "explicit_group":
				orders[0].T0OrderGroupID = "confirmed-round"
			case "wrong_account":
				fills[0].AccountID = "other"
			case "wrong_date":
				orders[0].TradeDate = "2026-10-15"
			case "wrong_event_day":
				fills[0].MatchedAt = fills[0].MatchedAt.AddDate(0, 0, -1)
			case "before_buy":
				fills[4].MatchedAt = fills[0].MatchedAt.Add(-time.Second)
			case "same_time":
				fills[4].MatchedAt = fills[0].MatchedAt
			case "missing_time":
				fills[0].MatchedAt = time.Time{}
			case "bad_price":
				fills[0].Price = 0
			case "unknown_instrument":
				groups[0].instrument.InstrumentType = ""
			case "missing_pcf":
				groups[0].redemptionUnit = 0
			case "missing_order":
				orders = orders[1:]
			case "ordinary_sale":
				orders[0].TradeSide, fills[0].TradeSide = trading.TradeSideSell, trading.TradeSideSell
			}
			if _, ok := closedDayPool("acct", "2026-10-16", "159915.SZ", groups, orders, fills, open, close); ok {
				t.Fatal("unsafe cost pool admitted")
			}
		})
	}
}

func TestClosedDayPoolRequiresSnapshotAndPreviewScope(t *testing.T) {
	groups, orders, fills := dayPoolFixture()
	at := fills[0].MatchedAt
	store := &fakePerformanceStore{observations: map[string]ledger.AssetPositionObservation{
		"open":  {AccountID: "acct", TradeDate: "2026-10-16", SnapshotType: "open", PositionCapturedAt: &at},
		"close": {AccountID: "acct", TradeDate: "2026-10-16", SnapshotType: "close", PositionCapturedAt: &at},
	}}
	s, err := New(Options{Store: store, ComponentResidualPreview: true, ClosedDayT0PoolPreviewDate: "20261016"})
	if err != nil {
		t.Fatal(err)
	}
	pooled, consumedOrders, consumedFills, err := s.previewClosedDayPools(context.Background(), "acct", "2026-10-16", groups, orders, fills, nil, nil)
	if err != nil || len(pooled) != 1 || len(consumedOrders) != 11 || len(consumedFills) != 11 {
		t.Fatalf("%+v %v", pooled, err)
	}
	if _, _, _, err := s.previewClosedDayPools(context.Background(), "acct", "2026-10-19", groups, orders, fills, nil, nil); err == nil {
		t.Fatal("cross-day review")
	}
	store.etfSettlements = []ledger.ETFSettlementFinalization{{SecurityID: "159915.SZ", Status: "confirmed"}}
	if _, _, _, err := s.previewClosedDayPools(context.Background(), "acct", "2026-10-16", groups, orders, fills, nil, nil); err == nil {
		t.Fatal("overrode finalized settlement")
	}
	store.etfSettlements = nil
	store.observations["close"] = ledger.AssetPositionObservation{}
	if _, _, _, err := s.previewClosedDayPools(context.Background(), "acct", "2026-10-16", groups, orders, fills, nil, nil); err == nil {
		t.Fatal("missing close observation treated as flat")
	}
	if _, err := New(Options{Store: store, ClosedDayT0PoolPreviewDate: "20261016"}); err == nil {
		t.Fatal("pool allowed outside non-persistent research mode")
	}
}

func TestClosedDayPoolUsesEveryRedemptionAndComponentOnlyOnce(t *testing.T) {
	groups, orders, fills := dayPoolFixture()
	pool, _ := closedDayPool("acct", "2026-10-16", "159915.SZ", groups, orders, fills, nil, nil)
	links := componentSaleLinks{groups: map[string]*componentTransferGroup{}}
	for i, r := range pool.redemptions {
		f := trading.Fill{FillID: r.FillID + "-stock", GatewayOrderID: r.GatewayOrderID + "-stock", Qty: 100, Price: float64(i + 1), Symbol: "600026", Exchange: trading.ExchangeSH}
		links.groups[r.GatewayOrderID] = &componentTransferGroup{expectedBySecurity: map[string]int64{"600026.SH": 100}, linkedBySecurity: map[string]int64{"600026.SH": 100}, linkedFills: []trading.Fill{f}}
	}
	componentFills, complete := pooledComponentFills(pool, links)
	if !complete || len(componentFills) != 4 {
		t.Fatal("lost later parent component fills")
	}
	client := &fakeContributionMarket{snapshots: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "trade_date": "20261016", "timestamp": "2026-10-16T09:50:00+08:00", "iopv": 2.8}}}}}
	s, err := New(Options{Store: &fakePerformanceStore{}, Market: client})
	if err != nil {
		t.Fatal(err)
	}
	items := s.calculateT0Contributions(context.Background(), "2026-10-16", []t0RedemptionGroup{pool}, links, nil, 20000000)
	if len(items) != 1 || items[0].NetContribution == nil {
		t.Fatalf("%+v", items)
	}
	assertClose(t, items[0].LinkedComponentSales, 1000)
	assertClose(t, *items[0].EstimatedExitValue, 16800000)
	assertClose(t, *items[0].NetContribution, -101200)
	if items[0].Orders != 11 {
		t.Fatalf("orders=%d", items[0].Orders)
	}
	pool.redemptions = append(pool.redemptions, pool.redemptions[0])
	componentFills, complete = pooledComponentFills(pool, links)
	if !complete || len(componentFills) != 4 {
		t.Fatal("counted same parent's component twice")
	}
	delete(links.groups, "r2")
	if _, complete = pooledComponentFills(pool, links); complete {
		t.Fatal("missing one parent marked complete")
	}
}
