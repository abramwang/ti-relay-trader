package orderflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/trading"
)

var (
	ErrRouteNotFound                   = errors.New("account route not found")
	ErrAccountDisabled                 = errors.New("account is disabled")
	ErrTradingDisabled                 = errors.New("account trading is disabled")
	ErrOrderTerminalNotCancelable      = errors.New("order is terminal and cannot be cancelled")
	ErrOrderWithoutLeavesNotCancelable = errors.New("order has no leaves quantity and cannot be cancelled")
	ErrMissingLedger                   = errors.New("ledger writer is required")
	ErrMissingPublisher                = errors.New("command publisher is required")
)

type LedgerWriter interface {
	UpsertAccount(ctx context.Context, account trading.Account) error
	UpsertOrder(ctx context.Context, order trading.Order) error
	GetOrder(ctx context.Context, accountID string, gatewayOrderID string) (trading.Order, error)
	ListOrders(ctx context.Context, query trading.OrderQuery) ([]trading.Order, error)
	ListFills(ctx context.Context, query trading.FillQuery) ([]trading.Fill, error)
	GetLatestAsset(ctx context.Context, accountID string) (trading.Asset, error)
	ListPositions(ctx context.Context, query trading.PositionQuery) ([]trading.Position, error)
	ArchiveRawStreamMessage(ctx context.Context, message ledger.RawStreamMessage) error
}

type CommandPublisher interface {
	PublishCommand(ctx context.Context, streamKey string, envelope redisstream.CommandEnvelope) (redisstream.CommandPublishResult, error)
}

type IDGenerator interface {
	NewID(prefix string) string
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	cfg       config.Config
	ledger    LedgerWriter
	publisher CommandPublisher
	ids       IDGenerator
	clock     Clock
}

type Options struct {
	Config    config.Config
	Ledger    LedgerWriter
	Publisher CommandPublisher
	IDs       IDGenerator
	Clock     Clock
}

type SubmitOptions struct {
	RequestID string
}

type BatchSubmitOptions struct {
	RequestID string
}

type CancelOptions struct {
	RequestID string
}

type SubmitOrderResult struct {
	Order          trading.Order                    `json:"order"`
	MessageID      string                           `json:"message_id"`
	StreamKey      string                           `json:"stream_key"`
	StreamID       string                           `json:"stream_id"`
	IdempotencyKey string                           `json:"idempotency_key"`
	RequestID      string                           `json:"request_id,omitempty"`
	Published      redisstream.CommandPublishResult `json:"published"`
}

type CancelOrderResult struct {
	Order          trading.Order                    `json:"order"`
	CancelID       string                           `json:"cancel_id"`
	MessageID      string                           `json:"message_id"`
	StreamKey      string                           `json:"stream_key"`
	StreamID       string                           `json:"stream_id"`
	IdempotencyKey string                           `json:"idempotency_key"`
	RequestID      string                           `json:"request_id,omitempty"`
	Published      redisstream.CommandPublishResult `json:"published"`
}

type BatchSubmitOrderResult struct {
	Orders         []trading.Order                  `json:"orders"`
	MessageID      string                           `json:"message_id"`
	StreamKey      string                           `json:"stream_key"`
	StreamID       string                           `json:"stream_id"`
	IdempotencyKey string                           `json:"idempotency_key"`
	RequestID      string                           `json:"request_id,omitempty"`
	Published      redisstream.CommandPublishResult `json:"published"`
}

type ListOrdersResult struct {
	Orders []trading.Order    `json:"orders"`
	Query  trading.OrderQuery `json:"query"`
	Count  int                `json:"count"`
}

type ListFillsResult struct {
	Fills []trading.Fill    `json:"fills"`
	Query trading.FillQuery `json:"query"`
	Count int               `json:"count"`
}

type GetAssetResult struct {
	Asset trading.Asset `json:"asset"`
}

type ListPositionsResult struct {
	Positions []trading.Position    `json:"positions"`
	Query     trading.PositionQuery `json:"query"`
	Count     int                   `json:"count"`
}

