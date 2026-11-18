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

type futuresCancelStub struct {
	futuresExecutionStub
	result         futures.CancelExecutionResult
	err            error
	cancels, reads int
}

func (s *futuresCancelStub) Cancel(_ context.Context, r futures.CancelExecutionRequest) (futures.CancelExecutionResult, error) {
	s.cancels++
	s.result.CancelExecutionRequest = r
	return s.result, s.err
}

func (s *futuresCancelStub) LookupCancel(_ context.Context, account, key string) (futures.CancelExecutionResult, error) {
	s.reads++
	if account != "300052141" || key != "cancel-1" {
		return futures.CancelExecutionResult{}, futures.ErrOrderRequestMissing
	}
	return s.result, s.err
}

func TestFuturesCancelHTTP(t *testing.T) {
	for _, scenario := range []string{"published", "unknown", "registered", "status", "status_disabled", "disabled", "production", "missing_service", "submit_only_service", "conflict", "missing", "error", "injected_receipt", "duplicate_key", "extra_query", "cross_origin", "form", "bad_method", "status_extra", "status_duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := config.Default()
			cfg.Service.Environment = config.EnvironmentTest
			cfg.FuturesTerminal.ExecutionEnabled = true
			stub := &futuresCancelStub{result: futures.CancelExecutionResult{MessageID: "cancel-message", CancelID: "cancel-id", Publication: futures.QueryPublication{CommandID: 2, State: "published"}}}
			deps := Dependencies{FuturesOrders: stub}
			path, method := "/v1/futures/cancels", http.MethodPost
			body := `{"idempotency_key":"cancel-1","account_id":"300052141","gateway_order_id":"local-order","trade_date":"20261119"}`
			contentType, origin := "application/json", ""
			want, cancels, reads := 202, 1, 0
			switch scenario {
			case "unknown":
				stub.result.Publication.State, stub.err = "attempted", futures.ErrQueryPublishUnknown
			case "registered":
				stub.result.Publication.State, stub.err = "registered", futures.ErrQueryPublishGate
			case "disabled":
				cfg.FuturesTerminal.ExecutionEnabled = false
				want, cancels = 503, 0
			case "production":
				cfg.Service.Environment = config.EnvironmentProduction
				want, cancels = 503, 0
			case "missing_service":
				deps.FuturesOrders = nil
				want, cancels = 503, 0
			case "submit_only_service":
				deps.FuturesOrders = &futuresExecutionStub{}
				want, cancels = 503, 0
			case "conflict":
				stub.result, stub.err, want = futures.CancelExecutionResult{}, futures.ErrOrderRequestConflict, 409
			case "missing":
				stub.result, stub.err, want = futures.CancelExecutionResult{}, futures.ErrOrderRequestMissing, 404
			case "error":
				stub.result, stub.err, want = futures.CancelExecutionResult{}, errors.New("private-db-secret"), 503
			case "injected_receipt":
				body = strings.Replace(body, `"account_id":`, `"receipt_id":12,"account_id":`, 1)
				want, cancels = 400, 0
			case "duplicate_key":
				body = strings.Replace(body, `"idempotency_key":`, `"idempotency_key":"other","idempotency_key":`, 1)
				want, cancels = 400, 0
			case "extra_query":
				path += "?session=old"
				want, cancels = 400, 0
			case "cross_origin":
				origin = "https://other.example"
				want, cancels = 400, 0
			case "form":
				contentType = "text/plain"
				want, cancels = 400, 0
			case "bad_method":
				method = http.MethodGet
				want, cancels = 405, 0
			case "status", "status_disabled", "status_extra", "status_duplicate":
				method, body, path = http.MethodGet, "", "/v1/futures/cancel-command?account_id=300052141&idempotency_key=cancel-1"
				want, cancels, reads = 200, 0, 1
				if scenario == "status_disabled" {
					cfg.FuturesTerminal.ExecutionEnabled = false
				}
				if scenario == "status_extra" {
					path += "&session=old"
					want, reads = 400, 0
				}
				if scenario == "status_duplicate" {
					path += "&idempotency_key=other"
					want, reads = 400, 0
				}
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", contentType)
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			NewWithDependencies(cfg, nil, deps).ServeHTTP(w, r)
			if w.Code != want || stub.cancels != cancels || stub.reads != reads || stub.submits != 0 {
				t.Fatal(w.Code, w.Body.String(), stub.cancels, stub.reads)
			}
			if strings.Contains(w.Body.String(), "private-db-secret") {
				t.Fatal("private error leaked")
			}
			if want != 405 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
			if want == 200 || want == 202 {
				var envelope struct {
					Data futures.CancelExecutionResult `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Data.BrokerStateKnown || envelope.Data.RetrySafe || envelope.Data.Publication.BrokerAccepted || envelope.Data.Publication.State != stub.result.Publication.State {
					t.Fatal(w.Body.String(), err)
				}
			}
		})
	}
}
