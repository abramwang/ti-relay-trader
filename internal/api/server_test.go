package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/events"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func statusConfigWithTradingDay(t *testing.T, isTradingDay bool) config.Config {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metadata/trading-day" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("date") == "" {
			t.Fatal("missing trading-day date query")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"date":                             20260619,
				"is_trading_day":                   isTradingDay,
				"previous_or_current_trading_date": 20260618,
			},
			"meta": map[string]any{"schema_version": "metadata_trading_day_status.v1"},
		})
	}))
	t.Cleanup(upstream.Close)
	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	cfg.Market.TimeoutSeconds = 1
	return cfg
}

func TestHealthzEnvelope(t *testing.T) {
	handler := New(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "req-health")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var envelope httpx.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.OK {
		t.Fatal("expected ok envelope")
	}
	if envelope.RequestID != "req-health" {
		t.Fatalf("request_id = %q", envelope.RequestID)
	}
}

func TestStatusIncludesDependencyHealth(t *testing.T) {
	cfg := statusConfigWithTradingDay(t, false)
	cfg.Service.Mode = config.ModeDocs
	cfg.Database.DSN = "postgres://configured"
	cfg.Redis.URL = "redis://configured"
	cfg.Jobs = map[string]config.JobConfig{
		"pre_open_init":         {Enabled: true, Schedule: "1 9 * * 1-5"},
		"post_close_settlement": {Enabled: true, Schedule: "30 15 * * 1-5"},
	}
	cfg.Accounts = []config.AccountRouteConfig{
		{AccountID: "acct-1", BrokerID: "huaxin", GatewayID: "gw-1", StreamPrefix: "relay:prod:v1:huaxin:gw-1", Enabled: true, TradingEnabled: true},
		{AccountID: "acct-2", BrokerID: "huaxin", GatewayID: "gw-2", StreamPrefix: "relay:prod:v1:huaxin:gw-2", Enabled: true, Simulated: true},
	}
	dbPinged := false
	redisPinged := false
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{},
		Jobs: &fakeJobRunStore{runs: []ledger.JobRun{{
			RunID:           "pre-open-1",
			JobName:         "pre_open_init",
			TargetTradeDate: "2026-06-14",
			Status:          "succeeded",
		}}},
		DatabasePing: func(_ context.Context) error {
			dbPinged = true
			return nil
		},
		RedisPing: func(_ context.Context) error {
			redisPinged = true
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var envelope struct {
		OK   bool       `json:"ok"`
		Data StatusView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !envelope.OK || envelope.Data.Status != "ok" || envelope.Data.Mode != string(config.ModeDocs) {
		t.Fatalf("status view = %#v", envelope.Data)
	}
	if envelope.Data.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q, want Asia/Shanghai", envelope.Data.Timezone)
	}
	if envelope.Data.Environment != string(config.EnvironmentTest) {
		t.Fatalf("environment = %q, want test", envelope.Data.Environment)
	}
	if !dbPinged || !redisPinged {
		t.Fatalf("dependency pings db=%v redis=%v", dbPinged, redisPinged)
	}
	if envelope.Data.Accounts.Configured != 2 || envelope.Data.Accounts.Enabled != 2 || envelope.Data.Accounts.TradingEnabled != 1 || envelope.Data.Accounts.Simulated != 1 {
		t.Fatalf("account summary = %#v", envelope.Data.Accounts)
	}
	if envelope.Data.Dependencies["database"].Status != "ok" || envelope.Data.Dependencies["redis"].Status != "ok" {
		t.Fatalf("dependencies = %#v", envelope.Data.Dependencies)
	}
	if envelope.Data.TradingDay.Timezone != "Asia/Shanghai" || envelope.Data.TradingDay.Phase == "" {
		t.Fatalf("trading day = %#v", envelope.Data.TradingDay)
	}
	if envelope.Data.TradingDay.IsTradingDay == nil || *envelope.Data.TradingDay.IsTradingDay {
		t.Fatalf("trading day is_trading_day = %#v, want false", envelope.Data.TradingDay.IsTradingDay)
	}
	if envelope.Data.TradingDay.PreviousOrCurrentTradingDate != "20260618" || envelope.Data.TradingDay.Source != "meridian" {
		t.Fatalf("trading day meridian fields = %#v", envelope.Data.TradingDay)
	}
	if envelope.Data.JobRuns["pre_open_init"].RunID != "pre-open-1" {
		t.Fatalf("job runs = %#v", envelope.Data.JobRuns)
	}
	if envelope.Data.Jobs["pre_open_init"].ExpectedTime != "09:01" || envelope.Data.Jobs["post_close_settlement"].ExpectedTime != "15:30" {
		t.Fatalf("job schedules = %#v", envelope.Data.Jobs)
	}
}

func TestStatusDegradedAndDoesNotLeakDependencyErrors(t *testing.T) {
	cfg := statusConfigWithTradingDay(t, true)
	cfg.Database.DSN = "postgres://configured"
	cfg.Redis.URL = "redis://configured"
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		DatabasePing: func(_ context.Context) error {
			return errors.New("password=secret database refused")
		},
		RedisPing: func(_ context.Context) error {
			return nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var envelope struct {
		Data StatusView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if envelope.Data.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", envelope.Data.Status)
	}
	if got := envelope.Data.Dependencies["database"].Message; got != "ping failed" {
		t.Fatalf("database message = %q", got)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("status leaked dependency error: %s", rec.Body.String())
	}
}

func TestStatusJobRunErrorDoesNotEmitZeroTime(t *testing.T) {
	cfg := statusConfigWithTradingDay(t, true)
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{},
		Jobs:   &fakeJobRunStore{err: errors.New("job store down")},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var envelope struct {
		Data StatusView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if strings.Contains(rec.Body.String(), "0001-01-01") {
		t.Fatalf("status leaked zero time: %s", rec.Body.String())
	}
	if got := envelope.Data.JobRuns["_error"].StartedAt; got != "" {
		t.Fatalf("job error started_at = %q, want empty", got)
	}
}

func TestAccountsFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID:      "acct-1",
			Alias:          "一号量化",
			BrokerID:       "huaxin",
			GatewayID:      "gw-1",
			StreamPrefix:   "relay:prod:v1:huaxin:gw-1",
			Enabled:        true,
			TradingEnabled: false,
			Simulated:      false,
		},
	}

	handler := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("response is not json: %s", rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Accounts []AccountView `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	if len(envelope.Data.Accounts) != 1 || envelope.Data.Accounts[0].Alias != "一号量化" {
		t.Fatalf("accounts = %#v", envelope.Data.Accounts)
	}
}

func TestAccountsPreferDatabaseAlias(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID:      "acct-1",
			Alias:          "配置别名",
			BrokerID:       "huaxin",
			GatewayID:      "gw-1",
			StreamPrefix:   "relay:test:v1:huaxin:gw-1",
			Enabled:        true,
			TradingEnabled: false,
			Simulated:      false,
		},
	}
	store := &fakeAccountAliasStore{aliases: map[string]string{"acct-1": "落库别名"}}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Accounts: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Source   string        `json:"source"`
			Accounts []AccountView `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	if envelope.Data.Source != "config+database" {
		t.Fatalf("source = %q", envelope.Data.Source)
	}
	if len(envelope.Data.Accounts) != 1 || envelope.Data.Accounts[0].Alias != "落库别名" {
		t.Fatalf("accounts = %#v", envelope.Data.Accounts)
	}
	if len(store.aliasIDs) != 1 || store.aliasIDs[0] != "acct-1" {
		t.Fatalf("alias ids = %#v", store.aliasIDs)
	}
}

func TestAccountRoutesExposeRoutingAndPermissions(t *testing.T) {
	cfg := config.Default()
	cfg.Service.Environment = config.EnvironmentProduction
	cfg.Redis.Env = "prod"
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID:      "acct-1",
			Alias:          "配置别名",
			BrokerID:       "huaxin",
			GatewayID:      "gw-1",
			StreamPrefix:   "relay:prod:v1:huaxin:gw-1",
			Enabled:        true,
			TradingEnabled: false,
			Simulated:      false,
		},
		{
			AccountID:      "acct-2",
			BrokerID:       "huaxin",
			GatewayID:      "gw-2",
			StreamPrefix:   "relay:prod:v1:huaxin:gw-2",
			Enabled:        false,
			TradingEnabled: false,
			Simulated:      false,
		},
	}
	store := &fakeAccountAliasStore{aliases: map[string]string{"acct-1": "落库别名"}}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Accounts: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/account-routes", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Source      string               `json:"source"`
			Environment string               `json:"environment"`
			RedisEnv    string               `json:"redis_env"`
			Protocol    string               `json:"protocol"`
			Summary     AccountStatusSummary `json:"summary"`
			Routes      []AccountRouteView   `json:"routes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode account routes: %v", err)
	}
	if envelope.Data.Source != "config+database" || envelope.Data.Environment != "production" || envelope.Data.RedisEnv != "prod" {
		t.Fatalf("route metadata = %#v", envelope.Data)
	}
	if envelope.Data.Protocol != "relay.stream.v1" {
		t.Fatalf("protocol = %q", envelope.Data.Protocol)
	}
	if envelope.Data.Summary.Configured != 2 || envelope.Data.Summary.Enabled != 1 || envelope.Data.Summary.TradingEnabled != 0 {
		t.Fatalf("summary = %#v", envelope.Data.Summary)
	}
	if len(envelope.Data.Routes) != 2 {
		t.Fatalf("routes = %#v", envelope.Data.Routes)
	}
	first := envelope.Data.Routes[0]
	if first.Alias != "落库别名" || !first.QueryEnabled || !first.ReadOnly || first.TradingEnabled {
		t.Fatalf("first route permissions = %#v", first)
	}
	if first.Streams.CmdQuery != "relay:prod:v1:huaxin:gw-1:cmd.query" || first.Streams.Reply != "relay:prod:v1:huaxin:gw-1:reply" {
		t.Fatalf("first route streams = %#v", first.Streams)
	}
	if envelope.Data.Routes[1].QueryEnabled || envelope.Data.Routes[1].ReadOnly {
		t.Fatalf("disabled route permissions = %#v", envelope.Data.Routes[1])
	}
}

func TestUpdateAccountAliasPersistsToStore(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID: "acct-1",
			Alias:     "配置别名",
			BrokerID:  "huaxin",
			GatewayID: "gw-1",
			Enabled:   true,
		},
	}
	store := &fakeAccountAliasStore{}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Accounts: store,
	})
	req := httptest.NewRequest(http.MethodPatch, "/v1/accounts/acct-1/alias", strings.NewReader(`{"alias":"生产一号"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.savedAccountID != "acct-1" || store.savedBrokerID != "huaxin" || store.savedAlias != "生产一号" {
		t.Fatalf("saved alias = account:%q broker:%q alias:%q", store.savedAccountID, store.savedBrokerID, store.savedAlias)
	}
	var envelope struct {
		Data struct {
			Account AccountView `json:"account"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode account alias: %v", err)
	}
	if envelope.Data.Account.Alias != "生产一号" {
		t.Fatalf("alias = %q", envelope.Data.Account.Alias)
	}
}

func TestAccountAsset(t *testing.T) {
	service := &fakeOrderSubmitter{
		assetResult: orderflow.GetAssetResult{
			Asset: trading.Asset{
				AccountID:     "acct-1",
				CashAvailable: 900000,
				CashTotal:     1000000,
			},
		},
	}
	cfg := config.Default()
	cfg.Market.BaseURL = ""
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/asset", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.assetAccountID != "acct-1" {
		t.Fatalf("account id = %q", service.assetAccountID)
	}
	if !strings.Contains(rec.Body.String(), `"cash_available":900000`) {
		t.Fatalf("response missing asset: %s", rec.Body.String())
	}
}

func TestAccountAssetEnrichesMissingMarketValueFromPositions(t *testing.T) {
	service := &fakeOrderSubmitter{
		assetResult: orderflow.GetAssetResult{
			Asset: trading.Asset{
				AccountID:     "acct-1",
				CashAvailable: 900,
				CashTotal:     1000,
				NetAsset:      1000,
				MarketValue:   0,
			},
		},
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{
				{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100, MarketValue: 720, UnrealizedPnL: 20},
				{AccountID: "acct-1", Symbol: "510300", Exchange: trading.ExchangeSH, Quantity: 1000, MarketValue: 4818, UnrealizedPnL: -10},
			},
			Count: 2,
		},
	}
	cfg := config.Default()
	cfg.Market.BaseURL = ""
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/asset", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, want := range []string{
		`"market_value":5538`,
		`"stock_value":720`,
		`"fund_value":4818`,
		`"position_profit":10`,
		`"net_asset":6538`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestAccountPositions(t *testing.T) {
	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100}},
			Query:     trading.PositionQuery{AccountID: "acct-1", Symbol: "600000", Limit: 10},
			Count:     1,
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions?symbol=600000&limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.positionQuery.AccountID != "acct-1" || service.positionQuery.Symbol != "600000" || service.positionQuery.Limit != 10 {
		t.Fatalf("position query = %#v", service.positionQuery)
	}
	if !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("response missing count: %s", rec.Body.String())
	}
}

func TestAccountPositionsEnrichesInstrumentName(t *testing.T) {
	meridian := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/market/snapshots" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[],"meta":{"count":0}}`)
			return
		}
		if r.URL.Path != "/v1/metadata/instruments" {
			t.Fatalf("unexpected meridian path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("security_ids"); got != "000767.SZ" {
			t.Fatalf("security_ids = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"security_id":"000767.SZ","name":"晋控电力"}],"meta":{"count":1}}`)
	}))
	defer meridian.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = meridian.URL
	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{AccountID: "acct-1", Symbol: "000767", Exchange: trading.ExchangeSZ, Quantity: 100}},
			Count:     1,
		},
	}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions?limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"晋控电力"`) {
		t.Fatalf("response missing instrument name: %s", rec.Body.String())
	}
}

func TestAccountPositionsDerivesZeroAvgCostFromTodayBuyFills(t *testing.T) {
	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{
				AccountID:   "acct-1",
				Symbol:      "688222",
				Name:        "成都先导",
				Exchange:    trading.ExchangeSH,
				Quantity:    800,
				SellableQty: 0,
				AvgCost:     0,
			}},
			Count: 1,
		},
		listFillsResult: orderflow.ListFillsResult{
			Fills: []trading.Fill{
				{AccountID: "acct-1", Symbol: "688222", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, Price: 26.09, Qty: 300},
				{AccountID: "acct-1", Symbol: "688222", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, Price: 26.10, Qty: 200},
				{AccountID: "acct-1", Symbol: "688222", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, Price: 26.08, Qty: 300},
			},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions?limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Data struct {
			Positions []trading.Position `json:"positions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.Positions) != 1 {
		t.Fatalf("positions = %#v", body.Data.Positions)
	}
	if diff := math.Abs(body.Data.Positions[0].AvgCost - 26.08875); diff > 0.000001 {
		t.Fatalf("avg_cost = %.8f, want 26.08875", body.Data.Positions[0].AvgCost)
	}
	if service.fillQuery.AccountID != "acct-1" || service.fillQuery.TradeDate != timeutil.Now().Format("2006-01-02") {
		t.Fatalf("fill query = %#v", service.fillQuery)
	}
}

