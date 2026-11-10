package performance

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/trading"
)

func componentPreviewLinks() componentSaleLinks {
	return componentSaleLinks{byFill: map[string][]componentSaleLink{}, groups: map[string]*componentTransferGroup{
		"redeem": {redemptionGatewayOrderID: "redeem", basketRoot: "510310", matchedAt: time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC),
			expectedBySecurity: map[string]int64{"600026.SH": 300}, linkedBySecurity: map[string]int64{}},
	}}
}

func componentPreviewFill() trading.Fill {
	return trading.Fill{AccountID: "acct-1", FillID: "sale", GatewayOrderID: "sale", Symbol: "600026", Exchange: trading.ExchangeSH,
		TradeSide: trading.TradeSideSell, BusinessType: trading.BusinessTypeStock, Qty: 300, Price: 21, Fee: 3, BasketID: "myasset",
		MatchedAt: time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)}
}

func TestComponentPreviewUniqueSaleEvidence(t *testing.T) {
	for _, scenario := range []string{"unique", "opening", "buy", "competing", "oversell", "before", "unknown_time", "conflicting_parent"} {
		t.Run(scenario, func(t *testing.T) {
			links, fill := componentPreviewLinks(), componentPreviewFill()
			var open []trading.Position
			var other []trading.Fill
			switch scenario {
			case "opening":
				open = []trading.Position{{Symbol: fill.Symbol, Exchange: fill.Exchange, Quantity: 1}}
			case "buy":
				buy := fill
				buy.FillID, buy.TradeSide = "buy", trading.TradeSideBuy
				other = append(other, buy)
			case "competing":
				g := *links.groups["redeem"]
				g.redemptionGatewayOrderID = "other"
				links.groups["other"] = &g
			case "oversell":
				fill.Qty++
			case "before":
				fill.MatchedAt = links.groups["redeem"].matchedAt
			case "unknown_time":
				fill.MatchedAt = time.Time{}
			case "conflicting_parent":
				fill.BasketID = "etfarb#159869"
			}
			fills := append([]trading.Fill{fill}, other...)
			before := append([]trading.Fill(nil), fills...)
			completeUniqueComponentSales(&links, fills, open)
			if links.excludes(fill) != (scenario == "unique") {
				t.Fatalf("unexpected inference: %+v", links)
			}
			if !reflect.DeepEqual(fills, before) {
				t.Fatal("source fill changed")
			}
			completeUniqueComponentSales(&links, fills, open)
			if scenario == "unique" && links.groups["redeem"].linkedBySecurity["600026.SH"] != 300 {
				t.Fatal("repeat inference duplicated quantity")
			}
		})
	}
}

func TestComponentPreviewRequiresPhysicalInventoryAndMarketEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "not_held", "missing_price", "missing_type", "core", "competing"} {
		t.Run(scenario, func(t *testing.T) {
			links := componentPreviewLinks()
			core := map[string]*costWorkingState{"600026.SH": newCostWorkingState("acct", "2026-08-03", "600026", "SH", "test")}
			residuals := map[string]*costWorkingState{}
			instruments := map[string]contributionInstrument{"600026.SH": {InstrumentType: "stock", Close: 20, HasClose: true}}
			closePositions := map[string]trading.Position{"600026.SH": {Quantity: 300}}
			switch scenario {
			case "not_held":
				closePositions = nil
			case "missing_price":
				instruments["600026.SH"] = contributionInstrument{InstrumentType: "stock"}
			case "missing_type":
				instruments["600026.SH"] = contributionInstrument{Close: 20, HasClose: true}
			case "core":
				core["600026.SH"].quantity = 1
			case "competing":
				g := *links.groups["redeem"]
				g.redemptionGatewayOrderID = "other"
				links.groups["other"] = &g
			}
			flags := createComponentResidualMarks("acct", "2026-08-03", core, residuals, links, instruments, nil, closePositions)
			if scenario == "not_held" {
				if len(residuals) != 0 || !containsString(flags, "component_transfer_without_closing_inventory_unclassified") {
					t.Fatal("fabricated inventory or lost unresolved transfer warning")
				}
				return
			}
			for _, s := range residuals {
				if (s.item.Status == "blocked") != (scenario != "valid") {
					t.Fatalf("unexpected admission: %+v", s)
				}
				if s.item.BuyQuantity != 0 || s.item.OpenQuantity != 0 || s.quantity != 300 {
					t.Fatal("fabricated acquisition")
				}
				if scenario == "valid" {
					assertClose(t, s.cost, 6000)
				}
			}
		})
	}
}

