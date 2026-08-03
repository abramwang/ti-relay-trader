package performance

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"net/url"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func TestCalculateContributionsUsesPositionCashFlowIdentity(t *testing.T) {
	store := &fakePerformanceStore{
		fills: []trading.Fill{
			{
				FillID:         "stock-buy",
				AccountID:      "acct-1",
				GatewayOrderID: "buy-order",
				Symbol:         "600000",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideBuy,
				BusinessType:   trading.BusinessTypeStock,
				Price:          10,
				Qty:            100,
				Fee:            1,
			},
			{
				FillID:         "stock-sell",
				AccountID:      "acct-1",
				GatewayOrderID: "sell-order",
				Symbol:         "600000",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				BusinessType:   trading.BusinessTypeStock,
				Price:          12,
				Qty:            150,
				Fee:            2,
			},
		},
		positions: map[string][]trading.Position{
			"open": {{
				AccountID: "acct-1",
				Symbol:    "600000",
				Exchange:  trading.ExchangeSH,
				Quantity:  100,
			}},
			"close": {{
				AccountID:   "acct-1",
				Symbol:      "600000",
				Exchange:    trading.ExchangeSH,
				Quantity:    50,
				MarketValue: 550,
			}},
		},
		daily: ledger.DailyPerformance{
			AccountID:    "acct-1",
			TradeDate:    "2026-07-24",
			OpenNetAsset: 100000,
			NetAsset:     100447,
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{map[string]any{
				"security_id":     "600000.SH",
				"name":            "浦发银行",
				"instrument_type": "stock",
			}}},
		},
		bars: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{map[string]any{
				"security_id": "600000.SH",
				"pre_close":   9.0,
				"close":       11.0,
			}}},
		},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260724")
	if err != nil {
		t.Fatalf("CalculateContributions() error = %v", err)
	}
	if len(result.Contributions) != 1 {
		t.Fatalf("contributions = %#v", result.Contributions)
	}
	item := result.Contributions[0]
	if item.StrategyType != StrategyStockCrossSection || item.Name != "浦发银行" {
		t.Fatalf("identity = %#v", item)
	}
	if item.GrossContribution == nil || *item.GrossContribution != 450 {
		t.Fatalf("gross contribution = %#v, want 450", item.GrossContribution)
	}
	if item.NetContribution == nil || *item.NetContribution != 447 {
		t.Fatalf("net contribution = %#v, want 447", item.NetContribution)
	}
	if item.FeeSource != "actual" || result.Summary.NetContribution != 447 {
		t.Fatalf("fees/summary = %#v / %#v", item, result.Summary)
	}
	if !result.Summary.FeeCoverageComplete || result.Summary.FeeRequiredOrders != 2 || result.Summary.FeeCoveredOrders != 2 {
		t.Fatalf("fee coverage = %#v", result.Summary)
	}
	if len(marketClient.queries) < 2 {
		t.Fatalf("market queries = %#v", marketClient.queries)
	}
	barQuery := marketClient.queries[1]
	if barQuery.Get("start_date") != "20260724" || barQuery.Get("end_date") != "20260724" || barQuery.Get("trade_date") != "" {
		t.Fatalf("bar query = %s", barQuery.Encode())
	}
}

func TestContributionFeeForFillChargesAuthoritativeOrderFeeOnce(t *testing.T) {
	feeAsOf := time.Date(2026, 8, 1, 15, 1, 0, 0, timeutil.Location())
	authoritative := authoritativeOrderFees([]ledger.OrderFeeRecord{{
		GatewayOrderID:      "gw-fee-1",
		FeeRecordID:         "fee-1",
		TotalFee:            6.25,
		FeeComplete:         true,
		AssociationComplete: true,
		FeeSource:           "broker_order_fund_detail",
		FeeAsOf:             feeAsOf,
	}})
	consumed := make(map[string]bool)
	fill := trading.Fill{GatewayOrderID: "gw-fee-1", Price: 10, Qty: 100}

	first := contributionFeeForFill(fill, contributionInstrument{}, nil, authoritative, consumed)
	second := contributionFeeForFill(fill, contributionInstrument{}, nil, authoritative, consumed)

	if first.actual != 6.25 || first.effective != 6.25 || first.source != "actual_order_fee:broker_order_fund_detail" {
		t.Fatalf("first fee = %#v", first)
	}
	if second.actual != 0 || second.effective != 0 || second.source != "" {
		t.Fatalf("second fee duplicated = %#v", second)
	}
}

func TestCalculateOrderFeeDayCoverageRequiresEveryExecutedOrder(t *testing.T) {
	fills := []trading.Fill{
		{FillID: "fill-1", GatewayOrderID: "order-1", Price: 10, Qty: 100},
		{FillID: "fill-2", GatewayOrderID: "order-1", Price: 10.1, Qty: 100},
		{
			FillID:         "fill-3",
			GatewayOrderID: "order-2",
			Price:          12,
			Qty:            100,
			AdapterContext: map[string]any{
				"fee_complete": true,
				"fee_source":   "broker_trade_query",
			},
		},
	}
	authoritative := authoritativeOrderFees([]ledger.OrderFeeRecord{{
		GatewayOrderID:      "order-1",
		FeeRecordID:         "fee-1",
		TotalFee:            5,
		FeeComplete:         true,
		AssociationComplete: true,
		FeeSource:           "broker_order_fund_detail",
		FeeAsOf:             time.Now(),
	}})

	coverage := calculateOrderFeeDayCoverage(fills, authoritative)
	if !coverage.complete || coverage.requiredOrders != 2 || coverage.coveredOrders != 2 || coverage.source != "broker_order_or_fill" {
		t.Fatalf("complete coverage = %#v", coverage)
	}

	delete(authoritative, "order-1")
	coverage = calculateOrderFeeDayCoverage(fills, authoritative)
	if coverage.complete || coverage.requiredOrders != 2 || coverage.coveredOrders != 1 || coverage.source != "broker_statement_pending" {
		t.Fatalf("incomplete coverage = %#v", coverage)
	}
}

func TestCalculateOrderFeeDayCoverageAllowsNoTradeDay(t *testing.T) {
	coverage := calculateOrderFeeDayCoverage(nil, nil)
	if !coverage.complete || coverage.requiredOrders != 0 || coverage.coveredOrders != 0 || coverage.source != "not_applicable" {
		t.Fatalf("coverage = %#v", coverage)
	}
}

func TestCalculateContributionFillFeeDoesNotTrustUnavailableFee(t *testing.T) {
	fee := calculateContributionFillFee(trading.Fill{
		Fee: 3,
		AdapterContext: map[string]any{
			"fee_complete": false,
			"fee_source":   "unavailable",
		},
	}, contributionInstrument{}, nil)
	if fee.actual != 0 || fee.source != "missing" || !containsString(fee.flags, "missing_fee_rule") {
		t.Fatalf("fee = %#v", fee)
	}
}

func TestCalculateOrderFeeDayCoverageUsesOneRecordPerOrder(t *testing.T) {
	fees := authoritativeOrderFees([]ledger.OrderFeeRecord{{
		GatewayOrderID:      "gw-covered",
		FeeRecordID:         "fee-covered",
		TotalFee:            6.25,
		FeeComplete:         true,
		AssociationComplete: true,
		FeeSource:           "broker_order_fund_detail",
		FeeAsOf:             time.Date(2026, 8, 3, 15, 1, 0, 0, timeutil.Location()),
	}})
	coverage := calculateOrderFeeDayCoverage([]trading.Fill{
		{FillID: "fill-1", GatewayOrderID: "gw-covered"},
		{FillID: "fill-2", GatewayOrderID: "gw-covered"},
		{FillID: "fill-3", GatewayOrderID: "gw-missing"},
	}, fees)

	if coverage.requiredOrders != 2 || coverage.coveredOrders != 1 || coverage.complete {
		t.Fatalf("coverage = %#v", coverage)
	}
	if coverage.source != "broker_statement_pending" {
		t.Fatalf("coverage source = %q", coverage.source)
	}
}

func TestCalculateOrderFeeDayCoverageAcceptsCompleteFillFeeAndNoTradeDay(t *testing.T) {
	noTrades := calculateOrderFeeDayCoverage(nil, nil)
	if !noTrades.complete || noTrades.requiredOrders != 0 || noTrades.source != "not_applicable" {
		t.Fatalf("no-trade coverage = %#v", noTrades)
	}

	fillCoverage := calculateOrderFeeDayCoverage([]trading.Fill{{
		FillID:         "fill-zero-fee",
		GatewayOrderID: "gw-zero-fee",
		Fee:            0,
		AdapterContext: map[string]any{
			"fee_complete": true,
			"fee_source":   "broker_fill_detail",
		},
	}}, nil)
	if !fillCoverage.complete || fillCoverage.coveredOrders != 1 || fillCoverage.source != "broker_order_or_fill" {
		t.Fatalf("fill coverage = %#v", fillCoverage)
	}
}

