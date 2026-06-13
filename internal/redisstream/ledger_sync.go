package redisstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

type LedgerWriter interface {
	UpsertAccount(ctx context.Context, account trading.Account) error
	UpsertOrder(ctx context.Context, order trading.Order) error
	AppendOrderEvent(ctx context.Context, event trading.OrderEvent, stream ledger.StreamRef, source ledger.SourceRef) error
	InsertFill(ctx context.Context, fill trading.Fill, stream ledger.StreamRef, source ledger.SourceRef) error
	ArchiveRawStreamMessage(ctx context.Context, message ledger.RawStreamMessage) error
}

type LedgerSyncOptions struct {
	Prefixes []string
	StartID  string
	Count    int64
	Block    time.Duration
	Roles    []string
}

type LedgerSyncReport struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Protocol    string               `json:"protocol"`
	RedisAddr   string               `json:"redis_addr"`
	Prefixes    []string             `json:"prefixes"`
	StartID     string               `json:"start_id"`
	Count       int64                `json:"count"`
	Block       string               `json:"block,omitempty"`
	Totals      LedgerProcessResult  `json:"totals"`
	Streams     []LedgerStreamReport `json:"streams"`
}

type LedgerStreamReport struct {
	Name    string              `json:"name"`
	Role    string              `json:"role"`
	StartID string              `json:"start_id"`
	Count   int                 `json:"count"`
	Totals  LedgerProcessResult `json:"totals"`
	Errors  []LedgerEntryError  `json:"errors,omitempty"`
}

type LedgerEntryError struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error"`
}

type LedgerProcessResult struct {
	Seen           int      `json:"seen"`
	Archived       int      `json:"archived"`
	Accounts       int      `json:"accounts"`
	Orders         int      `json:"orders"`
	OrderEvents    int      `json:"order_events"`
	Fills          int      `json:"fills"`
	Replies        int      `json:"replies"`
	Skipped        int      `json:"skipped"`
	SkipReasons    []string `json:"skip_reasons,omitempty"`
	ParseErrors    int      `json:"parse_errors"`
	LedgerErrors   int      `json:"ledger_errors"`
	Unsupported    int      `json:"unsupported"`
	LastStreamID   string   `json:"last_stream_id,omitempty"`
	LastMessageID  string   `json:"last_message_id,omitempty"`
	LastEventType  string   `json:"last_event_type,omitempty"`
	LastAction     string   `json:"last_action,omitempty"`
	LastAccountID  string   `json:"last_account_id,omitempty"`
	LastGatewayOID string   `json:"last_gateway_order_id,omitempty"`
}

func SyncLedger(ctx context.Context, cfg config.Config, writer LedgerWriter, opts LedgerSyncOptions) (LedgerSyncReport, error) {
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return LedgerSyncReport{}, errors.New("redis.url is required for ledger sync")
	}
	if writer == nil {
		return LedgerSyncReport{}, errors.New("ledger writer is required")
	}

	redisOptions, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return LedgerSyncReport{}, err
	}
	client := redis.NewClient(redisOptions)
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return LedgerSyncReport{}, err
	}

	prefixes := opts.Prefixes
	if len(prefixes) == 0 {
		prefixes = PrefixesFromConfig(cfg)
	}
	if len(prefixes) == 0 {
		return LedgerSyncReport{}, errors.New("no stream prefixes configured")
	}

	startID := strings.TrimSpace(opts.StartID)
	if startID == "" {
		startID = "0"
	}
	count := opts.Count
	if count <= 0 {
		count = 100
	}
	roles := outputRoles(opts.Roles)

	report := LedgerSyncReport{
		GeneratedAt: time.Now().UTC(),
		Protocol:    Protocol,
		RedisAddr:   maskRedisAddr(cfg.Redis.URL),
		Prefixes:    prefixes,
		StartID:     startID,
		Count:       count,
	}
	if opts.Block > 0 {
		report.Block = opts.Block.String()
	}

	for _, prefix := range prefixes {
		streams := NewStreams(prefix)
		for _, role := range roles {
			streamName := streamNameForRole(streams, role)
			if streamName == "" {
				continue
			}
			streamReport := readAndProcessStream(ctx, client, writer, streamName, role, startID, count, opts.Block)
			report.Streams = append(report.Streams, streamReport)
			report.Totals.add(streamReport.Totals)
		}
	}

	return report, nil
}

