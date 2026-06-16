package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

var ErrInvalidLedgerInput = errors.New("invalid ledger input")
var ErrOrderNotFound = errors.New("order not found")
var ErrAssetNotFound = errors.New("asset snapshot not found")
var ErrStreamCheckpointNotFound = errors.New("stream checkpoint not found")
var ErrJobRunNotFound = errors.New("job run not found")

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

type StreamCheckpoint struct {
	StreamKey       string         `json:"stream_key"`
	Role            string         `json:"stream_role"`
	LastStreamID    string         `json:"last_stream_id"`
	LastSeenAt      time.Time      `json:"last_seen_at,omitempty"`
	LastProcessedAt time.Time      `json:"last_processed_at,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	ProcessedCount  int64          `json:"processed_count"`
	ErrorCount      int64          `json:"error_count"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at,omitempty"`
}

type JobRun struct {
	RunID           string         `json:"run_id"`
	JobName         string         `json:"job_name"`
	TargetTradeDate string         `json:"target_trade_date"`
	Timezone        string         `json:"timezone"`
	Status          string         `json:"status"`
	Trigger         string         `json:"trigger,omitempty"`
	Skipped         bool           `json:"skipped,omitempty"`
	StartedAt       time.Time      `json:"started_at,omitempty"`
	FinishedAt      time.Time      `json:"finished_at,omitempty"`
	DurationMS      int64          `json:"duration_ms,omitempty"`
	Report          map[string]any `json:"report,omitempty"`
	ErrorSummary    string         `json:"error_summary,omitempty"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at,omitempty"`
}

type ReconciliationRun struct {
	RunID        string         `json:"run_id"`
	TradeDate    string         `json:"trade_date"`
	Status       string         `json:"status"`
	Source       string         `json:"source"`
	StartedAt    time.Time      `json:"started_at,omitempty"`
	CompletedAt  time.Time      `json:"completed_at,omitempty"`
	Summary      map[string]any `json:"summary,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
}

type ReconciliationInput struct {
	RunID      string         `json:"run_id"`
	Source     string         `json:"source"`
	InputType  string         `json:"input_type"`
	Payload    map[string]any `json:"payload,omitempty"`
	CapturedAt time.Time      `json:"captured_at,omitempty"`
}

type ReconciliationBreak struct {
	RunID           string         `json:"run_id"`
	AccountID       string         `json:"account_id,omitempty"`
	BreakType       string         `json:"break_type"`
	Severity        string         `json:"severity"`
	Status          string         `json:"status"`
	ObjectType      string         `json:"object_type"`
	ObjectID        string         `json:"object_id,omitempty"`
	InternalPayload map[string]any `json:"internal_payload,omitempty"`
	ExternalPayload map[string]any `json:"external_payload,omitempty"`
	Description     string         `json:"description,omitempty"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	ResolvedAt      time.Time      `json:"resolved_at,omitempty"`
}

type ReconciliationBreakQuery struct {
	RunID     string
	AccountID string
	Status    string
	Limit     int
}

type RawStreamSummaryBucket struct {
	Role           string    `json:"role"`
	MessageType    string    `json:"message_type,omitempty"`
	Action         string    `json:"action,omitempty"`
	EventType      string    `json:"event_type,omitempty"`
	Count          int64     `json:"count"`
	LastReceivedAt time.Time `json:"last_received_at,omitempty"`
}

