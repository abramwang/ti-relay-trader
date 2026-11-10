package main

import (
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func fuying23TestEvidence() fuying23InventoryEvidence {
	at := func(clock string) time.Time { v, _ := time.Parse(time.RFC3339, "2026-10-23T"+clock+"+08:00"); return v }
	e := fuying23InventoryEvidence{CashSHA: fuying23CashSHA, Open: []trading.Position{{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, Quantity: 292000}}}
	for _, phase := range []string{"open", "close"} {
		stamp := at("09:01:00")
		e.Observations = append(e.Observations, ledger.AssetPositionObservation{AccountID: fuying13Account, TradeDate: fuying23Date, SnapshotType: phase, PositionCapturedAt: &stamp})
	}
	for _, r := range []struct {
		id, clock string
		side      trading.TradeSide
		qty       int64
		amount    float64
	}{
		{"12002A620000066", "09:16:18", trading.TradeSideSell, 0, 0},
		{"12002A620001102", "09:55:32", trading.TradeSideSell, 292000, 278568},
		{"12002A620002593", "10:11:32", trading.TradeSideBuy, 1000000, 945000},
		{"12002A620002684", "10:13:56", trading.TradeSideRedemption, 1000000, 0},
		{"12002A620003017", "10:21:22", trading.TradeSideBuy, 536400, 504216},
		{"12002A620003020", "10:21:42", trading.TradeSideBuy, 463600, 437104.1},
		{"12002A620003347", "10:24:45", trading.TradeSideRedemption, 1000000, 0},
		{"12002A620003915", "11:08:09", trading.TradeSideBuy, 1000000, 944000},
		{"12002A620003992", "11:29:07", trading.TradeSideRedemption, 1000000, 0},
	} {
		business := trading.BusinessTypeStock
		if r.side == trading.TradeSideRedemption {
			business = trading.BusinessTypeETF
		}
		basket := "etfarb#159869"
		if r.id == "12002A620003020" {
			basket = ""
		}
		e.Orders = append(e.Orders, trading.Order{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, GatewayOrderID: fuying23Prefix + r.id, TradeSide: r.side, BusinessType: business, CumFilledQty: r.qty, OrderQty: 1000000, IsTerminal: true, BasketID: basket})
		if r.qty == 0 {
			continue
		}
		if r.side == trading.TradeSideRedemption {
			e.Transfers = append(e.Transfers, trading.ComponentTransfer{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, ComponentSymbol: "159869", ComponentExchange: trading.ExchangeSZ, GatewayOrderID: fuying23Prefix + r.id, TradeSide: r.side, BusinessType: business, Qty: r.qty, ComponentQty: r.qty, MatchedAt: at(r.clock)})
		} else {
			e.Fills = append(e.Fills, trading.Fill{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, GatewayOrderID: fuying23Prefix + r.id, FillID: r.id, TradeSide: r.side, BusinessType: business, Qty: r.qty, Price: r.amount / float64(r.qty), MatchedAt: at(r.clock)})
		}
	}
	for id, v := range map[string]int64{"2747072774": 11027610, "2747072498": -4859, "2747071957": -94404720, "2747070185": -4859, "2747070001": 11027610, "2747069812": -43712596, "2747069775": -50424121, "2747068634": -4859, "2747068292": 11027610, "2747067937": -94504725, "2747067726": 27855407} {
		f := brokerCashFlowRow{FlowID: id, TradeDate: "20261023", Code: "159869"}
		if v > 0 {
			f.InCents = v
		} else {
			f.OutCents = -v
		}
		e.Cash = append(e.Cash, f)
	}
	return e
}

func TestFuying23InventoryBoundariesAndExactAmounts(t *testing.T) {
	if fuying23BasketMatches("etfarb#1598699") || !fuying23BasketMatches("etfarb#159869.SZSE#102120#b") {
		t.Fatal("basket symbol boundary not exact")
	}
	e := fuying23TestEvidence()
	before := append([]trading.Fill(nil), e.Fills...)
	if err := validateFuying23Inventory(e); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, e.Fills) {
		t.Fatal("mutated raw executions")
	}
	// Split executions are allowed when their total and temporal inventory agree.
	f := e.Fills[3]
	e.Fills[3].Qty = f.Qty / 2
	f.Qty -= e.Fills[3].Qty
	f.FillID += "-second"
	e.Fills = append(e.Fills, f)
	if err := validateFuying23Inventory(e); err != nil {
		t.Fatal(err)
	}
	if err := validateFuying23Inventory(e); err != nil {
		t.Fatal("repeat audit changed evidence", err)
	}
}

