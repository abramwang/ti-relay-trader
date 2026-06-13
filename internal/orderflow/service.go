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
	ErrRouteNotFound    = errors.New("account route not found")
	ErrAccountDisabled  = errors.New("account is disabled")
	ErrTradingDisabled  = errors.New("account trading is disabled")
	ErrMissingLedger    = errors.New("ledger writer is required")
	ErrMissingPublisher = errors.New("command publisher is required")
)

type LedgerWriter interface {
	UpsertAccount(ctx context.Context, account trading.Account) error
	UpsertOrder(ctx context.Context, order trading.Order) error
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

type SubmitOrderResult struct {
	Order          trading.Order                    `json:"order"`
	MessageID      string                           `json:"message_id"`
	StreamKey      string                           `json:"stream_key"`
	StreamID       string                           `json:"stream_id"`
	IdempotencyKey string                           `json:"idempotency_key"`
	RequestID      string                           `json:"request_id,omitempty"`
	Published      redisstream.CommandPublishResult `json:"published"`
}

func New(opts Options) (*Service, error) {
	if opts.Ledger == nil {
		return nil, ErrMissingLedger
	}
	if opts.Publisher == nil {
		return nil, ErrMissingPublisher
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
	requestID := firstNonEmpty(opts.RequestID, service.ids.NewID("req-order-submit"))
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
	if err := service.archiveCommand(ctx, published, envelope, order); err != nil {
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

func (service *Service) archiveCommand(ctx context.Context, published redisstream.CommandPublishResult, envelope redisstream.CommandEnvelope, order trading.Order) error {
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
		Role:           redisstream.SuffixCmdTrade,
		MessageType:    envelope.MessageType,
		Action:         envelope.Action,
		AccountID:      order.AccountID,
		GatewayOrderID: order.GatewayOrderID,
		Body:           json.RawMessage(body),
		BodyText:       string(body),
		ReceivedAt:     service.clock.Now().UTC(),
	})
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