func TestCalculateContributionsInfersETFT0GroupAndUsesHistoricalIOPV(t *testing.T) {
	location := timeutil.Location()
	buyTime := time.Date(2026, 7, 24, 10, 0, 0, 0, location)
	redeemTime := time.Date(2026, 7, 24, 10, 7, 35, 0, location)
	store := &fakePerformanceStore{
		orders: []trading.Order{
			{
				AccountID:      "acct-1",
				GatewayOrderID: "etf-buy",
				Symbol:         "159915",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideBuy,
				BusinessType:   trading.BusinessTypeStock,
				OrderQty:       1_000_000,
				AcceptedAt:     buyTime,
			},
			{
				AccountID:      "acct-1",
				GatewayOrderID: "etf-redeem",
				Symbol:         "159915",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideRedemption,
				BusinessType:   trading.BusinessTypeETF,
				OrderQty:       1_000_000,
				AcceptedAt:     redeemTime,
			},
		},
		fills: []trading.Fill{
			{
				FillID:         "etf-buy-fill",
				AccountID:      "acct-1",
				GatewayOrderID: "etf-buy",
				Symbol:         "159915",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideBuy,
				BusinessType:   trading.BusinessTypeStock,
				Price:          4.8,
				Qty:            1_000_000,
				MatchedAt:      buyTime,
			},
			{
				FillID:         "redeem-a",
				AccountID:      "acct-1",
				GatewayOrderID: "etf-redeem",
				Symbol:         "159915",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideRedemption,
				BusinessType:   trading.BusinessTypeETF,
				Qty:            400_000,
				MatchedAt:      redeemTime.Add(-time.Second),
			},
			{
				FillID:         "redeem-b",
				AccountID:      "acct-1",
				GatewayOrderID: "etf-redeem",
				Symbol:         "159915",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideRedemption,
				BusinessType:   trading.BusinessTypeETF,
				Qty:            600_000,
				MatchedAt:      redeemTime,
			},
		},
		daily: ledger.DailyPerformance{OpenNetAsset: 10_000_000},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{map[string]any{
				"security_id":     "159915.SZ",
				"name":            "创业板ETF",
				"instrument_type": "etf",
			}}},
		},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{}}},
		snapshots: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{map[string]any{
				"security_id": "159915.SZ",
				"iopv":        4.82,
				"timestamp":   "2026-07-24T10:07:34+08:00",
			}}},
		},
		cashComponents: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{map[string]any{
				"security_id":           "159915.SZ",
				"unit_subscribe_redeem": "1000000",
			}}},
		},
	}
	service, err := New(Options{
		Store:             store,
		Market:            marketClient,
		ETFT0FrictionRate: 0.0015,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateContributions(context.Background(), "acct-1", "2026-07-24")
	if err != nil {
		t.Fatalf("CalculateContributions() error = %v", err)
	}
	if len(result.Contributions) != 1 {
		t.Fatalf("contributions = %#v", result.Contributions)
	}
	item := result.Contributions[0]
	if item.StrategyType != StrategyETFRedemptionT0 || item.PnLStatus != "estimated" {
		t.Fatalf("t0 identity = %#v", item)
	}
	if item.BuyQuantity != 1_000_000 || item.RedemptionQuantity != 1_000_000 || item.RedemptionUnit != 1_000_000 || item.ReferenceIOPV == nil || *item.ReferenceIOPV != 4.82 {
		t.Fatalf("t0 quantities/iopv = %#v", item)
	}
	if item.Orders != 2 || item.Fills != 3 {
		t.Fatalf("t0 orders/fills = %d/%d, want 2/3", item.Orders, item.Fills)
	}
	if item.NetContribution == nil || *item.NetContribution != 12_770 {
		t.Fatalf("net contribution = %#v, want 12770", item.NetContribution)
	}
	if !containsString(item.QualityFlags, "historical_t0_order_group_inferred") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
}

func TestBuildT0GroupsFlagsPCFRedemptionUnitMismatch(t *testing.T) {
	location := timeutil.Location()
	buyTime := time.Date(2026, 7, 24, 10, 0, 0, 0, location)
	redeemTime := buyTime.Add(10 * time.Minute)
	orders := []trading.Order{
		{
			GatewayOrderID: "etf-buy",
			Symbol:         "159915",
			Exchange:       trading.ExchangeSZ,
			TradeSide:      trading.TradeSideBuy,
			BusinessType:   trading.BusinessTypeStock,
			OrderQty:       1_000_000,
			AcceptedAt:     buyTime,
		},
		{
			GatewayOrderID: "etf-redeem",
			Symbol:         "159915",
			Exchange:       trading.ExchangeSZ,
			TradeSide:      trading.TradeSideRedemption,
			BusinessType:   trading.BusinessTypeETF,
			OrderQty:       1_000_000,
			AcceptedAt:     redeemTime,
		},
	}
	fills := []trading.Fill{
		{
			FillID:         "etf-buy-fill",
			GatewayOrderID: "etf-buy",
			Symbol:         "159915",
			Exchange:       trading.ExchangeSZ,
			TradeSide:      trading.TradeSideBuy,
			BusinessType:   trading.BusinessTypeStock,
			Price:          4.8,
			Qty:            1_000_000,
			MatchedAt:      buyTime,
		},
		{
			FillID:         "etf-redeem-fill",
			GatewayOrderID: "etf-redeem",
			Symbol:         "159915",
			Exchange:       trading.ExchangeSZ,
			TradeSide:      trading.TradeSideRedemption,
			BusinessType:   trading.BusinessTypeETF,
			Qty:            1_000_000,
			MatchedAt:      redeemTime,
		},
	}

	service := &Service{
		market: &fakeContributionMarket{
			snapshots: market.MeridianResponse{
				StatusCode: http.StatusOK,
				Payload: map[string]any{"data": []any{map[string]any{
					"security_id": "159915.SZ",
					"iopv":        4.82,
					"timestamp":   "2026-07-24T10:09:59+08:00",
				}}},
			},
		},
		etfT0FrictionRate: 0.0015,
	}
	groups, _, _ := service.buildT0Groups(
		orders,
		fills,
		map[string]contributionInstrument{
			"159915.SZ": {SecurityID: "159915.SZ", InstrumentType: "etf"},
		},
		map[string]int64{"159915.SZ": 900_000},
	)
	if len(groups) != 1 {
		t.Fatalf("groups = %#v", groups)
	}
	if !containsString(groups[0].flags, "redemption_quantity_not_pcf_unit_multiple") {
		t.Fatalf("quality flags = %#v", groups[0].flags)
	}
	item := service.calculateT0Contribution(context.Background(), "2026-07-24", groups[0], 10_000_000)
	if item.PnLStatus != "missing" || item.NetContribution != nil {
		t.Fatalf("mismatched PCF unit contribution = %#v", item)
	}
}

func TestRedemptionFillsFromComponentTransfersUsesOnlyParentETFRecord(t *testing.T) {
	matchedAt := time.Date(2026, 7, 29, 13, 4, 31, 0, timeutil.Location())
	orders := []trading.Order{{
		AccountID:      "acct-1",
		GatewayOrderID: "external-huaxin-acct-1-stream-1",
		Symbol:         "588200",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideRedemption,
		BusinessType:   trading.BusinessTypeETF,
		OrderQty:       4_500_000,
	}}
	transfers := []trading.ComponentTransfer{
		{
			FillID:         "parent-transfer",
			AccountID:      "acct-1",
			GatewayOrderID: "external-huaxin-acct-1-stream-1",
			Symbol:         "588200",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideRedemption,
			BusinessType:   trading.BusinessTypeETF,
			Qty:            4_500_000,
			MatchedAt:      matchedAt,
		},
		{
			FillID:         "component-transfer",
			AccountID:      "acct-1",
			GatewayOrderID: "external-huaxin-acct-1-stream-1",
			Symbol:         "688361",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideRedemption,
			BusinessType:   trading.BusinessTypeETF,
			Qty:            425,
			MatchedAt:      matchedAt,
		},
	}

	fills := redemptionFillsFromComponentTransfers(orders, nil, transfers)
	if len(fills) != 1 {
		t.Fatalf("fills = %#v", fills)
	}
	if fills[0].Symbol != "588200" || fills[0].Qty != 4_500_000 ||
		fills[0].AdapterContext["relay_component_transfer_source"] != true {
		t.Fatalf("fill = %#v", fills[0])
	}
}

func TestCalculateContributionsExcludesETFComponentSaleFees(t *testing.T) {
	matchedAt := time.Date(2026, 7, 24, 10, 8, 0, 0, timeutil.Location())
	store := &fakePerformanceStore{
		fills: []trading.Fill{
			{
				FillID:         "redeem",
				AccountID:      "acct-1",
				GatewayOrderID: "redeem-order",
				Symbol:         "159915",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideRedemption,
				BusinessType:   trading.BusinessTypeETF,
				Qty:            1_000_000,
				MatchedAt:      matchedAt,
			},
			{
				FillID:         "component-sale",
				AccountID:      "acct-1",
				GatewayOrderID: "component-order",
				Symbol:         "300750",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideSell,
				BusinessType:   trading.BusinessTypeStock,
				Price:          400,
				Qty:            100,
				MatchedAt:      matchedAt.Add(time.Minute),
			},
		},
		daily: ledger.DailyPerformance{OpenNetAsset: 10_000_000},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "159915.SZ", "instrument_type": "etf"},
				map[string]any{"security_id": "300750.SZ", "instrument_type": "stock"},
			}},
		},
		bars:      market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{}}},
		snapshots: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260724")
	if err != nil {
		t.Fatalf("CalculateContributions() error = %v", err)
	}
	var component SecurityContribution
	for _, item := range result.Contributions {
		if item.SecurityID == "300750.SZ" {
			component = item
			break
		}
	}
	if component.StrategyType != StrategyETFComponentTransfer || component.PnLStatus != "excluded" {
		t.Fatalf("component contribution = %#v", component)
	}
	if component.EffectiveFee != 0 || component.FeeSource != "included_in_etf_t0_friction" {
		t.Fatalf("component fee = %#v", component)
	}
	if containsString(component.QualityFlags, "missing_fee_rule") {
		t.Fatalf("component flags = %#v", component.QualityFlags)
	}
}

