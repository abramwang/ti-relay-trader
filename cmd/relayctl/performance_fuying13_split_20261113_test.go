package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func splitAuditTestLinks(t *testing.T) []ledger.T0Allocation {
	t.Helper()
	links, err := loadFuyingSplitAudit("../../" + fuyingSplitFixture)
	if err != nil {
		t.Fatal(err)
	}
	return links
}

func TestFuyingSplitOrderEvidence(t *testing.T) {
	for _, link := range splitAuditTestLinks(t) {
		o := trading.Order{AccountID: link.AccountID, GatewayOrderID: link.GatewayOrderID, TradeDate: link.TradeDate,
			Symbol: "159869", Exchange: "SZ", TradeSide: trading.TradeSide(link.TradeSide), BusinessType: "S",
			OrderQty: link.Quantity, CumFilledQty: link.Quantity, IsTerminal: true, Status: "filled"}
		if link.Quantity == 20200 {
			o.OrderQty, o.Status = 1000000, "cancelled"
		}
		if link.TradeSide == "R" {
			o.BusinessType = "E"
		}
		if err := validateFuyingSplitOrder(link, o); err != nil {
			t.Fatal(err)
		}
		for name, mutate := range map[string]func(*trading.Order){
			"account":  func(v *trading.Order) { v.AccountID = "other" },
			"date":     func(v *trading.Order) { v.TradeDate = "2026-10-16" },
			"id":       func(v *trading.Order) { v.GatewayOrderID += "x" },
			"symbol":   func(v *trading.Order) { v.Symbol = "159915" },
			"exchange": func(v *trading.Order) { v.Exchange = "SH" },
			"side":     func(v *trading.Order) { v.TradeSide = "S" },
			"business": func(v *trading.Order) { v.BusinessType = "unknown" },
			"executed": func(v *trading.Order) { v.CumFilledQty-- },
			"ordered":  func(v *trading.Order) { v.OrderQty++ },
			"terminal": func(v *trading.Order) { v.IsTerminal = false },
			"status":   func(v *trading.Order) { v.Status = "working" },
			"leaves":   func(v *trading.Order) { v.LeavesQty = 1 },
			"group":    func(v *trading.Order) { v.T0OrderGroupID = "core" },
		} {
			t.Run(link.LinkID+"/"+name, func(t *testing.T) {
				bad := o
				mutate(&bad)
				if validateFuyingSplitOrder(link, bad) == nil {
					t.Fatal("changed order accepted")
				}
			})
		}
	}
}

func TestFuyingSplitFillEvidence(t *testing.T) {
	for _, link := range splitAuditTestLinks(t) {
		fill := trading.Fill{FillID: "actual", AccountID: link.AccountID, GatewayOrderID: link.GatewayOrderID,
			TradeDate: link.TradeDate, Symbol: "159869", Exchange: "SZ", TradeSide: trading.TradeSide(link.TradeSide), Qty: link.Quantity}
		if err := validateFuyingSplitFills(link, []trading.Fill{fill}); err != nil {
			t.Fatal(err)
		}
		if (validateFuyingSplitFills(link, nil) == nil) != (link.TradeSide == "R") {
			t.Fatal("only redemptions may omit ordinary fills")
		}
		if validateFuyingSplitFills(link, []trading.Fill{fill, fill}) == nil {
			t.Fatal("duplicate accepted")
		}
		for name, mutate := range map[string]func(*trading.Fill){
			"account":  func(v *trading.Fill) { v.AccountID = "other" },
			"date":     func(v *trading.Fill) { v.TradeDate = "2026-10-16" },
			"order":    func(v *trading.Fill) { v.GatewayOrderID = "other" },
			"security": func(v *trading.Fill) { v.Symbol = "159915" },
			"side":     func(v *trading.Fill) { v.TradeSide = "S" },
			"quantity": func(v *trading.Fill) { v.Qty-- },
			"overflow": func(v *trading.Fill) { v.Qty = 1<<63 - 1 },
			"group":    func(v *trading.Fill) { v.T0OrderGroupID = "core" },
		} {
			t.Run(link.LinkID+"/"+name, func(t *testing.T) {
				bad := fill
				mutate(&bad)
				if validateFuyingSplitFills(link, []trading.Fill{bad}) == nil {
					t.Fatal("changed fill accepted")
				}
			})
		}
		if validateFuyingSplitFills(link, make([]trading.Fill, 500)) == nil {
			t.Fatal("possibly truncated evidence accepted")
		}
	}
}

func TestFuyingSplitReplayNormalizesTime(t *testing.T) {
	links := splitAuditTestLinks(t)
	old := append([]ledger.T0Allocation(nil), links...)
	for i := range old {
		old[i].ConfirmedAt = old[i].ConfirmedAt.In(time.FixedZone("", 8*3600))
	}
	got, err := mergeFuying13SplitReview(old, links)
	if err != nil || len(got) != 3 {
		t.Fatal("equivalent persisted timestamp conflicts", got, err)
	}
	old[0].ConfirmedAt = old[0].ConfirmedAt.Add(time.Second)
	if _, err := mergeFuying13SplitReview(old, links); err == nil {
		t.Fatal("different confirmation accepted")
	}
}

func TestFuyingSplitApplyRequiresBackupBeforeDatabase(t *testing.T) {
	err := runFuyingSplitAudit([]string{"-apply", "-config", "must-not-read", "-output", filepath.Join(t.TempDir(), "audit.json")})
	if err == nil || !strings.Contains(err.Error(), "fresh backup required") {
		t.Fatal(err)
	}
	if err := runFuyingSplitAudit(nil); err == nil {
		t.Fatal("missing output accepted")
	}
}
