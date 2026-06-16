package api

import (
	"bufio"
	"context"
	"encoding/csv"
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
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type Dependencies struct {
	Orders       OrderService
	Jobs         JobRunStore
	Settlements  SettlementStore
	Accounts     AccountAliasStore
	Market       *market.MeridianClient
	Events       *events.Hub
	DatabasePing HealthCheckFunc
	RedisPing    HealthCheckFunc
}

type HealthCheckFunc func(context.Context) error

const (
	settlementPageLimit = 500
	settlementMaxRows   = 20000
)

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

type SettlementStore interface {
	UpsertAssetSnapshotForDate(ctx context.Context, asset trading.Asset, tradeDate string, snapshotType string, source string, rawPayload any, capturedAt time.Time) error
	UpsertPositionSnapshot(ctx context.Context, position trading.Position, source string, rawPayload any, capturedAt time.Time) error
	UpsertReconciliationRun(ctx context.Context, run ledger.ReconciliationRun) (ledger.ReconciliationRun, error)
	UpsertReconciliationInput(ctx context.Context, input ledger.ReconciliationInput) error
	UpsertReconciliationBreak(ctx context.Context, item ledger.ReconciliationBreak) error
	ListReconciliationBreaks(ctx context.Context, query ledger.ReconciliationBreakQuery) ([]ledger.ReconciliationBreak, error)
	GetDailyPerformance(ctx context.Context, accountID string, tradeDate string) (ledger.DailyPerformance, error)
	ListDailyPerformance(ctx context.Context, accountID string, dateFrom string, dateTo string) ([]ledger.DailyPerformance, error)
	RawStreamSummary(ctx context.Context, accountID string, start time.Time, end time.Time) ([]ledger.RawStreamSummaryBucket, error)
}

type AccountAliasStore interface {
	AccountAliases(ctx context.Context, accountIDs []string) (map[string]string, error)
	UpsertAccountAlias(ctx context.Context, accountID string, brokerID string, alias string) error
}

type PerformanceSeriesSummary struct {
	AccountID                string   `json:"account_id"`
	DateFrom                 string   `json:"date_from"`
	DateTo                   string   `json:"date_to"`
	Count                    int      `json:"count"`
	StartNetAsset            float64  `json:"start_net_asset"`
	EndNetAsset              float64  `json:"end_net_asset"`
	TotalPnL                 float64  `json:"total_pnl"`
	TotalReturn              float64  `json:"total_return"`
	MaxDrawdown              float64  `json:"max_drawdown"`
	BenchmarkSecurityID      string   `json:"benchmark_security_id,omitempty"`
	BenchmarkStartClose      *float64 `json:"benchmark_start_close,omitempty"`
	BenchmarkEndClose        *float64 `json:"benchmark_end_close,omitempty"`
	BenchmarkTotalReturn     *float64 `json:"benchmark_total_return,omitempty"`
	BenchmarkMaxDrawdown     *float64 `json:"benchmark_max_drawdown,omitempty"`
	ExcessTotalReturn        *float64 `json:"excess_total_return,omitempty"`
	BenchmarkObservationDays int      `json:"benchmark_observation_days,omitempty"`
}

var errSettlementStoreUnavailable = errors.New("settlement store is unavailable")
var errMarketClientUnavailable = errors.New("meridian market client is unavailable")
var errBenchmarkBarsUnavailable = errors.New("benchmark bars are unavailable")

type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	started time.Time
	orders  OrderService
	jobs    JobRunStore
	settles SettlementStore
	aliases AccountAliasStore
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
		settles: deps.Settlements,
		aliases: deps.Accounts,
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
	mux.HandleFunc("/v1/account-routes", server.handleAccountRoutes)
	mux.HandleFunc("/v1/accounts", server.handleAccounts)
	mux.HandleFunc("/v1/accounts/", server.handleAccountPath)
	mux.HandleFunc("/v1/meridian/metadata/instruments", server.handleMeridianMetadataInstruments)
	mux.HandleFunc("/v1/meridian/market/bars", server.handleMeridianMarketBars)
	mux.HandleFunc("/v1/meridian/market/snapshots", server.handleMeridianMarketSnapshots)
	mux.HandleFunc("/v1/meridian/stream/market/snapshots", server.handleMeridianMarketSnapshotStream)
	mux.HandleFunc("/v1/events/stream", server.handleEventsStream)
	mux.HandleFunc("/v1/jobs/runs", server.handleJobRuns)
	mux.HandleFunc("/v1/settlements/snapshots", server.handleSettlementSnapshots)
	mux.HandleFunc("/v1/reconciliations/breaks", server.handleReconciliationBreaks)
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

func (s *Server) handleMeridianMarketBars(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.market == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "meridian market client is unavailable", nil)
		return
	}
	response, err := s.market.MarketBars(r.Context(), r.URL.Query())
	if err != nil {
		s.logger.Warn("meridian_market_bars_failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeUnavailable, "meridian market request failed", err.Error())
		return
	}
	s.writeMeridianResponse(w, r, response, "meridian market request failed")
}

func (s *Server) handleMeridianMarketSnapshotStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.market == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "meridian market client is unavailable", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "streaming is not supported", nil)
		return
	}

	response, err := s.market.MarketSnapshotStream(r.Context(), r.URL.Query())
	if err != nil {
		s.logger.Warn("meridian_market_snapshot_stream_failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeUnavailable, "meridian market stream request failed", err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		code := httpx.CodeBadRequest
		if response.StatusCode >= http.StatusInternalServerError {
			code = httpx.CodeUnavailable
		}
		httpx.WriteError(w, r, response.StatusCode, code, "meridian market stream request failed", string(body))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	reader := bufio.NewReader(response.Body)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			if _, err := io.WriteString(w, line); err != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				s.logger.Warn("meridian_market_snapshot_stream_read_failed", "error", readErr)
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		default:
		}
	}
}

func (s *Server) enrichOrderNames(ctx context.Context, orders []trading.Order) {
	if len(orders) == 0 {
		return
	}
	names := s.instrumentNames(ctx, orderSecurityIDs(orders))
	if len(names) == 0 {
		return
	}
	for i := range orders {
		if strings.TrimSpace(orders[i].Name) != "" {
			continue
		}
		if name := names[securityID(orders[i].Symbol, string(orders[i].Exchange))]; name != "" {
			orders[i].Name = name
		}
	}
}

func (s *Server) enrichFillNames(ctx context.Context, fills []trading.Fill) {
	if len(fills) == 0 {
		return
	}
	names := s.instrumentNames(ctx, fillSecurityIDs(fills))
	if len(names) == 0 {
		return
	}
	for i := range fills {
		if strings.TrimSpace(fills[i].Name) != "" {
			continue
		}
		if name := names[securityID(fills[i].Symbol, string(fills[i].Exchange))]; name != "" {
			fills[i].Name = name
		}
	}
}

func (s *Server) enrichPositionNames(ctx context.Context, positions []trading.Position) {
	if len(positions) == 0 {
		return
	}
	names := s.instrumentNames(ctx, positionSecurityIDs(positions))
	if len(names) == 0 {
		return
	}
	for i := range positions {
		if strings.TrimSpace(positions[i].Name) != "" {
			continue
		}
		if name := names[securityID(positions[i].Symbol, string(positions[i].Exchange))]; name != "" {
			positions[i].Name = name
		}
	}
}

type positionFillCost struct {
	symbol   string
	exchange trading.Exchange
	quantity int64
	qty      int64
	amount   float64
}

