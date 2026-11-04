package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"

	"ti-relay-trader/internal/config"
)

func TestFuturesInstrumentsPreservesCatalogAndErrors(t *testing.T) {
	cases := []struct {
		name   string
		query  url.Values
		status int
		body   string
	}{
		{"all", url.Values{}, 200, `{"data":[],"meta":{"publication_snapshot_id":"p1","total_count":541,"next_cursor":"opaque+/=","exchange_coverage":{"SHFE":{"status":"source_unavailable"}}}}`},
		{"native", url.Values{"source_instrument_id": {"sc2610"}, "exchange_id": {"INE"}}, 200, `{"data":[{"security_id":"SC2610.INE","source_instrument_id":"sc2610","exchange_id":"INE","price_tick":0.1,"volume_multiple":1000}],"meta":{"schema_version":"futures_instrument.v1","publication_snapshot_id":"synthetic-only"}}`},
		// Market metadata need not have Trader query evidence or broker order limits.
		{"official_market_without_trader", url.Values{"exchange_id": {"SHFE"}}, 200, `{"data":[{"security_id":"RB2611.SHFE","source_instrument_id":"rb2611","exchange_id":"SHFE","product_id":"RB","price_tick":1,"volume_multiple":10,"source":"shfe_official_business_parameters","lifecycle_quality":"shfe_official_contract_parameters","min_limit_order_volume":null,"max_limit_order_volume":null}],"meta":{"schema_version":"futures_instrument.v1","publication_snapshot_id":"synthetic-official-only","exchange_coverage":{"SHFE":{"status":"production_ready"}}}}`},
		{"page", url.Values{"security_ids": {"IF2612.CFFEX,IH2612.CFFEX"}, "exchange_ids": {"CFFEX"}, "product_ids": {"IF,IH"}, "trading_statuses": {"active_observed"}, "source_instrument_ids": {"IF2612,IH2612"}, "limit": {"2"}, "cursor": {"opaque+/="}}, 200, `{"data":[],"meta":{"publication_snapshot_id":"p1","next_cursor":null}}`},
		{"expired", url.Values{"cursor": {"old"}}, 409, `{"error":{"code":"cursor_expired","message":"publication changed"},"meta":{"publication_snapshot_id":"p2"}}`},
		{"ambiguous", url.Values{"source_instrument_id": {"ambiguous"}}, 409, `{"error":{"code":"conflict","message":"exchange required"}}`},
		{"unknown", url.Values{"security_id": {"UNKNOWN.CFFEX"}}, 404, `{"error":{"code":"not_found","message":"unknown contract"}}`},
		{"unpublished", url.Values{"source_instrument_id": {"rb2612"}, "exchange_id": {"SHFE"}}, 503, `{"error":{"code":"source_unavailable","details":{"coverage_status":"source_unavailable","exchange_id":"SHFE"}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requests atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/futures/instruments" || r.URL.Query().Encode() != c.query.Encode() {
					t.Errorf("changed query: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			}))
			defer upstream.Close()
			cfg := config.Default()
			cfg.Market.BaseURL = upstream.URL
			handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/meridian/futures/instruments?"+c.query.Encode(), nil))
			if rec.Code != c.status || requests.Load() != 1 {
				t.Fatalf("status=%d requests=%d body=%s", rec.Code, requests.Load(), rec.Body.String())
			}
			var got struct {
				Data  any `json:"data"`
				Error struct {
					Details any `json:"detail"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal([]byte(c.body), &want); err != nil {
				t.Fatal(err)
			}
			actual := got.Data
			if c.status >= 400 {
				actual = got.Error.Details
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("upstream metadata/error lost: %s", rec.Body.String())
			}
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/meridian/futures/instruments", nil))
			if rec.Code != http.StatusMethodNotAllowed || requests.Load() != 1 {
				t.Fatal("write forwarded to read-only catalog")
			}
		})
	}
}