func TestFuying23InventoryRejectsChangedOrIncompleteEvidence(t *testing.T) {
	cases := map[string]func(*fuying23InventoryEvidence){
		"wrong_account":               func(e *fuying23InventoryEvidence) { e.Orders[0].AccountID = "other" },
		"wrong_date":                  func(e *fuying23InventoryEvidence) { e.Fills[0].TradeDate = "2026-10-22" },
		"opening_quantity":            func(e *fuying23InventoryEvidence) { e.Open[0].Quantity++ },
		"closing_inventory":           func(e *fuying23InventoryEvidence) { e.Close = append(e.Close, e.Open[0]) },
		"missing_snapshot":            func(e *fuying23InventoryEvidence) { e.Observations[1].PositionCapturedAt = nil },
		"new_order":                   func(e *fuying23InventoryEvidence) { e.Orders = append(e.Orders, e.Orders[0]) },
		"duplicate_order":             func(e *fuying23InventoryEvidence) { e.Orders[0] = e.Orders[1] },
		"missing_fill":                func(e *fuying23InventoryEvidence) { e.Fills = e.Fills[1:] },
		"duplicate_fill":              func(e *fuying23InventoryEvidence) { e.Fills = append(e.Fills, e.Fills[0]) },
		"changed_fill_qty":            func(e *fuying23InventoryEvidence) { e.Fills[2].Qty++ },
		"limit_not_execution_price":   func(e *fuying23InventoryEvidence) { e.Fills[3].Price = 1.066 },
		"rounded_summary_price":       func(e *fuying23InventoryEvidence) { e.Fills[3].Price = .943 },
		"nonfinite_price":             func(e *fuying23InventoryEvidence) { e.Fills[3].Price = math.NaN() },
		"nonterminal":                 func(e *fuying23InventoryEvidence) { e.Orders[4].IsTerminal = false },
		"conflicting_group":           func(e *fuying23InventoryEvidence) { e.Orders[4].T0OrderGroupID = "other" },
		"conflicting_strategy":        func(e *fuying23InventoryEvidence) { e.Orders[4].StrategyID = "other" },
		"conflicting_basket":          func(e *fuying23InventoryEvidence) { e.Orders[5].BasketID = "other-strategy" },
		"missing_redemption":          func(e *fuying23InventoryEvidence) { e.Transfers = e.Transfers[1:] },
		"duplicate_redemption":        func(e *fuying23InventoryEvidence) { e.Transfers[1] = e.Transfers[0] },
		"core_not_sold_first":         func(e *fuying23InventoryEvidence) { e.Fills[0].MatchedAt = e.Fills[2].MatchedAt },
		"buy_crosses_round":           func(e *fuying23InventoryEvidence) { e.Fills[3].MatchedAt = e.Transfers[1].MatchedAt.Add(time.Second) },
		"simultaneous_buy_redemption": func(e *fuying23InventoryEvidence) { e.Transfers[1].MatchedAt = e.Fills[3].MatchedAt },
		"missing_timestamp":           func(e *fuying23InventoryEvidence) { e.Fills[0].MatchedAt = time.Time{} },
		"changed_cash_hash":           func(e *fuying23InventoryEvidence) { e.CashSHA = "other" },
		"cash_wrong_sign": func(e *fuying23InventoryEvidence) {
			e.Cash[0].InCents, e.Cash[0].OutCents = e.Cash[0].OutCents, e.Cash[0].InCents
		},
		"cash_duplicate": func(e *fuying23InventoryEvidence) { e.Cash[0] = e.Cash[1] },
		"cash_missing":   func(e *fuying23InventoryEvidence) { e.Cash = e.Cash[1:] },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := fuying23TestEvidence()
			mutate(&e)
			if err := validateFuying23Inventory(e); err == nil {
				t.Fatal("changed evidence accepted")
			}
		})
	}
}

func TestFuying23PreviewFailsBeforeConfigWithoutEarlierStages(t *testing.T) {
	err := runFuying13Review20261110([]string{"-oct23-inventory-preview", "-output", filepath.Join(t.TempDir(), "review.json"), "-config", "not-a-config"})
	if err == nil || !strings.Contains(err.Error(), "requires all earlier") {
		t.Fatal(err)
	}
}
