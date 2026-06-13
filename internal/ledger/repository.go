package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ti-relay-trader/internal/trading"
)

var ErrInvalidLedgerInput = errors.New("invalid ledger input")

type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type Repository struct {
	exec Executor
	now  func() time.Time
}

type StreamRef struct {
	Key string
	ID  string
}

type SourceRef struct {
	OriginMessageID string
	RequestID       string
	CorrelationID   string
	IdempotencyKey  string
}

type RawStreamMessage struct {
	StreamRef
	SourceRef
	Direction      string
	Role           string
	MessageType    string
	Action         string
	EventType      string
	Status         string
	Code           string
	AccountID      string
	GatewayOrderID string
	Body           any
	BodyText       string
	ParseError     string
	ReceivedAt     time.Time
}

func NewRepository(exec Executor) *Repository {
	return &Repository{
		exec: exec,
		now:  time.Now,
	}
}

func (repo *Repository) UpsertAccount(ctx context.Context, account trading.Account) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(account.AccountID) == "" {
		return fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(account.BrokerID) == "" {
		return fmt.Errorf("%w: broker_id is required", ErrInvalidLedgerInput)
	}
	if account.Status == "" {
		account.Status = trading.AccountStatusDisabled
	}
	tags, err := marshalJSONObject(account.Tags)
	if err != nil {
		return err
	}

	updatedAt := account.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = repo.now()
	}

	_, err = repo.exec.ExecContext(ctx, upsertAccountSQL,
		account.AccountID,
		account.BrokerID,
		account.Status,
		account.Enabled,
		account.TradingEnabled,
		account.Simulated,
		tags,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert account %s: %w", account.AccountID, err)
	}
	return nil
}

func (repo *Repository) UpsertOrder(ctx context.Context, order trading.Order) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeOrder(order)
	if err != nil {
		return err
	}
	rawPayload, err := marshalJSONObject(normalized)
	if err != nil {
		return err
	}
	adapterContext, err := marshalJSONObject(normalized.AdapterContext)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, upsertOrderSQL,
		normalized.AccountID,
		nullString(normalized.ClientOrderID),
		normalized.GatewayOrderID,
		nullInt64(normalized.OrderID),
		nullString(normalized.OrderStreamID),
		normalized.Symbol,
		normalized.Name,
		normalized.Exchange,
		normalized.TradeSide,
		normalized.BusinessType,
		nullString(string(normalized.OffsetType)),
		normalized.LimitPrice,
		normalized.OrderQty,
		normalized.SubmittedQty,
		normalized.CumFilledQty,
		normalized.LeavesQty,
		normalized.CancelledQty,
		normalized.InvalidQty,
		nullFloat64(normalized.AvgFillPrice),
		normalized.Fee,
		normalized.Status,
		normalized.GatewayStatus,
		nullInt(normalized.AdapterStatusCode),
		nullString(normalized.AdapterStatusName),
		normalized.IsTerminal,
		nullString(string(normalized.RejectCode)),
		nullString(normalized.RejectMessage),
		nullString(normalized.OriginMessageID),
		nullString(normalized.RequestID),
		nullString(normalized.IdempotencyKey),
		nullString(normalized.ShareholderID),
		nullTime(normalized.CreatedAt),
		nullTime(normalized.AcceptedAt),
		nullTime(normalized.InsertedAt),
		nullTime(normalized.LastUpdatedAt),
		nullTime(normalized.TerminalAt),
		rawPayload,
		adapterContext,
	)
	if err != nil {
		return fmt.Errorf("upsert order %s/%s: %w", normalized.AccountID, normalized.GatewayOrderID, err)
	}
	return nil
}

