package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/ledger"
)

type positionBaselineStore interface {
	GetPositionBaseline(context.Context, ledger.PositionBaselineQuery) (ledger.PositionBaselinePage, error)
}

func (s *Server) handlePositionBaseline(w http.ResponseWriter, r *http.Request, accountID string) {
	route, exists := s.cfg.AccountRoute(accountID)
	if !exists {
		httpx.WriteNotFound(w, r)
		return
	}
	store, ok := s.settles.(positionBaselineStore)
	if !ok {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "position baseline store unavailable", nil)
		return
	}
	values := r.URL.Query()
	for key := range values {
		switch key {
		case "trade_date", "snapshot_type", "baseline_id", "cursor", "limit":
		default:
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "unsupported position baseline query field", key)
			return
		}
		if len(values[key]) != 1 {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "duplicate query field", key)
			return
		}
	}
	dateValue := strings.TrimSpace(values.Get("trade_date"))
	date, err := normalizeAPIDate(dateValue)
	if dateValue == "" || err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "explicit historical trade_date required", nil)
		return
	}
	kind := strings.TrimSpace(values.Get("snapshot_type"))
	if kind == "" {
		kind = "close"
	}
	if kind != "close" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "only historical close baselines are supported", nil)
		return
	}
	limit := 500
	if values.Has("limit") {
		limit, err = strconv.Atoi(values.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 500 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "limit must be 1..500", nil)
		return
	}
	page, err := store.GetPositionBaseline(r.Context(), ledger.PositionBaselineQuery{
		Environment: string(s.cfg.Service.Environment), AccountID: accountID, BrokerID: route.BrokerID, GatewayID: route.GatewayID, TradeDate: date, SnapshotType: kind, Limit: limit, BaselineID: strings.TrimSpace(values.Get("baseline_id")), Cursor: strings.TrimSpace(values.Get("cursor")),
	})
	if err != nil {
		switch {
		case errors.Is(err, ledger.ErrPositionBaselineChanged):
			httpx.WriteError(w, r, http.StatusConflict, "POSITION_BASELINE_CHANGED", err.Error(), nil)
		case errors.Is(err, ledger.ErrPositionBaselineTooLarge):
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "POSITION_BASELINE_TOO_LARGE", err.Error(), map[string]int{"max_rows": ledger.PositionBaselineMaxRows})
		case errors.Is(err, ledger.ErrInvalidLedgerInput):
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		default:
			httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "position baseline read failed", nil)
		}
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, page)
}
