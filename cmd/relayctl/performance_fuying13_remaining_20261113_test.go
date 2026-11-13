package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func TestFuyingInventoryAuditTimestampStability(t *testing.T) {
	var current, frozen []ledger.T0Allocation
	for _, id := range []string{"a", "b", "c"} {
		x := ledger.T0Allocation{LinkID: id, AccountID: fuying13Account, TradeDate: fuying23Date, GatewayOrderID: id, SecurityID: "159869.SZ", TradeSide: "B", Quantity: 100, GroupID: "g", Role: "redemption", Revision: 1, Status: "active", EvidenceType: "inventory_evidence", EvidenceRef: "review", ConfirmedBy: "relay"}
		current = append(current, x)
		x.ConfirmedAt = time.Date(2026, 11, 11, 22, 30, 19, 0, time.FixedZone("", 28800))
		frozen = append(frozen, x)
	}
	a, err := stableFuyingInventoryLinks(current, frozen)
	if err != nil {
		t.Fatal(err)
	}
	b, err := stableFuyingInventoryLinks(current, frozen)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal(err)
	}
	if current[0].ConfirmedAt.IsZero() == false {
		t.Fatal("mutated source")
	}
	current[0].Quantity++
	if _, err := stableFuyingInventoryLinks(current, frozen); err == nil {
		t.Fatal("new economic payload reused old timestamp")
	}
	if _, err := stableFuyingInventoryLinks(current[:2], frozen); err == nil {
		t.Fatal("partial audit accepted")
	}
}

func overnightSourceFixture() fuyingOvernightSourceEvidence {
	at := time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC)
	o := trading.Order{AccountID: fuying13Account, GatewayOrderID: fuyingOvernightBuy, TradeDate: "2026-11-03", Symbol: "159915", Exchange: "SZ", BusinessType: "S", TradeSide: "B", IsTerminal: true, OrderQty: 1000000, CumFilledQty: 1000000, BasketID: "etfarb#159915.SZSE#105630#b"}
	e := fuyingOvernightSourceEvidence{Order: o, Fills: []trading.Fill{{AccountID: o.AccountID, GatewayOrderID: o.GatewayOrderID, TradeDate: o.TradeDate, Symbol: o.Symbol, Exchange: o.Exchange, BusinessType: "S", TradeSide: "B", FillID: "f", Qty: 1000000, Price: 2.452, MatchedAt: at}}, Close: []trading.Position{{AccountID: o.AccountID, TradeDate: o.TradeDate, Symbol: o.Symbol, Exchange: o.Exchange, Quantity: 1000000}}}
	e.Open = []trading.Position{{AccountID: o.AccountID, TradeDate: o.TradeDate, Symbol: o.Symbol, Exchange: o.Exchange, Quantity: 7300}}
	sale := o
	sale.GatewayOrderID, sale.TradeSide, sale.CumFilledQty, sale.OrderQty = "core-sale", "S", 7300, 7300
	e.CoreSales = []trading.Order{sale}
	fill := e.Fills[0]
	fill.GatewayOrderID, fill.FillID, fill.TradeSide, fill.Qty, fill.MatchedAt = sale.GatewayOrderID, "core-fill", "S", 7300, at.Add(-time.Hour)
	e.CoreFills = []trading.Fill{fill}
	for _, phase := range []string{"open", "close"} {
		e.Observations = append(e.Observations, ledger.AssetPositionObservation{AccountID: o.AccountID, TradeDate: o.TradeDate, SnapshotType: phase, PositionCapturedAt: &at})
	}
	return e
}

func TestFuyingOvernightSourceOwnership(t *testing.T) {
	if err := validateFuyingOvernightSource(overnightSourceFixture()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*fuyingOvernightSourceEvidence){
		"wrong account":     func(e *fuyingOvernightSourceEvidence) { e.Order.AccountID = "other" },
		"wrong side":        func(e *fuyingOvernightSourceEvidence) { e.Order.TradeSide = "S" },
		"partial":           func(e *fuyingOvernightSourceEvidence) { e.Order.CumFilledQty-- },
		"core label":        func(e *fuyingOvernightSourceEvidence) { e.Order.BasketID = "BTR" },
		"nonterminal":       func(e *fuyingOvernightSourceEvidence) { e.Order.IsTerminal = false },
		"missing fill":      func(e *fuyingOvernightSourceEvidence) { e.Fills = nil },
		"duplicate fill":    func(e *fuyingOvernightSourceEvidence) { e.Fills = append(e.Fills, e.Fills[0]) },
		"fill date":         func(e *fuyingOvernightSourceEvidence) { e.Fills[0].TradeDate = "2026-11-04" },
		"amount":            func(e *fuyingOvernightSourceEvidence) { e.Fills[0].Price++ },
		"conflicting group": func(e *fuyingOvernightSourceEvidence) { e.Fills[0].T0OrderGroupID = "core" },
		"position":          func(e *fuyingOvernightSourceEvidence) { e.Close[0].Quantity++ },
		"observation":       func(e *fuyingOvernightSourceEvidence) { e.Observations[0].PositionCapturedAt = nil },
		"missing core sale": func(e *fuyingOvernightSourceEvidence) { e.CoreFills = nil },
		"core sold late": func(e *fuyingOvernightSourceEvidence) {
			e.CoreFills[0].MatchedAt = e.Fills[0].MatchedAt.Add(time.Minute)
		},
		"core not flat": func(e *fuyingOvernightSourceEvidence) { e.CoreFills[0].Qty-- },
	} {
		t.Run(name, func(t *testing.T) {
			e := overnightSourceFixture()
			mutate(&e)
			if validateFuyingOvernightSource(e) == nil {
				t.Fatal("changed source accepted")
			}
		})
	}
}

func TestFuyingAllocationApplyGuardBeforeEvidence(t *testing.T) {
	called := false
	err := runFuyingAllocationAudit([]string{"-apply", "-output", filepath.Join(t.TempDir(), "audit.json")}, "test", func(context.Context, *ledger.Repository) (fuyingAllocationPlan, error) {
		called = true
		return fuyingAllocationPlan{}, errors.New("must not load")
	})
	if err == nil || !strings.Contains(err.Error(), "fresh backup required") || called {
		t.Fatal(err, called)
	}
}