func New(opts Options) (*Service, error) {
	if opts.Ledger == nil {
		return nil, ErrMissingLedger
	}
	if opts.IDs == nil {
		opts.IDs = defaultIDGenerator{}
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	return &Service{
		cfg:       opts.Config,
		ledger:    opts.Ledger,
		publisher: opts.Publisher,
		ids:       opts.IDs,
		clock:     opts.Clock,
	}, nil
}

func (service *Service) SubmitOrder(ctx context.Context, req trading.SubmitOrderRequest, opts SubmitOptions) (SubmitOrderResult, error) {
	if err := req.Validate(); err != nil {
		return SubmitOrderResult{}, err
	}
	route, err := service.routeForAccount(req.AccountID)
	if err != nil {
		return SubmitOrderResult{}, err
	}
	if service.publisher == nil {
		return SubmitOrderResult{}, ErrMissingPublisher
	}

	normalized := req
	if strings.TrimSpace(normalized.GatewayOrderID) == "" {
		normalized.GatewayOrderID = service.ids.NewID("gw")
	}
	if strings.TrimSpace(normalized.IdempotencyKey) == "" {
		normalized.IdempotencyKey = "order:" + normalized.AccountID + ":" + normalized.GatewayOrderID
	}
	if strings.TrimSpace(normalized.ClientOrderID) == "" {
		normalized.ClientOrderID = normalized.GatewayOrderID
	}

	now := service.clock.Now().UTC()
	messageID := service.ids.NewID("msg-order-submit")
	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = service.ids.NewID("req-order-submit")
	}
	order := draftOrderFromRequest(normalized, now)
	order.OriginMessageID = messageID
	order.RequestID = requestID

	account := trading.Account{
		AccountID:      route.AccountID,
		BrokerID:       route.BrokerID,
		GatewayID:      route.GatewayID,
		StreamPrefix:   route.StreamPrefix,
		Status:         trading.AccountStatusEnabled,
		Enabled:        route.Enabled,
		TradingEnabled: route.TradingEnabled,
		Simulated:      route.Simulated,
		Tags: map[string]string{
			"source": "config",
		},
		UpdatedAt: now,
	}
	if err := service.ledger.UpsertAccount(ctx, account); err != nil {
		return SubmitOrderResult{}, err
	}
	if err := service.ledger.UpsertOrder(ctx, order); err != nil {
		return SubmitOrderResult{}, err
	}

	streamKey, err := redisstream.CommandStreamForAction(redisstream.NewStreams(route.StreamPrefix), redisstream.ActionOrderSubmit)
	if err != nil {
		return SubmitOrderResult{}, err
	}
	envelope := redisstream.NewCommandEnvelope(
		redisstream.ActionOrderSubmit,
		messageID,
		requestID,
		requestID,
		normalized.IdempotencyKey,
		normalized,
		now,
	)
	published, err := service.publisher.PublishCommand(ctx, streamKey, envelope)
	if err != nil {
		return SubmitOrderResult{}, err
	}
	if err := service.archiveCommand(ctx, published, envelope, redisstream.SuffixCmdTrade, normalized.AccountID, normalized.GatewayOrderID); err != nil {
		return SubmitOrderResult{}, err
	}

	return SubmitOrderResult{
		Order:          order,
		MessageID:      messageID,
		StreamKey:      published.StreamKey,
		StreamID:       published.StreamID,
		IdempotencyKey: normalized.IdempotencyKey,
		RequestID:      requestID,
		Published:      published,
	}, nil
}

