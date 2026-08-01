package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PerformanceInception struct {
	AccountID             string         `json:"account_id"`
	InceptionDate         string         `json:"inception_date"`
	Status                string         `json:"status"`
	CleanStart            bool           `json:"clean_start"`
	OpeningCash           float64        `json:"opening_cash"`
	OpeningPositionSource string         `json:"opening_position_source"`
	CostSource            string         `json:"cost_source"`
	StrategyScope         []string       `json:"strategy_scope,omitempty"`
	Description           string         `json:"description,omitempty"`
	ConfirmedBy           string         `json:"confirmed_by,omitempty"`
	ConfirmedAt           time.Time      `json:"confirmed_at,omitempty"`
	RawPayload            map[string]any `json:"raw_payload,omitempty"`
	CreatedAt             time.Time      `json:"created_at,omitempty"`
	UpdatedAt             time.Time      `json:"updated_at,omitempty"`
}

type PositionCostState struct {
	AccountID                    string         `json:"account_id"`
	TradeDate                    string         `json:"trade_date"`
	Symbol                       string         `json:"symbol"`
	Exchange                     string         `json:"exchange"`
	CostBucket                   string         `json:"cost_bucket"`
	Status                       string         `json:"status"`
	FormulaVersion               string         `json:"formula_version"`
	PreviousCloseQuantity        int64          `json:"previous_close_quantity"`
	BrokerOpenQuantity           int64          `json:"broker_open_quantity"`
	OpenQuantity                 int64          `json:"open_quantity"`
	OpenTotalCost                float64        `json:"open_total_cost"`
	BuyQuantity                  int64          `json:"buy_quantity"`
	BuyAmount                    float64        `json:"buy_amount"`
	BuyFee                       float64        `json:"buy_fee"`
	SellQuantity                 int64          `json:"sell_quantity"`
	SellAmount                   float64        `json:"sell_amount"`
	SellFee                      float64        `json:"sell_fee"`
	RealizedPnL                  float64        `json:"realized_pnl"`
	CloseQuantity                int64          `json:"close_quantity"`
	CloseTotalCost               float64        `json:"close_total_cost"`
	AverageCost                  float64        `json:"average_cost"`
	BrokerCloseQuantity          int64          `json:"broker_close_quantity"`
	QuantityResidual             int64          `json:"quantity_residual"`
	ClosePrice                   float64        `json:"close_price"`
	CloseMarketValue             float64        `json:"close_market_value"`
	UnrealizedPnL                float64        `json:"unrealized_pnl"`
	FeeSource                    string         `json:"fee_source"`
	OpeningSource                string         `json:"opening_source"`
	CorporateActionType          string         `json:"corporate_action_type"`
	CorporateActionFactor        float64        `json:"corporate_action_factor"`
	CorporateActionQuantityDelta int64          `json:"corporate_action_quantity_delta"`
	CorporateActionSource        string         `json:"corporate_action_source,omitempty"`
	CorporateActionContext       map[string]any `json:"corporate_action_context,omitempty"`
	QualityFlags                 []string       `json:"quality_flags,omitempty"`
	Source                       string         `json:"source"`
	CalculatedAt                 time.Time      `json:"calculated_at,omitempty"`
	CreatedAt                    time.Time      `json:"created_at,omitempty"`
	UpdatedAt                    time.Time      `json:"updated_at,omitempty"`
}

type PositionCostStateQuery struct {
	AccountID  string
	TradeDate  string
	BeforeDate string
}