func ProcessLedgerEntry(ctx context.Context, writer LedgerWriter, stream, streamID string, values map[string]any) LedgerProcessResult {
	result := LedgerProcessResult{
		Seen:         1,
		LastStreamID: streamID,
	}

	envelope, err := DecodeEntry(stream, streamID, values)
	if err != nil {
		result.ParseErrors++
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, err.Error())
		if archiveErr := archiveParseError(ctx, writer, stream, streamID, values, err); archiveErr != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, archiveErr.Error())
		} else {
			result.Archived++
		}
		return result
	}

	result.LastMessageID = envelope.MessageID
	result.LastEventType = envelope.EventType
	result.LastAction = envelope.Action
	result.LastAccountID = envelope.Routing.AccountID
	result.LastGatewayOID = firstNonEmpty(envelope.GatewayOrderID, gatewayOrderIDFromPayload(envelope.Payload))

	if err := writer.ArchiveRawStreamMessage(ctx, rawMessageFromEnvelope(envelope)); err != nil {
		result.LedgerErrors++
		result.SkipReasons = append(result.SkipReasons, err.Error())
		return result
	}
	result.Archived++

	switch envelope.MessageType {
	case "reply":
		result.Replies++
		return result
	case "event":
		return processEventEnvelope(ctx, writer, envelope, result)
	default:
		if envelope.EventType == "order.event" || envelope.EventType == "fill.event" {
			return processEventEnvelope(ctx, writer, envelope, result)
		}
		result.Unsupported++
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, "unsupported message_type "+envelope.MessageType)
		return result
	}
}

func processEventEnvelope(ctx context.Context, writer LedgerWriter, envelope EntryEnvelope, result LedgerProcessResult) LedgerProcessResult {
	switch envelope.EventType {
	case "order.event":
		event, err := orderEventFromEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, event.AccountID)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Accounts++
		if err := writer.UpsertOrder(ctx, event.Order); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Orders++
		if err := writer.AppendOrderEvent(ctx, event, streamRef(envelope), sourceRef(envelope)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.OrderEvents++
		return result
	case "fill.event":
		fill, err := fillFromEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, fill.AccountID)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Accounts++
		if err := writer.InsertFill(ctx, fill, streamRef(envelope), sourceRef(envelope)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Fills++
		return result
	default:
		result.Unsupported++
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, "unsupported event_type "+envelope.EventType)
		return result
	}
}

func readAndProcessStream(ctx context.Context, client *redis.Client, writer LedgerWriter, streamName, role, startID string, count int64, block time.Duration) LedgerStreamReport {
	report := LedgerStreamReport{
		Name:    streamName,
		Role:    role,
		StartID: startID,
	}

	result, err := client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamName, startID},
		Count:   count,
		Block:   block,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return report
		}
		report.Errors = append(report.Errors, LedgerEntryError{Error: err.Error()})
		report.Totals.LedgerErrors++
		return report
	}

	for _, stream := range result {
		for _, message := range stream.Messages {
			entryResult := ProcessLedgerEntry(ctx, writer, stream.Stream, message.ID, message.Values)
			report.Count++
			report.Totals.add(entryResult)
			if len(entryResult.SkipReasons) > 0 {
				report.Errors = append(report.Errors, LedgerEntryError{
					StreamID: message.ID,
					Error:    strings.Join(entryResult.SkipReasons, "; "),
				})
			}
		}
	}
	return report
}

func archiveParseError(ctx context.Context, writer LedgerWriter, stream, streamID string, values map[string]any, parseErr error) error {
	body, _ := bodyString(values["body"])
	return writer.ArchiveRawStreamMessage(ctx, ledger.RawStreamMessage{
		StreamRef:  ledger.StreamRef{Key: stream, ID: streamID},
		Direction:  directionFromRole(roleFromStream(stream)),
		Role:       roleFromStream(stream),
		BodyText:   body,
		ParseError: parseErr.Error(),
		ReceivedAt: time.Now().UTC(),
	})
}

