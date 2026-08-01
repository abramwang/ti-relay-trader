package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type OrderFeeRecord struct {
	OrderFeeRecordPK    int64          `json:"order_fee_record_pk,omitempty"`
	AccountID           string         `json:"account_id"`
	FeeRecordID         string         `json:"fee_record_id"`
	TradeDate           string         `json:"trade_date"`
	RecordScope         string         `json:"record_scope"`
	GatewayOrderID      string         `json:"gateway_order_id,omitempty"`
	OrderID             int64          `json:"order_id,omitempty"`
	OrderStreamID       string         `json:"order_stream_id,omitempty"`
	FillID              string         `json:"fill_id,omitempty"`
	Symbol              string         `json:"symbol,omitempty"`
	Exchange            string         `json:"exchange,omitempty"`
	TradeSide           string         `json:"trade_side,omitempty"`
	BusinessType        string         `json:"business_type,omitempty"`
	OrderAmount         float64        `json:"order_amount"`
	Turnover            float64        `json:"turnover"`
	Commission          float64        `json:"commission"`
	StampTax            float64        `json:"stamp_tax"`
	TransferFee         float64        `json:"transfer_fee"`
	HandlingFee         float64        `json:"handling_fee"`
	RegulatoryFee       float64        `json:"regulatory_fee"`
	SettlementFee       float64        `json:"settlement_fee"`
	OtherFee            float64        `json:"other_fee"`
	TotalFee            float64        `json:"total_fee"`
	Currency            string         `json:"currency"`
	FeeComplete         bool           `json:"fee_complete"`
	FeeSource           string         `json:"fee_source"`
	FeeAsOf             time.Time      `json:"fee_as_of"`
	SettledAt           time.Time      `json:"settled_at,omitempty"`
	AssociationComplete bool           `json:"association_complete"`
	AdapterContext      map[string]any `json:"adapter_context,omitempty"`
	OriginMessageID     string         `json:"origin_message_id,omitempty"`
	RequestID           string         `json:"request_id,omitempty"`
	CorrelationID       string         `json:"correlation_id,omitempty"`
	IdempotencyKey      string         `json:"idempotency_key,omitempty"`
	StreamKey           string         `json:"stream_key,omitempty"`
	StreamID            string         `json:"stream_id,omitempty"`
	RawPayload          map[string]any `json:"raw_payload,omitempty"`
	CreatedAt           time.Time      `json:"created_at,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at,omitempty"`
}

type OrderFeeRecordQuery struct {
	AccountID      string `json:"account_id"`
	TradeDate      string `json:"trade_date,omitempty"`
	DateFrom       string `json:"date_from,omitempty"`
	DateTo         string `json:"date_to,omitempty"`
	GatewayOrderID string `json:"gateway_order_id,omitempty"`
	FeeComplete    *bool  `json:"fee_complete,omitempty"`
	Limit          int    `json:"limit"`
	Cursor         string `json:"cursor,omitempty"`
}

