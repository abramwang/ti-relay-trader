package performance

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func TestReportedBrokerAssetAuthorityDoesNotAssertSettlementClosure(t *testing.T) {
	observation := ledger.AssetPositionObservation{
		NetAsset: 120500,
		Source:   "broker_historical_funds_statement_one_time_audit_20261110",
		RawPayload: map[string]any{
			"broker_asset_base_authoritative":     true,
			"economic_nav_base_confirmed":         false,
			"asset_scope":                         "broker_reported_total_asset",
			"settlement_overlay_status":           "pending_review",
			"broker_unclassified_asset_component": 500.0,
		},
	}
	basis, flags, err := confirmedBrokerAssetBasis(observation, 100, 0, false)
	if err != nil || basis.Applied || len(flags) != 0 {
		t.Fatalf("reported asset authority must not infer zero or duplicate ETF rights: %+v %v %v", basis, flags, err)
	}
}

func TestEconomicNAVSameDayCashPreviewCannotPublish(t *testing.T) {
	obs, _ := cashInclusionObservation(t)
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, timeutil.Location())
	store := &fakePerformanceStore{
		daily:        ledger.DailyPerformance{AccountID: "acct-1", TradeDate: "2026-10-12", OpenNetAsset: 1000, CashTotal: 1010, NetAsset: 1010, OpenSnapshotSource: "open"},
		observations: map[string]ledger.AssetPositionObservation{"open": {CashTotal: 1000, NetAsset: 1000}, "close": {CashTotal: 1010, NetAsset: 1010}},
		orders: []trading.Order{
			{GatewayOrderID: "buy", AccountID: "acct-1", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 100, T0OrderGroupID: "group"},
			{GatewayOrderID: "redeem", AccountID: "acct-1", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, OrderQty: 100, T0OrderGroupID: "group"},
		},
		fills: []trading.Fill{
			{FillID: "buy-fill", AccountID: "acct-1", GatewayOrderID: "buy", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, Price: 1, Qty: 100, MatchedAt: at, T0OrderGroupID: "group"},
			{FillID: "redeem-fill", AccountID: "acct-1", GatewayOrderID: "redeem", Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, Qty: 100, MatchedAt: at.Add(time.Minute), T0OrderGroupID: "group"},
		},
	}
	store.orders[1].BasketID = "etfarb#510310"
	store.orders = append(store.orders, trading.Order{GatewayOrderID: "component-sale", AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideSell, OrderQty: 1, BasketID: "etfarb#510310#s"})
	store.fills = append(store.fills, trading.Fill{FillID: "component-sale-fill", AccountID: "acct-1", GatewayOrderID: "component-sale", Symbol: "600000", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideSell, Qty: 1, Price: 1, BasketID: "etfarb#510310#s", MatchedAt: at.Add(2 * time.Minute)})
	store.transfers = []trading.ComponentTransfer{{FillID: "component-transfer", AccountID: "acct-1", GatewayOrderID: "redeem", Symbol: "600000", Exchange: trading.ExchangeSH, ComponentSymbol: "600000", ComponentExchange: trading.ExchangeSH, ComponentQty: 1, BasketID: "etfarb#510310", MatchedAt: at.Add(time.Minute)}}
	client := &fakeContributionMarket{
		metadata:       market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "instrument_type": "etf"}}}},
		bars:           market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{}}},
		snapshots:      market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "iopv": 1.2, "timestamp": "2026-10-12T10:01:00+08:00"}}}},
		cashComponents: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "510310.SH", "unit_subscribe_redeem": "100"}}}},
	}
	s, err := New(Options{Store: store, Market: client, ETFT0FrictionRate: 0.0015})
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := s.CalculateContributions(context.Background(), "acct-1", "20261012")
	if err != nil {
		t.Fatal(err)
	}
	carry := contribution.Summary.ETFSettlementEstimate
	if carry < 60 {
		t.Fatalf("test carry missing: %+v", contribution)
	}
	obs.RawPayload["close_outstanding_etf_settlement_asset"] = 20 + carry - 60
	store.observations["reconcile"] = obs
	result, err := s.CalculateEconomicNAV(context.Background(), "acct-1", "20261012", EconomicNAVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "blocked" || !result.AssetBasis.Applied || !containsStringValue(result.QualityFlags, "same_day_etf_cash_inclusion_preview_only") {
		t.Fatalf("%+v", result)
	}
	assertClose(t, result.Valuation.ETFSettlementEstimate, carry-60)
	assertClose(t, result.NAV.CloseEconomicNAV, 1010+20+carry-60)
	assertClose(t, result.NAV.PnLComponents["market_valuation"].(map[string]any)["etf_settlement_estimate"].(float64), carry-60)
	_, err = s.CalculateEconomicNAV(context.Background(), "acct-1", "20261012", EconomicNAVOptions{Persist: true})
	if err == nil || !strings.Contains(err.Error(), "same-day ETF cash inclusion remains preview-only") {
		t.Fatalf("publish error: %v", err)
	}
}

