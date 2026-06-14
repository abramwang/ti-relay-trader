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
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type Dependencies struct {
	Orders       OrderService
	Jobs         JobRunStore
	Market       *market.MeridianClient
	Events       *events.Hub
	DatabasePing HealthCheckFunc
	RedisPing    HealthCheckFunc
}

type HealthCheckFunc func(context.Context) error

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
	RefreshOrders(ctx context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error)
	RefreshFills(ctx context.Context, accountID string, opts orderflow.RefreshOptions) (orderflow.RefreshQueryResult, error)
}

type JobRunStore interface {
	UpsertJobRun(ctx context.Context, run ledger.JobRun) (ledger.JobRun, error)
	LatestJobRuns(ctx context.Context, jobNames []string) ([]ledger.JobRun, error)
}

type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	started time.Time
	orders  OrderService
	jobs    JobRunStore
	market  *market.MeridianClient
	events  *events.Hub
	health  statusHealthChecks
}

type statusHealthChecks struct {
	Database HealthCheckFunc
	Redis    HealthCheckFunc
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
		started: timeutil.Now(),
		orders:  deps.Orders,
		jobs:    deps.Jobs,
		market:  marketClient,
		events:  deps.Events,
		health: statusHealthChecks{
			Database: deps.DatabasePing,
			Redis:    deps.RedisPing,
		},
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
	mux.HandleFunc("/v1/jobs/runs", server.handleJobRuns)
	mux.HandleFunc("/v1/history/orders", server.handleHistoryOrders)
	mux.HandleFunc("/v1/history/fills", server.handleHistoryFills)
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
		Time:       timeutil.Now(),
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
				Time:       timeutil.InBusinessLocation(now),
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
		event.Time = timeutil.Now()
	} else {
		event.Time = timeutil.InBusinessLocation(event.Time)
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

	httpx.WriteOK(w, r, http.StatusOK, s.statusPayload(r.Context(), "ok", false))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}

	httpx.WriteOK(w, r, http.StatusOK, s.statusPayload(r.Context(), "ok", true))
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
			if parts[2] == "history" {
				if r.Method != http.MethodGet {
					httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
					return
				}
				s.handleAccountPositions(w, r, accountID, true)
				return
			}
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
		s.handleAccountPositions(w, r, accountID, false)
	case "orders":
		if len(parts) != 3 || parts[2] != "refresh" {
			httpx.WriteNotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
			return
		}
		s.handleRefreshOrders(w, r, accountID)
	case "fills":
		if len(parts) != 3 || parts[2] != "refresh" {
			httpx.WriteNotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
			return
		}
		s.handleRefreshFills(w, r, accountID)
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

	status := http.StatusAccepted
	if result.Replayed {
		status = http.StatusOK
	}
	httpx.WriteOK(w, r, status, result)
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

	status := http.StatusAccepted
	if result.Replayed {
		status = http.StatusOK
	}
	httpx.WriteOK(w, r, status, result)
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
	query, err := parseOrderQuery(r.URL.Query(), true)
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

func (s *Server) handleHistoryOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	query, err := parseOrderQuery(r.URL.Query(), false)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid history order query", err.Error())
		return
	}
	query.History = true
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

func (s *Server) handleAccountPositions(w http.ResponseWriter, r *http.Request, accountID string, forceHistory bool) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	query, err := parsePositionQuery(accountID, r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid position query", err.Error())
		return
	}
	if forceHistory {
		query.History = true
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

func (s *Server) handleRefreshOrders(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	result, err := s.orders.RefreshOrders(r.Context(), accountID, orderflow.RefreshOptions{
		RequestID: httpx.RequestID(r),
	})
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleRefreshFills(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	result, err := s.orders.RefreshFills(r.Context(), accountID, orderflow.RefreshOptions{
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
	query, err := parseFillQuery(r.URL.Query(), true)
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

func (s *Server) handleHistoryFills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	query, err := parseFillQuery(r.URL.Query(), false)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid history fill query", err.Error())
		return
	}
	query.History = true
	result, err := s.orders.ListFills(r.Context(), query)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) handleJobRuns(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "job run store is unavailable", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		names := splitQueryCSV(r.URL.Query()["job_name"])
		runs, err := s.jobs.LatestJobRuns(r.Context(), names)
		if err != nil && !errors.Is(err, ledger.ErrJobRunNotFound) {
			s.logger.Warn("job_runs_query_failed", "error", err)
			httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "job run query failed", nil)
			return
		}
		if runs == nil {
			runs = []ledger.JobRun{}
		}
		httpx.WriteOK(w, r, http.StatusOK, map[string]any{"runs": runs, "count": len(runs)})
	case http.MethodPost:
		defer r.Body.Close()
		var req JobRunRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid job run body", err.Error())
			return
		}
		run, err := jobRunFromRequest(req)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid job run", err.Error())
			return
		}
		saved, err := s.jobs.UpsertJobRun(r.Context(), run)
		if err != nil {
			s.writeOrderError(w, r, err)
			return
		}
		httpx.WriteOK(w, r, http.StatusAccepted, map[string]any{"run": saved})
	default:
		httpx.WriteMethodNotAllowed(w, r, "GET, POST")
	}
}

