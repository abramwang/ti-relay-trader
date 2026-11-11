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

func overnightChainFixture(t *testing.T) (*Service, *allocationPerformanceStore) {
	t.Helper()
	source := overnightTestSource()
	at, _ := time.Parse(time.RFC3339, "2026-11-04T13:35:02+08:00")
	base := &fakePerformanceStore{inception: ledger.PerformanceInception{AccountID: "account", Status: "confirmed", InceptionDate: "2026-11-02"}, costStates: []ledger.PositionCostState{source},
		positions: map[string][]trading.Position{"open": {{Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 1000000}}, "close": {{Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 123200}}},
		orders: []trading.Order{
			{AccountID: "account", TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: "core", TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 123200, CumFilledQty: 123200, IsTerminal: true},
			{AccountID: "account", TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: "red", TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, OrderQty: 1000000, CumFilledQty: 1000000, IsTerminal: true},
		}, fills: []trading.Fill{{AccountID: "account", TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: "core", FillID: "core-fill", TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Qty: 123200, Price: 296296.0 / 123200, MatchedAt: at.Add(-time.Hour)}},
		transfers: []trading.ComponentTransfer{{AccountID: "account", TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, ComponentSymbol: "159915", ComponentExchange: trading.ExchangeSZ, GatewayOrderID: "red", TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, Qty: 1000000, ComponentQty: 1000000, MatchedAt: at, FillID: "parent"}},
		orderFees: []ledger.OrderFeeRecord{{GatewayOrderID: "core", RecordScope: "order", FeeComplete: true, AssociationComplete: true, FeeSource: "broker_order_fund_detail", TotalFee: 14.8148}, {GatewayOrderID: "red", RecordScope: "order", FeeComplete: true, AssociationComplete: true, FeeSource: "broker_order_fund_detail", TotalFee: 133.14}},
	}
	store := &allocationPerformanceStore{fakePerformanceStore: base, history: []ledger.T0Allocation{{LinkID: "source-audit", AccountID: "account", TradeDate: "2026-11-03", GatewayOrderID: "source-buy", SecurityID: "159915.SZ", TradeSide: "B", Quantity: 1000000, GroupID: "confirmed", Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation", EvidenceRef: "test-confirmation", ConfirmedBy: "test", ConfirmedAt: at}}}
	m := &fakeContributionMarket{metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "instrument_type": "etf"}}}},
		bars:           market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "pre_close": 2.403, "close": 2.561}}}},
		snapshots:      market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "iopv": 2.4549, "timestamp": "2026-11-04T13:35:00+08:00"}}}},
		cashComponents: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "unit_subscribe_redeem": 1000000}}}},
	}
	l := OvernightT0Allocation{AccountID: "account", SourceDate: "2026-11-03", RedemptionDate: "2026-11-04", SecurityID: "159915.SZ", GroupID: "confirmed", SourceBuyOrderID: "source-buy", RedemptionOrderID: "red", Quantity: 1000000, EvidenceRef: "test-confirmation"}
	s, err := New(Options{Store: store, Market: m, Calendar: weekdayCalendar{}, ComponentResidualPreview: true, OvernightT0Allocations: []OvernightT0Allocation{l}, Now: func() time.Time { return at.AddDate(0, 0, 7) }})
	if err != nil {
		t.Fatal(err)
	}
	return s, store
}