func (s *Server) enrichPositionCostsFromTodayFills(ctx context.Context, positions []trading.Position, query trading.PositionQuery) {
	if s.orders == nil || len(positions) == 0 || query.History || query.TradeDate != "" || query.DateFrom != "" || query.DateTo != "" {
		return
	}
	needed := make(map[string]positionFillCost)
	for _, position := range positions {
		if !positionNeedsFillDerivedCost(position) {
			continue
		}
		if id := securityID(position.Symbol, string(position.Exchange)); id != "" {
			target := needed[id]
			target.symbol = position.Symbol
			target.exchange = position.Exchange
			if position.Quantity > target.quantity {
				target.quantity = position.Quantity
			}
			needed[id] = target
		}
	}
	if len(needed) == 0 {
		return
	}

	costs := make(map[string]positionFillCost, len(needed))
	tradeDate := timeutil.Now().Format("2006-01-02")
	for id, target := range needed {
		cursor := ""
		for page := 0; page < 20; page++ {
			result, err := s.orders.ListFills(ctx, trading.FillQuery{
				AccountID: query.AccountID,
				Symbol:    target.symbol,
				Exchange:  target.exchange,
				TradeDate: tradeDate,
				Limit:     500,
				Cursor:    cursor,
			})
			if err != nil {
				s.logger.Warn("position_cost_enrichment_failed", "account_id", query.AccountID, "symbol", target.symbol, "exchange", target.exchange, "error", err)
				return
			}
			cost := costs[id]
			for _, fill := range result.Fills {
				if fill.TradeSide != trading.TradeSideBuy || fill.Qty <= 0 || fill.Price <= 0 {
					continue
				}
				cost.qty += fill.Qty
				cost.amount += fill.Price * float64(fill.Qty)
			}
			costs[id] = cost
			cursor = strings.TrimSpace(result.NextCursor)
			if cursor == "" || cost.qty >= target.quantity {
				break
			}
		}
	}

	for i := range positions {
		if !positionNeedsFillDerivedCost(positions[i]) {
			continue
		}
		cost := costs[securityID(positions[i].Symbol, string(positions[i].Exchange))]
		if cost.qty < positions[i].Quantity || cost.amount <= 0 {
			continue
		}
		positions[i].AvgCost = cost.amount / float64(cost.qty)
	}
}

func positionNeedsFillDerivedCost(position trading.Position) bool {
	return position.AvgCost == 0 && position.Quantity > 0 && position.SellableQty == 0
}

func (s *Server) instrumentNames(ctx context.Context, ids []string) map[string]string {
	if s.market == nil || len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	names := make(map[string]string, len(ids))
	const chunkSize = 100
	for offset := 0; offset < len(ids); offset += chunkSize {
		end := offset + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		limit := end - offset
		if limit < 100 {
			limit = 100
		}
		response, err := s.market.MetadataInstruments(ctx, url.Values{
			"security_ids": {strings.Join(ids[offset:end], ",")},
			"limit":        {strconv.Itoa(limit)},
		})
		if err != nil {
			s.logger.Warn("instrument_name_enrichment_failed", "error", err)
			return names
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
			s.logger.Warn("instrument_name_enrichment_failed", "status", response.StatusCode, "url", response.URL)
			continue
		}
		for _, row := range meridianRows(response.Payload) {
			id := normalizeSecurityID(stringFromMap(row, "security_id"))
			name := strings.TrimSpace(stringFromMap(row, "name"))
			if id != "" && name != "" {
				names[id] = name
			}
		}
	}
	return names
}

