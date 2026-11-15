package performance

import (
	"math"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func coreReplayFixture() ([]trading.Fill, []ledger.OrderFeeRecord) {
	at := time.Date(2026, 11, 10, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	fills := []trading.Fill{
		{AccountID: "a", TradeDate: "2026-11-10", Symbol: "510300", Exchange: trading.ExchangeSH, FillID: "b1", GatewayOrderID: "buy", Price: 4, Qty: 100, TradeSide: trading.TradeSideBuy, BusinessType: "S", MatchedAt: at},
		{AccountID: "a", TradeDate: "2026-11-10", Symbol: "510300", Exchange: trading.ExchangeSH, FillID: "b2", GatewayOrderID: "buy", Price: 4, Qty: 100, TradeSide: trading.TradeSideBuy, BusinessType: "S", MatchedAt: at.Add(time.Second)},
		{AccountID: "a", TradeDate: "2026-11-10", Symbol: "510300", Exchange: trading.ExchangeSH, FillID: "s1", GatewayOrderID: "sell", Price: 5, Qty: 100, TradeSide: trading.TradeSideSell, BusinessType: "S", MatchedAt: at.Add(2 * time.Second)},
	}
	var fees []ledger.OrderFeeRecord
	for _, side := range []string{"buy", "sell"} {
		tradeSide := "B"
		if side == "sell" {
			tradeSide = "S"
		}
		fees = append(fees, ledger.OrderFeeRecord{AccountID: "a", TradeDate: "2026-11-10", GatewayOrderID: side, Symbol: "510300", Exchange: "SH", TradeSide: tradeSide, Currency: "CNY", TotalFee: 2, FeeComplete: true, AssociationComplete: true, FeeSource: "oc_order_fee"})
	}
	return fills, fees
}

func TestReplayCoreCostDayFeesAndCarry(t *testing.T) {
	fills, fees := coreReplayFixture()
	r, err := ReplayCoreCostDay("a", "2026-11-10", "510300", "SH", 0, 0, fills, fees)
	if err != nil {
		t.Fatal(err)
	}
	if r.BuyFee != 2 || r.SellFee != 2 || r.CloseQuantity != 100 || r.CloseTotalCost != 401 || r.RealizedPnL != 97 {
		t.Fatalf("wrong fill/fee accounting: %+v", r)
	}
	next, err := ReplayCoreCostDay("a", "2026-11-11", "510300", "SH", r.CloseQuantity, r.CloseTotalCost, nil, nil)
	if err != nil || next.CloseTotalCost != 401 || next.CloseQuantity != 100 {
		t.Fatalf("carry failed: %+v %v", next, err)
	}
}

func TestReplayCoreCostDayRejectsMissingAndConflictingEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*[]trading.Fill, *[]ledger.OrderFeeRecord){
		"missing fee":        func(_ *[]trading.Fill, f *[]ledger.OrderFeeRecord) { *f = nil },
		"incomplete fee":     func(_ *[]trading.Fill, f *[]ledger.OrderFeeRecord) { (*f)[0].FeeComplete = false },
		"wrong fee account":  func(_ *[]trading.Fill, f *[]ledger.OrderFeeRecord) { (*f)[0].AccountID = "other" },
		"duplicate":          func(f *[]trading.Fill, _ *[]ledger.OrderFeeRecord) { *f = append(*f, (*f)[0]) },
		"zero price":         func(f *[]trading.Fill, _ *[]ledger.OrderFeeRecord) { (*f)[0].Price = 0 },
		"wrong day":          func(f *[]trading.Fill, _ *[]ledger.OrderFeeRecord) { (*f)[0].TradeDate = "2026-11-09" },
		"redemption":         func(f *[]trading.Fill, _ *[]ledger.OrderFeeRecord) { (*f)[0].TradeSide = trading.TradeSideRedemption },
		"oversell":           func(f *[]trading.Fill, _ *[]ledger.OrderFeeRecord) { (*f)[2].Qty = 201 },
		"ambiguous sequence": func(f *[]trading.Fill, _ *[]ledger.OrderFeeRecord) { (*f)[2].MatchedAt = (*f)[1].MatchedAt },
		"nan fee":            func(_ *[]trading.Fill, f *[]ledger.OrderFeeRecord) { (*f)[0].TotalFee = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) {
			fills, fees := coreReplayFixture()
			mutate(&fills, &fees)
			if _, err := ReplayCoreCostDay("a", "2026-11-10", "510300", "SH", 0, 0, fills, fees); err == nil {
				t.Fatal("unverified input accepted")
			}
		})
	}
}
