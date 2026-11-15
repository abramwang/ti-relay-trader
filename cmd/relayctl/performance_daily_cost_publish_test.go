package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDailyCostPublicationScope(t *testing.T) {
	for _, args := range [][]string{nil, {"--account", "other"}, {"--date", "20261114"}, {"--apply"}} {
		if runDailyCostPublish(args) == nil {
			t.Fatal("unsafe arguments accepted")
		}
	}
	b, err := os.ReadFile("../../outputs/recovery/fuying13_daily_attribution_20261115/formal-extension-v1.json")
	if os.IsNotExist(err) {
		t.Skip("private evidence absent")
	}
	if err != nil {
		t.Fatal(err)
	}
	var p dailyCostPlan
	if err = json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	c, prev, err := dailyCostCandidate(p)
	if err != nil || c.TradeDate != "2026-11-13" || len(c.Positions) != 52 || len(prev) != 46 {
		t.Fatal("invalid candidate", err)
	}
	for _, kind := range []string{"account", "test", "published", "writes", "missing-anchor", "anchor-change", "target", "partial-cost"} {
		_ = json.Unmarshal(b, &p)
		switch kind {
		case "account":
			p.Account = "other"
		case "test":
			p.Environment = "test"
		case "published":
			p.Publishable = true
		case "writes":
			p.Writes = 1
		case "missing-anchor":
			p.Formal = nil
		case "anchor-change":
			p.Costs[1].Positions[0].CloseTotalCost++
		case "target":
			p.Costs[2].TradeDate = "2026-11-12"
		case "partial-cost":
			p.Costs[2].Summary.QuantityBreaks = 1
		}
		if _, _, err := dailyCostCandidate(p); err == nil {
			t.Fatalf("unsafe scope: %s", kind)
		}
	}
}
