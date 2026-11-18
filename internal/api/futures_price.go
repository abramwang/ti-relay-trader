package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
)

var futuresPriceSecurityID = regexp.MustCompile(`^[A-Z]{1,3}[0-9]{4}\.(SHFE|CFFEX)$`)

func (s *Server) handleFuturesPricePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	// ParseQuery explicitly: malformed encodings must not silently lose fields.
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["security_id"]) != 1 || len(q["trade_date"]) != 1 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "security_id and trade_date are required; no other parameters", nil)
		return
	}
	securityID, day := q.Get("security_id"), q.Get("trade_date")
	parsed, err := time.Parse("20060102", day)
	if !futuresPriceSecurityID.MatchString(securityID) || err != nil || parsed.Format("20060102") != day {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "explicit CFFEX/SHFE contract and YYYYMMDD trade_date required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	unavailable := func(stage string) {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures price preview unavailable", map[string]string{"stage": stage})
	}
	catalog, err := s.market.FuturesPriceCatalog(ctx, securityID)
	if err != nil {
		unavailable("catalog_request")
		return
	}
	mapping, tick, err := futures.ResolveMeridianPriceCatalog(catalog, securityID)
	if err != nil {
		unavailable("catalog_validation")
		return
	}
	raw, err := s.market.FuturesPriceSnapshot(ctx, securityID)
	if err != nil {
		unavailable("snapshot_request")
		return
	}
	prepared, err := futures.PrepareMeridianPrice(raw, mapping, tick, day, time.Now())
	if err != nil {
		unavailable("snapshot_validation")
		return
	}
	digest := sha256.Sum256(catalog)
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{"mode": "price_preview_only", "business_authorized": false, "command_published": false, "publication_supported": false, "catalog_body_sha256": hex.EncodeToString(digest[:]), "preparation": prepared})
}
