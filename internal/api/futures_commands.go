package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
)

type futuresCommandHistoryReader interface {
	Commands(context.Context, string, futures.CommandHistoryOptions) (futures.CommandHistoryPage, error)
}

func (s *Server) handleFuturesCommands(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	reader, ok := s.futuresTerminal.(futuresCommandHistoryReader)
	if s.cfg.Service.Environment != config.EnvironmentTest || !ok {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures command history unavailable", nil)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	options := futures.CommandHistoryOptions{Limit: 50}
	valid := err == nil && len(q["account_id"]) == 1 && q.Get("account_id") != ""
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			valid = false
		}
		switch key {
		case "account_id":
		case "before_id", "limit":
			n, parseErr := strconv.ParseInt(q.Get(key), 10, 64)
			if parseErr != nil || n < 1 || strconv.FormatInt(n, 10) != q.Get(key) {
				valid = false
			}
			if key == "before_id" {
				options.BeforeID = n
			} else if n > 100 {
				valid = false
			} else {
				options.Limit = int(n)
			}
		default:
			valid = false
		}
	}
	if !valid || options.Validate() != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "account_id, optional positive before_id and limit (1..100) required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := reader.Commands(ctx, q.Get("account_id"), options)
	if err != nil {
		switch {
		case errors.Is(err, futures.ErrCommandHistoryRequest):
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid futures command history request", nil)
		case errors.Is(err, futures.ErrOrderRequestMissing):
			httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "configured futures account not found", nil)
		case errors.Is(err, futures.ErrStreamIdentity):
			httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures command history could not be validated", nil)
		default:
			httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures command history unavailable", nil)
		}
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, page)
}
