package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type fuyingExecutionCashProof struct {
	Trade          recovery20261027Trade `json:"execution"`
	Date           string                `json:"trade_date"`
	Buy            bool                  `json:"buy"`
	Fee            float64               `json:"actual_cash_derived_fee"`
	Flows          []brokerCashFlowRow   `json:"cash_rows"`
	GatewayOrderID string                `json:"gateway_order_id"`
	FillCount      int                   `json:"fill_count"`
}

type fuyingETFCashProof struct {
	PCF        map[string]any              `json:"component_pcf"`
	CashPCF    map[string]any              `json:"cash_pcf"`
	Orders     []trading.Order             `json:"orders"`
	Transfers  []trading.ComponentTransfer `json:"transfers"`
	Executions []fuyingExecutionCashProof  `json:"execution_cash_evidence"`
	Commission brokerCashFlowRow           `json:"redemption_day_commission"`
	Deferred   []brokerCashFlowRow         `json:"deferred_transfer_fees"`
	Receipt    brokerCashFlowRow           `json:"actual_cash_component_receipt"`
	Summary    map[string]any              `json:"summary"`
}

// This diagnostic follows the confirmed overnight lot. It never treats the
// unrelated target-day CORE purchase as the redemption's cost.
func proveFuyingETFCash(ctx context.Context, tx *sql.Tx, flows []brokerCashFlowRow, expected map[string]int64) (fuyingETFCashProof, error) {
	var p fuyingETFCashProof
	if _, _, err := loadFuyingOvernightConfirmation(); err != nil {
		return p, err
	}
	repo := ledger.NewRepository(tx)
	var redeemedAt time.Time
	for _, scope := range []struct {
		date, id string
		side     trading.TradeSide
		business trading.BusinessType
	}{
		{"2026-11-03", fuyingOvernightBuy, trading.TradeSideBuy, trading.BusinessTypeStock},
		{"2026-11-04", fuyingOvernightRedemption, trading.TradeSideRedemption, trading.BusinessTypeETF},
	} {
		orders, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: scope.date, Symbol: "159915", Exchange: trading.ExchangeSZ, Limit: 500})
		if err != nil {
			return p, err
		}
		if len(orders) >= 500 {
			return p, errors.New("order evidence truncated")
		}
		found := 0
		for _, o := range orders {
			if o.GatewayOrderID != scope.id {
				continue
			}
			if o.AccountID != fuying13Account || o.TradeDate != scope.date || o.OrderQty != 1000000 || o.CumFilledQty != 1000000 || !o.IsTerminal || o.TradeSide != scope.side || o.BusinessType != scope.business {
				return p, errors.New("overnight terminal order changed")
			}
			p.Orders = append(p.Orders, o)
			found++
		}
		if found != 1 {
			return p, errors.New("overnight order identity missing/ambiguous")
		}
	}
	transfers, err := repo.ListComponentTransfers(ctx, trading.ComponentTransferQuery{AccountID: fuying13Account, TradeDate: "2026-11-04", Limit: 1000})
	if err != nil {
		return p, err
	}
	if len(transfers) >= 1000 {
		return p, errors.New("transfer evidence truncated")
	}
	seen := map[string]bool{}
	for _, t := range transfers {
		if t.GatewayOrderID != fuyingOvernightRedemption {
			continue
		}
		want := expected[t.ComponentSymbol]
		if t.ComponentSymbol == "159915" {
			want = 1000000
			redeemedAt = t.MatchedAt
		}
		// The parent's nominal price is not an executable price or cash receipt.
		if want <= 0 || seen[t.ComponentSymbol] || t.ComponentQty != want || t.CashSubstitution || (t.ComponentSymbol != "159915" && t.Price != 0) || t.ComponentExchange != trading.ExchangeSZ || t.TradeSide != trading.TradeSideRedemption || t.MatchedAt.IsZero() || t.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != "2026-11-04" {
			return p, errors.New("PCF/parent transfer mismatch")
		}
		seen[t.ComponentSymbol] = true
		p.Transfers = append(p.Transfers, t)
	}
	if len(expected) != 98 || len(p.Transfers) != 99 || redeemedAt.IsZero() {
		return p, errors.New("incomplete PCF/transfer coverage")
	}
	rows, err := tx.QueryContext(ctx, `SELECT symbol, sum(qty), (sum(price*qty)*100)::bigint, count(DISTINCT gateway_order_id), min(matched_at),
 bool_and(exchange='SZ' AND qty>0 AND price>0 AND matched_at IS NOT NULL AND (matched_at AT TIME ZONE 'Asia/Shanghai')::date=trade_date), trade_date::text, min(gateway_order_id), count(*)
 FROM fills WHERE account_id=$1 AND ((trade_date='2026-11-03' AND gateway_order_id=$2 AND trade_side='B') OR (trade_date='2026-11-04' AND trade_side='S')) GROUP BY symbol,trade_date ORDER BY trade_date,symbol`, fuying13Account, fuyingOvernightBuy)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var t recovery20261027Trade
		var at time.Time
		var valid bool
		var tradeDate string
		var orderID string
		var fillCount int
		if err := rows.Scan(&t.Code, &t.Qty, &t.GrossCents, &t.Orders, &at, &valid, &tradeDate, &orderID, &fillCount); err != nil {
			rows.Close()
			return p, err
		}
		buy := tradeDate == "2026-11-03" && t.Code == "159915"
		if !buy && expected[t.Code] == 0 {
			continue
		}
		want := expected[t.Code]
		date := "20261104"
		subject := "卖出成交清算资金"
		if buy {
			want = 1000000
			date = "20261103"
			subject = "买入成交清算资金"
		}
		if !valid || t.Qty != want || t.Orders != 1 || (!buy && at.Before(redeemedAt)) {
			rows.Close()
			return p, fmt.Errorf("execution allocation ambiguous %s", t.Code)
		}
		e := fuyingExecutionCashProof{Trade: t, Date: compactToISODate(date), Buy: buy, GatewayOrderID: orderID, FillCount: fillCount}
		for _, f := range flows {
			if f.TradeDate == date && f.Code == t.Code && f.Subject == subject {
				e.Flows = append(e.Flows, f)
			}
		}
		fee, _, err := recovery20261027ExecutionFee(t, e.Flows, buy)
		if err != nil {
			rows.Close()
			return p, fmt.Errorf("execution %s: %w", t.Code, err)
		}
		e.Fee = centsFloat(fee)
		p.Executions = append(p.Executions, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	if len(p.Executions) != 99 {
		return p, errors.New("execution/cash evidence incomplete")
	}
	if err := completeFuyingETFCash(&p, flows, expected); err != nil {
		return p, err
	}
	return p, nil
}

