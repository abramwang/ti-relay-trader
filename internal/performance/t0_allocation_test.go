package performance

import (
	"context"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/trading"
)

type allocationPerformanceStore struct {
	*fakePerformanceStore
	history []ledger.T0Allocation
}

func (store *allocationPerformanceStore) ListT0AllocationHistory(_ context.Context, account, date string) ([]ledger.T0Allocation, error) {
	var rows []ledger.T0Allocation
	for _, item := range store.history {
		if item.AccountID == account && item.TradeDate == date {
			rows = append(rows, item)
		}
	}
	return rows, nil
}

func auditedResidualFixture(t *testing.T) (*Service, *allocationPerformanceStore) {
	service, base := residualFixture(t)
	order := &base.orders[0]
	order.AccountID = "acct-1"
	order.TradeDate = "2026-08-03"
	order.Symbol = "510310"
	order.Exchange = trading.ExchangeSH
	order.TradeSide = trading.TradeSideBuy
	order.CumFilledQty = 20
	order.IsTerminal = true
	order.T0OrderGroupID = ""
	base.fills[0].TradeDate = "2026-08-03"
	store := &allocationPerformanceStore{fakePerformanceStore: base, history: []ledger.T0Allocation{{
		LinkID: "partial-audit-v1", AccountID: "acct-1", TradeDate: "2026-08-03", GatewayOrderID: "partial", SecurityID: "510310.SH",
		TradeSide: "B", Quantity: 20, GroupID: "residual", Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation", EvidenceRef: "test-confirmation", ConfirmedBy: "test", ConfirmedAt: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	}}}
	service.store = store
	return service, store
}

func residualContribution(t *testing.T, result ContributionResult) SecurityContribution {
	t.Helper()
	for _, item := range result.Contributions {
		if item.EstimationMethod == "t0_residual_daily_mark_to_market" {
			return item
		}
	}
	t.Fatalf("missing residual contribution: %+v", result)
	return SecurityContribution{}
}

func TestAuditedResidualDailyPnLDoesNotRepeatLifetimeLoss(t *testing.T) {
	service, store := auditedResidualFixture(t)
	cost := residualCalculate(t, service, "20260803")
	if len(cost.AttributionLinkIDs) != 1 || cost.AttributionLinkIDs[0] != "partial-audit-v1" {
		t.Fatalf("missing cost audit: %+v", cost)
	}
	first, err := service.CalculateContributions(context.Background(), "acct-1", "20260803")
	if err != nil {
		t.Fatal(err)
	}
	firstResidual := residualContribution(t, first)
	if firstResidual.StrategyType != StrategyETFRedemptionT0 || firstResidual.StrategyID != "residual" || firstResidual.NetContribution == nil {
		t.Fatalf("%+v", firstResidual)
	}
	assertClose(t, *firstResidual.NetContribution, 19)
	assertClose(t, first.Summary.ClosePositionValue, 720)
	// Core daily profit is 100, not its 200 lifetime unrealized profit.
	assertClose(t, first.Summary.NetContribution, 119)
	if store.orders[0].T0OrderGroupID != "" || store.fills[0].T0OrderGroupID != "" {
		t.Fatal("mutated OC evidence")
	}
	store.costStates = cost.Positions
	store.positions["open"][0].Quantity = 120
	store.positions["close"][0].Quantity = 100
	sale := store.orders[0]
	sale.GatewayOrderID = "sale"
	sale.TradeDate = "2026-08-04"
	sale.TradeSide = trading.TradeSideSell
	store.orders = []trading.Order{sale}
	fill := store.fills[0]
	fill.GatewayOrderID = "sale"
	fill.FillID = "sale-fill"
	fill.TradeDate = "2026-08-04"
	fill.TradeSide = trading.TradeSideSell
	fill.Price = 6
	fill.MatchedAt = fill.MatchedAt.AddDate(0, 0, 1)
	store.fills = []trading.Fill{fill}
	link := store.history[0]
	link.LinkID = "sale-audit-v1"
	link.TradeDate = "2026-08-04"
	link.GatewayOrderID = "sale"
	link.TradeSide = "S"
	link.EvidenceType = "inventory_evidence"
	store.history = append(store.history, link)
	client := service.market.(*fakeContributionMarket)
	client.bars = market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "pre_close": 6, "close": 6}}}}
	second, err := service.CalculateContributions(context.Background(), "acct-1", "20260804")
	if err != nil {
		t.Fatal(err)
	}
	secondResidual := residualContribution(t, second)
	if secondResidual.NetContribution == nil {
		t.Fatalf("%+v", secondResidual)
	}
	assertClose(t, *secondResidual.NetContribution, -1)
	closed := residualCalculate(t, service, "20260804")
	assertClose(t, *firstResidual.NetContribution+*secondResidual.NetContribution, closed.Summary.RealizedPnL)
	if !containsString(second.QualityFlags, "t0_residual_preview_only") {
		t.Fatal("lost preview restriction")
	}
}

