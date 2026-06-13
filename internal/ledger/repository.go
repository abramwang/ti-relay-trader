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
var ErrOrderNotFound = errors.New("order not found")
var ErrAssetNotFound = errors.New("asset snapshot not found")

type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
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

func (repo *Repository) UpdateOrderStatus(ctx context.Context, event trading.OrderEvent) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeOrderEvent(event)
	if err != nil {
		return err
	}
	adapterContext, err := marshalJSONObject(normalized.AdapterContext)
	if err != nil {
		return err
	}

	result, err := repo.exec.ExecContext(ctx, updateOrderStatusSQL,
		normalized.AccountID,
		normalized.GatewayOrderID,
		nullInt64(normalized.Order.OrderID),
		nullString(normalized.Order.OrderStreamID),
		normalized.Order.SubmittedQty,
		normalized.Order.CumFilledQty,
		normalized.Order.LeavesQty,
		normalized.Order.CancelledQty,
		normalized.Order.InvalidQty,
		nullFloat64(normalized.Order.AvgFillPrice),
		normalized.Order.Fee,
		normalized.Status,
		normalized.GatewayStatus,
		normalized.IsTerminal,
		nullString(string(normalized.Order.RejectCode)),
		nullString(normalized.Order.RejectMessage),
		nullTime(normalized.ProducedAt),
		nullTime(normalized.Order.TerminalAt),
		adapterContext,
	)
	if err != nil {
		return fmt.Errorf("update order status %s/%s: %w", normalized.AccountID, normalized.GatewayOrderID, err)
	}
	if result != nil {
		rows, err := result.RowsAffected()
		if err == nil && rows == 0 {
			return fmt.Errorf("%w: %s/%s", ErrOrderNotFound, normalized.AccountID, normalized.GatewayOrderID)
		}
	}
	return nil
}

func (repo *Repository) GetOrder(ctx context.Context, accountID string, gatewayOrderID string) (trading.Order, error) {
	if repo == nil || repo.exec == nil {
		return trading.Order{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	accountID = strings.TrimSpace(accountID)
	gatewayOrderID = strings.TrimSpace(gatewayOrderID)
	if accountID == "" {
		return trading.Order{}, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if gatewayOrderID == "" {
		return trading.Order{}, fmt.Errorf("%w: gateway_order_id is required", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return trading.Order{}, err
	}
	rows, err := queryer.QueryContext(ctx, getOrderSQL, accountID, gatewayOrderID)
	if err != nil {
		return trading.Order{}, fmt.Errorf("get order %s/%s: %w", accountID, gatewayOrderID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return trading.Order{}, fmt.Errorf("get order %s/%s: %w", accountID, gatewayOrderID, err)
		}
		return trading.Order{}, fmt.Errorf("%w: %s/%s", ErrOrderNotFound, accountID, gatewayOrderID)
	}
	order, err := scanOrder(rows)
	if err != nil {
		return trading.Order{}, fmt.Errorf("scan order %s/%s: %w", accountID, gatewayOrderID, err)
	}
	return order, nil
}

func (repo *Repository) ListOrders(ctx context.Context, query trading.OrderQuery) ([]trading.Order, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeOrderQuery(query)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	sqlText, args := buildListOrdersSQL(normalized)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	orders := make([]trading.Order, 0, normalized.Limit)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list orders rows: %w", err)
	}
	return orders, nil
}

func (repo *Repository) ListFills(ctx context.Context, query trading.FillQuery) ([]trading.Fill, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeFillQuery(query)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	sqlText, args := buildListFillsSQL(normalized)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list fills: %w", err)
	}
	defer rows.Close()

	fills := make([]trading.Fill, 0, normalized.Limit)
	for rows.Next() {
		fill, err := scanFill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fill: %w", err)
		}
		fills = append(fills, fill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list fills rows: %w", err)
	}
	return fills, nil
}

func (repo *Repository) GetLatestAsset(ctx context.Context, accountID string) (trading.Asset, error) {
	if repo == nil || repo.exec == nil {
		return trading.Asset{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return trading.Asset{}, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return trading.Asset{}, err
	}
	rows, err := queryer.QueryContext(ctx, latestAssetSQL, accountID)
	if err != nil {
		return trading.Asset{}, fmt.Errorf("get latest asset %s: %w", accountID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return trading.Asset{}, fmt.Errorf("get latest asset %s: %w", accountID, err)
		}
		return trading.Asset{}, fmt.Errorf("%w: %s", ErrAssetNotFound, accountID)
	}
	asset, err := scanAsset(rows)
	if err != nil {
		return trading.Asset{}, fmt.Errorf("scan asset %s: %w", accountID, err)
	}
	return asset, nil
}

func (repo *Repository) UpsertAssetSnapshot(ctx context.Context, asset trading.Asset, snapshotType string, source string, rawPayload any, capturedAt time.Time) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeAsset(asset)
	if err != nil {
		return err
	}
	snapshotType = strings.TrimSpace(snapshotType)
	if snapshotType == "" {
		snapshotType = "intraday"
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "query"
	}
	if capturedAt.IsZero() {
		capturedAt = repo.now()
	}
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = capturedAt
	}
	body, err := marshalJSONObject(rawPayload)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, upsertAssetSnapshotSQL,
		capturedAt.Format("2006-01-02"),
		normalized.AccountID,
		snapshotType,
		normalized.CashAvailable,
		normalized.CashTotal,
		normalized.NetAsset,
		normalized.MarketValue,
		normalized.StockValue,
		normalized.FundValue,
		normalized.Commission,
		normalized.DayProfit,
		normalized.PositionProfit,
		normalized.CloseProfit,
		normalized.Credit,
		source,
		body,
		capturedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert asset snapshot %s: %w", normalized.AccountID, err)
	}
	return nil
}

