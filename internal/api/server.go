package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/events"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/trading"
)

type Dependencies struct {
	Orders OrderService
	Market *market.MeridianClient
	Events *events.Hub
}

type OrderService interface {
	SubmitOrder(ctx context.Context, req trading.SubmitOrderRequest, opts orderflow.SubmitOptions) (orderflow.SubmitOrderResult, error)
	BatchSubmitOrders(ctx context.Context, req trading.BatchSubmitOrderRequest, opts orderflow.BatchSubmitOptions) (orderflow.BatchSubmitOrderResult, error)
	CancelOrder(ctx context.Context, req trading.CancelOrderRequest, opts orderflow.CancelOptions) (orderflow.CancelOrderResult, error)
	ListOrders(ctx context.Context, query trading.OrderQuery) (orderflow.ListOrdersResult, error)
	ListFills(ctx context.Context, query trading.FillQuery) (orderflow.ListFillsResult, error)
	GetAsset(ctx context.Context, accountID string) (orderflow.GetAssetResult, error)
	ListPositions(ctx context.Context, query trading.PositionQuery) (orderflow.ListPositionsResult, error)
	RefreshAsset(ctx context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error)
	RefreshPositions(ctx context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error)
}

type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	started time.Time
	orders  OrderService
	market  *market.MeridianClient
	events  *events.Hub
}

func New(cfg config.Config, logger *slog.Logger) http.Handler {
	return NewWithDependencies(cfg, logger, Dependencies{})
}

func NewWithDependencies(cfg config.Config, logger *slog.Logger, deps Dependencies) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	marketClient := deps.Market
	if marketClient == nil {
		var err error
		marketClient, err = market.NewMeridianClient(cfg.Market)
		if err != nil {
			logger.Warn("meridian_client_unavailable", "error", err)
		}
	}

	server := &Server{
		cfg:     cfg,
		logger:  logger,
		started: time.Now().UTC(),
		orders:  deps.Orders,
		market:  marketClient,
		events:  deps.Events,
	}
	if server.events == nil {
		server.events = events.NewHub()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/v1/status", server.handleStatus)
	mux.HandleFunc("/v1/schema", server.handleSchema)
	mux.HandleFunc("/v1/accounts", server.handleAccounts)
	mux.HandleFunc("/v1/accounts/", server.handleAccountPath)
	mux.HandleFunc("/v1/meridian/metadata/instruments", server.handleMeridianMetadataInstruments)
	mux.HandleFunc("/v1/meridian/market/snapshots", server.handleMeridianMarketSnapshots)
	mux.HandleFunc("/v1/events/stream", server.handleEventsStream)
	mux.HandleFunc("/v1/orders", server.handleOrders)
	mux.HandleFunc("/v1/orders/", server.handleOrderPath)
	mux.HandleFunc("/v1/fills", server.handleFills)
	mux.HandleFunc("/", server.handleNotFound)

	return httpx.RequestLogger(logger)(mux)
}

func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "streaming is not supported", nil)
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	ch, unsubscribe := s.events.Subscribe(r.Context(), events.Subscription{
		AccountID: accountID,
		Buffer:    64,
	})
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = io.WriteString(w, "retry: 3000\n\n")
	if err := writeSSEEvent(w, events.Event{
		ID:         "connected",
		Type:       events.TypeConnected,
		AccountIDs: accountIDsForFilter(accountID),
		Time:       time.Now().UTC(),
		Source:     "relay-api",
		Data: map[string]any{
			"account_id": accountID,
			"request_id": httpx.RequestID(r),
		},
	}); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
		case now := <-heartbeat.C:
			if err := writeSSEEvent(w, events.Event{
				Type:       events.TypeHeartbeat,
				AccountIDs: accountIDsForFilter(accountID),
				Time:       now.UTC(),
				Source:     "relay-api",
			}); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSEEvent(w io.Writer, event events.Event) error {
	if event.Type == "" {
		event.Type = "relay.event"
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if event.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
		return err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func accountIDsForFilter(accountID string) []string {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil
	}
	return []string{accountID}
}

func (s *Server) handleMeridianMetadataInstruments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.market == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "meridian market client is unavailable", nil)
		return
	}
	response, err := s.market.MetadataInstruments(r.Context(), r.URL.Query())
	if err != nil {
		s.logger.Warn("meridian_metadata_instruments_failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeUnavailable, "meridian metadata request failed", err.Error())
		return
	}
	s.writeMeridianResponse(w, r, response, "meridian metadata request failed")
}

