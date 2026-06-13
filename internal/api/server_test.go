package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
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

func TestMethodNotAllowed(t *testing.T) {
	handler := New(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
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
	req              trading.SubmitOrderRequest
	requestID        string
	result           orderflow.SubmitOrderResult
	err              error
	cancelReq        trading.CancelOrderRequest
	cancelRequestID  string
	cancelResult     orderflow.CancelOrderResult
	cancelErr        error
	orderQuery       trading.OrderQuery
	listOrdersResult orderflow.ListOrdersResult
	listOrdersErr    error
	fillQuery        trading.FillQuery
	listFillsResult  orderflow.ListFillsResult
	listFillsErr     error
}

func (submitter *fakeOrderSubmitter) SubmitOrder(_ context.Context, req trading.SubmitOrderRequest, opts orderflow.SubmitOptions) (orderflow.SubmitOrderResult, error) {
	submitter.req = req
	submitter.requestID = opts.RequestID
	if submitter.err != nil {
		return orderflow.SubmitOrderResult{}, submitter.err
	}
	if submitter.result.MessageID == "" {
		return orderflow.SubmitOrderResult{}, errors.New("missing fake result")
	}
	return submitter.result, nil
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
