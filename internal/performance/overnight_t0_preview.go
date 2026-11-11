package performance

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

// OvernightT0Preview is a diagnostic, not a cost state, cash entitlement or NAV.
// Its fee credit reconciles the all-in model across days; it is not a cash refund.
type OvernightT0Preview struct {
	SourceDate                  string    `json:"source_date"`
	RedemptionDate              string    `json:"redemption_date"`
	Quantity                    int64     `json:"quantity"`
	SourceBuyAmount             float64   `json:"source_buy_amount"`
	SourceBuyFee                float64   `json:"source_buy_fee"`
	SourceCloseValue            float64   `json:"source_close_value"`
	SourceDayPnL                float64   `json:"source_day_pnl"`
	IOPV                        float64   `json:"iopv"`
	ReferenceTime               time.Time `json:"reference_time"`
	PriceSource                 string    `json:"price_source"`
	TargetClosePrice            float64   `json:"target_close_price"`
	EstimatedExitValue          float64   `json:"estimated_exit_value"`
	EstimatedAllInFee           float64   `json:"estimated_all_in_fee"`
	PreviouslyExpensedFeeCredit float64   `json:"previously_expensed_fee_credit"`
	RedemptionDayPnL            float64   `json:"redemption_day_pnl"`
	CombinedEstimatedPnL        float64   `json:"combined_estimated_pnl"`
	ConservationResidual        float64   `json:"conservation_residual"`
	Publishable                 bool      `json:"publishable"`
	QualityFlags                []string  `json:"quality_flags"`
}

// PreviewOvernightT0 accepts one newly acquired, fully carried and fully redeemed
// lot. Attribution and complete broker quantity evidence are checked by the review
// caller. It deliberately does not participate in normal calculation or persistence.
func (service *Service) PreviewOvernightT0(ctx context.Context, source ledger.PositionCostState, order trading.Order, transfer trading.ComponentTransfer) (OvernightT0Preview, error) {
	if !service.componentResidualPreview {
		return OvernightT0Preview{}, fmt.Errorf("overnight review requires non-publishing research mode")
	}
	if source.AccountID == "" || order.GatewayOrderID == "" || !isT0ResidualBucket(source.CostBucket) || source.CostBucket == t0ResidualPrefix || (source.Status != "calculated" && source.Status != "estimated") || source.OpenQuantity != 0 || source.SellQuantity != 0 || source.BuyQuantity <= 0 || source.BuyQuantity != source.CloseQuantity ||
		order.AccountID != source.AccountID || order.Symbol != source.Symbol || string(order.Exchange) != source.Exchange || !order.IsTerminal || order.TradeSide != trading.TradeSideRedemption || order.BusinessType != trading.BusinessTypeETF || order.CumFilledQty != source.CloseQuantity ||
		transfer.AccountID != source.AccountID || transfer.TradeDate != order.TradeDate || transfer.GatewayOrderID != order.GatewayOrderID || transfer.Symbol != source.Symbol || string(transfer.Exchange) != source.Exchange || transfer.ComponentSymbol != source.Symbol || transfer.ComponentExchange != transfer.Exchange || transfer.TradeSide != order.TradeSide || transfer.BusinessType != order.BusinessType || transfer.Qty != source.CloseQuantity || transfer.ComponentQty != transfer.Qty || transfer.MatchedAt.IsZero() {
		return OvernightT0Preview{}, fmt.Errorf("overnight source/redemption evidence mismatch")
	}
	date, target, err := parseTradeDate(order.TradeDate)
	if err != nil || date != order.TradeDate || source.TradeDate >= date || transfer.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != date || service.calendar == nil {
		return OvernightT0Preview{}, fmt.Errorf("overnight review requires dated evidence and calendar")
	}
	status, err := service.calendar.TradingDayStatus(ctx, target.AddDate(0, 0, -1).Format("20060102"))
	if err != nil || strings.ReplaceAll(source.TradeDate, "-", "") != strings.ReplaceAll(status.PreviousOrCurrentTradingDate, "-", "") {
		return OvernightT0Preview{}, fmt.Errorf("overnight review requires the immediately preceding trading day")
	}
	finalizations, err := service.store.ListETFSettlementFinalizations(ctx, source.AccountID, date)
	if err != nil || len(finalizations) != 0 {
		return OvernightT0Preview{}, fmt.Errorf("overnight estimate cannot replace existing settlement finalization: %v", err)
	}
	id := source.Symbol + "." + source.Exchange
	instruments, _ := service.loadContributionInstruments(ctx, date, []string{id})
	instrument := instruments[id]
	if !strings.EqualFold(instrument.InstrumentType, "etf") || !instrument.HasPreClose || !instrument.HasClose || instrument.Close <= 0 || math.IsNaN(instrument.Close) || math.IsInf(instrument.Close, 0) || math.IsNaN(instrument.PreClose) || math.IsInf(instrument.PreClose, 0) || math.Abs(instrument.PreClose-source.ClosePrice) > 0.0000001 {
		return OvernightT0Preview{}, fmt.Errorf("overnight previous-close bridge or ETF metadata unavailable/changed")
	}
	fills := redemptionFillsFromComponentTransfers([]trading.Order{order}, nil, []trading.ComponentTransfer{transfer})
	if len(fills) != 1 {
		return OvernightT0Preview{}, fmt.Errorf("missing unique redemption transfer")
	}
	iopv, at, priceSource, ok := service.loadRedemptionIOPV(ctx, date, fills[0])
	if !ok || at.After(transfer.MatchedAt) {
		return OvernightT0Preview{}, fmt.Errorf("missing non-forward-looking redemption IOPV")
	}
	result, err := overnightT0DailyBridge(source, iopv, service.etfT0FrictionRate)
	if err != nil {
		return result, err
	}
	result.RedemptionDate, result.IOPV, result.ReferenceTime, result.PriceSource = date, iopv, at, priceSource
	result.TargetClosePrice = instrument.Close
	return result, nil
}

