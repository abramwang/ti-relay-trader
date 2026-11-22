package market

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
)

func TestFuturesDCECatalogDoesNotEnableUnavailableQuotes(t *testing.T) {
	for _, securityID := range []string{"M2701.DCE", "L2701F.DCE"} {
		t.Run(securityID, func(t *testing.T) {
			catalog := `{"data":[],"meta":{"exchange_coverage":{"DCE":{"status":"production_ready"}}}}`
			unavailable := `{"error":{"code":"source_unavailable","details":{"process_running":"false","source_status":"stopped"}},"meta":{"schema_version":"futures_market_snapshot.v1","data_scope":"realtime","count":0}}`
			calls := make(map[string]int)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls[r.URL.Path]++
				query := r.URL.Query()
				if r.Method != http.MethodGet || query.Get("security_id") != securityID || query.Has("allow_stale") || query.Get("data_scope") == "historical" {
					t.Error("selector changed or stale fallback requested", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case futuresInstrumentsPath:
					io.WriteString(w, catalog)
				case futuresSnapshotsPath, futuresSnapshotStreamPath:
					if r.URL.Path == futuresSnapshotStreamPath && (query.Get("include_existing") != "true" || r.Header.Get("Accept") != "text/event-stream") {
						t.Error("unexpected SSE request", r.URL)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					io.WriteString(w, unavailable)
				default:
					t.Error("unexpected request", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewMeridianClient(config.MarketConfig{BaseURL: server.URL, TimeoutSeconds: 1})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if raw, err := client.FuturesPriceCatalog(ctx, securityID); err != nil || string(raw) != catalog {
				t.Fatal("catalog transport failed", string(raw), err)
			}
			if raw, err := client.FuturesPriceSnapshot(ctx, securityID); err == nil || len(raw) != 0 {
				t.Fatal("unavailable snapshot accepted as price evidence", string(raw), err)
			}
			response, err := client.FuturesSnapshotStream(ctx, url.Values{"security_id": {securityID}})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Content-Type") != "application/json" || string(raw) != unavailable {
				t.Fatal("SSE preflight failure was not preserved", response.StatusCode, string(raw), err)
			}
			for _, path := range []string{futuresInstrumentsPath, futuresSnapshotsPath, futuresSnapshotStreamPath} {
				if calls[path] != 1 {
					t.Fatal("unexpected retry or fallback", path, calls[path])
				}
			}
		})
	}
}

func TestFuturesPriceOriginalBody(t *testing.T) {
	body := " {\n\"data\":[{\"price\":3000.000000001}]}\n"
	for _, scenario := range []string{"snapshot", "catalog", "redirect", "html", "status", "too_large", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Query().Get("security_id") != "RB2701.SHFE" || r.URL.Query().Has("allow_stale") {
					t.Error("unexpected request", r.URL)
				}
				if scenario == "catalog" && (r.URL.Path != futuresInstrumentsPath || r.URL.Query().Get("limit") != "2") {
					t.Error("wrong catalog query")
				}
				if scenario == "redirect" {
					http.Redirect(w, r, "/unexpected", http.StatusFound)
					return
				}
				if scenario == "html" {
					w.Header().Set("Content-Type", "text/html")
					io.WriteString(w, "<html>error</html>")
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if scenario == "status" {
					w.WriteHeader(503)
				}
				if scenario == "too_large" {
					w.(http.Flusher).Flush()
					io.WriteString(w, strings.Repeat(" ", 32769))
					return
				}
				io.WriteString(w, body)
			}))
			defer srv.Close()
			client, err := NewMeridianClient(config.MarketConfig{BaseURL: srv.URL, TimeoutSeconds: 1})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			var raw []byte
			if scenario == "catalog" {
				raw, err = client.FuturesPriceCatalog(ctx, "RB2701.SHFE")
			} else {
				raw, err = client.FuturesPriceSnapshot(ctx, "RB2701.SHFE")
			}
			if scenario == "snapshot" || scenario == "catalog" {
				if err != nil || string(raw) != body {
					t.Fatal("original bytes changed", string(raw), err)
				}
			} else if err == nil {
				t.Fatal("invalid upstream accepted")
			}
			if calls > 1 {
				t.Fatal("redirect/retry occurred", calls)
			}
		})
	}
}