func (s *Server) writeOrderError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, trading.ErrInvalidSchema):
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid order request", err.Error())
	case errors.Is(err, ledger.ErrInvalidLedgerInput):
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid ledger request", err.Error())
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
	case errors.Is(err, orderflow.ErrIdempotencyConflict):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeIdempotencyConflict, "idempotency key conflicts with an existing order", err.Error())
	case errors.Is(err, orderflow.ErrDuplicateGatewayOrder):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "gateway_order_id already exists", err.Error())
	case errors.Is(err, orderflow.ErrUnsupportedBusinessType):
		httpx.WriteError(w, r, http.StatusNotImplemented, httpx.CodeNotImplemented, "order business type is not supported", err.Error())
	case errors.Is(err, orderflow.ErrOrderTerminalNotCancelable), errors.Is(err, orderflow.ErrOrderWithoutLeavesNotCancelable):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "order cannot be cancelled", err.Error())
	default:
		s.logger.Error("order_request_failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "order request failed", nil)
	}
}

func parseOrderQuery(values url.Values, defaultToday bool) (trading.OrderQuery, error) {
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return trading.OrderQuery{}, err
	}
	history := parseBool(values.Get("history"))
	tradeDate, dateFrom, dateTo := parseDateQuery(values)
	if defaultToday && !history && tradeDate == "" && dateFrom == "" && dateTo == "" {
		tradeDate = timeutil.Now().Format("2006-01-02")
	}
	return trading.OrderQuery{
		AccountID:      values.Get("account_id"),
		GatewayOrderID: values.Get("gateway_order_id"),
		ClientOrderID:  values.Get("client_order_id"),
		Symbol:         values.Get("symbol"),
		Exchange:       trading.Exchange(values.Get("exchange")),
		Status:         trading.OrderStatus(values.Get("status")),
		TradeDate:      tradeDate,
		DateFrom:       dateFrom,
		DateTo:         dateTo,
		History:        history,
		Limit:          limit,
		Cursor:         values.Get("cursor"),
	}, nil
}

func parseFillQuery(values url.Values, defaultToday bool) (trading.FillQuery, error) {
	limit, err := parseLimit(values.Get("limit"))
	if err != nil {
		return trading.FillQuery{}, err
	}
	history := parseBool(values.Get("history"))
	tradeDate, dateFrom, dateTo := parseDateQuery(values)
	if defaultToday && !history && tradeDate == "" && dateFrom == "" && dateTo == "" {
		tradeDate = timeutil.Now().Format("2006-01-02")
	}
	return trading.FillQuery{
		AccountID:      values.Get("account_id"),
		GatewayOrderID: values.Get("gateway_order_id"),
		Symbol:         values.Get("symbol"),
		Exchange:       trading.Exchange(values.Get("exchange")),
		TradeDate:      tradeDate,
		DateFrom:       dateFrom,
		DateTo:         dateTo,
		History:        history,
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
		TradeDate: values.Get("trade_date"),
		DateFrom:  values.Get("date_from"),
		DateTo:    values.Get("date_to"),
		History:   parseBool(values.Get("history")),
		Limit:     limit,
		Cursor:    values.Get("cursor"),
	}, nil
}

func parseDateQuery(values url.Values) (string, string, string) {
	return values.Get("trade_date"), values.Get("date_from"), values.Get("date_to")
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func splitQueryCSV(values []string) []string {
	var output []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				output = append(output, item)
			}
		}
	}
	return output
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

func (s *Server) statusPayload(ctx context.Context, status string, includeDependencies bool) StatusView {
	view := StatusView{
		Service:            "relay-api",
		Mode:               string(s.cfg.Service.Mode),
		Status:             status,
		PublicURL:          s.cfg.Service.PublicURL,
		Timezone:           s.cfg.Service.Timezone,
		TradingDay:         currentTradingDayStatus(),
		StartedAt:          s.started,
		UptimeSeconds:      int64(time.Since(s.started).Seconds()),
		AccountsConfigured: len(s.cfg.Accounts),
		Accounts:           summarizeAccounts(s.cfg.Accounts),
		JobsConfigured:     len(s.cfg.Jobs),
	}
	if includeDependencies {
		view.Dependencies = s.dependencyStatus(ctx)
		view.Status = statusFromDependencies(view.Dependencies)
		view.JobRuns = s.latestJobRunStatus(ctx)
	}
	return view
}

