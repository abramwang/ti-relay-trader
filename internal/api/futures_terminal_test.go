package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
)

type terminalStub struct{ calls int }

func (s *terminalStub) Snapshot(context.Context) []futures.TerminalAccount {
	s.calls++
	return []futures.TerminalAccount{{AccountID: "990000000001", ConnectionState: "stopped"}}
}

func TestFuturesTerminalReadOnlyAndEnvironmentIsolation(t *testing.T) {
	for _, env := range []config.Environment{config.EnvironmentTest, config.EnvironmentProduction} {
		cfg := config.Default()
		cfg.Service.Environment = env
		stub := &terminalStub{}
		h := NewWithDependencies(cfg, nil, Dependencies{FuturesTerminal: stub})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/futures/terminal", nil))
		var response struct {
			Data struct {
				Accounts       []futures.TerminalAccount
				TradingEnabled bool `json:"trading_enabled"`
				CancelEnabled  bool `json:"cancel_enabled"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		want := 0
		if env == config.EnvironmentTest {
			want = 1
		}
		if w.Code != 200 || len(response.Data.Accounts) != want || stub.calls != want || response.Data.TradingEnabled || response.Data.CancelEnabled || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Body.String(), stub.calls)
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, "/v1/futures/terminal", nil))
			if w.Code != http.StatusMethodNotAllowed || stub.calls != want {
				t.Fatal("write accepted", method)
			}
		}
	}
}
