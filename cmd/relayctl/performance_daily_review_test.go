package main

import (
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestVerifyDailyFormalCost(t *testing.T) {
	p := ledger.PositionCostState{AccountID: fuying13Account, TradeDate: "2026-11-11", Symbol: "510310", Exchange: "SH", CostBucket: "CORE", Status: "calculated", CloseQuantity: 100, CloseTotalCost: 200}
	c := performance.CostLedgerResult{AccountID: p.AccountID, TradeDate: p.TradeDate, Status: "calculated", Positions: []ledger.PositionCostState{p}}
	if err := verifyDailyFormalCost(c, []ledger.PositionCostState{p}); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ledger.PositionCostState){
		"account":  func(p *ledger.PositionCostState) { p.AccountID = "other" },
		"date":     func(p *ledger.PositionCostState) { p.TradeDate = "2026-11-12" },
		"symbol":   func(p *ledger.PositionCostState) { p.Symbol = "159915" },
		"cost":     func(p *ledger.PositionCostState) { p.CloseTotalCost++ },
		"quantity": func(p *ledger.PositionCostState) { p.CloseQuantity++ },
		"quality":  func(p *ledger.PositionCostState) { p.QualityFlags = []string{"unverified"} },
		"status":   func(p *ledger.PositionCostState) { p.Status = "blocked" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := p
			change(&changed)
			if verifyDailyFormalCost(c, []ledger.PositionCostState{changed}) == nil {
				t.Fatal("changed formal evidence accepted")
			}
		})
	}
	if verifyDailyFormalCost(c, nil) == nil || verifyDailyFormalCost(c, []ledger.PositionCostState{p, p}) == nil {
		t.Fatal("invalid row coverage accepted")
	}
	c.Positions = append(c.Positions, p)
	if verifyDailyFormalCost(c, []ledger.PositionCostState{p, p}) == nil {
		t.Fatal("duplicate identity accepted")
	}
}

func TestDailyReviewRejectsWritesAndScopeOverrides(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--account", "other"}, {"--date", "20261116"}, {"--csv", "reference/file.csv"}} {
		if runFuyingDailyReview(args) == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
}