func (service *Service) BatchSubmitOrders(ctx context.Context, req trading.BatchSubmitOrderRequest, opts BatchSubmitOptions) (BatchSubmitOrderResult, error) {
	if err := req.Validate(); err != nil {
		return BatchSubmitOrderResult{}, err
	}
	route, err := service.routeForAccount(req.AccountID)
	if err != nil {
		return BatchSubmitOrderResult{}, err
	}
	if service.publisher == nil {
		return BatchSubmitOrderResult{}, ErrMissingPublisher
	}

	normalized := req
	now := service.clock.Now().UTC()
	messageID := service.ids.NewID("msg-order-batch-submit")
	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = service.ids.NewID("req-order-batch-submit")
	}
	if strings.TrimSpace(normalized.IdempotencyKey) == "" {
		normalized.IdempotencyKey = "batch:" + normalized.AccountID + ":" + service.ids.NewID("batch")
	}

	account := trading.Account{
		AccountID:      route.AccountID,
		BrokerID:       route.BrokerID,
		GatewayID:      route.GatewayID,
		StreamPrefix:   route.StreamPrefix,
		Status:         trading.AccountStatusEnabled,
		Enabled:        route.Enabled,
		TradingEnabled: route.TradingEnabled,
		Simulated:      route.Simulated,
		Tags: map[string]string{
			"source": "config",
		},
		UpdatedAt: now,
	}
	if err := service.ledger.UpsertAccount(ctx, account); err != nil {
		return BatchSubmitOrderResult{}, err
	}

	orders := make([]trading.Order, 0, len(normalized.Orders))
	for i := range normalized.Orders {
		orderReq := normalized.Orders[i]
		if strings.TrimSpace(orderReq.AccountID) == "" {
			orderReq.AccountID = normalized.AccountID
		}
		if strings.TrimSpace(orderReq.GatewayOrderID) == "" {
			orderReq.GatewayOrderID = service.ids.NewID("gw")
		}
		if strings.TrimSpace(orderReq.ClientOrderID) == "" {
			orderReq.ClientOrderID = orderReq.GatewayOrderID
		}
		if strings.TrimSpace(orderReq.IdempotencyKey) == "" {
			orderReq.IdempotencyKey = normalized.IdempotencyKey + ":" + orderReq.GatewayOrderID
		}
		normalized.Orders[i] = orderReq

		order := draftOrderFromRequest(orderReq, now)
		order.OriginMessageID = messageID
		order.RequestID = requestID
		order.AdapterContext["batch_source"] = "relay-api"
		order.AdapterContext["batch_index"] = i
		if err := service.ledger.UpsertOrder(ctx, order); err != nil {
			return BatchSubmitOrderResult{}, err
		}
		orders = append(orders, order)
	}

	streamKey, err := redisstream.CommandStreamForAction(redisstream.NewStreams(route.StreamPrefix), redisstream.ActionOrderBatchSubmit)
	if err != nil {
		return BatchSubmitOrderResult{}, err
	}
	envelope := redisstream.NewCommandEnvelope(
		redisstream.ActionOrderBatchSubmit,
		messageID,
		requestID,
		requestID,
		normalized.IdempotencyKey,
		normalized,
		now,
	)
	published, err := service.publisher.PublishCommand(ctx, streamKey, envelope)
	if err != nil {
		return BatchSubmitOrderResult{}, err
	}
	if err := service.archiveCommand(ctx, published, envelope, redisstream.SuffixCmdTrade, normalized.AccountID, ""); err != nil {
		return BatchSubmitOrderResult{}, err
	}

	return BatchSubmitOrderResult{
		Orders:         orders,
		MessageID:      messageID,
		StreamKey:      published.StreamKey,
		StreamID:       published.StreamID,
		IdempotencyKey: normalized.IdempotencyKey,
		RequestID:      requestID,
		Published:      published,
	}, nil
}

func (service *Service) CancelOrder(ctx context.Context, req trading.CancelOrderRequest, opts CancelOptions) (CancelOrderResult, error) {
	if err := req.Validate(); err != nil {
		return CancelOrderResult{}, err
	}
	route, err := service.routeForAccount(req.AccountID)
	if err != nil {
		return CancelOrderResult{}, err
	}
	if service.publisher == nil {
		return CancelOrderResult{}, ErrMissingPublisher
	}

	order, err := service.ledger.GetOrder(ctx, req.AccountID, req.GatewayOrderID)
	if err != nil {
		return CancelOrderResult{}, err
	}
	if order.IsTerminal || order.Status.Terminal() {
		return CancelOrderResult{}, fmt.Errorf("%w: %s/%s status=%s", ErrOrderTerminalNotCancelable, req.AccountID, req.GatewayOrderID, order.Status)
	}
	if order.OrderQty > 0 && order.LeavesQty <= 0 {
		return CancelOrderResult{}, fmt.Errorf("%w: %s/%s", ErrOrderWithoutLeavesNotCancelable, req.AccountID, req.GatewayOrderID)
	}

	normalized := req
	if strings.TrimSpace(normalized.CancelID) == "" {
		normalized.CancelID = service.ids.NewID("cancel")
	}
	if strings.TrimSpace(normalized.IdempotencyKey) == "" {
		normalized.IdempotencyKey = "cancel:" + normalized.AccountID + ":" + normalized.GatewayOrderID + ":" + normalized.CancelID
	}

	now := service.clock.Now().UTC()
	messageID := service.ids.NewID("msg-order-cancel")
	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = service.ids.NewID("req-order-cancel")
	}
	streamKey, err := redisstream.CommandStreamForAction(redisstream.NewStreams(route.StreamPrefix), redisstream.ActionOrderCancel)
	if err != nil {
		return CancelOrderResult{}, err
	}
	envelope := redisstream.NewCommandEnvelope(
		redisstream.ActionOrderCancel,
		messageID,
		requestID,
		requestID,
		normalized.IdempotencyKey,
		normalized,
		now,
	)
	published, err := service.publisher.PublishCommand(ctx, streamKey, envelope)
	if err != nil {
		return CancelOrderResult{}, err
	}
	if err := service.archiveCommand(ctx, published, envelope, redisstream.SuffixCmdTrade, normalized.AccountID, normalized.GatewayOrderID); err != nil {
		return CancelOrderResult{}, err
	}

	return CancelOrderResult{
		Order:          order,
		CancelID:       normalized.CancelID,
		MessageID:      messageID,
		StreamKey:      published.StreamKey,
		StreamID:       published.StreamID,
		IdempotencyKey: normalized.IdempotencyKey,
		RequestID:      requestID,
		Published:      published,
	}, nil
}

