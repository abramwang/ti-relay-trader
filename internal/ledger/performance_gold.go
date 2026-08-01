package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type PerformanceNAVGold struct {
	PerformanceNAVGoldPK int64          `json:"performance_nav_gold_pk,omitempty"`
	AccountID            string         `json:"account_id"`
	TradeDate            string         `json:"trade_date"`
	Version              int            `json:"version,omitempty"`
	IsCurrent            bool           `json:"is_current"`
	Status               string         `json:"status"`
	CarriedOpenAsset     float64        `json:"carried_open_asset"`
	ObservedOpenAsset    float64        `json:"observed_open_asset"`
	OvernightAdjustment  float64        `json:"overnight_adjustment"`
	CloseAsset           float64        `json:"close_asset"`
	DailyPnL             float64        `json:"daily_pnl"`
	AssetScope           string         `json:"asset_scope"`
	Source               string         `json:"source"`
	SourceRef            string         `json:"source_ref"`
	ContentHash          string         `json:"content_hash"`
	ConfirmedBy          string         `json:"confirmed_by,omitempty"`
	ConfirmedAt          time.Time      `json:"confirmed_at,omitempty"`
	RawPayload           map[string]any `json:"raw_payload,omitempty"`
	CreatedAt            time.Time      `json:"created_at,omitempty"`
	UpdatedAt            time.Time      `json:"updated_at,omitempty"`
}

type PerformanceNAVGoldQuery struct {
	AccountID      string
	DateFrom       string
	DateTo         string
	Status         string
	Source         string
	IncludeHistory bool
}

