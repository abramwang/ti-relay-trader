package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

func TestFeeRuleListCompleteness(t *testing.T) {
	for _, test := range []struct {
		name     string
		rows     int
		limit    string
		count    int
		complete bool
	}{
		{"empty", 0, "500", 0, true},
		{"exact_limit", 500, "500", 500, true},
		{"extra_row", 501, "500", 500, false},
		{"small_limit", 3, "2", 2, false},
		{"default_limit", 201, "", 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			perf := &fakePerformanceService{rules: make([]ledger.FeeRule, test.rows)}
			handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Performance: perf})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/performance/fee-rules?account_id=acct-1&status=active&effective_on=20261010&limit="+test.limit, nil))
			var envelope struct {
				RequestID string `json:"request_id"`
				Time      string `json:"time"`
				Data      struct {
					Rules    []ledger.FeeRule `json:"rules"`
					Count    int              `json:"count"`
					Complete bool             `json:"is_complete"`
					Query    map[string]any   `json:"query"`
				} `json:"data"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
			if envelope.Data.Rules == nil || len(envelope.Data.Rules) != test.count || envelope.Data.Count != test.count || envelope.Data.Complete != test.complete {
				t.Fatalf("unexpected completeness: %s", rec.Body.String())
			}
			if envelope.RequestID == "" || envelope.Time == "" || envelope.Data.Query["effective_on"] != "2026-10-10" || envelope.Data.Query["account_id"] != "acct-1" {
				t.Fatalf("missing audit metadata: %s", rec.Body.String())
			}
			if !perf.feeRuleQuery.FetchExtra || perf.feeRuleQuery.AccountID != "acct-1" || perf.feeRuleQuery.Status != "active" || perf.feeRuleQuery.EffectiveOn != "2026-10-10" {
				t.Fatalf("query = %#v", perf.feeRuleQuery)
			}
		})
	}
}

func TestFeeRuleListInvalidQuery(t *testing.T) {
	for _, query := range []string{"limit=501", "limit=-1", "limit=x", "status=unknown", "effective_on=20260230"} {
		perf := &fakePerformanceService{}
		handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Performance: perf})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/performance/fee-rules?"+query, nil))
		if rec.Code != http.StatusBadRequest || perf.feeRuleQuery.FetchExtra {
			t.Fatalf("query %s returned %d: %s", query, rec.Code, rec.Body.String())
		}
	}
}

func TestFeeRuleWriteRemainsDisabled(t *testing.T) {
	perf := &fakePerformanceService{}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Performance: perf})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/performance/fee-rules", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden || perf.createdRule.RuleID != "" {
		t.Fatalf("write must remain disabled: %d %s", rec.Code, rec.Body.String())
	}
}