func TestCalculateContributionsDoesNotAssumeMissingOpenPositionIsZero(t *testing.T) {
	store := &fakePerformanceStore{
		positions: map[string][]trading.Position{
			"close": {{
				AccountID: "acct-1",
				Symbol:    "510300",
				Exchange:  trading.ExchangeSH,
				Quantity:  100_000,
			}},
		},
		daily: ledger.DailyPerformance{OpenNetAsset: 1_000_000},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "510300.SH", "instrument_type": "etf"},
			}},
		},
		bars: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "510300.SH", "pre_close": 4.8, "close": 4.9},
			}},
		},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260724")
	if err != nil {
		t.Fatalf("CalculateContributions() error = %v", err)
	}
	item := result.Contributions[0]
	if item.PnLStatus != "missing" || item.NetContribution != nil {
		t.Fatalf("contribution = %#v", item)
	}
	if !containsString(item.QualityFlags, "missing_open_position_snapshot") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
}

func TestCalculateContributionsBlocksReconciledSnapshotsWithQuantityMismatch(t *testing.T) {
	store := &fakePerformanceStore{
		positions: map[string][]trading.Position{
			"open": {{
				AccountID: "acct-1",
				Symbol:    "600000",
				Exchange:  trading.ExchangeSH,
				Quantity:  100,
			}},
			"close": {{
				AccountID: "acct-1",
				Symbol:    "600000",
				Exchange:  trading.ExchangeSH,
				Quantity:  90,
			}},
		},
		daily: ledger.DailyPerformance{OpenNetAsset: 100_000},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "600000.SH", "instrument_type": "stock"},
			}},
		},
		bars: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "600000.SH", "pre_close": 10.0, "close": 10.5},
			}},
		},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260724")
	if err != nil {
		t.Fatalf("CalculateContributions() error = %v", err)
	}
	item := result.Contributions[0]
	if item.PnLStatus != "missing" || item.NetContribution != nil || result.Summary.MissingItems != 1 {
		t.Fatalf("contribution = %#v summary=%#v", item, result.Summary)
	}
	if !containsString(item.QualityFlags, "position_quantity_not_reconciled") || !containsString(item.QualityFlags, "position_quantity_bridge_incomplete") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
}

