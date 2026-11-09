package performance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func partialDayExpiryFixture() (trading.Order, qualityFillGroup) {
	order := trading.Order{
		AccountID: "acct-1", GatewayOrderID: "partial", TradeDate: "2026-11-02",
		Symbol: "204001", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideSell,
		BusinessType: trading.BusinessTypeStock, Status: trading.OrderStatusPartiallyFilled,
		GatewayStatus: trading.GatewayStatusWorking, AdapterStatusCode: -8, AdapterStatusName: "dealt",
		OrderQty: 48050, CumFilledQty: 5900, LeavesQty: 42150,
	}
	fill := trading.Fill{AccountID: "acct-1", GatewayOrderID: "partial", FillID: "fill-1", TradeDate: order.TradeDate,
		Symbol: order.Symbol, Exchange: order.Exchange, TradeSide: order.TradeSide, BusinessType: order.BusinessType,
		Qty: 5900, Price: 1.195, MatchedAt: time.Date(2026, 11, 2, 14, 3, 37, 0, timeutil.Location())}
	return order, qualityFillGroup{quantity: 5900, fills: []trading.Fill{fill}}
}

func TestHistoricalPartialDayExpiryRequiresOptInAndPreviousShanghaiDate(t *testing.T) {
	for _, tc := range []struct {
		name, now        string
		enabled, expired bool
	}{
		{"disabled", "2026-11-09T15:00:00+08:00", false, false},
		{"intraday", "2026-11-02T14:30:00+08:00", true, false},
		{"same day after close", "2026-11-02T23:59:59+08:00", true, false},
		{"future order", "2026-11-01T15:00:00+08:00", true, false},
		{"next Shanghai date", "2026-11-02T16:00:00Z", true, true},
		{"historical", "2026-11-09T15:00:00+08:00", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			order, group := partialDayExpiryFixture()
			store := &fakePerformanceStore{orders: []trading.Order{order}, fills: group.fills}
			before, _ := json.Marshal(store.orders)
			at, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			service, err := New(Options{Store: store, HistoricalPartialDayOrderExpiry: tc.enabled, Now: func() time.Time { return at }})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.CalculateTradeQuality(context.Background(), "acct-1", "20261102", "20261102")
			if err != nil {
				t.Fatal(err)
			}
			if tc.expired {
				if result.Summary.NonTerminalOrders != 0 || result.Summary.AnomalyItems != 0 || result.Summary.DayEndExpiredPartialOrders != 1 || result.Summary.DayEndExpiredOrders != 1 {
					t.Fatalf("%+v", result)
				}
				if !containsStringValue(result.QualityFlags, "historical_partial_day_order_expired") {
					t.Fatal(result.QualityFlags)
				}
			} else if result.Summary.NonTerminalOrders != 1 || result.Summary.DayEndExpiredPartialOrders != 0 || result.Summary.AnomalyItems != 1 {
				t.Fatalf("%+v", result)
			}
			if result.Summary.CancelledOrders != 0 || result.Summary.PartiallyFilledOrders != 1 || result.Summary.ExecutedQuantity != 5900 || result.Summary.FillQuantity != 5900 || result.StatusBreakdown["partially_filled"] != 1 {
				t.Fatalf("changed execution/cancel facts: %+v", result)
			}
			after, _ := json.Marshal(store.orders)
			if string(before) != string(after) {
				t.Fatal("mutated original order")
			}
		})
	}
}

func TestHistoricalPartialDayExpiryDoesNotHideOtherQualityErrors(t *testing.T) {
	cases := map[string]func(*trading.Order, *qualityFillGroup){
		"missing fills":        func(o *trading.Order, f *qualityFillGroup) { f.quantity = 0; f.fills = nil },
		"quantity mismatch":    func(o *trading.Order, f *qualityFillGroup) { f.quantity-- },
		"wrong leaves":         func(o *trading.Order, f *qualityFillGroup) { o.LeavesQty-- },
		"broker error":         func(o *trading.Order, f *qualityFillGroup) { o.RejectMessage = "broker error" },
		"terminal flag":        func(o *trading.Order, f *qualityFillGroup) { o.IsTerminal = true },
		"terminal timestamp":   func(o *trading.Order, f *qualityFillGroup) { o.TerminalAt = time.Now() },
		"cancel evidence":      func(o *trading.Order, f *qualityFillGroup) { o.CancelledQty = 1 },
		"invalid quantity":     func(o *trading.Order, f *qualityFillGroup) { o.InvalidQty = 1 },
		"zero execution":       func(o *trading.Order, f *qualityFillGroup) { o.CumFilledQty = 0 },
		"over execution":       func(o *trading.Order, f *qualityFillGroup) { o.CumFilledQty = o.OrderQty + 1 },
		"unknown native state": func(o *trading.Order, f *qualityFillGroup) { o.AdapterStatusName = "pending_cancel" },
		"wrong native code":    func(o *trading.Order, f *qualityFillGroup) { o.AdapterStatusCode = 0 },
		"created":              func(o *trading.Order, f *qualityFillGroup) { o.Status = trading.OrderStatusCreated },
		"gateway rejected":     func(o *trading.Order, f *qualityFillGroup) { o.GatewayStatus = trading.GatewayStatusRejected },
		"ETF redemption": func(o *trading.Order, f *qualityFillGroup) {
			o.BusinessType = trading.BusinessTypeETF
			o.TradeSide = trading.TradeSideRedemption
		},
		"futures":       func(o *trading.Order, f *qualityFillGroup) { o.Exchange = trading.Exchange("SHFE") },
		"fill security": func(o *trading.Order, f *qualityFillGroup) { f.fills[0].Symbol = "600000" },
		"fill side":     func(o *trading.Order, f *qualityFillGroup) { f.fills[0].TradeSide = trading.TradeSideBuy },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			order, fills := partialDayExpiryFixture()
			mutate(&order, &fills)
			if historicalPartialDayOrderExpired(order, order.TradeDate, fills, time.Date(2026, 11, 9, 15, 0, 0, 0, timeutil.Location())) {
				t.Fatal("accepted invalid expiry")
			}
		})
	}
	order, fills := partialDayExpiryFixture()
	if historicalPartialDayOrderExpired(order, "bad-date", fills, time.Now()) {
		t.Fatal("accepted invalid date")
	}
}
