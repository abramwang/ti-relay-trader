package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

type fakeCycleReviews struct {
	value ledger.CycleReviewVersion
	err   error
}

func (f *fakeCycleReviews) GetCycleReviewVersion(context.Context, string, string) (ledger.CycleReviewVersion, error) {
	return f.value, f.err
}
func TestCycleReviewRoutes(t *testing.T) {
	d := performance.CycleReviewDocument{Schema: performance.CycleReviewSchema, AccountID: "a", VersionID: "v1", DateFrom: "2026-10-12", ObservedThrough: "2026-10-14", SourceSHA256: strings.Repeat("a", 64), Rows: []performance.CycleReviewRow{{SourceDate: "2026-10-12", SecurityID: "510310.SH", LastPosting: "2026-10-14", KnownLegs: 1, Profit: performance.ETFCycleProfit{CycleID: "2026-10-12/510310.SH", Status: "observed_cash_reconciled", NetCyclePnLCents: -100, Trading: performance.ETFCycleTradingEvidence{AccountID: "a", CycleID: "2026-10-12/510310.SH", AcquisitionDate: "2026-10-12", AcquisitionQuantity: 100, RedemptionQuantity: 100, AcquisitionGrossCents: 100, FeeScope: "ordinary_buys_and_component_sales_only", FeeBasis: "broker_cash_reconciled"}, QualityFlags: []string{"private_settlement_coverage_unverified", "observed_cycle_profit_not_account_daily_pnl"}}}}}
	b, _ := json.Marshal(d)
	v := ledger.CycleReviewVersion{AccountID: "a", VersionID: "v1", DateFrom: d.DateFrom, ObservedThrough: d.ObservedThrough, Payload: b, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256(b))}
	base := "/v1/accounts/a/performance/cycle-reviews"
	for _, tc := range []struct {
		name, path, method string
		code               int
		err                error
	}{
		{"range", base + "?date_from=20261012&date_to=20261013", "GET", 200, nil},
		{"empty range", base + "?date_from=20261013&date_to=20261014", "GET", 200, nil},
		{"missing version", base + "?date_from=20261012&date_to=20261013&version_id=v2", "GET", 404, sql.ErrNoRows},
		{"no publication", base + "?date_from=20261012&date_to=20261013", "GET", 200, sql.ErrNoRows},
		{"failure", base + "?date_from=20261012&date_to=20261013", "GET", 503, errors.New("private-secret")},
		{"missing date", base, "GET", 400, nil}, {"reversed", base + "?date_from=20261014&date_to=20261012", "GET", 400, nil},
		{"duplicate", base + "?date_from=20261012&date_from=20261013&date_to=20261014", "GET", 400, nil},
		{"no write", base, "POST", 405, nil}, {"unknown account", strings.Replace(base, "/a/", "/b/", 1), "GET", 404, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Accounts = []config.AccountRouteConfig{{AccountID: "a"}}
			h := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{CycleReviews: &fakeCycleReviews{value: v, err: tc.err}})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.code {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "private-secret") {
				t.Fatal("private error exposed")
			}
			if tc.name == "empty range" && !strings.Contains(rec.Body.String(), `"cycles":[]`) {
				t.Fatal("wrong date basis", rec.Body.String())
			}
		})
	}
}