func (s *Server) latestJobRunStatus(ctx context.Context) map[string]JobRunStatusView {
	if s.jobs == nil {
		return nil
	}
	names := []string{"pre_open_init", "post_close_settlement"}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	runs, err := s.jobs.LatestJobRuns(checkCtx, names)
	if err != nil {
		if errors.Is(err, ledger.ErrJobRunNotFound) {
			return map[string]JobRunStatusView{}
		}
		return map[string]JobRunStatusView{
			"_error": {Status: "unavailable", ErrorSummary: "job run query failed"},
		}
	}
	out := make(map[string]JobRunStatusView, len(runs))
	for _, run := range runs {
		out[run.JobName] = jobRunStatusView(run)
	}
	return out
}

func (s *Server) dependencyStatus(ctx context.Context) map[string]DependencyStatus {
	return map[string]DependencyStatus{
		"database":      s.pingDependency(ctx, strings.TrimSpace(s.cfg.Database.DSN) != "", s.health.Database),
		"redis":         s.pingDependency(ctx, strings.TrimSpace(s.cfg.Redis.URL) != "", s.health.Redis),
		"order_service": serviceDependency(s.orders != nil),
		"market":        serviceDependency(s.market != nil),
		"event_stream":  serviceDependency(s.events != nil),
		"auto_refresh":  configDependency(s.cfg.AutoRefreshEnabled()),
	}
}

func (s *Server) pingDependency(ctx context.Context, configured bool, checker HealthCheckFunc) DependencyStatus {
	if !configured {
		return DependencyStatus{Status: "not_configured", Configured: false}
	}
	if checker == nil {
		return DependencyStatus{Status: "unavailable", Configured: true, Message: "health checker unavailable"}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := checker(checkCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
			return DependencyStatus{Status: "timeout", Configured: true, LatencyMs: int64(time.Since(started) / time.Millisecond), Message: "ping timeout"}
		}
		return DependencyStatus{Status: "error", Configured: true, LatencyMs: int64(time.Since(started) / time.Millisecond), Message: "ping failed"}
	}
	return DependencyStatus{Status: "ok", Configured: true, LatencyMs: int64(time.Since(started) / time.Millisecond)}
}

func serviceDependency(available bool) DependencyStatus {
	if !available {
		return DependencyStatus{Status: "unavailable", Configured: false}
	}
	return DependencyStatus{Status: "ok", Configured: true}
}

func configDependency(enabled bool) DependencyStatus {
	if !enabled {
		return DependencyStatus{Status: "disabled", Configured: true}
	}
	return DependencyStatus{Status: "ok", Configured: true}
}

func statusFromDependencies(dependencies map[string]DependencyStatus) string {
	for _, name := range []string{"database", "redis", "order_service"} {
		dep, ok := dependencies[name]
		if !ok {
			continue
		}
		if name == "order_service" && dep.Status != "ok" {
			return "degraded"
		}
		if dep.Configured && dep.Status != "ok" {
			return "degraded"
		}
	}
	return "ok"
}

func summarizeAccounts(accounts []config.AccountRouteConfig) AccountStatusSummary {
	summary := AccountStatusSummary{Configured: len(accounts)}
	for _, account := range accounts {
		if account.Enabled {
			summary.Enabled++
		}
		if account.TradingEnabled {
			summary.TradingEnabled++
		}
		if account.Simulated {
			summary.Simulated++
		}
	}
	return summary
}

type StatusView struct {
	Service            string                      `json:"service"`
	Mode               string                      `json:"mode"`
	Status             string                      `json:"status"`
	PublicURL          string                      `json:"public_url"`
	Timezone           string                      `json:"timezone"`
	TradingDay         TradingDayStatusView        `json:"trading_day"`
	StartedAt          time.Time                   `json:"started_at"`
	UptimeSeconds      int64                       `json:"uptime_seconds"`
	AccountsConfigured int                         `json:"accounts_configured"`
	Accounts           AccountStatusSummary        `json:"accounts"`
	JobsConfigured     int                         `json:"jobs_configured"`
	Dependencies       map[string]DependencyStatus `json:"dependencies,omitempty"`
	JobRuns            map[string]JobRunStatusView `json:"job_runs,omitempty"`
}

type TradingDayStatusView struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
	Phase    string `json:"phase"`
}

