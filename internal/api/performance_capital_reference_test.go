package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

func capitalSample() (ledger.PerformanceNAV, ledger.PerformanceNAV) {
	prior := ledger.PerformanceNAV{AccountID: "acct", TradeDate: "2026-07-01", Status: "provisional", FormulaVersion: "broker_reconstructed_economic_nav.v1", CloseEconomicNAV: 153307151.58,
		PnLComponents: map[string]any{"market_valuation": map[string]any{"close_position_value": 22236308.0}}}
	nav := ledger.PerformanceNAV{AccountID: "acct", TradeDate: "2026-07-02", Status: "provisional", FormulaVersion: prior.FormulaVersion,
		OpenEconomicNAV: 153307151.58, ExternalNetFlow: -114000000, AccountDayPnL: -1778039.17,
		CloseEconomicNAV: 37529112.41, ReturnDenominator: 39307151.58, DailyReturn: -.045234495468, CumulativeNAV: .904229077133}
	return nav, prior
}

func TestCapitalReferenceSeparatesJuly2ReturnAndExposure(t *testing.T) {
	nav, prior := capitalSample()
	ref := performanceCapitalReference(nav, &prior)
	if ref.DailyReturn == nil || math.Abs(*ref.DailyReturn-(-.011597887976362074)) > 1e-12 {
		t.Fatalf("reference return = %+v", ref)
	}
	if ref.PositionRatioBefore == nil || ref.PositionRatioAfter == nil ||
		math.Abs(*ref.PositionRatioBefore-22236308.0/153307151.58) > 1e-12 ||
		math.Abs(*ref.PositionRatioAfter-22236308.0/39307151.58) > 1e-12 || ref.PositionValueDate != "2026-07-01" {
		t.Fatalf("constant-position reference = %+v", ref)
	}
	if nav.DailyReturn != -.045234495468 || nav.CumulativeNAV != .904229077133 || nav.AccountDayPnL != -1778039.17 {
		t.Fatal("reference changed the published NAV")
	}
	nav.AccountDayPnL = -nav.AccountDayPnL
	gain := performanceCapitalReference(nav, &prior)
	if math.Abs(*gain.DailyReturn+*ref.DailyReturn) > 1e-12 {
		t.Fatal("gain/loss rules are asymmetric")
	}
}

func TestCapitalReferenceMissingOrIncompatiblePositionEvidence(t *testing.T) {
	nav, prior := capitalSample()
	for _, change := range []string{"missing", "account", "blocked", "basis", "capital"} {
		t.Run(change, func(t *testing.T) {
			p := prior
			pointer := &p
			switch change {
			case "missing":
				pointer = nil
			case "account":
				p.AccountID = "other"
			case "blocked":
				p.Status = "blocked"
			case "basis":
				p.FormulaVersion = "broker_cash_repo_carveout.v1"
			case "capital":
				p.CloseEconomicNAV += 10
			}
			ref := performanceCapitalReference(nav, pointer)
			if ref.DailyReturn == nil || ref.PositionValue != nil || ref.PositionRatioBefore != nil || ref.PositionRatioAfter != nil || ref.PositionValueSource != "unavailable" {
				t.Fatalf("unsupported position imputed: %+v", ref)
			}
		})
	}
}

func TestCapitalReferenceZeroBlockedAndNonpositiveCapital(t *testing.T) {
	nav, prior := capitalSample()
	nav.PnLComponents = map[string]any{"market_valuation": map[string]any{"open_position_value": 0.0}}
	nav.AccountDayPnL = 0
	ref := performanceCapitalReference(nav, &prior)
	if ref.DailyReturn == nil || *ref.DailyReturn != 0 || ref.PositionRatioBefore == nil || *ref.PositionRatioBefore != 0 || ref.PositionValueSource != "open_position_value" {
		t.Fatalf("zero confused with missing: %+v", ref)
	}
	nav.ExternalNetFlow = -nav.OpenEconomicNAV
	ref = performanceCapitalReference(nav, &prior)
	if ref.CapitalAfterNetFlow != nil || ref.PositionRatioAfter != nil {
		t.Fatalf("invalid after-flow capital: %+v", ref)
	}
	nav.Status = "blocked"
	if ref := performanceCapitalReference(nav, &prior); ref.DailyReturn != nil || ref.PositionRatioBefore != nil {
		t.Fatalf("blocked reference leaked: %+v", ref)
	}
	nav.Status, nav.OpenEconomicNAV = "provisional", 0
	if ref := performanceCapitalReference(nav, &prior); ref.DailyReturn != nil {
		t.Fatalf("zero denominator: %+v", ref)
	}
}

type capitalReferenceService struct {
	*fakePerformanceService
	prior    *ledger.PerformanceNAV
	lookups  int
	priorErr error
}

func (s *capitalReferenceService) PreviousPerformanceNAV(_ context.Context, _, _ string) (*ledger.PerformanceNAV, error) {
	s.lookups++
	return s.prior, s.priorErr
}

func TestCapitalReferenceSingleDateAPIAndCSV(t *testing.T) {
	nav, prior := capitalSample()
	perf := &capitalReferenceService{fakePerformanceService: &fakePerformanceService{navs: []ledger.PerformanceNAV{nav}}, prior: &prior}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Settlements: &fakeSettlementStore{}, Performance: perf,
	})
	path := "/v1/accounts/acct/performance/series?date_from=20260702&date_to=20260702"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	var payload struct {
		Data struct {
			Series []ledger.DailyPerformance `json:"series"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(payload.Data.Series) != 1 || perf.lookups != 1 {
		t.Fatalf("API = %d %s", response.Code, response.Body.String())
	}
	item := payload.Data.Series[0]
	if item.CapitalReference == nil || item.CapitalReference.PositionRatioBefore == nil || item.ReturnRate != nav.DailyReturn {
		t.Fatalf("single-day reference = %+v", item)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, strings.Replace(path, "series?", "series.csv?", 1), nil))
	rows, err := csv.NewReader(strings.NewReader(response.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(rows) != 2 || len(rows[0]) != len(rows[1]) {
		t.Fatalf("CSV = %d %s", response.Code, response.Body.String())
	}
	values := map[string]string{}
	for i, field := range rows[0] {
		values[field] = rows[1][i]
	}
	if values["reference_position_value_date"] != "2026-07-01" || values["reference_daily_return"] == "" || values["reference_daily_return"] == values["return_rate"] {
		t.Fatalf("CSV reference = %+v", values)
	}
	perf.priorErr = context.DeadlineExceeded
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	item = payload.Data.Series[0]
	if response.Code != 200 || item.ReturnRate != nav.DailyReturn || item.CapitalReference.DailyReturn == nil || item.CapitalReference.PositionValue != nil {
		t.Fatalf("optional position query blocked/polluted official NAV: %d %+v", response.Code, item)
	}
}
