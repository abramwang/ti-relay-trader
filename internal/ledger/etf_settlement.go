package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type ETFSettlementFinalization struct {
	ETFSettlementPK            int64          `json:"etf_settlement_pk"`
	AccountID                  string         `json:"account_id"`
	SourceTradeDate            string         `json:"source_trade_date"`
	SecurityID                 string         `json:"security_id"`
	Version                    int            `json:"version"`
	IsCurrent                  bool           `json:"is_current"`
	Status                     string         `json:"status"`
	SettlementComplete         bool           `json:"settlement_complete"`
	RedemptionQuantity         int64          `json:"redemption_quantity"`
	RedemptionUnit             int64          `json:"redemption_unit"`
	BuyGrossAmount             float64        `json:"buy_gross_amount"`
	ComponentSaleGrossAmount   float64        `json:"component_sale_gross_amount"`
	ActualCashComponent        float64        `json:"actual_cash_component"`
	ActualCashSubstitution     float64        `json:"actual_cash_substitution"`
	ActualTotalFee             float64        `json:"actual_total_fee"`
	SourceCloseSettlementCarry float64        `json:"source_close_settlement_carry"`
	GrossContribution          float64        `json:"gross_contribution"`
	NetContribution            float64        `json:"net_contribution"`
	PCFTradeDate               string         `json:"pcf_trade_date"`
	PCFSchemaVersion           string         `json:"pcf_schema_version"`
	Source                     string         `json:"source"`
	ConfirmedBy                string         `json:"confirmed_by"`
	ConfirmedAt                time.Time      `json:"confirmed_at,omitempty"`
	RawPayload                 map[string]any `json:"raw_payload,omitempty"`
	CreatedAt                  time.Time      `json:"created_at"`
	UpdatedAt                  time.Time      `json:"updated_at"`
}

func (repo *Repository) ListETFSettlementFinalizations(ctx context.Context, accountID, sourceTradeDate string) ([]ETFSettlementFinalization, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	normalizedDate, err := normalizeTradeDate(sourceTradeDate)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listETFSettlementFinalizationsSQL, accountID, normalizedDate)
	if err != nil {
		return nil, fmt.Errorf("list ETF settlement finalizations %s/%s: %w", accountID, normalizedDate, err)
	}
	defer rows.Close()
	items := make([]ETFSettlementFinalization, 0)
	for rows.Next() {
		item, err := scanETFSettlementFinalization(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ETF settlement finalization: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repo *Repository) UpsertETFSettlementFinalization(ctx context.Context, item ETFSettlementFinalization) (ETFSettlementFinalization, error) {
	normalized, err := normalizeETFSettlementFinalization(item)
	if err != nil {
		return ETFSettlementFinalization{}, err
	}
	rawPayload, err := marshalJSONObject(normalized.RawPayload)
	if err != nil {
		return ETFSettlementFinalization{}, err
	}
	args := []any{
		normalized.AccountID, normalized.SourceTradeDate, normalized.SecurityID,
		normalized.Status, normalized.SettlementComplete, normalized.RedemptionQuantity,
		normalized.RedemptionUnit, normalized.BuyGrossAmount, normalized.ComponentSaleGrossAmount,
		normalized.ActualCashComponent, normalized.ActualCashSubstitution, normalized.ActualTotalFee,
		normalized.SourceCloseSettlementCarry, normalized.GrossContribution, normalized.NetContribution,
		nullString(normalized.PCFTradeDate), normalized.PCFSchemaVersion, normalized.Source,
		normalized.ConfirmedBy, nullableTime(normalized.ConfirmedAt), rawPayload,
	}

	if db, ok := repo.exec.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return ETFSettlementFinalization{}, fmt.Errorf("begin ETF settlement finalization transaction: %w", err)
		}
		defer tx.Rollback()
		if err := lockETFSettlementFinalization(ctx, tx, normalized); err != nil {
			return ETFSettlementFinalization{}, err
		}
		result, err := queryETFSettlementFinalizationUpsert(ctx, tx, args)
		if err != nil {
			return ETFSettlementFinalization{}, err
		}
		if err := tx.Commit(); err != nil {
			return ETFSettlementFinalization{}, fmt.Errorf("commit ETF settlement finalization: %w", err)
		}
		return result, nil
	}

	queryer, err := repo.queryer()
	if err != nil {
		return ETFSettlementFinalization{}, err
	}
	if tx, ok := repo.exec.(*sql.Tx); ok {
		if err := lockETFSettlementFinalization(ctx, tx, normalized); err != nil {
			return ETFSettlementFinalization{}, err
		}
	}
	return queryETFSettlementFinalizationUpsert(ctx, queryer, args)
}

func lockETFSettlementFinalization(ctx context.Context, exec Executor, item ETFSettlementFinalization) error {
	if _, err := exec.ExecContext(ctx, lockETFSettlementFinalizationSQL, item.AccountID, item.SourceTradeDate, item.SecurityID); err != nil {
		return fmt.Errorf("lock ETF settlement finalization %s/%s/%s: %w", item.AccountID, item.SourceTradeDate, item.SecurityID, err)
	}
	return nil
}