func componentPreviewCost() ledger.PositionCostState {
	return ledger.PositionCostState{AccountID: "acct-1", TradeDate: "2026-08-03", Symbol: "600026", Exchange: "SH",
		CostBucket: componentResidualPrefix + "2026-08-03:redeem", CloseQuantity: 300, CloseTotalCost: 6000, CloseMarketValue: 6000, Status: "estimated"}
}

func TestComponentPreviewDailyContributionDoesNotRepeatSourcePnL(t *testing.T) {
	cost := componentPreviewCost()
	instrument := contributionInstrument{SecurityID: "600026.SH", Symbol: "600026", Exchange: "SH", InstrumentType: "stock", HasClose: true, HasPreClose: true, PreClose: 20, Close: 21}
	bucket := contributionBucket{instrument: instrument, close: trading.Position{Quantity: 300}}
	core, rows, err := splitComponentContributionBucket(bucket, []ledger.PositionCostState{cost}, componentSaleLinks{}, 100000)
	if err != nil || len(rows) != 1 || core.close.Quantity != 0 {
		t.Fatalf("%+v %v", rows, err)
	}
	assertClose(t, *rows[0].NetContribution, 0)
	assertClose(t, rows[0].CloseValue, 6300)
	// A partial next-day sale plus marked remaining shares contributes only
	// today's price change, not the source-day IOPV gain again.
	cost.OpenQuantity, cost.CloseQuantity, cost.SellQuantity = 300, 100, 200
	cost.SellAmount, cost.SellFee = 4200, 3
	fill := componentPreviewFill()
	fill.Qty = 200
	bucket.open.Quantity, bucket.close.Quantity = 300, 100
	bucket.fills = []trading.Fill{fill}
	bucket.orders = []trading.Order{{GatewayOrderID: fill.GatewayOrderID}}
	core, rows, err = splitComponentContributionBucket(bucket, []ledger.PositionCostState{cost}, componentSaleLinks{}, 100000)
	if err != nil || core.open.Quantity != 0 || core.close.Quantity != 0 || len(core.fills) != 0 || len(core.orders) != 0 {
		t.Fatalf("%+v %v", core, err)
	}
	assertClose(t, *rows[0].NetContribution, 297)
	assertClose(t, rows[0].Turnover, 4200)
	// No-sale following day uses the new previous close, not original basis.
	cost.OpenQuantity, cost.CloseQuantity, cost.SellQuantity = 100, 100, 0
	cost.SellAmount, cost.SellFee = 0, 0
	bucket.open.Quantity, bucket.close.Quantity, bucket.fills, bucket.orders = 100, 100, nil, nil
	bucket.instrument.PreClose, bucket.instrument.Close = 21, 22
	_, rows, err = splitComponentContributionBucket(bucket, []ledger.PositionCostState{cost}, componentSaleLinks{}, 100000)
	if err != nil {
		t.Fatal(err)
	}
	assertClose(t, *rows[0].NetContribution, 100)
	bucket.instrument.PreClose = math.NaN()
	if _, _, err = splitComponentContributionBucket(bucket, []ledger.PositionCostState{cost}, componentSaleLinks{}, 100000); err == nil {
		t.Fatal("accepted non-finite market price")
	}
}

