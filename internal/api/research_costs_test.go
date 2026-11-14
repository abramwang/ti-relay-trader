package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/performance"
)

type fakeResearchCostReader struct {
	calls      int
	err        error
	wrongScope bool
}

func (f *fakeResearchCostReader) ListVersions(_ context.Context, account string) ([]performance.ResearchCostVersionInfo, error) {
	f.calls++
	return []performance.ResearchCostVersionInfo{}, f.err
}

func (f *fakeResearchCostReader) Compare(_ context.Context, account, version string) (performance.ResearchCostComparison, error) {
	f.calls++
	if f.wrongScope {
		account = "another-account"
	}
	return performance.ResearchCostComparison{AccountID: account, VersionID: version, ResearchOnly: true, AnchorAndAllocationsMatch: false}, f.err
}

func TestResearchCostRoutes(t *testing.T) {
	const base = "/v1/accounts/research-account/performance/"
	for _, tc := range []struct {
		name, method, path  string
		want, calls         int
		err                 error
		missing, wrongScope bool
	}{
		{name: "empty versions", method: "GET", path: base + "research-cost-versions", want: 200, calls: 1},
		{name: "stale evidence still readable", method: "GET", path: base + "research-cost-comparison?version_id=v1", want: 200, calls: 1},
		{name: "explicit version", method: "GET", path: base + "research-cost-comparison", want: 400},
		{name: "duplicate version", method: "GET", path: base + "research-cost-comparison?version_id=v1&version_id=v2", want: 400},
		{name: "unknown query", method: "GET", path: base + "research-cost-comparison?version_id=v1&publish=true", want: 400},
		{name: "list query", method: "GET", path: base + "research-cost-versions?account_id=other", want: 400},
		{name: "nul version", method: "GET", path: base + "research-cost-comparison?version_id=a%00b", want: 400},
		{name: "trim version", method: "GET", path: base + "research-cost-comparison?version_id=%20v1", want: 400},
		{name: "unknown account", method: "GET", path: "/v1/accounts/other/performance/research-cost-versions", want: 404},
		{name: "no write", method: "POST", path: base + "research-cost-comparison?version_id=v1", want: 405},
		{name: "wrong path", method: "GET", path: base + "research-cost-comparison/extra?version_id=v1", want: 404},
		{name: "not installed", method: "GET", path: base + "research-cost-versions", want: 503, missing: true},
		{name: "not found", method: "GET", path: base + "research-cost-comparison?version_id=v1", want: 404, calls: 1, err: sql.ErrNoRows},
		{name: "error redacted", method: "GET", path: base + "research-cost-comparison?version_id=v1", want: 503, calls: 1, err: errors.New("private-database-error")},
		{name: "scope mismatch", method: "GET", path: base + "research-cost-comparison?version_id=v1", want: 503, calls: 1, wrongScope: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Accounts = []config.AccountRouteConfig{{AccountID: "research-account"}}
			fake := &fakeResearchCostReader{err: tc.err, wrongScope: tc.wrongScope}
			deps := Dependencies{}
			if !tc.missing {
				deps.ResearchCosts = fake
			}
			handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.want || fake.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", rec.Code, fake.calls, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "private-database-error") {
				t.Fatal("leaked internal error")
			}
			if rec.Code == 200 && rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("research result must not be cached")
			}
			if tc.name == "empty versions" && !strings.Contains(rec.Body.String(), `"versions":[]`) {
				t.Fatal("missing explicit empty array")
			}
			if tc.name == "stale evidence still readable" && !strings.Contains(rec.Body.String(), `"anchor_and_allocations_match":false`) {
				t.Fatal("stale evidence hidden")
			}
		})
	}
}