func orderSecurityIDs(orders []trading.Order) []string {
	ids := make([]string, 0, len(orders))
	seen := make(map[string]struct{}, len(orders))
	for _, order := range orders {
		id := securityID(order.Symbol, string(order.Exchange))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func positionSecurityIDs(positions []trading.Position) []string {
	ids := make([]string, 0, len(positions))
	seen := make(map[string]struct{}, len(positions))
	for _, position := range positions {
		id := securityID(position.Symbol, string(position.Exchange))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func fillSecurityIDs(fills []trading.Fill) []string {
	ids := make([]string, 0, len(fills))
	seen := make(map[string]struct{}, len(fills))
	for _, fill := range fills {
		id := securityID(fill.Symbol, string(fill.Exchange))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func securityID(symbol string, exchange string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	exchange = normalizeExchange(exchange, symbol)
	if symbol == "" || exchange == "" {
		return ""
	}
	if strings.Contains(symbol, ".") {
		return normalizeSecurityID(symbol)
	}
	return symbol + "." + exchange
}

func normalizeSecurityID(value string) string {
	raw := strings.ToUpper(strings.TrimSpace(value))
	if raw == "" {
		return ""
	}
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) == 1 {
		exchange := normalizeExchange("", parts[0])
		if exchange == "" {
			return ""
		}
		return parts[0] + "." + exchange
	}
	exchange := normalizeExchange(parts[1], parts[0])
	if exchange == "" {
		return ""
	}
	return parts[0] + "." + exchange
}

func normalizeExchange(value string, symbol string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "SH", "XSHG", "SHSE", "SSE":
		return "SH"
	case "SZ", "XSHE", "SZSE":
		return "SZ"
	case "BJ", "XBSE", "BSE":
		return "BJ"
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	switch {
	case strings.HasPrefix(symbol, "6"), strings.HasPrefix(symbol, "5"), strings.HasPrefix(symbol, "9"):
		return "SH"
	case strings.HasPrefix(symbol, "0"), strings.HasPrefix(symbol, "1"), strings.HasPrefix(symbol, "2"), strings.HasPrefix(symbol, "3"):
		return "SZ"
	case strings.HasPrefix(symbol, "4"), strings.HasPrefix(symbol, "8"):
		return "BJ"
	default:
		return ""
	}
}

func meridianRows(payload map[string]any) []map[string]any {
	rows, ok := payload["data"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		values, ok := row.(map[string]any)
		if ok {
			out = append(out, values)
		}
	}
	return out
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

	aliasOverrides, source := s.accountAliasOverrides(r.Context())
	accounts := make([]AccountView, 0, len(s.cfg.Accounts))
	for _, account := range s.cfg.Accounts {
		accounts = append(accounts, AccountView{
			AccountID:      account.AccountID,
			Alias:          effectiveAccountAlias(account, aliasOverrides),
			BrokerID:       account.BrokerID,
			GatewayID:      account.GatewayID,
			Enabled:        account.Enabled,
			TradingEnabled: account.TradingEnabled,
			Simulated:      account.Simulated,
		})
	}

	httpx.WriteOK(w, r, http.StatusOK, map[string]any{
		"source":   source,
		"accounts": accounts,
	})
}

func (s *Server) handleAccountRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}

	aliasOverrides, source := s.accountAliasOverrides(r.Context())
	routes := make([]AccountRouteView, 0, len(s.cfg.Accounts))
	for _, account := range s.cfg.Accounts {
		streams := redisstream.NewStreams(account.StreamPrefix)
		routes = append(routes, AccountRouteView{
			AccountID:      account.AccountID,
			Alias:          effectiveAccountAlias(account, aliasOverrides),
			BrokerID:       account.BrokerID,
			GatewayID:      account.GatewayID,
			StreamPrefix:   streams.Prefix,
			Enabled:        account.Enabled,
			TradingEnabled: account.TradingEnabled,
			QueryEnabled:   account.Enabled,
			ReadOnly:       account.Enabled && !account.TradingEnabled,
			Simulated:      account.Simulated,
			Environment:    string(s.cfg.Service.Environment),
			Streams: AccountRouteStreamsView{
				CmdTrade: streams.CmdTrade,
				CmdQuery: streams.CmdQuery,
				Reply:    streams.Reply,
				Event:    streams.Event,
				HB:       streams.HB,
				DLQ:      streams.DLQ,
			},
		})
	}

	httpx.WriteOK(w, r, http.StatusOK, map[string]any{
		"source":      source,
		"environment": string(s.cfg.Service.Environment),
		"redis_env":   s.cfg.Redis.Env,
		"protocol":    redisstream.Protocol,
		"summary":     summarizeAccounts(s.cfg.Accounts),
		"routes":      routes,
	})
}

func (s *Server) accountAliasOverrides(ctx context.Context) (map[string]string, string) {
	accountIDs := make([]string, 0, len(s.cfg.Accounts))
	for _, account := range s.cfg.Accounts {
		accountIDs = append(accountIDs, account.AccountID)
	}
	aliasOverrides := map[string]string{}
	source := "config"
	if s.aliases != nil {
		aliases, err := s.aliases.AccountAliases(ctx, accountIDs)
		if err != nil {
			s.logger.Warn("account_alias_lookup_failed", "error", err)
		} else {
			aliasOverrides = aliases
			if len(aliases) > 0 {
				source = "config+database"
			}
		}
	}
	return aliasOverrides, source
}

func effectiveAccountAlias(account config.AccountRouteConfig, aliasOverrides map[string]string) string {
	if override := strings.TrimSpace(aliasOverrides[account.AccountID]); override != "" {
		return override
	}
	return strings.TrimSpace(account.Alias)
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
	case "alias":
		if len(parts) != 2 {
			httpx.WriteNotFound(w, r)
			return
		}
		if r.Method != http.MethodPatch {
			httpx.WriteMethodNotAllowed(w, r, http.MethodPatch)
			return
		}
		s.handleAccountAlias(w, r, accountID)
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
	case "performance":
		if len(parts) != 3 {
			httpx.WriteNotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
			return
		}
		switch parts[2] {
		case "daily":
			s.handleDailyPerformance(w, r, accountID)
		case "series":
			s.handlePerformanceSeries(w, r, accountID)
		case "series.csv":
			s.handlePerformanceSeriesCSV(w, r, accountID)
		default:
			httpx.WriteNotFound(w, r)
		}
	default:
		httpx.WriteNotFound(w, r)
	}
}

func (s *Server) handleAccountAlias(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.aliases == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "account alias store is unavailable", nil)
		return
	}
	account, ok := s.cfg.AccountRoute(accountID)
	if !ok {
		httpx.WriteNotFound(w, r)
		return
	}

	defer r.Body.Close()
	var req AccountAliasRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body", err.Error())
		return
	}
	alias := strings.TrimSpace(req.Alias)
	if len([]rune(alias)) > 24 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "alias must be 24 characters or fewer", nil)
		return
	}

	if err := s.aliases.UpsertAccountAlias(r.Context(), account.AccountID, account.BrokerID, alias); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "update account alias failed", err.Error())
		return
	}
	effectiveAlias := alias
	if effectiveAlias == "" {
		effectiveAlias = strings.TrimSpace(account.Alias)
	}
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{
		"account": AccountView{
			AccountID:      account.AccountID,
			Alias:          effectiveAlias,
			BrokerID:       account.BrokerID,
			GatewayID:      account.GatewayID,
			Enabled:        account.Enabled,
			TradingEnabled: account.TradingEnabled,
			Simulated:      account.Simulated,
		},
	})
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
	s.enrichOrderNames(r.Context(), result.Orders)
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
	s.enrichOrderNames(r.Context(), result.Orders)
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
	s.enrichPositionNames(r.Context(), result.Positions)
	s.enrichPositionCostsFromTodayFills(r.Context(), result.Positions, query)
	httpx.WriteOK(w, r, http.StatusOK, result)
}

func (s *Server) handleDailyPerformance(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.settles == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settlement store is unavailable", nil)
		return
	}
	tradeDate := strings.TrimSpace(r.URL.Query().Get("trade_date"))
	if tradeDate == "" {
		tradeDate = timeutil.Now().Format("2006-01-02")
	}
	normalizedTradeDate, err := normalizeAPIDate(tradeDate)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid trade_date", err.Error())
		return
	}
	performance, err := s.settles.GetDailyPerformance(r.Context(), accountID, normalizedTradeDate)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{
		"performance": performance,
	})
}

func (s *Server) handlePerformanceSeries(w http.ResponseWriter, r *http.Request, accountID string) {
	series, summary, err := s.performanceSeriesFromRequest(r, accountID)
	if err != nil {
		s.writePerformanceSeriesError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{
		"summary": summary,
		"series":  series,
	})
}