func TestComponentPreviewParentValuationNoDoubleCount(t *testing.T) {
	for _, unresolved := range []bool{false, true} {
		links := componentPreviewLinks()
		if unresolved {
			links.groups["redeem"].expectedBySecurity["unclassified.SZ"] = 1
		}
		items := []SecurityContribution{{SecurityID: "510310.SH", StrategyID: "round", PnLStatus: "estimated", EstimatedExitValue: floatPointer(10000),
			LinkedComponentSales: 1000, EstimatedFee: 15, ActualFee: 5, NetContribution: floatPointer(100)}}
		groups := []t0RedemptionGroup{{securityID: "510310.SH", groupID: "round", redemptions: []trading.Fill{{GatewayOrderID: "redeem"}}}}
		err := applyComponentResidualValuation(items, groups, []ledger.PositionCostState{componentPreviewCost()}, "2026-08-03", links)
		if unresolved {
			if err == nil || items[0].ETFSettlementEstimate != nil {
				t.Fatal("unclassified transfer treated as complete settlement")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		assertClose(t, *items[0].ETFSettlementEstimate, 2990)
		assertClose(t, *items[0].NetContribution, 100)
	}
}

func TestComponentPreviewCostCarryAndPublicationGuards(t *testing.T) {
	service, store := residualFixture(t)
	service.componentResidualPreview = true
	store.costStates = []ledger.PositionCostState{componentPreviewCost()}
	store.orders, store.fills = nil, nil
	store.positions = map[string][]trading.Position{"open": {{Symbol: "600026", Exchange: trading.ExchangeSH, Quantity: 300}}, "close": {{Symbol: "600026", Exchange: trading.ExchangeSH, Quantity: 100}}}
	fill := componentPreviewFill()
	fill.Qty, fill.MatchedAt = 200, fill.MatchedAt.AddDate(0, 0, 1)
	store.fills = []trading.Fill{fill}
	client := service.market.(*fakeContributionMarket)
	client.metadata = market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "600026.SH", "instrument_type": "stock"}}}}
	client.bars = market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "600026.SH", "pre_close": 20, "close": 21}}}}
	result := residualCalculate(t, service, "20260804")
	if result.Status == "blocked" || result.Summary.QuantityBreaks != 0 || result.Summary.T0ComponentQuantity != 100 {
		t.Fatalf("%+v", result)
	}
	assertClose(t, result.Summary.CloseMarketValue, 2100)
	assertClose(t, result.Summary.T0ComponentMarkedBasis, 2000)
	store.costStates[0].Status = "blocked"
	if blocked := residualCalculate(t, service, "20260804"); blocked.Status != "blocked" {
		t.Fatal("previous blocked component cost was promoted to estimated")
	}
	store.costStates[0].Status = "estimated"
	_, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260804", CostLedgerOptions{Persist: true})
	if err == nil || len(store.costUpserts) != 0 {
		t.Fatal("persisted research cost")
	}
	_, err = service.CalculateEconomicNAV(context.Background(), "acct-1", "20260804", EconomicNAVOptions{Persist: true})
	if err == nil || len(store.navUpserts) != 0 {
		t.Fatal("persisted research NAV")
	}
	service.componentResidualPreview = false
	if result := residualCalculate(t, service, "20260804"); result.Status != "blocked" {
		t.Fatal("normal service accepted research buckets")
	}
	service.componentResidualPreview = true
	if result := residualCalculate(t, service, "20260805"); result.Status != "blocked" {
		t.Fatal("component cost silently reanchored across missing day")
	}
}