type JobRunStatusView struct {
	RunID           string `json:"run_id,omitempty"`
	JobName         string `json:"job_name,omitempty"`
	TargetTradeDate string `json:"target_trade_date,omitempty"`
	Status          string `json:"status"`
	Skipped         bool   `json:"skipped,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	FinishedAt      string `json:"finished_at,omitempty"`
	DurationMS      int64  `json:"duration_ms,omitempty"`
	ErrorSummary    string `json:"error_summary,omitempty"`
}

type AccountStatusSummary struct {
	Configured     int `json:"configured"`
	Enabled        int `json:"enabled"`
	TradingEnabled int `json:"trading_enabled"`
	Simulated      int `json:"simulated"`
}

type DependencyStatus struct {
	Status     string `json:"status"`
	Configured bool   `json:"configured"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
	Message    string `json:"message,omitempty"`
}

type AccountView struct {
	AccountID      string `json:"account_id"`
	BrokerID       string `json:"broker_id"`
	GatewayID      string `json:"gateway_id"`
	Enabled        bool   `json:"enabled"`
	TradingEnabled bool   `json:"trading_enabled"`
	Simulated      bool   `json:"simulated"`
}

type JobRunRequest struct {
	RunID           string         `json:"run_id,omitempty"`
	JobName         string         `json:"job_name,omitempty"`
	TargetTradeDate string         `json:"target_trade_date,omitempty"`
	Timezone        string         `json:"timezone,omitempty"`
	Status          string         `json:"status,omitempty"`
	Trigger         string         `json:"trigger,omitempty"`
	Skipped         bool           `json:"skipped,omitempty"`
	StartedAt       time.Time      `json:"started_at,omitempty"`
	FinishedAt      time.Time      `json:"finished_at,omitempty"`
	DurationMS      int64          `json:"duration_ms,omitempty"`
	Report          map[string]any `json:"report,omitempty"`
	ErrorSummary    string         `json:"error_summary,omitempty"`
}

func jobRunFromRequest(req JobRunRequest) (ledger.JobRun, error) {
	report := req.Report
	if report == nil {
		report = map[string]any{}
	}
	jobName := firstNonEmpty(req.JobName, stringFromMap(report, "job"))
	targetTradeDate := req.TargetTradeDate
	if targetTradeDate == "" {
		if tradingDay, ok := report["trading_day"].(map[string]any); ok {
			targetTradeDate = stringFromMap(tradingDay, "target_trade_date")
		}
	}
	status := req.Status
	if status == "" {
		status = "succeeded"
		if boolFromMap(report, "skipped") || req.Skipped {
			status = "skipped"
		}
		if ok, exists := report["ok"].(bool); exists && !ok {
			status = "failed"
		}
	}
	errorSummary := req.ErrorSummary
	if errorSummary == "" {
		if errorsValue, ok := report["errors"].([]any); ok && len(errorsValue) > 0 {
			errorSummary = fmt.Sprint(errorsValue[0])
		}
	}
	return ledger.JobRun{
		RunID:           req.RunID,
		JobName:         jobName,
		TargetTradeDate: targetTradeDate,
		Timezone:        firstNonEmpty(req.Timezone, stringFromMap(report, "timezone"), timeutil.LocationName),
		Status:          status,
		Trigger:         req.Trigger,
		Skipped:         req.Skipped || boolFromMap(report, "skipped"),
		StartedAt:       req.StartedAt,
		FinishedAt:      req.FinishedAt,
		DurationMS:      req.DurationMS,
		Report:          report,
		ErrorSummary:    errorSummary,
	}, nil
}

func currentTradingDayStatus() TradingDayStatusView {
	now := timeutil.Now()
	return TradingDayStatusView{
		Date:     now.Format("2006-01-02"),
		Timezone: timeutil.LocationName,
		Phase:    tradingPhase(now),
	}
}

func tradingPhase(now time.Time) string {
	local := timeutil.InBusinessLocation(now)
	hour, minute, _ := local.Clock()
	minutes := hour*60 + minute
	switch {
	case minutes < 9*60+15:
		return "pre_open"
	case minutes < 9*60+30:
		return "call_auction"
	case minutes < 11*60+30:
		return "continuous"
	case minutes < 13*60:
		return "lunch_break"
	case minutes < 15*60:
		return "continuous"
	default:
		return "post_close"
	}
}

func jobRunStatusView(run ledger.JobRun) JobRunStatusView {
	return JobRunStatusView{
		RunID:           run.RunID,
		JobName:         run.JobName,
		TargetTradeDate: run.TargetTradeDate,
		Status:          run.Status,
		Skipped:         run.Skipped,
		StartedAt:       optionalBusinessTime(run.StartedAt),
		FinishedAt:      optionalBusinessTime(run.FinishedAt),
		DurationMS:      run.DurationMS,
		ErrorSummary:    run.ErrorSummary,
	}
}

func optionalBusinessTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return timeutil.FormatRFC3339Nano(value)
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key]; ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func boolFromMap(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key]
	if !ok {
		return false
	}
	if boolValue, ok := value.(bool); ok {
		return boolValue
	}
	return parseBool(fmt.Sprint(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
