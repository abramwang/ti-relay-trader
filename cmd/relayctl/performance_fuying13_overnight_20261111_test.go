package main

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func fuyingOvernightTestEvidence() fuyingOvernightEvidence {
	at, _ := time.Parse(time.RFC3339, "2026-11-04T13:35:02+08:00")
	e := fuyingOvernightEvidence{Source: ledger.PositionCostState{AccountID: fuying13Account, TradeDate: "2026-11-03", Symbol: "159915", Exchange: "SZ", CostBucket: "ETF_T0_RESIDUAL:" + fuyingOvernightGroup, Status: "calculated", BuyQuantity: 1000000, CloseQuantity: 1000000, BuyAmount: 2452000, BuyFee: 122.6, CloseTotalCost: 2452122.6}}
	e.Orders = []trading.Order{
		{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: fuyingOvernightCoreBuy, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 123200, CumFilledQty: 123200, IsTerminal: true, BasketID: "BTR.84406746|J.0"},
		{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: fuyingOvernightRedemption, TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, OrderQty: 1000000, CumFilledQty: 1000000, IsTerminal: true, BasketID: "etfarb#159915"},
	}
	e.Fills = []trading.Fill{{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, GatewayOrderID: fuyingOvernightCoreBuy, FillID: "fill", TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Qty: 123200, Price: 296296.0 / 123200, MatchedAt: at.Add(-time.Hour)}}
	e.Transfers = []trading.ComponentTransfer{{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, ComponentSymbol: "159915", ComponentExchange: trading.ExchangeSZ, GatewayOrderID: fuyingOvernightRedemption, TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, Qty: 1000000, ComponentQty: 1000000, MatchedAt: at}}
	e.Open = []trading.Position{{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 1000000}}
	e.Close = []trading.Position{{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, Quantity: 123200}}
	for _, phase := range []string{"open", "close"} {
		e.Observations = append(e.Observations, ledger.AssetPositionObservation{AccountID: fuying13Account, TradeDate: "2026-11-04", SnapshotType: phase, PositionCapturedAt: &at})
	}
	e.Fees = []ledger.OrderFeeRecord{{AccountID: fuying13Account, TradeDate: "2026-11-04", GatewayOrderID: fuyingOvernightCoreBuy, RecordScope: "order", FeeComplete: true, AssociationComplete: true, FeeSource: "broker_order_fund_detail", TotalFee: 14.8148}}
	return e
}

func TestFuyingOvernightEvidenceScope(t *testing.T) {
	if err := validateFuyingOvernightEvidence(fuyingOvernightTestEvidence()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*fuyingOvernightEvidence){
		"source_is_core":       func(e *fuyingOvernightEvidence) { e.Source.CostBucket = "CORE" },
		"prior_inventory":      func(e *fuyingOvernightEvidence) { e.Source.OpenQuantity = 1 },
		"wrong_source_day":     func(e *fuyingOvernightEvidence) { e.Source.TradeDate = "2026-11-02" },
		"wrong_source_fee":     func(e *fuyingOvernightEvidence) { e.Source.BuyFee++ },
		"source_blocked":       func(e *fuyingOvernightEvidence) { e.Source.Status = "blocked" },
		"duplicate_order":      func(e *fuyingOvernightEvidence) { e.Orders[1] = e.Orders[0] },
		"extra_order":          func(e *fuyingOvernightEvidence) { e.Orders = append(e.Orders, e.Orders[0]) },
		"not_terminal":         func(e *fuyingOvernightEvidence) { e.Orders[1].IsTerminal = false },
		"conflicting_group":    func(e *fuyingOvernightEvidence) { e.Orders[0].T0OrderGroupID = "other" },
		"conflicting_strategy": func(e *fuyingOvernightEvidence) { e.Orders[0].StrategyID = "other" },
		"wrong_fill_day":       func(e *fuyingOvernightEvidence) { e.Fills[0].TradeDate = "2026-11-03" },
		"wrong_fill_account":   func(e *fuyingOvernightEvidence) { e.Fills[0].AccountID = "other" },
		"duplicate_fill":       func(e *fuyingOvernightEvidence) { e.Fills = append(e.Fills, e.Fills[0]) },
		"missing_fill":         func(e *fuyingOvernightEvidence) { e.Fills = nil },
		"wrong_fill_qty":       func(e *fuyingOvernightEvidence) { e.Fills[0].Qty++ },
		"wrong_fill_price":     func(e *fuyingOvernightEvidence) { e.Fills[0].Price++ },
		"nonfinite_fill":       func(e *fuyingOvernightEvidence) { e.Fills[0].Price = math.NaN() },
		"missing_observation":  func(e *fuyingOvernightEvidence) { e.Observations[1].PositionCapturedAt = nil },
		"wrong_open":           func(e *fuyingOvernightEvidence) { e.Open[0].Quantity++ },
		"wrong_close":          func(e *fuyingOvernightEvidence) { e.Close[0].Quantity++ },
		"extra_transfer":       func(e *fuyingOvernightEvidence) { e.Transfers = append(e.Transfers, e.Transfers[0]) },
		"partial_transfer":     func(e *fuyingOvernightEvidence) { e.Transfers[0].Qty-- },
		"component_not_parent": func(e *fuyingOvernightEvidence) { e.Transfers[0].ComponentSymbol = "000001" },
		"missing_fee":          func(e *fuyingOvernightEvidence) { e.Fees = nil },
		"ambiguous_fee":        func(e *fuyingOvernightEvidence) { e.Fees = append(e.Fees, e.Fees[0]) },
		"incomplete_fee":       func(e *fuyingOvernightEvidence) { e.Fees[0].FeeComplete = false },
		"wrong_fee_order":      func(e *fuyingOvernightEvidence) { e.Fees[0].GatewayOrderID = fuyingOvernightRedemption },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := fuyingOvernightTestEvidence()
			change(&e)
			if err := validateFuyingOvernightEvidence(e); err == nil {
				t.Fatal("changed evidence accepted")
			}
		})
	}
}

func TestFuyingOvernightConfirmationAndPrerequisites(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	link, hash, err := loadFuyingOvernightConfirmation()
	if err != nil || len(hash) != 64 || link.Role != "residual" || link.Quantity != 1000000 || link.GatewayOrderID != fuyingOvernightBuy {
		t.Fatalf("bad confirmation %+v %s %v", link, hash, err)
	}
	err = runFuying13Review20261110([]string{"-nov04-overnight-preview", "-output", filepath.Join(t.TempDir(), "unused.json"), "-config", "does-not-exist"})
	if err == nil || !strings.Contains(err.Error(), "requires October 23") {
		t.Fatalf("prerequisites bypassed: %v", err)
	}
}
