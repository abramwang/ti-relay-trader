package main

import (
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestFuyingTransferPreviewScope(t *testing.T) {
	q := ledger.CashLedgerQuery{AccountID: fuying13Account, TradeDate: fuyingTransferDate, FlowClass: "external_flow", Status: "confirmed"}
	rows, err := addFuyingTransfer(q, nil)
	if err != nil || len(rows) != 1 || rows[0].Amount != -5000000 || !rows[0].EffectiveAt.IsZero() || rows[0].RawPayload["effective_time_precision"] != "date" || rows[0].RawPayload["destination_credit_verified"] != false || rows[0].RawPayload["product_flow_class"] != "internal_transfer" {
		t.Fatalf("source evidence/time/scope lost: %+v, %v", rows, err)
	}
	if _, err := addFuyingTransfer(q, rows); err == nil {
		t.Fatal("duplicate injection allowed")
	}
	compact := q
	compact.TradeDate = "20261113"
	if rows, err := addFuyingTransfer(compact, nil); err != nil || len(rows) != 1 {
		t.Fatal("compact engine date not matched")
	}
	for name, change := range map[string]func(*ledger.CashLedgerQuery){
		"other account":    func(q *ledger.CashLedgerQuery) { q.AccountID = "307000051388" },
		"other date":       func(q *ledger.CashLedgerQuery) { q.TradeDate = "2026-11-12" },
		"range":            func(q *ledger.CashLedgerQuery) { q.TradeDate = "" },
		"product internal": func(q *ledger.CashLedgerQuery) { q.FlowClass = "internal_transfer" },
		"settlement":       func(q *ledger.CashLedgerQuery) { q.FlowClass = "settlement_adjustment" },
		"unconfirmed":      func(q *ledger.CashLedgerQuery) { q.Status = "pending" },
	} {
		t.Run(name, func(t *testing.T) {
			other := q
			change(&other)
			existing := []ledger.CashLedgerEntry{{EntryID: "preserved", Amount: 10.8}}
			got, err := addFuyingTransfer(other, existing)
			if err != nil || len(got) != 1 || got[0].EntryID != "preserved" || got[0].Amount != 10.8 {
				t.Fatalf("unrelated cash changed: %+v %v", got, err)
			}
		})
	}
}

func TestFuyingTransferComparisonRejectsOtherChanges(t *testing.T) {
	before := performance.EconomicNAVResult{AccountID: fuying13Account, TradeDate: fuyingTransferDate}
	after := before
	after.NAV.AccountDayPnL = 5000000
	after.CashFlows.ExternalNetFlow = -5000000
	after.CashFlows.ExternalFlowCount = 1
	if err := validateFuyingTransferComparison(before, after); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*performance.EconomicNAVResult){
		"wrong account":  func(r *performance.EconomicNAVResult) { r.AccountID = "other" },
		"wrong date":     func(r *performance.EconomicNAVResult) { r.TradeDate = "2026-11-12" },
		"persisted":      func(r *performance.EconomicNAVResult) { r.Persisted = true },
		"doubled":        func(r *performance.EconomicNAVResult) { r.CashFlows.ExternalFlowCount = 2 },
		"valuation":      func(r *performance.EconomicNAVResult) { r.Valuation.ClosePositionValue = 1 },
		"basis":          func(r *performance.EconomicNAVResult) { r.AssetBasis.Applied = true },
		"closing assets": func(r *performance.EconomicNAVResult) { r.NAV.CloseEconomicNAV = 1 },
		"opening assets": func(r *performance.EconomicNAVResult) { r.NAV.OpenEconomicNAV = 1 },
		"invented fee":   func(r *performance.EconomicNAVResult) { r.NAV.AccountDayPnL -= 10.8 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := after
			change(&changed)
			if err := validateFuyingTransferComparison(before, changed); err == nil {
				t.Fatal("unrelated economic changes accepted")
			}
		})
	}
}

func TestFuyingTransferPreviewRejectsWriteAndMissingArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--config", "config/relay.prod.yaml"}} {
		if err := runFuyingTransferPreview(args); err == nil {
			t.Fatalf("invalid request accepted: %v", args)
		}
	}
}
