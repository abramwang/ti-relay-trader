package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/performance"
)

type ResearchCostService interface {
	ListVersions(context.Context, string) ([]performance.ResearchCostVersionInfo, error)
	Compare(context.Context, string, string) (performance.ResearchCostComparison, error)
}

func (s *Server) handleResearchCosts(w http.ResponseWriter, r *http.Request, account string, list bool) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := s.cfg.AccountRoute(account); !ok {
		httpx.WriteNotFound(w, r)
		return
	}
	if s.researchCosts == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "research cost storage unavailable", nil)
		return
	}
	query := r.URL.Query()
	for key, values := range query {
		if list || key != "version_id" || len(values) != 1 {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "unsupported research query", nil)
			return
		}
	}
	var result any
	var err error
	if list {
		var versions []performance.ResearchCostVersionInfo
		versions, err = s.researchCosts.ListVersions(r.Context(), account)
		result = map[string]any{"account_id": account, "versions": versions, "research_only": true}
	} else {
		version := query.Get("version_id")
		if version == "" || strings.TrimSpace(version) != version || len(version) > 160 || strings.ContainsRune(version, 0) {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "explicit version_id required", nil)
			return
		}
		var comparison performance.ResearchCostComparison
		comparison, err = s.researchCosts.Compare(r.Context(), account, version)
		if err == nil && (comparison.AccountID != account || comparison.VersionID != version || !comparison.ResearchOnly || comparison.NAVPublishable) {
			err = errors.New("research response scope mismatch")
		}
		result = map[string]any{"comparison": comparison}
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteNotFound(w, r)
			return
		}
		s.logger.Warn("research_cost_read_failed", "account_id", account, "error", err)
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "research cost comparison unavailable", nil)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}