func TestAuditedResidualHoldingDayKeepsOwnershipWithoutNewTrades(t *testing.T) {
	service, store := auditedResidualFixture(t)
	store.costStates = residualCalculate(t, service, "20260803").Positions
	store.orders = nil
	store.fills = nil
	store.positions["open"][0].Quantity = 120
	client := service.market.(*fakeContributionMarket)
	client.bars = market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "pre_close": 6, "close": 7}}}}
	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260804")
	if err != nil {
		t.Fatal(err)
	}
	item := residualContribution(t, result)
	if item.NetContribution == nil || item.StrategyID != "residual" {
		t.Fatalf("%+v", item)
	}
	assertClose(t, *item.NetContribution, 20)
	assertClose(t, result.Summary.OpenPositionValue, 720)
	assertClose(t, result.Summary.ClosePositionValue, 840)
	assertClose(t, result.Summary.NetContribution, 120)
}

func TestT0AllocationRejectsChangedOrderOrFillEvidence(t *testing.T) {
	for _, change := range []func(*allocationPerformanceStore){
		func(s *allocationPerformanceStore) { s.orders[0].CumFilledQty++ },
		func(s *allocationPerformanceStore) { s.orders[0].IsTerminal = false },
		func(s *allocationPerformanceStore) { s.orders[0].AccountID = "another" },
		func(s *allocationPerformanceStore) { s.fills[0].Price = 0; s.fills[0].Qty++ },
		func(s *allocationPerformanceStore) { s.fills[0].T0OrderGroupID = "oc-other" },
		func(s *allocationPerformanceStore) { s.orders = nil },
		func(s *allocationPerformanceStore) { s.history[0].Role = "redemption" },
	} {
		service, store := auditedResidualFixture(t)
		change(store)
		if _, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260803", CostLedgerOptions{}); err == nil {
			t.Fatal("accepted drifted audit input")
		}
	}
}

func TestResidualAttributionRequiresMeridianPreviousClose(t *testing.T) {
	service, store := auditedResidualFixture(t)
	store.costStates = residualCalculate(t, service, "20260803").Positions
	store.orders = nil
	store.fills = nil
	store.positions["open"][0].Quantity = 120
	store.positions["open"][0].LastPrice = 6
	client := service.market.(*fakeContributionMarket)
	client.bars = market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "close": 7}}}}
	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260804")
	if err != nil {
		t.Fatal(err)
	}
	item := residualContribution(t, result)
	if item.PnLStatus != "missing" || item.NetContribution != nil || !containsString(item.QualityFlags, "missing_meridian_residual_valuation") {
		t.Fatalf("used broker fallback as authoritative mark: %+v", item)
	}
}

func TestT0AllocationRevocationStopsOverlay(t *testing.T) {
	service, store := auditedResidualFixture(t)
	revoked := store.history[0]
	revoked.Revision = 2
	revoked.Supersedes = revoked.LinkID
	revoked.LinkID = "revoked"
	revoked.Status = "revoked"
	store.history = append(store.history, revoked)
	result := residualCalculate(t, service, "20260803")
	if len(result.AttributionLinkIDs) != 0 || result.Summary.T0ResidualBuckets != 0 {
		t.Fatalf("revoked overlay used: %+v", result)
	}
}

func TestT0ResidualNAVPreviewCannotPublish(t *testing.T) {
	service, store := auditedResidualFixture(t)
	_, err := service.CalculateEconomicNAV(context.Background(), "acct-1", "20260803", EconomicNAVOptions{Persist: true, Status: "finalized"})
	if err == nil || !strings.Contains(err.Error(), "preview-only") {
		t.Fatalf("expected publication guard: %v", err)
	}
	if len(store.navUpserts) != 0 || len(store.reconciliationUpserts) != 0 || len(store.costUpserts) != 0 {
		t.Fatal("wrote preview financial records")
	}
}

func TestUnfilledT0OrderDoesNotCreateResidualPublicationBlock(t *testing.T) {
	service, store := auditedResidualFixture(t)
	store.history = nil
	store.fills = nil
	store.orders[0].CumFilledQty = 0
	store.orders[0].T0OrderGroupID = "unfilled-cancelled"
	store.positions["close"][0].Quantity = 100
	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260803")
	if err != nil {
		t.Fatal(err)
	}
	if containsString(result.QualityFlags, "t0_residual_preview_only") {
		t.Fatal("unfilled order created a residual block")
	}
}