func TestAccountPositionsComputesTotalAndDayPnLFromQuotesAndTodayFills(t *testing.T) {
	meridian := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/metadata/instruments":
			_, _ = io.WriteString(w, `{"data":[{"security_id":"600000.SH","name":"浦发银行"}],"meta":{"count":1}}`)
		case "/v1/market/snapshots":
			if got := r.URL.Query().Get("security_ids"); got != "600000.SH" {
				t.Fatalf("security_ids = %q", got)
			}
			_, _ = io.WriteString(w, `{"data":[{"security_id":"600000.SH","open":8.0,"last":11.0}],"meta":{"count":1}}`)
		default:
			t.Fatalf("unexpected meridian path %s", r.URL.Path)
		}
	}))
	defer meridian.Close()

	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{
				AccountID:   "acct-1",
				Symbol:      "600000",
				Exchange:    trading.ExchangeSH,
				Quantity:    300,
				SellableQty: 200,
				TodayQty:    100,
				AvgCost:     9,
			}},
			Count: 1,
		},
		listFillsResult: orderflow.ListFillsResult{
			Fills: []trading.Fill{
				{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, Price: 10, Qty: 100},
			},
		},
	}
	cfg := config.Default()
	cfg.Market.BaseURL = meridian.URL
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions?limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Data struct {
			Positions []trading.Position `json:"positions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.Positions) != 1 {
		t.Fatalf("positions = %#v", body.Data.Positions)
	}
	position := body.Data.Positions[0]
	for label, gotWant := range map[string][2]float64{
		"market_value":       {position.MarketValue, 3300},
		"unrealized_pnl":     {position.UnrealizedPnL, 600},
		"day_unrealized_pnl": {position.DayUnrealizedPnL, 700},
	} {
		if diff := math.Abs(gotWant[0] - gotWant[1]); diff > 0.000001 {
			t.Fatalf("%s = %.8f, want %.8f", label, gotWant[0], gotWant[1])
		}
	}
}

func TestHistoricalAccountPositions(t *testing.T) {
	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{AccountID: "acct-1", TradeDate: "2026-06-12", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100}},
			Query:     trading.PositionQuery{AccountID: "acct-1", History: true, TradeDate: "20260612", Limit: 10},
			Count:     1,
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions/history?trade_date=20260612&limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !service.positionQuery.History || service.positionQuery.TradeDate != "20260612" {
		t.Fatalf("position query = %#v", service.positionQuery)
	}
}

func TestHistoricalAccountPositionsDoesNotDeriveAvgCostFromTodayFills(t *testing.T) {
	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{
				AccountID:   "acct-1",
				TradeDate:   "2026-06-12",
				Symbol:      "688222",
				Exchange:    trading.ExchangeSH,
				Quantity:    800,
				SellableQty: 0,
				AvgCost:     0,
			}},
			Count: 1,
		},
		listFillsResult: orderflow.ListFillsResult{
			Fills: []trading.Fill{{AccountID: "acct-1", Symbol: "688222", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideBuy, Price: 26.09, Qty: 800}},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions/history?trade_date=20260612&limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Data struct {
			Positions []trading.Position `json:"positions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data.Positions) != 1 || body.Data.Positions[0].AvgCost != 0 {
		t.Fatalf("positions = %#v", body.Data.Positions)
	}
	if service.fillQuery.AccountID != "" {
		t.Fatalf("historical positions should not query current fills: %#v", service.fillQuery)
	}
}