func (s *Server) handlePerformanceSeriesCSV(w http.ResponseWriter, r *http.Request, accountID string) {
	series, summary, err := s.performanceSeriesFromRequest(r, accountID)
	if err != nil {
		s.writePerformanceSeriesError(w, r, err)
		return
	}
	filename := fmt.Sprintf("relay-performance-%s-%s-%s.csv", sanitizeFilenamePart(accountID), summary.DateFrom, summary.DateTo)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	_ = writer.Write([]string{
		"account_id",
		"trade_date",
		"net_asset",
		"previous_net_asset",
		"open_net_asset",
		"overnight_adjustment",
		"asset_change",
		"intraday_pnl",
		"intraday_return",
		"open_snapshot_source",
		"daily_pnl",
		"return_rate",
		"cumulative_return",
		"drawdown",
		"benchmark_security_id",
		"benchmark_close",
		"benchmark_return",
		"benchmark_cumulative_return",
		"benchmark_drawdown",
		"excess_return",
		"excess_cumulative_return",
		"cash_available",
		"cash_total",
		"market_value",
		"position_market_value",
		"unrealized_pnl",
		"settled_profit",
		"realized_pnl",
		"gross_pnl",
		"net_pnl",
		"fills_count",
		"buy_amount",
		"sell_amount",
		"turnover",
		"fee_total",
		"open_captured_at",
		"captured_at",
	})
	for _, item := range series {
		_ = writer.Write([]string{
			item.AccountID,
			item.TradeDate,
			formatCSVFloat(item.NetAsset),
			formatCSVFloat(item.PreviousNetAsset),
			formatCSVFloat(item.OpenNetAsset),
			formatCSVFloat(item.OvernightAdjustment),
			formatCSVFloat(item.AssetChange),
			formatCSVFloat(item.IntradayPnL),
			formatCSVFloat(item.IntradayReturn),
			item.OpenSnapshotSource,
			formatCSVFloat(item.DailyPnL),
			formatCSVFloat(item.ReturnRate),
			formatCSVFloat(item.CumulativeReturn),
			formatCSVFloat(item.Drawdown),
			item.BenchmarkSecurityID,
			formatCSVOptionalFloat(item.BenchmarkClose),
			formatCSVOptionalFloat(item.BenchmarkReturn),
			formatCSVOptionalFloat(item.BenchmarkCumulative),
			formatCSVOptionalFloat(item.BenchmarkDrawdown),
			formatCSVOptionalFloat(item.ExcessReturn),
			formatCSVOptionalFloat(item.ExcessCumulative),
			formatCSVFloat(item.CashAvailable),
			formatCSVFloat(item.CashTotal),
			formatCSVFloat(item.MarketValue),
			formatCSVFloat(item.PositionMarketValue),
			formatCSVFloat(item.UnrealizedPnL),
			formatCSVFloat(item.SettledProfit),
			formatCSVFloat(item.RealizedPnL),
			formatCSVFloat(item.GrossPnL),
			formatCSVFloat(item.NetPnL),
			strconv.FormatInt(item.FillsCount, 10),
			formatCSVFloat(item.BuyAmount),
			formatCSVFloat(item.SellAmount),
			formatCSVFloat(item.Turnover),
			formatCSVFloat(item.FeeTotal),
			formatCSVTime(item.OpenCapturedAt),
			formatCSVTime(item.CapturedAt),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		s.logger.Warn("performance_series_csv_write_failed", "error", err)
	}
}

func (s *Server) performanceSeriesFromRequest(r *http.Request, accountID string) ([]ledger.DailyPerformance, PerformanceSeriesSummary, error) {
	if s.settles == nil {
		return nil, PerformanceSeriesSummary{}, errSettlementStoreUnavailable
	}
	values := r.URL.Query()
	dateFrom := strings.TrimSpace(values.Get("date_from"))
	dateTo := strings.TrimSpace(values.Get("date_to"))
	if tradeDate := strings.TrimSpace(values.Get("trade_date")); tradeDate != "" {
		dateFrom = tradeDate
		dateTo = tradeDate
	}
	if dateFrom == "" && dateTo == "" {
		dateTo = timeutil.Now().Format("2006-01-02")
		dateFrom = dateTo
	}
	if dateFrom == "" {
		dateFrom = dateTo
	}
	if dateTo == "" {
		dateTo = dateFrom
	}
	normalizedDateFrom, err := normalizeAPIDate(dateFrom)
	if err != nil {
		return nil, PerformanceSeriesSummary{}, fmt.Errorf("%w: invalid date_from: %v", ledger.ErrInvalidLedgerInput, err)
	}
	normalizedDateTo, err := normalizeAPIDate(dateTo)
	if err != nil {
		return nil, PerformanceSeriesSummary{}, fmt.Errorf("%w: invalid date_to: %v", ledger.ErrInvalidLedgerInput, err)
	}
	series, err := s.settles.ListDailyPerformance(r.Context(), accountID, normalizedDateFrom, normalizedDateTo)
	if err != nil {
		return nil, PerformanceSeriesSummary{}, err
	}
	series, summary := buildPerformanceSeries(accountID, normalizedDateFrom, normalizedDateTo, series)
	if benchmarkSecurityID := strings.TrimSpace(values.Get("benchmark_security_id")); benchmarkSecurityID != "" {
		if s.market == nil {
			return nil, PerformanceSeriesSummary{}, errMarketClientUnavailable
		}
		if err := s.applyBenchmarkSeries(r.Context(), series, &summary, benchmarkSecurityID); err != nil {
			return nil, PerformanceSeriesSummary{}, err
		}
	}
	return series, summary, nil
}

func (s *Server) writePerformanceSeriesError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errSettlementStoreUnavailable) {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settlement store is unavailable", nil)
		return
	}
	if errors.Is(err, errMarketClientUnavailable) {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "meridian market client is unavailable", nil)
		return
	}
	if errors.Is(err, errBenchmarkBarsUnavailable) {
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeUnavailable, "benchmark bars request failed", err.Error())
		return
	}
	s.writeOrderError(w, r, err)
}

func buildPerformanceSeries(accountID string, dateFrom string, dateTo string, series []ledger.DailyPerformance) ([]ledger.DailyPerformance, PerformanceSeriesSummary) {
	summary := PerformanceSeriesSummary{
		AccountID: accountID,
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		Count:     len(series),
	}
	if len(series) == 0 {
		return series, summary
	}
	base := series[0].PreviousNetAsset
	if base <= 0 {
		base = series[0].NetAsset
	}
	peak := base
	if peak <= 0 {
		peak = series[0].NetAsset
	}
	maxDrawdown := 0.0
	for idx := range series {
		netAsset := series[idx].NetAsset
		if base > 0 {
			series[idx].CumulativeReturn = (netAsset - base) / base
		}
		if netAsset > peak {
			peak = netAsset
		}
		if peak > 0 {
			series[idx].Drawdown = (netAsset - peak) / peak
		}
		if series[idx].Drawdown < maxDrawdown {
			maxDrawdown = series[idx].Drawdown
		}
	}
	endNetAsset := series[len(series)-1].NetAsset
	summary.StartNetAsset = base
	summary.EndNetAsset = endNetAsset
	if base > 0 {
		summary.TotalPnL = endNetAsset - base
		summary.TotalReturn = summary.TotalPnL / base
	}
	summary.MaxDrawdown = maxDrawdown
	return series, summary
}

func (s *Server) applyBenchmarkSeries(ctx context.Context, series []ledger.DailyPerformance, summary *PerformanceSeriesSummary, securityID string) error {
	securityID = strings.TrimSpace(securityID)
	if summary != nil {
		summary.BenchmarkSecurityID = securityID
	}
	if securityID == "" || len(series) == 0 {
		return nil
	}

	closes := make(map[string]float64, len(series))
	for _, item := range series {
		normalizedDate, err := normalizeAPIDate(item.TradeDate)
		if err != nil {
			continue
		}
		closePrice, ok, err := s.benchmarkClose(ctx, securityID, normalizedDate)
		if err != nil {
			return err
		}
		if ok {
			closes[normalizedDate] = closePrice
		}
	}

	var startClose float64
	var previousClose float64
	var lastClose float64
	var peakClose float64
	var maxDrawdown float64
	observations := 0
	for idx := range series {
		series[idx].BenchmarkSecurityID = securityID
		normalizedDate, err := normalizeAPIDate(series[idx].TradeDate)
		if err != nil {
			continue
		}
		closePrice, ok := closes[normalizedDate]
		if !ok || closePrice <= 0 {
			continue
		}
		series[idx].BenchmarkClose = floatPtr(closePrice)
		if observations == 0 {
			startClose = closePrice
			previousClose = closePrice
			peakClose = closePrice
			series[idx].BenchmarkReturn = floatPtr(0)
			series[idx].ExcessReturn = floatPtr(0)
		} else if previousClose > 0 {
			benchmarkReturn := (closePrice - previousClose) / previousClose
			series[idx].BenchmarkReturn = floatPtr(benchmarkReturn)
			series[idx].ExcessReturn = floatPtr(series[idx].ReturnRate - benchmarkReturn)
		}
		if startClose > 0 {
			benchmarkCumulative := (closePrice - startClose) / startClose
			series[idx].BenchmarkCumulative = floatPtr(benchmarkCumulative)
			series[idx].ExcessCumulative = floatPtr(series[idx].CumulativeReturn - benchmarkCumulative)
		}
		if closePrice > peakClose {
			peakClose = closePrice
		}
		if peakClose > 0 {
			series[idx].BenchmarkDrawdown = floatPtr((closePrice - peakClose) / peakClose)
		}
		if series[idx].BenchmarkDrawdown != nil && *series[idx].BenchmarkDrawdown < maxDrawdown {
			maxDrawdown = *series[idx].BenchmarkDrawdown
		}
		previousClose = closePrice
		lastClose = closePrice
		observations++
	}
	if summary != nil && observations > 0 {
		summary.BenchmarkStartClose = floatPtr(startClose)
		summary.BenchmarkEndClose = floatPtr(lastClose)
		if startClose > 0 {
			benchmarkTotalReturn := (lastClose - startClose) / startClose
			summary.BenchmarkTotalReturn = floatPtr(benchmarkTotalReturn)
			summary.ExcessTotalReturn = floatPtr(summary.TotalReturn - benchmarkTotalReturn)
		}
		summary.BenchmarkMaxDrawdown = floatPtr(maxDrawdown)
		summary.BenchmarkObservationDays = observations
	}
	return nil
}

