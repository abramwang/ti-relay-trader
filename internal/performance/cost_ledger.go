package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

const positionCostFormulaVersion = "performance_position_cost.v3"

type CostLedgerOptions struct {
	Persist bool `json:"persist"`
}

type CostLedgerSummary struct {
	Securities            int     `json:"securities"`
	CalculatedItems       int     `json:"calculated_items"`
	EstimatedItems        int     `json:"estimated_items"`
	BlockedItems          int     `json:"blocked_items"`
	QuantityBreaks        int     `json:"quantity_breaks"`
	CorporateActions      int     `json:"corporate_actions"`
	QuantityAdjustments   int     `json:"quantity_adjustments"`
	CorporateActionBreaks int     `json:"corporate_action_breaks"`
	T0CostBuckets         int     `json:"t0_cost_buckets"`
	T0BlockedBuckets      int     `json:"t0_blocked_buckets"`
	T0BuyQuantity         int64   `json:"t0_buy_quantity"`
	T0RedemptionQuantity  int64   `json:"t0_redemption_quantity"`
	T0BuyAmount           float64 `json:"t0_buy_amount"`
	MissingFeeItems       int     `json:"missing_fee_items"`
	OpenQuantity          int64   `json:"open_quantity"`
	OpenTotalCost         float64 `json:"open_total_cost"`
	BuyQuantity           int64   `json:"buy_quantity"`
	BuyAmount             float64 `json:"buy_amount"`
	BuyFee                float64 `json:"buy_fee"`
	SellQuantity          int64   `json:"sell_quantity"`
	SellAmount            float64 `json:"sell_amount"`
	SellFee               float64 `json:"sell_fee"`
	RealizedPnL           float64 `json:"realized_pnl"`
	CloseQuantity         int64   `json:"close_quantity"`
	CloseTotalCost        float64 `json:"close_total_cost"`
	CloseMarketValue      float64 `json:"close_market_value"`
	UnrealizedPnL         float64 `json:"unrealized_pnl"`
	AttributionResidual   float64 `json:"attribution_residual"`
}

type CostLedgerResult struct {
	AccountID      string                      `json:"account_id"`
	TradeDate      string                      `json:"trade_date"`
	Status         string                      `json:"status"`
	FormulaVersion string                      `json:"formula_version"`
	Persisted      bool                        `json:"persisted"`
	Inception      ledger.PerformanceInception `json:"inception"`
	OpeningSource  string                      `json:"opening_source"`
	Summary        CostLedgerSummary           `json:"summary"`
	Positions      []ledger.PositionCostState  `json:"positions"`
	QualityFlags   []string                    `json:"quality_flags,omitempty"`
	CalculatedAt   time.Time                   `json:"calculated_at"`
}

type costWorkingState struct {
	item     ledger.PositionCostState
	quantity int64
	cost     float64
	flags    []string
}

func (service *Service) GetPerformanceInception(ctx context.Context, accountID string) (ledger.PerformanceInception, error) {
	return service.store.GetPerformanceInception(ctx, strings.TrimSpace(accountID))
}

func (service *Service) UpsertPerformanceInception(ctx context.Context, item ledger.PerformanceInception) (ledger.PerformanceInception, error) {
	return service.store.UpsertPerformanceInception(ctx, item)
}