func queryETFSettlementFinalizationUpsert(ctx context.Context, queryer Queryer, args []any) (ETFSettlementFinalization, error) {
	rows, err := queryer.QueryContext(ctx, upsertETFSettlementFinalizationSQL, args...)
	if err != nil {
		return ETFSettlementFinalization{}, fmt.Errorf("upsert ETF settlement finalization %s/%s/%s: %w", args[0], args[1], args[2], err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ETFSettlementFinalization{}, errorsNoRows("upsert ETF settlement finalization")
	}
	return scanETFSettlementFinalization(rows)
}

func normalizeETFSettlementFinalization(item ETFSettlementFinalization) (ETFSettlementFinalization, error) {
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.SecurityID = strings.ToUpper(strings.TrimSpace(item.SecurityID))
	item.Status = strings.ToLower(strings.TrimSpace(item.Status))
	item.PCFSchemaVersion = strings.TrimSpace(item.PCFSchemaVersion)
	item.Source = strings.TrimSpace(item.Source)
	item.ConfirmedBy = strings.TrimSpace(item.ConfirmedBy)
	date, err := normalizeTradeDate(item.SourceTradeDate)
	if err != nil {
		return ETFSettlementFinalization{}, err
	}
	item.SourceTradeDate = date
	if item.PCFTradeDate != "" {
		item.PCFTradeDate, err = normalizeTradeDate(item.PCFTradeDate)
		if err != nil {
			return ETFSettlementFinalization{}, err
		}
	}
	if item.AccountID == "" || item.SecurityID == "" || item.Source == "" {
		return ETFSettlementFinalization{}, fmt.Errorf("%w: ETF settlement identity is incomplete", ErrInvalidLedgerInput)
	}
	if item.Status != "pending" && item.Status != "confirmed" && item.Status != "voided" {
		return ETFSettlementFinalization{}, fmt.Errorf("%w: invalid ETF settlement status %q", ErrInvalidLedgerInput, item.Status)
	}
	if item.RedemptionQuantity <= 0 || item.RedemptionUnit <= 0 || item.RedemptionQuantity%item.RedemptionUnit != 0 {
		return ETFSettlementFinalization{}, fmt.Errorf("%w: invalid ETF redemption quantity/unit", ErrInvalidLedgerInput)
	}
	if item.BuyGrossAmount < 0 || item.ComponentSaleGrossAmount < 0 || item.ActualCashSubstitution < 0 || item.ActualTotalFee < 0 {
		return ETFSettlementFinalization{}, fmt.Errorf("%w: ETF settlement amounts cannot be negative", ErrInvalidLedgerInput)
	}
	wantGross := item.ComponentSaleGrossAmount + item.ActualCashComponent + item.ActualCashSubstitution - item.BuyGrossAmount
	wantNet := wantGross - item.ActualTotalFee
	if math.Abs(item.GrossContribution-wantGross) > 0.01 || math.Abs(item.NetContribution-wantNet) > 0.01 {
		return ETFSettlementFinalization{}, fmt.Errorf("%w: ETF settlement contribution identity does not close", ErrInvalidLedgerInput)
	}
	if item.Status == "confirmed" && (!item.SettlementComplete || item.PCFTradeDate == "" || item.PCFSchemaVersion == "" || item.ConfirmedBy == "" || item.ConfirmedAt.IsZero()) {
		return ETFSettlementFinalization{}, fmt.Errorf("%w: confirmed ETF settlement requires complete PCF and audit evidence", ErrInvalidLedgerInput)
	}
	if item.RawPayload == nil {
		item.RawPayload = map[string]any{}
	}
	return item, nil
}

func scanETFSettlementFinalization(scanner interface{ Scan(...any) error }) (ETFSettlementFinalization, error) {
	var item ETFSettlementFinalization
	var pcfTradeDate sql.NullString
	var confirmedAt sql.NullTime
	var rawPayload []byte
	err := scanner.Scan(
		&item.ETFSettlementPK, &item.AccountID, &item.SourceTradeDate, &item.SecurityID,
		&item.Version, &item.IsCurrent, &item.Status, &item.SettlementComplete,
		&item.RedemptionQuantity, &item.RedemptionUnit, &item.BuyGrossAmount,
		&item.ComponentSaleGrossAmount, &item.ActualCashComponent, &item.ActualCashSubstitution,
		&item.ActualTotalFee, &item.SourceCloseSettlementCarry, &item.GrossContribution,
		&item.NetContribution, &pcfTradeDate, &item.PCFSchemaVersion, &item.Source,
		&item.ConfirmedBy, &confirmedAt, &rawPayload, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return ETFSettlementFinalization{}, err
	}
	item.PCFTradeDate = pcfTradeDate.String
	if confirmedAt.Valid {
		item.ConfirmedAt = confirmedAt.Time
	}
	if len(rawPayload) > 0 {
		if err := json.Unmarshal(rawPayload, &item.RawPayload); err != nil {
			return ETFSettlementFinalization{}, err
		}
	}
	return item, nil
}

func errorsNoRows(operation string) error {
	return fmt.Errorf("%s: %w", operation, sql.ErrNoRows)
}
