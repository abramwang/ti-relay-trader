package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
)

type previewAccountReader struct {
	accounts []futures.TerminalAccount
	calls    int
}

func (s *previewAccountReader) Snapshot(context.Context) []futures.TerminalAccount {
	panic("preview must not load historical ledgers")
}
func (s *previewAccountReader) Heartbeats(context.Context) []futures.TerminalAccount {
	s.calls++
	return s.accounts
}

func TestFuturesOrderPreview(t *testing.T) {
	for _, scenario := range []string{"ready", "account_read_only", "execution_manifest", "execution_disabled", "route_mismatch", "broker_offline", "stale_heartbeat", "day_mismatch", "epoch_missing", "stopped", "latched", "unknown_account", "duplicate_account", "production", "off_tick", "above_limit", "outside_scope", "injection", "query_override", "quote_stale", "get"} {
		t.Run(scenario, func(t *testing.T) {
			catalog, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("../../testdata/fixtures/meridian_futures_price.synthetic.json")
			if err != nil {
				t.Fatal(err)
			}
			var quote map[string]any
			if err := json.Unmarshal(raw, &quote); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			row := quote["data"].([]any)[0].(map[string]any)
			row["received_at"] = now.Format(time.RFC3339Nano)
			row["exchange_timestamp"] = now.Add(-100 * time.Millisecond).Format(time.RFC3339Nano)
			if scenario == "quote_stale" {
				row["received_at"] = now.Add(-time.Minute).Format(time.RFC3339Nano)
			}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" {
					t.Error("upstream write")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/futures/instruments":
					w.Write(catalog)
				case "/v1/futures/snapshots":
					json.NewEncoder(w).Encode(quote)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
				}
			}))
			defer upstream.Close()
			day, generation, clear := "20261117", int64(1), true
			hb := &futures.AdvertisedHeartbeat{SessionID: "new-session", TradeDate: &day, ConnectionGeneration: &generation, ProducedAt: now, RedisReady: true, BrokerReady: true, SettlementConfirmed: true, RecoveryClear: &clear, AcceptingTradeCommands: true}
			a := futures.TerminalAccount{AccountID: "300052141", Route: futures.QueryRoute{Env: "test", BrokerID: "ctp_4900", GatewayID: "gateway", AccountID: "300052141"}, Heartbeat: hb}
			cfg := config.Default()
			cfg.Service.Environment = config.EnvironmentTest
			cfg.Market.BaseURL = upstream.URL
			cfg.Accounts = []config.AccountRouteConfig{{AccountID: a.AccountID, BrokerID: a.Route.BrokerID, GatewayID: a.Route.GatewayID, Enabled: true, TradingEnabled: true}}
			if scenario == "account_read_only" {
				cfg.Accounts[0].TradingEnabled = false
			}
			if scenario == "route_mismatch" {
				cfg.Accounts[0].GatewayID = "different"
			}
			if scenario == "broker_offline" {
				hb.AcceptingTradeCommands = false
			}
			if scenario == "stopped" {
				hb.State = "STOPPED"
			}
			if scenario == "latched" {
				hb.RuntimeBlockedReason = "journal_unavailable"
			}
			if scenario == "stale_heartbeat" {
				hb.ProducedAt = now.Add(-time.Minute)
			}
			if scenario == "day_mismatch" {
				day = "20261116"
			}
			if scenario == "epoch_missing" {
				a.ConnectionError = "epoch_missing_or_changed"
			}
			if scenario == "production" {
				cfg.Service.Environment = config.EnvironmentProduction
			}
			stub := &previewAccountReader{accounts: []futures.TerminalAccount{a}}
			if scenario == "unknown_account" {
				stub.accounts = nil
			}
			if scenario == "duplicate_account" {
				stub.accounts = append(stub.accounts, a)
			}
			body := `{"account_id":"300052141","security_id":"RB2701.SHFE","trade_date":"20261117","trade_side":"B","offset_type":"O","hedge_type":"speculation","qty":1,"price":"3000.000"}`
			if scenario == "off_tick" {
				body = strings.Replace(body, "3000.000", "3000.5", 1)
			}
			if scenario == "above_limit" {
				body = strings.Replace(body, "3000.000", "9999.000", 1)
			}
			if scenario == "outside_scope" {
				body = strings.Replace(body, `"qty":1`, `"qty":2`, 1)
			}
			if scenario == "injection" {
				body = strings.Replace(body, `"qty":1`, `"qty":1,"account_permission_enabled":true`, 1)
			}
			path, method := "/v1/futures/orders/preview", "POST"
			if scenario == "query_override" {
				path += "?price_source=oc_test_native_facts.v1"
			}
			if scenario == "get" {
				method = "GET"
			}
			w := httptest.NewRecorder()
			deps := Dependencies{FuturesTerminal: stub}
			if scenario == "execution_manifest" || scenario == "execution_disabled" {
				cfg.Accounts = nil
				cfg.FuturesTerminal.ExecutionEnabled = scenario == "execution_manifest"
				deps.FuturesOrders = &terminalPolicyStub{}
			}
			handler := NewWithDependencies(cfg, nil, deps)
			handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
			want := 200
			switch scenario {
			case "production", "duplicate_account", "quote_stale":
				want = 503
			case "unknown_account":
				want = 404
			case "injection", "query_override":
				want = 400
			case "get":
				want = 405
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body.String())
			}
			if want != 200 {
				if scenario != "quote_stale" && calls != 0 {
					t.Fatal("unexpected market access")
				}
				if scenario == "production" && stub.calls != 0 {
					t.Fatal("production read TEST scope")
				}
				return
			}
			var response struct {
				Data futuresOrderPreview `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			v := response.Data
			if v.CanSubmit || v.CommandPublished || v.Quote.BusinessAuthorized || slices.Contains(v.Blockers, "publication_not_connected") != (scenario != "execution_manifest") || calls != 2 {
				t.Fatal(w.Body.String())
			}
			wantPermission := scenario != "account_read_only" && scenario != "route_mismatch" && scenario != "execution_disabled"
			wantBroker := !slices.Contains([]string{"broker_offline", "stale_heartbeat", "day_mismatch", "epoch_missing", "stopped", "latched"}, scenario)
			if v.AccountPermissionEnabled != wantPermission || v.BrokerAcceptingSubmit != wantBroker || v.PriceValid != (scenario != "off_tick") {
				t.Fatal(w.Body.String())
			}
			if scenario == "outside_scope" && !slices.Contains(v.Blockers, "outside_current_test_scope") {
				t.Fatal(w.Body.String())
			}
			var original futures.OrderPreviewRequest
			json.NewDecoder(strings.NewReader(body)).Decode(&original)
			if v.Intent != original {
				t.Fatal("intent modified")
			}
		})
	}
}