func (repo *Repository) AppendOrderEvent(ctx context.Context, event trading.OrderEvent, stream StreamRef, source SourceRef) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeOrderEvent(event)
	if err != nil {
		return err
	}
	payload, err := marshalJSONObject(normalized)
	if err != nil {
		return err
	}
	adapterContext, err := marshalJSONObject(normalized.AdapterContext)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, appendOrderEventSQL,
		normalized.AccountID,
		normalized.GatewayOrderID,
		nullString(normalized.EventID),
		normalized.EventType,
		normalized.Status,
		normalized.GatewayStatus,
		normalized.IsTerminal,
		nullString(stream.Key),
		nullString(stream.ID),
		nullString(firstNonEmpty(source.OriginMessageID, normalized.Order.OriginMessageID)),
		nullString(firstNonEmpty(source.RequestID, normalized.Order.RequestID)),
		nullString(source.CorrelationID),
		nullTime(normalized.ProducedAt),
		payload,
		adapterContext,
	)
	if err != nil {
		return fmt.Errorf("append order event %s/%s: %w", normalized.AccountID, normalized.GatewayOrderID, err)
	}
	return nil
}

func (repo *Repository) InsertFill(ctx context.Context, fill trading.Fill, stream StreamRef, source SourceRef) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeFill(fill)
	if err != nil {
		return err
	}
	rawPayload, err := marshalJSONObject(normalized)
	if err != nil {
		return err
	}
	adapterContext, err := marshalJSONObject(normalized.AdapterContext)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, insertFillSQL,
		normalized.AccountID,
		nullString(normalized.FillID),
		normalized.GatewayOrderID,
		nullInt64(normalized.OrderID),
		nullString(normalized.OrderStreamID),
		normalized.Symbol,
		normalized.Name,
		normalized.Exchange,
		normalized.TradeSide,
		normalized.Price,
		normalized.Qty,
		normalized.Fee,
		nullDate(normalized.TradeDate),
		nullInt64(normalized.MatchTimestamp),
		nullTime(normalized.MatchedAt),
		nullString(normalized.ShareholderID),
		nullString(stream.Key),
		nullString(stream.ID),
		nullString(source.OriginMessageID),
		nullString(source.RequestID),
		rawPayload,
		adapterContext,
	)
	if err != nil {
		return fmt.Errorf("insert fill %s/%s/%s: %w", normalized.AccountID, normalized.GatewayOrderID, normalized.FillID, err)
	}
	return nil
}

