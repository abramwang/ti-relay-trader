package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
)

type futuresCancelService interface {
	Cancel(context.Context, futures.CancelExecutionRequest) (futures.CancelExecutionResult, error)
	LookupCancel(context.Context, string, string) (futures.CancelExecutionResult, error)
}

func (s *Server) handleFuturesCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if !s.futuresExecutionAvailable(w, r, true) {
		return
	}
	service, ok := s.futuresOrders.(futuresCancelService)
	if !ok {
		s.writeFuturesExecutionError(w, r, futures.ErrOrderExecutionOff)
		return
	}
	if !validFuturesWriteRequest(r) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "JSON cancel intention required; cross-origin writes are not allowed", nil)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	request, decodeErr := futures.DecodeCancelExecutionRequest(raw)
	if err != nil || decodeErr != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid futures cancel intention", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result, err := service.Cancel(ctx, request)
	if result.Publication.CommandID > 0 {
		httpx.WriteOK(w, r, http.StatusAccepted, result)
		return
	}
	s.writeFuturesExecutionError(w, r, err)
}

func (s *Server) handleFuturesCancelCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if !s.futuresExecutionAvailable(w, r, false) {
		return
	}
	service, ok := s.futuresOrders.(futuresCancelService)
	if !ok {
		s.writeFuturesExecutionError(w, r, futures.ErrOrderExecutionOff)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["account_id"]) != 1 || len(q["idempotency_key"]) != 1 || q.Get("account_id") == "" || q.Get("idempotency_key") == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "account_id and idempotency_key are required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := service.LookupCancel(ctx, q.Get("account_id"), q.Get("idempotency_key"))
	if err != nil {
		s.writeFuturesExecutionError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}