func (repo *Repository) UpsertOrderFeeRecord(ctx context.Context, record OrderFeeRecord) error {
	if repo == nil || repo.exec == nil {
		return fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeOrderFeeRecord(record)
	if err != nil {
		return err
	}
	adapterContext, err := marshalJSONObject(normalized.AdapterContext)
	if err != nil {
		return err
	}
	rawPayload, err := marshalJSONObject(normalized.RawPayload)
	if err != nil {
		return err
	}
	_, err = repo.exec.ExecContext(ctx, upsertOrderFeeRecordSQL,
		normalized.AccountID,
		normalized.FeeRecordID,
		normalized.TradeDate,
		normalized.RecordScope,
		normalized.GatewayOrderID,
		normalized.OrderID,
		normalized.OrderStreamID,
		normalized.FillID,
		normalized.Symbol,
		normalized.Exchange,
		normalized.TradeSide,
		normalized.BusinessType,
		normalized.OrderAmount,
		normalized.Turnover,
		normalized.Commission,
		normalized.StampTax,
		normalized.TransferFee,
		normalized.HandlingFee,
		normalized.RegulatoryFee,
		normalized.SettlementFee,
		normalized.OtherFee,
		normalized.TotalFee,
		normalized.Currency,
		normalized.FeeComplete,
		normalized.FeeSource,
		normalized.FeeAsOf,
		nullableTime(normalized.SettledAt),
		normalized.AssociationComplete,
		adapterContext,
		normalized.OriginMessageID,
		normalized.RequestID,
		normalized.CorrelationID,
		normalized.IdempotencyKey,
		normalized.StreamKey,
		normalized.StreamID,
		rawPayload,
	)
	if err != nil {
		return fmt.Errorf("upsert order fee record %s/%s: %w", normalized.AccountID, normalized.FeeRecordID, err)
	}
	return nil
}

func (repo *Repository) ListOrderFeeRecords(ctx context.Context, query OrderFeeRecordQuery) ([]OrderFeeRecord, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.GatewayOrderID = strings.TrimSpace(query.GatewayOrderID)
	if query.AccountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	tradeDate, dateFrom, dateTo, err := normalizeQueryDates(query.TradeDate, query.DateFrom, query.DateTo)
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	offset, err := feeQueryOffset(query.Cursor)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listOrderFeeRecordsSQL,
		query.AccountID,
		nullString(tradeDate),
		nullString(dateFrom),
		nullString(dateTo),
		nullString(query.GatewayOrderID),
		nullableBool(query.FeeComplete),
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list order fee records %s: %w", query.AccountID, err)
	}
	defer rows.Close()
	items := make([]OrderFeeRecord, 0)
	for rows.Next() {
		item, err := scanOrderFeeRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order fee record: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeOrderFeeRecord(record OrderFeeRecord) (OrderFeeRecord, error) {
	record.AccountID = strings.TrimSpace(record.AccountID)
	record.FeeRecordID = strings.TrimSpace(record.FeeRecordID)
	record.RecordScope = firstLedgerValue(record.RecordScope, "order")
	record.GatewayOrderID = strings.TrimSpace(record.GatewayOrderID)
	record.OrderStreamID = strings.TrimSpace(record.OrderStreamID)
	record.FillID = strings.TrimSpace(record.FillID)
	record.Symbol = strings.TrimSpace(record.Symbol)
	record.Exchange = strings.ToUpper(strings.TrimSpace(record.Exchange))
	record.TradeSide = strings.TrimSpace(record.TradeSide)
	record.BusinessType = strings.TrimSpace(record.BusinessType)
	record.Currency = firstLedgerValue(record.Currency, "CNY")
	record.FeeSource = firstLedgerValue(record.FeeSource, "unavailable")
	if record.AccountID == "" || record.FeeRecordID == "" {
		return OrderFeeRecord{}, fmt.Errorf("%w: account_id and fee_record_id are required", ErrInvalidLedgerInput)
	}
	tradeDate, err := normalizeTradeDate(record.TradeDate)
	if err != nil || tradeDate == "" {
		if err != nil {
			return OrderFeeRecord{}, err
		}
		return OrderFeeRecord{}, fmt.Errorf("%w: trade_date is required", ErrInvalidLedgerInput)
	}
	record.TradeDate = tradeDate
	if record.RecordScope != "order" {
		return OrderFeeRecord{}, fmt.Errorf("%w: record_scope must be order", ErrInvalidLedgerInput)
	}
	if record.FeeAsOf.IsZero() {
		return OrderFeeRecord{}, fmt.Errorf("%w: fee_as_of is required", ErrInvalidLedgerInput)
	}
	if record.AssociationComplete && record.GatewayOrderID == "" {
		return OrderFeeRecord{}, fmt.Errorf("%w: gateway_order_id is required for an associated fee", ErrInvalidLedgerInput)
	}
	values := []*float64{
		&record.OrderAmount, &record.Turnover, &record.Commission, &record.StampTax,
		&record.TransferFee, &record.HandlingFee, &record.RegulatoryFee,
		&record.SettlementFee, &record.OtherFee, &record.TotalFee,
	}
	for _, value := range values {
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return OrderFeeRecord{}, fmt.Errorf("%w: fee amounts must be finite and non-negative", ErrInvalidLedgerInput)
		}
		*value = math.Round(*value*1_000_000) / 1_000_000
	}
	return record, nil
}

func scanOrderFeeRecord(row rowScanner) (OrderFeeRecord, error) {
	var item OrderFeeRecord
	var settledAt sql.NullTime
	var adapterContext []byte
	var rawPayload []byte
	err := row.Scan(
		&item.OrderFeeRecordPK,
		&item.AccountID,
		&item.FeeRecordID,
		&item.TradeDate,
		&item.RecordScope,
		&item.GatewayOrderID,
		&item.OrderID,
		&item.OrderStreamID,
		&item.FillID,
		&item.Symbol,
		&item.Exchange,
		&item.TradeSide,
		&item.BusinessType,
		&item.OrderAmount,
		&item.Turnover,
		&item.Commission,
		&item.StampTax,
		&item.TransferFee,
		&item.HandlingFee,
		&item.RegulatoryFee,
		&item.SettlementFee,
		&item.OtherFee,
		&item.TotalFee,
		&item.Currency,
		&item.FeeComplete,
		&item.FeeSource,
		&item.FeeAsOf,
		&settledAt,
		&item.AssociationComplete,
		&adapterContext,
		&item.OriginMessageID,
		&item.RequestID,
		&item.CorrelationID,
		&item.IdempotencyKey,
		&item.StreamKey,
		&item.StreamID,
		&rawPayload,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return OrderFeeRecord{}, err
	}
	if settledAt.Valid {
		item.SettledAt = settledAt.Time
	}
	_ = json.Unmarshal(adapterContext, &item.AdapterContext)
	_ = json.Unmarshal(rawPayload, &item.RawPayload)
	return item, nil
}

func feeQueryOffset(cursor string) (int, error) {
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

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

const orderFeeRecordColumnsSQL = `
    order_fee_record_pk,
    account_id,
    fee_record_id,
    trade_date::text,
    record_scope,
    gateway_order_id,
    order_id,
    order_stream_id,
    fill_id,
    symbol,
    exchange,
    trade_side,
    business_type,
    order_amount,
    turnover,
    commission,
    stamp_tax,
    transfer_fee,
    handling_fee,
    regulatory_fee,
    settlement_fee,
    other_fee,
    total_fee,
    currency,
    fee_complete,
    fee_source,
    fee_as_of,
    settled_at,
    association_complete,
    adapter_context,
    origin_message_id,
    request_id,
    correlation_id,
    idempotency_key,
    stream_key,
    stream_id,
    raw_payload,
    created_at,
    updated_at
`

const upsertOrderFeeRecordSQL = `
WITH upserted AS (
    INSERT INTO order_fee_records (
        account_id, fee_record_id, trade_date, record_scope, gateway_order_id,
        order_id, order_stream_id, fill_id, symbol, exchange, trade_side,
        business_type, order_amount, turnover, commission, stamp_tax,
        transfer_fee, handling_fee, regulatory_fee, settlement_fee, other_fee,
        total_fee, currency, fee_complete, fee_source, fee_as_of, settled_at,
        association_complete, adapter_context, origin_message_id, request_id,
        correlation_id, idempotency_key, stream_key, stream_id, raw_payload
    ) VALUES (
        $1, $2, $3::date, $4, $5,
        $6, $7, $8, $9, $10, $11,
        $12, $13, $14, $15, $16,
        $17, $18, $19, $20, $21,
        $22, $23, $24, $25, $26, $27,
        $28, $29::jsonb, $30, $31,
        $32, $33, $34, $35, $36::jsonb
    )
    ON CONFLICT (account_id, fee_record_id) DO UPDATE SET
        trade_date = EXCLUDED.trade_date,
        record_scope = EXCLUDED.record_scope,
        gateway_order_id = EXCLUDED.gateway_order_id,
        order_id = EXCLUDED.order_id,
        order_stream_id = EXCLUDED.order_stream_id,
        fill_id = EXCLUDED.fill_id,
        symbol = EXCLUDED.symbol,
        exchange = EXCLUDED.exchange,
        trade_side = EXCLUDED.trade_side,
        business_type = EXCLUDED.business_type,
        order_amount = EXCLUDED.order_amount,
        turnover = EXCLUDED.turnover,
        commission = EXCLUDED.commission,
        stamp_tax = EXCLUDED.stamp_tax,
        transfer_fee = EXCLUDED.transfer_fee,
        handling_fee = EXCLUDED.handling_fee,
        regulatory_fee = EXCLUDED.regulatory_fee,
        settlement_fee = EXCLUDED.settlement_fee,
        other_fee = EXCLUDED.other_fee,
        total_fee = EXCLUDED.total_fee,
        currency = EXCLUDED.currency,
        fee_complete = EXCLUDED.fee_complete,
        fee_source = EXCLUDED.fee_source,
        fee_as_of = EXCLUDED.fee_as_of,
        settled_at = EXCLUDED.settled_at,
        association_complete = EXCLUDED.association_complete,
        adapter_context = EXCLUDED.adapter_context,
        origin_message_id = EXCLUDED.origin_message_id,
        request_id = EXCLUDED.request_id,
        correlation_id = EXCLUDED.correlation_id,
        idempotency_key = EXCLUDED.idempotency_key,
        stream_key = EXCLUDED.stream_key,
        stream_id = EXCLUDED.stream_id,
        raw_payload = EXCLUDED.raw_payload,
        updated_at = now()
    WHERE (
            EXCLUDED.fee_as_of > order_fee_records.fee_as_of
            AND (
                NOT (
                    order_fee_records.fee_complete
                    AND order_fee_records.association_complete
                    AND order_fee_records.fee_source <> 'unavailable'
                )
                OR (
                    EXCLUDED.fee_complete
                    AND EXCLUDED.association_complete
                    AND EXCLUDED.fee_source <> 'unavailable'
                )
            )
        )
        OR (
            EXCLUDED.fee_as_of = order_fee_records.fee_as_of
            AND EXCLUDED.fee_complete
            AND EXCLUDED.association_complete
            AND EXCLUDED.fee_source <> 'unavailable'
            AND NOT (
                order_fee_records.fee_complete
                AND order_fee_records.association_complete
                AND order_fee_records.fee_source <> 'unavailable'
            )
        )
    RETURNING *
), effective AS (
    SELECT * FROM upserted
    UNION ALL
    SELECT existing.*
    FROM order_fee_records AS existing
    WHERE existing.account_id = $1
        AND existing.fee_record_id = $2
        AND NOT EXISTS (SELECT 1 FROM upserted)
)
UPDATE orders
SET
    fee = effective.total_fee,
    adapter_context = COALESCE(orders.adapter_context, '{}'::jsonb) || jsonb_build_object(
        'fee', effective.total_fee,
        'fee_complete', effective.fee_complete,
        'fee_source', effective.fee_source,
        'fee_as_of', effective.fee_as_of,
        'fee_record_id', effective.fee_record_id,
        'fee_record_scope', effective.record_scope
    ),
    updated_at = now()
FROM effective
WHERE orders.account_id = effective.account_id
    AND orders.trade_date = effective.trade_date
    AND orders.gateway_order_id = effective.gateway_order_id
    AND effective.fee_complete
    AND effective.association_complete
    AND effective.fee_source <> 'unavailable'
`

const listOrderFeeRecordsSQL = `
SELECT ` + orderFeeRecordColumnsSQL + `
FROM order_fee_records
WHERE account_id = $1
    AND ($2::date IS NULL OR trade_date = $2::date)
    AND ($3::date IS NULL OR trade_date >= $3::date)
    AND ($4::date IS NULL OR trade_date <= $4::date)
    AND ($5::text IS NULL OR gateway_order_id = $5)
    AND ($6::boolean IS NULL OR fee_complete = $6)
ORDER BY trade_date DESC, fee_as_of DESC, fee_record_id
LIMIT $7 OFFSET $8
`