func TestOvernightChainReleasesOnlyReviewedLotAndSplitsDailyReturn(t *testing.T) {
	s, store := overnightChainFixture(t)
	original := append([]trading.Fill(nil), store.fills...)
	ctx := context.Background()
	cost, err := s.CalculateCostLedger(ctx, "account", "2026-11-04", CostLedgerOptions{})
	if err != nil || cost.Status == "blocked" || cost.Summary.QuantityBreaks != 0 || len(cost.OvernightRedemptions) != 1 {
		t.Fatalf("cost: %+v %v", cost, err)
	}
	core := residualItem(t, cost, "CORE")
	res := residualItem(t, cost, t0ResidualPrefix+"confirmed")
	if core.OpenQuantity != 0 || core.BuyQuantity != 123200 || core.CloseQuantity != 123200 || res.OpenQuantity != 1000000 || res.BuyQuantity != 0 || res.SellQuantity != 1000000 || res.CloseQuantity != 0 {
		t.Fatalf("wrong quantities %+v %+v", core, res)
	}
	assertClose(t, core.CloseTotalCost, 296310.8148)
	assertClose(t, res.CloseTotalCost, 0)
	assertClose(t, res.RealizedPnL, -782.35)
	contrib, err := s.CalculateContributions(ctx, "account", "2026-11-04")
	if err != nil {
		t.Fatal(err)
	}
	var t0, ordinary *SecurityContribution
	for i := range contrib.Contributions {
		c := &contrib.Contributions[i]
		if c.SecurityID != "159915.SZ" {
			continue
		}
		if c.StrategyID == "confirmed" {
			if t0 != nil {
				t.Fatal("double counted overnight residual")
			}
			t0 = c
		} else {
			ordinary = c
		}
	}
	if t0 == nil || ordinary == nil || t0.NetContribution == nil || ordinary.NetContribution == nil {
		t.Fatalf("missing contribution %+v", contrib)
	}
	assertClose(t, *t0.NetContribution, 48340.25)
	assertClose(t, *ordinary.NetContribution, 19204.3852)
	assertClose(t, *t0.NetContribution+cost.OvernightRedemptions[0].DailyBridge.SourceDayPnL, -782.35)
	assertClose(t, t0.PriorDayFeeCredit, 122.6)
	if t0.BuyAmount != 0 || t0.BuyQuantity != 0 || t0.OpenQuantity != 1000000 || !reflect.DeepEqual(original, store.fills) {
		t.Fatal("invented current-day buy or changed source fills")
	}
	repeat, err := s.CalculateCostLedger(ctx, "account", "2026-11-04", CostLedgerOptions{})
	if err != nil || !reflect.DeepEqual(cost, repeat) {
		t.Fatalf("non-repeatable cost preview %v", err)
	}
	if _, err = s.CalculateCostLedger(ctx, "account", "2026-11-04", CostLedgerOptions{Persist: true}); err == nil {
		t.Fatal("published research cost")
	}
	if _, err = s.CalculateEconomicNAV(ctx, "account", "2026-11-04", EconomicNAVOptions{Persist: true}); err == nil {
		t.Fatal("published research NAV")
	}
	if len(store.costUpserts) != 0 || len(store.navUpserts) != 0 {
		t.Fatal("financial writes")
	}
	// Tomorrow only the core cost survives. The source fee cannot be credited twice.
	store.costStates = cost.Positions
	store.orders, store.fills, store.transfers = nil, nil, nil
	store.positions["open"][0].Quantity = 123200
	next, err := s.CalculateCostLedger(ctx, "account", "2026-11-05", CostLedgerOptions{})
	if err != nil || next.Status == "blocked" || len(next.OvernightRedemptions) != 0 {
		t.Fatalf("next-day carry %+v %v", next, err)
	}
	assertClose(t, residualItem(t, next, "CORE").CloseTotalCost, 296310.8148)
}

func TestOvernightChainRejectsUnprovedOrConflictingRelease(t *testing.T) {
	cases := map[string]func(*Service, *allocationPerformanceStore){
		"selected_same_day_buy": func(s *Service, p *allocationPerformanceStore) {
			p.orders[0].OrderQty, p.orders[0].CumFilledQty = 1000000, 1000000
			p.orders[0].AcceptedAt = p.transfers[0].MatchedAt.Add(-time.Hour)
			p.fills[0].Qty = 1000000
		},
		"missing_source":            func(s *Service, p *allocationPerformanceStore) { p.costStates = nil },
		"stale_source":              func(s *Service, p *allocationPerformanceStore) { p.costStates[0].TradeDate = "2026-11-02" },
		"blocked_source":            func(s *Service, p *allocationPerformanceStore) { p.costStates[0].Status = "blocked" },
		"different_source_quantity": func(s *Service, p *allocationPerformanceStore) { p.costStates[0].CloseQuantity-- },
		"missing_audit":             func(s *Service, p *allocationPerformanceStore) { p.history = nil },
		"wrong_audit_order":         func(s *Service, p *allocationPerformanceStore) { p.history[0].GatewayOrderID = "other" },
		"wrong_audit_evidence":      func(s *Service, p *allocationPerformanceStore) { p.history[0].EvidenceRef = "other" },
		"nonuser_audit":             func(s *Service, p *allocationPerformanceStore) { p.history[0].EvidenceType = "inventory_evidence" },
		"partial_redemption":        func(s *Service, p *allocationPerformanceStore) { p.transfers[0].Qty-- },
		"duplicate_parent":          func(s *Service, p *allocationPerformanceStore) { p.transfers = append(p.transfers, p.transfers[0]) },
		"missing_parent":            func(s *Service, p *allocationPerformanceStore) { p.transfers = nil },
		"changed_open_quantity":     func(s *Service, p *allocationPerformanceStore) { p.positions["open"][0].Quantity++ },
		"target_group_reuse":        func(s *Service, p *allocationPerformanceStore) { p.orders[0].T0OrderGroupID = "confirmed" },
		"missing_pcf": func(s *Service, p *allocationPerformanceStore) {
			s.market.(*fakeContributionMarket).cashComponents = market.MeridianResponse{}
		},
		"settled": func(s *Service, p *allocationPerformanceStore) {
			p.etfSettlements = []ledger.ETFSettlementFinalization{{AccountID: "account"}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, p := overnightChainFixture(t)
			change(s, p)
			r, err := s.CalculateCostLedger(context.Background(), "account", "2026-11-04", CostLedgerOptions{})
			if err == nil && r.Status != "blocked" {
				t.Fatalf("unsafe release accepted %+v", r)
			}
		})
	}
	// Default path still blocks this unallocated overnight redemption.
	s, p := overnightChainFixture(t)
	s.overnightT0Allocations = nil
	r, err := s.CalculateCostLedger(context.Background(), "account", "2026-11-04", CostLedgerOptions{})
	if err != nil || r.Status != "blocked" || p.costStates[0].CloseQuantity != 1000000 {
		t.Fatalf("default behavior changed: %+v %v", r, err)
	}
}

