package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
)

type futuresExecutionStub struct {
	result  futures.OrderExecutionResult
	err     error
	submits int
	lookups int
	request futures.OrderExecutionRequest
}

func (s *futuresExecutionStub) Submit(_ context.Context, r futures.OrderExecutionRequest) (futures.OrderExecutionResult, error) {
	s.submits++
	s.request = r
	return s.result, s.err
}
func (s *futuresExecutionStub) Lookup(_ context.Context, account, key string) (futures.OrderExecutionResult, error) {
	s.lookups++
	if account != "300052141" || key != "one-request" {
		return futures.OrderExecutionResult{}, futures.ErrOrderRequestMissing
	}
	return s.result, s.err
}

const futuresOrderRequestJSON = `{"idempotency_key":"one-request","order":{"account_id":"300052141","security_id":"RB2701.SHFE","trade_date":"20261117","trade_side":"B","offset_type":"O","hedge_type":"speculation","qty":1,"price":"3000.000"}}`

func TestFuturesExecutionHTTP(t *testing.T) {
	for _, scenario := range []string{"published", "unknown", "registered", "status", "status_switch_off", "conflict", "missing", "unavailable", "capture_unavailable", "close_unavailable", "production", "disabled", "switch_off", "injected_permission", "injected_cffex", "injected_position", "query_override", "duplicate_key", "number_price", "bad_method", "cross_origin", "form_body", "bad_status_query", "extra_status_query", "duplicate_status_query"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := config.Default()
			cfg.Service.Environment = config.EnvironmentTest
			cfg.FuturesTerminal.ExecutionEnabled = true
			stub := &futuresExecutionStub{result: futures.OrderExecutionResult{MessageID: "registered-message", Publication: futures.QueryPublication{CommandID: 1, State: "published"}}}
			intention, err := futures.DecodeOrderExecutionRequest([]byte(futuresOrderRequestJSON))
			if err != nil {
				t.Fatal(err)
			}
			stub.result.Order = intention.Order
			stub.result.IdempotencyKey = intention.IdempotencyKey
			deps := Dependencies{FuturesOrders: stub}
			path, method, body := "/v1/futures/orders", http.MethodPost, futuresOrderRequestJSON
			contentType, origin := "application/json", ""
			want, submits, lookups := 202, 1, 0
			switch scenario {
			case "unknown":
				stub.result.Publication.State = "attempted"
				stub.err = futures.ErrQueryPublishUnknown
			case "registered":
				stub.result.Publication.State = "registered"
				stub.err = futures.ErrQueryPublishGate
			case "conflict":
				stub.result, stub.err, want = futures.OrderExecutionResult{}, futures.ErrOrderRequestConflict, 409
			case "missing":
				stub.result, stub.err, want = futures.OrderExecutionResult{}, futures.ErrOrderRequestMissing, 404
			case "unavailable":
				stub.result, stub.err, want = futures.OrderExecutionResult{}, errors.New("private-database-address"), 503
			case "capture_unavailable":
				stub.result, stub.err, want = futures.OrderExecutionResult{}, futures.ErrCaptureUnavailable, 503
			case "close_unavailable":
				stub.result, stub.err, want = futures.OrderExecutionResult{}, futures.ErrCloseReservation, 503
			case "production":
				cfg.Service.Environment = config.EnvironmentProduction
				want, submits = 503, 0
			case "disabled":
				deps.FuturesOrders = nil
				want, submits = 503, 0
			case "switch_off":
				cfg.FuturesTerminal.ExecutionEnabled = false
				want, submits = 503, 0
			case "injected_permission":
				body = strings.Replace(body, `"order":`, `"submit_enabled":true,"order":`, 1)
				want, submits = 400, 0
			case "injected_cffex":
				body = strings.Replace(body, `"order":`, `"cffex":{"products":["IF"],"max_order_qty":100},"order":`, 1)
				want, submits = 400, 0
			case "injected_position":
				body = strings.Replace(body, `"order":`, `"position_command_id":123,"order":`, 1)
				want, submits = 400, 0
			case "query_override":
				path += "?env=test"
				want, submits = 400, 0
			case "duplicate_key":
				body = strings.Replace(body, `"idempotency_key":`, `"idempotency_key":"duplicate","idempotency_key":`, 1)
				want, submits = 400, 0
			case "number_price":
				body = strings.Replace(body, `"3000.000"`, `3000.000`, 1)
				want, submits = 400, 0
			case "bad_method":
				method, want, submits = http.MethodGet, 405, 0
			case "cross_origin":
				origin, want, submits = "https://other.example", 400, 0
			case "form_body":
				contentType, want, submits = "text/plain", 400, 0
			case "status", "status_switch_off", "bad_status_query", "extra_status_query", "duplicate_status_query":
				method, path, body = http.MethodGet, "/v1/futures/order-command?account_id=300052141&idempotency_key=one-request", ""
				want, submits, lookups = 200, 0, 1
				if scenario == "status_switch_off" {
					cfg.FuturesTerminal.ExecutionEnabled = false
				}
				if scenario != "status" && scenario != "status_switch_off" {
					want, lookups = 400, 0
					switch scenario {
					case "bad_status_query":
						path += "%ff%"
					case "extra_status_query":
						path += "&session=old"
					case "duplicate_status_query":
						path += "&idempotency_key=second"
					}
				}
			}
			h := NewWithDependencies(cfg, nil, deps)
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", contentType)
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want || stub.submits != submits || stub.lookups != lookups {
				t.Fatal(w.Code, w.Body.String(), stub.submits, stub.lookups)
			}
			if scenario == "capture_unavailable" && !strings.Contains(w.Body.String(), "reply/event receiver is unavailable") {
				t.Fatal(w.Body.String())
			}
			if scenario == "close_unavailable" && !strings.Contains(w.Body.String(), "position baseline requires reconciliation") {
				t.Fatal(w.Body.String())
			}
			if w.Code != 405 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
			if strings.Contains(w.Body.String(), "private-database-address") {
				t.Fatal("leaked infrastructure error")
			}
			if want == 202 || want == 200 {
				var envelope struct {
					Data futures.OrderExecutionResult `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Data.BrokerStateKnown || envelope.Data.RetrySafe || envelope.Data.Publication.BrokerAccepted || envelope.Data.Publication.State != stub.result.Publication.State {
					t.Fatal(w.Body.String(), err)
				}
			}
			if submits > 0 && stub.request.Order.Price != "3000.000" {
				t.Fatal("price changed", stub.request)
			}
		})
	}
}
