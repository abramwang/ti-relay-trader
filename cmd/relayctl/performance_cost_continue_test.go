package main

import (
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestCleanContinuationCost(t *testing.T) {
	valid := func() performance.CostLedgerResult {
		return performance.CostLedgerResult{AccountID: "a", TradeDate: "2026-11-10", Status: "calculated",
			QualityFlags: []string{"reverse_repo_excluded_from_position_cost"},
			Positions:    []ledger.PositionCostState{{AccountID: "a", TradeDate: "2026-11-10", CostBucket: "CORE", Status: "calculated"}}}
	}
	if err := cleanContinuationCost(valid(), "a", "2026-11-10"); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*performance.CostLedgerResult){
		"other account":     func(c *performance.CostLedgerResult) { c.AccountID = "b" },
		"other date":        func(c *performance.CostLedgerResult) { c.TradeDate = "2026-11-09" },
		"estimated":         func(c *performance.CostLedgerResult) { c.Status = "estimated" },
		"persisted":         func(c *performance.CostLedgerResult) { c.Persisted = true },
		"missing positions": func(c *performance.CostLedgerResult) { c.Positions = nil },
		"quantity break":    func(c *performance.CostLedgerResult) { c.Summary.QuantityBreaks = 1 },
		"missing fee":       func(c *performance.CostLedgerResult) { c.Summary.MissingFeeItems = 1 },
		"t0":                func(c *performance.CostLedgerResult) { c.Summary.T0CostBuckets = 1 },
		"residual":          func(c *performance.CostLedgerResult) { c.Summary.T0ResidualBuckets = 1 },
		"preview flag":      func(c *performance.CostLedgerResult) { c.QualityFlags = []string{"t0_residual_preview_only"} },
		"bad row":           func(c *performance.CostLedgerResult) { c.Positions[0].CostBucket = "ETF_T0_RESIDUAL:x" },
		"flagged row":       func(c *performance.CostLedgerResult) { c.Positions[0].QualityFlags = []string{"estimated"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid()
			change(&c)
			if err := cleanContinuationCost(c, "a", "2026-11-10"); err == nil {
				t.Fatal("unsafe anchor accepted")
			}
		})
	}
}

func TestCostContinuationRejectsWriteAndCSVOptions(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--funds", "reference/example.csv"}, {"--csv", "reference/example.csv"}} {
		if err := runCostContinuation(args); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}