func (s *Server) handleMeridianMarketSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.market == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "meridian market client is unavailable", nil)
		return
	}
	response, err := s.market.MarketSnapshots(r.Context(), r.URL.Query())
	if err != nil {
		s.logger.Warn("meridian_market_snapshots_failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeUnavailable, "meridian market request failed", err.Error())
		return
	}
	s.writeMeridianResponse(w, r, response, "meridian market request failed")
}

func (s *Server) writeMeridianResponse(w http.ResponseWriter, r *http.Request, response market.MeridianResponse, message string) {
	if response.StatusCode >= http.StatusBadRequest {
		code := httpx.CodeBadRequest
		if response.StatusCode >= http.StatusInternalServerError {
			code = httpx.CodeUnavailable
		}
		httpx.WriteError(w, r, response.StatusCode, code, message, response.Payload)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, response.Payload)
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

func (s *Server) handleAccountPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/accounts/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 && len(parts) != 3 {
		httpx.WriteNotFound(w, r)
		return
	}
	accountID, err := url.PathUnescape(parts[0])
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid account_id path", err.Error())
		return
	}
	switch parts[1] {
	case "asset":
		if len(parts) == 3 {
			if parts[2] != "refresh" {
				httpx.WriteNotFound(w, r)
				return
			}
			if r.Method != http.MethodPost {
				httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
				return
			}
			s.handleRefreshAsset(w, r, accountID)
			return
		}
		if r.Method != http.MethodGet {
			httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
			return
		}
		s.handleAccountAsset(w, r, accountID)
	case "positions":
		if len(parts) == 3 {
			if parts[2] != "refresh" {
				httpx.WriteNotFound(w, r)
				return
			}
			if r.Method != http.MethodPost {
				httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
				return
			}
			s.handleRefreshPositions(w, r, accountID)
			return
		}
		if r.Method != http.MethodGet {
			httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
			return
		}
		s.handleAccountPositions(w, r, accountID)
	default:
		httpx.WriteNotFound(w, r)
	}
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleSubmitOrder(w, r)
	case http.MethodGet:
		s.handleListOrders(w, r)
	default:
		httpx.WriteMethodNotAllowed(w, r, "GET, POST")
	}
}

func (s *Server) handleOrderPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/orders/")
	path = strings.Trim(path, "/")
	if path == "batch" {
		if r.Method != http.MethodPost {
			httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
			return
		}
		s.handleBatchSubmitOrders(w, r)
		return
	}

	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
			return
		}
		s.handleCancelOrder(w, r, parts[0])
		return
	}

	httpx.WriteNotFound(w, r)
}

