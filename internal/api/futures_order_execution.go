package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
)

func (s *Server) futuresExecutionAvailable(w http.ResponseWriter, r *http.Request, write bool) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.Service.Environment != config.EnvironmentTest || (write && !s.cfg.FuturesTerminal.ExecutionEnabled) || s.futuresOrders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures TEST execution service is not enabled", nil)
		return false
	}
	return true
}

func (s *Server) handleFuturesOrderSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if !s.futuresExecutionAvailable(w, r, true) {
		return
	}
	if !validFuturesWriteRequest(r) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "JSON order intention required; cross-origin writes are not allowed", nil)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	request, decodeErr := futures.DecodeOrderExecutionRequest(raw)
	if err != nil || decodeErr != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid futures order intention", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result, err := s.futuresOrders.Submit(ctx, request)
	if result.Publication.CommandID > 0 {
		// HTTP 202 acknowledges durable registration only. In particular an
		// unknown publication is returned with its identity, not hidden by 500.
		httpx.WriteOK(w, r, http.StatusAccepted, result)
		return
	}
	s.writeFuturesExecutionError(w, r, err)
}

func validFuturesWriteRequest(r *http.Request) bool {
	typ, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	origin, originErr := url.Parse(r.Header.Get("Origin"))
	return mediaErr == nil && typ == "application/json" && r.URL.RawQuery == "" && (r.Header.Get("Origin") == "" || (originErr == nil && origin.Host == r.Host && (origin.Scheme == "http" || origin.Scheme == "https")))
}

func (s *Server) handleFuturesOrderCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if !s.futuresExecutionAvailable(w, r, false) {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["account_id"]) != 1 || len(q["idempotency_key"]) != 1 || q.Get("account_id") == "" || q.Get("idempotency_key") == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "account_id and idempotency_key are required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := s.futuresOrders.Lookup(ctx, q.Get("account_id"), q.Get("idempotency_key"))
	if err != nil {
		s.writeFuturesExecutionError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) writeFuturesExecutionError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures execution unavailable; query the same request key before taking further action"
	switch {
	case errors.Is(err, futures.ErrOrderRequestConflict):
		status, code, message = http.StatusConflict, httpx.CodeConflict, "idempotency key belongs to a different order intention"
	case errors.Is(err, futures.ErrOrderRequestMissing):
		status, code, message = http.StatusNotFound, httpx.CodeNotFound, "futures order request not found"
	case errors.Is(err, futures.ErrStreamIdentity):
		status, code, message = http.StatusBadRequest, httpx.CodeBadRequest, "invalid futures order identity or price evidence"
	}
	httpx.WriteError(w, r, status, code, message, nil)
}
