package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestFuyingResearchComparisonArgumentGuards(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", "missing", "--output", "unused"}, {"--apply"}, {"--config", "missing", "--account", "a", "--version", "v", "--output", "x", "--csv", "./x"}} {
		if err := runResearchCostCompare(args); err == nil {
			t.Fatal("unsafe args accepted")
		}
	}
}

func TestFuyingResearchComparisonCSV(t *testing.T) {
	r := performance.ResearchCostComparison{AccountID: "account", VersionID: "version", ResearchOnly: true, Days: []performance.ResearchCostComparisonDay{{TradeDate: "2026-10-12", StoredDayMissing: true, Rows: []performance.ResearchCostComparisonRow{{SecurityID: "510310.SH", CostBucket: "=unsafe", Outcome: "research_only", Research: &ledger.PositionCostState{CloseQuantity: 100, CloseTotalCost: 200, RealizedPnL: -1}, DeltaUnavailable: "stored_row_missing"}}}}}
	path := filepath.Join(t.TempDir(), "rows.csv")
	if err := writeResearchCostCSV(path, r); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatal(err)
	}
	values := map[string]string{}
	for i, key := range rows[0] {
		values[key] = rows[1][i]
	}
	if values["stored_close_cost"] != "" || values["delta_close_cost_micros"] != "" || values["research_close_cost"] != "200" || values["research_realized_pnl"] != "-1" || values["cost_bucket"] != "'=unsafe" {
		t.Fatal(values)
	}
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private report permissions", err)
	}
	if err := writeResearchCostCSV(path, r); err == nil {
		t.Fatal("overwrote report")
	}
}
