package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func TestDatedAssetUsesCloseSnapshotWithoutPerformanceOverlayOrLiveValues(t *testing.T) {
	cfg := config.Default()
	cfg.Service.Environment = config.EnvironmentProduction
	cfg.Accounts = []config.AccountRouteConfig{{AccountID: "acct-1"}}
	store := &fakeSettlementStore{assetSnapshotResult: trading.Asset{
		AccountID: "acct-1", CashTotal: 1000, MarketValue: 200, ReverseRepoReceivable: 3000,
		NetAsset: 4200, UpdatedAt: time.Date(2026, 10, 22, 15, 1, 0, 0, timeutil.Location()),
	}}
	orders := &fakeOrderSubmitter{}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: orders, Settlements: store,
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/asset?trade_date=20261022", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			Asset trading.Asset `json:"asset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	asset := response.Data.Asset
	if asset.NetAsset != 4200 || asset.ReverseRepoReceivable != 3000 || asset.AccountDayPnL != nil || asset.AccountDayPnLStatus != "pending" {
		t.Fatalf("snapshot or financial availability changed: %+v", asset)
	}
	if store.assetSnapshotAccountID != "acct-1" || store.assetSnapshotTradeDate != "2026-10-22" || store.assetSnapshotQueryType != "close" {
		t.Fatalf("wrong snapshot: %+v", store)
	}
	if orders.assetAccountID != "" || orders.fillQuery.AccountID != "" || store.performanceAccountID != "" {
		t.Fatal("dated snapshot used live values, recomputed repo, or loaded performance diagnostics")
	}
}

func TestDatedAssetRejectsMissingSnapshotsAndInvalidScope(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		err  error
		want int
	}{
		{"bad_date", "/v1/accounts/acct-1/asset?trade_date=20260230", nil, 400},
		{"unknown_account", "/v1/accounts/unknown/asset?trade_date=20261022", nil, 404},
		{"missing_close", "/v1/accounts/acct-1/asset?trade_date=20261022", ledger.ErrAssetNotFound, 404},
		{"database_failure", "/v1/accounts/acct-1/asset?trade_date=20261022", errors.New("database failed"), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Accounts = []config.AccountRouteConfig{{AccountID: "acct-1"}}
			orders := &fakeOrderSubmitter{}
			handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
				Orders: orders, Settlements: &fakeSettlementStore{err: test.err},
			})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, test.path, nil))
			if rec.Code != test.want || orders.assetAccountID != "" {
				t.Fatalf("invalid snapshot fell back to live data: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDatedAssetUsesRequestedLedgerDateEvenWhenImportedLater(t *testing.T) {
	cfg := config.Default()
	cfg.Service.Environment = config.EnvironmentProduction
	cfg.Accounts = []config.AccountRouteConfig{{AccountID: "acct-1"}}
	store := &fakeSettlementStore{assetSnapshotResult: trading.Asset{
		AccountID: "acct-1", CashTotal: 1000, ReverseRepoReceivable: 3000, NetAsset: 4000,
		UpdatedAt: time.Date(2026, 10, 23, 10, 0, 0, 0, timeutil.Location()),
	}}
	perf := &fakePerformanceService{navs: []ledger.PerformanceNAV{{
		AccountID: "acct-1", TradeDate: "2026-10-22", IsCurrent: true, Status: "provisional",
		FormulaVersion: "performance_economic_nav.v2.7", CloseEconomicNAV: 4000, AccountDayPnL: 0,
		PnLComponents: map[string]any{"market_valuation": map[string]any{"close_visible_cash": 1000.0, "close_position_value": 0.0}},
	}}}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{}, Settlements: store, Performance: perf,
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/asset?trade_date=20261022", nil))
	var response struct {
		Data struct {
			Asset trading.Asset `json:"asset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	asset := response.Data.Asset
	if rec.Code != 200 || asset.NetAsset != 4000 || asset.AccountDayPnL == nil || *asset.AccountDayPnL != 0 ||
		asset.AccountDayPnLStatus != "available" || asset.AccountDayPnLTradeDate != "2026-10-22" {
		t.Fatalf("snapshot import clock replaced the ledger date or lost repo-only assets: %s", rec.Body.String())
	}
}
