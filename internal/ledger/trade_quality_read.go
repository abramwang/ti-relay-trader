package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"ti-relay-trader/internal/trading"
)

type TradeQualityLedger struct {
	Orders []trading.Order
	Fills  []trading.Fill
	Fees   []OrderFeeRecord
}

// Keep the ordinary row scanners and ordering, but sort/read each ledger once.
func (repo *Repository) ReadTradeQualityLedger(ctx context.Context, accountID, dateFrom, dateTo string, limit int) (TradeQualityLedger, error) {
	result := TradeQualityLedger{}
	if strings.TrimSpace(accountID) == "" || limit <= 0 || limit > 200000 {
		return result, fmt.Errorf("%w: invalid trade quality account or row limit", ErrInvalidLedgerInput)
	}
	_, from, to, err := normalizeQueryDates("", dateFrom, dateTo)
	if err != nil || from == "" || to == "" {
		return result, fmt.Errorf("%w: explicit trade quality dates required", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return result, err
	}
	query, args := buildListOrdersSQL(trading.OrderQuery{AccountID: accountID, DateFrom: from, DateTo: to, History: true, Limit: limit + 1})
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("read trade quality orders: %w", err)
	}
	for rows.Next() {
		order, scanErr := scanOrder(rows)
		if scanErr != nil {
			rows.Close()
			return result, scanErr
		}
		result.Orders = append(result.Orders, order)
		if len(result.Orders) > limit {
			rows.Close()
			return TradeQualityLedger{}, fmt.Errorf("trade quality orders exceed %d rows", limit)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	query, args = buildListFillsSQL(trading.FillQuery{AccountID: accountID, DateFrom: from, DateTo: to, History: true, Limit: limit + 1})
	rows, err = queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("read trade quality fills: %w", err)
	}
	for rows.Next() {
		fill, scanErr := scanFill(rows)
		if scanErr != nil {
			rows.Close()
			return result, scanErr
		}
		result.Fills = append(result.Fills, fill)
		if len(result.Fills) > limit {
			rows.Close()
			return TradeQualityLedger{}, fmt.Errorf("trade quality fills exceed %d rows", limit)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	// Quality only needs effective fee identities and totals, not the raw reports.
	rows, err = queryer.QueryContext(ctx, `SELECT gateway_order_id, trade_date::text,
		total_fee, fee_as_of, fee_complete, association_complete, fee_source
		FROM order_fee_records WHERE account_id=$1 AND trade_date >= $2::date AND trade_date <= $3::date
		ORDER BY trade_date DESC, fee_as_of DESC, fee_record_id LIMIT $4`, accountID, from, to, limit+1)
	if err != nil {
		return result, fmt.Errorf("read trade quality fees: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var record OrderFeeRecord
		var asOf sql.NullTime
		if err := rows.Scan(&record.GatewayOrderID, &record.TradeDate, &record.TotalFee, &asOf, &record.FeeComplete, &record.AssociationComplete, &record.FeeSource); err != nil {
			return result, err
		}
		record.AccountID, record.FeeAsOf = accountID, asOf.Time
		result.Fees = append(result.Fees, record)
		if len(result.Fees) > limit {
			return TradeQualityLedger{}, fmt.Errorf("trade quality fees exceed %d rows", limit)
		}
	}
	return result, rows.Err()
}