func (s *Server) benchmarkClose(ctx context.Context, securityID string, normalizedTradeDate string) (float64, bool, error) {
	values := url.Values{}
	values.Set("security_id", securityID)
	values.Set("trade_date", strings.ReplaceAll(normalizedTradeDate, "-", ""))
	values.Set("frequency", "1m")
	values.Set("adjustment", "none")
	values.Set("start_time", "14:55:00")
	values.Set("end_time", "15:00:00")
	values.Set("limit", "10")

	response, err := s.market.MarketBars(ctx, values)
	if err != nil {
		return 0, false, fmt.Errorf("%w: %v", errBenchmarkBarsUnavailable, err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return 0, false, fmt.Errorf("%w: status %d", errBenchmarkBarsUnavailable, response.StatusCode)
	}
	if errorPayload, ok := response.Payload["error"].(map[string]any); ok {
		return 0, false, fmt.Errorf("%w: %s", errBenchmarkBarsUnavailable, meridianErrorMessage(errorPayload))
	}
	rows, ok := response.Payload["data"].([]any)
	if !ok || len(rows) == 0 {
		return 0, false, nil
	}

	var latestKey string
	var latestClose float64
	found := false
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		closePrice, ok := floatFromAny(row["close"])
		if !ok || closePrice <= 0 {
			continue
		}
		key := stringFromAny(row["datetime"])
		if key == "" {
			key = stringFromAny(row["trade_date"])
		}
		if !found || key >= latestKey {
			latestKey = key
			latestClose = closePrice
			found = true
		}
	}
	return latestClose, found, nil
}

func meridianErrorMessage(payload map[string]any) string {
	if message, ok := payload["message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if code, ok := payload["code"].(string); ok && strings.TrimSpace(code) != "" {
		return strings.TrimSpace(code)
	}
	return "meridian returned an error payload"
}

func floatFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func formatCSVFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatCSVOptionalFloat(value *float64) string {
	if value == nil {
		return ""
	}
	return formatCSVFloat(*value)
}

func floatPtr(value float64) *float64 {
	return &value
}

func formatCSVTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return timeutil.FormatRFC3339Nano(value)
}

func sanitizeFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "\"", "_", " ", "_", "\t", "_", "\n", "_", "\r", "_")
	return replacer.Replace(value)
}

func (s *Server) handleRefreshAsset(w http.ResponseWriter, r *http.Request, accountID string) {
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}
	opts, err := refreshOptionsFromRequest(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid refresh query", err.Error())
		return
	}
	result, err := s.orders.RefreshAsset(r.Context(), accountID, opts)
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
	opts, err := refreshOptionsFromRequest(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid refresh query", err.Error())
		return
	}
	result, err := s.orders.RefreshPositions(r.Context(), accountID, opts)
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
	opts, err := refreshOptionsFromRequest(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid refresh query", err.Error())
		return
	}
	result, err := s.orders.RefreshOrders(r.Context(), accountID, opts)
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
	opts, err := refreshOptionsFromRequest(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid refresh query", err.Error())
		return
	}
	result, err := s.orders.RefreshFills(r.Context(), accountID, opts)
	if err != nil {
		s.writeOrderError(w, r, err)
		return
	}
	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func refreshOptionsFromRequest(r *http.Request) (orderflow.RefreshOptions, error) {
	opts := orderflow.RefreshOptions{RequestID: httpx.RequestID(r)}
	rawTradeDate := strings.TrimSpace(r.URL.Query().Get("trade_date"))
	if rawTradeDate == "" {
		return opts, nil
	}
	normalized, err := normalizeAPIDate(rawTradeDate)
	if err != nil {
		return orderflow.RefreshOptions{}, err
	}
	opts.TradeDate = strings.ReplaceAll(normalized, "-", "")
	return opts, nil
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
	s.enrichFillNames(r.Context(), result.Fills)
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
	s.enrichFillNames(r.Context(), result.Fills)
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

func (s *Server) handleSettlementSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteMethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if s.orders == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "order service is unavailable", nil)
		return
	}

	defer r.Body.Close()
	var req SettlementSnapshotRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid settlement snapshot body", err.Error())
		return
	}
	if s.settles == nil && !req.DryRun {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settlement store is unavailable", nil)
		return
	}

	result, err := s.buildSettlementSnapshot(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid settlement snapshot request", err.Error())
		return
	}
	httpx.WriteOK(w, r, http.StatusAccepted, result)
}

func (s *Server) handleReconciliationBreaks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.settles == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settlement store is unavailable", nil)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	breaks, err := s.settles.ListReconciliationBreaks(r.Context(), ledger.ReconciliationBreakQuery{
		RunID:     r.URL.Query().Get("run_id"),
		AccountID: r.URL.Query().Get("account_id"),
		Status:    r.URL.Query().Get("status"),
		Limit:     limit,
	})
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "reconciliation break query failed", nil)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, map[string]any{"breaks": breaks})
}