func (service *Service) ListOrders(ctx context.Context, query trading.OrderQuery) (ListOrdersResult, error) {
	normalized, err := normalizeOrderQuery(query)
	if err != nil {
		return ListOrdersResult{}, err
	}
	orders, err := service.ledger.ListOrders(ctx, normalized)
	if err != nil {
		return ListOrdersResult{}, err
	}
	return ListOrdersResult{
		Orders: orders,
		Query:  normalized,
		Count:  len(orders),
	}, nil
}

func (service *Service) ListFills(ctx context.Context, query trading.FillQuery) (ListFillsResult, error) {
	normalized, err := normalizeFillQuery(query)
	if err != nil {
		return ListFillsResult{}, err
	}
	fills, err := service.ledger.ListFills(ctx, normalized)
	if err != nil {
		return ListFillsResult{}, err
	}
	return ListFillsResult{
		Fills: fills,
		Query: normalized,
		Count: len(fills),
	}, nil
}

func (service *Service) GetAsset(ctx context.Context, accountID string) (GetAssetResult, error) {
	accountID = strings.TrimSpace(accountID)
	if _, err := service.routeForConfiguredAccount(accountID); err != nil {
		return GetAssetResult{}, err
	}
	asset, err := service.ledger.GetLatestAsset(ctx, accountID)
	if err != nil {
		return GetAssetResult{}, err
	}
	return GetAssetResult{Asset: asset}, nil
}

func (service *Service) ListPositions(ctx context.Context, query trading.PositionQuery) (ListPositionsResult, error) {
	normalized, err := normalizePositionQuery(query)
	if err != nil {
		return ListPositionsResult{}, err
	}
	if _, err := service.routeForConfiguredAccount(normalized.AccountID); err != nil {
		return ListPositionsResult{}, err
	}
	positions, err := service.ledger.ListPositions(ctx, normalized)
	if err != nil {
		return ListPositionsResult{}, err
	}
	return ListPositionsResult{
		Positions: positions,
		Query:     normalized,
		Count:     len(positions),
	}, nil
}

func (service *Service) routeForConfiguredAccount(accountID string) (config.AccountRouteConfig, error) {
	route, ok := service.cfg.AccountRoute(strings.TrimSpace(accountID))
	if !ok {
		return config.AccountRouteConfig{}, fmt.Errorf("%w: %s", ErrRouteNotFound, accountID)
	}
	return route, nil
}

func (service *Service) routeForAccount(accountID string) (config.AccountRouteConfig, error) {
	route, ok := service.cfg.AccountRoute(accountID)
	if !ok {
		return config.AccountRouteConfig{}, fmt.Errorf("%w: %s", ErrRouteNotFound, accountID)
	}
	if !route.Enabled {
		return config.AccountRouteConfig{}, fmt.Errorf("%w: %s", ErrAccountDisabled, accountID)
	}
	if !route.TradingEnabled {
		return config.AccountRouteConfig{}, fmt.Errorf("%w: %s", ErrTradingDisabled, accountID)
	}
	return route, nil
}