func TestHistoricalAccountPositionsEnrichesInstrumentName(t *testing.T) {
	meridian := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metadata/instruments" {
			t.Fatalf("unexpected meridian path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("security_ids"); got != "601728.SH" {
			t.Fatalf("security_ids = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"security_id":"601728.XSHG","name":"中国电信"}],"meta":{"count":1}}`)
	}))
	defer meridian.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = meridian.URL
	service := &fakeOrderSubmitter{
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{AccountID: "acct-1", TradeDate: "2026-06-12", Symbol: "601728", Exchange: trading.ExchangeSH, Quantity: 100}},
			Count:     1,
		},
	}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/positions/history?trade_date=20260612&limit=10", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"中国电信"`) {
		t.Fatalf("response missing instrument name: %s", rec.Body.String())
	}
}

func TestRefreshAccountAsset(t *testing.T) {
	service := &fakeOrderSubmitter{
		refreshAssetResult: orderflow.RefreshQueryResult{
			AccountID: "acct-1",
			Action:    redisstream.ActionAccountAsset,
			MessageID: "msg-asset-1",
			StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.query",
			StreamID:  "1-0",
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/acct-1/asset/refresh", nil)
	req.Header.Set("X-Request-ID", "req-refresh-asset")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.refreshAssetAccountID != "acct-1" || service.refreshAssetRequestID != "req-refresh-asset" {
		t.Fatalf("refresh asset call = %q/%q", service.refreshAssetAccountID, service.refreshAssetRequestID)
	}
	if !strings.Contains(rec.Body.String(), redisstream.ActionAccountAsset) {
		t.Fatalf("response missing action: %s", rec.Body.String())
	}
}

func TestRefreshAccountPositions(t *testing.T) {
	service := &fakeOrderSubmitter{
		refreshPositionsResult: orderflow.RefreshQueryResult{
			AccountID: "acct-1",
			Action:    redisstream.ActionAccountPositions,
			MessageID: "msg-positions-1",
			StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.query",
			StreamID:  "1-0",
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/acct-1/positions/refresh", nil)
	req.Header.Set("X-Request-ID", "req-refresh-positions")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.refreshPositionsAccountID != "acct-1" || service.refreshPositionsRequestID != "req-refresh-positions" {
		t.Fatalf("refresh positions call = %q/%q", service.refreshPositionsAccountID, service.refreshPositionsRequestID)
	}
	if !strings.Contains(rec.Body.String(), redisstream.ActionAccountPositions) {
		t.Fatalf("response missing action: %s", rec.Body.String())
	}
}

func TestRefreshAccountOrders(t *testing.T) {
	service := &fakeOrderSubmitter{
		refreshOrdersResult: orderflow.RefreshQueryResult{
			AccountID: "acct-1",
			Action:    redisstream.ActionOrderList,
			MessageID: "msg-orders-1",
			StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.query",
			StreamID:  "1-0",
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/acct-1/orders/refresh?trade_date=20260615", nil)
	req.Header.Set("X-Request-ID", "req-refresh-orders")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.refreshOrdersAccountID != "acct-1" || service.refreshOrdersRequestID != "req-refresh-orders" {
		t.Fatalf("refresh orders call = %q/%q", service.refreshOrdersAccountID, service.refreshOrdersRequestID)
	}
	if service.refreshOrdersTradeDate != "20260615" {
		t.Fatalf("refresh orders trade_date = %q", service.refreshOrdersTradeDate)
	}
	if !strings.Contains(rec.Body.String(), redisstream.ActionOrderList) {
		t.Fatalf("response missing action: %s", rec.Body.String())
	}
}

func TestRefreshAccountFills(t *testing.T) {
	service := &fakeOrderSubmitter{
		refreshFillsResult: orderflow.RefreshQueryResult{
			AccountID: "acct-1",
			Action:    redisstream.ActionFillList,
			MessageID: "msg-fills-1",
			StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.query",
			StreamID:  "1-0",
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/acct-1/fills/refresh?trade_date=2026-06-15", nil)
	req.Header.Set("X-Request-ID", "req-refresh-fills")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.refreshFillsAccountID != "acct-1" || service.refreshFillsRequestID != "req-refresh-fills" {
		t.Fatalf("refresh fills call = %q/%q", service.refreshFillsAccountID, service.refreshFillsRequestID)
	}
	if service.refreshFillsTradeDate != "20260615" {
		t.Fatalf("refresh fills trade_date = %q", service.refreshFillsTradeDate)
	}
	if !strings.Contains(rec.Body.String(), redisstream.ActionFillList) {
		t.Fatalf("response missing action: %s", rec.Body.String())
	}
}

func TestSchemaDiscovery(t *testing.T) {
	handler := New(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/v1/schema", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("response is not json: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "relay.trading.v1alpha1") {
		t.Fatalf("response missing schema version: %s", rec.Body.String())
	}
}

func TestMeridianMarketSnapshotsProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/metadata/trading-day":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date":                             20260614,
					"is_trading_day":                   false,
					"previous_or_current_trading_date": 20260612,
				},
			})
		case "/v1/market/snapshots":
			if r.URL.Query().Get("trade_date") != "20260612" {
				t.Fatalf("trade_date = %q", r.URL.Query().Get("trade_date"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"security_id":  "600000.SH",
					"market_level": "level1",
					"last":         9.67,
					"pre_close":    9.59,
				}},
				"meta": map[string]any{"schema_version": "market_snapshot.v1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	cfg.Market.TimeoutSeconds = 1
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/v1/meridian/market/snapshots?security_id=600000.SH", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"security_id":"600000.SH"`) || !strings.Contains(rec.Body.String(), `"schema_version":"market_snapshot.v1"`) {
		t.Fatalf("response did not preserve meridian payload: %s", rec.Body.String())
	}
}

func TestMeridianMarketSnapshotStreamProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream/market/snapshots" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("market_level") != "level1" {
			t.Fatalf("market_level = %q", r.URL.Query().Get("market_level"))
		}
		if r.URL.Query().Get("include_existing") != "true" {
			t.Fatalf("include_existing = %q", r.URL.Query().Get("include_existing"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: market_snapshots\n")
		_, _ = io.WriteString(w, `data: {"data":[{"security_id":"600000.SH","last":9.66}]}`+"\n\n")
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/v1/meridian/stream/market/snapshots?security_ids=600000.SH", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "event: market_snapshots") || !strings.Contains(rec.Body.String(), `"last":9.66`) {
		t.Fatalf("response did not proxy sse payload: %s", rec.Body.String())
	}
}

func TestEventsStreamPublishesSSE(t *testing.T) {
	eventHub := events.NewHub()
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Events: eventHub,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events/stream?account_id=acct-1", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request event stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
	}

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	waitForSSELine(t, lines, "event: relay.connected")
	eventHub.Publish(events.Event{
		Type:       events.TypeOrderChanged,
		AccountIDs: []string{"acct-1"},
		Data:       map[string]any{"order_events": 1},
	})
	waitForSSELine(t, lines, "event: order.changed")
}

func TestMethodNotAllowed(t *testing.T) {
	handler := New(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func waitForSSELine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before %q", want)
			}
			if line == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func TestSubmitOrderAccepted(t *testing.T) {
	submitter := &fakeOrderSubmitter{
		result: orderflow.SubmitOrderResult{
			Order: trading.Order{
				AccountID:      "acct-1",
				GatewayOrderID: "gw-1",
				Status:         trading.OrderStatusCreated,
			},
			MessageID:      "msg-1",
			StreamKey:      "relay:prod:v1:huaxin:gw-1:cmd.trade",
			StreamID:       "1-0",
			IdempotencyKey: "idem-1",
			Published: redisstream.CommandPublishResult{
				StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.trade",
				StreamID:  "1-0",
				BodyBytes: 123,
			},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: submitter,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"account_id":"acct-1",
		"symbol":"600000",
		"exchange":"SH",
		"trade_side":"B",
		"business_type":"S",
		"offset_type":"C",
		"price":9.54,
		"qty":100
	}`))
	req.Header.Set("X-Request-ID", "req-submit")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if submitter.requestID != "req-submit" {
		t.Fatalf("request id passed to submitter = %q", submitter.requestID)
	}
	if submitter.req.AccountID != "acct-1" || submitter.req.Symbol != "600000" {
		t.Fatalf("submit request = %#v", submitter.req)
	}
	if !strings.Contains(rec.Body.String(), "msg-1") {
		t.Fatalf("response missing result: %s", rec.Body.String())
	}
}

func TestSubmitOrderReplayReturnsOK(t *testing.T) {
	submitter := &fakeOrderSubmitter{
		result: orderflow.SubmitOrderResult{
			Order: trading.Order{
				AccountID:      "acct-1",
				GatewayOrderID: "gateway-1",
				Status:         trading.OrderStatusCancelled,
			},
			IdempotencyKey: "idem-1",
			Replayed:       true,
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: submitter,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"account_id":"acct-1",
		"gateway_order_id":"gateway-1",
		"symbol":"600000",
		"exchange":"SH",
		"trade_side":"B",
		"business_type":"S",
		"price":9.67,
		"qty":100,
		"idempotency_key":"idem-1"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"replayed":true`) {
		t.Fatalf("response missing replayed marker: %s", rec.Body.String())
	}
}

func TestSubmitOrderUnavailableWithoutService(t *testing.T) {
	handler := New(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSubmitOrderTradingDisabled(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{err: orderflow.ErrTradingDisabled},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"account_id":"acct-1",
		"symbol":"600000",
		"exchange":"SH",
		"trade_side":"B",
		"business_type":"S",
		"price":9.54,
		"qty":100
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestSubmitOrderMissingPublisherUnavailable(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{err: orderflow.ErrMissingPublisher},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"account_id":"acct-1",
		"symbol":"600000",
		"exchange":"SH",
		"trade_side":"B",
		"business_type":"S",
		"price":9.67,
		"qty":100
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestSubmitOrderIdempotencyConflict(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{err: orderflow.ErrIdempotencyConflict},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"account_id":"acct-1",
		"symbol":"600000",
		"exchange":"SH",
		"trade_side":"B",
		"business_type":"S",
		"price":9.67,
		"qty":100,
		"idempotency_key":"idem-1"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"IDEMPOTENCY_CONFLICT"`) {
		t.Fatalf("response missing idempotency code: %s", rec.Body.String())
	}
}

func TestSubmitOrderUnsupportedBusinessType(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{err: orderflow.ErrUnsupportedBusinessType},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"account_id":"acct-1",
		"symbol":"510300",
		"exchange":"SH",
		"trade_side":"B",
		"business_type":"E",
		"price":4.818,
		"qty":100
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"NOT_IMPLEMENTED"`) {
		t.Fatalf("response missing not implemented code: %s", rec.Body.String())
	}
}

func TestSubmitOrderBadJSON(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{bad-json`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestBatchSubmitOrdersAccepted(t *testing.T) {
	service := &fakeOrderSubmitter{
		batchResult: orderflow.BatchSubmitOrderResult{
			Orders: []trading.Order{
				{AccountID: "acct-1", GatewayOrderID: "gw-b1", Status: trading.OrderStatusCreated},
				{AccountID: "acct-1", GatewayOrderID: "gw-b2", Status: trading.OrderStatusCreated},
			},
			MessageID:      "msg-batch-1",
			StreamKey:      "relay:prod:v1:huaxin:gw-1:cmd.trade",
			StreamID:       "3-0",
			IdempotencyKey: "idem-batch-1",
			Published: redisstream.CommandPublishResult{
				StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.trade",
				StreamID:  "3-0",
				BodyBytes: 256,
			},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders/batch", strings.NewReader(`{
		"account_id":"acct-1",
		"orders":[
			{"symbol":"600000","exchange":"SH","trade_side":"B","business_type":"S","price":9.67,"qty":100},
			{"symbol":"000001","exchange":"SZ","trade_side":"B","business_type":"S","price":11.24,"qty":100}
		],
		"idempotency_key":"idem-batch-1"
	}`))
	req.Header.Set("X-Request-ID", "req-batch")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.batchReq.AccountID != "acct-1" || len(service.batchReq.Orders) != 2 || service.batchRequestID != "req-batch" {
		t.Fatalf("batch request = %#v request_id=%s", service.batchReq, service.batchRequestID)
	}
	if !strings.Contains(rec.Body.String(), "msg-batch-1") {
		t.Fatalf("response missing result: %s", rec.Body.String())
	}
}

func TestCancelOrderAccepted(t *testing.T) {
	service := &fakeOrderSubmitter{
		cancelResult: orderflow.CancelOrderResult{
			Order: trading.Order{
				AccountID:      "acct-1",
				GatewayOrderID: "gateway-1",
				Status:         trading.OrderStatusWorking,
			},
			CancelID:       "cancel-1",
			MessageID:      "msg-cancel-1",
			StreamKey:      "relay:prod:v1:huaxin:gw-1:cmd.trade",
			StreamID:       "2-0",
			IdempotencyKey: "idem-cancel-1",
			Published: redisstream.CommandPublishResult{
				StreamKey: "relay:prod:v1:huaxin:gw-1:cmd.trade",
				StreamID:  "2-0",
				BodyBytes: 123,
			},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders/gateway-1/cancel", strings.NewReader(`{
		"account_id":"acct-1",
		"gateway_order_id":"gateway-1"
	}`))
	req.Header.Set("X-Request-ID", "req-cancel")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.cancelReq.GatewayOrderID != "gateway-1" || service.cancelRequestID != "req-cancel" {
		t.Fatalf("cancel request = %#v request_id=%s", service.cancelReq, service.cancelRequestID)
	}
	if !strings.Contains(rec.Body.String(), "msg-cancel-1") {
		t.Fatalf("response missing result: %s", rec.Body.String())
	}
}

func TestCancelOrderPathBodyMismatch(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders/gateway-1/cancel", strings.NewReader(`{
		"account_id":"acct-1",
		"gateway_order_id":"gateway-2"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCancelOrderTerminalConflict(t *testing.T) {
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{cancelErr: orderflow.ErrOrderTerminalNotCancelable},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/orders/gateway-1/cancel", strings.NewReader(`{
		"account_id":"acct-1",
		"gateway_order_id":"gateway-1"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestListOrders(t *testing.T) {
	service := &fakeOrderSubmitter{
		listOrdersResult: orderflow.ListOrdersResult{
			Orders: []trading.Order{{AccountID: "acct-1", GatewayOrderID: "gateway-1"}},
			Query:  trading.OrderQuery{AccountID: "acct-1", Limit: 5},
			Count:  1,
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/orders?account_id=acct-1&status=working&limit=5", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.orderQuery.AccountID != "acct-1" || service.orderQuery.Status != trading.OrderStatusWorking || service.orderQuery.Limit != 5 {
		t.Fatalf("query = %#v", service.orderQuery)
	}
	if service.orderQuery.TradeDate != timeutil.Now().Format("2006-01-02") {
		t.Fatalf("default trade date = %q", service.orderQuery.TradeDate)
	}
	if !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("response missing count: %s", rec.Body.String())
	}
}

func TestListOrdersEnrichesInstrumentName(t *testing.T) {
	meridian := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metadata/instruments" {
			t.Fatalf("unexpected meridian path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("security_ids"); got != "000767.SZ" {
			t.Fatalf("security_ids = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"security_id":"000767.SZ","name":"晋控电力"}],"meta":{"count":1}}`)
	}))
	defer meridian.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = meridian.URL
	service := &fakeOrderSubmitter{
		listOrdersResult: orderflow.ListOrdersResult{
			Orders: []trading.Order{{
				AccountID:      "acct-1",
				GatewayOrderID: "external-1",
				Symbol:         "000767",
				Exchange:       trading.ExchangeSZ,
			}},
			Count: 1,
		},
	}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/orders?account_id=acct-1&limit=5", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"晋控电力"`) {
		t.Fatalf("response missing instrument name: %s", rec.Body.String())
	}
}

func TestHistoryOrdersUsesExplicitDateRange(t *testing.T) {
	service := &fakeOrderSubmitter{
		listOrdersResult: orderflow.ListOrdersResult{Count: 0},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/history/orders?account_id=acct-1&date_from=20260612&date_to=20260613", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !service.orderQuery.History || service.orderQuery.TradeDate != "" || service.orderQuery.DateFrom != "20260612" || service.orderQuery.DateTo != "20260613" {
		t.Fatalf("history order query = %#v", service.orderQuery)
	}
}

func TestListFills(t *testing.T) {
	service := &fakeOrderSubmitter{
		listFillsResult: orderflow.ListFillsResult{
			Fills: []trading.Fill{{FillID: "fill-1", AccountID: "acct-1", GatewayOrderID: "gateway-1"}},
			Query: trading.FillQuery{AccountID: "acct-1", Limit: 5},
			Count: 1,
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/fills?account_id=acct-1&limit=5", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.fillQuery.AccountID != "acct-1" || service.fillQuery.Limit != 5 {
		t.Fatalf("query = %#v", service.fillQuery)
	}
	if service.fillQuery.TradeDate != timeutil.Now().Format("2006-01-02") {
		t.Fatalf("default trade date = %q", service.fillQuery.TradeDate)
	}
	if !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("response missing count: %s", rec.Body.String())
	}
}

func TestListFillsEnrichesInstrumentName(t *testing.T) {
	meridian := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metadata/instruments" {
			t.Fatalf("unexpected meridian path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("security_ids"); got != "601728.SH" {
			t.Fatalf("security_ids = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"security_id":"601728.XSHG","name":"中国电信"}],"meta":{"count":1}}`)
	}))
	defer meridian.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = meridian.URL
	service := &fakeOrderSubmitter{
		listFillsResult: orderflow.ListFillsResult{
			Fills: []trading.Fill{{
				FillID:         "fill-1",
				AccountID:      "acct-1",
				GatewayOrderID: "external-1",
				Symbol:         "601728",
				Exchange:       trading.ExchangeSH,
			}},
			Count: 1,
		},
	}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: service,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/fills?account_id=acct-1&limit=5", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"中国电信"`) {
		t.Fatalf("response missing instrument name: %s", rec.Body.String())
	}
}

func TestJobRunPostPersistsReport(t *testing.T) {
	store := &fakeJobRunStore{}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Jobs: store,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/runs", strings.NewReader(`{
		"trigger":"unit-test",
		"report":{
			"ok":true,
			"job":"pre_open_init",
			"timezone":"Asia/Shanghai",
			"trading_day":{"target_trade_date":"20260614"}
		}
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if store.saved.JobName != "pre_open_init" || store.saved.TargetTradeDate != "20260614" || store.saved.Trigger != "unit-test" {
		t.Fatalf("saved job run = %#v", store.saved)
	}
}

func TestSettlementSnapshotPostWritesCloseSnapshots(t *testing.T) {
	service := &fakeOrderSubmitter{
		assetResult: orderflow.GetAssetResult{
			Asset: trading.Asset{AccountID: "acct-1", NetAsset: 1200000, CashAvailable: 1000000, MarketValue: 200000},
		},
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100}},
			Count:     1,
		},
		listOrdersResult: orderflow.ListOrdersResult{
			Orders: []trading.Order{{AccountID: "acct-1", GatewayOrderID: "gw-working", Status: trading.OrderStatusWorking}},
			Count:  1,
		},
		listFillsResult: orderflow.ListFillsResult{
			Fills: []trading.Fill{{FillID: "fill-1", AccountID: "acct-1", GatewayOrderID: "gw-filled"}},
			Count: 1,
		},
	}
	store := &fakeSettlementStore{}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders:      service,
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/settlements/snapshots", strings.NewReader(`{
		"run_id":"settlement-20260612",
		"trade_date":"20260612",
		"account_ids":["acct-1"],
		"snapshot_type":"close",
		"source":"post_close_settlement"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.assetAccountID != "acct-1" || service.positionQuery.AccountID != "acct-1" {
		t.Fatalf("service queries asset=%q positions=%#v", service.assetAccountID, service.positionQuery)
	}
	if service.orderQuery.TradeDate != "2026-06-12" || !service.orderQuery.History {
		t.Fatalf("order query = %#v", service.orderQuery)
	}
	if len(store.assetSnapshots) != 1 || store.assetSnapshots[0].tradeDate != "2026-06-12" || store.assetSnapshots[0].snapshotType != "close" {
		t.Fatalf("asset snapshots = %#v", store.assetSnapshots)
	}
	if len(store.positionSnapshots) != 1 || store.positionSnapshots[0].position.TradeDate != "2026-06-12" {
		t.Fatalf("position snapshots = %#v", store.positionSnapshots)
	}
	if store.reconciliation.RunID != "settlement-20260612" || store.reconciliation.Status != "completed" {
		t.Fatalf("reconciliation = %#v", store.reconciliation)
	}
	if len(store.inputs) != 4 {
		t.Fatalf("reconciliation inputs = %#v", store.inputs)
	}
	if len(store.breaks) != 1 || store.breaks[0].BreakType != "non_terminal_order" {
		t.Fatalf("reconciliation breaks = %#v", store.breaks)
	}
	if !strings.Contains(rec.Body.String(), `"non_terminal_orders":1`) {
		t.Fatalf("response missing non-terminal count: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"reconciliation_breaks":1`) {
		t.Fatalf("response missing break count: %s", rec.Body.String())
	}
}

func TestSettlementSnapshotPostWritesOpenAssetOnly(t *testing.T) {
	service := &fakeOrderSubmitter{
		assetResult: orderflow.GetAssetResult{
			Asset: trading.Asset{AccountID: "acct-1", NetAsset: 1300000, CashAvailable: 1100000, MarketValue: 200000},
		},
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100}},
			Count:     1,
		},
		listOrdersResult: orderflow.ListOrdersResult{Orders: []trading.Order{}, Count: 0},
		listFillsResult:  orderflow.ListFillsResult{Fills: []trading.Fill{}, Count: 0},
	}
	store := &fakeSettlementStore{}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders:      service,
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/settlements/snapshots", strings.NewReader(`{
		"run_id":"pre_open_init-20260615",
		"trade_date":"20260615",
		"account_ids":["acct-1"],
		"snapshot_type":"open",
		"source":"pre_open_init"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.assetSnapshots) != 1 || store.assetSnapshots[0].snapshotType != "open" || store.assetSnapshots[0].source != "pre_open_init" {
		t.Fatalf("asset snapshots = %#v", store.assetSnapshots)
	}
	if len(store.positionSnapshots) != 0 {
		t.Fatalf("open snapshot should not write position snapshots: %#v", store.positionSnapshots)
	}
	if store.reconciliation.RunID != "pre_open_init-20260615" || store.reconciliation.Source != "pre_open_init" {
		t.Fatalf("reconciliation = %#v", store.reconciliation)
	}
}

func TestSettlementSnapshotAccountErrorIsNonFatal(t *testing.T) {
	service := &fakeOrderSubmitter{
		assetErr: errors.New("asset snapshot not found"),
		positionsResult: orderflow.ListPositionsResult{
			Positions: []trading.Position{},
			Count:     0,
		},
		listOrdersResult: orderflow.ListOrdersResult{Orders: []trading.Order{}, Count: 0},
		listFillsResult:  orderflow.ListFillsResult{Fills: []trading.Fill{}, Count: 0},
	}
	store := &fakeSettlementStore{}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders:      service,
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/settlements/snapshots", strings.NewReader(`{
		"run_id":"pre_open_init-20260617",
		"trade_date":"20260617",
		"account_ids":["acct-new"],
		"snapshot_type":"open",
		"source":"pre_open_init"
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.assetSnapshots) != 0 {
		t.Fatalf("asset snapshots = %#v", store.assetSnapshots)
	}
	if store.reconciliation.Status != "completed" || store.reconciliation.ErrorMessage != "" {
		t.Fatalf("reconciliation = %#v", store.reconciliation)
	}
	if len(store.breaks) != 1 || store.breaks[0].BreakType != "account_refresh_failed" || store.breaks[0].AccountID != "acct-new" {
		t.Fatalf("breaks = %#v", store.breaks)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"account_error_count":1`) || !strings.Contains(body, `"warnings"`) {
		t.Fatalf("response missing nonfatal account warning: %s", body)
	}
}

func TestReconciliationBreaksQuery(t *testing.T) {
	store := &fakeSettlementStore{
		breaks: []ledger.ReconciliationBreak{{
			RunID:      "settlement-20260612",
			AccountID:  "acct-1",
			BreakType:  "non_terminal_order",
			Severity:   "warning",
			Status:     "open",
			ObjectType: "order",
			ObjectID:   "gw-working",
		}},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/reconciliations/breaks?run_id=settlement-20260612&status=open", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.breakQuery.RunID != "settlement-20260612" || store.breakQuery.Status != "open" {
		t.Fatalf("break query = %#v", store.breakQuery)
	}
	if !strings.Contains(rec.Body.String(), `"break_type":"non_terminal_order"`) {
		t.Fatalf("response missing break: %s", rec.Body.String())
	}
}

func TestDailyPerformanceQuery(t *testing.T) {
	store := &fakeSettlementStore{
		performance: ledger.DailyPerformance{
			AccountID:        "acct-1",
			TradeDate:        "2026-06-12",
			NetAsset:         1200000,
			PreviousNetAsset: 1190000,
			OpenNetAsset:     1195000,
			IntradayPnL:      5000,
			IntradayReturn:   0.0041841004,
			DailyPnL:         10000,
			ReturnRate:       0.0084033613,
			FillsCount:       2,
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/performance/daily?trade_date=20260612", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.performanceAccountID != "acct-1" || store.performanceTradeDate != "2026-06-12" {
		t.Fatalf("performance query = %q/%q", store.performanceAccountID, store.performanceTradeDate)
	}
	if !strings.Contains(rec.Body.String(), `"daily_pnl":10000`) {
		t.Fatalf("response missing daily pnl: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"open_net_asset":1195000`) || !strings.Contains(rec.Body.String(), `"intraday_pnl":5000`) {
		t.Fatalf("response missing open-to-close fields: %s", rec.Body.String())
	}
}

func TestMeridianMarketBarsProxy(t *testing.T) {
	var barsQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/market/bars":
			barsQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"security_id":     "600000.SH",
					"instrument_type": "stock",
					"trade_date":      20260612,
					"datetime":        "2026-06-12T15:00:00+08:00",
					"frequency":       "1m",
					"adjustment":      "none",
					"close":           9.67,
					"schema_version":  "market_bar.v1",
				}},
				"meta": map[string]any{"schema_version": "market_bar.v1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	cfg.Market.TimeoutSeconds = 1
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/v1/meridian/market/bars?security_id=600000.SH&trade_date=20260612&frequency=1m&adjustment=none&limit=5", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(barsQuery, "security_id=600000.SH") || !strings.Contains(barsQuery, "frequency=1m") {
		t.Fatalf("query not passed through: %s", barsQuery)
	}
	if !strings.Contains(rec.Body.String(), `"schema_version":"market_bar.v1"`) || !strings.Contains(rec.Body.String(), `"close":9.67`) {
		t.Fatalf("response did not preserve meridian bars payload: %s", rec.Body.String())
	}
}

