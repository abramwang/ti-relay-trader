package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/trading"
)

func TestFuturesPricePreview(t *testing.T) {
	for _, scenario := range []string{"test", "production", "dce", "bad_query", "duplicate_selector", "bad_date", "bad_encoding", "raw_quote_injection", "source_override", "post", "catalog_unavailable", "catalog_invalid", "snapshot_html", "stale", "duplicate_contract"} {
		t.Run(scenario, func(t *testing.T) {
			catalog, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("../../testdata/fixtures/meridian_futures_price.synthetic.json")
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if json.Unmarshal(raw, &payload) != nil {
				t.Fatal("fixture")
			}
			row := payload["data"].([]any)[0].(map[string]any)
			security := "RB2701.SHFE"
			if scenario == "dce" {
				security = "L2612F.DCE"
				row["security_id"], row["source_instrument_id"], row["exchange_id"] = security, "l2612F", "DCE"
				var c map[string]any
				if err := json.Unmarshal(catalog, &c); err != nil {
					t.Fatal(err)
				}
				item := c["data"].([]any)[0].(map[string]any)
				item["security_id"], item["source_instrument_id"], item["exchange_id"], item["ctp_product_id"] = security, "l2612F", "DCE", nil
				c["meta"].(map[string]any)["exchange_coverage"] = map[string]any{"DCE": map[string]any{"status": "production_ready"}}
				catalog, _ = json.Marshal(c)
			}
			now := time.Now()
			row["exchange_timestamp"] = now.Add(-100 * time.Millisecond).Format(time.RFC3339Nano)
			row["received_at"] = now.Format(time.RFC3339Nano)
			if scenario == "stale" {
				row["exchange_timestamp"] = now.Add(-10 * time.Second).Format(time.RFC3339Nano)
			}
			if scenario == "duplicate_contract" {
				payload["data"] = []any{row, row}
				payload["meta"].(map[string]any)["count"] = 2
			}
			raw, _ = json.MarshalIndent(payload, "", "  ")
			var requests []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.URL.Query().Get("security_id") != security || r.URL.Query().Has("allow_stale") {
					t.Error("unexpected upstream query", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/futures/instruments":
					if scenario == "catalog_unavailable" {
						w.WriteHeader(503)
						io.WriteString(w, `{"error":"private upstream details"}`)
						return
					}
					if scenario == "catalog_invalid" {
						io.WriteString(w, `{"data":[],"meta":{}}`)
						return
					}
					w.Write(catalog)
				case "/v1/futures/snapshots":
					if scenario == "snapshot_html" {
						w.Header().Set("Content-Type", "text/html")
						io.WriteString(w, "<html>private details</html>")
						return
					}
					w.Write(raw)
				default:
					t.Error("unexpected upstream path", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			cfg := config.Default()
			cfg.Market.BaseURL = upstream.URL
			if scenario == "production" {
				cfg.Service.Environment = config.EnvironmentProduction
			}
			handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{})
			path := "/v1/futures/price-preview?security_id=" + security + "&trade_date=20261117"
			method := http.MethodGet
			switch scenario {
			case "bad_query":
				path += "&allow_stale=true"
			case "duplicate_selector":
				path += "&security_id=AU2612.SHFE"
			case "bad_date":
				path = strings.ReplaceAll(path, "20261117", "20260230")
			case "bad_encoding":
				path += "&ignored=%zz"
			case "raw_quote_injection":
				path += "&meridian_snapshot=anything"
			case "source_override":
				path += "&price_source=oc_test_native_facts.v1"
			case "post":
				method = http.MethodPost
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("preview cacheable")
			}
			if scenario != "test" && scenario != "production" && scenario != "dce" {
				want := 503
				if scenario == "post" {
					want = 405
				} else if oneOfPriceScenario(scenario, "bad_query", "duplicate_selector", "bad_date", "bad_encoding", "raw_quote_injection", "source_override") {
					want = 400
				}
				if rec.Code != want || strings.Contains(rec.Body.String(), "private") {
					t.Fatal(rec.Code, rec.Body.String())
				}
				if want < 500 && len(requests) != 0 {
					t.Fatal("invalid request reached upstream")
				}
				if strings.HasPrefix(scenario, "catalog_") && len(requests) != 1 {
					t.Fatal("queried snapshot after invalid catalog")
				}
				return
			}
			var envelope struct {
				OK   bool `json:"ok"`
				Data struct {
					Mode        string                           `json:"mode"`
					Authorized  bool                             `json:"business_authorized"`
					Published   bool                             `json:"command_published"`
					Supported   bool                             `json:"publication_supported"`
					CatalogSHA  string                           `json:"catalog_body_sha256"`
					Preparation futures.MeridianPricePreparation `json:"preparation"`
				} `json:"data"`
			}
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil || !envelope.OK || envelope.Data.Mode != "price_preview_only" || envelope.Data.Authorized || envelope.Data.Published || envelope.Data.Supported || envelope.Data.Preparation.BusinessAuthorized {
				t.Fatal(rec.Code, rec.Body.String())
			}
			if string(envelope.Data.Preparation.Market.MeridianSnapshot) != string(raw) {
				t.Fatal("snapshot original bytes changed")
			}
			hash := sha256.Sum256(catalog)
			if envelope.Data.CatalogSHA != hex.EncodeToString(hash[:]) || strings.Join(requests, ",") != "/v1/futures/instruments,/v1/futures/snapshots" {
				t.Fatal("wrong input provenance", requests)
			}
		})
	}
	cat := trading.Catalog()
	found := false
	for _, capability := range cat.Capabilities {
		if capability == trading.CapabilityFuturesPricePreview {
			found = true
		}
	}
	if !found {
		t.Fatal("missing capability")
	}
}

func oneOfPriceScenario(value string, values ...string) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}