func TestCalculateContributionsFallsBackToPreviousCloseWithQuantityBridge(t *testing.T) {
	store := &fakePerformanceStore{
		fills: []trading.Fill{{
			FillID:         "sell",
			AccountID:      "acct-1",
			GatewayOrderID: "sell-order",
			Symbol:         "600000",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideSell,
			BusinessType:   trading.BusinessTypeStock,
			Price:          11,
			Qty:            100,
		}},
		positionsByDate: map[string][]trading.Position{
			"2026-07-22|close": {{
				AccountID: "acct-1",
				Symbol:    "600000",
				Exchange:  trading.ExchangeSH,
				Quantity:  100,
			}},
		},
		daily: ledger.DailyPerformance{OpenNetAsset: 100_000},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "600000.SH", "instrument_type": "stock"},
			}},
		},
		bars: market.MeridianResponse{
			StatusCode: 200,
			Payload: map[string]any{"data": []any{
				map[string]any{"security_id": "600000.SH", "pre_close": 10.0, "close": 10.5},
			}},
		},
	}
	service, err := New(Options{Store: store, Market: marketClient, Calendar: weekdayCalendar{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateContributions(context.Background(), "acct-1", "20260723")
	if err != nil {
		t.Fatalf("CalculateContributions() error = %v", err)
	}
	item := result.Contributions[0]
	if item.PnLStatus != "estimated" || item.NetContribution == nil || *item.NetContribution != 100 {
		t.Fatalf("contribution = %#v", item)
	}
	if !containsString(item.QualityFlags, "open_position_from_previous_close") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
}

func TestCalculateReverseRepoAggregatesFillsAndPersists(t *testing.T) {
	store := &fakePerformanceStore{
		fills: []trading.Fill{
			{
				FillID:         "f-actual-a",
				AccountID:      "acct-1",
				GatewayOrderID: "repo-a",
				Symbol:         "204001",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          1.4,
				Qty:            1000,
				Fee:            1.2,
			},
			{
				FillID:         "relay-summary:repo-a",
				AccountID:      "acct-1",
				GatewayOrderID: "repo-a",
				Symbol:         "204001",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          1.4,
				Qty:            1000,
			},
			{
				FillID:         "f-actual-b",
				AccountID:      "acct-1",
				GatewayOrderID: "repo-b",
				Symbol:         "204001",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          1.5,
				Qty:            500,
			},
			{
				FillID:         "stock-sell",
				AccountID:      "acct-1",
				GatewayOrderID: "stock-order",
				Symbol:         "600000",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          9.2,
				Qty:            100,
			},
		},
		repoRule: ledger.FeeRule{RuleID: "repo-fee", RepoFeeRate: 0.00005},
		now:      time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC),
	}
	service, err := New(Options{
		Store:    store,
		Calendar: weekdayCalendar{},
		Now: func() time.Time {
			return store.now
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateReverseRepo(context.Background(), "acct-1", "20260723", true)
	if err != nil {
		t.Fatalf("CalculateReverseRepo() error = %v", err)
	}

	if result.Orders != 2 || result.Fills != 2 {
		t.Fatalf("counts orders=%d fills=%d, want 2/2", result.Orders, result.Fills)
	}
	if result.FirstSettlement != "2026-07-24" || result.MaturitySettlement != "2026-07-27" || result.OccupationDays != 3 {
		t.Fatalf("settlement = %s/%s days=%d", result.FirstSettlement, result.MaturitySettlement, result.OccupationDays)
	}
	if len(result.Accruals) != 2 || result.Accruals[0].GatewayOrderID != "repo-a" || result.Accruals[1].GatewayOrderID != "repo-b" {
		t.Fatalf("accrual order = %#v", result.Accruals)
	}
	assertClose(t, result.Accruals[0].Principal, 100000)
	assertClose(t, result.Accruals[0].GrossInterest, 11.506849)
	assertClose(t, result.Accruals[0].EffectiveFee, 1.2)
	if result.Accruals[0].FeeSource != "actual_fill" {
		t.Fatalf("repo-a fee_source = %q", result.Accruals[0].FeeSource)
	}
	assertClose(t, result.Accruals[1].Principal, 50000)
	assertClose(t, result.Accruals[1].GrossInterest, 6.164384)
	assertClose(t, result.Accruals[1].EffectiveFee, 2.5)
	if result.Accruals[1].FeeSource != "fee_rule:repo-fee" {
		t.Fatalf("repo-b fee_source = %q", result.Accruals[1].FeeSource)
	}
	if len(store.upserts) != 2 {
		t.Fatalf("persisted accruals = %d, want 2", len(store.upserts))
	}
	if len(store.fillQueries) != 1 || store.fillQueries[0].TradeDate != "2026-07-23" || !store.fillQueries[0].History {
		t.Fatalf("fill query = %#v", store.fillQueries)
	}
}

func TestCalculateReverseRepoMarksMissingFeeRule(t *testing.T) {
	store := &fakePerformanceStore{
		fills: []trading.Fill{{
			FillID:         "f-1",
			AccountID:      "acct-1",
			GatewayOrderID: "repo-a",
			Symbol:         "204001",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideSell,
			Price:          1.35,
			Qty:            10,
		}},
		repoRuleErr: sql.ErrNoRows,
	}
	service, err := New(Options{Store: store, Calendar: weekdayCalendar{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateReverseRepo(context.Background(), "acct-1", "2026-07-23", false)
	if err != nil {
		t.Fatalf("CalculateReverseRepo() error = %v", err)
	}

	if result.Persisted {
		t.Fatal("Persisted = true, want false")
	}
	if len(result.Accruals) != 1 {
		t.Fatalf("accruals = %d, want 1", len(result.Accruals))
	}
	if result.Accruals[0].FeeSource != "missing" {
		t.Fatalf("fee_source = %q, want missing", result.Accruals[0].FeeSource)
	}
	if len(result.Accruals[0].QualityFlags) != 1 || result.Accruals[0].QualityFlags[0] != "missing_repo_fee" {
		t.Fatalf("quality flags = %#v", result.Accruals[0].QualityFlags)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("upserts = %d, want 0", len(store.upserts))
	}
}

func TestCalculateEconomicNAVUsesCashFlowsReverseRepoAndPersists(t *testing.T) {
	now := time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)
	store := &fakePerformanceStore{
		daily: ledger.DailyPerformance{
			AccountID:           "acct-1",
			TradeDate:           "2026-07-24",
			CashTotal:           900000,
			NetAsset:            1000000,
			OpenNetAsset:        980000,
			OpenSnapshotSource:  "open",
			PositionMarketValue: 100000,
			FillsCount:          2,
			BuyAmount:           30000,
			SellAmount:          35000,
			Turnover:            65000,
			FeeTotal:            12.3,
		},
		baselines: []ledger.NavBaseline{{
			AccountID:          "acct-1",
			EffectiveDate:      "2026-07-01",
			Status:             "confirmed",
			InitialEconomicNAV: 950000,
		}},
		cashByClass: map[string][]ledger.CashLedgerEntry{
			"external_flow": {{
				EntryID:     "flow-1",
				AccountID:   "acct-1",
				TradeDate:   "2026-07-24",
				Amount:      10000,
				EffectiveAt: time.Date(2026, 7, 24, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				Status:      "confirmed",
			}},
			"income_expense": {{
				EntryID:   "income-1",
				AccountID: "acct-1",
				TradeDate: "2026-07-24",
				Amount:    5,
				Status:    "confirmed",
			}},
		},
		repoAccruals: []ledger.ReverseRepoAccrual{{
			AccountID:      "acct-1",
			TradeDate:      "2026-07-24",
			GatewayOrderID: "repo-1",
			Principal:      100000,
			NetInterest:    10,
			Receivable:     100010,
			EffectiveFee:   1,
			Status:         "estimated",
		}},
		navs: []ledger.PerformanceNAV{{
			AccountID:        "acct-1",
			TradeDate:        "2026-07-23",
			CumulativeNAV:    1.02,
			CloseEconomicNAV: 980000,
		}},
		now: now,
	}
	service, err := New(Options{
		Store:          store,
		FormulaVersion: "performance_economic_nav.unit",
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateEconomicNAV(context.Background(), "acct-1", "20260724", EconomicNAVOptions{Persist: true})
	if err != nil {
		t.Fatalf("CalculateEconomicNAV() error = %v", err)
	}

	if !result.Persisted {
		t.Fatal("Persisted = false, want true")
	}
	assertClose(t, result.NAV.OpenEconomicNAV, 980000)
	assertClose(t, result.NAV.CloseEconomicNAV, 1000000)
	assertClose(t, result.NAV.ExternalNetFlow, 10000)
	assertClose(t, result.NAV.AccountDayPnL, 10000)
	if result.NAV.FormulaVersion != "performance_economic_nav.unit" {
		t.Fatalf("formula = %q", result.NAV.FormulaVersion)
	}
	if len(store.navUpserts) != 1 || len(store.reconciliationUpserts) != 1 {
		t.Fatalf("upserts nav=%d reconciliation=%d", len(store.navUpserts), len(store.reconciliationUpserts))
	}
	if result.ReverseRepo.Orders != 1 || result.ReverseRepo.Source != "ledger" {
		t.Fatalf("reverse repo summary = %#v", result.ReverseRepo)
	}
	if result.ReverseRepo.PrincipalTreatment != "separate" || result.ReverseRepo.PrincipalReceivable != 100000 || result.ReverseRepo.PrincipalCashOverlap != 0 {
		t.Fatalf("reverse repo principal resolution = %#v", result.ReverseRepo)
	}
	assertClose(t, result.ReverseRepo.EstimatedNetInterest, 10)
	assertClose(t, result.ReverseRepo.RecognizedNetInterest, 0)
	assertClose(t, result.ReverseRepo.EstimatedReceivable, 100010)
	assertClose(t, result.ReverseRepo.Receivable, 100000)
	if !containsString(result.QualityFlags, "reverse_repo_estimated_interest_excluded") {
		t.Fatalf("quality flags = %#v", result.QualityFlags)
	}
	if result.NAV.PnLComponents["cash_management"] == nil {
		t.Fatalf("missing cash management component: %#v", result.NAV.PnLComponents)
	}
}

func TestResolveReverseRepoPrincipalUsesAccountingIdentity(t *testing.T) {
	tests := []struct {
		name                    string
		baseCloseNAV            float64
		openEconomicNAV         float64
		formalAttributedPnL     float64
		wantTreatment           string
		wantCashOverlap         float64
		wantPrincipalReceivable float64
		wantAmbiguous           bool
	}{
		{
			name:                    "principal separate from visible cash",
			baseCloseNAV:            900,
			openEconomicNAV:         1000,
			formalAttributedPnL:     0,
			wantTreatment:           "separate",
			wantPrincipalReceivable: 100,
		},
		{
			name:                "principal embedded in visible cash",
			baseCloseNAV:        1000,
			openEconomicNAV:     1000,
			formalAttributedPnL: 0,
			wantTreatment:       "embedded",
			wantCashOverlap:     100,
		},
		{
			name:                    "ambiguous bridge is blocked",
			baseCloseNAV:            950,
			openEconomicNAV:         1000,
			formalAttributedPnL:     0,
			wantTreatment:           "ambiguous",
			wantPrincipalReceivable: 0,
			wantCashOverlap:         100,
			wantAmbiguous:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := EconomicNAVReverseRepoSummary{
				Orders:               1,
				Principal:            100,
				NetInterest:          2,
				EstimatedNetInterest: 2,
			}
			flags := resolveReverseRepoPrincipal(&summary, tt.baseCloseNAV, tt.openEconomicNAV, 0, 0, tt.formalAttributedPnL, 10)

			if summary.PrincipalTreatment != tt.wantTreatment {
				t.Fatalf("treatment = %q, want %q", summary.PrincipalTreatment, tt.wantTreatment)
			}
			assertClose(t, summary.PrincipalCashOverlap, tt.wantCashOverlap)
			assertClose(t, summary.PrincipalReceivable, tt.wantPrincipalReceivable)
			assertClose(t, summary.Receivable, tt.wantPrincipalReceivable)
			assertClose(t, summary.RecognizedNetInterest, 0)
			if containsString(flags, "reverse_repo_principal_treatment_ambiguous") != tt.wantAmbiguous {
				t.Fatalf("flags = %#v", flags)
			}
		})
	}
}

func TestCalculateReturnDenominatorUsesMidSessionWeightForDateOnlyCashFlow(t *testing.T) {
	tradeDate := time.Date(2026, 7, 29, 0, 0, 0, 0, timeutil.Location())
	flows := []ledger.CashLedgerEntry{{
		EntryID:     "withdraw-date-only",
		Amount:      -200,
		EffectiveAt: time.Date(2026, 7, 29, 15, 0, 0, 0, timeutil.Location()),
		RawPayload: map[string]any{
			"effective_time_precision": "date",
		},
	}}

	denominator, details, flags := calculateReturnDenominator(1000, flows, tradeDate)

	assertClose(t, denominator, 900)
	if len(details) != 1 || details[0]["weight"] != 0.5 || details[0]["weighted"] != -100.0 {
		t.Fatalf("weight details = %#v", details)
	}
	if !containsString(flags, "external_flow_time_estimated_mid_session") {
		t.Fatalf("flags = %#v", flags)
	}
}

func TestCalculateEconomicNAVUsesMeridianPositionValuationInsteadOfBrokerCost(t *testing.T) {
	store := &fakePerformanceStore{
		daily: ledger.DailyPerformance{
			AccountID:          "acct-1",
			TradeDate:          "2026-07-24",
			CashTotal:          899500,
			NetAsset:           899500,
			OpenNetAsset:       900000,
			OpenSnapshotSource: "open",
		},
		positions: map[string][]trading.Position{
			"open": {{
				AccountID:   "acct-1",
				Symbol:      "600000",
				Exchange:    trading.ExchangeSH,
				Quantity:    100,
				AvgCost:     9999,
				MarketValue: 999900,
			}},
			"close": {{
				AccountID:   "acct-1",
				Symbol:      "600000",
				Exchange:    trading.ExchangeSH,
				Quantity:    150,
				AvgCost:     8888,
				MarketValue: 1333200,
			}},
		},
		fills: []trading.Fill{{
			FillID:         "buy-1",
			AccountID:      "acct-1",
			GatewayOrderID: "order-1",
			Symbol:         "600000",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideBuy,
			BusinessType:   trading.BusinessTypeStock,
			Price:          10,
			Qty:            50,
		}},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "instrument_type": "stock",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "pre_close": 9.0, "close": 11.0,
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateEconomicNAV(context.Background(), "acct-1", "20260724", EconomicNAVOptions{})
	if err != nil {
		t.Fatalf("CalculateEconomicNAV() error = %v", err)
	}
	assertClose(t, result.Valuation.OpenPositionValue, 900)
	assertClose(t, result.Valuation.ClosePositionValue, 1650)
	assertClose(t, result.NAV.OpenEconomicNAV, 900900)
	assertClose(t, result.NAV.CloseEconomicNAV, 901150)
	assertClose(t, result.NAV.AccountDayPnL, 250)
	if result.Valuation.BrokerOpenPositionValue == result.Valuation.OpenPositionValue || result.Valuation.BrokerClosePositionValue == result.Valuation.ClosePositionValue {
		t.Fatalf("broker values unexpectedly used: %#v", result.Valuation)
	}
	if !containsString(result.QualityFlags, "broker_position_cost_excluded") {
		t.Fatalf("quality flags = %#v", result.QualityFlags)
	}
}

func TestCalculateCostLedgerRollsTrustedOpeningCostWithMovingAverage(t *testing.T) {
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{
			AccountID:             "acct-1",
			InceptionDate:         "2026-07-24",
			Status:                "confirmed",
			CleanStart:            true,
			OpeningPositionSource: "broker_open_snapshot",
		},
		positions: map[string][]trading.Position{
			"open": {{
				AccountID: "acct-1",
				Symbol:    "600000",
				Exchange:  trading.ExchangeSH,
				Quantity:  100,
				AvgCost:   10,
			}},
			"close": {{
				AccountID: "acct-1",
				Symbol:    "600000",
				Exchange:  trading.ExchangeSH,
				Quantity:  150,
			}},
		},
		fills: []trading.Fill{
			{FillID: "buy", AccountID: "acct-1", GatewayOrderID: "buy-order", Symbol: "600000", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Price: 12, Qty: 100, Fee: 2},
			{FillID: "sell", AccountID: "acct-1", GatewayOrderID: "sell-order", Symbol: "600000", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideSell, BusinessType: trading.BusinessTypeStock, Price: 14, Qty: 50, Fee: 1},
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "instrument_type": "stock",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "pre_close": 11.0, "close": 13.0,
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260724", CostLedgerOptions{Persist: true})
	if err != nil {
		t.Fatalf("CalculateCostLedger() error = %v", err)
	}
	if result.Status != "calculated" || !result.Persisted || len(result.Positions) != 1 {
		t.Fatalf("result = %#v", result)
	}
	item := result.Positions[0]
	if item.CloseQuantity != 150 || item.QuantityResidual != 0 {
		t.Fatalf("quantity state = %#v", item)
	}
	assertClose(t, item.CloseTotalCost, 1651.5)
	assertClose(t, item.AverageCost, 11.01)
	assertClose(t, item.RealizedPnL, 148.5)
	assertClose(t, item.CloseMarketValue, 1950)
	assertClose(t, item.UnrealizedPnL, 298.5)
	if len(store.costUpserts) != 1 {
		t.Fatalf("cost upserts = %#v", store.costUpserts)
	}
}

func TestCalculateCostLedgerBlocksQuantityMismatch(t *testing.T) {
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{
			AccountID:             "acct-1",
			InceptionDate:         "2026-07-24",
			Status:                "confirmed",
			OpeningPositionSource: "broker_open_snapshot",
		},
		positions: map[string][]trading.Position{
			"open":  {{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100, AvgCost: 10}},
			"close": {{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 80}},
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "instrument_type": "stock",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "pre_close": 10.0, "close": 11.0,
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260724", CostLedgerOptions{})
	if err != nil {
		t.Fatalf("CalculateCostLedger() error = %v", err)
	}
	if result.Status != "blocked" || result.Summary.QuantityBreaks != 1 {
		t.Fatalf("result = %#v", result)
	}
	if !containsString(result.Positions[0].QualityFlags, "position_quantity_not_reconciled") {
		t.Fatalf("quality flags = %#v", result.Positions[0].QualityFlags)
	}
}

func TestCalculateCostLedgerAdjustsQuantityForMeridianCorporateAction(t *testing.T) {
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{
			AccountID:     "acct-1",
			InceptionDate: "2026-07-20",
			Status:        "confirmed",
		},
		costStates: []ledger.PositionCostState{{
			AccountID:      "acct-1",
			TradeDate:      "2026-07-20",
			Symbol:         "588200",
			Exchange:       "SH",
			CostBucket:     "CORE",
			Status:         "calculated",
			CloseQuantity:  100,
			CloseTotalCost: 1000,
		}},
		positions: map[string][]trading.Position{
			"open":  {{AccountID: "acct-1", Symbol: "588200", Exchange: trading.ExchangeSH, Quantity: 300}},
			"close": {{AccountID: "acct-1", Symbol: "588200", Exchange: trading.ExchangeSH, Quantity: 300}},
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "588200.SH", "instrument_type": "etf",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "588200.SH", "pre_close": 0.4, "close": 0.41,
		}}}},
		adjustFactors: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id":   "588200.SH",
			"ex_date":       20260721,
			"ex_factor":     3.0,
			"ex_cum_factor": 3.0,
			"source":        "mysql_ti_db",
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260721", CostLedgerOptions{})
	if err != nil {
		t.Fatalf("CalculateCostLedger() error = %v", err)
	}
	if result.Status != "calculated" || result.Summary.CorporateActions != 1 || result.Summary.QuantityAdjustments != 1 {
		t.Fatalf("result = %#v", result)
	}
	item := result.Positions[0]
	if item.PreviousCloseQuantity != 100 || item.BrokerOpenQuantity != 300 || item.OpenQuantity != 300 || item.CloseQuantity != 300 {
		t.Fatalf("quantities = %#v", item)
	}
	if item.CorporateActionType != "quantity_adjustment" || item.CorporateActionFactor != 3 || item.CorporateActionQuantityDelta != 200 {
		t.Fatalf("corporate action = %#v", item)
	}
	assertClose(t, item.OpenTotalCost, 1000)
	assertClose(t, item.CloseTotalCost, 1000)
	assertClose(t, item.AverageCost, 3.333333)
	if !containsString(item.QualityFlags, "corporate_action_quantity_adjusted") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
	if len(marketClient.queries) < 3 || marketClient.queries[2].Get("start_date") != "20260721" {
		t.Fatalf("Meridian queries = %#v", marketClient.queries)
	}
}

func TestCalculateCostLedgerTreatsUnchangedQuantityFactorAsPriceAdjustment(t *testing.T) {
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{AccountID: "acct-1", InceptionDate: "2026-07-15", Status: "confirmed"},
		costStates: []ledger.PositionCostState{{
			AccountID: "acct-1", TradeDate: "2026-07-15", Symbol: "600000", Exchange: "SH",
			CostBucket: "CORE", Status: "calculated", CloseQuantity: 100, CloseTotalCost: 1000,
		}},
		positions: map[string][]trading.Position{
			"open":  {{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100}},
			"close": {{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100}},
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "instrument_type": "stock",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "pre_close": 8.89, "close": 8.95,
		}}}},
		adjustFactors: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "600000.SH", "ex_date": "20260716", "ex_factor": 1.0472451375925815,
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260716", CostLedgerOptions{})
	if err != nil {
		t.Fatalf("CalculateCostLedger() error = %v", err)
	}
	item := result.Positions[0]
	if result.Status != "calculated" || item.CorporateActionType != "price_adjustment" || item.OpenQuantity != 100 {
		t.Fatalf("result/item = %#v / %#v", result, item)
	}
	assertClose(t, item.CloseTotalCost, 1000)
	if !containsString(item.QualityFlags, "corporate_action_price_adjustment") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
}

func TestCalculateCostLedgerBlocksCorporateActionQuantityMismatch(t *testing.T) {
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{AccountID: "acct-1", InceptionDate: "2026-07-20", Status: "confirmed"},
		costStates: []ledger.PositionCostState{{
			AccountID: "acct-1", TradeDate: "2026-07-20", Symbol: "588200", Exchange: "SH",
			CostBucket: "CORE", Status: "calculated", CloseQuantity: 100, CloseTotalCost: 1000,
		}},
		positions: map[string][]trading.Position{
			"open":  {{AccountID: "acct-1", Symbol: "588200", Exchange: trading.ExchangeSH, Quantity: 250}},
			"close": {{AccountID: "acct-1", Symbol: "588200", Exchange: trading.ExchangeSH, Quantity: 250}},
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "588200.SH", "instrument_type": "etf",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "588200.SH", "pre_close": 0.4, "close": 0.41,
		}}}},
		adjustFactors: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "588200.SH", "ex_date": 20260721, "ex_factor": 3.0,
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260721", CostLedgerOptions{})
	if err != nil {
		t.Fatalf("CalculateCostLedger() error = %v", err)
	}
	item := result.Positions[0]
	if result.Status != "blocked" || result.Summary.CorporateActionBreaks != 1 || item.CorporateActionType != "mismatch" {
		t.Fatalf("result/item = %#v / %#v", result, item)
	}
	if !containsString(item.QualityFlags, "corporate_action_mismatch") {
		t.Fatalf("quality flags = %#v", item.QualityFlags)
	}
}

func TestCalculateCostLedgerSeparatesExplicitETFT0CostFromCorePosition(t *testing.T) {
	location := timeutil.Location()
	buyTime := time.Date(2026, 8, 3, 10, 0, 0, 0, location)
	redeemTime := buyTime.Add(10 * time.Minute)
	store := &fakePerformanceStore{
		inception: ledger.PerformanceInception{
			AccountID:             "acct-1",
			InceptionDate:         "2026-08-03",
			Status:                "confirmed",
			OpeningPositionSource: "broker_open_snapshot",
		},
		positions: map[string][]trading.Position{
			"open":  {{AccountID: "acct-1", Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 100, AvgCost: 4}},
			"close": {{AccountID: "acct-1", Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 200}},
		},
		orders: []trading.Order{
			{
				AccountID: "acct-1", GatewayOrderID: "t0-buy", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 1000,
				T0OrderGroupID: "basket-1", AcceptedAt: buyTime,
			},
			{
				AccountID: "acct-1", GatewayOrderID: "core-buy", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 100,
				AcceptedAt: buyTime.Add(time.Minute),
			},
			{
				AccountID: "acct-1", GatewayOrderID: "t0-redeem", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, OrderQty: 1000,
				T0OrderGroupID: "basket-1", AcceptedAt: redeemTime,
			},
		},
		fills: []trading.Fill{
			{
				FillID: "t0-buy-fill", AccountID: "acct-1", GatewayOrderID: "t0-buy", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Price: 4.8, Qty: 1000,
				T0OrderGroupID: "basket-1", MatchedAt: buyTime,
			},
			{
				FillID: "core-buy-fill", AccountID: "acct-1", GatewayOrderID: "core-buy", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Price: 4.7, Qty: 100, Fee: 1,
				MatchedAt: buyTime.Add(time.Minute),
			},
			{
				FillID: "t0-redeem-fill", AccountID: "acct-1", GatewayOrderID: "t0-redeem", Symbol: "159915", Exchange: trading.ExchangeSZ,
				TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, Qty: 1000,
				T0OrderGroupID: "basket-1", MatchedAt: redeemTime,
			},
		},
	}
	marketClient := &fakeContributionMarket{
		metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "159915.SZ", "instrument_type": "etf",
		}}}},
		bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "159915.SZ", "pre_close": 4.6, "close": 4.9,
		}}}},
		cashComponents: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{
			"security_id": "159915.SZ", "unit_subscribe_redeem": 1000,
		}}}},
	}
	service, err := New(Options{Store: store, Market: marketClient})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateCostLedger(context.Background(), "acct-1", "20260803", CostLedgerOptions{})
	if err != nil {
		t.Fatalf("CalculateCostLedger() error = %v", err)
	}
	if result.Status != "calculated" || result.FormulaVersion != "performance_position_cost.v3" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Positions) != 2 || result.Summary.T0CostBuckets != 1 || result.Summary.T0BlockedBuckets != 0 {
		t.Fatalf("positions/summary = %#v / %#v", result.Positions, result.Summary)
	}
	var core, t0 ledger.PositionCostState
	for _, item := range result.Positions {
		if item.CostBucket == "CORE" {
			core = item
		} else if item.CostBucket == "ETF_T0:basket-1" {
			t0 = item
		}
	}
	if core.CloseQuantity != 200 || core.BuyQuantity != 100 || core.BrokerCloseQuantity != 200 || core.QuantityResidual != 0 {
		t.Fatalf("core = %#v", core)
	}
	assertClose(t, core.CloseTotalCost, 871)
	if t0.BuyQuantity != 1000 || t0.SellQuantity != 1000 || t0.CloseQuantity != 0 || t0.CloseTotalCost != 0 {
		t.Fatalf("t0 = %#v", t0)
	}
	assertClose(t, t0.BuyAmount, 4800)
	if t0.FeeSource != "included_in_etf_t0_friction" || !containsString(t0.QualityFlags, "etf_t0_cost_separated") {
		t.Fatalf("t0 quality = %#v", t0)
	}
	if result.Summary.T0BuyQuantity != 1000 || result.Summary.T0RedemptionQuantity != 1000 || result.Summary.T0BuyAmount != 4800 {
		t.Fatalf("t0 summary = %#v", result.Summary)
	}
}

