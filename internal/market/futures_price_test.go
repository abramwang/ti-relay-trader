package market

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
)

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