func (service *Service) CalculateCostLedger(ctx context.Context, accountID, tradeDate string, options CostLedgerOptions) (CostLedgerResult, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return CostLedgerResult{}, errors.New("account_id is required")
	}
	normalizedDate, _, err := parseTradeDate(tradeDate)
	if err != nil {
		return CostLedgerResult{}, err
	}
	result := CostLedgerResult{
		AccountID:      accountID,
		TradeDate:      normalizedDate,
		Status:         "calculated",
		FormulaVersion: positionCostFormulaVersion,
		CalculatedAt:   service.now(),
	}
	inception, err := service.store.GetPerformanceInception(ctx, accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			result.Status = "blocked"
			result.QualityFlags = appendUnique(result.QualityFlags, "missing_performance_inception")
			return result, nil
		}
		return CostLedgerResult{}, err
	}
	result.Inception = inception
	if inception.Status != "confirmed" {
		result.Status = "blocked"
		result.QualityFlags = appendUnique(result.QualityFlags, "performance_inception_not_confirmed")
	}
	if normalizedDate < inception.InceptionDate {
		return CostLedgerResult{}, fmt.Errorf("trade_date %s is before performance inception %s", normalizedDate, inception.InceptionDate)
	}

	working := make(map[string]*costWorkingState)
	openPositions, err := service.listContributionPositions(ctx, accountID, normalizedDate, "open")
	if err != nil {
		return CostLedgerResult{}, err
	}
	openBySecurity := make(map[string]trading.Position, len(openPositions))
	for _, position := range openPositions {
		openBySecurity[contributionSecurityID(position.Symbol, position.Exchange)] = position
	}
	if normalizedDate == inception.InceptionDate {
		result.OpeningSource = inception.OpeningPositionSource
		for _, position := range openPositions {
			key := contributionSecurityID(position.Symbol, position.Exchange)
			state := newCostWorkingState(accountID, normalizedDate, position.Symbol, string(position.Exchange), result.OpeningSource)
			state.quantity = position.Quantity
			state.cost = trustedBrokerPositionCost(position)
			state.item.OpenQuantity = position.Quantity
			state.item.BrokerOpenQuantity = position.Quantity
			state.item.OpenTotalCost = state.cost
			if position.Quantity > 0 && state.cost <= 0 {
				state.flags = appendUnique(state.flags, "missing_trusted_open_cost")
				state.item.Status = "blocked"
			}
			working[key] = state
		}
	} else {
		result.OpeningSource = "relay_previous_close_cost"
		previous, err := service.store.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{
			AccountID:  accountID,
			BeforeDate: normalizedDate,
		})
		if err != nil {
			return CostLedgerResult{}, err
		}
		if len(previous) == 0 {
			openPositions, openErr := service.listContributionPositions(ctx, accountID, normalizedDate, "open")
			if openErr != nil {
				return CostLedgerResult{}, openErr
			}
			if !inception.CleanStart || len(openPositions) > 0 {
				result.Status = "blocked"
				result.QualityFlags = appendUnique(result.QualityFlags, "missing_previous_cost_state")
				return result, nil
			}
			result.QualityFlags = appendUnique(result.QualityFlags, "empty_clean_start_continuation")
		}
		for _, previousState := range previous {
			if previousState.CostBucket != "CORE" || previousState.CloseQuantity == 0 {
				continue
			}
			key := strings.TrimSpace(previousState.Symbol) + "." + strings.TrimSpace(previousState.Exchange)
			state := newCostWorkingState(accountID, normalizedDate, previousState.Symbol, previousState.Exchange, result.OpeningSource)
			state.quantity = previousState.CloseQuantity
			state.cost = previousState.CloseTotalCost
			state.item.OpenQuantity = previousState.CloseQuantity
			state.item.OpenTotalCost = previousState.CloseTotalCost
			if previousState.Status == "blocked" {
				state.flags = appendUnique(state.flags, "previous_cost_state_blocked")
				state.item.Status = "blocked"
			}
			working[key] = state
		}
	}

	orders, err := service.listContributionOrders(ctx, accountID, normalizedDate)
	if err != nil {
		return CostLedgerResult{}, err
	}
	fills, err := service.listTradeDateFills(ctx, accountID, normalizedDate)
	if err != nil {
		return CostLedgerResult{}, err
	}
	fills = dedupeContributionFills(fills)
	redemptionTransferFills, transferErr := service.listRedemptionTransferFills(ctx, accountID, normalizedDate, orders, fills)
	if transferErr != nil {
		result.QualityFlags = appendUnique(result.QualityFlags, "etf_redemption_transfer_ledger_unavailable")
	} else if len(redemptionTransferFills) > 0 {
		fills = append(fills, redemptionTransferFills...)
		fills = dedupeContributionFills(fills)
		result.QualityFlags = appendUnique(result.QualityFlags, "etf_redemption_from_transfer_ledger")
	}
	ordinaryFills := fills[:0]
	for _, fill := range fills {
		if strings.TrimSpace(fill.Symbol) == reverseRepoSymbol && fill.Exchange == trading.ExchangeSH {
			result.QualityFlags = appendUnique(result.QualityFlags, "reverse_repo_excluded_from_position_cost")
			continue
		}
		ordinaryFills = append(ordinaryFills, fill)
	}
	fills = ordinaryFills
	sort.SliceStable(fills, func(i, j int) bool {
		return fills[i].MatchedAt.Before(fills[j].MatchedAt)
	})
	securityIDs := make([]string, 0)
	seenSecurity := make(map[string]bool)
	for key := range working {
		securityIDs = append(securityIDs, key)
		seenSecurity[key] = true
	}
	for key := range openBySecurity {
		if !seenSecurity[key] {
			securityIDs = append(securityIDs, key)
			seenSecurity[key] = true
		}
	}
	for _, fill := range fills {
		key := contributionSecurityID(fill.Symbol, fill.Exchange)
		if !seenSecurity[key] {
			securityIDs = append(securityIDs, key)
			seenSecurity[key] = true
		}
		if working[key] == nil {
			working[key] = newCostWorkingState(accountID, normalizedDate, fill.Symbol, string(fill.Exchange), result.OpeningSource)
		}
	}

	closePositions, err := service.listContributionPositions(ctx, accountID, normalizedDate, "close")
	if err != nil {
		return CostLedgerResult{}, err
	}
	closePositions, closePositionFlags := service.supplementClosePositionsFromNextOpen(ctx, accountID, normalizedDate, closePositions)
	result.QualityFlags = appendUnique(result.QualityFlags, closePositionFlags...)
	closeBySecurity := make(map[string]trading.Position, len(closePositions))
	for _, position := range closePositions {
		key := contributionSecurityID(position.Symbol, position.Exchange)
		closeBySecurity[key] = position
		if !seenSecurity[key] {
			securityIDs = append(securityIDs, key)
			seenSecurity[key] = true
		}
		if working[key] == nil {
			working[key] = newCostWorkingState(accountID, normalizedDate, position.Symbol, string(position.Exchange), result.OpeningSource)
		}
	}
	if _, err := service.store.GetAssetPositionObservation(ctx, accountID, normalizedDate, "close"); err != nil {
		result.Status = "blocked"
		result.QualityFlags = appendUnique(result.QualityFlags, "missing_close_position_snapshot")
	}

	instruments, marketFlags := service.loadContributionInstruments(ctx, normalizedDate, securityIDs)
	result.QualityFlags = appendUnique(result.QualityFlags, marketFlags...)
	redemptionUnits, pcfFlags := service.loadPCFRedemptionUnits(ctx, normalizedDate, fills)
	result.QualityFlags = appendUnique(result.QualityFlags, pcfFlags...)
	t0Groups, _, _ := service.buildT0Groups(orders, fills, instruments, redemptionUnits)
	t0Buckets := buildT0CostBuckets(accountID, normalizedDate, t0Groups)
	if normalizedDate != inception.InceptionDate {
		factors, factorsAvailable, factorFlags := service.loadCorporateActionFactors(ctx, normalizedDate, securityIDs)
		result.QualityFlags = appendUnique(result.QualityFlags, factorFlags...)
		for key, state := range working {
			openPosition := openBySecurity[key]
			factor, hasFactor := factors[key]
			applyCorporateActionOpening(state, openPosition.Quantity, factor, hasFactor, factorsAvailable)
		}
		for key, openPosition := range openBySecurity {
			if working[key] != nil || openPosition.Quantity == 0 {
				continue
			}
			state := newCostWorkingState(accountID, normalizedDate, openPosition.Symbol, string(openPosition.Exchange), result.OpeningSource)
			state.quantity = openPosition.Quantity
			state.item.BrokerOpenQuantity = openPosition.Quantity
			state.item.OpenQuantity = openPosition.Quantity
			state.item.OpenTotalCost = trustedBrokerPositionCost(openPosition)
			state.cost = state.item.OpenTotalCost
			state.item.Status = "blocked"
			state.flags = appendUnique(state.flags, "opening_position_without_previous_cost")
			working[key] = state
		}
	}
	rules, ruleErr := service.store.ListFeeRules(ctx, ledger.FeeRuleQuery{
		AccountID:   accountID,
		Status:      "active",
		EffectiveOn: normalizedDate,
		Limit:       500,
	})
	if ruleErr != nil {
		result.QualityFlags = appendUnique(result.QualityFlags, "fee_rules_unavailable")
		rules = nil
	}
	feeRecords, err := service.listOrderFeeRecords(ctx, accountID, normalizedDate)
	if err != nil {
		return CostLedgerResult{}, err
	}
	authoritativeFees := authoritativeOrderFees(feeRecords)
	consumedOrderFees := make(map[string]bool)

	for _, fill := range fills {
		if t0Buckets.consumedFills[contributionFillKey(fill)] {
			continue
		}
		key := contributionSecurityID(fill.Symbol, fill.Exchange)
		state := working[key]
		instrument := instruments[key]
		fee := contributionFeeForFill(fill, instrument, rules, authoritativeFees, consumedOrderFees)
		state.item.FeeSource = mergeContributionFeeSource(state.item.FeeSource, fee.source)
		state.flags = appendUnique(state.flags, fee.flags...)
		amount := contributionFillAmount(fill)
		switch fill.TradeSide {
		case trading.TradeSideBuy:
			state.item.BuyQuantity += fill.Qty
			state.item.BuyAmount += amount
			state.item.BuyFee += fee.effective
			state.quantity += fill.Qty
			state.cost += amount + fee.effective
		case trading.TradeSideSell:
			state.item.SellQuantity += fill.Qty
			state.item.SellAmount += amount
			state.item.SellFee += fee.effective
			if fill.Qty > state.quantity {
				state.flags = appendUnique(state.flags, "sell_quantity_exceeds_cost_position")
				state.item.Status = "blocked"
				continue
			}
			averageCost := 0.0
			if state.quantity > 0 {
				averageCost = state.cost / float64(state.quantity)
			}
			settledCost := averageCost * float64(fill.Qty)
			state.item.RealizedPnL += amount - fee.effective - settledCost
			state.quantity -= fill.Qty
			state.cost -= settledCost
			if state.quantity == 0 || math.Abs(state.cost) < 0.000001 {
				state.cost = 0
			}
		case trading.TradeSidePurchase, trading.TradeSideRedemption:
			state.flags = appendUnique(state.flags, "etf_subscription_redemption_requires_separate_cost_bucket")
			state.item.Status = "blocked"
		}
	}
	for _, state := range t0Buckets.states {
		state.item.CalculatedAt = result.CalculatedAt
		result.Positions = append(result.Positions, state.item)
	}

	keys := make([]string, 0, len(working))
	for key := range working {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state := working[key]
		state.item.CloseQuantity = state.quantity
		state.item.CloseTotalCost = roundMoney(math.Max(0, state.cost))
		if state.quantity > 0 {
			state.item.AverageCost = roundMoney(state.item.CloseTotalCost / float64(state.quantity))
		}
		brokerClose := closeBySecurity[key]
		state.item.BrokerCloseQuantity = brokerClose.Quantity
		state.item.QuantityResidual = brokerClose.Quantity - state.quantity
		if state.item.QuantityResidual != 0 {
			state.flags = appendUnique(state.flags, "position_quantity_not_reconciled")
			state.item.Status = "blocked"
		}
		instrument := instruments[key]
		if instrument.HasClose {
			state.item.ClosePrice = instrument.Close
			state.item.CloseMarketValue = roundMoney(instrument.Close * float64(brokerClose.Quantity))
			state.item.UnrealizedPnL = roundMoney(state.item.CloseMarketValue - state.item.CloseTotalCost)
		} else if brokerClose.Quantity > 0 {
			state.flags = appendUnique(state.flags, "missing_meridian_close")
			state.item.Status = "blocked"
		}
		if containsStringValue(state.flags, "missing_fee_rule") && state.item.Status == "calculated" {
			state.item.Status = "estimated"
		}
		state.item.BuyAmount = roundMoney(state.item.BuyAmount)
		state.item.BuyFee = roundMoney(state.item.BuyFee)
		state.item.SellAmount = roundMoney(state.item.SellAmount)
		state.item.SellFee = roundMoney(state.item.SellFee)
		state.item.RealizedPnL = roundMoney(state.item.RealizedPnL)
		state.item.QualityFlags = state.flags
		state.item.CalculatedAt = result.CalculatedAt
		result.Positions = append(result.Positions, state.item)
	}
	finalizeCostLedgerResult(&result)
	if options.Persist {
		if inception.Status != "confirmed" {
			return result, fmt.Errorf("performance inception must be confirmed before cost ledger persistence")
		}
		for index := range result.Positions {
			saved, err := service.store.UpsertPositionCostState(ctx, result.Positions[index])
			if err != nil {
				return CostLedgerResult{}, err
			}
			result.Positions[index] = saved
		}
		result.Persisted = true
	}
	return result, nil
}