func cashInclusionObservation(t *testing.T) (ledger.AssetPositionObservation, map[string]any) {
	t.Helper()
	hash := strings.Repeat("a", 64)
	proof := map[string]any{
		"schema": "broker_same_day_etf_receipts_audit.v1", "account_id": "acct-1", "trade_date": "2026-10-12",
		"scope":               "broker_end_of_day_cash_not_OC_intraday_atomic_snapshot",
		"source_sha256":       map[string]any{"funds": hash, "cash_flows": hash, "settlements": hash, "executions": hash},
		"cash_chain_verified": true, "cash_multiset_verified": true, "included_cash_total": "60.00",
		"receipts": []any{map[string]any{"account_id": "acct-1", "trade_date": "2026-10-12", "source_trade_date": "2026-10-12",
			"broker_flow_id": "flow-1", "broker_order_id": "order-1", "security_id": "510310.SH", "amount": "60.00"}},
	}
	return ledger.AssetPositionObservation{
		AccountID: "acct-1", TradeDate: "2026-10-12", NetAsset: 1010,
		Source: "broker_historical_funds_statement_one_time_audit",
		RawPayload: map[string]any{
			"economic_nav_base_confirmed": true, "recurring_import": false,
			"asset_scope": "broker_reported_total_asset_excluding_fund_occupancy", "statement_sha256": hash,
			"reported_open_total_asset": 1000.0, "reported_close_total_asset": 1010.0,
			"reported_daily_pnl": 10.0, "reported_deposit": 0.0, "reported_withdrawal": 0.0,
			"open_outstanding_etf_settlement_asset": 20.0, "close_outstanding_etf_settlement_asset": 60.0,
			"same_day_etf_cash_inclusion": proof,
		},
	}, proof
}

func TestConfirmedBrokerAssetBasisRemovesIncludedSameDayCashOnce(t *testing.T) {
	observation, _ := cashInclusionObservation(t)
	before, _ := json.Marshal(observation)
	for i := 0; i < 2; i++ {
		basis, flags, err := confirmedBrokerAssetBasis(observation, 100, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		assertClose(t, basis.SameDayETFSettlementCashIncluded, 60)
		assertClose(t, basis.OpenEconomicNAV, 1020)
		assertClose(t, basis.CloseEconomicNAV, 1070)
		if !containsStringValue(flags, "same_day_etf_cash_included_in_broker_asset_basis") {
			t.Fatal(flags)
		}
	}
	after, _ := json.Marshal(observation)
	if string(before) != string(after) {
		t.Fatal("source evidence changed")
	}
	// A previous-day release stays separate from cash included on the redemption day.
	observation.RawPayload["close_outstanding_etf_settlement_asset"] = 55.0
	basis, _, err := confirmedBrokerAssetBasis(observation, 100, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	assertClose(t, basis.CloseEconomicNAV, 1065)
}

func TestSameDayETFCashRejectsUnprovedOrDuplicateEvidence(t *testing.T) {
	cases := map[string]func(map[string]any){
		"no chain":      func(p map[string]any) { p["cash_chain_verified"] = false },
		"no multiset":   func(p map[string]any) { p["cash_multiset_verified"] = false },
		"wrong scope":   func(p map[string]any) { p["scope"] = "OC_intraday" },
		"wrong schema":  func(p map[string]any) { p["schema"] = "other" },
		"wrong account": func(p map[string]any) { p["account_id"] = "acct-2" },
		"wrong date":    func(p map[string]any) { p["trade_date"] = "2026-10-13" },
		"missing hash":  func(p map[string]any) { delete(p["source_sha256"].(map[string]any), "executions") },
		"hash mismatch": func(p map[string]any) { p["source_sha256"].(map[string]any)["funds"] = strings.Repeat("b", 64) },
		"no receipts":   func(p map[string]any) { p["receipts"] = []any{} },
		"wrong sum":     func(p map[string]any) { p["included_cash_total"] = "59.99" },
		"duplicate": func(p map[string]any) {
			rows := p["receipts"].([]any)
			p["receipts"] = append(rows, rows[0])
			p["included_cash_total"] = "120.00"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			obs, proof := cashInclusionObservation(t)
			mutate(proof)
			if _, err := confirmedSameDayETFCash(obs, 200); err == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	for field, value := range map[string]string{"account_id": "other", "trade_date": "2026-10-13", "source_trade_date": "2026-10-09", "broker_flow_id": " ", "broker_order_id": "", "security_id": "", "amount": "NaN"} {
		t.Run(field, func(t *testing.T) {
			obs, proof := cashInclusionObservation(t)
			proof["receipts"].([]any)[0].(map[string]any)[field] = value
			if _, err := confirmedSameDayETFCash(obs, 100); err == nil {
				t.Fatal("accepted invalid receipt")
			}
		})
	}
	for _, amount := range []string{"-1.00", "0.00", "6e1", "60.001", "Inf"} {
		obs, proof := cashInclusionObservation(t)
		proof["receipts"].([]any)[0].(map[string]any)["amount"] = amount
		if _, err := confirmedSameDayETFCash(obs, 100); err == nil {
			t.Fatalf("accepted %s", amount)
		}
	}
	for _, carry := range []float64{59.99, -1, math.NaN(), math.Inf(1)} {
		obs, _ := cashInclusionObservation(t)
		if _, err := confirmedSameDayETFCash(obs, carry); err == nil {
			t.Fatal("accepted invalid carry")
		}
	}
}

func TestSameDayETFCashCannotApplyToUnconfirmedOrIntradayBasis(t *testing.T) {
	obs, _ := cashInclusionObservation(t)
	obs.RawPayload["economic_nav_base_confirmed"] = false
	basis, _, err := confirmedBrokerAssetBasis(obs, 100, 0, false)
	if err != nil || basis.Applied || basis.SameDayETFSettlementCashIncluded != 0 {
		t.Fatal(basis, err)
	}
	obs.RawPayload["economic_nav_base_confirmed"] = true
	obs.Source = "OC"
	if _, _, err = confirmedBrokerAssetBasis(obs, 100, 0, false); err == nil {
		t.Fatal("accepted intraday basis")
	}
}