func (repo *Repository) ArchiveRawStreamMessage(ctx context.Context, message RawStreamMessage) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(message.StreamRef.Key) == "" {
		return fmt.Errorf("%w: stream_key is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(message.StreamRef.ID) == "" {
		return fmt.Errorf("%w: stream_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(message.Direction) == "" {
		return fmt.Errorf("%w: direction is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(message.Role) == "" {
		return fmt.Errorf("%w: stream_role is required", ErrInvalidLedgerInput)
	}

	body, err := marshalOptionalJSON(message.Body)
	if err != nil {
		return err
	}
	receivedAt := message.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = repo.now()
	}

	_, err = repo.exec.ExecContext(ctx, archiveRawStreamMessageSQL,
		message.StreamRef.Key,
		message.StreamRef.ID,
		message.Direction,
		message.Role,
		nullString(message.MessageType),
		nullString(message.Action),
		nullString(message.EventType),
		nullString(message.Status),
		nullString(message.Code),
		nullString(message.AccountID),
		nullString(message.GatewayOrderID),
		nullString(message.OriginMessageID),
		nullString(message.RequestID),
		nullString(message.CorrelationID),
		nullString(message.IdempotencyKey),
		body,
		nullString(message.BodyText),
		nullString(message.ParseError),
		receivedAt,
	)
	if err != nil {
		return fmt.Errorf("archive raw stream message %s/%s: %w", message.StreamRef.Key, message.StreamRef.ID, err)
	}
	return nil
}

func normalizeOrder(order trading.Order) (trading.Order, error) {
	if strings.TrimSpace(order.AccountID) == "" {
		return order, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(order.GatewayOrderID) == "" {
		return order, fmt.Errorf("%w: gateway_order_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(order.Symbol) == "" {
		return order, fmt.Errorf("%w: symbol is required", ErrInvalidLedgerInput)
	}
	if !order.Exchange.Valid() {
		return order, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if !order.TradeSide.Valid() {
		return order, fmt.Errorf("%w: trade_side must be B, S, P, or R", ErrInvalidLedgerInput)
	}
	if !order.BusinessType.Valid() {
		return order, fmt.Errorf("%w: business_type must be S or E", ErrInvalidLedgerInput)
	}
	if !order.OffsetType.Valid() {
		return order, fmt.Errorf("%w: offset_type must be O, C, or empty", ErrInvalidLedgerInput)
	}
	if order.LimitPrice <= 0 {
		return order, fmt.Errorf("%w: limit_price must be positive", ErrInvalidLedgerInput)
	}
	if order.OrderQty <= 0 {
		return order, fmt.Errorf("%w: order_qty must be positive", ErrInvalidLedgerInput)
	}
	if order.Status == "" {
		order.Status = trading.OrderStatusCreated
	}
	if order.GatewayStatus == "" {
		order.GatewayStatus = trading.GatewayStatusAccepted
	}
	if order.Status.Terminal() {
		order.IsTerminal = true
	}
	if order.LeavesQty == 0 && order.CumFilledQty == 0 && order.CancelledQty == 0 && order.InvalidQty == 0 && !order.IsTerminal {
		order.LeavesQty = order.OrderQty
	}
	return order, nil
}

func normalizeOrderEvent(event trading.OrderEvent) (trading.OrderEvent, error) {
	if event.EventType == "" {
		event.EventType = trading.EventTypeOrder
	}
	if event.EventType != trading.EventTypeOrder {
		return event, fmt.Errorf("%w: event_type must be order.event", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(event.AccountID) == "" {
		event.AccountID = event.Order.AccountID
	}
	if strings.TrimSpace(event.GatewayOrderID) == "" {
		event.GatewayOrderID = event.Order.GatewayOrderID
	}
	if strings.TrimSpace(event.AccountID) == "" {
		return event, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(event.GatewayOrderID) == "" {
		return event, fmt.Errorf("%w: gateway_order_id is required", ErrInvalidLedgerInput)
	}
	if event.Status == "" {
		event.Status = event.Order.Status
	}
	if event.Status == "" {
		event.Status = trading.OrderStatusCreated
	}
	if event.GatewayStatus == "" {
		event.GatewayStatus = event.Order.GatewayStatus
	}
	if event.GatewayStatus == "" {
		event.GatewayStatus = trading.GatewayStatusAccepted
	}
	if event.Status.Terminal() {
		event.IsTerminal = true
	}
	return event, nil
}

func normalizeFill(fill trading.Fill) (trading.Fill, error) {
	if strings.TrimSpace(fill.AccountID) == "" {
		return fill, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(fill.GatewayOrderID) == "" {
		return fill, fmt.Errorf("%w: gateway_order_id is required", ErrInvalidLedgerInput)
	}
	if strings.TrimSpace(fill.Symbol) == "" {
		return fill, fmt.Errorf("%w: symbol is required", ErrInvalidLedgerInput)
	}
	if !fill.Exchange.Valid() {
		return fill, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if !fill.TradeSide.Valid() {
		return fill, fmt.Errorf("%w: trade_side must be B, S, P, or R", ErrInvalidLedgerInput)
	}
	if fill.Price <= 0 {
		return fill, fmt.Errorf("%w: price must be positive", ErrInvalidLedgerInput)
	}
	if fill.Qty <= 0 {
		return fill, fmt.Errorf("%w: qty must be positive", ErrInvalidLedgerInput)
	}
	return fill, nil
}

func marshalJSONObject(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal json: %w", ErrInvalidLedgerInput, err)
	}
	return body, nil
}

func marshalOptionalJSON(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case json.RawMessage:
		if !json.Valid(typed) {
			return nil, fmt.Errorf("%w: body json is invalid", ErrInvalidLedgerInput)
		}
		return []byte(typed), nil
	case []byte:
		if !json.Valid(typed) {
			return nil, fmt.Errorf("%w: body json is invalid", ErrInvalidLedgerInput)
		}
		return typed, nil
	default:
		return marshalJSONObject(value)
	}
}

func nullString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	return sql.NullString{String: value, Valid: value != ""}
}

func nullInt(value int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(value), Valid: value != 0}
}

func nullInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value != 0}
}

func nullFloat64(value float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: value, Valid: value != 0}
}

func nullTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: !value.IsZero()}
}

func nullDate(value string) sql.NullString {
	value = strings.TrimSpace(value)
	return sql.NullString{String: value, Valid: value != ""}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
