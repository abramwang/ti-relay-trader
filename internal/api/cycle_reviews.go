package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

type CycleReviewStore interface {
	GetCycleReviewVersion(context.Context, string, string) (ledger.CycleReviewVersion, error)
}

func (s *Server) handleCycleReviews(w http.ResponseWriter, r *http.Request, account string) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := s.cfg.AccountRoute(account); !ok {
		httpx.WriteNotFound(w, r)
		return
	}
	q := r.URL.Query()
	for k, v := range q {
		if len(v) != 1 || (k != "date_from" && k != "date_to" && k != "version_id") {
			httpx.WriteError(w, r, 400, httpx.CodeBadRequest, "unsupported cycle review query", nil)
			return
		}
	}
	date := func(value string) (string, error) {
		if len(value) == 8 {
			value = value[:4] + "-" + value[4:6] + "-" + value[6:]
		}
		d, e := time.Parse("2006-01-02", value)
		if e != nil {
			return "", e
		}
		return d.Format("2006-01-02"), nil
	}
	from, e1 := date(q.Get("date_from"))
	to, e2 := date(q.Get("date_to"))
	version := q.Get("version_id")
	if e1 != nil || e2 != nil || from > to || len(version) > 160 || strings.TrimSpace(version) != version || strings.ContainsRune(version, 0) {
		httpx.WriteError(w, r, 400, httpx.CodeBadRequest, "valid date_from/date_to required", nil)
		return
	}
	if s.cycleReviews == nil {
		httpx.WriteError(w, r, 503, httpx.CodeUnavailable, "cycle review storage unavailable", nil)
		return
	}
	v, err := s.cycleReviews.GetCycleReviewVersion(r.Context(), account, version)
	if errors.Is(err, sql.ErrNoRows) {
		if version != "" {
			httpx.WriteNotFound(w, r)
			return
		}
		httpx.WriteOK(w, r, 200, map[string]any{"account_id": account, "available": false, "cycles": []any{}, "date_basis": "source_trade_date", "date_from": from, "date_to": to})
		return
	}
	var d performance.CycleReviewDocument
	if err == nil {
		err = json.Unmarshal(v.Payload, &d)
	}
	if err == nil {
		err = performance.ValidateCycleReview(d)
	}
	if err == nil && (d.AccountID != account || v.AccountID != account || d.VersionID != v.VersionID || (version != "" && version != v.VersionID) || d.DateFrom != v.DateFrom || d.ObservedThrough != v.ObservedThrough) {
		err = errors.New("cycle review scope mismatch")
	}
	if err != nil {
		s.logger.Warn("cycle_review_read_failed", "account_id", account, "error", err)
		httpx.WriteError(w, r, 503, httpx.CodeUnavailable, "cycle review unavailable", nil)
		return
	}
	rows := []performance.CycleReviewRow{}
	for _, row := range d.Rows {
		if row.SourceDate >= from && row.SourceDate <= to {
			rows = append(rows, row)
		}
	}
	httpx.WriteOK(w, r, 200, map[string]any{"account_id": account, "available": true, "version_id": v.VersionID, "published_at": v.CreatedAt, "payload_sha256": v.PayloadSHA256, "source_sha256": d.SourceSHA256, "coverage_from": d.DateFrom, "observed_through": d.ObservedThrough, "date_from": from, "date_to": to, "date_basis": "source_trade_date", "account_nav_publishable": false, "final_settlement": false, "cycles": rows, "version_cycle_count": len(d.Rows)})
}