func newCostWorkingState(accountID, tradeDate, symbol, exchange, openingSource string) *costWorkingState {
	return &costWorkingState{item: ledger.PositionCostState{
		AccountID:           accountID,
		TradeDate:           tradeDate,
		Symbol:              strings.TrimSpace(symbol),
		Exchange:            strings.ToUpper(strings.TrimSpace(exchange)),
		CostBucket:          "CORE",
		Status:              "calculated",
		FormulaVersion:      positionCostFormulaVersion,
		FeeSource:           "none",
		OpeningSource:       openingSource,
		CorporateActionType: "none",
		Source:              "relay.performance.cost_ledger",
	}}
}

func finalizeCostLedgerResult(result *CostLedgerResult) {
	for _, item := range result.Positions {
		result.Summary.OpenQuantity += item.OpenQuantity
		result.Summary.OpenTotalCost += item.OpenTotalCost
		result.Summary.BuyQuantity += item.BuyQuantity
		result.Summary.BuyAmount += item.BuyAmount
		result.Summary.BuyFee += item.BuyFee
		result.Summary.SellQuantity += item.SellQuantity
		result.Summary.SellAmount += item.SellAmount
		result.Summary.SellFee += item.SellFee
		result.Summary.RealizedPnL += item.RealizedPnL
		result.Summary.CloseQuantity += item.CloseQuantity
		result.Summary.CloseTotalCost += item.CloseTotalCost
		result.Summary.CloseMarketValue += item.CloseMarketValue
		result.Summary.UnrealizedPnL += item.UnrealizedPnL
		result.QualityFlags = appendUnique(result.QualityFlags, item.QualityFlags...)
		if item.QuantityResidual != 0 {
			result.Summary.QuantityBreaks++
		}
		if isT0CostBucket(item.CostBucket) {
			result.Summary.T0CostBuckets++
			result.Summary.T0BuyQuantity += item.BuyQuantity
			result.Summary.T0RedemptionQuantity += item.SellQuantity
			result.Summary.T0BuyAmount += item.BuyAmount
			if item.Status == "blocked" {
				result.Summary.T0BlockedBuckets++
			}
		}
		switch item.CorporateActionType {
		case "price_adjustment":
			result.Summary.CorporateActions++
		case "quantity_adjustment":
			result.Summary.CorporateActions++
			result.Summary.QuantityAdjustments++
		case "mismatch":
			result.Summary.CorporateActionBreaks++
		}
		if containsStringValue(item.QualityFlags, "missing_fee_rule") {
			result.Summary.MissingFeeItems++
		}
		switch item.Status {
		case "blocked":
			result.Summary.BlockedItems++
		case "estimated":
			result.Summary.EstimatedItems++
		default:
			result.Summary.CalculatedItems++
		}
	}
	result.Summary.Securities = len(result.Positions)
	result.Summary.OpenTotalCost = roundMoney(result.Summary.OpenTotalCost)
	result.Summary.BuyAmount = roundMoney(result.Summary.BuyAmount)
	result.Summary.BuyFee = roundMoney(result.Summary.BuyFee)
	result.Summary.SellAmount = roundMoney(result.Summary.SellAmount)
	result.Summary.SellFee = roundMoney(result.Summary.SellFee)
	result.Summary.RealizedPnL = roundMoney(result.Summary.RealizedPnL)
	result.Summary.CloseTotalCost = roundMoney(result.Summary.CloseTotalCost)
	result.Summary.CloseMarketValue = roundMoney(result.Summary.CloseMarketValue)
	result.Summary.UnrealizedPnL = roundMoney(result.Summary.UnrealizedPnL)
	result.Summary.T0BuyAmount = roundMoney(result.Summary.T0BuyAmount)
	if result.Summary.BlockedItems > 0 || result.Summary.QuantityBreaks > 0 {
		result.Status = "blocked"
	} else if result.Summary.EstimatedItems > 0 || result.Summary.MissingFeeItems > 0 {
		result.Status = "estimated"
	}
}

func containsStringValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func trustedBrokerPositionCost(position trading.Position) float64 {
	if position.CostComplete && position.TotalCost > 0 {
		return roundMoney(position.TotalCost)
	}
	if position.AvgCostSource != "" {
		return 0
	}
	if position.AvgCost > 0 && position.Quantity > 0 {
		return roundMoney(position.AvgCost * float64(position.Quantity))
	}
	return 0
}