func rawMessageFromEnvelope(envelope EntryEnvelope) ledger.RawStreamMessage {
	return ledger.RawStreamMessage{
		StreamRef: ledger.StreamRef{Key: envelope.Stream, ID: envelope.StreamID},
		SourceRef: ledger.SourceRef{
			OriginMessageID: firstNonEmpty(envelope.OriginMessageID, envelope.MessageID),
			RequestID:       envelope.RequestID,
			CorrelationID:   firstNonEmpty(envelope.CorrelationID, envelope.RequestCorrelationID),
			IdempotencyKey:  envelope.IdempotencyKey,
		},
		Direction:      directionFromRole(roleFromStream(envelope.Stream)),
		Role:           roleFromStream(envelope.Stream),
		MessageType:    envelope.MessageType,
		Action:         envelope.Action,
		EventType:      envelope.EventType,
		Status:         envelope.Status,
		Code:           envelope.Code,
		AccountID:      envelope.Routing.AccountID,
		GatewayOrderID: firstNonEmpty(envelope.GatewayOrderID, gatewayOrderIDFromPayload(envelope.Payload)),
		Body:           json.RawMessage(envelope.BodyText),
		BodyText:       envelope.BodyText,
		ReceivedAt:     time.Now().UTC(),
	}
}

func orderEventFromEnvelope(envelope EntryEnvelope) (trading.OrderEvent, error) {
	var payload orderPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return trading.OrderEvent{}, fmt.Errorf("decode order.event payload: %w", err)
	}
	mergeEnvelopeFields(&payload, envelope)
	if err := payload.validateOrderLedgerFields(); err != nil {
		return trading.OrderEvent{}, err
	}

	order := payload.toOrder(envelope)
	event := trading.OrderEvent{
		EventID:        envelope.MessageID,
		EventType:      trading.EventTypeOrder,
		AccountID:      order.AccountID,
		GatewayOrderID: order.GatewayOrderID,
		Status:         order.Status,
		GatewayStatus:  order.GatewayStatus,
		IsTerminal:     order.IsTerminal,
		Order:          order,
		ProducedAt:     envelope.ProducedAt,
		AdapterContext: envelope.AdapterContext,
	}
	return event, nil
}

func fillFromEnvelope(envelope EntryEnvelope) (trading.Fill, error) {
	var payload fillPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return trading.Fill{}, fmt.Errorf("decode fill.event payload: %w", err)
	}
	if payload.AccountID == "" {
		payload.AccountID = envelope.Routing.AccountID
	}
	if payload.GatewayOrderID == "" {
		payload.GatewayOrderID = envelope.GatewayOrderID
	}
	if payload.FillID == "" {
		payload.FillID = stringFromMap(envelope.AdapterContext, "match_stream_id")
	}
	if payload.Fee == 0 {
		payload.Fee = floatFromMap(envelope.AdapterContext, "fee")
	}
	if err := payload.validate(); err != nil {
		return trading.Fill{}, err
	}
	return trading.Fill{
		FillID:         payload.FillID,
		AccountID:      payload.AccountID,
		GatewayOrderID: payload.GatewayOrderID,
		OrderID:        payload.OrderID,
		OrderStreamID:  payload.OrderStreamID,
		Symbol:         payload.Symbol,
		Name:           payload.Name,
		Exchange:       trading.Exchange(payload.Exchange),
		TradeSide:      trading.TradeSide(payload.TradeSide),
		Price:          payload.Price,
		Qty:            payload.Qty,
		Fee:            payload.Fee,
		TradeDate:      payload.TradeDate,
		MatchTimestamp: payload.MatchTimestamp,
		MatchedAt:      parseTime(payload.MatchedAt),
		ShareholderID:  payload.ShareholderID,
		AdapterContext: envelope.AdapterContext,
	}, nil
}