func (repo *Repository) UpsertPerformanceInception(ctx context.Context, item PerformanceInception) (PerformanceInception, error) {
	if repo == nil || repo.exec == nil {
		return PerformanceInception{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	item.AccountID = strings.TrimSpace(item.AccountID)
	if item.AccountID == "" {
		return PerformanceInception{}, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	date, err := normalizeTradeDate(item.InceptionDate)
	if err != nil {
		return PerformanceInception{}, err
	}
	item.InceptionDate = date
	item.Status = firstLedgerValue(item.Status, "draft")
	item.OpeningPositionSource = firstLedgerValue(item.OpeningPositionSource, "broker_open_snapshot")
	item.CostSource = firstLedgerValue(item.CostSource, "broker_open_snapshot")
	if item.OpeningCash < 0 {
		return PerformanceInception{}, fmt.Errorf("%w: opening_cash cannot be negative", ErrInvalidLedgerInput)
	}
	switch item.Status {
	case "draft", "confirmed", "voided":
	default:
		return PerformanceInception{}, fmt.Errorf("%w: invalid inception status %q", ErrInvalidLedgerInput, item.Status)
	}
	strategyScope, err := json.Marshal(item.StrategyScope)
	if err != nil {
		return PerformanceInception{}, fmt.Errorf("marshal strategy scope: %w", err)
	}
	rawPayload, err := marshalJSONObject(item.RawPayload)
	if err != nil {
		return PerformanceInception{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return PerformanceInception{}, err
	}
	rows, err := queryer.QueryContext(ctx, upsertPerformanceInceptionSQL,
		item.AccountID, item.InceptionDate, item.Status, item.CleanStart, item.OpeningCash,
		item.OpeningPositionSource, item.CostSource, strategyScope, item.Description,
		nullString(item.ConfirmedBy), nullableTime(item.ConfirmedAt), rawPayload,
	)
	if err != nil {
		return PerformanceInception{}, fmt.Errorf("upsert performance inception %s: %w", item.AccountID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return PerformanceInception{}, sql.ErrNoRows
	}
	return scanPerformanceInception(rows)
}

func (repo *Repository) GetPerformanceInception(ctx context.Context, accountID string) (PerformanceInception, error) {
	queryer, err := repo.queryer()
	if err != nil {
		return PerformanceInception{}, err
	}
	rows, err := queryer.QueryContext(ctx, getPerformanceInceptionSQL, strings.TrimSpace(accountID))
	if err != nil {
		return PerformanceInception{}, fmt.Errorf("get performance inception %s: %w", accountID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return PerformanceInception{}, sql.ErrNoRows
	}
	return scanPerformanceInception(rows)
}

func (repo *Repository) UpsertPositionCostState(ctx context.Context, item PositionCostState) (PositionCostState, error) {
	if repo == nil || repo.exec == nil {
		return PositionCostState{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.Symbol = strings.TrimSpace(item.Symbol)
	item.Exchange = strings.ToUpper(strings.TrimSpace(item.Exchange))
	if item.AccountID == "" || item.Symbol == "" || item.Exchange == "" {
		return PositionCostState{}, fmt.Errorf("%w: account_id, symbol and exchange are required", ErrInvalidLedgerInput)
	}
	date, err := normalizeTradeDate(item.TradeDate)
	if err != nil {
		return PositionCostState{}, err
	}
	item.TradeDate = date
	item.CostBucket = firstLedgerValue(item.CostBucket, "CORE")
	item.Status = firstLedgerValue(item.Status, "calculated")
	item.FormulaVersion = firstLedgerValue(item.FormulaVersion, "performance_position_cost.v3")
	item.FeeSource = firstLedgerValue(item.FeeSource, "none")
	item.CorporateActionType = firstLedgerValue(item.CorporateActionType, "none")
	item.Source = firstLedgerValue(item.Source, "relay.performance.cost_ledger")
	qualityFlags, err := json.Marshal(item.QualityFlags)
	if err != nil {
		return PositionCostState{}, fmt.Errorf("marshal cost quality flags: %w", err)
	}
	corporateActionContext, err := marshalJSONObject(item.CorporateActionContext)
	if err != nil {
		return PositionCostState{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return PositionCostState{}, err
	}
	rows, err := queryer.QueryContext(ctx, upsertPositionCostStateSQL,
		item.AccountID, item.TradeDate, item.Symbol, item.Exchange, item.CostBucket,
		item.Status, item.FormulaVersion, item.PreviousCloseQuantity, item.BrokerOpenQuantity,
		item.OpenQuantity, item.OpenTotalCost,
		item.BuyQuantity, item.BuyAmount, item.BuyFee, item.SellQuantity, item.SellAmount,
		item.SellFee, item.RealizedPnL, item.CloseQuantity, item.CloseTotalCost,
		item.AverageCost, item.BrokerCloseQuantity, item.QuantityResidual, item.ClosePrice,
		item.CloseMarketValue, item.UnrealizedPnL, item.FeeSource, item.OpeningSource,
		item.CorporateActionType, item.CorporateActionFactor, item.CorporateActionQuantityDelta,
		item.CorporateActionSource, corporateActionContext, qualityFlags, item.Source,
		nullableTime(item.CalculatedAt),
	)
	if err != nil {
		return PositionCostState{}, fmt.Errorf("upsert position cost %s/%s/%s.%s: %w", item.AccountID, item.TradeDate, item.Symbol, item.Exchange, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return PositionCostState{}, sql.ErrNoRows
	}
	return scanPositionCostState(rows)
}

func (repo *Repository) ListPositionCostStates(ctx context.Context, query PositionCostStateQuery) ([]PositionCostState, error) {
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	accountID := strings.TrimSpace(query.AccountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	statement := listPositionCostStatesSQL
	date := strings.TrimSpace(query.TradeDate)
	if date != "" {
		date, err = normalizeTradeDate(date)
	} else {
		date, err = normalizeTradeDate(query.BeforeDate)
		statement = listLatestPositionCostStatesBeforeSQL
	}
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, statement, accountID, date)
	if err != nil {
		return nil, fmt.Errorf("list position costs %s/%s: %w", accountID, date, err)
	}
	defer rows.Close()
	items := make([]PositionCostState, 0)
	for rows.Next() {
		item, err := scanPositionCostState(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanPerformanceInception(row rowScanner) (PerformanceInception, error) {
	var item PerformanceInception
	var strategyScope []byte
	var rawPayload []byte
	var confirmedBy sql.NullString
	var confirmedAt sql.NullTime
	err := row.Scan(
		&item.AccountID, &item.InceptionDate, &item.Status, &item.CleanStart, &item.OpeningCash,
		&item.OpeningPositionSource, &item.CostSource, &strategyScope, &item.Description,
		&confirmedBy, &confirmedAt, &rawPayload, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return PerformanceInception{}, err
	}
	item.ConfirmedBy = confirmedBy.String
	if confirmedAt.Valid {
		item.ConfirmedAt = confirmedAt.Time
	}
	_ = json.Unmarshal(strategyScope, &item.StrategyScope)
	_ = json.Unmarshal(rawPayload, &item.RawPayload)
	return item, nil
}

func scanPositionCostState(row rowScanner) (PositionCostState, error) {
	var item PositionCostState
	var qualityFlags []byte
	var corporateActionContext []byte
	err := row.Scan(
		&item.AccountID, &item.TradeDate, &item.Symbol, &item.Exchange, &item.CostBucket,
		&item.Status, &item.FormulaVersion, &item.PreviousCloseQuantity, &item.BrokerOpenQuantity,
		&item.OpenQuantity, &item.OpenTotalCost,
		&item.BuyQuantity, &item.BuyAmount, &item.BuyFee, &item.SellQuantity, &item.SellAmount,
		&item.SellFee, &item.RealizedPnL, &item.CloseQuantity, &item.CloseTotalCost,
		&item.AverageCost, &item.BrokerCloseQuantity, &item.QuantityResidual, &item.ClosePrice,
		&item.CloseMarketValue, &item.UnrealizedPnL, &item.FeeSource, &item.OpeningSource,
		&item.CorporateActionType, &item.CorporateActionFactor, &item.CorporateActionQuantityDelta,
		&item.CorporateActionSource, &corporateActionContext, &qualityFlags, &item.Source,
		&item.CalculatedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return PositionCostState{}, err
	}
	_ = json.Unmarshal(corporateActionContext, &item.CorporateActionContext)
	_ = json.Unmarshal(qualityFlags, &item.QualityFlags)
	return item, nil
}

func firstLedgerValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

const upsertPerformanceInceptionSQL = `
INSERT INTO performance_account_inceptions (
    account_id, inception_date, status, clean_start, opening_cash,
    opening_position_source, cost_source, strategy_scope, description,
    confirmed_by, confirmed_at, raw_payload
) VALUES (
    $1, $2::date, $3, $4, $5,
    $6, $7, $8::jsonb, $9,
    $10, $11, $12::jsonb
)
ON CONFLICT (account_id) DO UPDATE SET
    inception_date = EXCLUDED.inception_date,
    status = EXCLUDED.status,
    clean_start = EXCLUDED.clean_start,
    opening_cash = EXCLUDED.opening_cash,
    opening_position_source = EXCLUDED.opening_position_source,
    cost_source = EXCLUDED.cost_source,
    strategy_scope = EXCLUDED.strategy_scope,
    description = EXCLUDED.description,
    confirmed_by = EXCLUDED.confirmed_by,
    confirmed_at = EXCLUDED.confirmed_at,
    raw_payload = EXCLUDED.raw_payload,
    updated_at = now()
RETURNING
    account_id, inception_date::text, status, clean_start, opening_cash,
    opening_position_source, cost_source, strategy_scope, description,
    confirmed_by, confirmed_at, raw_payload, created_at, updated_at
`

const getPerformanceInceptionSQL = `
SELECT
    account_id, inception_date::text, status, clean_start, opening_cash,
    opening_position_source, cost_source, strategy_scope, description,
    confirmed_by, confirmed_at, raw_payload, created_at, updated_at
FROM performance_account_inceptions
WHERE account_id = $1
`

const positionCostStateColumnsSQL = `
    account_id, trade_date::text, symbol, exchange, cost_bucket,
    status, formula_version, previous_close_quantity, broker_open_quantity,
    open_quantity, open_total_cost,
    buy_quantity, buy_amount, buy_fee, sell_quantity, sell_amount,
    sell_fee, realized_pnl, close_quantity, close_total_cost,
    average_cost, broker_close_quantity, quantity_residual, close_price,
    close_market_value, unrealized_pnl, fee_source, opening_source,
    corporate_action_type, corporate_action_factor, corporate_action_quantity_delta,
    corporate_action_source, corporate_action_context, quality_flags, source,
    calculated_at, created_at, updated_at
`

const upsertPositionCostStateSQL = `
INSERT INTO performance_position_cost_states (
    account_id, trade_date, symbol, exchange, cost_bucket,
    status, formula_version, previous_close_quantity, broker_open_quantity,
    open_quantity, open_total_cost,
    buy_quantity, buy_amount, buy_fee, sell_quantity, sell_amount,
    sell_fee, realized_pnl, close_quantity, close_total_cost,
    average_cost, broker_close_quantity, quantity_residual, close_price,
    close_market_value, unrealized_pnl, fee_source, opening_source,
    corporate_action_type, corporate_action_factor, corporate_action_quantity_delta,
    corporate_action_source, corporate_action_context, quality_flags, source, calculated_at
) VALUES (
    $1, $2::date, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20,
    $21, $22, $23, $24,
    $25, $26, $27, $28,
    $29, $30, $31,
    $32, $33::jsonb, $34::jsonb, $35, COALESCE($36, now())
)
ON CONFLICT (account_id, trade_date, symbol, exchange, cost_bucket) DO UPDATE SET
    status = EXCLUDED.status,
    formula_version = EXCLUDED.formula_version,
    previous_close_quantity = EXCLUDED.previous_close_quantity,
    broker_open_quantity = EXCLUDED.broker_open_quantity,
    open_quantity = EXCLUDED.open_quantity,
    open_total_cost = EXCLUDED.open_total_cost,
    buy_quantity = EXCLUDED.buy_quantity,
    buy_amount = EXCLUDED.buy_amount,
    buy_fee = EXCLUDED.buy_fee,
    sell_quantity = EXCLUDED.sell_quantity,
    sell_amount = EXCLUDED.sell_amount,
    sell_fee = EXCLUDED.sell_fee,
    realized_pnl = EXCLUDED.realized_pnl,
    close_quantity = EXCLUDED.close_quantity,
    close_total_cost = EXCLUDED.close_total_cost,
    average_cost = EXCLUDED.average_cost,
    broker_close_quantity = EXCLUDED.broker_close_quantity,
    quantity_residual = EXCLUDED.quantity_residual,
    close_price = EXCLUDED.close_price,
    close_market_value = EXCLUDED.close_market_value,
    unrealized_pnl = EXCLUDED.unrealized_pnl,
    fee_source = EXCLUDED.fee_source,
    opening_source = EXCLUDED.opening_source,
    corporate_action_type = EXCLUDED.corporate_action_type,
    corporate_action_factor = EXCLUDED.corporate_action_factor,
    corporate_action_quantity_delta = EXCLUDED.corporate_action_quantity_delta,
    corporate_action_source = EXCLUDED.corporate_action_source,
    corporate_action_context = EXCLUDED.corporate_action_context,
    quality_flags = EXCLUDED.quality_flags,
    source = EXCLUDED.source,
    calculated_at = EXCLUDED.calculated_at,
    updated_at = now()
RETURNING ` + positionCostStateColumnsSQL

const listPositionCostStatesSQL = `
SELECT ` + positionCostStateColumnsSQL + `
FROM performance_position_cost_states
WHERE account_id = $1 AND trade_date = $2::date
ORDER BY cost_bucket, symbol, exchange
`

const listLatestPositionCostStatesBeforeSQL = `
SELECT ` + positionCostStateColumnsSQL + `
FROM performance_position_cost_states
WHERE account_id = $1
  AND trade_date = (
      SELECT max(trade_date)
      FROM performance_position_cost_states
      WHERE account_id = $1 AND trade_date < $2::date
  )
ORDER BY cost_bucket, symbol, exchange
`