func overnightT0DailyBridge(source ledger.PositionCostState, iopv, friction float64) (OvernightT0Preview, error) {
	for _, v := range []float64{source.BuyAmount, source.BuyFee, source.CloseTotalCost, source.ClosePrice, source.CloseMarketValue, iopv, friction} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return OvernightT0Preview{}, fmt.Errorf("invalid overnight amount")
		}
	}
	if source.CloseQuantity <= 0 || source.BuyAmount <= 0 || source.ClosePrice <= 0 || iopv <= 0 || friction <= 0 || friction >= 1 ||
		math.Abs(source.CloseMarketValue-float64(source.CloseQuantity)*source.ClosePrice) > 0.0001 || math.Abs(source.CloseTotalCost-source.BuyAmount-source.BuyFee) > 0.0001 {
		return OvernightT0Preview{}, fmt.Errorf("overnight acquisition or mark does not reconcile")
	}
	exit := roundMoney(float64(source.CloseQuantity) * iopv)
	fee := roundMoney(exit * friction)
	sourcePnL := roundMoney(source.CloseMarketValue - source.BuyAmount - source.BuyFee)
	// The 15bp estimate already includes both sides. Credit only the source fee
	// already expensed, never its principal or an unproved settlement payment.
	targetPnL := roundMoney(exit - source.CloseMarketValue - fee + source.BuyFee)
	combined := roundMoney(exit - source.BuyAmount - fee)
	return OvernightT0Preview{SourceDate: source.TradeDate, Quantity: source.CloseQuantity, SourceBuyAmount: source.BuyAmount, SourceBuyFee: source.BuyFee,
		SourceCloseValue: source.CloseMarketValue, SourceDayPnL: sourcePnL, EstimatedExitValue: exit, EstimatedAllInFee: fee,
		PreviouslyExpensedFeeCredit: source.BuyFee, RedemptionDayPnL: targetPnL, CombinedEstimatedPnL: combined,
		ConservationResidual: roundMoney(sourcePnL + targetPnL - combined),
		QualityFlags:         []string{"overnight_t0_diagnostic_only", "iopv_all_in_friction_estimate", "fee_credit_is_not_cash", "settlement_and_nav_unverified"}}, nil
}