func (s *Server) handleSubmitOrder(w http.ResponseWriter, r *http.Request) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}

	defer r.Body.Close()
	var req trading.SubmitOrderRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body", err.Error())
		return
	}

	result, err := s.orders.SubmitOrder(r.Context(), req, orderflow.SubmitOptions{
		RequestID: httpx.RequestID(r),
	})
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}

	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleBatchSubmitOrders(w http.ResponseWriter, r *http.Request) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}

	defer r.Body.Close()
	var req trading.BatchSubmitOrderRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body", err.Error())
		return
	}

	result, err := s.orders.BatchSubmitOrders(r.Context(), req, orderflow.BatchSubmitOptions{
		RequestID: httpx.RequestID(r),
	})
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}

	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request, gatewayOrderID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}

	decodedGatewayOrderID, err := url.PathUnescape(gatewayOrderID)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid gateway_order_id path", err.Error())
		return
	}

	defer r.Body.Close()
	var req trading.CancelOrderRequest
	if r.Body != nil && r.ContentLength != 0 {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body", err.Error())
			return
		}
	}
	if req.GatewayOrderID == "" {
		req.GatewayOrderID = decodedGatewayOrderID
	} else if req.GatewayOrderID != decodedGatewayOrderID {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "gateway_order_id path and body mismatch", nil)
		return
	}

	result, err := s.orders.CancelOrder(r.Context(), req, orderflow.CancelOptions{
		RequestID: httpx.RequestID(r),
	})
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}

	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	query, err := parseOrderQuery(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid order query", err.Error())
		return
	}
	result, err := s.orders.ListOrders(r.Context(), query)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) handleAccountAsset(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	result, err := s.orders.GetAsset(r.Context(), accountID)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) handleAccountPositions(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	query, err := parsePositionQuery(accountID, r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid position query", err.Error())
		return
	}
	result, err := s.orders.ListPositions(r.Context(), query)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) handleRefreshAsset(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	result, err := s.orders.RefreshAsset(r.Context(), accountID, orderflow.RefreshOptions{
		RequestID: httpx.RequestID(r),
	})
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleRefreshPositions(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	result, err := s.orders.RefreshPositions(r.Context(), accountID, orderflow.RefreshOptions{
		RequestID: httpx.RequestID(r),
	})
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleFills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	query, err := parseFillQuery(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid fill query", err.Error())
		return
	}
	result, err := s.orders.ListFills(r.Context(), query)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) writeOrderError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, trading.ErrInvalidSchema):
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid order request", err.Error())
	case errors.Is(err, ledger.ErrAssetNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "asset snapshot not found", err.Error())
	case errors.Is(err, ledger.ErrOrderNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "order not found", err.Error())
	case errors.Is(err, orderflow.ErrRouteNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "account route not found", err.Error())
	case errors.Is(err, orderflow.ErrAccountDisabled), errors.Is(err, orderflow.ErrTradingDisabled):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "account is not enabled for trading", err.Error())
	case errors.Is(err, orderflow.ErrMissingPublisher):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "redis command publisher is unavailable", err.Error())
	case errors.Is(err, orderflow.ErrOrderTerminalNotCancelable), errors.Is(err, orderflow.ErrOrderWithoutLeavesNotCancelable):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "order cannot be cancelled", err.Error())
	default:
		s.logger.Error("order_request_failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "order request failed", nil)
	}
}

func parseOrderQuery(values url.Values) (trading.OrderQuery, error) {
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return trading.OrderQuery{}, err
	}
	return trading.OrderQuery{
		AccountID:      values.Get("account_id"),
		GatewayOrderID: values.Get("gateway_order_id"),
		ClientOrderID:  values.Get("client_order_id"),
		Symbol:         values.Get("symbol"),
		Exchange:       trading.Exchange(values.Get("exchange")),
		Status:         trading.OrderStatus(values.Get("status")),
		Limit:          limit,
		Cursor:         values.Get("cursor"),
	}, nil
}

func parseFillQuery(values url.Values) (trading.FillQuery, error) {
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return trading.FillQuery{}, err
	}
	return trading.FillQuery{
		AccountID:      values.Get("account_id"),
		GatewayOrderID: values.Get("gateway_order_id"),
		Symbol:         values.Get("symbol"),
		Exchange:       trading.Exchange(values.Get("exchange")),
		Limit:          limit,
		Cursor:         values.Get("cursor"),
	}, nil
}

func parsePositionQuery(accountID string, values url.Values) (trading.PositionQuery, error) {
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return trading.PositionQuery{}, err
	}
	return trading.PositionQuery{
		AccountID: accountID,
		Symbol:    values.Get("symbol"),
		Exchange:  trading.Exchange(values.Get("exchange")),
		Limit:     limit,
		Cursor:    values.Get("cursor"),
	}, nil
}

func parseLimit(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if limit < 0 {
		return 0, errors.New("limit must be non-negative")
	}
	return limit, nil
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
