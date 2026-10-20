package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

func TestReconstructedEconomicNAVIsPublishedAndAuthoritativeForDefaultRange(t *testing.T) {
	nav := ledger.PerformanceNAV{AccountID: "acct", TradeDate: "2025-07-08", FormulaVersion: "broker_reconstructed_economic_nav.v1", Status: "provisional",
		OpenEconomicNAV: 1000, CloseEconomicNAV: 1005, AccountDayPnL: 5, DailyReturn: .005, CumulativeNAV: 1.005,
		UpdatedAt:     time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC),
		PnLComponents: map[string]any{"account_ledger_verified": true, "trading_observation": map[string]any{"positions_count": 2.0}}}
	series := mergePerformanceNAVDates(nil, []ledger.PerformanceNAV{nav})
	series = overlayPerformanceNAVSeries(series, []ledger.PerformanceNAV{nav})
	if len(series) != 1 || series[0].CapturedAt.IsZero() || series[0].PositionsCount != 2 || series[0].NetAsset != 1005 {
		t.Fatalf("reconstructed published date = %+v", series)
	}
	if source, ok := authoritativePerformanceNAVSource(nav, false); !ok || source != "broker_reconstructed_economic_nav" {
		t.Fatalf("verified historical default anchor = %s/%v", source, ok)
	}
	delete(nav.PnLComponents, "account_ledger_verified")
	if _, ok := authoritativePerformanceNAVSource(nav, false); ok {
		t.Fatal("unverified historical rows must not become authoritative defaults")
	}
}

func TestCashRepoCarveoutPublishedSeries(t *testing.T) {
	navs := []ledger.PerformanceNAV{
		{AccountID: "acct", TradeDate: "2025-04-01", FormulaVersion: "broker_cash_repo_carveout.v1", Status: "finalized", OpenEconomicNAV: 1000, CloseEconomicNAV: 1000, CumulativeNAV: 1},
		{AccountID: "acct", TradeDate: "2025-04-02", FormulaVersion: "broker_cash_repo_carveout.v1", Status: "finalized", OpenEconomicNAV: 1000, CloseEconomicNAV: 1001, AccountDayPnL: 1, DailyReturn: .001, CumulativeNAV: 1.001},
	}
	if err := validatePerformanceNAVScope(navs); err != nil {
		t.Fatal(err)
	}
	series := overlayPerformanceNAVSeries(mergePerformanceNAVDates(nil, navs), navs)
	series, summary := buildPerformanceSeries("acct", "2025-04-01", "2025-04-02", series)
	if len(series) != 2 || summary.TotalPnL != 1 || summary.EndNetAsset != 1001 || series[1].FormulaVersion != "broker_cash_repo_carveout.v1" {
		t.Fatalf("published series = %+v, %+v", series, summary)
	}
	if source, ok := authoritativePerformanceNAVSource(navs[0], false); !ok || source != "broker_statement" {
		t.Fatalf("approved source = %q / %v", source, ok)
	}
}

func TestPublishedPerformanceSurvivesUnavailableOptionalBenchmark(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"archive_incomplete"}`))
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	store := &fakeSettlementStore{performanceSeries: []ledger.DailyPerformance{{AccountID: "acct", TradeDate: "2025-04-01", NetAsset: 1000}}}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Settlements: store})
	request := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct/performance/series?date_from=20250401&date_to=20250401&benchmark_security_id=000001.SH", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"benchmark_error"`) ||
		!strings.Contains(response.Body.String(), `"benchmark_bars_unavailable"`) || strings.Contains(response.Body.String(), `"excess_total_return"`) {
		t.Fatalf("optional benchmark response = %d %s", response.Code, response.Body.String())
	}
}

func TestPerformanceCarveoutCannotBeSplicedIntoAccountNAV(t *testing.T) {
	navs := []ledger.PerformanceNAV{
		{TradeDate: "2025-07-07", FormulaVersion: "broker_cash_repo_carveout.v1", Status: "finalized", CloseEconomicNAV: 1000},
		{TradeDate: "2026-09-09", FormulaVersion: "performance_economic_nav.v2.7", Status: "provisional", OpenEconomicNAV: 9000, CloseEconomicNAV: 9001},
	}
	if err := validatePerformanceNAVScope(navs); !errors.Is(err, errPerformanceScopeConflict) {
		t.Fatalf("scope conflict = %v", err)
	}
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.writePerformanceSeriesError(response, request, errPerformanceScopeConflict)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "PERFORMANCE_SCOPE_CONFLICT") {
		t.Fatalf("scope response = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.writePerformanceSeriesError(response, request, validatePerformanceNAVScope(navs))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "published_ranges") ||
		!strings.Contains(response.Body.String(), "2025-07-07") || !strings.Contains(response.Body.String(), "2026-09-09") {
		t.Fatalf("missing actionable history coverage: %d %s", response.Code, response.Body.String())
	}
	// A daily read may load all earlier NAVs: a different scope is not a previous-close anchor.
	series := overlayPerformanceNAVSeries([]ledger.DailyPerformance{{TradeDate: "2026-09-09"}}, navs)
	if series[0].PreviousNetAsset != 0 || series[0].OvernightAdjustment != 0 {
		t.Fatalf("unrelated carve-out reused as prior close: %+v", series[0])
	}
	navs[1].Status = "blocked"
	if err := validatePerformanceNAVScope(navs); err != nil {
		t.Fatalf("blocked rows do not form a second published scope: %v", err)
	}
}