func TestBuildT0CostBucketsBlocksAmbiguousHistoricalGroup(t *testing.T) {
	group := t0RedemptionGroup{
		securityID:     "588200.SH",
		groupID:        "redeem-1",
		redemptionUnit: 1000,
		flags:          []string{"historical_t0_order_group_inferred", "ambiguous_t0_order_group"},
		buyFills: []trading.Fill{{
			FillID: "buy", AccountID: "acct-1", GatewayOrderID: "buy-1", Symbol: "588200", Exchange: trading.ExchangeSH,
			TradeSide: trading.TradeSideBuy, Price: 1.2, Qty: 1000,
		}},
		redemptions: []trading.Fill{{
			FillID: "redeem", AccountID: "acct-1", GatewayOrderID: "redeem-1", Symbol: "588200", Exchange: trading.ExchangeSH,
			TradeSide: trading.TradeSideRedemption, Qty: 1000,
		}},
	}
	result := buildT0CostBuckets("acct-1", "2026-08-03", []t0RedemptionGroup{group})
	if len(result.states) != 1 || result.states[0].item.Status != "blocked" {
		t.Fatalf("result = %#v", result)
	}
	if result.states[0].item.CostBucket != "ETF_T0:redeem-1" || !containsString(result.states[0].flags, "ambiguous_t0_order_group") {
		t.Fatalf("state = %#v", result.states[0])
	}
}

