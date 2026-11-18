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

type futuresPricePreview struct {
	Mode                 string                           `json:"mode"`
	BusinessAuthorized   bool                             `json:"business_authorized"`
	CommandPublished     bool                             `json:"command_published"`
	PublicationSupported bool                             `json:"publication_supported"`
	CatalogBodySHA256    string                           `json:"catalog_body_sha256"`
	Preparation          futures.MeridianPricePreparation `json:"preparation"`
}

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
	preview, stage := s.prepareFuturesPrice(r.Context(), securityID, day)
	if stage != "" {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures price preview unavailable", map[string]string{"stage": stage})
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, preview)
}

func (s *Server) prepareFuturesPrice(parent context.Context, securityID, day string) (futuresPricePreview, string) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var result futuresPricePreview
	catalog, err := s.market.FuturesPriceCatalog(ctx, securityID)
	if err != nil {
		return result, "catalog_request"
	}
	mapping, tick, err := futures.ResolveMeridianPriceCatalog(catalog, securityID)
	if err != nil {
		return result, "catalog_validation"
	}
	raw, err := s.market.FuturesPriceSnapshot(ctx, securityID)
	if err != nil {
		return result, "snapshot_request"
	}
	prepared, err := futures.PrepareMeridianPrice(raw, mapping, tick, day, time.Now())
	if err != nil {
		return result, "snapshot_validation"
	}
	digest := sha256.Sum256(catalog)
	return futuresPricePreview{Mode: "price_preview_only", CatalogBodySHA256: hex.EncodeToString(digest[:]), Preparation: prepared}, ""
}