func TestOvernightChainOptionsAreResearchOnlyAndCopied(t *testing.T) {
	s, p := overnightChainFixture(t)
	links := append([]OvernightT0Allocation(nil), s.overnightT0Allocations...)
	if _, err := New(Options{Store: p, OvernightT0Allocations: links}); err == nil {
		t.Fatal("production opt-in accepted")
	}
	if _, err := New(Options{Store: p, ComponentResidualPreview: true, OvernightT0Allocations: append(links, links[0])}); err == nil {
		t.Fatal("duplicate release accepted")
	}
	next, err := New(Options{Store: p, ComponentResidualPreview: true, OvernightT0Allocations: links})
	if err != nil {
		t.Fatal(err)
	}
	links[0].Quantity = 1
	if next.overnightT0Allocations[0].Quantity != 1000000 {
		t.Fatal("shared mutable config")
	}
}

func TestOvernightSettlementEstimateDoesNotTreatPreviousMarkAsCash(t *testing.T) {
	s, store := overnightChainFixture(t)
	fills := append(append([]trading.Fill(nil), store.fills...), redemptionFillsFromComponentTransfers(store.orders, store.fills, store.transfers)...)
	instruments := map[string]contributionInstrument{"159915.SZ": {SecurityID: "159915.SZ", InstrumentType: "etf", PreClose: 2.403, Close: 2.561, HasPreClose: true, HasClose: true}}
	groups, _, _ := s.buildT0Groups(store.orders, fills, instruments, map[string]int64{"159915.SZ": 1000000})
	groups, err := s.attachOvernightT0(context.Background(), "account", "2026-11-04", groups, store.orders, fills, store.transfers)
	if err != nil || len(groups) != 1 {
		t.Fatalf("%v %+v", err, groups)
	}
	fees := authoritativeOrderFees(store.orderFees)
	fees["component-sale"] = ledger.OrderFeeRecord{TotalFee: 100}
	componentFills := []trading.Fill{{GatewayOrderID: "component-sale", TradeSide: trading.TradeSideSell, Qty: 100000, Price: 24}}
	item := s.calculateT0Contribution(context.Background(), "2026-11-04", groups[0], componentFills, true, fees, 0)
	if item.ETFSettlementEstimate == nil {
		t.Fatalf("missing estimate %+v", item)
	}
	assertClose(t, *item.ETFSettlementEstimate, 51573.39)
	assertClose(t, -2452000-122.6+item.LinkedComponentSales-item.ActualFee+*item.ETFSettlementEstimate, -782.35)
	assertClose(t, item.ActualFee, 233.14) // Only today's executed charges.
	assertClose(t, item.EstimatedFee-item.PriorDayFeeCredit, item.EffectiveFee)
	// Day-3 pre-close affects daily attribution, not what is still to settle.
	changed := groups[0]
	plan := *changed.overnight
	changed.overnight = &plan
	changed.overnight.source.CloseMarketValue += 1000
	other := s.calculateT0Contribution(context.Background(), "2026-11-04", changed, componentFills, true, fees, 0)
	assertClose(t, *other.ETFSettlementEstimate, *item.ETFSettlementEstimate)
	assertClose(t, *other.NetContribution, *item.NetContribution-1000)
	// Unsold stock is another asset, not both inventory and parent receivable.
	links := componentSaleLinks{groups: map[string]*componentTransferGroup{"red": {expectedBySecurity: map[string]int64{"600026.SH": 300}, linkedBySecurity: map[string]int64{}}}}
	costs := []ledger.PositionCostState{{Symbol: "600026", Exchange: "SH", CostBucket: componentResidualPrefix + "2026-11-04:red", Status: "estimated", CloseQuantity: 300, CloseMarketValue: 6000}}
	items := []SecurityContribution{item}
	for i := 0; i < 2; i++ {
		if err := applyComponentResidualValuation(items, groups, costs, "2026-11-04", links); err != nil {
			t.Fatal(err)
		}
		assertClose(t, *items[0].ETFSettlementEstimate, 45573.39)
		assertClose(t, *items[0].NetContribution, *item.NetContribution)
	}
}