func (s *Server) buildSettlementSnapshot(ctx context.Context, req SettlementSnapshotRequest) (SettlementSnapshotResult, error) {
	tradeDate, err := normalizeAPIDate(firstNonEmpty(req.TradeDate, timeutil.Now().Format("2006-01-02")))
	if err != nil {
		return SettlementSnapshotResult{}, err
	}
	snapshotType := firstNonEmpty(req.SnapshotType, "close")
	switch snapshotType {
	case "intraday", "open", "close", "reconcile":
	default:
		return SettlementSnapshotResult{}, fmt.Errorf("snapshot_type must be intraday, open, close, or reconcile")
	}
	source := firstNonEmpty(req.Source, "post_close_settlement")
	accountIDs := settlementAccountIDs(req.AccountIDs, s.cfg.Accounts)
	if len(accountIDs) == 0 {
		return SettlementSnapshotResult{}, fmt.Errorf("account_ids is required when no enabled accounts are configured")
	}
	runID := firstNonEmpty(req.RunID, fmt.Sprintf("%s-%s", source, strings.ReplaceAll(tradeDate, "-", "")))
	startedAt := timeutil.Now()
	result := SettlementSnapshotResult{
		RunID:        runID,
		TradeDate:    tradeDate,
		SnapshotType: snapshotType,
		Source:       source,
		Status:       "completed",
		DryRun:       req.DryRun,
		Accounts:     make([]SettlementSnapshotAccountResult, 0, len(accountIDs)),
		Errors:       []string{},
		StartedAt:    timeutil.FormatRFC3339Nano(startedAt),
	}

	for _, accountID := range accountIDs {
		accountResult := s.buildAccountSettlementSnapshot(ctx, accountID, tradeDate, snapshotType, source, runID, req.DryRun, startedAt)
		result.Accounts = append(result.Accounts, accountResult)
		result.AssetSnapshots += boolAsInt(accountResult.AssetSnapshotWritten)
		result.PositionSnapshots += accountResult.PositionSnapshotsWritten
		result.OrdersCount += accountResult.OrdersCount
		result.FillsCount += accountResult.FillsCount
		result.NonTerminalOrders += accountResult.NonTerminalOrders
		result.ReconciliationInputs += accountResult.ReconciliationInputs
		result.ReconciliationBreaks += accountResult.ReconciliationBreaks
		if len(accountResult.Errors) > 0 {
			result.Status = "failed"
			result.Errors = append(result.Errors, accountResult.Errors...)
		}
	}

	result.CompletedAt = timeutil.FormatRFC3339Nano(timeutil.Now())
	if !req.DryRun {
		run, err := s.settles.UpsertReconciliationRun(ctx, ledger.ReconciliationRun{
			RunID:        runID,
			TradeDate:    tradeDate,
			Status:       reconciliationStatus(result.Status),
			Source:       source,
			StartedAt:    startedAt,
			CompletedAt:  timeutil.Now(),
			Summary:      result.summary(),
			ErrorMessage: firstErrorSummary(result.Errors),
		})
		if err != nil {
			result.Status = "failed"
			result.Errors = append(result.Errors, fmt.Sprintf("reconciliation run: %v", err))
		} else {
			result.ReconciliationRun = &run
			for _, accountResult := range result.Accounts {
				for _, input := range accountResult.inputs {
					if err := s.settles.UpsertReconciliationInput(ctx, input); err != nil {
						result.Status = "failed"
						result.Errors = append(result.Errors, fmt.Sprintf("reconciliation input %s/%s: %v", input.Source, input.InputType, err))
					}
				}
				for _, item := range accountResult.breaks {
					if err := s.settles.UpsertReconciliationBreak(ctx, item); err != nil {
						result.Status = "failed"
						result.Errors = append(result.Errors, fmt.Sprintf("reconciliation break %s/%s: %v", item.BreakType, item.ObjectID, err))
					}
				}
			}
			if result.Status == "failed" {
				run, err := s.settles.UpsertReconciliationRun(ctx, ledger.ReconciliationRun{
					RunID:        runID,
					TradeDate:    tradeDate,
					Status:       "failed",
					Source:       source,
					StartedAt:    startedAt,
					CompletedAt:  timeutil.Now(),
					Summary:      result.summary(),
					ErrorMessage: firstErrorSummary(result.Errors),
				})
				if err == nil {
					result.ReconciliationRun = &run
				}
			}
		}
	}
	return result, nil
}

func (s *Server) buildAccountSettlementSnapshot(ctx context.Context, accountID string, tradeDate string, snapshotType string, source string, runID string, dryRun bool, capturedAt time.Time) SettlementSnapshotAccountResult {
	out := SettlementSnapshotAccountResult{AccountID: accountID, Breaks: []ledger.ReconciliationBreak{}}
	assetResult, err := s.orders.GetAsset(ctx, accountID)
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("asset: %v", err))
	}
	positionResult, err := s.orders.ListPositions(ctx, trading.PositionQuery{AccountID: accountID, Limit: 2000})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("positions: %v", err))
	}
	ordersResult, err := s.listAllSettlementOrders(ctx, accountID, tradeDate)
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("orders: %v", err))
	}
	fillsResult, err := s.listAllSettlementFills(ctx, accountID, tradeDate)
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("fills: %v", err))
	}
	out.PositionsCount = len(positionResult.Positions)
	out.OrdersCount = len(ordersResult.Orders)
	out.FillsCount = len(fillsResult.Fills)
	for _, order := range ordersResult.Orders {
		if !order.IsTerminal && !order.Status.Terminal() {
			out.NonTerminalOrders++
			if len(out.NonTerminalOrderIDs) < 20 {
				out.NonTerminalOrderIDs = append(out.NonTerminalOrderIDs, order.GatewayOrderID)
			}
		}
	}

	rawBase := map[string]any{
		"run_id":        runID,
		"trade_date":    tradeDate,
		"snapshot_type": snapshotType,
		"source":        source,
	}
	rawSummary := []ledger.RawStreamSummaryBucket{}
	if s.settles != nil {
		rawSummary, err = s.settles.RawStreamSummary(ctx, accountID, capturedAt, timeutil.Now())
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("raw stream summary: %v", err))
		}
	}

	out.inputs = append(out.inputs,
		reconciliationInput(runID, source, accountID, "relay_ledger_summary", capturedAt, relayLedgerSummaryPayload(accountID, tradeDate, assetResult.Asset, positionResult.Positions, ordersResult.Orders, fillsResult.Fills, out, rawBase)),
		reconciliationInput(runID, source, accountID, "pnl_input_summary", capturedAt, pnlInputSummaryPayload(assetResult.Asset, positionResult.Positions, fillsResult.Fills)),
		reconciliationInput(runID, source, accountID, "redis_raw_summary", capturedAt, map[string]any{
			"account_id": accountID,
			"trade_date": tradeDate,
			"window": map[string]any{
				"start": timeutil.FormatRFC3339Nano(capturedAt),
				"end":   timeutil.FormatRFC3339Nano(timeutil.Now()),
			},
			"buckets": rawSummary,
		}),
		reconciliationInput(runID, source, accountID, "counter_query_summary", capturedAt, map[string]any{
			"account_id": accountID,
			"trade_date": tradeDate,
			"errors":     out.Errors,
			"queries": map[string]any{
				"asset_ok":     assetResult.Asset.AccountID != "",
				"positions_ok": positionResult.Positions != nil,
				"orders_ok":    ordersResult.Orders != nil,
				"fills_ok":     fillsResult.Fills != nil,
			},
		}),
	)

	for _, errText := range out.Errors {
		out.breaks = append(out.breaks, reconciliationBreak(runID, accountID, "account_refresh_failed", "critical", "account", accountID, map[string]any{"error": errText}, nil, "settlement account query or raw summary failed"))
	}
	for _, order := range ordersResult.Orders {
		if !order.IsTerminal && !order.Status.Terminal() {
			out.breaks = append(out.breaks, reconciliationBreak(runID, accountID, "non_terminal_order", "warning", "order", order.GatewayOrderID, orderBreakPayload(order), nil, "order is not terminal at settlement"))
		}
	}
	out.breaks = append(out.breaks, orderFillQuantityBreaks(runID, accountID, ordersResult.Orders, fillsResult.Fills)...)

	if len(out.Errors) == 0 && !dryRun {
		assetRaw := cloneMap(rawBase)
		assetRaw["asset"] = assetResult.Asset
		if err := s.settles.UpsertAssetSnapshotForDate(ctx, assetResult.Asset, tradeDate, snapshotType, source, assetRaw, capturedAt); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("asset snapshot: %v", err))
			out.breaks = append(out.breaks, reconciliationBreak(runID, accountID, "asset_snapshot_missing", "critical", "asset", accountID, map[string]any{"error": err.Error(), "snapshot_type": snapshotType}, assetRaw, fmt.Sprintf("asset %s snapshot was not written", snapshotType)))
		} else {
			out.AssetSnapshotWritten = true
		}
		if snapshotType == "close" {
			for _, position := range positionResult.Positions {
				position.TradeDate = tradeDate
				positionRaw := cloneMap(rawBase)
				positionRaw["position"] = position
				if err := s.settles.UpsertPositionSnapshot(ctx, position, source, positionRaw, capturedAt); err != nil {
					out.Errors = append(out.Errors, fmt.Sprintf("position snapshot %s.%s: %v", position.Symbol, position.Exchange, err))
					out.breaks = append(out.breaks, reconciliationBreak(runID, accountID, "position_snapshot_missing", "critical", "position", fmt.Sprintf("%s.%s", position.Symbol, position.Exchange), map[string]any{"error": err.Error(), "snapshot_type": snapshotType}, positionRaw, "position close snapshot was not written"))
					continue
				}
				out.PositionSnapshotsWritten++
			}
		}
	}

	out.ReconciliationInputs = len(out.inputs)
	out.ReconciliationBreaks = len(out.breaks)
	out.Breaks = append(out.Breaks, out.breaks...)
	return out
}