type orderPayload struct {
	AccountID         string  `json:"account_id"`
	ClientOrderID     string  `json:"client_order_id"`
	GatewayOrderID    string  `json:"gateway_order_id"`
	OrderID           int64   `json:"order_id"`
	OrderStreamID     string  `json:"order_stream_id"`
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name"`
	Exchange          string  `json:"exchange"`
	TradeSide         string  `json:"trade_side"`
	BusinessType      string  `json:"business_type"`
	OffsetType        string  `json:"offset_type"`
	LimitPrice        float64 `json:"limit_price"`
	Price             float64 `json:"price"`
	OrderQty          int64   `json:"order_qty"`
	Qty               int64   `json:"qty"`
	SubmittedQty      int64   `json:"submitted_qty"`
	CumFilledQty      int64   `json:"cum_filled_qty"`
	LeavesQty         int64   `json:"leaves_qty"`
	CancelledQty      int64   `json:"cancelled_qty"`
	InvalidQty        int64   `json:"invalid_qty"`
	AvgFillPrice      float64 `json:"avg_fill_price"`
	Fee               float64 `json:"fee"`
	Status            string  `json:"status"`
	GatewayStatus     string  `json:"gateway_status"`
	AdapterStatusCode int     `json:"adapter_status_code"`
	AdapterStatusName string  `json:"adapter_status_name"`
	IsTerminal        bool    `json:"is_terminal"`
	RejectCode        string  `json:"reject_code"`
	RejectMessage     string  `json:"reject_message"`
	ShareholderID     string  `json:"shareholder_id"`
	CreatedAt         string  `json:"created_at"`
	AcceptedAt        string  `json:"accepted_at"`
	InsertedAt        string  `json:"inserted_at"`
	LastUpdatedAt     string  `json:"last_updated_at"`
	TerminalAt        string  `json:"terminal_at"`
}

type fillPayload struct {
	FillID         string  `json:"fill_id"`
	AccountID      string  `json:"account_id"`
	GatewayOrderID string  `json:"gateway_order_id"`
	OrderID        int64   `json:"order_id"`
	OrderStreamID  string  `json:"order_stream_id"`
	Symbol         string  `json:"symbol"`
	Name           string  `json:"name"`
	Exchange       string  `json:"exchange"`
	TradeSide      string  `json:"trade_side"`
	Price          float64 `json:"price"`
	Qty            int64   `json:"qty"`
	Fee            float64 `json:"fee"`
	TradeDate      string  `json:"trade_date"`
	MatchTimestamp int64   `json:"match_timestamp"`
	MatchedAt      string  `json:"matched_at"`
	ShareholderID  string  `json:"shareholder_id"`
}

func mergeEnvelopeFields(payload *orderPayload, envelope EntryEnvelope) {
	if payload.AccountID == "" {
		payload.AccountID = envelope.Routing.AccountID
	}
	if payload.GatewayOrderID == "" {
		payload.GatewayOrderID = envelope.GatewayOrderID
	}
	if payload.OrderID == 0 {
		payload.OrderID = int64FromMap(envelope.AdapterContext, "order_id")
	}
	if payload.OrderStreamID == "" {
		payload.OrderStreamID = stringFromMap(envelope.AdapterContext, "order_stream_id")
	}
	if payload.Fee == 0 {
		payload.Fee = floatFromMap(envelope.AdapterContext, "fee")
	}
	if payload.AdapterStatusCode == 0 {
		payload.AdapterStatusCode = int(int64FromMap(envelope.AdapterContext, "order_status_code"))
	}
	if payload.AdapterStatusName == "" {
		payload.AdapterStatusName = stringFromMap(envelope.AdapterContext, "order_status_name")
	}
}

