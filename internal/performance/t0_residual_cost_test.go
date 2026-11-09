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

func residualFixture(t *testing.T) (*Service, *fakePerformanceStore) {
	t.Helper()
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{AccountID: "acct-1", InceptionDate: "2026-08-03", Status: "confirmed", OpeningPositionSource: "broker_open_snapshot"},
		positions: map[string][]trading.Position{
			"open":  {{Symbol: "510310", Exchange: trading.ExchangeSH, Quantity: 100, AvgCost: 4}},
			"close": {{Symbol: "510310", Exchange: trading.ExchangeSH, Quantity: 120}},
		},
		orders: []trading.Order{{GatewayOrderID: "partial", OrderQty: 1000, T0OrderGroupID: "residual"}},
		fills: []trading.Fill{{AccountID: "acct-1", GatewayOrderID: "partial", FillID: "partial-fill", Symbol: "510310", Exchange: trading.ExchangeSH,
			BusinessType: trading.BusinessTypeStock, TradeSide: trading.TradeSideBuy, Qty: 20, Price: 5, Fee: 1, MatchedAt: time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)}},
	}
	client := &fakeContributionMarket{
		metadata:       market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "instrument_type": "etf"}}}},
		bars:           market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "pre_close": 5, "close": 6}}}},
		cashComponents: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "unit_subscribe_redeem": 1000}}}},
	}
	service, err := New(Options{Store: store, Market: client, Calendar: weekdayCalendar{}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func residualCalculate(t *testing.T, service *Service, date string) CostLedgerResult {
	t.Helper()
	result, err := service.CalculateCostLedger(context.Background(), "acct-1", date, CostLedgerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func residualItem(t *testing.T, result CostLedgerResult, bucket string) ledger.PositionCostState {
	t.Helper()
	for _, item := range result.Positions {
		if item.CostBucket == bucket {
			return item
		}
	}
	t.Fatalf("missing bucket %s: %+v", bucket, result)
	return ledger.PositionCostState{}
}

func TestT0ResidualCostIsSeparatedAndCarried(t *testing.T) {
	service, store := residualFixture(t)
	first := residualCalculate(t, service, "20260803")
	if first.Status == "blocked" || first.Summary.QuantityBreaks != 0 || first.Summary.T0ResidualQuantity != 20 {
		t.Fatalf("%+v", first)
	}
	if !containsString(first.QualityFlags, "t0_residual_preview_only") {
		t.Fatal("residual output must advertise its preview-only boundary")
	}
	assertClose(t, residualItem(t, first, "CORE").CloseTotalCost, 400)
	assertClose(t, residualItem(t, first, t0ResidualPrefix+"residual").CloseTotalCost, 101)
	assertClose(t, first.Summary.CloseMarketValue, 720)
	assertClose(t, first.Summary.UnrealizedPnL, 219)
	second := residualCalculate(t, service, "20260803")
	if !reflect.DeepEqual(first.Summary, second.Summary) {
		t.Fatal("repeated calculation changed totals")
	}
	if store.fills[0].T0OrderGroupID != "" {
		t.Fatal("mutated source fill")
	}
	store.costStates = first.Positions
	store.orders, store.fills = nil, nil
	store.positions["open"][0].Quantity = 120
	next := residualCalculate(t, service, "20260804")
	if next.Status == "blocked" || next.Summary.QuantityBreaks != 0 || next.Summary.T0ResidualQuantity != 20 {
		t.Fatalf("%+v", next)
	}
	assertClose(t, next.Summary.OpenTotalCost, 501)
	assertClose(t, residualItem(t, next, "CORE").CloseTotalCost, 400)
	assertClose(t, residualItem(t, next, t0ResidualPrefix+"residual").CloseTotalCost, 101)
	_, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260804", CostLedgerOptions{Persist: true})
	if err == nil || len(store.costUpserts) != 0 {
		t.Fatalf("preview must not write: %v %+v", err, store.costUpserts)
	}
}

func TestT0ResidualCostSaleNeedsExplicitOwnership(t *testing.T) {
	for _, group := range []string{"residual", "", "another-lot"} {
		t.Run("group="+group, func(t *testing.T) {
			service, store := residualFixture(t)
			store.costStates = residualCalculate(t, service, "20260803").Positions
			store.positions["open"][0].Quantity = 120
			store.positions["close"][0].Quantity = 100
			store.orders = nil
			store.fills = []trading.Fill{{AccountID: "acct-1", GatewayOrderID: "sale", FillID: "sale", Symbol: "510310", Exchange: trading.ExchangeSH,
				BusinessType: trading.BusinessTypeStock, TradeSide: trading.TradeSideSell, Qty: 20, Price: 6, Fee: 1, T0OrderGroupID: group}}
			result := residualCalculate(t, service, "20260804")
			assertClose(t, residualItem(t, result, "CORE").CloseTotalCost, 400)
			if group == "residual" {
				if result.Status == "blocked" || result.Summary.T0ResidualQuantity != 0 {
					t.Fatalf("%+v", result)
				}
				assertClose(t, residualItem(t, result, t0ResidualPrefix+"residual").RealizedPnL, 18)
			} else if result.Status != "blocked" {
				t.Fatalf("unallocated sale must block: %+v", result)
			}
		})
	}
}

func TestT0ResidualCostRejectsConflictingOwnership(t *testing.T) {
	service, store := residualFixture(t)
	store.fills[0].T0OrderGroupID = "different"
	result := residualCalculate(t, service, "20260803")
	if result.Status != "blocked" || !containsString(result.QualityFlags, "t0_residual_group_conflict") {
		t.Fatalf("%+v", result)
	}
}

func TestT0ResidualCostCannotReanchorAcrossGap(t *testing.T) {
	service, store := residualFixture(t)
	store.costStates = residualCalculate(t, service, "20260803").Positions
	store.inception.CostSource = "broker_open_snapshot"
	result := residualCalculate(t, service, "20260805")
	if result.Status != "blocked" || !containsString(result.QualityFlags, "t0_residual_reanchor_forbidden") {
		t.Fatalf("%+v", result)
	}
}

func TestT0ResidualOpeningCannotGuessCorporateActionAllocation(t *testing.T) {
	core := newCostWorkingState("acct", "2026-08-04", "510310", "SH", "previous")
	core.quantity, core.cost = 100, 400
	residual := newCostWorkingState("acct", "2026-08-04", "510310", "SH", "previous")
	residual.quantity, residual.cost = 20, 101
	applyBucketedOpening(core, []*costWorkingState{residual}, 240, corporateActionFactor{exFactor: 2}, true, true)
	if core.item.Status != "blocked" || residual.item.Status != "blocked" || core.quantity != 100 || residual.quantity != 20 {
		t.Fatal("guessed corporate-action allocation")
	}
}

func TestT0ResidualPreparationScopesAndRedemptionGuard(t *testing.T) {
	states := make(map[string]*costWorkingState)
	fill := trading.Fill{FillID: "buy", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, T0OrderGroupID: "shared"}
	instruments := map[string]contributionInstrument{"510310.SH": {InstrumentType: "etf"}, "159915.SZ": {InstrumentType: "etf"}}
	other := fill
	other.FillID, other.Symbol, other.Exchange = "buy-other", "159915", trading.ExchangeSZ
	prepareT0Residuals("acct", "2026-08-03", nil, []trading.Fill{fill, other}, instruments, nil, nil, states)
	if len(states) != 2 {
		t.Fatal("mixed securities sharing group")
	}
	key := residualCostKey("510310.SH", t0ResidualPrefix+"shared")
	states[key].quantity = 20
	prepareT0Residuals("acct", "2026-08-04", nil, nil, instruments, []t0RedemptionGroup{{securityID: "510310.SH", groupID: "shared", explicit: true}}, nil, states)
	if states[key].item.Status != "blocked" {
		t.Fatal("carried redemption needs allocation")
	}
	if states[residualCostKey("159915.SZ", t0ResidualPrefix+"shared")].item.Status == "blocked" {
		t.Fatal("cross-security contamination")
	}
}

func TestT0ResidualPreparationRejectsUnknownInstrumentAndConsumedConflict(t *testing.T) {
	fill := trading.Fill{FillID: "buy", GatewayOrderID: "buy-order", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, T0OrderGroupID: "lot"}
	states := make(map[string]*costWorkingState)
	prepareT0Residuals("acct", "2026-08-03", nil, []trading.Fill{fill}, nil, nil, nil, states)
	key := residualCostKey("510310.SH", t0ResidualPrefix+"lot")
	if states[key].item.Status != "blocked" || !containsString(states[key].flags, "t0_residual_instrument_type_unconfirmed") {
		t.Fatal("missing Meridian metadata must not create a valid ETF bucket")
	}
	states = make(map[string]*costWorkingState)
	prepareT0Residuals("acct", "2026-08-03", []trading.Order{{GatewayOrderID: "buy-order", T0OrderGroupID: "other"}},
		[]trading.Fill{fill}, map[string]contributionInstrument{"510310.SH": {InstrumentType: "etf"}}, nil,
		map[string]bool{contributionFillKey(fill): true}, states)
	if states[key].item.Status != "blocked" || !containsString(states[key].flags, "t0_residual_group_conflict") {
		t.Fatal("consumption by a redemption group must not conceal conflicting ownership")
	}
}

func TestT0ResidualDoesNotConsumeCompletedRedemptionBuy(t *testing.T) {
	service, store := residualFixture(t)
	buy := store.fills[0]
	buy.GatewayOrderID, buy.FillID, buy.T0OrderGroupID, buy.Qty = "full-buy", "full-buy-fill", "full", 1000
	buy.MatchedAt = buy.MatchedAt.Add(time.Minute)
	redeem := buy
	redeem.GatewayOrderID, redeem.FillID, redeem.TradeSide = "redeem", "redeem-fill", trading.TradeSideRedemption
	redeem.BusinessType, redeem.Price = trading.BusinessTypeETF, 0
	redeem.MatchedAt = buy.MatchedAt.Add(time.Minute)
	store.fills = append(store.fills, buy, redeem)
	store.orders = append(store.orders,
		trading.Order{GatewayOrderID: "full-buy", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, OrderQty: 1000, T0OrderGroupID: "full", AcceptedAt: buy.MatchedAt},
		trading.Order{GatewayOrderID: "redeem", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideRedemption, OrderQty: 1000, T0OrderGroupID: "full", AcceptedAt: redeem.MatchedAt})
	result := residualCalculate(t, service, "20260803")
	if result.Status == "blocked" || result.Summary.QuantityBreaks != 0 || result.Summary.T0ResidualQuantity != 20 || result.Summary.T0BuyQuantity != 1000 {
		t.Fatalf("%+v", result)
	}
	assertClose(t, residualItem(t, result, "CORE").CloseTotalCost, 400)
	assertClose(t, residualItem(t, result, t0ResidualPrefix+"residual").CloseTotalCost, 101)
}