func (s *Server) listAllSettlementOrders(ctx context.Context, accountID string, tradeDate string) (orderflow.ListOrdersResult, error) {
	query := trading.OrderQuery{AccountID: accountID, TradeDate: tradeDate, History: true, Limit: settlementPageLimit}
	combined := orderflow.ListOrdersResult{Query: query}
	for {
		result, err := s.orders.ListOrders(ctx, query)
		if err != nil {
			return combined, err
		}
		combined.Orders = append(combined.Orders, result.Orders...)
		combined.Count = len(combined.Orders)
		if result.NextCursor == "" {
			return combined, nil
		}
		if len(combined.Orders) >= settlementMaxRows {
			return combined, fmt.Errorf("settlement order query exceeded %d rows", settlementMaxRows)
		}
		query.Cursor = result.NextCursor
	}
}

func (s *Server) listAllSettlementFills(ctx context.Context, accountID string, tradeDate string) (orderflow.ListFillsResult, error) {
	query := trading.FillQuery{AccountID: accountID, TradeDate: tradeDate, History: true, Limit: settlementPageLimit}
	combined := orderflow.ListFillsResult{Query: query}
	for {
		result, err := s.orders.ListFills(ctx, query)
		if err != nil {
			return combined, err
		}
		combined.Fills = append(combined.Fills, result.Fills...)
		combined.Count = len(combined.Fills)
		if result.NextCursor == "" {
			return combined, nil
		}
		if len(combined.Fills) >= settlementMaxRows {
			return combined, fmt.Errorf("settlement fill query exceeded %d rows", settlementMaxRows)
		}
		query.Cursor = result.NextCursor
	}
}

func reconciliationInput(runID string, source string, accountID string, inputType string, capturedAt time.Time, payload map[string]any) ledger.ReconciliationInput {
	return ledger.ReconciliationInput{
		RunID:      runID,
		Source:     source,
		InputType:  accountID + ":" + inputType,
		Payload:    payload,
		CapturedAt: capturedAt,
	}
}

func reconciliationBreak(runID string, accountID string, breakType string, severity string, objectType string, objectID string, internalPayload map[string]any, externalPayload map[string]any, description string) ledger.ReconciliationBreak {
	return ledger.ReconciliationBreak{
		RunID:           runID,
		AccountID:       accountID,
		BreakType:       breakType,
		Severity:        severity,
		Status:          "open",
		ObjectType:      objectType,
		ObjectID:        objectID,
		InternalPayload: internalPayload,
		ExternalPayload: externalPayload,
		Description:     description,
	}
}

func relayLedgerSummaryPayload(accountID string, tradeDate string, asset trading.Asset, positions []trading.Position, orders []trading.Order, fills []trading.Fill, result SettlementSnapshotAccountResult, rawBase map[string]any) map[string]any {
	payload := cloneMap(rawBase)
	payload["account_id"] = accountID
	payload["trade_date"] = tradeDate
	payload["asset"] = map[string]any{
		"account_id":     asset.AccountID,
		"net_asset":      asset.NetAsset,
		"cash_available": asset.CashAvailable,
		"market_value":   asset.MarketValue,
	}
	payload["counts"] = map[string]any{
		"positions":           len(positions),
		"orders":              len(orders),
		"fills":               len(fills),
		"non_terminal_orders": result.NonTerminalOrders,
	}
	payload["non_terminal_order_ids"] = result.NonTerminalOrderIDs
	return payload
}

func pnlInputSummaryPayload(asset trading.Asset, positions []trading.Position, fills []trading.Fill) map[string]any {
	var positionMarketValue float64
	for _, position := range positions {
		positionMarketValue += position.MarketValue
	}
	var buyAmount float64
	var sellAmount float64
	var feeTotal float64
	for _, fill := range fills {
		amount := fill.Price * float64(fill.Qty)
		feeTotal += fill.Fee
		switch fill.TradeSide {
		case trading.TradeSideBuy, trading.TradeSidePurchase:
			buyAmount += amount
		case trading.TradeSideSell, trading.TradeSideRedemption:
			sellAmount += amount
		}
	}
	return map[string]any{
		"asset": map[string]any{
			"net_asset":       asset.NetAsset,
			"cash_available":  asset.CashAvailable,
			"market_value":    asset.MarketValue,
			"day_profit":      asset.DayProfit,
			"position_profit": asset.PositionProfit,
			"close_profit":    asset.CloseProfit,
		},
		"positions": map[string]any{
			"count":        len(positions),
			"market_value": positionMarketValue,
		},
		"fills": map[string]any{
			"count":       len(fills),
			"buy_amount":  buyAmount,
			"sell_amount": sellAmount,
			"fee_total":   feeTotal,
		},
	}
}

func orderFillQuantityBreaks(runID string, accountID string, orders []trading.Order, fills []trading.Fill) []ledger.ReconciliationBreak {
	fillQtyByOrder := make(map[string]int64)
	for _, fill := range fills {
		fillQtyByOrder[fill.GatewayOrderID] += fill.Qty
	}
	out := make([]ledger.ReconciliationBreak, 0)
	for _, order := range orders {
		if order.CumFilledQty == 0 {
			continue
		}
		fillQty := fillQtyByOrder[order.GatewayOrderID]
		if fillQty == order.CumFilledQty {
			continue
		}
		out = append(out, reconciliationBreak(runID, accountID, "order_fill_qty_mismatch", "warning", "order", order.GatewayOrderID, orderBreakPayload(order), map[string]any{"fill_qty": fillQty}, "order cum_filled_qty does not match summed fill qty"))
	}
	return out
}