func (payload orderPayload) validateOrderLedgerFields() error {
	missing := make([]string, 0)
	if strings.TrimSpace(payload.AccountID) == "" {
		missing = append(missing, "account_id")
	}
	if strings.TrimSpace(payload.GatewayOrderID) == "" {
		missing = append(missing, "gateway_order_id")
	}
	if strings.TrimSpace(payload.Symbol) == "" {
		missing = append(missing, "symbol")
	}
	if strings.TrimSpace(payload.Exchange) == "" {
		missing = append(missing, "exchange")
	}
	if strings.TrimSpace(payload.TradeSide) == "" {
		missing = append(missing, "trade_side")
	}
	if strings.TrimSpace(payload.BusinessType) == "" {
		missing = append(missing, "business_type")
	}
	if firstPositive(payload.LimitPrice, payload.Price) <= 0 {
		missing = append(missing, "limit_price")
	}
	if firstPositiveInt(payload.OrderQty, payload.Qty) <= 0 {
		missing = append(missing, "order_qty")
	}
	if len(missing) > 0 {
		return fmt.Errorf("order.event payload incomplete for ledger write: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func (payload orderPayload) toOrder(envelope EntryEnvelope) trading.Order {
	limitPrice := firstPositive(payload.LimitPrice, payload.Price)
	orderQty := firstPositiveInt(payload.OrderQty, payload.Qty)
	gatewayStatus := trading.GatewayStatus(payload.GatewayStatus)
	status := orderStatusFromPayload(payload.Status, gatewayStatus, payload.CumFilledQty, payload.LeavesQty)
	isTerminal := payload.IsTerminal || status.Terminal() || gatewayStatus.Terminal()
	return trading.Order{
		AccountID:         payload.AccountID,
		ClientOrderID:     payload.ClientOrderID,
		GatewayOrderID:    payload.GatewayOrderID,
		OrderID:           payload.OrderID,
		OrderStreamID:     payload.OrderStreamID,
		Symbol:            payload.Symbol,
		Name:              payload.Name,
		Exchange:          trading.Exchange(payload.Exchange),
		TradeSide:         trading.TradeSide(payload.TradeSide),
		BusinessType:      trading.BusinessType(payload.BusinessType),
		OffsetType:        trading.OffsetType(payload.OffsetType),
		LimitPrice:        limitPrice,
		OrderQty:          orderQty,
		SubmittedQty:      payload.SubmittedQty,
		CumFilledQty:      payload.CumFilledQty,
		LeavesQty:         payload.LeavesQty,
		CancelledQty:      payload.CancelledQty,
		InvalidQty:        payload.InvalidQty,
		AvgFillPrice:      payload.AvgFillPrice,
		Fee:               payload.Fee,
		Status:            status,
		GatewayStatus:     gatewayStatus,
		AdapterStatusCode: payload.AdapterStatusCode,
		AdapterStatusName: payload.AdapterStatusName,
		IsTerminal:        isTerminal,
		RejectCode:        trading.ErrorCode(payload.RejectCode),
		RejectMessage:     payload.RejectMessage,
		OriginMessageID:   envelope.OriginMessageID,
		RequestID:         envelope.RequestID,
		IdempotencyKey:    envelope.IdempotencyKey,
		ShareholderID:     payload.ShareholderID,
		CreatedAt:         parseTime(payload.CreatedAt),
		AcceptedAt:        parseTime(payload.AcceptedAt),
		InsertedAt:        parseTime(payload.InsertedAt),
		LastUpdatedAt:     parseTime(payload.LastUpdatedAt),
		TerminalAt:        parseTime(payload.TerminalAt),
		AdapterContext:    envelope.AdapterContext,
	}
}

func (payload fillPayload) validate() error {
	missing := make([]string, 0)
	if strings.TrimSpace(payload.AccountID) == "" {
		missing = append(missing, "account_id")
	}
	if strings.TrimSpace(payload.GatewayOrderID) == "" {
		missing = append(missing, "gateway_order_id")
	}
	if strings.TrimSpace(payload.Symbol) == "" {
		missing = append(missing, "symbol")
	}
	if strings.TrimSpace(payload.Exchange) == "" {
		missing = append(missing, "exchange")
	}
	if strings.TrimSpace(payload.TradeSide) == "" {
		missing = append(missing, "trade_side")
	}
	if payload.Price <= 0 {
		missing = append(missing, "price")
	}
	if payload.Qty <= 0 {
		missing = append(missing, "qty")
	}
	if len(missing) > 0 {
		return fmt.Errorf("fill.event payload incomplete for ledger write: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func accountFromEnvelope(envelope EntryEnvelope, accountID string) trading.Account {
	brokerID := firstNonEmpty(envelope.Routing.BrokerID, "unknown")
	return trading.Account{
		AccountID:      accountID,
		BrokerID:       brokerID,
		Status:         trading.AccountStatusEnabled,
		Enabled:        true,
		TradingEnabled: false,
		Simulated:      false,
		Tags: map[string]string{
			"source":     "redis-stream",
			"env":        envelope.Routing.Env,
			"gateway_id": envelope.Routing.GatewayID,
		},
		UpdatedAt: time.Now().UTC(),
	}
}

func orderStatusFromPayload(status string, gatewayStatus trading.GatewayStatus, cumFilledQty, leavesQty int64) trading.OrderStatus {
	if status != "" {
		return trading.OrderStatus(status)
	}
	switch gatewayStatus {
	case trading.GatewayStatusAccepted:
		return trading.OrderStatusAccepted
	case trading.GatewayStatusWorking:
		if cumFilledQty > 0 && leavesQty > 0 {
			return trading.OrderStatusPartiallyFilled
		}
		return trading.OrderStatusWorking
	case trading.GatewayStatusFilled:
		return trading.OrderStatusFilled
	case trading.GatewayStatusCancelled:
		return trading.OrderStatusCancelled
	case trading.GatewayStatusRejected:
		return trading.OrderStatusRejected
	default:
		return trading.OrderStatusCreated
	}
}

func streamRef(envelope EntryEnvelope) ledger.StreamRef {
	return ledger.StreamRef{Key: envelope.Stream, ID: envelope.StreamID}
}

func sourceRef(envelope EntryEnvelope) ledger.SourceRef {
	return ledger.SourceRef{
		OriginMessageID: envelope.OriginMessageID,
		RequestID:       envelope.RequestID,
		CorrelationID:   firstNonEmpty(envelope.CorrelationID, envelope.RequestCorrelationID),
		IdempotencyKey:  envelope.IdempotencyKey,
	}
}

func outputRoles(roles []string) []string {
	if len(roles) == 0 {
		return []string{SuffixReply, SuffixEvent}
	}
	normalized := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role != "" {
			normalized = append(normalized, role)
		}
	}
	return normalized
}

func streamNameForRole(streams Streams, role string) string {
	switch role {
	case SuffixReply:
		return streams.Reply
	case SuffixEvent:
		return streams.Event
	case SuffixHB:
		return streams.HB
	case SuffixDLQ:
		return streams.DLQ
	default:
		return ""
	}
}

func roleFromStream(stream string) string {
	if idx := strings.LastIndex(stream, ":"); idx >= 0 && idx+1 < len(stream) {
		return stream[idx+1:]
	}
	return ""
}

func directionFromRole(role string) string {
	switch role {
	case SuffixCmdTrade, SuffixCmdQuery:
		return "in"
	default:
		return "out"
	}
}

func gatewayOrderIDFromPayload(raw json.RawMessage) string {
	var payload struct {
		GatewayOrderID string `json:"gateway_order_id"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.GatewayOrderID
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveInt(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func floatFromMap(values map[string]any, key string) float64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func int64FromMap(values map[string]any, key string) int64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	case json.Number:
		i, _ := value.Int64()
		return i
	default:
		return 0
	}
}

func (result *LedgerProcessResult) add(other LedgerProcessResult) {
	result.Seen += other.Seen
	result.Archived += other.Archived
	result.Accounts += other.Accounts
	result.Orders += other.Orders
	result.OrderEvents += other.OrderEvents
	result.Fills += other.Fills
	result.Replies += other.Replies
	result.Skipped += other.Skipped
	result.ParseErrors += other.ParseErrors
	result.LedgerErrors += other.LedgerErrors
	result.Unsupported += other.Unsupported
	result.SkipReasons = append(result.SkipReasons, other.SkipReasons...)
	if other.LastStreamID != "" {
		result.LastStreamID = other.LastStreamID
	}
	if other.LastMessageID != "" {
		result.LastMessageID = other.LastMessageID
	}
	if other.LastEventType != "" {
		result.LastEventType = other.LastEventType
	}
	if other.LastAction != "" {
		result.LastAction = other.LastAction
	}
	if other.LastAccountID != "" {
		result.LastAccountID = other.LastAccountID
	}
	if other.LastGatewayOID != "" {
		result.LastGatewayOID = other.LastGatewayOID
	}
}

var _ LedgerWriter = (*ledger.Repository)(nil)
