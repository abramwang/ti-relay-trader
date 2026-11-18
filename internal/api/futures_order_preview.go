package api

import (
	"context"
	"io"
	"net/http"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
	"ti-relay-trader/internal/httpx"
)

type futuresOrderPreview struct {
	Mode                     string                      `json:"mode"`
	Intent                   futures.OrderPreviewRequest `json:"intent"`
	Quote                    futuresPricePreview         `json:"quote"`
	AccountPermissionEnabled bool                        `json:"account_permission_enabled"`
	BrokerAcceptingSubmit    bool                        `json:"broker_accepting_submit"`
	PriceValid               bool                        `json:"price_valid"`
	CanSubmit                bool                        `json:"can_submit"`
	CommandPublished         bool                        `json:"command_published"`
	Blockers                 []string                    `json:"blockers"`
}

// Read-only despite POST: no order registration, Redis command or permit is
// created. A successful preview is never accepted by the trade publisher.
func (s *Server) handleFuturesOrderPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	intent, decodeErr := futures.DecodeOrderPreview(raw)
	if err != nil || decodeErr != nil || r.URL.RawQuery != "" || !futuresPriceSecurityID.MatchString(intent.SecurityID) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid futures preview intention", nil)
		return
	}
	// Only the existing TEST observation reader is integrated. Never consult
	// that reader in production, even if an account number happens to match.
	reader, ok := s.futuresTerminal.(interface {
		Heartbeats(context.Context) []futures.TerminalAccount
	})
	if s.cfg.Service.Environment != config.EnvironmentTest || !ok {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures account preview unavailable", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var account *futures.TerminalAccount
	for _, a := range reader.Heartbeats(ctx) {
		if a.AccountID == intent.AccountID && a.Route.AccountID == intent.AccountID && a.Route.Env == "test" {
			if account != nil {
				httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ambiguous futures account", nil)
				return
			}
			copy := a
			account = &copy
		}
	}
	if account == nil {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "futures account not found", nil)
		return
	}
	quote, stage := s.prepareFuturesPrice(ctx, intent.SecurityID, intent.TradeDate)
	if stage != "" {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "futures price preview unavailable", map[string]string{"stage": stage})
		return
	}
	result := futuresOrderPreview{Mode: "order_preview_only", Intent: intent, Quote: quote, Blockers: []string{"publication_not_connected"}}
	// Reuse the server account configuration, never the browser or OC's
	// accepting flags, to describe the Relay account switch.
	for _, a := range s.cfg.Accounts {
		if a.AccountID == intent.AccountID && a.BrokerID == account.Route.BrokerID && a.GatewayID == account.Route.GatewayID {
			result.AccountPermissionEnabled = a.Enabled && a.TradingEnabled
		}
	}
	if !result.AccountPermissionEnabled {
		result.Blockers = append(result.Blockers, "relay_account_read_only")
	}
	h := account.Heartbeat
	now := time.Now()
	result.BrokerAcceptingSubmit = account.ConnectionError == "" && h != nil &&
		h.SessionID != "" && h.ConnectionGeneration != nil && *h.ConnectionGeneration > 0 &&
		h.TradeDate != nil && *h.TradeDate == intent.TradeDate &&
		!h.ProducedAt.After(now) && now.Sub(h.ProducedAt) <= 5*time.Second &&
		h.RedisReady && h.BrokerReady && h.SettlementConfirmed &&
		h.RecoveryClear != nil && *h.RecoveryClear && h.AcceptingTradeCommands &&
		h.StateText != "stopping" && h.State != "STOPPED" && h.RuntimeBlockedReason == ""
	if !result.BrokerAcceptingSubmit {
		result.Blockers = append(result.Blockers, "broker_not_ready")
	}
	result.PriceValid = futures.ValidatePreviewPrice(intent, quote.Preparation.Market.PriceTick) == nil
	if !result.PriceValid {
		result.Blockers = append(result.Blockers, "price_off_tick")
	}
	if quote.Preparation.Market.ExchangeID != "SHFE" || intent.TradeSide != "B" || intent.OffsetType != "O" || intent.Qty != 1 {
		result.Blockers = append(result.Blockers, "outside_current_test_scope")
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}
