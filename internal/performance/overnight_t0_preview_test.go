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

func overnightTestSource() ledger.PositionCostState {
	return ledger.PositionCostState{AccountID: "account", TradeDate: "2026-11-03", Symbol: "159915", Exchange: "SZ", CostBucket: t0ResidualPrefix + "confirmed", Status: "calculated",
		BuyQuantity: 1000000, CloseQuantity: 1000000, BuyAmount: 2452000, BuyFee: 122.6, CloseTotalCost: 2452122.6, ClosePrice: 2.403, CloseMarketValue: 2403000}
}

func TestOvernightDailyBridgeDoesNotDuplicateFeesOrLifetimeReturn(t *testing.T) {
	for _, iopv := range []float64{2.3, 2.5, 2.6} {
		source := overnightTestSource()
		before := source
		r, err := overnightT0DailyBridge(source, iopv, .0015)
		if err != nil {
			t.Fatal(err)
		}
		assertClose(t, r.SourceDayPnL, -49122.6)
		assertClose(t, r.PreviouslyExpensedFeeCredit, 122.6)
		assertClose(t, r.SourceDayPnL+r.RedemptionDayPnL, r.CombinedEstimatedPnL)
		assertClose(t, r.CombinedEstimatedPnL, iopv*1000000-2452000-iopv*1000000*.0015)
		if r.ConservationResidual != 0 || r.Publishable || !reflect.DeepEqual(source, before) {
			t.Fatalf("mutable or publishable result: %+v", r)
		}
	}
	for _, change := range []func(*ledger.PositionCostState){
		func(s *ledger.PositionCostState) { s.CloseTotalCost++ }, func(s *ledger.PositionCostState) { s.CloseMarketValue++ },
		func(s *ledger.PositionCostState) { s.BuyFee = math.NaN() }, func(s *ledger.PositionCostState) { s.ClosePrice = math.Inf(1) },
		func(s *ledger.PositionCostState) { s.CloseQuantity = 0 },
	} {
		s := overnightTestSource()
		change(&s)
		if _, err := overnightT0DailyBridge(s, 2.5, .0015); err == nil {
			t.Fatal("invalid basis accepted")
		}
	}
}

func TestOvernightPreviewGuardsAndMarketEvidence(t *testing.T) {
	for _, name := range []string{"ok", "disabled", "source_gap", "missing_calendar", "changed_preclose", "missing_iopv", "future_iopv", "partial_redemption", "wrong_account", "settled"} {
		t.Run(name, func(t *testing.T) {
			source := overnightTestSource()
			at, _ := time.Parse(time.RFC3339, "2026-11-04T13:35:02+08:00")
			order := trading.Order{AccountID: "account", TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: "red", TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, CumFilledQty: 1000000, IsTerminal: true}
			tr := trading.ComponentTransfer{AccountID: "account", TradeDate: order.TradeDate, GatewayOrderID: "red", Symbol: "159915", Exchange: trading.ExchangeSZ, ComponentSymbol: "159915", ComponentExchange: trading.ExchangeSZ, TradeSide: order.TradeSide, BusinessType: order.BusinessType, Qty: 1000000, ComponentQty: 1000000, MatchedAt: at, FillID: "parent"}
			row := map[string]any{"security_id": "159915.SZ", "trade_date": 20261104, "pre_close": 2.403, "close": 2.561}
			tick := map[string]any{"security_id": "159915.SZ", "iopv": 2.55, "timestamp": "2026-11-04T13:35:00+08:00"}
			m := &fakeContributionMarket{metadata: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{map[string]any{"security_id": "159915.SZ", "instrument_type": "etf"}}}},
				bars: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{row}}}, snapshots: market.MeridianResponse{StatusCode: 200, Payload: map[string]any{"data": []any{tick}}}}
			store := &fakePerformanceStore{}
			s := &Service{store: store, calendar: weekdayCalendar{}, market: m, componentResidualPreview: true, etfT0FrictionRate: .0015, now: func() time.Time { return at.AddDate(0, 0, 7) }}
			switch name {
			case "disabled":
				s.componentResidualPreview = false
			case "source_gap":
				source.TradeDate = "2026-11-02"
			case "missing_calendar":
				s.calendar = nil
			case "changed_preclose":
				row["pre_close"] = 2.4
			case "missing_iopv":
				delete(tick, "iopv")
			case "future_iopv":
				tick["timestamp"] = "2026-11-04T13:35:03+08:00"
			case "partial_redemption":
				tr.Qty--
			case "wrong_account":
				order.AccountID = "other"
			case "settled":
				store.etfSettlements = []ledger.ETFSettlementFinalization{{AccountID: "account"}}
			}
			r, err := s.PreviewOvernightT0(context.Background(), source, order, tr)
			if name == "ok" {
				if err != nil {
					t.Fatal(err)
				}
				if r.Publishable || r.ConservationResidual != 0 || r.IOPV != 2.55 {
					t.Fatalf("bad preview %+v", r)
				}
			} else if err == nil {
				t.Fatal("unsafe review accepted")
			}
		})
	}
}