func TestPerformanceSeriesQuery(t *testing.T) {
	store := &fakeSettlementStore{
		performanceSeries: []ledger.DailyPerformance{
			{AccountID: "acct-1", TradeDate: "2026-06-12", NetAsset: 1000, PreviousNetAsset: 900, DailyPnL: 100, ReturnRate: 0.1111111111},
			{AccountID: "acct-1", TradeDate: "2026-06-13", NetAsset: 950, PreviousNetAsset: 1000, DailyPnL: -50, ReturnRate: -0.05},
			{AccountID: "acct-1", TradeDate: "2026-06-14", NetAsset: 1100, PreviousNetAsset: 950, DailyPnL: 150, ReturnRate: 0.1578947368},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/performance/series?date_from=20260612&date_to=20260614", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.performanceSeriesAccountID != "acct-1" || store.performanceSeriesDateFrom != "2026-06-12" || store.performanceSeriesDateTo != "2026-06-14" {
		t.Fatalf("series query = %q/%q/%q", store.performanceSeriesAccountID, store.performanceSeriesDateFrom, store.performanceSeriesDateTo)
	}
	if !strings.Contains(rec.Body.String(), `"max_drawdown":-0.05`) || !strings.Contains(rec.Body.String(), `"total_return":0.2222222222222222`) {
		t.Fatalf("response missing performance curve summary: %s", rec.Body.String())
	}
}

func TestPerformanceSeriesQueryWithBenchmarkBars(t *testing.T) {
	var queries []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/market/bars" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		tradeDate := r.URL.Query().Get("trade_date")
		closePrice := 100.0
		if tradeDate == "20260613" {
			closePrice = 105.0
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"security_id":    r.URL.Query().Get("security_id"),
				"trade_date":     tradeDate,
				"datetime":       tradeDate[:4] + "-" + tradeDate[4:6] + "-" + tradeDate[6:8] + "T15:00:00+08:00",
				"frequency":      "1m",
				"adjustment":     "none",
				"close":          closePrice,
				"schema_version": "market_bar.v1",
			}},
			"meta": map[string]any{"schema_version": "market_bar.v1"},
		})
	}))
	defer upstream.Close()

	store := &fakeSettlementStore{
		performanceSeries: []ledger.DailyPerformance{
			{AccountID: "acct-1", TradeDate: "2026-06-12", NetAsset: 1000, PreviousNetAsset: 1000, ReturnRate: 0},
			{AccountID: "acct-1", TradeDate: "2026-06-13", NetAsset: 1120, PreviousNetAsset: 1000, ReturnRate: 0.12},
		},
	}
	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	cfg.Market.TimeoutSeconds = 1
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/performance/series?date_from=20260612&date_to=20260613&benchmark_security_id=000300.SH", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(queries) != 2 {
		t.Fatalf("meridian bars calls = %d, want 2: %v", len(queries), queries)
	}
	if !strings.Contains(queries[0], "trade_date=20260612") || !strings.Contains(queries[0], "start_time=14%3A55%3A00") || !strings.Contains(queries[0], "end_time=15%3A00%3A00") {
		t.Fatalf("benchmark query did not use meridian bars close window: %s", queries[0])
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"benchmark_security_id":"000300.SH"`) || !strings.Contains(body, `"benchmark_total_return":0.05`) {
		t.Fatalf("response missing benchmark summary: %s", body)
	}
	if !strings.Contains(body, `"benchmark_observation_days":2`) || !strings.Contains(body, `"excess_total_return"`) {
		t.Fatalf("response missing benchmark observation or excess return: %s", body)
	}
}

func TestPerformanceSeriesCSV(t *testing.T) {
	store := &fakeSettlementStore{
		performanceSeries: []ledger.DailyPerformance{
			{AccountID: "acct-1", TradeDate: "2026-06-12", NetAsset: 1000, PreviousNetAsset: 900, DailyPnL: 100, ReturnRate: 0.1111111111, BenchmarkSecurityID: "000300.SH", BenchmarkClose: floatPtr(4777.32)},
		},
	}
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Settlements: store,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/acct-1/performance/series.csv?date_from=20260612&date_to=20260612", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "benchmark_security_id,benchmark_close") || !strings.Contains(rec.Body.String(), "acct-1,2026-06-12,1000") {
		t.Fatalf("csv response missing row: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "open_net_asset,overnight_adjustment,asset_change,intraday_pnl,intraday_return") {
		t.Fatalf("csv response missing open-to-close columns: %s", rec.Body.String())
	}
}

type fakeOrderSubmitter struct {
	req                       trading.SubmitOrderRequest
	requestID                 string
	result                    orderflow.SubmitOrderResult
	err                       error
	batchReq                  trading.BatchSubmitOrderRequest
	batchRequestID            string
	batchResult               orderflow.BatchSubmitOrderResult
	batchErr                  error
	cancelReq                 trading.CancelOrderRequest
	cancelRequestID           string
	cancelResult              orderflow.CancelOrderResult
	cancelErr                 error
	orderQuery                trading.OrderQuery
	listOrdersResult          orderflow.ListOrdersResult
	listOrdersErr             error
	fillQuery                 trading.FillQuery
	listFillsResult           orderflow.ListFillsResult
	listFillsErr              error
	assetAccountID            string
	assetResult               orderflow.GetAssetResult
	assetErr                  error
	positionQuery             trading.PositionQuery
	positionsResult           orderflow.ListPositionsResult
	positionsErr              error
	refreshAssetAccountID     string
	refreshAssetRequestID     string
	refreshAssetResult        orderflow.RefreshQueryResult
	refreshAssetErr           error
	refreshPositionsAccountID string
	refreshPositionsRequestID string
	refreshPositionsResult    orderflow.RefreshQueryResult
	refreshPositionsErr       error
	refreshOrdersAccountID    string
	refreshOrdersRequestID    string
	refreshOrdersTradeDate    string
	refreshOrdersResult       orderflow.RefreshQueryResult
	refreshOrdersErr          error
	refreshFillsAccountID     string
	refreshFillsRequestID     string
	refreshFillsTradeDate     string
	refreshFillsResult        orderflow.RefreshQueryResult
	refreshFillsErr           error
}

type fakeJobRunStore struct {
	saved ledger.JobRun
	runs  []ledger.JobRun
	err   error
}

type fakeAccountAliasStore struct {
	aliases        map[string]string
	aliasIDs       []string
	savedAccountID string
	savedBrokerID  string
	savedAlias     string
	err            error
}

type fakeSettlementStore struct {
	assetSnapshots []struct {
		asset        trading.Asset
		tradeDate    string
		snapshotType string
		source       string
	}
	positionSnapshots []struct {
		position trading.Position
		source   string
	}
	reconciliation             ledger.ReconciliationRun
	inputs                     []ledger.ReconciliationInput
	breaks                     []ledger.ReconciliationBreak
	breakQuery                 ledger.ReconciliationBreakQuery
	performance                ledger.DailyPerformance
	performanceAccountID       string
	performanceTradeDate       string
	performanceSeries          []ledger.DailyPerformance
	performanceSeriesAccountID string
	performanceSeriesDateFrom  string
	performanceSeriesDateTo    string
	err                        error
}

func (store *fakeAccountAliasStore) AccountAliases(_ context.Context, accountIDs []string) (map[string]string, error) {
	if store.err != nil {
		return nil, store.err
	}
	store.aliasIDs = append([]string(nil), accountIDs...)
	aliases := make(map[string]string, len(store.aliases))
	for accountID, alias := range store.aliases {
		aliases[accountID] = alias
	}
	return aliases, nil
}

func (store *fakeAccountAliasStore) UpsertAccountAlias(_ context.Context, accountID string, brokerID string, alias string) error {
	if store.err != nil {
		return store.err
	}
	store.savedAccountID = accountID
	store.savedBrokerID = brokerID
	store.savedAlias = alias
	if store.aliases == nil {
		store.aliases = map[string]string{}
	}
	store.aliases[accountID] = alias
	return nil
}

func (store *fakeSettlementStore) UpsertAssetSnapshotForDate(_ context.Context, asset trading.Asset, tradeDate string, snapshotType string, source string, _ any, _ time.Time) error {
	if store.err != nil {
		return store.err
	}
	store.assetSnapshots = append(store.assetSnapshots, struct {
		asset        trading.Asset
		tradeDate    string
		snapshotType string
		source       string
	}{asset: asset, tradeDate: tradeDate, snapshotType: snapshotType, source: source})
	return nil
}

func (store *fakeSettlementStore) UpsertPositionSnapshot(_ context.Context, position trading.Position, source string, _ any, _ time.Time) error {
	if store.err != nil {
		return store.err
	}
	store.positionSnapshots = append(store.positionSnapshots, struct {
		position trading.Position
		source   string
	}{position: position, source: source})
	return nil
}

func (store *fakeSettlementStore) UpsertReconciliationRun(_ context.Context, run ledger.ReconciliationRun) (ledger.ReconciliationRun, error) {
	if store.err != nil {
		return ledger.ReconciliationRun{}, store.err
	}
	store.reconciliation = run
	return run, nil
}

func (store *fakeSettlementStore) UpsertReconciliationInput(_ context.Context, input ledger.ReconciliationInput) error {
	if store.err != nil {
		return store.err
	}
	store.inputs = append(store.inputs, input)
	return nil
}

func (store *fakeSettlementStore) UpsertReconciliationBreak(_ context.Context, item ledger.ReconciliationBreak) error {
	if store.err != nil {
		return store.err
	}
	store.breaks = append(store.breaks, item)
	return nil
}

func (store *fakeSettlementStore) ListReconciliationBreaks(_ context.Context, query ledger.ReconciliationBreakQuery) ([]ledger.ReconciliationBreak, error) {
	if store.err != nil {
		return nil, store.err
	}
	store.breakQuery = query
	return store.breaks, nil
}

func (store *fakeSettlementStore) GetDailyPerformance(_ context.Context, accountID string, tradeDate string) (ledger.DailyPerformance, error) {
	if store.err != nil {
		return ledger.DailyPerformance{}, store.err
	}
	store.performanceAccountID = accountID
	store.performanceTradeDate = tradeDate
	return store.performance, nil
}

func (store *fakeSettlementStore) ListDailyPerformance(_ context.Context, accountID string, dateFrom string, dateTo string) ([]ledger.DailyPerformance, error) {
	if store.err != nil {
		return nil, store.err
	}
	store.performanceSeriesAccountID = accountID
	store.performanceSeriesDateFrom = dateFrom
	store.performanceSeriesDateTo = dateTo
	return store.performanceSeries, nil
}

func (store *fakeSettlementStore) RawStreamSummary(_ context.Context, _ string, _ time.Time, _ time.Time) ([]ledger.RawStreamSummaryBucket, error) {
	if store.err != nil {
		return nil, store.err
	}
	return []ledger.RawStreamSummaryBucket{{Role: "event", MessageType: "event", EventType: "order.event", Count: 1}}, nil
}

func (store *fakeJobRunStore) UpsertJobRun(_ context.Context, run ledger.JobRun) (ledger.JobRun, error) {
	store.saved = run
	if store.err != nil {
		return ledger.JobRun{}, store.err
	}
	if run.RunID == "" {
		run.RunID = "run-1"
	}
	store.runs = append(store.runs, run)
	return run, nil
}

func (store *fakeJobRunStore) LatestJobRuns(_ context.Context, _ []string) ([]ledger.JobRun, error) {
	if store.err != nil {
		return nil, store.err
	}
	if len(store.runs) == 0 {
		return nil, ledger.ErrJobRunNotFound
	}
	return store.runs, nil
}

func (submitter *fakeOrderSubmitter) SubmitOrder(_ context.Context, req trading.SubmitOrderRequest, opts orderflow.SubmitOptions) (orderflow.SubmitOrderResult, error) {
	submitter.req = req
	submitter.requestID = opts.RequestID
	if submitter.err != nil {
		return orderflow.SubmitOrderResult{}, submitter.err
	}
	if submitter.result.MessageID == "" && !submitter.result.Replayed {
		return orderflow.SubmitOrderResult{}, errors.New("missing fake result")
	}
	return submitter.result, nil
}

func (submitter *fakeOrderSubmitter) BatchSubmitOrders(_ context.Context, req trading.BatchSubmitOrderRequest, opts orderflow.BatchSubmitOptions) (orderflow.BatchSubmitOrderResult, error) {
	submitter.batchReq = req
	submitter.batchRequestID = opts.RequestID
	if submitter.batchErr != nil {
		return orderflow.BatchSubmitOrderResult{}, submitter.batchErr
	}
	if submitter.batchResult.MessageID == "" && !submitter.batchResult.Replayed {
		return orderflow.BatchSubmitOrderResult{}, errors.New("missing fake batch result")
	}
	return submitter.batchResult, nil
}

func (submitter *fakeOrderSubmitter) CancelOrder(_ context.Context, req trading.CancelOrderRequest, opts orderflow.CancelOptions) (orderflow.CancelOrderResult, error) {
	submitter.cancelReq = req
	submitter.cancelRequestID = opts.RequestID
	if submitter.cancelErr != nil {
		return orderflow.CancelOrderResult{}, submitter.cancelErr
	}
	if submitter.cancelResult.MessageID == "" {
		return orderflow.CancelOrderResult{}, errors.New("missing fake cancel result")
	}
	return submitter.cancelResult, nil
}

func (submitter *fakeOrderSubmitter) ListOrders(_ context.Context, query trading.OrderQuery) (orderflow.ListOrdersResult, error) {
	submitter.orderQuery = query
	if submitter.listOrdersErr != nil {
		return orderflow.ListOrdersResult{}, submitter.listOrdersErr
	}
	return submitter.listOrdersResult, nil
}

func (submitter *fakeOrderSubmitter) ListFills(_ context.Context, query trading.FillQuery) (orderflow.ListFillsResult, error) {
	submitter.fillQuery = query
	if submitter.listFillsErr != nil {
		return orderflow.ListFillsResult{}, submitter.listFillsErr
	}
	return submitter.listFillsResult, nil
}

func (submitter *fakeOrderSubmitter) GetAsset(_ context.Context, accountID string) (orderflow.GetAssetResult, error) {
	submitter.assetAccountID = accountID
	if submitter.assetErr != nil {
		return orderflow.GetAssetResult{}, submitter.assetErr
	}
	return submitter.assetResult, nil
}

func (submitter *fakeOrderSubmitter) ListPositions(_ context.Context, query trading.PositionQuery) (orderflow.ListPositionsResult, error) {
	submitter.positionQuery = query
	if submitter.positionsErr != nil {
		return orderflow.ListPositionsResult{}, submitter.positionsErr
	}
	return submitter.positionsResult, nil
}

func (submitter *fakeOrderSubmitter) RefreshAsset(_ context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error) {
	submitter.refreshAssetAccountID = accountID
	submitter.refreshAssetRequestID = opts.RequestID
	if submitter.refreshAssetErr != nil {
		return orderflow.RefreshQueryResult{}, submitter.refreshAssetErr
	}
	return submitter.refreshAssetResult, nil
}

func (submitter *fakeOrderSubmitter) RefreshPositions(_ context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error) {
	submitter.refreshPositionsAccountID = accountID
	submitter.refreshPositionsRequestID = opts.RequestID
	if submitter.refreshPositionsErr != nil {
		return orderflow.RefreshQueryResult{}, submitter.refreshPositionsErr
	}
	return submitter.refreshPositionsResult, nil
}

func (submitter *fakeOrderSubmitter) RefreshOrders(_ context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error) {
	submitter.refreshOrdersAccountID = accountID
	submitter.refreshOrdersRequestID = opts.RequestID
	submitter.refreshOrdersTradeDate = opts.TradeDate
	if submitter.refreshOrdersErr != nil {
		return orderflow.RefreshQueryResult{}, submitter.refreshOrdersErr
	}
	return submitter.refreshOrdersResult, nil
}

func (submitter *fakeOrderSubmitter) RefreshFills(_ context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error) {
	submitter.refreshFillsAccountID = accountID
	submitter.refreshFillsRequestID = opts.RequestID
	submitter.refreshFillsTradeDate = opts.TradeDate
	if submitter.refreshFillsErr != nil {
		return orderflow.RefreshQueryResult{}, submitter.refreshFillsErr
	}
	return submitter.refreshFillsResult, nil
}