func completeFuyingETFCash(p *fuyingETFCashProof, flows []brokerCashFlowRow, expected map[string]int64) error {
	fees := map[string]bool{}
	used := map[string]bool{}
	var deferred, commission, cash int64
	for _, f := range flows {
		kind := ""
		if f.TradeDate == "20261104" && f.Summary == "深圳ETF赎回159915佣金:A620008286" {
			if p.Commission.FlowID != "" || f.InCents != 0 || f.OutCents != 12037 {
				return errors.New("redemption day commission changed")
			}
			p.Commission = f
			commission = f.OutCents
			kind = "commission"
		} else if strings.Contains(f.Summary, "159915") && strings.Contains(f.Summary, "成交日20261104") {
			prefix, suffix := "深圳ETF赎回159915(", ")过户费，成交日20261104"
			if strings.HasPrefix(f.Summary, prefix) && strings.HasSuffix(f.Summary, suffix) {
				code := strings.TrimSuffix(strings.TrimPrefix(f.Summary, prefix), suffix)
				if expected[code] <= 0 || fees[code] || f.TradeDate != "20261105" || f.InCents != 0 || f.OutCents <= 0 {
					return errors.New("deferred fee identity/direction changed")
				}
				fees[code] = true
				p.Deferred = append(p.Deferred, f)
				deferred += f.OutCents
				kind = "fee"
			} else if f.Summary == "深圳ETF申赎159915现金差额代收付，成交日20261104，A620008286" {
				if p.Receipt.FlowID != "" || f.Code != "159915" || f.Subject != "ETF现金差额划入" || f.TradeDate != "20261106" || f.InCents != 337695 || f.OutCents != 0 {
					return errors.New("cash component receipt changed")
				}
				p.Receipt = f
				cash = f.InCents
				kind = "cash"
			} else {
				return fmt.Errorf("unclassified source-linked cash %s", f.FlowID)
			}
		}
		if kind != "" {
			if f.FlowID == "" || used[f.FlowID] {
				return errors.New("reused cash identity")
			}
			used[f.FlowID] = true
		}
	}
	if len(fees) != 98 || deferred != 1304 || commission != 12037 || cash != 337695 {
		return errors.New("deferred cash coverage incomplete")
	}
	var buyGross, sellGross, buyFee, sellFee int64
	buys, sales := 0, 0
	executed := map[string]bool{}
	for _, e := range p.Executions {
		date, subject, qty := "2026-11-04", "卖出成交清算资金", expected[e.Trade.Code]
		if e.Buy {
			date, subject, qty = "2026-11-03", "买入成交清算资金", 1000000
			if e.Trade.Code != "159915" {
				return errors.New("unexpected ETF buy code")
			}
		}
		if executed[e.Trade.Code] || e.Date != date || qty <= 0 || e.Trade.Qty != qty || e.Trade.Orders != 1 {
			return errors.New("execution scope changed")
		}
		executed[e.Trade.Code] = true
		for _, f := range e.Flows {
			if f.TradeDate != strings.ReplaceAll(date, "-", "") || f.Code != e.Trade.Code || f.Subject != subject {
				return errors.New("execution cash scope changed")
			}
		}
		fee, ids, err := recovery20261027ExecutionFee(e.Trade, e.Flows, e.Buy)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == "" || used[id] {
				return errors.New("execution cash reused")
			}
			used[id] = true
		}
		if e.Buy {
			buys++
			buyGross += e.Trade.GrossCents
			buyFee += fee
		} else {
			sales++
			sellGross += e.Trade.GrossCents
			sellFee += fee
		}
	}
	if buys != 1 || sales != 98 || buyGross != 245200000 || buyFee != 12260 {
		return errors.New("overnight purchase/execution coverage changed")
	}
	totalFee := buyFee + sellFee + commission + deferred
	p.Summary = map[string]any{"source_buy_date": "2026-11-03", "redemption_date": "2026-11-04", "cash_posted_date": "2026-11-06",
		"buy_gross": centsFloat(buyGross), "component_sale_gross": centsFloat(sellGross), "buy_fee": centsFloat(buyFee), "component_sale_fees": centsFloat(sellFee), "redemption_day_commission": centsFloat(commission), "deferred_transfer_fees": centsFloat(deferred), "cash_component": centsFloat(cash), "total_actual_fees": centsFloat(totalFee),
		"whole_cycle_cash_pnl": centsFloat(sellGross + cash - buyGross - totalFee), "nov04_close_separate_carry": centsFloat(cash - deferred), "nov05_close_separate_carry": centsFloat(cash), "nov06_close_separate_carry": 0,
		"scope": "reviewed overnight cycle cash reconciliation, not account daily NAV or irrevocable final settlement", "economic_nav_publishable": false, "future_corrections_excluded": false}
	sort.Slice(p.Deferred, func(i, j int) bool { return p.Deferred[i].FlowID < p.Deferred[j].FlowID })
	return nil
}