func (service *Service) archiveCommand(ctx context.Context, published redisstream.CommandPublishResult, envelope redisstream.CommandEnvelope, role string, accountID string, gatewayOrderID string) error {
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return service.ledger.ArchiveRawStreamMessage(ctx, ledger.RawStreamMessage{
		StreamRef: ledger.StreamRef{
			Key: published.StreamKey,
			ID:  published.StreamID,
		},
		SourceRef: ledger.SourceRef{
			OriginMessageID: envelope.MessageID,
			RequestID:       envelope.RequestID,
			CorrelationID:   envelope.CorrelationID,
			IdempotencyKey:  envelope.IdempotencyKey,
		},
		Direction:      "in",
		Role:           role,
		MessageType:    envelope.MessageType,
		Action:         envelope.Action,
		AccountID:      accountID,
		GatewayOrderID: gatewayOrderID,
		Body:           json.RawMessage(body),
		BodyText:       string(body),
		ReceivedAt:     service.clock.Now().UTC(),
	})
}

func normalizeOrderQuery(query trading.OrderQuery) (trading.OrderQuery, error) {
	normalized := query
	normalized.AccountID = strings.TrimSpace(normalized.AccountID)
	normalized.GatewayOrderID = strings.TrimSpace(normalized.GatewayOrderID)
	normalized.ClientOrderID = strings.TrimSpace(normalized.ClientOrderID)
	normalized.Symbol = strings.TrimSpace(normalized.Symbol)
	normalized.Cursor = strings.TrimSpace(normalized.Cursor)
	if normalized.Exchange != "" && !normalized.Exchange.Valid() {
		return normalized, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", trading.ErrInvalidSchema)
	}
	if normalized.Status != "" && !normalized.Status.Valid() {
		return normalized, fmt.Errorf("%w: status is invalid", trading.ErrInvalidSchema)
	}
	if normalized.Limit <= 0 {
		normalized.Limit = 100
	}
	if normalized.Limit > 500 {
		normalized.Limit = 500
	}
	return normalized, nil
}

func normalizeFillQuery(query trading.FillQuery) (trading.FillQuery, error) {
	normalized := query
	normalized.AccountID = strings.TrimSpace(normalized.AccountID)
	normalized.GatewayOrderID = strings.TrimSpace(normalized.GatewayOrderID)
	normalized.Symbol = strings.TrimSpace(normalized.Symbol)
	normalized.Cursor = strings.TrimSpace(normalized.Cursor)
	if normalized.Exchange != "" && !normalized.Exchange.Valid() {
		return normalized, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", trading.ErrInvalidSchema)
	}
	if normalized.Limit <= 0 {
		normalized.Limit = 100
	}
	if normalized.Limit > 500 {
		normalized.Limit = 500
	}
	return normalized, nil
}

func normalizePositionQuery(query trading.PositionQuery) (trading.PositionQuery, error) {
	normalized := query
	normalized.AccountID = strings.TrimSpace(normalized.AccountID)
	normalized.Symbol = strings.TrimSpace(normalized.Symbol)
	normalized.Cursor = strings.TrimSpace(normalized.Cursor)
	if normalized.AccountID == "" {
		return normalized, fmt.Errorf("%w: account_id is required", trading.ErrInvalidSchema)
	}
	if normalized.Exchange != "" && !normalized.Exchange.Valid() {
		return normalized, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", trading.ErrInvalidSchema)
	}
	if normalized.Limit <= 0 {
		normalized.Limit = 500
	}
	if normalized.Limit > 2000 {
		normalized.Limit = 2000
	}
	return normalized, nil
}

func draftOrderFromRequest(req trading.SubmitOrderRequest, now time.Time) trading.Order {
	return trading.Order{
		AccountID:       req.AccountID,
		ClientOrderID:   req.ClientOrderID,
		GatewayOrderID:  req.GatewayOrderID,
		Symbol:          req.Symbol,
		Exchange:        req.Exchange,
		TradeSide:       req.TradeSide,
		BusinessType:    req.BusinessType,
		OffsetType:      req.OffsetType,
		LimitPrice:      req.Price,
		OrderQty:        req.Qty,
		LeavesQty:       req.Qty,
		Status:          trading.OrderStatusCreated,
		GatewayStatus:   trading.GatewayStatusAccepted,
		IsTerminal:      false,
		OriginMessageID: "",
		RequestID:       "",
		IdempotencyKey:  req.IdempotencyKey,
		CreatedAt:       now,
		LastUpdatedAt:   now,
		AdapterContext: map[string]any{
			"draft_source": "relay-api",
		},
	}
}

type defaultIDGenerator struct{}

var defaultCounter atomic.Uint64

func (defaultIDGenerator) NewID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), defaultCounter.Add(1))
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
