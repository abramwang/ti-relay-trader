package main

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func syntheticCarveout(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"trade_date", "close_asset", "bank_net_flow", "strategy_net_flow", "daily_pnl", "daily_end_net_flow_convention_denominator", "daily_end_net_flow_convention_unit_nav"})
	start := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 65; i++ {
		row := []string{start.AddDate(0, 0, i).Format("20060102"), "218030.91", "0", "0", "0", "218030.91", "1"}
		if i == 0 {
			row[4], row[5] = "", ""
		}
		if i == 64 {
			row[0], row[1], row[2], row[4] = "20250707", "10001442.42", "9782879.23", "532.28"
			row[6] = strconv.FormatFloat(1+532.28/218030.91, 'g', 17, 64)
		}
		_ = w.Write(row)
	}
	w.Flush()
	return buf.Bytes()
}

func TestFuyingCarveoutPublicationPlan(t *testing.T) {
	navs, err := buildFuyingCarveout(syntheticCarveout(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(navs) != 65 || navs[0].ReturnDenominator != 0 || navs[0].AccountDayPnL != 0 || navs[64].AccountDayPnL != 532.28 || navs[64].Status != "finalized" {
		t.Fatalf("unexpected publication plan: %+v", navs)
	}
	if navs[0].PnLComponents["opening_daily_pnl_known"] != false || navs[64].PnLComponents["full_accrual_cutoff_verified"] != false {
		t.Fatal("observed anchor or accrual uncertainty lost")
	}
	if !sameFuyingCarveout(navs[64], navs[64]) {
		t.Fatal("identical publication must be idempotent")
	}
	changed := navs[64]
	changed.AccountDayPnL++
	if sameFuyingCarveout(navs[64], changed) {
		t.Fatal("changed published NAV must not be overwritten")
	}
	changed = navs[64]
	changed.SettlementAdjustment = 1
	if sameFuyingCarveout(navs[64], changed) {
		t.Fatal("changed settlement adjustment must not be accepted")
	}
	changed = navs[64]
	changed.QualityFlags = []string{"unreviewed"}
	if sameFuyingCarveout(navs[64], changed) {
		t.Fatal("changed quality policy must not be accepted")
	}
}

func TestFuyingCarveoutRejectsInvalidBridgeAndChain(t *testing.T) {
	for _, change := range [][2]string{{"9782879.23", "9782879.24"}, {"532.28", "532.29"}, {"20250707", "20250708"}, {"218030.91,0,0,,,1", "218030.92,0,0,,,1"}} {
		data := strings.Replace(string(syntheticCarveout(t)), change[0], change[1], 1)
		if _, err := buildFuyingCarveout([]byte(data), time.Now()); err == nil {
			t.Fatalf("accepted mutation %+v", change)
		}
	}
}

func TestFuyingCarveoutRejectsUnreviewedFileAndMissingConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.csv")
	if err := os.WriteFile(path, syntheticCarveout(t), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFuyingCarveout(path, time.Now()); err == nil {
		t.Fatal("synthetic or modified evidence must fail fingerprint guard")
	}
	if err := runFuyingCarveoutPublication([]string{"-input", path}); err == nil {
		t.Fatal("publication without user confirmation must fail")
	}
}