func (repo *Repository) ListPositions(ctx context.Context, query trading.PositionQuery) ([]trading.Position, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizePositionQuery(query)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	sqlText, args := buildListPositionsSQL(normalized)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	defer rows.Close()

	positions := make([]trading.Position, 0, normalized.Limit)
	for rows.Next() {
		position, err := scanPosition(rows)
		if err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list positions rows: %w", err)
	}
	return positions, nil
}

func (repo *Repository) UpsertPosition(ctx context.Context, position trading.Position, source string, rawPayload any, updatedAt time.Time) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizePosition(position)
	if err != nil {
		return err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "query"
	}
	if updatedAt.IsZero() {
		updatedAt = repo.now()
	}
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = updatedAt
	}
	body, err := marshalJSONObject(rawPayload)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, upsertPositionSQL,
		normalized.AccountID,
		normalized.Symbol,
		normalized.Name,
		normalized.Exchange,
		normalized.Quantity,
		normalized.SellableQty,
		normalized.InitialQty,
		normalized.TodayQty,
		normalized.AvgCost,
		nullFloat64(normalized.LastPrice),
		normalized.MarketValue,
		normalized.UnrealizedPnL,
		normalized.SettledProfit,
		nullString(normalized.ShareholderID),
		source,
		body,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert position %s/%s.%s: %w", normalized.AccountID, normalized.Symbol, normalized.Exchange, err)
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

func (repo *Repository) queryer() (Queryer, error) {
	queryer, ok := repo.exec.(Queryer)
	if !ok {
		return nil, fmt.Errorf("%w: repository executor does not support queries", ErrInvalidLedgerInput)
	}
	return queryer, nil
}

