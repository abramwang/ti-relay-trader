package api

import (
	"log/slog"
	"net/http"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/trading"
)

type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	started time.Time
}

func New(cfg config.Config, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		cfg:     cfg,
		logger:  logger,
		started: time.Now().UTC(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/v1/status", server.handleStatus)
	mux.HandleFunc("/v1/schema", server.handleSchema)
	mux.HandleFunc("/v1/accounts", server.handleAccounts)
	mux.HandleFunc("/", server.handleNotFound)

	return httpx.RequestLogger(logger)(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}

	httpx.WriteOK(w, r, http.StatusOK, s.statusPayload("ok"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}

	httpx.WriteOK(w, r, http.StatusOK, s.statusPayload("ok"))
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}

	httpx.WriteOK(w, r, http.StatusOK, trading.Catalog())
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}

	accounts := make([]AccountView, 0, len(s.cfg.Accounts))
	for _, account := range s.cfg.Accounts {
		accounts = append(accounts, AccountView{
			AccountID:      account.AccountID,
			BrokerID:       account.BrokerID,
			GatewayID:      account.GatewayID,
			Enabled:        account.Enabled,
			TradingEnabled: account.TradingEnabled,
			Simulated:      account.Simulated,
		})
	}

	httpx.WriteOK(w, r, http.StatusOK, map[string]any{
		"source":   "config",
		"accounts": accounts,
	})
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteNotFound(w, r)
}

func (s *Server) statusPayload(status string) StatusView {
	return StatusView{
		Service:            "relay-api",
		Mode:               string(config.ModeAPI),
		Status:             status,
		PublicURL:          s.cfg.Service.PublicURL,
		StartedAt:          s.started,
		UptimeSeconds:      int64(time.Since(s.started).Seconds()),
		AccountsConfigured: len(s.cfg.Accounts),
		JobsConfigured:     len(s.cfg.Jobs),
	}
}

type StatusView struct {
	Service            string    `json:"service"`
	Mode               string    `json:"mode"`
	Status             string    `json:"status"`
	PublicURL          string    `json:"public_url"`
	StartedAt          time.Time `json:"started_at"`
	UptimeSeconds      int64     `json:"uptime_seconds"`
	AccountsConfigured int       `json:"accounts_configured"`
	JobsConfigured     int       `json:"jobs_configured"`
}

type AccountView struct {
	AccountID      string `json:"account_id"`
	BrokerID       string `json:"broker_id"`
	GatewayID      string `json:"gateway_id"`
	Enabled        bool   `json:"enabled"`
	TradingEnabled bool   `json:"trading_enabled"`
	Simulated      bool   `json:"simulated"`
}