func TestComponentPreviewSourceDayCostAndDefaultIsolation(t *testing.T) {
	service, store := residualFixture(t)
	store.positions = map[string][]trading.Position{"open": {}, "close": {{Symbol: "600026", Exchange: trading.ExchangeSH, Quantity: 300}}}
	at := componentPreviewFill().MatchedAt
	store.orders = []trading.Order{
		{GatewayOrderID: "buy", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 1000, T0OrderGroupID: "round"},
		{GatewayOrderID: "redeem", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, OrderQty: 1000, T0OrderGroupID: "round"},
	}
	store.fills = []trading.Fill{
		{AccountID: "acct-1", GatewayOrderID: "buy", FillID: "buy", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Qty: 1000, Price: 5, MatchedAt: at},
		{AccountID: "acct-1", GatewayOrderID: "redeem", FillID: "redeem", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, Qty: 1000, MatchedAt: at.Add(time.Minute)},
	}
	store.transfers = []trading.ComponentTransfer{{AccountID: "acct-1", GatewayOrderID: "redeem", FillID: "component", Symbol: "600026", Exchange: trading.ExchangeSH,
		ComponentSymbol: "600026", ComponentExchange: trading.ExchangeSH, ComponentQty: 300, MatchedAt: at.Add(time.Minute)}}
	client := service.market.(*fakeContributionMarket)
	client.metadata.Payload["data"] = []any{map[string]any{"security_id": "510310.SH", "instrument_type": "etf"}, map[string]any{"security_id": "600026.SH", "instrument_type": "stock"}}
	client.bars.Payload["data"] = []any{map[string]any{"security_id": "510310.SH", "pre_close": 5, "close": 5}, map[string]any{"security_id": "600026.SH", "pre_close": 19, "close": 20}}
	baseline := residualCalculate(t, service, "20260803")
	if baseline.Status != "blocked" || baseline.Summary.T0ComponentBuckets != 0 {
		t.Fatal("default production path unexpectedly inferred component lots")
	}
	service.componentResidualPreview = true
	result := residualCalculate(t, service, "20260803")
	if result.Status == "blocked" || result.Summary.QuantityBreaks != 0 || result.Summary.T0ComponentQuantity != 300 {
		t.Fatalf("%+v", result)
	}
	assertClose(t, result.Summary.CloseMarketValue, 6000)
	assertClose(t, result.Summary.T0ComponentMarkedBasis, 6000)
	if lot := residualItem(t, result, componentResidualPrefix+"2026-08-03:redeem"); lot.BuyQuantity != 0 || lot.OpenQuantity != 0 || lot.CloseQuantity != 300 {
		t.Fatalf("fabricated acquisition: %+v", lot)
	}
	if repeated := residualCalculate(t, service, "20260803"); !reflect.DeepEqual(result.Summary, repeated.Summary) {
		t.Fatal("repeated source-day preview changed totals")
	}
	if store.fills[0].T0OrderGroupID != "" || len(store.transfers) != 1 {
		t.Fatal("source facts mutated")
	}
	store.costStates, store.transfers, store.orders, store.fills = result.Positions, nil, nil, nil
	store.positions["open"] = append([]trading.Position(nil), store.positions["close"]...)
	next := residualCalculate(t, service, "20260804")
	if next.Status == "blocked" || next.Summary.T0ComponentQuantity != 300 {
		t.Fatalf("source-day lot failed to carry: %+v", next)
	}
}

func TestComponentPreviewCarriedSaleRequiresUniqueUncontestedLot(t *testing.T) {
	for _, scenario := range []string{"unique", "core", "other_lot", "new_transfer", "explicit_basket", "oversell", "buy"} {
		t.Run(scenario, func(t *testing.T) {
			core := map[string]*costWorkingState{"600026.SH": newCostWorkingState("acct", "2026-08-04", "600026", "SH", "test")}
			lot := newCostWorkingState("acct", "2026-08-04", "600026", "SH", "previous")
			lot.item.CostBucket, lot.quantity = componentPreviewCost().CostBucket, 300
			residuals := map[string]*costWorkingState{"lot": lot}
			fill := componentPreviewFill()
			links := componentSaleLinks{}
			var other []trading.Fill
			switch scenario {
			case "core":
				core["600026.SH"].quantity = 100
			case "other_lot":
				copy := *lot
				residuals["other"] = &copy
			case "new_transfer":
				links = componentPreviewLinks()
			case "explicit_basket":
				fill.BasketID = "etfarb#159869"
			case "oversell":
				fill.Qty++
			case "buy":
				buy := fill
				buy.TradeSide = trading.TradeSideBuy
				other = append(other, buy)
			}
			routes := routeCarriedComponentSales(core, residuals, append([]trading.Fill{fill}, other...), links)
			if (routes[contributionFillKey(fill)] != "") != (scenario == "unique") {
				t.Fatalf("unexpected route %+v", routes)
			}
			if scenario != "unique" && lot.item.Status != "blocked" {
				t.Fatal("ambiguous lot not blocked")
			}
		})
	}
}
