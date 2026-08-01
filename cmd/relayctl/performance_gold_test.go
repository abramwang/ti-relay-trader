package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadPerformanceGoldCSV(t *testing.T) {
	path := writePerformanceGoldTestCSV(t, `trade_date,open_asset_excluding_fund_occupancy,close_asset_excluding_fund_occupancy,daily_pnl
2026-07-29,5320996.04,5329753.88,8745.97
20260730,5329753.88,5240051.09,-89878.32
`)
	confirmedAt := time.Date(2026, 8, 1, 16, 0, 0, 0, time.UTC)

	items, err := readPerformanceGoldCSV(
		path,
		"314000046830",
		defaultPerformanceGoldSource,
		"manual.csv",
		"confirmed",
		"user",
		confirmedAt,
	)
	if err != nil {
		t.Fatalf("readPerformanceGoldCSV() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("item count = %d", len(items))
	}
	if items[1].TradeDate != "2026-07-30" {
		t.Fatalf("normalized trade date = %q", items[1].TradeDate)
	}
	if diff := items[0].OvernightAdjustment - 11.87; diff < -0.000001 || diff > 0.000001 {
		t.Fatalf("overnight adjustment = %.6f", items[0].OvernightAdjustment)
	}
	if items[0].ConfirmedAt != confirmedAt || items[0].RawPayload["row_number"] != 2 {
		t.Fatalf("audit fields = %#v", items[0])
	}
}

func TestReadPerformanceGoldCSVRejectsDuplicateTradeDate(t *testing.T) {
	path := writePerformanceGoldTestCSV(t, `trade_date,open_asset_excluding_fund_occupancy,close_asset_excluding_fund_occupancy,daily_pnl
2026-07-29,100,101,1
20260729,101,102,1
`)

	_, err := readPerformanceGoldCSV(path, "acct-1", defaultPerformanceGoldSource, "manual.csv", "draft", "", time.Time{})
	if err == nil || !strings.Contains(err.Error(), "duplicates trade_date") {
		t.Fatalf("readPerformanceGoldCSV() error = %v", err)
	}
}

func writePerformanceGoldTestCSV(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gold.csv")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write test CSV: %v", err)
	}
	return path
}
