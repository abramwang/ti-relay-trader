package main

import (
	"encoding/json"
	"os"
	"testing"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestFuying13ConfirmedSplitFixture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/fuying13_t0_split_buy_review_20261110.json")
	if err != nil {
		t.Fatal(err)
	}
	var links []ledger.T0Allocation
	if err := json.Unmarshal(raw, &links); err != nil {
		t.Fatal(err)
	}
	if err := validateFuying13SplitReview(links); err != nil {
		t.Fatal(err)
	}
	got, err := mergeFuying13SplitReview(nil, links)
	if err != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	got, err = mergeFuying13SplitReview(got, links)
	if err != nil || len(got) != 3 {
		t.Fatal("duplicate overlay", got, err)
	}
	changed := append([]ledger.T0Allocation(nil), links...)
	changed[0].Quantity++
	if err := validateFuying13SplitReview(changed); err == nil {
		t.Fatal("changed confirmed quantity accepted")
	}
	if _, err := mergeFuying13SplitReview(changed, links); err == nil {
		t.Fatal("overrode conflicting audit")
	}
	if err := validateFuying13SplitReview(links[:2]); err == nil {
		t.Fatal("partial group accepted")
	}
}

func TestFuying13CostReviewNoWriteMode(t *testing.T) {
	if err := runFuying13Review20261110([]string{"-persist"}); err == nil {
		t.Fatal("write option accepted")
	}
	if err := runFuying13Review20261110(nil); err == nil {
		t.Fatal("missing report accepted")
	}
}

func TestFuying13CostCarryEligibility(t *testing.T) {
	c := performance.CostLedgerResult{AccountID: fuying13Account, TradeDate: "2026-10-12", Status: "estimated", Positions: []ledger.PositionCostState{{}}}
	if !fuying13CostCarryEligible(c, c.TradeDate) {
		t.Fatal("eligible cost rejected")
	}
	for _, mutate := range []func(*performance.CostLedgerResult){
		func(v *performance.CostLedgerResult) { v.Status = "blocked" }, func(v *performance.CostLedgerResult) { v.Status = "" },
		func(v *performance.CostLedgerResult) { v.AccountID = "other" }, func(v *performance.CostLedgerResult) { v.TradeDate = "2026-10-13" },
		func(v *performance.CostLedgerResult) { v.Summary.QuantityBreaks = 1 }, func(v *performance.CostLedgerResult) { v.Summary.T0BlockedBuckets = 1 },
		func(v *performance.CostLedgerResult) { v.Summary.BlockedItems = 1 }, func(v *performance.CostLedgerResult) { v.Positions = nil },
		func(v *performance.CostLedgerResult) { v.Persisted = true },
	} {
		bad := c
		mutate(&bad)
		if fuying13CostCarryEligible(bad, c.TradeDate) {
			t.Fatal("unsafe carry accepted", bad)
		}
	}
}
