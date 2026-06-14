package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/events"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/trading"
)

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
	cfg := config.Default()
	cfg.Service.Mode = config.ModeDocs
	cfg.Database.DSN = "postgres://configured"
	cfg.Redis.URL = "redis://configured"
	cfg.Accounts = []config.AccountRouteConfig{
		{AccountID: "acct-1", BrokerID: "huaxin", GatewayID: "gw-1", StreamPrefix: "relay:prod:v1:huaxin:gw-1", Enabled: true, TradingEnabled: true},
		{AccountID: "acct-2", BrokerID: "huaxin", GatewayID: "gw-2", StreamPrefix: "relay:prod:v1:huaxin:gw-2", Enabled: true, Simulated: true},
	}
	dbPinged := false
	redisPinged := false
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
		Orders: &fakeOrderSubmitter{},
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
	if !dbPinged || !redisPinged {
		t.Fatalf("dependency pings db=%v redis=%v", dbPinged, redisPinged)
	}
	if envelope.Data.Accounts.Configured != 2 || envelope.Data.Accounts.Enabled != 2 || envelope.Data.Accounts.TradingEnabled != 1 || envelope.Data.Accounts.Simulated != 1 {
		t.Fatalf("account summary = %#v", envelope.Data.Accounts)
	}
	if envelope.Data.Dependencies["database"].Status != "ok" || envelope.Data.Dependencies["redis"].Status != "ok" {
		t.Fatalf("dependencies = %#v", envelope.Data.Dependencies)
	}
}

func TestStatusDegradedAndDoesNotLeakDependencyErrors(t *testing.T) {
	cfg := config.Default()
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

func TestAccountsFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID:      "acct-1",
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
	handler := NewWithDependencies(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{
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
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/acct-1/orders/refresh", nil)
	req.Header.Set("X-Request-ID", "req-refresh-orders")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.refreshOrdersAccountID != "acct-1" || service.refreshOrdersRequestID != "req-refresh-orders" {
		t.Fatalf("refresh orders call = %q/%q", service.refreshOrdersAccountID, service.refreshOrdersRequestID)
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
	req := httptest.NewRequest(http.MethodPost, "/v1/accounts/acct-1/fills/refresh", nil)
	req.Header.Set("X-Request-ID", "req-refresh-fills")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if service.refreshFillsAccountID != "acct-1" || service.refreshFillsRequestID != "req-refresh-fills" {
		t.Fatalf("refresh fills call = %q/%q", service.refreshFillsAccountID, service.refreshFillsRequestID)
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
	if !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("response missing count: %s", rec.Body.String())
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
	if !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("response missing count: %s", rec.Body.String())
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
	refreshOrdersResult       orderflow.RefreshQueryResult
	refreshOrdersErr          error
	refreshFillsAccountID     string
	refreshFillsRequestID     string
	refreshFillsResult        orderflow.RefreshQueryResult
	refreshFillsErr           error
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
	if submitter.refreshOrdersErr != nil {
		return orderflow.RefreshQueryResult{}, submitter.refreshOrdersErr
	}
	return submitter.refreshOrdersResult, nil
}

func (submitter *fakeOrderSubmitter) RefreshFills(_ context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error) {
	submitter.refreshFillsAccountID = accountID
	submitter.refreshFillsRequestID = opts.RequestID
	if submitter.refreshFillsErr != nil {
		return orderflow.RefreshQueryResult{}, submitter.refreshFillsErr
	}
	return submitter.refreshFillsResult, nil
}