func orderBreakPayload(order trading.Order) map[string]any {
	return map[string]any{
		"gateway_order_id": order.GatewayOrderID,
		"status":           order.Status,
		"gateway_status":   order.GatewayStatus,
		"is_terminal":      order.IsTerminal,
		"order_qty":        order.OrderQty,
		"cum_filled_qty":   order.CumFilledQty,
		"leaves_qty":       order.LeavesQty,
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
		Environment:        string(s.cfg.Service.Environment),
		Status:             status,
		PublicURL:          s.cfg.Service.PublicURL,
		Timezone:           s.cfg.Service.Timezone,
		TradingDay:         currentTradingDayStatus(),
		StartedAt:          s.started,
		UptimeSeconds:      int64(time.Since(s.started).Seconds()),
		AccountsConfigured: len(s.cfg.Accounts),
		Accounts:           summarizeAccounts(s.cfg.Accounts),
		JobsConfigured:     len(s.cfg.Jobs),
		Jobs:               jobScheduleViews(s.cfg.Jobs, s.cfg.Service.Timezone),
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

func jobScheduleViews(jobs map[string]config.JobConfig, timezone string) map[string]JobScheduleView {
	if len(jobs) == 0 {
		return nil
	}
	out := make(map[string]JobScheduleView, len(jobs))
	for name, job := range jobs {
		out[name] = JobScheduleView{
			JobName:      name,
			Enabled:      job.Enabled,
			Schedule:     strings.TrimSpace(job.Schedule),
			ExpectedTime: expectedTimeFromCron(job.Schedule),
			Timezone:     timezone,
		}
	}
	return out
}

func expectedTimeFromCron(schedule string) string {
	fields := strings.Fields(schedule)
	if len(fields) < 2 {
		return ""
	}
	minute, errMinute := strconv.Atoi(fields[0])
	hour, errHour := strconv.Atoi(fields[1])
	if errMinute != nil || errHour != nil || minute < 0 || minute > 59 || hour < 0 || hour > 23 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

type StatusView struct {
	Service            string                      `json:"service"`
	Mode               string                      `json:"mode"`
	Environment        string                      `json:"environment"`
	Status             string                      `json:"status"`
	PublicURL          string                      `json:"public_url"`
	Timezone           string                      `json:"timezone"`
	TradingDay         TradingDayStatusView        `json:"trading_day"`
	StartedAt          time.Time                   `json:"started_at"`
	UptimeSeconds      int64                       `json:"uptime_seconds"`
	AccountsConfigured int                         `json:"accounts_configured"`
	Accounts           AccountStatusSummary        `json:"accounts"`
	JobsConfigured     int                         `json:"jobs_configured"`
	Jobs               map[string]JobScheduleView  `json:"jobs,omitempty"`
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

type JobScheduleView struct {
	JobName      string `json:"job_name"`
	Enabled      bool   `json:"enabled"`
	Schedule     string `json:"schedule,omitempty"`
	ExpectedTime string `json:"expected_time,omitempty"`
	Timezone     string `json:"timezone"`
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
	Alias          string `json:"alias,omitempty"`
	BrokerID       string `json:"broker_id"`
	GatewayID      string `json:"gateway_id"`
	Enabled        bool   `json:"enabled"`
	TradingEnabled bool   `json:"trading_enabled"`
	Simulated      bool   `json:"simulated"`
}

type AccountRouteView struct {
	AccountID      string                  `json:"account_id"`
	Alias          string                  `json:"alias,omitempty"`
	BrokerID       string                  `json:"broker_id"`
	GatewayID      string                  `json:"gateway_id"`
	StreamPrefix   string                  `json:"stream_prefix"`
	Enabled        bool                    `json:"enabled"`
	TradingEnabled bool                    `json:"trading_enabled"`
	QueryEnabled   bool                    `json:"query_enabled"`
	ReadOnly       bool                    `json:"read_only"`
	Simulated      bool                    `json:"simulated"`
	Environment    string                  `json:"environment"`
	Streams        AccountRouteStreamsView `json:"streams"`
}

type AccountRouteStreamsView struct {
	CmdTrade string `json:"cmd_trade"`
	CmdQuery string `json:"cmd_query"`
	Reply    string `json:"reply"`
	Event    string `json:"event"`
	HB       string `json:"hb"`
	DLQ      string `json:"dlq"`
}

type AccountAliasRequest struct {
	Alias string `json:"alias"`
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

type SettlementSnapshotRequest struct {
	RunID        string   `json:"run_id,omitempty"`
	TradeDate    string   `json:"trade_date,omitempty"`
	AccountIDs   []string `json:"account_ids,omitempty"`
	SnapshotType string   `json:"snapshot_type,omitempty"`
	Source       string   `json:"source,omitempty"`
	DryRun       bool     `json:"dry_run,omitempty"`
}

type SettlementSnapshotResult struct {
	RunID                string                            `json:"run_id"`
	TradeDate            string                            `json:"trade_date"`
	SnapshotType         string                            `json:"snapshot_type"`
	Source               string                            `json:"source"`
	Status               string                            `json:"status"`
	DryRun               bool                              `json:"dry_run,omitempty"`
	StartedAt            string                            `json:"started_at"`
	CompletedAt          string                            `json:"completed_at"`
	AssetSnapshots       int                               `json:"asset_snapshots"`
	PositionSnapshots    int                               `json:"position_snapshots"`
	OrdersCount          int                               `json:"orders_count"`
	FillsCount           int                               `json:"fills_count"`
	NonTerminalOrders    int                               `json:"non_terminal_orders"`
	ReconciliationInputs int                               `json:"reconciliation_inputs"`
	ReconciliationBreaks int                               `json:"reconciliation_breaks"`
	Accounts             []SettlementSnapshotAccountResult `json:"accounts"`
	ReconciliationRun    *ledger.ReconciliationRun         `json:"reconciliation_run,omitempty"`
	Errors               []string                          `json:"errors,omitempty"`
}

type SettlementSnapshotAccountResult struct {
	AccountID                string                       `json:"account_id"`
	AssetSnapshotWritten     bool                         `json:"asset_snapshot_written"`
	PositionsCount           int                          `json:"positions_count"`
	PositionSnapshotsWritten int                          `json:"position_snapshots_written"`
	OrdersCount              int                          `json:"orders_count"`
	FillsCount               int                          `json:"fills_count"`
	NonTerminalOrders        int                          `json:"non_terminal_orders"`
	NonTerminalOrderIDs      []string                     `json:"non_terminal_order_ids,omitempty"`
	Errors                   []string                     `json:"errors,omitempty"`
	ReconciliationInputs     int                          `json:"reconciliation_inputs"`
	ReconciliationBreaks     int                          `json:"reconciliation_breaks"`
	Breaks                   []ledger.ReconciliationBreak `json:"breaks,omitempty"`
	inputs                   []ledger.ReconciliationInput
	breaks                   []ledger.ReconciliationBreak
}

func (result SettlementSnapshotResult) summary() map[string]any {
	return map[string]any{
		"run_id":                result.RunID,
		"trade_date":            result.TradeDate,
		"snapshot_type":         result.SnapshotType,
		"source":                result.Source,
		"status":                result.Status,
		"dry_run":               result.DryRun,
		"asset_snapshots":       result.AssetSnapshots,
		"position_snapshots":    result.PositionSnapshots,
		"orders_count":          result.OrdersCount,
		"fills_count":           result.FillsCount,
		"non_terminal_orders":   result.NonTerminalOrders,
		"reconciliation_inputs": result.ReconciliationInputs,
		"reconciliation_breaks": result.ReconciliationBreaks,
		"accounts":              result.Accounts,
		"errors":                result.Errors,
	}
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

func normalizeAPIDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("trade_date is required")
	}
	layout := "2006-01-02"
	if len(value) == 8 {
		layout = "20060102"
	}
	parsed, err := time.ParseInLocation(layout, value, timeutil.Location())
	if err != nil {
		return "", errors.New("trade_date must be YYYYMMDD or YYYY-MM-DD")
	}
	return parsed.Format("2006-01-02"), nil
}

func settlementAccountIDs(requested []string, configured []config.AccountRouteConfig) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(accountID string) {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			return
		}
		if _, ok := seen[accountID]; ok {
			return
		}
		seen[accountID] = struct{}{}
		out = append(out, accountID)
	}
	for _, accountID := range requested {
		add(accountID)
	}
	if len(out) > 0 {
		return out
	}
	for _, account := range configured {
		if account.Enabled {
			add(account.AccountID)
		}
	}
	return out
}

func cloneMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func boolAsInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func reconciliationStatus(status string) string {
	if status == "failed" {
		return "failed"
	}
	return "completed"
}

func firstErrorSummary(errors []string) string {
	if len(errors) == 0 {
		return ""
	}
	if len(errors) > 5 {
		errors = errors[:5]
	}
	return strings.Join(errors, "; ")
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