type DailyPerformance struct {
	AccountID           string    `json:"account_id"`
	TradeDate           string    `json:"trade_date"`
	CashAvailable       float64   `json:"cash_available"`
	CashTotal           float64   `json:"cash_total"`
	NetAsset            float64   `json:"net_asset"`
	PreviousNetAsset    float64   `json:"previous_net_asset"`
	OpenNetAsset        float64   `json:"open_net_asset"`
	OvernightAdjustment float64   `json:"overnight_adjustment"`
	AssetChange         float64   `json:"asset_change"`
	IntradayPnL         float64   `json:"intraday_pnl"`
	IntradayReturn      float64   `json:"intraday_return"`
	OpenSnapshotSource  string    `json:"open_snapshot_source,omitempty"`
	DailyPnL            float64   `json:"daily_pnl"`
	ReturnRate          float64   `json:"return_rate"`
	MarketValue         float64   `json:"market_value"`
	StockValue          float64   `json:"stock_value"`
	FundValue           float64   `json:"fund_value"`
	DayProfit           float64   `json:"day_profit"`
	PositionProfit      float64   `json:"position_profit"`
	CloseProfit         float64   `json:"close_profit"`
	Credit              float64   `json:"credit"`
	PositionsCount      int64     `json:"positions_count"`
	PositionMarketValue float64   `json:"position_market_value"`
	UnrealizedPnL       float64   `json:"unrealized_pnl"`
	SettledProfit       float64   `json:"settled_profit"`
	RealizedPnL         float64   `json:"realized_pnl"`
	GrossPnL            float64   `json:"gross_pnl"`
	NetPnL              float64   `json:"net_pnl"`
	FillsCount          int64     `json:"fills_count"`
	BuyAmount           float64   `json:"buy_amount"`
	SellAmount          float64   `json:"sell_amount"`
	Turnover            float64   `json:"turnover"`
	FeeTotal            float64   `json:"fee_total"`
	CumulativeReturn    float64   `json:"cumulative_return"`
	Drawdown            float64   `json:"drawdown"`
	BenchmarkSecurityID string    `json:"benchmark_security_id,omitempty"`
	BenchmarkClose      *float64  `json:"benchmark_close,omitempty"`
	BenchmarkReturn     *float64  `json:"benchmark_return,omitempty"`
	BenchmarkCumulative *float64  `json:"benchmark_cumulative_return,omitempty"`
	BenchmarkDrawdown   *float64  `json:"benchmark_drawdown,omitempty"`
	ExcessReturn        *float64  `json:"excess_return,omitempty"`
	ExcessCumulative    *float64  `json:"excess_cumulative_return,omitempty"`
	QualityFlags        []string  `json:"quality_flags,omitempty"`
	OpenCapturedAt      time.Time `json:"open_captured_at,omitempty"`
	CapturedAt          time.Time `json:"captured_at,omitempty"`
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

func (repo *Repository) UpsertAccountAlias(ctx context.Context, accountID string, brokerID string, alias string) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	accountID = strings.TrimSpace(accountID)
	brokerID = strings.TrimSpace(brokerID)
	alias = strings.TrimSpace(alias)
	if accountID == "" {
		return fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if brokerID == "" {
		return fmt.Errorf("%w: broker_id is required", ErrInvalidLedgerInput)
	}
	if _, err := repo.exec.ExecContext(ctx, upsertAccountAliasSQL, accountID, brokerID, alias, repo.now()); err != nil {
		return fmt.Errorf("upsert account alias %s: %w", accountID, err)
	}
	return nil
}

func (repo *Repository) AccountAliases(ctx context.Context, accountIDs []string) (map[string]string, error) {
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	cleaned := make([]string, 0, len(accountIDs))
	seen := make(map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		cleaned = append(cleaned, accountID)
	}
	if len(cleaned) == 0 {
		return map[string]string{}, nil
	}

	builder := strings.Builder{}
	builder.WriteString("SELECT account_id, account_name FROM accounts WHERE account_id IN (")
	args := make([]any, 0, len(cleaned))
	for i, accountID := range cleaned {
		if i > 0 {
			builder.WriteString(", ")
		}
		args = append(args, accountID)
		builder.WriteString(fmt.Sprintf("$%d", len(args)))
	}
	builder.WriteString(")")

	rows, err := queryer.QueryContext(ctx, builder.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query account aliases: %w", err)
	}
	defer rows.Close()

	aliases := make(map[string]string, len(cleaned))
	for rows.Next() {
		var accountID string
		var alias string
		if err := rows.Scan(&accountID, &alias); err != nil {
			return nil, fmt.Errorf("scan account alias: %w", err)
		}
		if strings.TrimSpace(alias) != "" {
			aliases[accountID] = strings.TrimSpace(alias)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account alias rows: %w", err)
	}
	return aliases, nil
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

func (repo *Repository) GetOrderByIdempotencyKey(ctx context.Context, accountID string, idempotencyKey string) (trading.Order, error) {
	if repo == nil || repo.exec == nil {
		return trading.Order{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	accountID = strings.TrimSpace(accountID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if accountID == "" {
		return trading.Order{}, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if idempotencyKey == "" {
		return trading.Order{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return trading.Order{}, err
	}
	rows, err := queryer.QueryContext(ctx, getOrderByIdempotencyKeySQL, accountID, idempotencyKey)
	if err != nil {
		return trading.Order{}, fmt.Errorf("get order by idempotency key %s: %w", accountID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return trading.Order{}, fmt.Errorf("get order by idempotency key %s: %w", accountID, err)
		}
		return trading.Order{}, fmt.Errorf("%w: %s idempotency_key=%s", ErrOrderNotFound, accountID, idempotencyKey)
	}
	order, err := scanOrder(rows)
	if err != nil {
		return trading.Order{}, fmt.Errorf("scan order by idempotency key %s: %w", accountID, err)
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

func (repo *Repository) GetDailyPerformance(ctx context.Context, accountID string, tradeDate string) (DailyPerformance, error) {
	if repo == nil || repo.exec == nil {
		return DailyPerformance{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return DailyPerformance{}, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	tradeDate, err := normalizeTradeDate(tradeDate)
	if err != nil {
		return DailyPerformance{}, err
	}
	if tradeDate == "" {
		return DailyPerformance{}, fmt.Errorf("%w: trade_date is required", ErrInvalidLedgerInput)
	}
	start, err := dateStart(tradeDate)
	if err != nil {
		return DailyPerformance{}, err
	}
	end := start.AddDate(0, 0, 1)

	queryer, err := repo.queryer()
	if err != nil {
		return DailyPerformance{}, err
	}
	rows, err := queryer.QueryContext(ctx, dailyPerformanceSQL, accountID, tradeDate, start, end)
	if err != nil {
		return DailyPerformance{}, fmt.Errorf("get daily performance %s/%s: %w", accountID, tradeDate, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return DailyPerformance{}, fmt.Errorf("get daily performance %s/%s: %w", accountID, tradeDate, err)
		}
		return DailyPerformance{}, fmt.Errorf("%w: close asset snapshot %s/%s", ErrAssetNotFound, accountID, tradeDate)
	}
	performance, err := scanDailyPerformance(rows)
	if err != nil {
		return DailyPerformance{}, fmt.Errorf("scan daily performance %s/%s: %w", accountID, tradeDate, err)
	}
	return performance, nil
}

func (repo *Repository) ListDailyPerformance(ctx context.Context, accountID string, dateFrom string, dateTo string) ([]DailyPerformance, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	if dateFrom == "" && dateTo == "" {
		return nil, fmt.Errorf("%w: date_from or date_to is required", ErrInvalidLedgerInput)
	}
	if dateFrom == "" {
		dateFrom = dateTo
	}
	if dateTo == "" {
		dateTo = dateFrom
	}
	var err error
	dateFrom, err = normalizeTradeDate(dateFrom)
	if err != nil {
		return nil, err
	}
	dateTo, err = normalizeTradeDate(dateTo)
	if err != nil {
		return nil, err
	}
	start, err := dateStart(dateFrom)
	if err != nil {
		return nil, err
	}
	endDate, err := dateStart(dateTo)
	if err != nil {
		return nil, err
	}
	if endDate.Before(start) {
		return nil, fmt.Errorf("%w: date_to must be greater than or equal to date_from", ErrInvalidLedgerInput)
	}
	endExclusive := endDate.AddDate(0, 0, 1)

	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, dailyPerformanceSeriesSQL, accountID, dateFrom, dateTo, start, endExclusive)
	if err != nil {
		return nil, fmt.Errorf("list daily performance %s/%s-%s: %w", accountID, dateFrom, dateTo, err)
	}
	defer rows.Close()

	var series []DailyPerformance
	for rows.Next() {
		item, err := scanDailyPerformance(rows)
		if err != nil {
			return nil, fmt.Errorf("scan daily performance series %s/%s-%s: %w", accountID, dateFrom, dateTo, err)
		}
		series = append(series, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list daily performance rows %s/%s-%s: %w", accountID, dateFrom, dateTo, err)
	}
	return series, nil
}

func (repo *Repository) UpsertAssetSnapshot(ctx context.Context, asset trading.Asset, snapshotType string, source string, rawPayload any, capturedAt time.Time) error {
	tradeDate := ""
	if !capturedAt.IsZero() {
		tradeDate = capturedAt.Format("2006-01-02")
	}
	return repo.UpsertAssetSnapshotForDate(ctx, asset, tradeDate, snapshotType, source, rawPayload, capturedAt)
}

func (repo *Repository) UpsertAssetSnapshotForDate(ctx context.Context, asset trading.Asset, tradeDate string, snapshotType string, source string, rawPayload any, capturedAt time.Time) error {
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
	tradeDate, err = normalizeTradeDate(firstNonEmpty(tradeDate, capturedAt.Format("2006-01-02")))
	if err != nil {
		return err
	}
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = capturedAt
	}
	body, err := marshalJSONObject(rawPayload)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, upsertAssetSnapshotSQL,
		tradeDate,
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

func (repo *Repository) ListPositionSnapshots(ctx context.Context, query trading.PositionQuery) ([]trading.Position, error) {
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
	sqlText, args := buildListPositionSnapshotsSQL(normalized)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list position snapshots: %w", err)
	}
	defer rows.Close()

	positions := make([]trading.Position, 0, normalized.Limit)
	for rows.Next() {
		position, err := scanPositionSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("scan position snapshot: %w", err)
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list position snapshot rows: %w", err)
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

func (repo *Repository) UpsertPositionSnapshot(ctx context.Context, position trading.Position, source string, rawPayload any, capturedAt time.Time) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizePosition(position)
	if err != nil {
		return err
	}
	if capturedAt.IsZero() {
		capturedAt = repo.now()
	}
	tradeDate, err := normalizeTradeDate(firstNonEmpty(normalized.TradeDate, capturedAt.Format("2006-01-02")))
	if err != nil {
		return err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "close"
	}
	body, err := marshalJSONObject(rawPayload)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, upsertPositionSnapshotSQL,
		tradeDate,
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
		capturedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert position snapshot %s/%s.%s: %w", normalized.AccountID, normalized.Symbol, normalized.Exchange, err)
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
	if !strings.HasPrefix(normalized.FillID, "relay-summary:") {
		if _, err := repo.exec.ExecContext(ctx, deleteSummaryFillsForOrderSQL, normalized.AccountID, normalized.GatewayOrderID, nullString(normalized.OrderStreamID)); err != nil {
			return fmt.Errorf("delete summary fills %s/%s: %w", normalized.AccountID, normalized.GatewayOrderID, err)
		}
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

func (repo *Repository) GetStreamCheckpoint(ctx context.Context, streamKey string) (StreamCheckpoint, error) {
	if repo == nil || repo.exec == nil {
		return StreamCheckpoint{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	streamKey = strings.TrimSpace(streamKey)
	if streamKey == "" {
		return StreamCheckpoint{}, fmt.Errorf("%w: stream_key is required", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return StreamCheckpoint{}, err
	}
	rows, err := queryer.QueryContext(ctx, getStreamCheckpointSQL, streamKey)
	if err != nil {
		return StreamCheckpoint{}, fmt.Errorf("get stream checkpoint %s: %w", streamKey, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return StreamCheckpoint{}, fmt.Errorf("get stream checkpoint %s: %w", streamKey, err)
		}
		return StreamCheckpoint{}, fmt.Errorf("%w: %s", ErrStreamCheckpointNotFound, streamKey)
	}
	checkpoint, err := scanStreamCheckpoint(rows)
	if err != nil {
		return StreamCheckpoint{}, fmt.Errorf("scan stream checkpoint %s: %w", streamKey, err)
	}
	return checkpoint, nil
}

func (repo *Repository) UpsertStreamCheckpoint(ctx context.Context, checkpoint StreamCheckpoint) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	checkpoint.StreamKey = strings.TrimSpace(checkpoint.StreamKey)
	checkpoint.Role = strings.TrimSpace(checkpoint.Role)
	checkpoint.LastStreamID = strings.TrimSpace(checkpoint.LastStreamID)
	if checkpoint.StreamKey == "" {
		return fmt.Errorf("%w: stream_key is required", ErrInvalidLedgerInput)
	}
	if checkpoint.Role == "" {
		return fmt.Errorf("%w: stream_role is required", ErrInvalidLedgerInput)
	}
	if checkpoint.LastStreamID == "" {
		checkpoint.LastStreamID = "0"
	}
	if checkpoint.ProcessedCount < 0 {
		return fmt.Errorf("%w: processed_count must be non-negative", ErrInvalidLedgerInput)
	}
	if checkpoint.ErrorCount < 0 {
		return fmt.Errorf("%w: error_count must be non-negative", ErrInvalidLedgerInput)
	}
	metadata, err := marshalJSONObject(checkpoint.Metadata)
	if err != nil {
		return err
	}

	_, err = repo.exec.ExecContext(ctx, upsertStreamCheckpointSQL,
		checkpoint.StreamKey,
		checkpoint.Role,
		checkpoint.LastStreamID,
		nullTime(checkpoint.LastSeenAt),
		nullTime(checkpoint.LastProcessedAt),
		checkpoint.LastError,
		checkpoint.ProcessedCount,
		checkpoint.ErrorCount,
		metadata,
	)
	if err != nil {
		return fmt.Errorf("upsert stream checkpoint %s: %w", checkpoint.StreamKey, err)
	}
	return nil
}

func (repo *Repository) UpsertJobRun(ctx context.Context, run JobRun) (JobRun, error) {
	if repo == nil || repo.exec == nil {
		return JobRun{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeJobRun(run, repo.now())
	if err != nil {
		return JobRun{}, err
	}
	report, err := marshalJSONObject(normalized.Report)
	if err != nil {
		return JobRun{}, err
	}
	_, err = repo.exec.ExecContext(ctx, upsertJobRunSQL,
		normalized.RunID,
		normalized.JobName,
		normalized.TargetTradeDate,
		normalized.Timezone,
		normalized.Status,
		nullString(normalized.Trigger),
		normalized.Skipped,
		nullTime(normalized.StartedAt),
		nullTime(normalized.FinishedAt),
		normalized.DurationMS,
		report,
		nullString(normalized.ErrorSummary),
	)
	if err != nil {
		return JobRun{}, fmt.Errorf("upsert job run %s/%s: %w", normalized.JobName, normalized.RunID, err)
	}
	return normalized, nil
}

func (repo *Repository) LatestJobRuns(ctx context.Context, jobNames []string) ([]JobRun, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	sqlText, args := buildLatestJobRunsSQL(jobNames)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("latest job runs: %w", err)
	}
	defer rows.Close()
	runs := make([]JobRun, 0, len(jobNames))
	for rows.Next() {
		run, err := scanJobRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("latest job run rows: %w", err)
	}
	if len(runs) == 0 && len(jobNames) > 0 {
		return nil, ErrJobRunNotFound
	}
	return runs, nil
}

func (repo *Repository) UpsertReconciliationRun(ctx context.Context, run ReconciliationRun) (ReconciliationRun, error) {
	if repo == nil || repo.exec == nil {
		return ReconciliationRun{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeReconciliationRun(run, repo.now())
	if err != nil {
		return ReconciliationRun{}, err
	}
	summary, err := marshalJSONObject(normalized.Summary)
	if err != nil {
		return ReconciliationRun{}, err
	}
	_, err = repo.exec.ExecContext(ctx, upsertReconciliationRunSQL,
		normalized.RunID,
		normalized.TradeDate,
		normalized.Status,
		normalized.Source,
		normalized.StartedAt,
		nullTime(normalized.CompletedAt),
		summary,
		nullString(normalized.ErrorMessage),
	)
	if err != nil {
		return ReconciliationRun{}, fmt.Errorf("upsert reconciliation run %s: %w", normalized.RunID, err)
	}
	return normalized, nil
}

func (repo *Repository) UpsertReconciliationInput(ctx context.Context, input ReconciliationInput) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeReconciliationInput(input, repo.now())
	if err != nil {
		return err
	}
	payload, err := marshalJSONObject(normalized.Payload)
	if err != nil {
		return err
	}
	_, err = repo.exec.ExecContext(ctx, upsertReconciliationInputSQL,
		normalized.RunID,
		normalized.Source,
		normalized.InputType,
		payload,
		normalized.CapturedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert reconciliation input %s/%s/%s: %w", normalized.RunID, normalized.Source, normalized.InputType, err)
	}
	return nil
}

func (repo *Repository) UpsertReconciliationBreak(ctx context.Context, item ReconciliationBreak) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeReconciliationBreak(item, repo.now())
	if err != nil {
		return err
	}
	internalPayload, err := marshalJSONObject(normalized.InternalPayload)
	if err != nil {
		return err
	}
	externalPayload, err := marshalJSONObject(normalized.ExternalPayload)
	if err != nil {
		return err
	}
	_, err = repo.exec.ExecContext(ctx, upsertReconciliationBreakSQL,
		normalized.RunID,
		nullString(normalized.AccountID),
		normalized.BreakType,
		normalized.Severity,
		normalized.Status,
		normalized.ObjectType,
		nullString(normalized.ObjectID),
		internalPayload,
		externalPayload,
		normalized.Description,
		normalized.CreatedAt,
		nullTime(normalized.ResolvedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert reconciliation break %s/%s/%s/%s: %w", normalized.RunID, normalized.BreakType, normalized.ObjectType, normalized.ObjectID, err)
	}
	return nil
}

func (repo *Repository) ListReconciliationBreaks(ctx context.Context, query ReconciliationBreakQuery) ([]ReconciliationBreak, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	normalized := normalizeReconciliationBreakQuery(query)
	sqlText, args := buildListReconciliationBreaksSQL(normalized)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list reconciliation breaks: %w", err)
	}
	defer rows.Close()
	items := make([]ReconciliationBreak, 0)
	for rows.Next() {
		item, err := scanReconciliationBreak(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reconciliation break: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reconciliation break rows: %w", err)
	}
	return items, nil
}

func (repo *Repository) RawStreamSummary(ctx context.Context, accountID string, start time.Time, end time.Time) ([]RawStreamSummaryBucket, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if start.IsZero() || end.IsZero() {
		return nil, fmt.Errorf("%w: start and end are required", ErrInvalidLedgerInput)
	}
	rows, err := queryer.QueryContext(ctx, rawStreamSummarySQL, accountID, start, end)
	if err != nil {
		return nil, fmt.Errorf("raw stream summary %s: %w", accountID, err)
	}
	defer rows.Close()
	buckets := make([]RawStreamSummaryBucket, 0)
	for rows.Next() {
		bucket, err := scanRawStreamSummaryBucket(rows)
		if err != nil {
			return nil, fmt.Errorf("scan raw stream summary: %w", err)
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("raw stream summary rows: %w", err)
	}
	return buckets, nil
}

func (repo *Repository) queryer() (Queryer, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
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
	var err error
	query.TradeDate, query.DateFrom, query.DateTo, err = normalizeQueryDates(query.TradeDate, query.DateFrom, query.DateTo)
	if err != nil {
		return query, err
	}
	if query.Exchange != "" && !query.Exchange.Valid() {
		return query, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if query.Status != "" && !query.Status.Valid() {
		return query, fmt.Errorf("%w: status is invalid", ErrInvalidLedgerInput)
	}
	if _, err := queryCursorOffset(query.Cursor); err != nil {
		return query, err
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
	var err error
	query.TradeDate, query.DateFrom, query.DateTo, err = normalizeQueryDates(query.TradeDate, query.DateFrom, query.DateTo)
	if err != nil {
		return query, err
	}
	if query.Exchange != "" && !query.Exchange.Valid() {
		return query, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if _, err := queryCursorOffset(query.Cursor); err != nil {
		return query, err
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
	var err error
	query.TradeDate, query.DateFrom, query.DateTo, err = normalizeQueryDates(query.TradeDate, query.DateFrom, query.DateTo)
	if err != nil {
		return query, err
	}
	if query.AccountID == "" {
		return query, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	if query.Exchange != "" && !query.Exchange.Valid() {
		return query, fmt.Errorf("%w: exchange must be SH, SZ, or BJ", ErrInvalidLedgerInput)
	}
	if _, err := queryCursorOffset(query.Cursor); err != nil {
		return query, err
	}
	if query.Limit <= 0 {
		query.Limit = 500
	}
	if query.Limit > 2000 {
		query.Limit = 2000
	}
	return query, nil
}

func normalizeQueryDates(tradeDate string, dateFrom string, dateTo string) (string, string, string, error) {
	tradeDate = strings.TrimSpace(tradeDate)
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	var err error
	if tradeDate != "" {
		tradeDate, err = normalizeTradeDate(tradeDate)
		if err != nil {
			return "", "", "", err
		}
		if dateFrom != "" || dateTo != "" {
			return "", "", "", fmt.Errorf("%w: trade_date cannot be combined with date_from/date_to", ErrInvalidLedgerInput)
		}
		return tradeDate, tradeDate, tradeDate, nil
	}
	if dateFrom != "" {
		dateFrom, err = normalizeTradeDate(dateFrom)
		if err != nil {
			return "", "", "", err
		}
	}
	if dateTo != "" {
		dateTo, err = normalizeTradeDate(dateTo)
		if err != nil {
			return "", "", "", err
		}
	}
	if dateFrom != "" && dateTo != "" && dateFrom > dateTo {
		return "", "", "", fmt.Errorf("%w: date_from must be <= date_to", ErrInvalidLedgerInput)
	}
	return "", dateFrom, dateTo, nil
}

func queryCursorOffset(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: cursor must be a non-negative offset", ErrInvalidLedgerInput)
	}
	return offset, nil
}

func normalizeTradeDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	layout := "2006-01-02"
	if len(value) == 8 {
		layout = "20060102"
	}
	parsed, err := time.ParseInLocation(layout, value, timeutil.Location())
	if err != nil {
		return "", fmt.Errorf("%w: date must be YYYYMMDD or YYYY-MM-DD", ErrInvalidLedgerInput)
	}
	return parsed.Format("2006-01-02"), nil
}

func dateStart(value string) (time.Time, error) {
	normalized, err := normalizeTradeDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("2006-01-02", normalized, timeutil.Location())
}

func dateEndExclusive(value string) (time.Time, error) {
	start, err := dateStart(value)
	if err != nil {
		return time.Time{}, err
	}
	return start.AddDate(0, 0, 1), nil
}

func nextTradeDate(value string) (string, error) {
	start, err := dateStart(value)
	if err != nil {
		return "", err
	}
	return start.AddDate(0, 0, 1).Format("2006-01-02"), nil
}

func normalizeJobRun(run JobRun, now time.Time) (JobRun, error) {
	run.JobName = strings.TrimSpace(run.JobName)
	run.RunID = strings.TrimSpace(run.RunID)
	run.TargetTradeDate = strings.TrimSpace(run.TargetTradeDate)
	run.Timezone = strings.TrimSpace(run.Timezone)
	run.Status = strings.TrimSpace(run.Status)
	run.Trigger = strings.TrimSpace(run.Trigger)
	run.ErrorSummary = strings.TrimSpace(run.ErrorSummary)
	if run.JobName == "" {
		return run, fmt.Errorf("%w: job_name is required", ErrInvalidLedgerInput)
	}
	var err error
	run.TargetTradeDate, err = normalizeTradeDate(run.TargetTradeDate)
	if err != nil {
		return run, err
	}
	if run.TargetTradeDate == "" {
		return run, fmt.Errorf("%w: target_trade_date is required", ErrInvalidLedgerInput)
	}
	if run.Timezone == "" {
		run.Timezone = timeutil.LocationName
	}
	if run.Status == "" {
		run.Status = "succeeded"
	}
	if run.Status == "completed" {
		run.Status = "succeeded"
	}
	switch run.Status {
	case "running", "succeeded", "skipped", "failed":
	default:
		return run, fmt.Errorf("%w: job status must be running, succeeded, skipped, or failed", ErrInvalidLedgerInput)
	}
	if now.IsZero() {
		now = time.Now()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	if run.FinishedAt.IsZero() && run.Status != "running" {
		run.FinishedAt = now
	}
	if run.DurationMS == 0 && !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() {
		run.DurationMS = int64(run.FinishedAt.Sub(run.StartedAt) / time.Millisecond)
		if run.DurationMS < 0 {
			run.DurationMS = 0
		}
	}
	if run.RunID == "" {
		run.RunID = fmt.Sprintf("%s-%s-%d", run.JobName, strings.ReplaceAll(run.TargetTradeDate, "-", ""), run.StartedAt.UnixNano())
	}
	if run.Report == nil {
		run.Report = map[string]any{}
	}
	return run, nil
}

func normalizeReconciliationRun(run ReconciliationRun, now time.Time) (ReconciliationRun, error) {
	run.RunID = strings.TrimSpace(run.RunID)
	run.TradeDate = strings.TrimSpace(run.TradeDate)
	run.Status = strings.TrimSpace(run.Status)
	run.Source = strings.TrimSpace(run.Source)
	run.ErrorMessage = strings.TrimSpace(run.ErrorMessage)
	var err error
	run.TradeDate, err = normalizeTradeDate(run.TradeDate)
	if err != nil {
		return run, err
	}
	if run.TradeDate == "" {
		return run, fmt.Errorf("%w: trade_date is required", ErrInvalidLedgerInput)
	}
	if run.Source == "" {
		run.Source = "post_close_settlement"
	}
	if run.Status == "" {
		run.Status = "completed"
	}
	switch run.Status {
	case "running", "completed", "failed":
	default:
		return run, fmt.Errorf("%w: reconciliation status must be running, completed, or failed", ErrInvalidLedgerInput)
	}
	if now.IsZero() {
		now = time.Now()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	if run.CompletedAt.IsZero() && run.Status != "running" {
		run.CompletedAt = now
	}
	if run.RunID == "" {
		run.RunID = fmt.Sprintf("%s-%s", run.Source, strings.ReplaceAll(run.TradeDate, "-", ""))
	}
	if run.Summary == nil {
		run.Summary = map[string]any{}
	}
	return run, nil
}

func normalizeReconciliationInput(input ReconciliationInput, now time.Time) (ReconciliationInput, error) {
	input.RunID = strings.TrimSpace(input.RunID)
	input.Source = strings.TrimSpace(input.Source)
	input.InputType = strings.TrimSpace(input.InputType)
	if input.RunID == "" {
		return input, fmt.Errorf("%w: run_id is required", ErrInvalidLedgerInput)
	}
	if input.Source == "" {
		return input, fmt.Errorf("%w: source is required", ErrInvalidLedgerInput)
	}
	if input.InputType == "" {
		return input, fmt.Errorf("%w: input_type is required", ErrInvalidLedgerInput)
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.CapturedAt.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		input.CapturedAt = now
	}
	return input, nil
}

func normalizeReconciliationBreak(item ReconciliationBreak, now time.Time) (ReconciliationBreak, error) {
	item.RunID = strings.TrimSpace(item.RunID)
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.BreakType = strings.TrimSpace(item.BreakType)
	item.Severity = strings.TrimSpace(item.Severity)
	item.Status = strings.TrimSpace(item.Status)
	item.ObjectType = strings.TrimSpace(item.ObjectType)
	item.ObjectID = strings.TrimSpace(item.ObjectID)
	item.Description = strings.TrimSpace(item.Description)
	if item.RunID == "" {
		return item, fmt.Errorf("%w: run_id is required", ErrInvalidLedgerInput)
	}
	if item.BreakType == "" {
		return item, fmt.Errorf("%w: break_type is required", ErrInvalidLedgerInput)
	}
	if item.ObjectType == "" {
		return item, fmt.Errorf("%w: object_type is required", ErrInvalidLedgerInput)
	}
	if item.Severity == "" {
		item.Severity = "warning"
	}
	switch item.Severity {
	case "info", "warning", "critical":
	default:
		return item, fmt.Errorf("%w: severity must be info, warning, or critical", ErrInvalidLedgerInput)
	}
	if item.Status == "" {
		item.Status = "open"
	}
	switch item.Status {
	case "open", "resolved", "ignored":
	default:
		return item, fmt.Errorf("%w: break status must be open, resolved, or ignored", ErrInvalidLedgerInput)
	}
	if item.InternalPayload == nil {
		item.InternalPayload = map[string]any{}
	}
	if item.ExternalPayload == nil {
		item.ExternalPayload = map[string]any{}
	}
	if item.CreatedAt.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		item.CreatedAt = now
	}
	return item, nil
}

func normalizeReconciliationBreakQuery(query ReconciliationBreakQuery) ReconciliationBreakQuery {
	query.RunID = strings.TrimSpace(query.RunID)
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.Status = strings.TrimSpace(query.Status)
	if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 200
	}
	return query
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
	if query.DateFrom != "" || query.DateTo != "" {
		appendTimestampRange(&where, &args, "created_at", query.DateFrom, query.DateTo)
	}

	builder := strings.Builder{}
	builder.WriteString(orderSelectColumns)
	if len(where) > 0 {
		builder.WriteString("WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
		builder.WriteString("\n")
	}
	builder.WriteString("ORDER BY COALESCE(last_updated_at, created_at) DESC, gateway_order_id DESC")
	appendLimitOffset(&builder, &args, query.Limit, query.Cursor)
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
	if query.DateFrom != "" || query.DateTo != "" {
		appendFillDateRange(&where, &args, query.DateFrom, query.DateTo)
	}

	builder := strings.Builder{}
	builder.WriteString(fillSelectColumns)
	if len(where) > 0 {
		builder.WriteString("WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
		builder.WriteString("\n")
	}
	builder.WriteString("ORDER BY COALESCE(matched_at, created_at) DESC, fill_pk DESC")
	appendLimitOffset(&builder, &args, query.Limit, query.Cursor)
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
	builder.WriteString("ORDER BY market_value DESC, symbol ASC, exchange ASC")
	appendLimitOffset(&builder, &args, query.Limit, query.Cursor)
	return builder.String(), args
}

func buildListPositionSnapshotsSQL(query trading.PositionQuery) (string, []any) {
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
	if query.DateFrom != "" {
		args = append(args, query.DateFrom)
		where = append(where, fmt.Sprintf("trade_date >= $%d::date", len(args)))
	}
	if query.DateTo != "" {
		next, _ := nextTradeDate(query.DateTo)
		args = append(args, next)
		where = append(where, fmt.Sprintf("trade_date < $%d::date", len(args)))
	}

	builder := strings.Builder{}
	builder.WriteString(positionSnapshotSelectColumns)
	builder.WriteString("WHERE ")
	builder.WriteString(strings.Join(where, " AND "))
	builder.WriteString("\n")
	builder.WriteString("ORDER BY trade_date DESC, market_value DESC, symbol ASC, exchange ASC")
	appendLimitOffset(&builder, &args, query.Limit, query.Cursor)
	return builder.String(), args
}

func buildLatestJobRunsSQL(jobNames []string) (string, []any) {
	cleaned := make([]string, 0, len(jobNames))
	for _, name := range jobNames {
		name = strings.TrimSpace(name)
		if name != "" {
			cleaned = append(cleaned, name)
		}
	}
	builder := strings.Builder{}
	builder.WriteString(strings.Replace(jobRunSelectColumns, "\nSELECT", "\nSELECT DISTINCT ON (job_name)", 1))
	var args []any
	if len(cleaned) > 0 {
		placeholders := make([]string, 0, len(cleaned))
		for _, name := range cleaned {
			args = append(args, name)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		builder.WriteString("WHERE job_name IN (")
		builder.WriteString(strings.Join(placeholders, ", "))
		builder.WriteString(")\n")
	}
	builder.WriteString("ORDER BY job_name, COALESCE(finished_at, started_at, updated_at) DESC, job_run_pk DESC")
	return builder.String(), args
}

func buildListReconciliationBreaksSQL(query ReconciliationBreakQuery) (string, []any) {
	var where []string
	var args []any
	appendFilter := func(column string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if query.RunID != "" {
		appendFilter("run_id", query.RunID)
	}
	if query.AccountID != "" {
		appendFilter("account_id", query.AccountID)
	}
	if query.Status != "" {
		appendFilter("status", query.Status)
	}
	builder := strings.Builder{}
	builder.WriteString(reconciliationBreakSelectColumns)
	if len(where) > 0 {
		builder.WriteString("WHERE ")
		builder.WriteString(strings.Join(where, " AND "))
		builder.WriteString("\n")
	}
	args = append(args, query.Limit)
	builder.WriteString(fmt.Sprintf("ORDER BY created_at DESC, reconciliation_break_pk DESC LIMIT $%d", len(args)))
	return builder.String(), args
}

func appendLimitOffset(builder *strings.Builder, args *[]any, limit int, cursor string) {
	*args = append(*args, limit)
	builder.WriteString(fmt.Sprintf(" LIMIT $%d", len(*args)))
	offset, _ := queryCursorOffset(cursor)
	if offset <= 0 {
		return
	}
	*args = append(*args, offset)
	builder.WriteString(fmt.Sprintf(" OFFSET $%d", len(*args)))
}

func appendTimestampRange(where *[]string, args *[]any, column string, dateFrom string, dateTo string) {
	if dateFrom != "" {
		start, _ := dateStart(dateFrom)
		*args = append(*args, start)
		*where = append(*where, fmt.Sprintf("%s >= $%d", column, len(*args)))
	}
	if dateTo != "" {
		end, _ := dateEndExclusive(dateTo)
		*args = append(*args, end)
		*where = append(*where, fmt.Sprintf("%s < $%d", column, len(*args)))
	}
}

func appendFillDateRange(where *[]string, args *[]any, dateFrom string, dateTo string) {
	if dateFrom != "" {
		start, _ := dateStart(dateFrom)
		*args = append(*args, dateFrom, start)
		dateArg := len(*args) - 1
		timeArg := len(*args)
		*where = append(*where, fmt.Sprintf("((trade_date IS NOT NULL AND trade_date >= $%d::date) OR (trade_date IS NULL AND COALESCE(matched_at, created_at) >= $%d))", dateArg, timeArg))
	}
	if dateTo != "" {
		next, _ := nextTradeDate(dateTo)
		end, _ := dateEndExclusive(dateTo)
		*args = append(*args, next, end)
		dateArg := len(*args) - 1
		timeArg := len(*args)
		*where = append(*where, fmt.Sprintf("((trade_date IS NOT NULL AND trade_date < $%d::date) OR (trade_date IS NULL AND COALESCE(matched_at, created_at) < $%d))", dateArg, timeArg))
	}
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

func scanDailyPerformance(row rowScanner) (DailyPerformance, error) {
	var performance DailyPerformance
	var openCapturedAt sql.NullTime
	var capturedAt sql.NullTime
	err := row.Scan(
		&performance.AccountID,
		&performance.TradeDate,
		&performance.CashAvailable,
		&performance.CashTotal,
		&performance.NetAsset,
		&performance.PreviousNetAsset,
		&performance.OpenNetAsset,
		&performance.OvernightAdjustment,
		&performance.AssetChange,
		&performance.IntradayPnL,
		&performance.IntradayReturn,
		&performance.OpenSnapshotSource,
		&performance.DailyPnL,
		&performance.ReturnRate,
		&performance.MarketValue,
		&performance.StockValue,
		&performance.FundValue,
		&performance.DayProfit,
		&performance.PositionProfit,
		&performance.CloseProfit,
		&performance.Credit,
		&performance.PositionsCount,
		&performance.PositionMarketValue,
		&performance.UnrealizedPnL,
		&performance.SettledProfit,
		&performance.FillsCount,
		&performance.BuyAmount,
		&performance.SellAmount,
		&performance.Turnover,
		&performance.FeeTotal,
		&openCapturedAt,
		&capturedAt,
	)
	if err != nil {
		return DailyPerformance{}, err
	}
	if openCapturedAt.Valid {
		performance.OpenCapturedAt = openCapturedAt.Time
	}
	performance.CapturedAt = capturedAt.Time
	derivePerformancePnL(&performance)
	return performance, nil
}

func derivePerformancePnL(performance *DailyPerformance) {
	if performance == nil {
		return
	}
	performance.RealizedPnL = performance.SettledProfit
	performance.GrossPnL = performance.RealizedPnL + performance.UnrealizedPnL
	performance.NetPnL = performance.GrossPnL - performance.FeeTotal
	if performance.OpenSnapshotSource == "" && performance.PreviousNetAsset > 0 {
		performance.OpenNetAsset = performance.PreviousNetAsset
		performance.OpenSnapshotSource = "previous_close_fallback"
	}
	if performance.OpenSnapshotSource == "previous_close_fallback" {
		performance.QualityFlags = append(performance.QualityFlags, "missing_open_asset", "open_asset_fallback")
	}
	if performance.OpenSnapshotSource == "" {
		performance.QualityFlags = append(performance.QualityFlags, "missing_open_asset")
	}
	if performance.OvernightAdjustment != 0 {
		performance.QualityFlags = append(performance.QualityFlags, "overnight_adjustment_unclassified")
	}
}

func scanPosition(row rowScanner) (trading.Position, error) {
	var position trading.Position
	var tradeDate sql.NullString
	var lastPrice sql.NullFloat64
	var shareholderID sql.NullString
	var updatedAt sql.NullTime
	err := row.Scan(
		&position.AccountID,
		&tradeDate,
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
	position.TradeDate = tradeDate.String
	position.LastPrice = lastPrice.Float64
	position.ShareholderID = shareholderID.String
	position.UpdatedAt = updatedAt.Time
	return position, nil
}

func scanPositionSnapshot(row rowScanner) (trading.Position, error) {
	return scanPosition(row)
}

func scanStreamCheckpoint(row rowScanner) (StreamCheckpoint, error) {
	var checkpoint StreamCheckpoint
	var lastSeenAt sql.NullTime
	var lastProcessedAt sql.NullTime
	var metadata []byte
	var updatedAt sql.NullTime

	err := row.Scan(
		&checkpoint.StreamKey,
		&checkpoint.Role,
		&checkpoint.LastStreamID,
		&lastSeenAt,
		&lastProcessedAt,
		&checkpoint.LastError,
		&checkpoint.ProcessedCount,
		&checkpoint.ErrorCount,
		&metadata,
		&updatedAt,
	)
	if err != nil {
		return StreamCheckpoint{}, err
	}
	checkpoint.LastSeenAt = lastSeenAt.Time
	checkpoint.LastProcessedAt = lastProcessedAt.Time
	checkpoint.UpdatedAt = updatedAt.Time
	checkpoint.Metadata = map[string]any{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &checkpoint.Metadata); err != nil {
			return StreamCheckpoint{}, err
		}
	}
	return checkpoint, nil
}

func scanJobRun(row rowScanner) (JobRun, error) {
	var run JobRun
	var trigger sql.NullString
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	var report []byte
	var errorSummary sql.NullString
	var createdAt sql.NullTime
	var updatedAt sql.NullTime
	err := row.Scan(
		&run.RunID,
		&run.JobName,
		&run.TargetTradeDate,
		&run.Timezone,
		&run.Status,
		&trigger,
		&run.Skipped,
		&startedAt,
		&finishedAt,
		&run.DurationMS,
		&report,
		&errorSummary,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return JobRun{}, err
	}
	run.Trigger = trigger.String
	run.StartedAt = startedAt.Time
	run.FinishedAt = finishedAt.Time
	run.ErrorSummary = errorSummary.String
	run.CreatedAt = createdAt.Time
	run.UpdatedAt = updatedAt.Time
	run.Report = map[string]any{}
	if len(report) > 0 {
		if err := json.Unmarshal(report, &run.Report); err != nil {
			return JobRun{}, err
		}
	}
	return run, nil
}

func scanReconciliationBreak(row rowScanner) (ReconciliationBreak, error) {
	var item ReconciliationBreak
	var accountID sql.NullString
	var objectID sql.NullString
	var internalPayload []byte
	var externalPayload []byte
	var createdAt sql.NullTime
	var resolvedAt sql.NullTime
	err := row.Scan(
		&item.RunID,
		&accountID,
		&item.BreakType,
		&item.Severity,
		&item.Status,
		&item.ObjectType,
		&objectID,
		&internalPayload,
		&externalPayload,
		&item.Description,
		&createdAt,
		&resolvedAt,
	)
	if err != nil {
		return ReconciliationBreak{}, err
	}
	item.AccountID = accountID.String
	item.ObjectID = objectID.String
	item.CreatedAt = createdAt.Time
	item.ResolvedAt = resolvedAt.Time
	item.InternalPayload = map[string]any{}
	if len(internalPayload) > 0 {
		if err := json.Unmarshal(internalPayload, &item.InternalPayload); err != nil {
			return ReconciliationBreak{}, err
		}
	}
	item.ExternalPayload = map[string]any{}
	if len(externalPayload) > 0 {
		if err := json.Unmarshal(externalPayload, &item.ExternalPayload); err != nil {
			return ReconciliationBreak{}, err
		}
	}
	return item, nil
}

func scanRawStreamSummaryBucket(row rowScanner) (RawStreamSummaryBucket, error) {
	var bucket RawStreamSummaryBucket
	var lastReceivedAt sql.NullTime
	err := row.Scan(
		&bucket.Role,
		&bucket.MessageType,
		&bucket.Action,
		&bucket.EventType,
		&bucket.Count,
		&lastReceivedAt,
	)
	if err != nil {
		return RawStreamSummaryBucket{}, err
	}
	bucket.LastReceivedAt = lastReceivedAt.Time
	return bucket, nil
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
	var inferredTerminal bool
	order.Status, order.GatewayStatus, inferredTerminal = trading.NormalizeOrderExecutionState(
		order.Status,
		order.GatewayStatus,
		order.OrderQty,
		order.CumFilledQty,
		order.LeavesQty,
	)
	if inferredTerminal || order.Status.Terminal() || order.GatewayStatus.Terminal() {
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
	var inferredTerminal bool
	event.Status, event.GatewayStatus, inferredTerminal = trading.NormalizeOrderExecutionState(
		event.Status,
		event.GatewayStatus,
		event.Order.OrderQty,
		event.Order.CumFilledQty,
		event.Order.LeavesQty,
	)
	event.Order.Status = event.Status
	event.Order.GatewayStatus = event.GatewayStatus
	if inferredTerminal || event.Status.Terminal() || event.GatewayStatus.Terminal() || event.Order.IsTerminal {
		event.IsTerminal = true
		event.Order.IsTerminal = true
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