func (repo *Repository) UpsertPerformanceNAVGold(ctx context.Context, item PerformanceNAVGold) (PerformanceNAVGold, error) {
	if repo == nil || repo.exec == nil {
		return PerformanceNAVGold{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := PreparePerformanceNAVGold(item)
	if err != nil {
		return PerformanceNAVGold{}, err
	}
	rawPayload, err := marshalJSONObject(normalized.RawPayload)
	if err != nil {
		return PerformanceNAVGold{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return PerformanceNAVGold{}, err
	}
	rows, err := queryer.QueryContext(ctx, upsertPerformanceNAVGoldSQL,
		normalized.AccountID,
		normalized.TradeDate,
		normalized.Status,
		normalized.CarriedOpenAsset,
		normalized.CloseAsset,
		normalized.DailyPnL,
		normalized.AssetScope,
		normalized.Source,
		normalized.SourceRef,
		normalized.ContentHash,
		nullString(normalized.ConfirmedBy),
		nullableTime(normalized.ConfirmedAt),
		rawPayload,
	)
	if err != nil {
		return PerformanceNAVGold{}, fmt.Errorf("upsert performance nav gold %s/%s/%s: %w", normalized.AccountID, normalized.TradeDate, normalized.Source, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return PerformanceNAVGold{}, sql.ErrNoRows
	}
	return scanPerformanceNAVGold(rows)
}

func (repo *Repository) ListPerformanceNAVGold(ctx context.Context, query PerformanceNAVGoldQuery) ([]PerformanceNAVGold, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.Status = strings.TrimSpace(query.Status)
	query.Source = strings.TrimSpace(query.Source)
	if query.AccountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	_, dateFrom, dateTo, err := normalizeQueryDates("", query.DateFrom, query.DateTo)
	if err != nil {
		return nil, err
	}
	if query.Status != "" {
		switch query.Status {
		case "draft", "confirmed", "voided":
		default:
			return nil, fmt.Errorf("%w: invalid gold status %q", ErrInvalidLedgerInput, query.Status)
		}
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listPerformanceNAVGoldSQL,
		query.AccountID,
		nullString(dateFrom),
		nullString(dateTo),
		nullString(query.Status),
		nullString(query.Source),
		query.IncludeHistory,
	)
	if err != nil {
		return nil, fmt.Errorf("list performance nav gold %s: %w", query.AccountID, err)
	}
	defer rows.Close()
	items := make([]PerformanceNAVGold, 0)
	for rows.Next() {
		item, err := scanPerformanceNAVGold(rows)
		if err != nil {
			return nil, fmt.Errorf("scan performance nav gold: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func PreparePerformanceNAVGold(item PerformanceNAVGold) (PerformanceNAVGold, error) {
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.Status = firstLedgerValue(item.Status, "draft")
	item.AssetScope = firstLedgerValue(item.AssetScope, "excluding_fund_occupancy")
	item.Source = firstLedgerValue(item.Source, "manual_user_confirmed")
	item.SourceRef = strings.TrimSpace(item.SourceRef)
	item.ConfirmedBy = strings.TrimSpace(item.ConfirmedBy)
	if item.AccountID == "" || item.SourceRef == "" {
		return PerformanceNAVGold{}, fmt.Errorf("%w: account_id and source_ref are required", ErrInvalidLedgerInput)
	}
	tradeDate, err := normalizeTradeDate(item.TradeDate)
	if err != nil || tradeDate == "" {
		if err != nil {
			return PerformanceNAVGold{}, err
		}
		return PerformanceNAVGold{}, fmt.Errorf("%w: trade_date is required", ErrInvalidLedgerInput)
	}
	item.TradeDate = tradeDate
	switch item.Status {
	case "draft", "confirmed", "voided":
	default:
		return PerformanceNAVGold{}, fmt.Errorf("%w: invalid gold status %q", ErrInvalidLedgerInput, item.Status)
	}
	for name, value := range map[string]float64{
		"carried_open_asset": item.CarriedOpenAsset,
		"close_asset":        item.CloseAsset,
		"daily_pnl":          item.DailyPnL,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return PerformanceNAVGold{}, fmt.Errorf("%w: %s must be finite", ErrInvalidLedgerInput, name)
		}
	}
	item.CarriedOpenAsset = roundPerformanceGoldDecimal(item.CarriedOpenAsset)
	item.CloseAsset = roundPerformanceGoldDecimal(item.CloseAsset)
	item.DailyPnL = roundPerformanceGoldDecimal(item.DailyPnL)
	item.ObservedOpenAsset = item.CloseAsset - item.DailyPnL
	item.OvernightAdjustment = item.ObservedOpenAsset - item.CarriedOpenAsset
	item.ObservedOpenAsset = roundPerformanceGoldDecimal(item.ObservedOpenAsset)
	item.OvernightAdjustment = roundPerformanceGoldDecimal(item.OvernightAdjustment)
	if item.CarriedOpenAsset < 0 || item.CloseAsset < 0 || item.ObservedOpenAsset < 0 {
		return PerformanceNAVGold{}, fmt.Errorf("%w: gold asset values cannot be negative", ErrInvalidLedgerInput)
	}
	if item.Status == "confirmed" && (item.ConfirmedBy == "" || item.ConfirmedAt.IsZero()) {
		return PerformanceNAVGold{}, fmt.Errorf("%w: confirmed_by and confirmed_at are required for confirmed gold", ErrInvalidLedgerInput)
	}
	content := struct {
		Status           string  `json:"status"`
		CarriedOpenAsset float64 `json:"carried_open_asset"`
		CloseAsset       float64 `json:"close_asset"`
		DailyPnL         float64 `json:"daily_pnl"`
		AssetScope       string  `json:"asset_scope"`
		SourceRef        string  `json:"source_ref"`
	}{
		Status:           item.Status,
		CarriedOpenAsset: item.CarriedOpenAsset,
		CloseAsset:       item.CloseAsset,
		DailyPnL:         item.DailyPnL,
		AssetScope:       item.AssetScope,
		SourceRef:        item.SourceRef,
	}
	body, err := json.Marshal(content)
	if err != nil {
		return PerformanceNAVGold{}, fmt.Errorf("%w: marshal gold hash input: %w", ErrInvalidLedgerInput, err)
	}
	digest := sha256.Sum256(body)
	item.ContentHash = hex.EncodeToString(digest[:])
	return item, nil
}

func roundPerformanceGoldDecimal(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func scanPerformanceNAVGold(row rowScanner) (PerformanceNAVGold, error) {
	var item PerformanceNAVGold
	var confirmedBy sql.NullString
	var confirmedAt sql.NullTime
	var rawPayload []byte
	err := row.Scan(
		&item.PerformanceNAVGoldPK,
		&item.AccountID,
		&item.TradeDate,
		&item.Version,
		&item.IsCurrent,
		&item.Status,
		&item.CarriedOpenAsset,
		&item.ObservedOpenAsset,
		&item.OvernightAdjustment,
		&item.CloseAsset,
		&item.DailyPnL,
		&item.AssetScope,
		&item.Source,
		&item.SourceRef,
		&item.ContentHash,
		&confirmedBy,
		&confirmedAt,
		&rawPayload,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return PerformanceNAVGold{}, err
	}
	item.ConfirmedBy = confirmedBy.String
	if confirmedAt.Valid {
		item.ConfirmedAt = confirmedAt.Time
	}
	_ = json.Unmarshal(rawPayload, &item.RawPayload)
	return item, nil
}

const performanceNAVGoldColumnsSQL = `
    performance_nav_gold_pk,
    account_id,
    trade_date::text,
    version,
    is_current,
    status,
    carried_open_asset,
    observed_open_asset,
    overnight_adjustment,
    close_asset,
    daily_pnl,
    asset_scope,
    source,
    source_ref,
    content_hash,
    confirmed_by,
    confirmed_at,
    raw_payload,
    created_at,
    updated_at
`

const upsertPerformanceNAVGoldSQL = `
WITH existing_current AS (
    SELECT ` + performanceNAVGoldColumnsSQL + `
    FROM performance_nav_gold_versions
    WHERE account_id = $1
        AND trade_date = $2::date
        AND source = $8
        AND content_hash = $10
        AND is_current
),
retire_current AS (
    UPDATE performance_nav_gold_versions
    SET is_current = FALSE, updated_at = now()
    WHERE account_id = $1
        AND trade_date = $2::date
        AND source = $8
        AND is_current
        AND NOT EXISTS (SELECT 1 FROM existing_current)
    RETURNING 1
),
next_version AS (
    SELECT COALESCE(max(version), 0) + 1 AS version
    FROM performance_nav_gold_versions
    WHERE account_id = $1
        AND trade_date = $2::date
        AND source = $8
),
retire_count AS (
    SELECT count(*) FROM retire_current
),
inserted AS (
    INSERT INTO performance_nav_gold_versions (
        account_id,
        trade_date,
        version,
        is_current,
        status,
        carried_open_asset,
        close_asset,
        daily_pnl,
        asset_scope,
        source,
        source_ref,
        content_hash,
        confirmed_by,
        confirmed_at,
        raw_payload
    )
    SELECT
        $1,
        $2::date,
        next_version.version,
        TRUE,
        $3,
        $4,
        $5,
        $6,
        $7,
        $8,
        $9,
        $10,
        $11,
        $12,
        $13::jsonb
    FROM next_version, retire_count
    WHERE NOT EXISTS (SELECT 1 FROM existing_current)
    RETURNING ` + performanceNAVGoldColumnsSQL + `
)
SELECT * FROM inserted
UNION ALL
SELECT * FROM existing_current
LIMIT 1
`

const listPerformanceNAVGoldSQL = `
SELECT ` + performanceNAVGoldColumnsSQL + `
FROM performance_nav_gold_versions
WHERE account_id = $1
    AND ($2::date IS NULL OR trade_date >= $2::date)
    AND ($3::date IS NULL OR trade_date <= $3::date)
    AND ($4::text IS NULL OR status = $4)
    AND ($5::text IS NULL OR source = $5)
    AND ($6 OR is_current)
ORDER BY trade_date, source, version DESC
`
