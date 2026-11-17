package api

import (
	"context"
	"net/http"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
	"time"
)

func (s *Server) handleFuturesTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, httpx.CodeBadRequest, "read-only endpoint", nil)
		return
	}
	accounts := []futures.TerminalAccount{}
	if s.cfg.Service.Environment == config.EnvironmentTest && s.cfg.FuturesTerminal.ManifestFile != "" && s.futuresTerminal == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "CTP observation reader unavailable", nil)
		return
	}
	if s.cfg.Service.Environment == config.EnvironmentTest && s.futuresTerminal != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		accounts = s.futuresTerminal.Snapshot(ctx)
	}
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{"accounts": accounts, "mode": "query_observation_only", "trading_enabled": false, "cancel_enabled": false})
}
