package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
)

type commandHistoryStub struct {
	terminalStub
	historyCalls int
	account      string
	options      futures.CommandHistoryOptions
	err          error
}

func (s *commandHistoryStub) Commands(_ context.Context, a string, o futures.CommandHistoryOptions) (futures.CommandHistoryPage, error) {
	s.historyCalls++
	s.account, s.options = a, o
	return futures.CommandHistoryPage{Items: []futures.CommandHistoryItem{}}, s.err
}

func TestFuturesCommandsReadOnlyWithoutExecution(t *testing.T) {
	cfg := config.Default()
	cfg.Service.Environment = config.EnvironmentTest
	s := &commandHistoryStub{}
	h := NewWithDependencies(cfg, nil, Dependencies{FuturesTerminal: s})
	for _, query := range []string{"account_id=300052141", "account_id=300052141&limit=2&before_id=43"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/futures/commands?"+query, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"items":[]`) || s.calls != 0 || s.account != "300052141" {
			t.Fatal(w.Code, w.Body.String(), s)
		}
	}
	if s.options.Limit != 2 || s.options.BeforeID != 43 {
		t.Fatal(s.options)
	}
}

func TestFuturesCommandsRejectInvalidOrProductionRequests(t *testing.T) {
	for _, query := range []string{"", "account_id=", "account_id=300052141&account_id=300052141", "account_id=300052141&limit=0", "account_id=300052141&limit=101", "account_id=300052141&limit=1&limit=2", "account_id=300052141&before_id=-1", "account_id=300052141&before_id=0", "account_id=300052141&before_id=01", "account_id=300052141&before_id=9223372036854775808", "account_id=300052141&before_id=", "account_id=300052141&gateway_id=foreign", "account_id=300052141&before_id=%ZZ"} {
		cfg := config.Default()
		cfg.Service.Environment = config.EnvironmentTest
		s := &commandHistoryStub{}
		w := httptest.NewRecorder()
		NewWithDependencies(cfg, nil, Dependencies{FuturesTerminal: s}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/futures/commands?"+query, nil))
		if w.Code != 400 || s.historyCalls != 0 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	for _, method := range []string{"POST", "DELETE"} {
		cfg := config.Default()
		cfg.Service.Environment = config.EnvironmentTest
		s := &commandHistoryStub{}
		w := httptest.NewRecorder()
		NewWithDependencies(cfg, nil, Dependencies{FuturesTerminal: s}).ServeHTTP(w, httptest.NewRequest(method, "/v1/futures/commands?account_id=300052141", nil))
		if w.Code != 405 || s.historyCalls != 0 {
			t.Fatal(w.Code)
		}
	}
	for _, prod := range []bool{false, true} {
		cfg := config.Default()
		cfg.Service.Environment = config.EnvironmentTest
		s := &commandHistoryStub{}
		deps := Dependencies{}
		if prod {
			cfg.Service.Environment = config.EnvironmentProduction
			deps.FuturesTerminal = s
		}
		w := httptest.NewRecorder()
		NewWithDependencies(cfg, nil, deps).ServeHTTP(w, httptest.NewRequest("GET", "/v1/futures/commands?account_id=300052141", nil))
		if w.Code != 503 || s.historyCalls != 0 {
			t.Fatal(w.Code)
		}
	}
}

func TestFuturesCommandsErrorsDoNotBecomeEmptySuccess(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{futures.ErrOrderRequestMissing, 404}, {futures.ErrCommandHistoryRequest, 400},
		{futures.ErrStreamIdentity, 503}, {errors.New("private database detail"), 503},
	} {
		cfg := config.Default()
		cfg.Service.Environment = config.EnvironmentTest
		s := &commandHistoryStub{err: test.err}
		w := httptest.NewRecorder()
		NewWithDependencies(cfg, nil, Dependencies{FuturesTerminal: s}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/futures/commands?account_id=300052141", nil))
		if w.Code != test.status || strings.Contains(w.Body.String(), "private database detail") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
