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
	"os"
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

func TestAttributionReviewReadOnlyFiltering(t *testing.T) {
	b, err := os.ReadFile("../../outputs/recovery/fuying13_cycle_review_20261115/profit-v13.json")
	if os.IsNotExist(err) {
		t.Skip("private evidence absent")
	}
	if err != nil {
		t.Fatal(err)
	}
	var s performance.CycleReviewSource
	_ = json.Unmarshal(b, &s)
	b, err = os.ReadFile("../../outputs/recovery/fuying13_daily_attribution_20261115/review-v6.json")
	if os.IsNotExist(err) {
		t.Skip("private evidence absent")
	}
	if err != nil {
		t.Fatal(err)
	}
	var a performance.AttributionReview
	_ = json.Unmarshal(b, &a)
	a.SourceSHA = strings.Repeat("a", 64)
	s.Attribution = &a
	v, err := performance.BuildCycleReviewVersion(s, "v5", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{{AccountID: s.AccountID}}
	h := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{CycleReviews: &fakeCycleReviews{value: v}})
	for _, date := range []string{"20261103", "20261113", "20261114"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/accounts/"+s.AccountID+"/performance/cycle-reviews?date_from="+date+"&date_to="+date, nil))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var response struct {
			Data struct {
				Attribution *performance.AttributionReview `json:"attribution_review"`
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		x := response.Data.Attribution
		if x == nil || x.Publishable || x.Final {
			t.Fatal("lost scope")
		}
		want := 1
		if date == "20261114" {
			want = 0
		}
		if len(x.Days) != want {
			t.Fatal("date filter", len(x.Days))
		}
		for _, p := range x.Parts {
			if strings.ReplaceAll(p.Date, "-", "") != date {
				t.Fatal("part leaked across dates")
			}
		}
		if date == "20261113" && len(x.Statements.Days) != 1 {
			t.Fatal("statement not filtered")
		}
	}
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

func TestCycleDailyReviewRouteFiltering(t *testing.T) {
	leg := performance.ETFCycleCashLeg{SecurityID: "510310.SH", ObservedETFCashLeg: performance.ObservedETFCashLeg{AccountID: "a", FlowID: "flow", SourceDate: "2026-10-12", PostedDate: "2026-10-13", AmountCents: 100, SourceEvidence: performance.InferredETFCashSource, EvidenceRef: "synthetic"}}
	cash, err := performance.ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12", "2026-10-13", "2026-10-14"}, []performance.ETFCycleCashLeg{leg})
	if err != nil {
		t.Fatal(err)
	}
	s := performance.CycleReviewSource{AccountID: "a", Cycles: cash, CashLegs: []performance.ETFCycleCashLeg{leg}}
	s.Profit.Profits, err = performance.CalculateETFCycleProfits(cash, []performance.ETFCycleTradingEvidence{{AccountID: "a", CycleID: "2026-10-12/510310.SH", AcquisitionDate: "2026-10-12", AcquisitionQuantity: 100, RedemptionQuantity: 100, AcquisitionGrossCents: 1000, ComponentSaleCents: 1000, FeeScope: "ordinary_buys_and_component_sales_only", FeeBasis: "broker_cash_reconciled", EvidenceRefs: []string{"synthetic"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range cash.Days {
		s.AccountDays = append(s.AccountDays, performance.CycleAccountDay{TradeDate: day.TradeDate, PublishedPnLCents: day.AdjustmentCents, TimingAdjustmentCents: day.AdjustmentCents, NoObservedTimingOrOther: len(day.ActiveCycleIDs) == 0})
	}
	v, err := performance.BuildCycleReviewVersion(s, "v4", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		date        string
		days, flows int
	}{{"20261013", 1, 1}, {"20261014", 1, 0}, {"20261015", 0, 0}} {
		cfg := config.Default()
		cfg.Accounts = []config.AccountRouteConfig{{AccountID: "a"}}
		h := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{CycleReviews: &fakeCycleReviews{value: v}})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/accounts/a/performance/cycle-reviews?date_from="+tc.date+"&date_to="+tc.date, nil))
		var reply struct {
			Data struct {
				Cycles []performance.CycleReviewRow  `json:"cycles"`
				Daily  *performance.CycleDailyReview `json:"daily_review"`
				Basis  string                        `json:"daily_date_basis"`
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || len(reply.Data.Cycles) != 0 || reply.Data.Daily == nil || len(reply.Data.Daily.Days) != tc.days || len(reply.Data.Daily.InferredLegs) != tc.flows || reply.Data.Basis != "trade_date" {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if tc.date == "20261013" && reply.Data.Daily.Days[0].Timing.Inferred.OpenReceivableCents != 100 {
			t.Fatal("date filtering lost opening exposure")
		}
	}
}
