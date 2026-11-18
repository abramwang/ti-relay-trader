package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/redisstream"
	"time"
)

type terminalPolicyStub struct{ futuresExecutionStub }

func (s *terminalPolicyStub) TerminalExecution(futures.TerminalAccount, time.Time) futures.TerminalExecutionView {
	return futures.TerminalExecutionView{SubmitAuthorized: true, CancelAuthorized: true, SubmitEnabled: true, CancelEnabled: true, ValidUntil: time.Now().Add(time.Second)}
}

func TestTerminalExecutionRequiresGlobalAndService(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, available := range []bool{false, true} {
			cfg := config.Default()
			cfg.Service.Environment = config.EnvironmentTest
			cfg.FuturesTerminal.ExecutionEnabled = enabled
			deps := Dependencies{FuturesTerminal: &terminalStub{}}
			if available {
				deps.FuturesOrders = &terminalPolicyStub{}
			}
			w := httptest.NewRecorder()
			NewWithDependencies(cfg, nil, deps).ServeHTTP(w, httptest.NewRequest("GET", "/v1/futures/terminal", nil))
			var response struct {
				Data struct {
					Accounts       []futures.TerminalAccount `json:"accounts"`
					TradingEnabled bool                      `json:"trading_enabled"`
					CancelEnabled  bool                      `json:"cancel_enabled"`
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			want := enabled && available
			if w.Code != 200 || len(response.Data.Accounts) != 1 || response.Data.TradingEnabled != want || response.Data.CancelEnabled != want || response.Data.Accounts[0].TradingEnabled != want || (response.Data.Accounts[0].Execution != nil) != want {
				t.Fatal(w.Body.String())
			}
		}
	}
}

type terminalStub struct{ calls int }

func (s *terminalStub) Snapshot(context.Context) []futures.TerminalAccount {
	s.calls++
	return []futures.TerminalAccount{{AccountID: "990000000001", ConnectionState: "stopped"}}
}

type heartbeatTerminalStub struct {
	terminalStub
	heartbeatCalls int
}

func (s *heartbeatTerminalStub) Heartbeats(context.Context) []futures.TerminalAccount {
	s.heartbeatCalls++
	return []futures.TerminalAccount{{AccountID: "300052141", Alias: "CTP", ConnectionState: "unavailable", ConnectionError: "epoch_missing_or_changed"}}
}

func TestOperationsFuturesHeartbeatIsolation(t *testing.T) {
	for _, env := range []config.Environment{config.EnvironmentTest, config.EnvironmentProduction} {
		for _, available := range []bool{true, false} {
			cfg := config.Default()
			cfg.Service.Environment = env
			cfg.FuturesTerminal.ManifestFile = "configured-manifest.json"
			stub := &heartbeatTerminalStub{}
			deps := Dependencies{Operations: &fakeOperationsService{snapshot: redisstream.RuntimeSnapshot{
				Gateways: []redisstream.GatewayRuntimeStatus{{AccountID: "00030484", Status: "online"}},
			}}}
			if available {
				deps.FuturesTerminal = stub
			}
			h := NewWithDependencies(cfg, nil, deps)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/operations/status", nil))
			var response struct {
				Data struct {
					Gateways []redisstream.GatewayRuntimeStatus `json:"gateways"`
					Futures  []futures.TerminalAccount          `json:"futures_accounts"`
					Error    string                             `json:"futures_error"`
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			want := 0
			if env == config.EnvironmentTest && available {
				want = 1
			}
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || len(response.Data.Gateways) != 1 || len(response.Data.Futures) != want || stub.heartbeatCalls != want || stub.calls != 0 {
				t.Fatal(w.Body.String(), stub)
			}
			if (response.Data.Error != "") != (env == config.EnvironmentTest && !available) {
				t.Fatal(w.Body.String())
			}
			if want == 1 && (response.Data.Futures[0].TradingEnabled || response.Data.Futures[0].ConnectionError != "epoch_missing_or_changed") {
				t.Fatal(w.Body.String())
			}
		}
	}
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