func TestReconcileEconomicNAVUsesNextOpenObservation(t *testing.T) {
	store := &fakePerformanceStore{
		navs: []ledger.PerformanceNAV{{
			PerformanceNAVPK: 42,
			AccountID:        "acct-1",
			TradeDate:        "2026-07-24",
			Version:          2,
			Status:           "provisional",
			FormulaVersion:   "performance_economic_nav.unit",
			CloseEconomicNAV: 1000000,
			QualityFlags:     []string{"strategy_attribution_pending"},
		}},
		observation: ledger.AssetPositionObservation{
			AccountID:           "acct-1",
			TradeDate:           "2026-07-27",
			SnapshotType:        "open",
			CashTotal:           880000,
			PositionMarketValue: 120020,
			PositionsCount:      3,
		},
		cashByClass: map[string][]ledger.CashLedgerEntry{
			"external_flow": {{
				EntryID:     "deposit-1",
				AccountID:   "acct-1",
				TradeDate:   "2026-07-27",
				Amount:      100,
				EffectiveAt: time.Date(2026, 7, 27, 9, 1, 0, 0, time.FixedZone("CST", 8*3600)),
				Status:      "confirmed",
			}},
			"income_expense": {{
				EntryID:     "interest-1",
				AccountID:   "acct-1",
				TradeDate:   "2026-07-27",
				Amount:      -80,
				EffectiveAt: time.Date(2026, 7, 27, 9, 1, 0, 0, time.FixedZone("CST", 8*3600)),
				Status:      "confirmed",
			}},
		},
	}
	service, err := New(Options{Store: store, Calendar: weekdayCalendar{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.ReconcileEconomicNAV(context.Background(), "acct-1", "20260724", EconomicNAVReconcileOptions{Persist: true})
	if err != nil {
		t.Fatalf("ReconcileEconomicNAV() error = %v", err)
	}

	if !result.Persisted {
		t.Fatal("Persisted = false, want true")
	}
	if result.ObservedTradeDate != "2026-07-27" {
		t.Fatalf("observed date = %q", result.ObservedTradeDate)
	}
	assertClose(t, result.Reconciliation.ObservedOpenAssets, 1000020)
	assertClose(t, result.Reconciliation.OvernightExternalNetFlow, 100)
	assertClose(t, result.Reconciliation.KnownOvernightIncomeExpense, -80)
	assertClose(t, result.Reconciliation.Residual, 0)
	if result.Status != "auto_completed" {
		t.Fatalf("status = %q", result.Status)
	}
	if len(store.reconciliationUpserts) != 1 || store.reconciliationUpserts[0].PerformanceNAVPK != 42 {
		t.Fatalf("reconciliation upserts = %#v", store.reconciliationUpserts)
	}
}

func TestReviewNAVReconciliationConfirmsAndFinalizesNAV(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 8, 0, 0, time.UTC)
	store := &fakePerformanceStore{
		navs: []ledger.PerformanceNAV{{
			PerformanceNAVPK:  42,
			AccountID:         "acct-1",
			TradeDate:         "2026-07-24",
			Version:           1,
			IsCurrent:         true,
			Status:            "provisional",
			FormulaVersion:    "performance_economic_nav.unit",
			OpenEconomicNAV:   1000000,
			CloseEconomicNAV:  1000100,
			ReturnDenominator: 1000000,
			CumulativeNAV:     1.0001,
			QualityFlags:      []string{"strategy_attribution_pending"},
			Source:            "unit",
		}},
		reconciliations: []ledger.NAVReconciliation{{
			ReconciliationID:    "nav-recon-acct-1-20260724-v1",
			PerformanceNAVPK:    42,
			AccountID:           "acct-1",
			TradeDate:           "2026-07-24",
			ObservedTradeDate:   "2026-07-27",
			Status:              "auto_completed",
			ObservedOpenAssets:  1000108,
			ProvisionalCloseNAV: 1000100,
			Residual:            8,
			AutoThreshold:       50,
			WarningThreshold:    500,
			Details:             map[string]any{"source": "unit"},
		}},
		now: now,
	}
	service, err := New(Options{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.ReviewNAVReconciliation(context.Background(), "acct-1", "20260724", NAVReconciliationReviewOptions{
		Action:   "confirm",
		Operator: "tester",
		Note:     "residual ok",
	})
	if err != nil {
		t.Fatalf("ReviewNAVReconciliation() error = %v", err)
	}

	if !result.Persisted || result.Status != "confirmed" {
		t.Fatalf("result persisted/status = %v/%q", result.Persisted, result.Status)
	}
	if len(store.reconciliationUpserts) != 1 || store.reconciliationUpserts[0].ReviewedBy != "tester" || store.reconciliationUpserts[0].Status != "confirmed" {
		t.Fatalf("reconciliation upserts = %#v", store.reconciliationUpserts)
	}
	if len(store.navStatusUpdates) != 1 || store.navStatusUpdates[0].Status != "finalized" {
		t.Fatalf("nav updates = %#v", store.navStatusUpdates)
	}
	if store.navStatusUpdates[0].FinalizedAt.IsZero() {
		t.Fatalf("finalized_at not set: %#v", store.navStatusUpdates[0])
	}
	if !containsString(store.navStatusUpdates[0].QualityFlags, "nav_reconciliation_confirmed") {
		t.Fatalf("quality flags = %#v", store.navStatusUpdates[0].QualityFlags)
	}
}

func TestReviewNAVReconciliationBlocksNAV(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 8, 0, 0, time.UTC)
	store := &fakePerformanceStore{
		navs: []ledger.PerformanceNAV{{
			PerformanceNAVPK:  42,
			AccountID:         "acct-1",
			TradeDate:         "2026-07-24",
			Version:           1,
			IsCurrent:         true,
			Status:            "provisional",
			FormulaVersion:    "performance_economic_nav.unit",
			OpenEconomicNAV:   1000000,
			CloseEconomicNAV:  1000100,
			ReturnDenominator: 1000000,
			CumulativeNAV:     1.0001,
			Source:            "unit",
		}},
		reconciliations: []ledger.NAVReconciliation{{
			ReconciliationID:    "nav-recon-acct-1-20260724-v1",
			PerformanceNAVPK:    42,
			AccountID:           "acct-1",
			TradeDate:           "2026-07-24",
			ObservedTradeDate:   "2026-07-27",
			Status:              "review_required",
			ProvisionalCloseNAV: 1000100,
			Residual:            800,
			WarningThreshold:    500,
		}},
		now: now,
	}
	service, err := New(Options{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.ReviewNAVReconciliation(context.Background(), "acct-1", "20260724", NAVReconciliationReviewOptions{
		Action:   "block",
		Operator: "risk",
		Note:     "large residual",
	})
	if err != nil {
		t.Fatalf("ReviewNAVReconciliation() error = %v", err)
	}

	if result.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", result.Status)
	}
	if len(store.navStatusUpdates) != 1 || store.navStatusUpdates[0].Status != "blocked" {
		t.Fatalf("nav updates = %#v", store.navStatusUpdates)
	}
	if !containsString(store.navStatusUpdates[0].QualityFlags, "nav_finalization_blocked") {
		t.Fatalf("quality flags = %#v", store.navStatusUpdates[0].QualityFlags)
	}
}

func TestCalculateTradeQualitySummarizesExecutionAndAnomalies(t *testing.T) {
	location := timeutil.Location()
	updatedAt := time.Date(2026, 7, 24, 14, 30, 0, 0, location)
	store := &fakePerformanceStore{
		orders: []trading.Order{
			{
				AccountID:      "acct-1",
				GatewayOrderID: "filled-ok",
				TradeDate:      "2026-07-24",
				Symbol:         "600000",
				Exchange:       trading.ExchangeSH,
				OrderQty:       100,
				CumFilledQty:   100,
				Status:         trading.OrderStatusFilled,
				GatewayStatus:  trading.GatewayStatusFilled,
				IsTerminal:     true,
				CreatedAt:      updatedAt.Add(-time.Minute),
				TerminalAt:     updatedAt,
				LastUpdatedAt:  updatedAt,
			},
			{
				AccountID:      "acct-1",
				GatewayOrderID: "partial-cancel",
				TradeDate:      "2026-07-24",
				Symbol:         "000001",
				Exchange:       trading.ExchangeSZ,
				OrderQty:       100,
				CumFilledQty:   40,
				Status:         trading.OrderStatusCancelled,
				GatewayStatus:  trading.GatewayStatusCancelled,
				IsTerminal:     true,
				CreatedAt:      updatedAt.Add(-time.Minute),
				TerminalAt:     updatedAt.Add(time.Minute),
				LastUpdatedAt:  updatedAt.Add(time.Minute),
			},
			{
				AccountID:      "acct-1",
				GatewayOrderID: "rejected",
				TradeDate:      "2026-07-24",
				Symbol:         "300750",
				Exchange:       trading.ExchangeSZ,
				OrderQty:       100,
				Status:         trading.OrderStatusRejected,
				GatewayStatus:  trading.GatewayStatusRejected,
				IsTerminal:     true,
				RejectMessage:  "price outside limit",
				CreatedAt:      updatedAt.Add(-time.Minute),
				TerminalAt:     updatedAt.Add(2 * time.Minute),
				LastUpdatedAt:  updatedAt.Add(2 * time.Minute),
			},
			{
				AccountID:      "acct-1",
				GatewayOrderID: "working",
				TradeDate:      "2026-07-24",
				Symbol:         "510300",
				Exchange:       trading.ExchangeSH,
				OrderQty:       100,
				Status:         trading.OrderStatusWorking,
				GatewayStatus:  trading.GatewayStatusWorking,
				LastUpdatedAt:  updatedAt.Add(3 * time.Minute),
			},
			{
				AccountID:         "acct-1",
				GatewayOrderID:    "fill-mismatch",
				TradeDate:         "2026-07-24",
				Symbol:            "159915",
				Exchange:          trading.ExchangeSZ,
				OrderQty:          100,
				CumFilledQty:      100,
				Status:            trading.OrderStatusFilled,
				GatewayStatus:     trading.GatewayStatusFilled,
				IsTerminal:        true,
				AdapterStatusCode: -8,
				AdapterStatusName: "dealt",
				CreatedAt:         updatedAt.Add(-time.Minute),
				TerminalAt:        updatedAt.Add(4 * time.Minute),
				LastUpdatedAt:     updatedAt.Add(4 * time.Minute),
			},
		},
		fills: []trading.Fill{
			{FillID: "filled-ok-1", AccountID: "acct-1", GatewayOrderID: "filled-ok", TradeDate: "2026-07-24", Symbol: "600000", Exchange: trading.ExchangeSH, Price: 10, Qty: 100, Fee: 1},
			{FillID: "relay-summary:filled-ok", AccountID: "acct-1", GatewayOrderID: "filled-ok", TradeDate: "2026-07-24", Symbol: "600000", Exchange: trading.ExchangeSH, Price: 10, Qty: 100},
			{FillID: "partial-cancel-1", AccountID: "acct-1", GatewayOrderID: "partial-cancel", TradeDate: "2026-07-24", Symbol: "000001", Exchange: trading.ExchangeSZ, Price: 12, Qty: 40, Fee: 0.5},
			{FillID: "fill-mismatch-1", AccountID: "acct-1", GatewayOrderID: "fill-mismatch", TradeDate: "2026-07-24", Symbol: "159900", Exchange: trading.ExchangeSZ, Price: 4.8, Qty: 80, Fee: 0.8},
			{FillID: "orphan-1", AccountID: "acct-1", GatewayOrderID: "missing-order", TradeDate: "2026-07-24", Symbol: "601318", Exchange: trading.ExchangeSH, Price: 60, Qty: 30, Fee: 0.6},
		},
	}
	service, err := New(Options{Store: store, Now: func() time.Time { return updatedAt }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateTradeQuality(context.Background(), "acct-1", "20260724", "2026-07-24")
	if err != nil {
		t.Fatalf("CalculateTradeQuality() error = %v", err)
	}
	if result.DateFrom != "2026-07-24" || result.DateTo != "2026-07-24" {
		t.Fatalf("range = %s..%s", result.DateFrom, result.DateTo)
	}
	summary := result.Summary
	if summary.Orders != 5 || summary.OrdersWithFills != 3 || summary.FullyFilledOrders != 2 || summary.PartiallyFilledOrders != 1 {
		t.Fatalf("execution summary = %#v", summary)
	}
	if summary.CancelledOrders != 1 || summary.RejectedOrders != 1 || summary.NonTerminalOrders != 1 {
		t.Fatalf("status summary = %#v", summary)
	}
	if summary.AbnormalOrders != 3 || summary.OrphanFillGroups != 1 || summary.AnomalyItems != 4 {
		t.Fatalf("anomaly summary = %#v, anomalies=%#v", summary, result.Anomalies)
	}
	if summary.Fills != 4 || summary.FillQuantity != 250 {
		t.Fatalf("fill summary = %#v", summary)
	}
	if summary.ExecutedOrderRate != 0.6 || summary.FullFillRate != 0.4 || summary.QuantityFillRate != 0.44 {
		t.Fatalf("rates = %#v", summary)
	}
	if summary.Turnover != 3664 || summary.Fee != 2.9 {
		t.Fatalf("turnover/fee = %#v", summary)
	}
	if len(store.orderQueries) != 1 || store.orderQueries[0].DateFrom != "2026-07-24" || store.orderQueries[0].DateTo != "2026-07-24" {
		t.Fatalf("order queries = %#v", store.orderQueries)
	}
	if !tradeQualityHasAnomalyFlag(result.Anomalies, "fill-mismatch", "order_fill_quantity_mismatch") {
		t.Fatalf("missing fill mismatch anomaly: %#v", result.Anomalies)
	}
	if !tradeQualityHasAnomalyFlag(result.Anomalies, "fill-mismatch", "fill_order_security_mismatch") {
		t.Fatalf("missing fill security mismatch anomaly: %#v", result.Anomalies)
	}
	if !tradeQualityHasAnomalyFlag(result.Anomalies, "missing-order", "orphan_fill") {
		t.Fatalf("missing orphan fill anomaly: %#v", result.Anomalies)
	}
	if tradeQualityHasAnomalyFlag(result.Anomalies, "partial-cancel", "broker_error_message") {
		t.Fatalf("normal broker cancelled status treated as error: %#v", result.Anomalies)
	}
}

func TestCalculateTradeQualityRejectsReversedRange(t *testing.T) {
	service, err := New(Options{Store: &fakePerformanceStore{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = service.CalculateTradeQuality(context.Background(), "acct-1", "20260725", "20260724")
	if err == nil || !errors.Is(err, ledger.ErrInvalidLedgerInput) {
		t.Fatalf("CalculateTradeQuality() error = %v, want invalid ledger input", err)
	}
}

func TestTradeQualityDoesNotMatchReusedOrderIDAcrossDates(t *testing.T) {
	oldKey := tradeQualityKey("2026-07-23", "reused-order")
	groups := map[string]qualityFillGroup{
		oldKey: {quantity: 500},
	}
	keysByOrder := map[string][]string{
		"reused-order": {oldKey},
	}

	group, matchedKey := matchTradeQualityFillGroup(
		groups,
		keysByOrder,
		tradeQualityKey("2026-07-24", "reused-order"),
		"reused-order",
	)
	if matchedKey != "" || group.quantity != 0 {
		t.Fatalf("cross-date fill matched key=%q group=%#v", matchedKey, group)
	}
}

func TestTradeQualityTerminalTimeAllowsSmallClockSkew(t *testing.T) {
	createdAt := time.Date(2026, 7, 24, 10, 0, 0, 0, timeutil.Location())
	order := trading.Order{
		GatewayOrderID: "clock-skew",
		TradeDate:      "2026-07-24",
		OrderQty:       100,
		Status:         trading.OrderStatusCancelled,
		GatewayStatus:  trading.GatewayStatusCancelled,
		IsTerminal:     true,
		CreatedAt:      createdAt,
		TerminalAt:     createdAt.Add(-3 * time.Second),
	}
	anomaly := tradeQualityOrderAnomaly(order, order.TradeDate, qualityFillGroup{})
	if containsString(anomaly.Flags, "terminal_before_created") {
		t.Fatalf("small clock skew flagged: %#v", anomaly.Flags)
	}

	order.TerminalAt = createdAt.Add(-6 * time.Second)
	anomaly = tradeQualityOrderAnomaly(order, order.TradeDate, qualityFillGroup{})
	if !containsString(anomaly.Flags, "terminal_before_created") {
		t.Fatalf("large clock skew not flagged: %#v", anomaly.Flags)
	}
}

func tradeQualityHasAnomalyFlag(items []TradeQualityAnomaly, gatewayOrderID, flag string) bool {
	for _, item := range items {
		if item.GatewayOrderID == gatewayOrderID && containsString(item.Flags, flag) {
			return true
		}
	}
	return false
}

type fakePerformanceStore struct {
	orders                []trading.Order
	orderQueries          []trading.OrderQuery
	fills                 []trading.Fill
	positions             map[string][]trading.Position
	positionsByDate       map[string][]trading.Position
	feeRules              []ledger.FeeRule
	orderFees             []ledger.OrderFeeRecord
	fillQueries           []trading.FillQuery
	daily                 ledger.DailyPerformance
	dailyErr              error
	observation           ledger.AssetPositionObservation
	observationErr        error
	repoRule              ledger.FeeRule
	repoRuleErr           error
	cashByClass           map[string][]ledger.CashLedgerEntry
	cashQueries           []ledger.CashLedgerQuery
	baselines             []ledger.NavBaseline
	repoAccruals          []ledger.ReverseRepoAccrual
	navs                  []ledger.PerformanceNAV
	reconciliations       []ledger.NAVReconciliation
	upserts               []ledger.ReverseRepoAccrual
	navUpserts            []ledger.PerformanceNAV
	navStatusUpdates      []ledger.PerformanceNAV
	reconciliationUpserts []ledger.NAVReconciliation
	inception             ledger.PerformanceInception
	inceptionErr          error
	costStates            []ledger.PositionCostState
	costUpserts           []ledger.PositionCostState
	now                   time.Time
}

type fakeContributionMarket struct {
	metadata       market.MeridianResponse
	adjustFactors  market.MeridianResponse
	bars           market.MeridianResponse
	snapshots      market.MeridianResponse
	cashComponents market.MeridianResponse
	queries        []url.Values
}

func (client *fakeContributionMarket) MetadataAdjustFactors(_ context.Context, values url.Values) (market.MeridianResponse, error) {
	client.queries = append(client.queries, values)
	return client.adjustFactors, nil
}

func (client *fakeContributionMarket) MetadataInstruments(_ context.Context, values url.Values) (market.MeridianResponse, error) {
	client.queries = append(client.queries, values)
	return client.metadata, nil
}

func (client *fakeContributionMarket) MarketBars(_ context.Context, values url.Values) (market.MeridianResponse, error) {
	client.queries = append(client.queries, values)
	return client.bars, nil
}

func (client *fakeContributionMarket) MarketSnapshots(_ context.Context, values url.Values) (market.MeridianResponse, error) {
	client.queries = append(client.queries, values)
	return client.snapshots, nil
}

func (client *fakeContributionMarket) MarketETFCashComponents(_ context.Context, values url.Values) (market.MeridianResponse, error) {
	client.queries = append(client.queries, values)
	return client.cashComponents, nil
}

func (store *fakePerformanceStore) ListOrders(_ context.Context, query trading.OrderQuery) ([]trading.Order, error) {
	store.orderQueries = append(store.orderQueries, query)
	return store.orders, nil
}

func (store *fakePerformanceStore) ListFills(_ context.Context, query trading.FillQuery) ([]trading.Fill, error) {
	store.fillQueries = append(store.fillQueries, query)
	return store.fills, nil
}

func (store *fakePerformanceStore) ListOrderFeeRecords(_ context.Context, _ ledger.OrderFeeRecordQuery) ([]ledger.OrderFeeRecord, error) {
	return store.orderFees, nil
}

func (store *fakePerformanceStore) ListPositionSnapshots(_ context.Context, query trading.PositionQuery) ([]trading.Position, error) {
	if store.positionsByDate != nil {
		return store.positionsByDate[query.TradeDate+"|"+query.SnapshotType], nil
	}
	if store.positions == nil {
		return nil, nil
	}
	return store.positions[query.SnapshotType], nil
}

func (store *fakePerformanceStore) GetDailyPerformance(_ context.Context, _, _ string) (ledger.DailyPerformance, error) {
	if store.dailyErr != nil {
		return ledger.DailyPerformance{}, store.dailyErr
	}
	return store.daily, nil
}

func (store *fakePerformanceStore) GetAssetPositionObservation(_ context.Context, _, _, _ string) (ledger.AssetPositionObservation, error) {
	if store.observationErr != nil {
		return ledger.AssetPositionObservation{}, store.observationErr
	}
	return store.observation, nil
}

func (store *fakePerformanceStore) CreateFeeRule(_ context.Context, rule ledger.FeeRule) (ledger.FeeRule, error) {
	return rule, nil
}

func (store *fakePerformanceStore) ListFeeRules(_ context.Context, _ ledger.FeeRuleQuery) ([]ledger.FeeRule, error) {
	return store.feeRules, nil
}

func (store *fakePerformanceStore) EffectiveRepoFeeRule(_ context.Context, _, _ string) (ledger.FeeRule, error) {
	if store.repoRuleErr != nil {
		return ledger.FeeRule{}, store.repoRuleErr
	}
	return store.repoRule, nil
}

func (store *fakePerformanceStore) CreateCashLedgerEntry(_ context.Context, entry ledger.CashLedgerEntry) (ledger.CashLedgerEntry, error) {
	return entry, nil
}

func (store *fakePerformanceStore) ListCashLedgerEntries(_ context.Context, query ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error) {
	store.cashQueries = append(store.cashQueries, query)
	if store.cashByClass == nil {
		return nil, nil
	}
	return store.cashByClass[query.FlowClass], nil
}

func (store *fakePerformanceStore) ConfirmCashLedgerEntry(_ context.Context, _, _, _ string, _ time.Time) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, nil
}

func (store *fakePerformanceStore) VoidCashLedgerEntry(_ context.Context, _, _, _ string, _ time.Time) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, nil
}

func (store *fakePerformanceStore) CreateNavBaseline(_ context.Context, baseline ledger.NavBaseline) (ledger.NavBaseline, error) {
	return baseline, nil
}

func (store *fakePerformanceStore) ListNavBaselines(_ context.Context, _ string) ([]ledger.NavBaseline, error) {
	return store.baselines, nil
}

func (store *fakePerformanceStore) UpsertReverseRepoAccrual(_ context.Context, accrual ledger.ReverseRepoAccrual) error {
	store.upserts = append(store.upserts, accrual)
	return nil
}

func (store *fakePerformanceStore) ListReverseRepoAccruals(_ context.Context, _, _ string) ([]ledger.ReverseRepoAccrual, error) {
	return store.repoAccruals, nil
}

func (store *fakePerformanceStore) ListPerformanceNAVs(_ context.Context, _, _, _ string) ([]ledger.PerformanceNAV, error) {
	return store.navs, nil
}

func (store *fakePerformanceStore) ListNAVReconciliations(_ context.Context, _, _, _ string) ([]ledger.NAVReconciliation, error) {
	return store.reconciliations, nil
}

func (store *fakePerformanceStore) UpsertPerformanceNAV(_ context.Context, nav ledger.PerformanceNAV) (ledger.PerformanceNAV, error) {
	if nav.PerformanceNAVPK == 0 {
		nav.PerformanceNAVPK = int64(len(store.navUpserts) + 1)
	}
	if nav.Version == 0 {
		nav.Version = len(store.navUpserts) + 1
	}
	store.navUpserts = append(store.navUpserts, nav)
	return nav, nil
}

func (store *fakePerformanceStore) UpdatePerformanceNAVStatus(_ context.Context, nav ledger.PerformanceNAV) (ledger.PerformanceNAV, error) {
	store.navStatusUpdates = append(store.navStatusUpdates, nav)
	return nav, nil
}

func (store *fakePerformanceStore) UpsertNAVReconciliation(_ context.Context, item ledger.NAVReconciliation) (ledger.NAVReconciliation, error) {
	store.reconciliationUpserts = append(store.reconciliationUpserts, item)
	return item, nil
}

func (store *fakePerformanceStore) GetPerformanceInception(_ context.Context, _ string) (ledger.PerformanceInception, error) {
	if store.inceptionErr != nil {
		return ledger.PerformanceInception{}, store.inceptionErr
	}
	if store.inception.AccountID == "" {
		return ledger.PerformanceInception{}, sql.ErrNoRows
	}
	return store.inception, nil
}

func (store *fakePerformanceStore) UpsertPerformanceInception(_ context.Context, item ledger.PerformanceInception) (ledger.PerformanceInception, error) {
	store.inception = item
	return item, nil
}

func (store *fakePerformanceStore) ListPositionCostStates(_ context.Context, _ ledger.PositionCostStateQuery) ([]ledger.PositionCostState, error) {
	return store.costStates, nil
}

func (store *fakePerformanceStore) UpsertPositionCostState(_ context.Context, item ledger.PositionCostState) (ledger.PositionCostState, error) {
	store.costUpserts = append(store.costUpserts, item)
	return item, nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestTrustedBrokerPositionCostPrefersCompleteTotalCost(t *testing.T) {
	position := trading.Position{
		Quantity:     100,
		AvgCost:      9.54,
		TotalCost:    960,
		CostComplete: true,
	}
	assertClose(t, trustedBrokerPositionCost(position), 960)

	position.CostComplete = false
	assertClose(t, trustedBrokerPositionCost(position), 954)

	position.AvgCostSource = "unavailable"
	assertClose(t, trustedBrokerPositionCost(position), 0)
}

type weekdayCalendar struct{}

func (weekdayCalendar) TradingDayStatus(_ context.Context, date string) (market.TradingDayStatus, error) {
	parsed, err := time.Parse("20060102", date)
	if err != nil {
		return market.TradingDayStatus{}, err
	}
	weekday := parsed.Weekday()
	isTrading := weekday != time.Saturday && weekday != time.Sunday
	previousOrCurrent := parsed
	for previousOrCurrent.Weekday() == time.Saturday || previousOrCurrent.Weekday() == time.Sunday {
		previousOrCurrent = previousOrCurrent.AddDate(0, 0, -1)
	}
	return market.TradingDayStatus{
		Date:                         parsed.Format("20060102"),
		IsTradingDay:                 isTrading,
		IsTradingDayKnown:            true,
		PreviousOrCurrentTradingDate: previousOrCurrent.Format("20060102"),
	}, nil
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("got %.6f, want %.6f", got, want)
	}
}
