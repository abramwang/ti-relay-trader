package api

import (
	"context"
	"net/http"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
	"time"
)

type futuresTerminalExecution interface {
	TerminalExecution(futures.TerminalAccount, time.Time) futures.TerminalExecutionView
}

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
	var submit, cancelEnabled bool
	policy, supportsPolicy := s.futuresOrders.(futuresTerminalExecution)
	for i := range accounts {
		accounts[i].TradingEnabled, accounts[i].CancelEnabled, accounts[i].Execution = false, false, nil
		if supportsPolicy && s.cfg.FuturesTerminal.ExecutionEnabled && s.cfg.Service.Environment == config.EnvironmentTest {
			view := policy.TerminalExecution(accounts[i], time.Now())
			accounts[i].Execution = &view
			accounts[i].TradingEnabled, accounts[i].CancelEnabled = view.SubmitEnabled, view.CancelEnabled
			submit, cancelEnabled = submit || view.SubmitEnabled, cancelEnabled || view.CancelEnabled
		}
	}
	mode := "query_observation_only"
	if submit || cancelEnabled {
		mode = "test_execution"
	}
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{"accounts": accounts, "mode": mode, "trading_enabled": submit, "cancel_enabled": cancelEnabled})
}
