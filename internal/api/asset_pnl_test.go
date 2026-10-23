package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func TestAssetPublishedDayPnLRequiresMatchingCloseSnapshot(t *testing.T) {
	for _, test := range []struct {
		name          string
		change        func(*trading.Asset, *ledger.PerformanceNAV, *fakePerformanceService, *config.Config)
		wantPublished bool
		wantPnL       float64
	}{
		{name: "published", wantPublished: true, wantPnL: 24818.659135},
		{name: "zero", wantPublished: true, change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.AccountDayPnL = 0
		}},
		{name: "negative", wantPublished: true, wantPnL: -100, change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.AccountDayPnL = -100
		}},
		{name: "blocked", change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.Status = "blocked"
		}},
		{name: "old_version", change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.IsCurrent = false
		}},
		{name: "wrong_account", change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.AccountID = "other"
		}},
		{name: "wrong_day", change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.TradeDate = "2026-10-16"
		}},
		{name: "no_valuation", change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.PnLComponents = nil
		}},
		{name: "same_net_changed_cash_and_market", change: func(asset *trading.Asset, _ *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			asset.CashTotal += 100
			asset.MarketValue -= 100
		}},
		{name: "repo_changed", change: func(asset *trading.Asset, _ *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			asset.NetAsset -= 4260000
		}},
		{name: "pre_open", change: func(asset *trading.Asset, _ *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			asset.UpdatedAt = asset.UpdatedAt.Add(-6 * time.Hour)
		}},
		{name: "test_counter", change: func(_ *trading.Asset, _ *ledger.PerformanceNAV, _ *fakePerformanceService, cfg *config.Config) {
			cfg.Service.Environment = config.EnvironmentTest
		}},
		{name: "query_failed", change: func(_ *trading.Asset, _ *ledger.PerformanceNAV, perf *fakePerformanceService, _ *config.Config) {
			perf.err = errors.New("read failed")
		}},
		{name: "nonfinite", change: func(_ *trading.Asset, nav *ledger.PerformanceNAV, _ *fakePerformanceService, _ *config.Config) {
			nav.AccountDayPnL = math.NaN()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			asset := trading.Asset{AccountID: "acct-1", CashTotal: 41000508.679135, MarketValue: 4005806.2,
				NetAsset: 49266314.879135, ReverseRepoReceivable: 4260000, DayProfit: 26567.359135,
				UpdatedAt: time.Date(2026, 10, 19, 15, 1, 8, 0, timeutil.Location())}
			nav := ledger.PerformanceNAV{AccountID: "acct-1", TradeDate: "2026-10-19", IsCurrent: true,
				Status: "provisional", FormulaVersion: "performance_economic_nav.v2.7",
				CloseEconomicNAV: 49266314.879135, AccountDayPnL: 24818.659135,
				PnLComponents: map[string]any{"market_valuation": map[string]any{"close_visible_cash": asset.CashTotal, "close_position_value": asset.MarketValue}}}
			cfg := config.Default()
			cfg.Service.Environment = config.EnvironmentProduction
			perf := &fakePerformanceService{}
			if test.change != nil {
				test.change(&asset, &nav, perf, &cfg)
			}
			perf.navs = []ledger.PerformanceNAV{nav}
			server := &Server{cfg: cfg, perf: perf, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			got := server.enrichAssetWithPublishedDayPnL(context.Background(), asset)
			if (got.AccountDayPnL != nil) != test.wantPublished {
				t.Fatalf("published=%v want=%v: %+v", got.AccountDayPnL, test.wantPublished, got)
			}
			if test.wantPublished {
				if math.Abs(*got.AccountDayPnL-test.wantPnL) > 0.000001 || got.DayProfit != *got.AccountDayPnL || got.AccountDayPnLTradeDate != nav.TradeDate || got.AccountDayPnLSource != nav.FormulaVersion {
					t.Fatalf("incorrect published account pnl: %+v", got)
				}
			} else if got.DayProfit != asset.DayProfit {
				t.Fatal("unmatched NAV overwrote the asset")
			}
		})
	}
}

func TestCloseAssetWithoutPublishedPnLNeverAdvertisesEstimatedAccountReturn(t *testing.T) {
	for _, test := range []struct {
		name       string
		navs       []ledger.PerformanceNAV
		wantStatus string
	}{
		{"unavailable", nil, "pending"},
		{"blocked", []ledger.PerformanceNAV{{AccountID: "acct-1", TradeDate: "2026-10-23", IsCurrent: true, Status: "blocked"}}, "blocked"},
		{"mismatched", []ledger.PerformanceNAV{{AccountID: "acct-1", TradeDate: "2026-10-23", IsCurrent: true,
			Status: "provisional", FormulaVersion: "performance_economic_nav.v2.7", CloseEconomicNAV: 1100,
			PnLComponents: map[string]any{"market_valuation": map[string]any{"close_visible_cash": 900.0, "close_position_value": 100.0}}}}, "snapshot_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Service.Environment = config.EnvironmentProduction
			server := &Server{cfg: cfg, perf: &fakePerformanceService{navs: test.navs}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			asset := trading.Asset{AccountID: "acct-1", CashTotal: 1000, MarketValue: 100, NetAsset: 1100,
				DayProfit: -2000000, UpdatedAt: time.Date(2026, 10, 23, 15, 1, 0, 0, timeutil.Location())}
			got := server.enrichAssetWithPublishedDayPnL(context.Background(), asset)
			if got.AccountDayPnL != nil || got.AccountDayPnLStatus != test.wantStatus || got.AccountDayPnLReason == "" || got.AccountDayPnLTradeDate != "2026-10-23" {
				t.Fatalf("missing PnL represented as a valid number: %+v", got)
			}
			if got.NetAsset != asset.NetAsset || got.DayProfit != asset.DayProfit {
				t.Fatal("quality projection overwrote the stored asset or raw broker diagnostic")
			}
		})
	}
}