func normalizeOrderQuery(query trading.OrderQuery) (trading.OrderQuery, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.GatewayOrderID = strings.TrimSpace(query.GatewayOrderID)
	query.ClientOrderID = strings.TrimSpace(query.ClientOrderID)
	query.Symbol = strings.TrimSpace(query.Symbol)
	query.Cursor = strings.TrimSpace(query.Cursor)
	if query.Exchange != "" && !query.Exchange.Valid() {
		return query, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if query.Status != "" && !query.Status.Valid() {
		return query, fmt.Errorf("%w: status is invalid", ErrInvalidLedgerInput)
	}
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 500 {
		query.Limit = 500
	}
	return query, nil
}

func normalizeFillQuery(query trading.FillQuery) (trading.FillQuery, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.GatewayOrderID = strings.TrimSpace(query.GatewayOrderID)
	query.Symbol = strings.TrimSpace(query.Symbol)
	query.Cursor = strings.TrimSpace(query.Cursor)
	if query.Exchange != "" && !query.Exchange.Valid() {
		return query, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 500 {
		query.Limit = 500
	}
	return query, nil
}

func normalizePositionQuery(query trading.PositionQuery) (trading.PositionQuery, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.Symbol = strings.TrimSpace(query.Symbol)
	query.Cursor = strings.TrimSpace(query.Cursor)
	if query.AccountID == "" {
		return query, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if query.Exchange != "" && !query.Exchange.Valid() {
		return query, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if query.Limit <= 0 {
		query.Limit = 500
	}
	if query.Limit > 2000 {
		query.Limit = 2000
	}
	return query, nil
}

func normalizeAsset(asset trading.Asset) (trading.Asset, error) {
	asset.AccountID = strings.TrimSpace(asset.AccountID)
	if asset.AccountID == "" {
		return asset, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	return asset, nil
}

func normalizePosition(position trading.Position) (trading.Position, error) {
	position.AccountID = strings.TrimSpace(position.AccountID)
	position.Symbol = strings.TrimSpace(position.Symbol)
	if position.AccountID == "" {
		return position, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if position.Symbol == "" {
		return position, fmt.Errorf("%w: symbol is required", ErrInvalidLedgerInput)
	}
	if !position.Exchange.Valid() {
		return position, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if position.SellableQty == 0 && position.Quantity > 0 {
		position.SellableQty = position.Quantity
	}
	return position, nil
}

func buildListOrdersSQL(query trading.OrderQuery) (string, []any) {
	var where []string
	var args []any
	appendFilter := func(column string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if query.AccountID != "" {
		appendFilter("account_id", query.AccountID)
	}
	if query.GatewayOrderID != "" {
		appendFilter("gateway_order_id", query.GatewayOrderID)
	}
	if query.ClientOrderID != "" {
		appendFilter("client_order_id", query.ClientOrderID)
	}
	if query.Symbol != "" {
		appendFilter("symbol", query.Symbol)
	}
	if query.Exchange != "" {
		appendFilter("exchange", query.Exchange)
	}
	if query.Status != "" {
		appendFilter("status", query.Status)
	}

	builder := strings.Builder{}
	builder.WriteString(orderSelectColumns)
	if len(where) > 0 {
		builder.WriteString("WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
		builder.WriteString("\n")
	}
	args = append(args, query.Limit)
	builder.WriteString(fmt.Sprintf("ORDER BY COALESCE(last_updated_at, created_at) DESC, gateway_order_id DESC LIMIT $%d", len(args)))
	return builder.String(), args
}

func buildListFillsSQL(query trading.FillQuery) (string, []any) {
	var where []string
	var args []any
	appendFilter := func(column string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if query.AccountID != "" {
		appendFilter("account_id", query.AccountID)
	}
	if query.GatewayOrderID != "" {
		appendFilter("gateway_order_id", query.GatewayOrderID)
	}
	if query.Symbol != "" {
		appendFilter("symbol", query.Symbol)
	}
	if query.Exchange != "" {
		appendFilter("exchange", query.Exchange)
	}

	builder := strings.Builder{}
	builder.WriteString(fillSelectColumns)
	if len(where) > 0 {
		builder.WriteString("WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
		builder.WriteString("\n")
	}
	args = append(args, query.Limit)
	builder.WriteString(fmt.Sprintf("ORDER BY COALESCE(matched_at, created_at) DESC, fill_pk DESC LIMIT $%d", len(args)))
	return builder.String(), args
}

func buildListPositionsSQL(query trading.PositionQuery) (string, []any) {
	var where []string
	var args []any
	appendFilter := func(column string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	appendFilter("account_id", query.AccountID)
	if query.Symbol != "" {
		appendFilter("symbol", query.Symbol)
	}
	if query.Exchange != "" {
		appendFilter("exchange", query.Exchange)
	}

	builder := strings.Builder{}
	builder.WriteString(positionSelectColumns)
	builder.WriteString("WHERE ")
	builder.WriteString(strings.Join(where, " AND "))
	builder.WriteString("\n")
	args = append(args, query.Limit)
	builder.WriteString(fmt.Sprintf("ORDER BY market_value DESC, symbol ASC, exchange ASC LIMIT $%d", len(args)))
	return builder.String(), args
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (trading.Order, error) {
	var order trading.Order
	var clientOrderID sql.NullString
	var orderID sql.NullInt64
	var orderStreamID sql.NullString
	var offsetType sql.NullString
	var avgFillPrice sql.NullFloat64
	var adapterStatusCode sql.NullInt64
	var adapterStatusName sql.NullString
	var rejectCode sql.NullString
	var rejectMessage sql.NullString
	var originMessageID sql.NullString
	var requestID sql.NullString
	var idempotencyKey sql.NullString
	var shareholderID sql.NullString
	var createdAt sql.NullTime
	var acceptedAt sql.NullTime
	var insertedAt sql.NullTime
	var lastUpdatedAt sql.NullTime
	var terminalAt sql.NullTime
	var adapterContext []byte

	err := row.Scan(
		&order.AccountID,
		&clientOrderID,
		&order.GatewayOrderID,
		&orderID,
		&orderStreamID,
		&order.Symbol,
		&order.Name,
		&order.Exchange,
		&order.TradeSide,
		&order.BusinessType,
		&offsetType,
		&order.LimitPrice,
		&order.OrderQty,
		&order.SubmittedQty,
		&order.CumFilledQty,
		&order.LeavesQty,
		&order.CancelledQty,
		&order.InvalidQty,
		&avgFillPrice,
		&order.Fee,
		&order.Status,
		&order.GatewayStatus,
		&adapterStatusCode,
		&adapterStatusName,
		&order.IsTerminal,
		&rejectCode,
		&rejectMessage,
		&originMessageID,
		&requestID,
		&idempotencyKey,
		&shareholderID,
		&createdAt,
		&acceptedAt,
		&insertedAt,
		&lastUpdatedAt,
		&terminalAt,
		&adapterContext,
	)
	if err != nil {
		return trading.Order{}, err
	}
	order.ClientOrderID = clientOrderID.String
	order.OrderID = orderID.Int64
	order.OrderStreamID = orderStreamID.String
	order.OffsetType = trading.OffsetType(offsetType.String)
	order.AvgFillPrice = avgFillPrice.Float64
	order.AdapterStatusCode = int(adapterStatusCode.Int64)
	order.AdapterStatusName = adapterStatusName.String
	order.RejectCode = trading.ErrorCode(rejectCode.String)
	order.RejectMessage = rejectMessage.String
	order.OriginMessageID = originMessageID.String
	order.RequestID = requestID.String
	order.IdempotencyKey = idempotencyKey.String
	order.ShareholderID = shareholderID.String
	order.CreatedAt = createdAt.Time
	order.AcceptedAt = acceptedAt.Time
	order.InsertedAt = insertedAt.Time
	order.LastUpdatedAt = lastUpdatedAt.Time
	order.TerminalAt = terminalAt.Time
	order.AdapterContext = map[string]any{}
	if len(adapterContext) > 0 {
		if err := json.Unmarshal(adapterContext, &order.AdapterContext); err != nil {
			return trading.Order{}, err
		}
	}
	return order, nil
}

func scanFill(row rowScanner) (trading.Fill, error) {
	var fill trading.Fill
	var fillID sql.NullString
	var orderID sql.NullInt64
	var orderStreamID sql.NullString
	var tradeDate sql.NullString
	var matchTimestamp sql.NullInt64
	var matchedAt sql.NullTime
	var shareholderID sql.NullString
	var adapterContext []byte

	err := row.Scan(
		&fillID,
		&fill.AccountID,
		&fill.GatewayOrderID,
		&orderID,
		&orderStreamID,
		&fill.Symbol,
		&fill.Name,
		&fill.Exchange,
		&fill.TradeSide,
		&fill.Price,
		&fill.Qty,
		&fill.Fee,
		&tradeDate,
		&matchTimestamp,
		&matchedAt,
		&shareholderID,
		&adapterContext,
	)
	if err != nil {
		return trading.Fill{}, err
	}
	fill.FillID = fillID.String
	fill.OrderID = orderID.Int64
	fill.OrderStreamID = orderStreamID.String
	fill.TradeDate = tradeDate.String
	fill.MatchTimestamp = matchTimestamp.Int64
	fill.MatchedAt = matchedAt.Time
	fill.ShareholderID = shareholderID.String
	fill.AdapterContext = map[string]any{}
	if len(adapterContext) > 0 {
		if err := json.Unmarshal(adapterContext, &fill.AdapterContext); err != nil {
			return trading.Fill{}, err
		}
	}
	return fill, nil
}

func scanAsset(row rowScanner) (trading.Asset, error) {
	var asset trading.Asset
	var updatedAt sql.NullTime
	err := row.Scan(
		&asset.AccountID,
		&asset.CashAvailable,
		&asset.CashTotal,
		&asset.NetAsset,
		&asset.MarketValue,
		&asset.StockValue,
		&asset.FundValue,
		&asset.Commission,
		&asset.DayProfit,
		&asset.PositionProfit,
		&asset.CloseProfit,
		&asset.Credit,
		&updatedAt,
	)
	if err != nil {
		return trading.Asset{}, err
	}
	asset.UpdatedAt = updatedAt.Time
	return asset, nil
}

func scanPosition(row rowScanner) (trading.Position, error) {
	var position trading.Position
	var lastPrice sql.NullFloat64
	var shareholderID sql.NullString
	var updatedAt sql.NullTime
	err := row.Scan(
		&position.AccountID,
		&position.Symbol,
		&position.Name,
		&position.Exchange,
		&position.Quantity,
		&position.SellableQty,
		&position.InitialQty,
		&position.TodayQty,
		&position.AvgCost,
		&lastPrice,
		&position.MarketValue,
		&position.UnrealizedPnL,
		&position.SettledProfit,
		&shareholderID,
		&updatedAt,
	)
	if err != nil {
		return trading.Position{}, err
	}
	position.LastPrice = lastPrice.Float64
	position.ShareholderID = shareholderID.String
	position.UpdatedAt = updatedAt.Time
	return position, nil
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
