package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const (
	StrategyStockCrossSection    = "stock_cross_section"
	StrategyETFCrossSection      = "etf_cross_section"
	StrategyETFRedemptionT0      = "etf_redemption_t0"
	StrategyCashManagement       = "cash_management"
	StrategyETFComponentTransfer = "etf_component_transfer"
	StrategyUnattributed         = "unattributed"

	contributionFormulaVersion = "performance_contribution.v1"
	contributionPageLimit      = 500
	maxContributionPages       = 40
	contributionMarketBatch    = 100
	t0ContributionConcurrency  = 8
)

type ContributionResult struct {
	AccountID      string                 `json:"account_id"`
	TradeDate      string                 `json:"trade_date"`
	FormulaVersion string                 `json:"formula_version"`
	Summary        ContributionSummary    `json:"summary"`
	Contributions  []SecurityContribution `json:"contributions"`
	Strategies     []StrategyContribution `json:"strategies"`
	QualityFlags   []string               `json:"quality_flags,omitempty"`
	GeneratedAt    time.Time              `json:"generated_at"`
}

type ContributionSummary struct {
	OpenEconomicNAV        float64 `json:"open_economic_nav"`
	CloseEconomicNAV       float64 `json:"close_economic_nav"`
	OpenPositionValue      float64 `json:"open_position_value"`
	ClosePositionValue     float64 `json:"close_position_value"`
	Securities             int     `json:"securities"`
	Orders                 int     `json:"orders"`
	Fills                  int     `json:"fills"`
	BuyAmount              float64 `json:"buy_amount"`
	SellAmount             float64 `json:"sell_amount"`
	Turnover               float64 `json:"turnover"`
	ActualFee              float64 `json:"actual_fee"`
	EstimatedFee           float64 `json:"estimated_fee"`
	EffectiveFee           float64 `json:"effective_fee"`
	GrossContribution      float64 `json:"gross_contribution"`
	NetContribution        float64 `json:"net_contribution"`
	ContributionBPS        float64 `json:"contribution_bps"`
	AccountDayPnL          float64 `json:"account_day_pnl"`
	AccountDayPnLAvailable bool    `json:"account_day_pnl_available"`
	AttributionResidual    float64 `json:"attribution_residual"`
	EstimatedItems         int     `json:"estimated_items"`
	MissingItems           int     `json:"missing_items"`
	MissingFeeItems        int     `json:"missing_fee_items"`
	FeeRequiredOrders      int     `json:"fee_required_orders"`
	FeeCoveredOrders       int     `json:"fee_covered_orders"`
	FeeCoverageComplete    bool    `json:"fee_coverage_complete"`
	FeeCoverageSource      string  `json:"fee_coverage_source"`
	ExcludedItems          int     `json:"excluded_items"`
}

type SecurityContribution struct {
	SecurityID         string     `json:"security_id"`
	Symbol             string     `json:"symbol"`
	Exchange           string     `json:"exchange"`
	Name               string     `json:"name,omitempty"`
	InstrumentType     string     `json:"instrument_type,omitempty"`
	StrategyType       string     `json:"strategy_type"`
	StrategyID         string     `json:"strategy_id,omitempty"`
	OpenQuantity       int64      `json:"open_quantity"`
	CloseQuantity      int64      `json:"close_quantity"`
	BuyQuantity        int64      `json:"buy_quantity"`
	SellQuantity       int64      `json:"sell_quantity"`
	RedemptionQuantity int64      `json:"redemption_quantity,omitempty"`
	RedemptionUnit     int64      `json:"redemption_unit,omitempty"`
	BuyAmount          float64    `json:"buy_amount"`
	SellAmount         float64    `json:"sell_amount"`
	Turnover           float64    `json:"turnover"`
	OpenPrice          *float64   `json:"open_price,omitempty"`
	ClosePrice         *float64   `json:"close_price,omitempty"`
	OpenValue          float64    `json:"open_value"`
	CloseValue         float64    `json:"close_value"`
	MarketValue        float64    `json:"market_value"`
	Weight             float64    `json:"weight"`
	ActualFee          float64    `json:"actual_fee"`
	EstimatedFee       float64    `json:"estimated_fee"`
	EffectiveFee       float64    `json:"effective_fee"`
	FeeSource          string     `json:"fee_source"`
	GrossContribution  *float64   `json:"gross_contribution,omitempty"`
	NetContribution    *float64   `json:"net_contribution,omitempty"`
	ContributionBPS    *float64   `json:"contribution_bps,omitempty"`
	EstimatedExitValue *float64   `json:"estimated_exit_value,omitempty"`
	ReferenceIOPV      *float64   `json:"reference_iopv,omitempty"`
	ReferenceTime      *time.Time `json:"reference_time,omitempty"`
	PnLStatus          string     `json:"pnl_status"`
	EstimationMethod   string     `json:"estimation_method,omitempty"`
	PriceSource        string     `json:"price_source,omitempty"`
	Orders             int        `json:"orders"`
	Fills              int        `json:"fills"`
	QualityFlags       []string   `json:"quality_flags,omitempty"`
}

type StrategyContribution struct {
	StrategyType    string   `json:"strategy_type"`
	Securities      int      `json:"securities"`
	BuyAmount       float64  `json:"buy_amount"`
	SellAmount      float64  `json:"sell_amount"`
	Turnover        float64  `json:"turnover"`
	EffectiveFee    float64  `json:"effective_fee"`
	NetContribution float64  `json:"net_contribution"`
	ContributionBPS float64  `json:"contribution_bps"`
	EstimatedItems  int      `json:"estimated_items"`
	MissingItems    int      `json:"missing_items"`
	ExcludedItems   int      `json:"excluded_items"`
	QualityFlags    []string `json:"quality_flags,omitempty"`
}

type contributionInstrument struct {
	SecurityID     string
	Symbol         string
	Exchange       string
	Name           string
	InstrumentType string
	PriceSource    string
	PreClose       float64
	Close          float64
	HasPreClose    bool
	HasClose       bool
}

type contributionBucket struct {
	instrument contributionInstrument
	open       trading.Position
	close      trading.Position
	hasOpen    bool
	hasClose   bool
	orders     []trading.Order
	fills      []trading.Fill
}

type t0RedemptionGroup struct {
	securityID     string
	groupID        string
	explicit       bool
	instrument     contributionInstrument
	orders         []trading.Order
	buyFills       []trading.Fill
	redemptions    []trading.Fill
	redemptionUnit int64
	flags          []string
}

type contributionFee struct {
	actual    float64
	estimated float64
	effective float64
	source    string
	flags     []string
}

// CalculateContributions builds a read-only, explainable daily attribution view.
// It does not persist inferred strategy links.
func (service *Service) CalculateContributions(ctx context.Context, accountID, tradeDate string) (ContributionResult, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ContributionResult{}, errors.New("account_id is required")
	}
	normalizedDate, err := service.resolveContributionDate(ctx, tradeDate)
	if err != nil {
		return ContributionResult{}, err
	}
	value, err, _ := service.contributionFlights.Do(accountID+"|"+normalizedDate, func() (any, error) {
		return service.calculateContributions(ctx, accountID, normalizedDate)
	})
	if err != nil {
		return ContributionResult{}, err
	}
	result, ok := value.(ContributionResult)
	if !ok {
		return ContributionResult{}, errors.New("invalid contribution calculation result")
	}
	return result, nil
}

func (service *Service) calculateContributions(ctx context.Context, accountID, normalizedDate string) (ContributionResult, error) {
	orders, err := service.listContributionOrders(ctx, accountID, normalizedDate)
	if err != nil {
		return ContributionResult{}, err
	}
	fills, err := service.listTradeDateFills(ctx, accountID, normalizedDate)
	if err != nil {
		return ContributionResult{}, err
	}
	fills = dedupeContributionFills(fills)
	feeRecords, err := service.listOrderFeeRecords(ctx, accountID, normalizedDate)
	if err != nil {
		return ContributionResult{}, err
	}
	authoritativeFees := authoritativeOrderFees(feeRecords)
	feeCoverage := calculateOrderFeeDayCoverage(fills, authoritativeFees)
	consumedOrderFees := make(map[string]bool)
	ordinaryFillCount := len(fills)
	redemptionTransferFills, transferErr := service.listRedemptionTransferFills(ctx, accountID, normalizedDate, orders, fills)
	if transferErr == nil && len(redemptionTransferFills) > 0 {
		fills = append(fills, redemptionTransferFills...)
		fills = dedupeContributionFills(fills)
	}

	openPositions, err := service.listContributionPositions(ctx, accountID, normalizedDate, "open")
	if err != nil {
		return ContributionResult{}, err
	}
	openPositionFlags := make([]string, 0)
	if len(openPositions) == 0 {
		openPositions, openPositionFlags = service.previousClosePositionFallback(ctx, accountID, normalizedDate)
	}
	closePositions, err := service.listContributionPositions(ctx, accountID, normalizedDate, "close")
	if err != nil {
		return ContributionResult{}, err
	}
	closePositions, closePositionFlags := service.supplementClosePositionsFromNextOpen(ctx, accountID, normalizedDate, closePositions)

	result := ContributionResult{
		AccountID:      accountID,
		TradeDate:      normalizedDate,
		FormulaVersion: contributionFormulaVersion,
		GeneratedAt:    service.now(),
	}
	result.Summary.FeeRequiredOrders = feeCoverage.requiredOrders
	result.Summary.FeeCoveredOrders = feeCoverage.coveredOrders
	result.Summary.FeeCoverageComplete = feeCoverage.complete
	result.Summary.FeeCoverageSource = feeCoverage.source
	if !feeCoverage.complete {
		result.QualityFlags = appendUnique(result.QualityFlags, "order_fee_day_incomplete", "broker_delivery_statement_pending")
	}
	result.QualityFlags = appendUnique(result.QualityFlags, openPositionFlags...)
	result.QualityFlags = appendUnique(result.QualityFlags, closePositionFlags...)
	if transferErr != nil {
		result.QualityFlags = appendUnique(result.QualityFlags, "component_transfer_ledger_unavailable")
	} else if len(redemptionTransferFills) > 0 {
		result.QualityFlags = appendUnique(result.QualityFlags, "etf_redemption_from_transfer_ledger")
	}
	result.Summary.Orders = len(orders)
	result.Summary.Fills = ordinaryFillCount

	navs, navErr := service.store.ListPerformanceNAVs(ctx, accountID, normalizedDate, normalizedDate)
	if navErr == nil && len(navs) > 0 {
		nav := navs[0]
		result.Summary.OpenEconomicNAV = nav.OpenEconomicNAV
		result.Summary.CloseEconomicNAV = nav.CloseEconomicNAV
		result.Summary.AccountDayPnL = nav.AccountDayPnL
		result.Summary.AccountDayPnLAvailable = true
	} else if navErr != nil {
		result.QualityFlags = appendUnique(result.QualityFlags, "economic_nav_unavailable")
	}

	daily, dailyErr := service.store.GetDailyPerformance(ctx, accountID, normalizedDate)
	if dailyErr != nil && !errors.Is(dailyErr, sql.ErrNoRows) {
		return ContributionResult{}, dailyErr
	}
	if dailyErr == nil {
		result.Summary.OpenEconomicNAV = firstPositiveFloat(result.Summary.OpenEconomicNAV, daily.OpenNetAsset, daily.PreviousNetAsset)
		result.Summary.CloseEconomicNAV = firstPositiveFloat(result.Summary.CloseEconomicNAV, daily.NetAsset)
	} else {
		result.QualityFlags = appendUnique(result.QualityFlags, "missing_daily_performance")
	}
	if result.Summary.OpenEconomicNAV <= 0 {
		result.QualityFlags = appendUnique(result.QualityFlags, "missing_open_economic_nav")
	}

	buckets := buildContributionBuckets(orders, fills, openPositions, closePositions)
	securityIDs := make([]string, 0, len(buckets))
	for securityID := range buckets {
		securityIDs = append(securityIDs, securityID)
	}
	sort.Strings(securityIDs)

	instruments, marketFlags := service.loadContributionInstruments(ctx, normalizedDate, securityIDs)
	result.QualityFlags = appendUnique(result.QualityFlags, marketFlags...)
	for securityID, instrument := range instruments {
		bucket := buckets[securityID]
		instrument.Name = firstNonBlank(instrument.Name, bucket.instrument.Name)
		instrument.InstrumentType = firstNonBlank(instrument.InstrumentType, bucket.instrument.InstrumentType)
		bucket.instrument = instrument
		buckets[securityID] = bucket
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

	redemptionUnits, pcfFlags := service.loadPCFRedemptionUnits(ctx, normalizedDate, fills)
	result.QualityFlags = appendUnique(result.QualityFlags, pcfFlags...)
	t0Groups, consumedOrders, consumedFills := service.buildT0Groups(orders, fills, instruments, redemptionUnits)
	for _, item := range service.calculateT0Contributions(ctx, normalizedDate, t0Groups, result.Summary.OpenEconomicNAV) {
		result.Contributions = append(result.Contributions, item)
		result.QualityFlags = appendUnique(result.QualityFlags, item.QualityFlags...)
	}

	hasRedemption := len(t0Groups) > 0
	for _, securityID := range securityIDs {
		bucket := buckets[securityID]
		filteredOrders := filterContributionOrders(bucket.orders, consumedOrders)
		filteredFills := filterContributionFills(bucket.fills, consumedFills)
		if securityID == reverseRepoSecurityID {
			continue
		}
		if len(filteredOrders) == 0 && len(filteredFills) == 0 && !bucket.hasOpen && !bucket.hasClose {
			continue
		}
		bucket.orders = filteredOrders
		bucket.fills = filteredFills
		item := calculateOrdinaryContribution(bucket, rules, authoritativeFees, consumedOrderFees, result.Summary.OpenEconomicNAV, hasRedemption)
		result.Contributions = append(result.Contributions, item)
		result.QualityFlags = appendUnique(result.QualityFlags, item.QualityFlags...)
	}

	repoItem, ok := service.calculateCashManagementContribution(ctx, accountID, normalizedDate, result.Summary.OpenEconomicNAV)
	if ok {
		result.Contributions = append(result.Contributions, repoItem)
		result.QualityFlags = appendUnique(result.QualityFlags, repoItem.QualityFlags...)
	}

	finalizeContributionResult(&result)
	if result.Summary.AccountDayPnLAvailable {
		result.Summary.AttributionResidual = roundMoney(result.Summary.AccountDayPnL - result.Summary.NetContribution)
		if math.Abs(result.Summary.AttributionResidual) > service.warningToleranceCNY {
			result.QualityFlags = appendUnique(result.QualityFlags, "attribution_residual_exceeds_warning")
		}
	} else {
		result.QualityFlags = appendUnique(result.QualityFlags, "account_day_pnl_unavailable")
	}
	return result, nil
}

func (service *Service) resolveContributionDate(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" {
		normalized, _, err := parseTradeDate(value)
		return normalized, err
	}
	date := service.now().In(timeutil.Location()).Format("20060102")
	if service.calendar != nil {
		status, err := service.calendar.TradingDayStatus(ctx, date)
		if err == nil && strings.TrimSpace(status.PreviousOrCurrentTradingDate) != "" {
			date = status.PreviousOrCurrentTradingDate
		}
	}
	normalized, _, err := parseTradeDate(date)
	return normalized, err
}

func (service *Service) listContributionOrders(ctx context.Context, accountID, tradeDate string) ([]trading.Order, error) {
	items := make([]trading.Order, 0)
	for page := 0; page < maxContributionPages; page++ {
		batch, err := service.store.ListOrders(ctx, trading.OrderQuery{
			AccountID: accountID,
			TradeDate: tradeDate,
			History:   true,
			Limit:     contributionPageLimit,
			Cursor:    strconv.Itoa(page * contributionPageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list contribution orders: %w", err)
		}
		items = append(items, batch...)
		if len(batch) < contributionPageLimit {
			return items, nil
		}
	}
	return nil, fmt.Errorf("contribution orders exceed %d rows", contributionPageLimit*maxContributionPages)
}

func (service *Service) listContributionPositions(ctx context.Context, accountID, tradeDate, snapshotType string) ([]trading.Position, error) {
	items := make([]trading.Position, 0)
	for page := 0; page < maxContributionPages; page++ {
		batch, err := service.store.ListPositionSnapshots(ctx, trading.PositionQuery{
			AccountID:    accountID,
			TradeDate:    tradeDate,
			SnapshotType: snapshotType,
			History:      true,
			Limit:        contributionPageLimit,
			Cursor:       strconv.Itoa(page * contributionPageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list %s contribution positions: %w", snapshotType, err)
		}
		items = append(items, batch...)
		if len(batch) < contributionPageLimit {
			return items, nil
		}
	}
	return nil, fmt.Errorf("%s contribution positions exceed %d rows", snapshotType, contributionPageLimit*maxContributionPages)
}

func (service *Service) supplementClosePositionsFromNextOpen(ctx context.Context, accountID, tradeDate string, closePositions []trading.Position) ([]trading.Position, []string) {
	if service.calendar == nil {
		return closePositions, nil
	}
	_, parsedDate, err := parseTradeDate(tradeDate)
	if err != nil {
		return closePositions, nil
	}
	nextDate, err := service.nextTradingDay(ctx, parsedDate)
	if err != nil {
		return closePositions, nil
	}
	nextOpen, err := service.listContributionPositions(ctx, accountID, nextDate.Format("2006-01-02"), "open")
	if err != nil || len(nextOpen) == 0 {
		return closePositions, nil
	}
	existing := make(map[string]bool, len(closePositions))
	for _, position := range closePositions {
		existing[contributionSecurityID(position.Symbol, position.Exchange)] = true
	}
	added := 0
	for _, position := range nextOpen {
		key := contributionSecurityID(position.Symbol, position.Exchange)
		if existing[key] || position.Quantity <= 0 {
			continue
		}
		position.TradeDate = tradeDate
		position.SnapshotType = "next_open_close_fallback"
		closePositions = append(closePositions, position)
		existing[key] = true
		added++
	}
	if added == 0 {
		return closePositions, nil
	}
	return closePositions, []string{"close_position_supplemented_from_next_open"}
}

func (service *Service) previousClosePositionFallback(ctx context.Context, accountID, tradeDate string) ([]trading.Position, []string) {
	if service.calendar == nil {
		return nil, nil
	}
	_, parsed, err := parseTradeDate(tradeDate)
	if err != nil {
		return nil, nil
	}
	status, err := service.calendar.TradingDayStatus(ctx, parsed.AddDate(0, 0, -1).Format("20060102"))
	if err != nil || strings.TrimSpace(status.PreviousOrCurrentTradingDate) == "" {
		return nil, nil
	}
	previousDate, _, err := parseTradeDate(status.PreviousOrCurrentTradingDate)
	if err != nil || previousDate == tradeDate {
		return nil, nil
	}
	positions, err := service.listContributionPositions(ctx, accountID, previousDate, "close")
	if err != nil || len(positions) == 0 {
		return nil, nil
	}
	for index := range positions {
		positions[index].SnapshotType = "previous_close_fallback"
	}
	return positions, []string{"open_positions_from_previous_close"}
}

func (service *Service) listRedemptionTransferFills(
	ctx context.Context,
	accountID string,
	tradeDate string,
	orders []trading.Order,
	fills []trading.Fill,
) ([]trading.Fill, error) {
	store, ok := service.store.(interface {
		ListComponentTransfers(context.Context, trading.ComponentTransferQuery) ([]trading.ComponentTransfer, error)
	})
	if !ok {
		return nil, nil
	}
	transfers := make([]trading.ComponentTransfer, 0)
	for page := 0; page < maxContributionPages; page++ {
		batch, err := store.ListComponentTransfers(ctx, trading.ComponentTransferQuery{
			AccountID: accountID,
			TradeDate: tradeDate,
			History:   true,
			Limit:     contributionPageLimit,
			Cursor:    strconv.Itoa(page * contributionPageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list contribution component transfers: %w", err)
		}
		transfers = append(transfers, batch...)
		if len(batch) < contributionPageLimit {
			break
		}
		if page == maxContributionPages-1 {
			return nil, fmt.Errorf("contribution component transfers exceed %d rows", contributionPageLimit*maxContributionPages)
		}
	}
	return redemptionFillsFromComponentTransfers(orders, fills, transfers), nil
}

func redemptionFillsFromComponentTransfers(orders []trading.Order, fills []trading.Fill, transfers []trading.ComponentTransfer) []trading.Fill {
	ordersByID := make(map[string]trading.Order, len(orders))
	for _, order := range orders {
		ordersByID[order.GatewayOrderID] = order
	}
	hasOrdinaryRedemption := make(map[string]bool)
	for _, fill := range fills {
		if isRedemptionFill(fill) {
			hasOrdinaryRedemption[fill.GatewayOrderID] = true
		}
	}
	seen := make(map[string]bool)
	out := make([]trading.Fill, 0)
	for _, transfer := range transfers {
		order, ok := ordersByID[transfer.GatewayOrderID]
		if !ok || hasOrdinaryRedemption[transfer.GatewayOrderID] {
			continue
		}
		if !isETFBusinessRedemption(order.TradeSide, order.BusinessType) ||
			contributionSecurityID(order.Symbol, order.Exchange) != contributionSecurityID(transfer.Symbol, transfer.Exchange) {
			continue
		}
		key := strings.Join([]string{
			transfer.GatewayOrderID,
			transfer.FillID,
			transfer.OrderStreamID,
			strconv.FormatInt(transfer.MatchTimestamp, 10),
			strconv.FormatInt(transfer.Qty, 10),
		}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		context := make(map[string]any, len(transfer.AdapterContext)+1)
		for name, value := range transfer.AdapterContext {
			context[name] = value
		}
		context["relay_component_transfer_source"] = true
		out = append(out, trading.Fill{
			FillID:         "transfer:" + firstNonBlank(transfer.FillID, transfer.OrderStreamID),
			AccountID:      transfer.AccountID,
			GatewayOrderID: transfer.GatewayOrderID,
			OrderID:        transfer.OrderID,
			OrderStreamID:  transfer.OrderStreamID,
			Symbol:         transfer.Symbol,
			Name:           firstNonBlank(transfer.Name, order.Name),
			Exchange:       transfer.Exchange,
			TradeSide:      transfer.TradeSide,
			BusinessType:   transfer.BusinessType,
			Price:          0,
			Qty:            transfer.Qty,
			TradeDate:      transfer.TradeDate,
			MatchTimestamp: transfer.MatchTimestamp,
			MatchedAt:      transfer.MatchedAt,
			ShareholderID:  transfer.ShareholderID,
			StrategyType:   firstNonBlank(transfer.StrategyType, order.StrategyType),
			StrategyID:     firstNonBlank(transfer.StrategyID, order.StrategyID),
			BasketID:       firstNonBlank(transfer.BasketID, order.BasketID),
			ParentOrderID:  firstNonBlank(transfer.ParentOrderID, order.ParentOrderID),
			T0OrderGroupID: firstNonBlank(transfer.T0OrderGroupID, order.T0OrderGroupID),
			AdapterContext: context,
		})
	}
	return out
}

func dedupeContributionFills(fills []trading.Fill) []trading.Fill {
	hasActual := make(map[string]bool)
	seen := make(map[string]struct{})
	for _, fill := range fills {
		if !strings.HasPrefix(fill.FillID, "relay-summary:") {
			hasActual[fill.GatewayOrderID] = true
		}
	}
	items := make([]trading.Fill, 0, len(fills))
	for _, fill := range fills {
		if strings.HasPrefix(fill.FillID, "relay-summary:") && hasActual[fill.GatewayOrderID] {
			continue
		}
		key := strings.Join([]string{fill.AccountID, fill.GatewayOrderID, fill.FillID}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, fill)
	}
	return items
}

func buildContributionBuckets(orders []trading.Order, fills []trading.Fill, openPositions, closePositions []trading.Position) map[string]contributionBucket {
	buckets := make(map[string]contributionBucket)
	for _, position := range openPositions {
		securityID := contributionSecurityID(position.Symbol, position.Exchange)
		bucket := buckets[securityID]
		bucket.open = position
		bucket.hasOpen = true
		bucket.instrument = contributionInstrumentFromPosition(position)
		buckets[securityID] = bucket
	}
	for _, position := range closePositions {
		securityID := contributionSecurityID(position.Symbol, position.Exchange)
		bucket := buckets[securityID]
		bucket.close = position
		bucket.hasClose = true
		if bucket.instrument.SecurityID == "" {
			bucket.instrument = contributionInstrumentFromPosition(position)
		}
		buckets[securityID] = bucket
	}
	for _, order := range orders {
		securityID := contributionSecurityID(order.Symbol, order.Exchange)
		bucket := buckets[securityID]
		bucket.orders = append(bucket.orders, order)
		if bucket.instrument.SecurityID == "" {
			bucket.instrument = contributionInstrument{
				SecurityID: securityID,
				Symbol:     order.Symbol,
				Exchange:   string(order.Exchange),
				Name:       order.Name,
			}
		}
		buckets[securityID] = bucket
	}
	for _, fill := range fills {
		securityID := contributionSecurityID(fill.Symbol, fill.Exchange)
		bucket := buckets[securityID]
		bucket.fills = append(bucket.fills, fill)
		if bucket.instrument.SecurityID == "" {
			bucket.instrument = contributionInstrument{
				SecurityID: securityID,
				Symbol:     fill.Symbol,
				Exchange:   string(fill.Exchange),
				Name:       fill.Name,
			}
		}
		buckets[securityID] = bucket
	}
	return buckets
}

func contributionInstrumentFromPosition(position trading.Position) contributionInstrument {
	return contributionInstrument{
		SecurityID: contributionSecurityID(position.Symbol, position.Exchange),
		Symbol:     position.Symbol,
		Exchange:   string(position.Exchange),
		Name:       position.Name,
	}
}

func contributionSecurityID(symbol string, exchange trading.Exchange) string {
	symbol = strings.TrimSpace(symbol)
	if strings.Contains(symbol, ".") {
		return strings.ToUpper(symbol)
	}
	exchangeText := strings.ToUpper(strings.TrimSpace(string(exchange)))
	if exchangeText == "" {
		return symbol
	}
	return strings.ToUpper(symbol + "." + exchangeText)
}

func (service *Service) loadContributionInstruments(ctx context.Context, tradeDate string, securityIDs []string) (map[string]contributionInstrument, []string) {
	items := make(map[string]contributionInstrument, len(securityIDs))
	for _, securityID := range securityIDs {
		symbol, exchange := splitContributionSecurityID(securityID)
		items[securityID] = contributionInstrument{SecurityID: securityID, Symbol: symbol, Exchange: exchange}
	}
	if service.market == nil || len(securityIDs) == 0 {
		return items, []string{"meridian_contribution_market_unavailable"}
	}

	flags := make([]string, 0)
	for start := 0; start < len(securityIDs); start += contributionMarketBatch {
		end := start + contributionMarketBatch
		if end > len(securityIDs) {
			end = len(securityIDs)
		}
		batch := securityIDs[start:end]
		values := url.Values{"security_ids": {strings.Join(batch, ",")}, "limit": {strconv.Itoa(len(batch))}}
		response, err := service.market.MetadataInstruments(ctx, values)
		if err != nil || response.StatusCode >= 400 {
			flags = appendUnique(flags, "meridian_instrument_metadata_unavailable")
		} else {
			for _, row := range contributionRows(response.Payload) {
				securityID := strings.ToUpper(contributionString(row["security_id"]))
				instrument, ok := items[securityID]
				if !ok {
					continue
				}
				instrument.Name = firstContributionString(row, "name", "security_name", "short_name")
				instrument.InstrumentType = strings.ToLower(firstContributionString(row, "instrument_type", "security_type"))
				items[securityID] = instrument
			}
		}

		barValues := url.Values{
			"security_ids": {strings.Join(batch, ",")},
			"start_date":   {strings.ReplaceAll(tradeDate, "-", "")},
			"end_date":     {strings.ReplaceAll(tradeDate, "-", "")},
			"frequency":    {"1d"},
			"adjustment":   {"none"},
			"limit":        {strconv.Itoa(len(batch) * 2)},
		}
		response, err = service.market.MarketBars(ctx, barValues)
		if err != nil || response.StatusCode >= 400 {
			flags = appendUnique(flags, "meridian_daily_bars_unavailable")
			continue
		}
		for _, row := range contributionRows(response.Payload) {
			securityID := strings.ToUpper(contributionString(row["security_id"]))
			instrument, ok := items[securityID]
			if !ok {
				continue
			}
			if value, ok := contributionFloat(row["pre_close"]); ok && value > 0 {
				instrument.PreClose = value
				instrument.HasPreClose = true
			}
			if value, ok := contributionFloat(row["close"]); ok && value > 0 {
				instrument.Close = value
				instrument.HasClose = true
			}
			if instrument.HasPreClose || instrument.HasClose {
				instrument.PriceSource = "meridian_1d_unadjusted"
			}
			if instrument.InstrumentType == "" {
				instrument.InstrumentType = strings.ToLower(contributionString(row["instrument_type"]))
			}
			items[securityID] = instrument
		}

		if tradeDate == service.now().In(timeutil.Location()).Format("2006-01-02") {
			usedLevel1 := service.fillCurrentContributionPricesFromLevel1(ctx, tradeDate, batch, items)
			if usedLevel1 {
				flags = appendUnique(flags, "meridian_level1_close_fallback")
			}
		}
	}
	return items, flags
}

func (service *Service) fillCurrentContributionPricesFromLevel1(
	ctx context.Context,
	tradeDate string,
	securityIDs []string,
	items map[string]contributionInstrument,
) bool {
	missing := make([]string, 0, len(securityIDs))
	for _, securityID := range securityIDs {
		instrument := items[securityID]
		if !instrument.HasPreClose || !instrument.HasClose {
			missing = append(missing, securityID)
		}
	}
	if len(missing) == 0 {
		return false
	}
	response, err := service.market.MarketSnapshots(ctx, url.Values{
		"security_ids": {strings.Join(missing, ",")},
		"trade_date":   {strings.ReplaceAll(tradeDate, "-", "")},
		"data_scope":   {"realtime"},
		"market_level": {"level1"},
		"limit":        {strconv.Itoa(len(missing))},
	})
	if err != nil || response.StatusCode >= http.StatusBadRequest {
		return false
	}
	used := false
	for _, row := range contributionRows(response.Payload) {
		if corporateActionDate(row["trade_date"]) != tradeDate {
			continue
		}
		securityID := strings.ToUpper(contributionString(row["security_id"]))
		instrument, ok := items[securityID]
		if !ok {
			continue
		}
		rowUsed := false
		if !instrument.HasPreClose {
			if value, ok := contributionFloat(row["pre_close"]); ok && value > 0 {
				instrument.PreClose = value
				instrument.HasPreClose = true
				rowUsed = true
			}
		}
		if !instrument.HasClose {
			if value, ok := contributionFloat(row["last"]); ok && value > 0 {
				instrument.Close = value
				instrument.HasClose = true
				rowUsed = true
			}
		}
		if rowUsed {
			if instrument.PriceSource == "" {
				instrument.PriceSource = "meridian_level1_snapshot"
			} else if instrument.PriceSource != "meridian_level1_snapshot" {
				instrument.PriceSource += "+meridian_level1_snapshot"
			}
			items[securityID] = instrument
			used = true
		}
	}
	return used
}

func (service *Service) loadPCFRedemptionUnits(ctx context.Context, tradeDate string, fills []trading.Fill) (map[string]int64, []string) {
	securitySet := make(map[string]struct{})
	for _, fill := range fills {
		if isRedemptionFill(fill) {
			securitySet[contributionSecurityID(fill.Symbol, fill.Exchange)] = struct{}{}
		}
	}
	if len(securitySet) == 0 {
		return nil, nil
	}
	if service.market == nil {
		return nil, []string{"meridian_etf_pcf_unavailable"}
	}
	securityIDs := make([]string, 0, len(securitySet))
	for securityID := range securitySet {
		securityIDs = append(securityIDs, securityID)
	}
	sort.Strings(securityIDs)
	units := make(map[string]int64, len(securityIDs))
	flags := make([]string, 0)
	for start := 0; start < len(securityIDs); start += contributionMarketBatch {
		end := start + contributionMarketBatch
		if end > len(securityIDs) {
			end = len(securityIDs)
		}
		batch := securityIDs[start:end]
		response, err := service.market.MarketETFCashComponents(ctx, url.Values{
			"security_ids": {strings.Join(batch, ",")},
			"trade_date":   {strings.ReplaceAll(tradeDate, "-", "")},
			"limit":        {strconv.Itoa(len(batch))},
		})
		if err != nil || response.StatusCode >= http.StatusBadRequest || response.Payload["error"] != nil {
			flags = appendUnique(flags, "meridian_etf_pcf_unavailable")
			continue
		}
		for _, row := range contributionRows(response.Payload) {
			securityID := strings.ToUpper(contributionString(row["security_id"]))
			value, ok := contributionFloat(row["unit_subscribe_redeem"])
			if !ok || value <= 0 || math.Abs(value-math.Round(value)) > 1e-6 {
				continue
			}
			units[securityID] = int64(math.Round(value))
		}
	}
	if len(units) < len(securityIDs) {
		flags = append(flags, "missing_meridian_etf_redemption_unit")
	}
	return units, flags
}

func (service *Service) buildT0Groups(orders []trading.Order, fills []trading.Fill, instruments map[string]contributionInstrument, redemptionUnits map[string]int64) ([]t0RedemptionGroup, map[string]bool, map[string]bool) {
	ordersByID := make(map[string]trading.Order, len(orders))
	fillsByOrder := make(map[string][]trading.Fill)
	for _, order := range orders {
		ordersByID[order.GatewayOrderID] = order
	}
	for _, fill := range fills {
		fillsByOrder[fill.GatewayOrderID] = append(fillsByOrder[fill.GatewayOrderID], fill)
	}

	redemptionBatches := make(map[string][]trading.Fill)
	redemptionOrderIDs := make([]string, 0)
	for _, fill := range fills {
		if isRedemptionFill(fill) {
			if _, ok := redemptionBatches[fill.GatewayOrderID]; !ok {
				redemptionOrderIDs = append(redemptionOrderIDs, fill.GatewayOrderID)
			}
			redemptionBatches[fill.GatewayOrderID] = append(redemptionBatches[fill.GatewayOrderID], fill)
		}
	}
	sort.SliceStable(redemptionOrderIDs, func(i, j int) bool {
		left := redemptionBatches[redemptionOrderIDs[i]]
		right := redemptionBatches[redemptionOrderIDs[j]]
		return contributionFillTime(left[0]).Before(contributionFillTime(right[0]))
	})

	consumedOrders := make(map[string]bool)
	consumedFills := make(map[string]bool)
	groups := make([]t0RedemptionGroup, 0, len(redemptionOrderIDs))
	for _, redemptionOrderID := range redemptionOrderIDs {
		redemptions := redemptionBatches[redemptionOrderID]
		redemption := redemptions[0]
		securityID := contributionSecurityID(redemption.Symbol, redemption.Exchange)
		group := t0RedemptionGroup{
			securityID:     securityID,
			instrument:     instruments[securityID],
			redemptions:    redemptions,
			redemptionUnit: redemptionUnits[securityID],
		}
		redemptionOrder := ordersByID[redemption.GatewayOrderID]
		explicitGroupID := firstNonBlank(redemption.T0OrderGroupID, redemptionOrder.T0OrderGroupID)
		group.groupID = firstNonBlank(explicitGroupID, redemption.GatewayOrderID)
		group.explicit = explicitGroupID != ""

		candidates := make([]trading.Order, 0)
		var candidateQuantity int64
		for _, order := range orders {
			if consumedOrders[order.GatewayOrderID] || contributionSecurityID(order.Symbol, order.Exchange) != securityID {
				continue
			}
			if order.TradeSide != trading.TradeSideBuy || isETFBusinessRedemption(order.TradeSide, order.BusinessType) {
				continue
			}
			if explicitGroupID != "" && order.T0OrderGroupID != explicitGroupID {
				continue
			}
			orderTime := contributionOrderTime(order)
			redemptionTime := contributionFillTime(redemption)
			if explicitGroupID == "" && (orderTime.IsZero() || redemptionTime.IsZero()) {
				continue
			}
			if orderTime.After(redemptionTime) {
				continue
			}
			candidates = append(candidates, order)
			candidateQuantity += order.OrderQty
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return contributionOrderTime(candidates[i]).After(contributionOrderTime(candidates[j]))
		})

		var target int64
		for _, item := range redemptions {
			target += item.Qty
		}
		if target <= 0 {
			target = redemptionOrder.OrderQty
		}
		if group.redemptionUnit <= 0 {
			group.flags = appendUnique(group.flags, "missing_meridian_etf_redemption_unit")
		} else if target <= 0 || target%group.redemptionUnit != 0 {
			group.flags = appendUnique(group.flags, "redemption_quantity_not_pcf_unit_multiple")
		}
		var selectedQty int64
		for _, order := range candidates {
			if selectedQty >= target {
				break
			}
			orderQty := order.OrderQty
			if orderQty <= 0 {
				for _, fill := range fillsByOrder[order.GatewayOrderID] {
					orderQty += fill.Qty
				}
			}
			if orderQty <= 0 || selectedQty+orderQty > target {
				continue
			}
			group.orders = append(group.orders, order)
			selectedQty += orderQty
		}

		if selectedQty != target || target <= 0 {
			group.flags = appendUnique(group.flags, "incomplete_t0_order_group")
			group.orders = nil
		} else {
			if explicitGroupID == "" {
				group.flags = appendUnique(group.flags, "historical_t0_order_group_inferred")
				if candidateQuantity > target {
					group.flags = appendUnique(group.flags, "ambiguous_t0_order_group")
				}
			}
			for _, order := range group.orders {
				consumedOrders[order.GatewayOrderID] = true
				for _, fill := range fillsByOrder[order.GatewayOrderID] {
					group.buyFills = append(group.buyFills, fill)
					consumedFills[contributionFillKey(fill)] = true
				}
			}
		}
		consumedOrders[redemption.GatewayOrderID] = true
		for _, item := range redemptions {
			consumedFills[contributionFillKey(item)] = true
		}
		if instrument := instruments[securityID]; !isETFInstrument(instrument.InstrumentType) {
			group.flags = appendUnique(group.flags, "redemption_instrument_type_unconfirmed")
		}
		groups = append(groups, group)
	}
	return groups, consumedOrders, consumedFills
}

func (service *Service) calculateT0Contributions(ctx context.Context, tradeDate string, groups []t0RedemptionGroup, openNAV float64) []SecurityContribution {
	items := make([]SecurityContribution, len(groups))
	if len(groups) == 0 {
		return items
	}

	limit := t0ContributionConcurrency
	if len(groups) < limit {
		limit = len(groups)
	}
	semaphore := make(chan struct{}, limit)
	var waitGroup sync.WaitGroup
	for index, group := range groups {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			items[index] = service.calculateT0Contribution(ctx, tradeDate, group, openNAV)
		}()
	}
	waitGroup.Wait()
	return items
}

func (service *Service) calculateT0Contribution(ctx context.Context, tradeDate string, group t0RedemptionGroup, openNAV float64) SecurityContribution {
	symbol, exchange := splitContributionSecurityID(group.securityID)
	item := SecurityContribution{
		SecurityID:       group.securityID,
		Symbol:           symbol,
		Exchange:         exchange,
		Name:             group.instrument.Name,
		InstrumentType:   firstNonBlank(group.instrument.InstrumentType, "etf"),
		StrategyType:     StrategyETFRedemptionT0,
		StrategyID:       group.groupID,
		PnLStatus:        "missing",
		FeeSource:        "estimated",
		PriceSource:      "meridian_historical_level1_iopv",
		EstimationMethod: "redemption_iopv_minus_t0_buy_cost_minus_configured_friction",
		QualityFlags:     append([]string(nil), group.flags...),
		Orders:           len(group.orders) + 1,
		Fills:            len(group.buyFills) + len(group.redemptions),
		RedemptionUnit:   group.redemptionUnit,
	}
	for _, fill := range group.buyFills {
		item.BuyQuantity += fill.Qty
		item.BuyAmount += contributionFillAmount(fill)
	}

	var exitValue float64
	var weightedIOPV float64
	var referenceQty int64
	allIOPV := len(group.redemptions) > 0
	for _, redemption := range group.redemptions {
		item.RedemptionQuantity += redemption.Qty
		item.SellQuantity += redemption.Qty
		iopv, referenceTime, ok := service.loadRedemptionIOPV(ctx, tradeDate, redemption)
		if !ok {
			allIOPV = false
			item.QualityFlags = appendUnique(item.QualityFlags, "missing_redemption_iopv")
			continue
		}
		exitValue += float64(redemption.Qty) * iopv
		weightedIOPV += float64(redemption.Qty) * iopv
		referenceQty += redemption.Qty
		if item.ReferenceTime == nil || referenceTime.After(*item.ReferenceTime) {
			copied := referenceTime
			item.ReferenceTime = &copied
		}
	}
	item.BuyAmount = roundMoney(item.BuyAmount)
	item.SellAmount = roundMoney(exitValue)
	item.Turnover = roundMoney(item.BuyAmount + item.SellAmount)
	if referenceQty > 0 {
		value := weightedIOPV / float64(referenceQty)
		item.ReferenceIOPV = floatPointer(value)
	}

	validPCFUnit := item.RedemptionUnit <= 0 || item.RedemptionQuantity%item.RedemptionUnit == 0
	completeCost := item.BuyQuantity == item.RedemptionQuantity && item.BuyQuantity > 0 && validPCFUnit
	if !completeCost {
		item.QualityFlags = appendUnique(item.QualityFlags, "incomplete_t0_buy_cost")
	}
	if allIOPV && completeCost {
		item.EstimatedExitValue = floatPointer(roundMoney(exitValue))
		item.EstimatedFee = roundMoney(exitValue * service.etfT0FrictionRate)
		item.EffectiveFee = item.EstimatedFee
		gross := roundMoney(exitValue - item.BuyAmount)
		net := roundMoney(gross - item.EffectiveFee)
		item.GrossContribution = floatPointer(gross)
		item.NetContribution = floatPointer(net)
		item.ContributionBPS = contributionBPSPointer(net, openNAV)
		item.PnLStatus = "estimated"
		item.QualityFlags = appendUnique(item.QualityFlags, "etf_t0_iopv_estimate", "configured_friction_estimate")
	}
	return item
}

func (service *Service) loadRedemptionIOPV(ctx context.Context, tradeDate string, fill trading.Fill) (float64, time.Time, bool) {
	if service.market == nil {
		return 0, time.Time{}, false
	}
	target := contributionFillTime(fill)
	if target.IsZero() {
		return 0, time.Time{}, false
	}
	target = target.In(timeutil.Location())
	values := url.Values{
		"security_id":  {contributionSecurityID(fill.Symbol, fill.Exchange)},
		"trade_date":   {strings.ReplaceAll(tradeDate, "-", "")},
		"data_scope":   {"historical"},
		"market_level": {"level1"},
		"start_time":   {target.Add(-2 * time.Minute).Format("15:04:05")},
		"end_time":     {target.Format("15:04:05")},
		"limit":        {"500"},
	}
	response, err := service.market.MarketSnapshots(ctx, values)
	if err != nil || response.StatusCode >= 400 {
		return 0, time.Time{}, false
	}
	var selectedValue float64
	var selectedTime time.Time
	for _, row := range contributionRows(response.Payload) {
		value, ok := contributionFloat(row["iopv"])
		if !ok || value <= 0 {
			continue
		}
		rowTime, ok := contributionMarketTime(row, tradeDate)
		if !ok || rowTime.After(target) {
			continue
		}
		if selectedTime.IsZero() || rowTime.After(selectedTime) {
			selectedValue = value
			selectedTime = rowTime
		}
	}
	return selectedValue, selectedTime, selectedValue > 0
}

func calculateOrdinaryContribution(
	bucket contributionBucket,
	rules []ledger.FeeRule,
	authoritativeFees map[string]ledger.OrderFeeRecord,
	consumedOrderFees map[string]bool,
	openNAV float64,
	hasRedemption bool,
) SecurityContribution {
	instrument := bucket.instrument
	item := SecurityContribution{
		SecurityID:     instrument.SecurityID,
		Symbol:         instrument.Symbol,
		Exchange:       instrument.Exchange,
		Name:           firstNonBlank(instrument.Name, bucket.open.Name, bucket.close.Name),
		InstrumentType: instrument.InstrumentType,
		OpenQuantity:   bucket.open.Quantity,
		CloseQuantity:  bucket.close.Quantity,
		MarketValue:    bucket.close.MarketValue,
		Orders:         len(bucket.orders),
		Fills:          len(bucket.fills),
		PnLStatus:      "calculated",
		PriceSource:    firstNonBlank(instrument.PriceSource, "meridian_1d_unadjusted"),
	}
	componentCandidate := hasRedemption &&
		item.OpenQuantity == 0 &&
		item.CloseQuantity == 0 &&
		!isETFInstrument(instrument.InstrumentType) &&
		allContributionFillsSell(bucket.fills)

	if instrument.HasPreClose {
		item.OpenPrice = floatPointer(instrument.PreClose)
		item.OpenValue = roundMoney(float64(item.OpenQuantity) * instrument.PreClose)
	} else if bucket.hasOpen && bucket.open.LastPrice > 0 {
		item.OpenPrice = floatPointer(bucket.open.LastPrice)
		item.OpenValue = roundMoney(firstPositiveFloat(bucket.open.MarketValue, float64(item.OpenQuantity)*bucket.open.LastPrice))
		item.PriceSource = "broker_open_snapshot_fallback"
		item.QualityFlags = appendUnique(item.QualityFlags, "missing_meridian_pre_close")
	} else if item.OpenQuantity != 0 {
		item.QualityFlags = appendUnique(item.QualityFlags, "missing_open_price")
	}
	if instrument.HasClose {
		item.ClosePrice = floatPointer(instrument.Close)
		item.CloseValue = roundMoney(float64(item.CloseQuantity) * instrument.Close)
	} else if bucket.hasClose && bucket.close.LastPrice > 0 {
		item.ClosePrice = floatPointer(bucket.close.LastPrice)
		item.CloseValue = roundMoney(firstPositiveFloat(bucket.close.MarketValue, float64(item.CloseQuantity)*bucket.close.LastPrice))
		item.PriceSource = "broker_close_snapshot_fallback"
		item.QualityFlags = appendUnique(item.QualityFlags, "missing_meridian_close")
	} else if item.CloseQuantity != 0 {
		item.QualityFlags = appendUnique(item.QualityFlags, "missing_close_price")
	}
	item.MarketValue = item.CloseValue

	var fees contributionFee
	for _, fill := range bucket.fills {
		amount := contributionFillAmount(fill)
		switch fill.TradeSide {
		case trading.TradeSideBuy, trading.TradeSidePurchase:
			item.BuyQuantity += fill.Qty
			item.BuyAmount += amount
		case trading.TradeSideSell:
			item.SellQuantity += fill.Qty
			item.SellAmount += amount
		case trading.TradeSideRedemption:
			item.RedemptionQuantity += fill.Qty
		}
		if !componentCandidate {
			fillFee := contributionFeeForFill(fill, instrument, rules, authoritativeFees, consumedOrderFees)
			fees.actual += fillFee.actual
			fees.estimated += fillFee.estimated
			fees.effective += fillFee.effective
			fees.flags = appendUnique(fees.flags, fillFee.flags...)
			fees.source = mergeContributionFeeSource(fees.source, fillFee.source)
		}
	}
	item.BuyAmount = roundMoney(item.BuyAmount)
	item.SellAmount = roundMoney(item.SellAmount)
	item.Turnover = roundMoney(item.BuyAmount + item.SellAmount)
	item.ActualFee = roundMoney(fees.actual)
	item.EstimatedFee = roundMoney(fees.estimated)
	item.EffectiveFee = roundMoney(fees.effective)
	item.FeeSource = firstNonBlank(fees.source, "none")
	item.QualityFlags = appendUnique(item.QualityFlags, fees.flags...)

	item.StrategyType = contributionStrategyType(instrument.InstrumentType)
	if strategy := dominantContributionStrategy(bucket.orders, bucket.fills); strategy != "" {
		item.StrategyType = strategy
	}
	item.StrategyID = dominantContributionStrategyID(bucket.orders, bucket.fills)

	componentOnly := componentCandidate && item.BuyQuantity == 0 && item.SellQuantity > 0
	if componentOnly && !isETFInstrument(instrument.InstrumentType) {
		item.StrategyType = StrategyETFComponentTransfer
		item.PnLStatus = "excluded"
		item.FeeSource = "included_in_etf_t0_friction"
		item.EstimationMethod = "component_sale_excluded_from_etf_t0_estimate"
		item.QualityFlags = appendUnique(item.QualityFlags, "component_sale_excluded_from_estimated_etf_t0", "missing_transfer_link")
		return item
	}

	impliedCloseQuantity := item.OpenQuantity + item.BuyQuantity - item.SellQuantity
	if !bucket.hasOpen && item.CloseQuantity != item.BuyQuantity-item.SellQuantity {
		item.PnLStatus = "missing"
		item.QualityFlags = appendUnique(item.QualityFlags, "missing_open_position_snapshot", "position_quantity_bridge_incomplete")
		return item
	}
	if !bucket.hasClose && impliedCloseQuantity != 0 {
		item.PnLStatus = "missing"
		item.QualityFlags = appendUnique(item.QualityFlags, "missing_close_position_snapshot", "position_quantity_bridge_incomplete")
		return item
	}
	if bucket.hasOpen && bucket.hasClose && impliedCloseQuantity != item.CloseQuantity {
		item.PnLStatus = "missing"
		item.QualityFlags = appendUnique(item.QualityFlags, "position_quantity_not_reconciled", "position_quantity_bridge_incomplete")
		return item
	}
	if (item.OpenQuantity != 0 && item.OpenPrice == nil) || (item.CloseQuantity != 0 && item.ClosePrice == nil) {
		item.PnLStatus = "missing"
		item.QualityFlags = appendUnique(item.QualityFlags, "incomplete_position_valuation")
		return item
	}
	gross := roundMoney(item.CloseValue + item.SellAmount - item.BuyAmount - item.OpenValue)
	net := roundMoney(gross - item.EffectiveFee)
	item.GrossContribution = floatPointer(gross)
	item.NetContribution = floatPointer(net)
	item.ContributionBPS = contributionBPSPointer(net, openNAV)
	if bucket.open.SnapshotType == "previous_close_fallback" {
		item.PnLStatus = "estimated"
		item.QualityFlags = appendUnique(item.QualityFlags, "open_position_from_previous_close")
	}
	return item
}

func (service *Service) calculateCashManagementContribution(ctx context.Context, accountID, tradeDate string, openNAV float64) (SecurityContribution, bool) {
	result, err := service.CalculateReverseRepo(ctx, accountID, tradeDate, false)
	if err != nil || result.Orders == 0 {
		return SecurityContribution{}, false
	}
	net := result.NetInterest
	gross := result.GrossInterest
	feeSource := ""
	actualFee := 0.0
	estimatedFee := 0.0
	for _, accrual := range result.Accruals {
		feeSource = mergeContributionFeeSource(feeSource, accrual.FeeSource)
		if accrual.ActualFee != nil {
			actualFee += *accrual.ActualFee
		}
		if accrual.EstimatedFee != nil {
			estimatedFee += *accrual.EstimatedFee
		}
	}
	item := SecurityContribution{
		SecurityID:        reverseRepoSecurityID,
		Symbol:            reverseRepoSymbol,
		Exchange:          string(trading.ExchangeSH),
		Name:              "上交所质押式回购",
		InstrumentType:    "reverse_repo",
		StrategyType:      StrategyCashManagement,
		SellQuantity:      int64(math.Round(result.Principal / reverseRepoCashMultiple)),
		SellAmount:        result.Principal,
		Turnover:          result.Principal,
		ActualFee:         roundMoney(actualFee),
		EstimatedFee:      roundMoney(estimatedFee),
		EffectiveFee:      result.EffectiveFee,
		FeeSource:         firstNonBlank(feeSource, "missing"),
		GrossContribution: floatPointer(gross),
		NetContribution:   floatPointer(net),
		ContributionBPS:   contributionBPSPointer(net, openNAV),
		PnLStatus:         "calculated",
		EstimationMethod:  "actual_occupation_days_interest",
		Orders:            result.Orders,
		Fills:             result.Fills,
		QualityFlags:      result.QualityFlags,
	}
	return item, true
}

func calculateContributionFillFee(fill trading.Fill, instrument contributionInstrument, rules []ledger.FeeRule) contributionFee {
	feeSource := strings.TrimSpace(contributionString(fill.AdapterContext["fee_source"]))
	if feeSource != "unavailable" && (fill.Fee > 0 || contributionBool(fill.AdapterContext["fee_complete"])) {
		source := "actual"
		if feeSource != "" {
			source = "actual_fill:" + feeSource
		}
		return contributionFee{
			actual:    fill.Fee,
			effective: fill.Fee,
			source:    source,
		}
	}
	rule, ok := selectContributionFeeRule(fill, instrument, rules)
	if !ok {
		return contributionFee{source: "missing", flags: []string{"missing_fee_rule"}}
	}
	amount := contributionFillAmount(fill)
	commission := amount * rule.CommissionRate
	if rule.CommissionRate > 0 && commission < rule.MinimumCommission {
		commission = rule.MinimumCommission
	}
	fee := commission + amount*(rule.TransferFeeRate+rule.HandlingFeeRate+rule.OtherRate) + rule.FixedFee
	if fill.TradeSide == trading.TradeSideSell || fill.TradeSide == trading.TradeSideRedemption {
		fee += amount * rule.StampDutyRate
	}
	return contributionFee{
		estimated: fee,
		effective: fee,
		source:    "estimated",
		flags:     []string{"estimated_fee_from_account_rule"},
	}
}

func contributionFeeForFill(
	fill trading.Fill,
	instrument contributionInstrument,
	rules []ledger.FeeRule,
	authoritativeFees map[string]ledger.OrderFeeRecord,
	consumedOrderFees map[string]bool,
) contributionFee {
	gatewayOrderID := strings.TrimSpace(fill.GatewayOrderID)
	if record, ok := authoritativeFees[gatewayOrderID]; ok {
		if consumedOrderFees[gatewayOrderID] {
			return contributionFee{}
		}
		consumedOrderFees[gatewayOrderID] = true
		return contributionFee{
			actual:    record.TotalFee,
			effective: record.TotalFee,
			source:    "actual_order_fee:" + record.FeeSource,
		}
	}
	return calculateContributionFillFee(fill, instrument, rules)
}

func selectContributionFeeRule(fill trading.Fill, instrument contributionInstrument, rules []ledger.FeeRule) (ledger.FeeRule, bool) {
	bestScore := -1
	var best ledger.FeeRule
	for _, rule := range rules {
		score := 0
		matches := []struct {
			ruleValue string
			value     string
		}{
			{rule.Market, instrument.Exchange},
			{rule.InstrumentType, instrument.InstrumentType},
			{rule.BusinessType, string(fill.BusinessType)},
			{rule.TradeSide, string(fill.TradeSide)},
		}
		valid := true
		for _, match := range matches {
			ruleValue := strings.TrimSpace(strings.ToLower(match.ruleValue))
			value := strings.TrimSpace(strings.ToLower(match.value))
			switch {
			case ruleValue == "" || ruleValue == "*":
			case ruleValue == value:
				score++
			default:
				valid = false
			}
		}
		if valid && score > bestScore {
			best = rule
			bestScore = score
		}
	}
	return best, bestScore >= 0
}

func finalizeContributionResult(result *ContributionResult) {
	sort.SliceStable(result.Contributions, func(i, j int) bool {
		return math.Abs(contributionNetValue(result.Contributions[i])) > math.Abs(contributionNetValue(result.Contributions[j]))
	})
	strategies := make(map[string]*StrategyContribution)
	for index := range result.Contributions {
		item := &result.Contributions[index]
		if result.Summary.CloseEconomicNAV > 0 {
			item.Weight = roundRatio(item.MarketValue / result.Summary.CloseEconomicNAV)
		}
		result.Summary.BuyAmount += item.BuyAmount
		result.Summary.SellAmount += item.SellAmount
		result.Summary.Turnover += item.Turnover
		result.Summary.OpenPositionValue += item.OpenValue
		result.Summary.ClosePositionValue += item.CloseValue
		result.Summary.ActualFee += item.ActualFee
		result.Summary.EstimatedFee += item.EstimatedFee
		result.Summary.EffectiveFee += item.EffectiveFee
		if containsStringValue(item.QualityFlags, "missing_fee_rule") {
			result.Summary.MissingFeeItems++
		}

		strategy := strategies[item.StrategyType]
		if strategy == nil {
			strategy = &StrategyContribution{StrategyType: item.StrategyType}
			strategies[item.StrategyType] = strategy
		}
		strategy.Securities++
		strategy.BuyAmount += item.BuyAmount
		strategy.SellAmount += item.SellAmount
		strategy.Turnover += item.Turnover
		strategy.EffectiveFee += item.EffectiveFee
		strategy.QualityFlags = appendUnique(strategy.QualityFlags, item.QualityFlags...)

		switch item.PnLStatus {
		case "estimated":
			result.Summary.EstimatedItems++
			strategy.EstimatedItems++
		case "missing":
			result.Summary.MissingItems++
			strategy.MissingItems++
		case "excluded":
			result.Summary.ExcludedItems++
			strategy.ExcludedItems++
		}
		if item.GrossContribution != nil {
			result.Summary.GrossContribution += *item.GrossContribution
		}
		if item.NetContribution != nil {
			result.Summary.NetContribution += *item.NetContribution
			strategy.NetContribution += *item.NetContribution
		}
	}
	result.Summary.Securities = len(result.Contributions)
	result.Summary.BuyAmount = roundMoney(result.Summary.BuyAmount)
	result.Summary.SellAmount = roundMoney(result.Summary.SellAmount)
	result.Summary.Turnover = roundMoney(result.Summary.Turnover)
	result.Summary.OpenPositionValue = roundMoney(result.Summary.OpenPositionValue)
	result.Summary.ClosePositionValue = roundMoney(result.Summary.ClosePositionValue)
	result.Summary.ActualFee = roundMoney(result.Summary.ActualFee)
	result.Summary.EstimatedFee = roundMoney(result.Summary.EstimatedFee)
	result.Summary.EffectiveFee = roundMoney(result.Summary.EffectiveFee)
	result.Summary.GrossContribution = roundMoney(result.Summary.GrossContribution)
	result.Summary.NetContribution = roundMoney(result.Summary.NetContribution)
	if result.Summary.OpenEconomicNAV > 0 {
		result.Summary.ContributionBPS = roundRatio(result.Summary.NetContribution / result.Summary.OpenEconomicNAV * 10000)
	}

	keys := make([]string, 0, len(strategies))
	for key := range strategies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		strategy := strategies[key]
		strategy.BuyAmount = roundMoney(strategy.BuyAmount)
		strategy.SellAmount = roundMoney(strategy.SellAmount)
		strategy.Turnover = roundMoney(strategy.Turnover)
		strategy.EffectiveFee = roundMoney(strategy.EffectiveFee)
		strategy.NetContribution = roundMoney(strategy.NetContribution)
		if result.Summary.OpenEconomicNAV > 0 {
			strategy.ContributionBPS = roundRatio(strategy.NetContribution / result.Summary.OpenEconomicNAV * 10000)
		}
		result.Strategies = append(result.Strategies, *strategy)
	}
}

func filterContributionOrders(orders []trading.Order, consumed map[string]bool) []trading.Order {
	items := make([]trading.Order, 0, len(orders))
	for _, order := range orders {
		if !consumed[order.GatewayOrderID] {
			items = append(items, order)
		}
	}
	return items
}

func filterContributionFills(fills []trading.Fill, consumed map[string]bool) []trading.Fill {
	items := make([]trading.Fill, 0, len(fills))
	for _, fill := range fills {
		if !consumed[contributionFillKey(fill)] {
			items = append(items, fill)
		}
	}
	return items
}

func contributionFillKey(fill trading.Fill) string {
	return strings.Join([]string{fill.AccountID, fill.GatewayOrderID, fill.FillID}, "\x00")
}

func contributionFillAmount(fill trading.Fill) float64 {
	return roundMoney(fill.Price * float64(fill.Qty))
}

func allContributionFillsSell(fills []trading.Fill) bool {
	if len(fills) == 0 {
		return false
	}
	for _, fill := range fills {
		if fill.TradeSide != trading.TradeSideSell {
			return false
		}
	}
	return true
}

func contributionStrategyType(instrumentType string) string {
	switch {
	case isETFInstrument(instrumentType):
		return StrategyETFCrossSection
	case isStockInstrument(instrumentType):
		return StrategyStockCrossSection
	default:
		return StrategyUnattributed
	}
}

func dominantContributionStrategy(orders []trading.Order, fills []trading.Fill) string {
	counts := make(map[string]int)
	for _, order := range orders {
		if value := strings.TrimSpace(order.StrategyType); value != "" {
			counts[value]++
		}
	}
	for _, fill := range fills {
		if value := strings.TrimSpace(fill.StrategyType); value != "" {
			counts[value]++
		}
	}
	return dominantContributionValue(counts)
}

func dominantContributionStrategyID(orders []trading.Order, fills []trading.Fill) string {
	counts := make(map[string]int)
	for _, order := range orders {
		if value := strings.TrimSpace(order.StrategyID); value != "" {
			counts[value]++
		}
	}
	for _, fill := range fills {
		if value := strings.TrimSpace(fill.StrategyID); value != "" {
			counts[value]++
		}
	}
	return dominantContributionValue(counts)
}

func dominantContributionValue(counts map[string]int) string {
	best := ""
	bestCount := 0
	for value, count := range counts {
		if count > bestCount || (count == bestCount && value < best) {
			best = value
			bestCount = count
		}
	}
	return best
}

func isRedemptionFill(fill trading.Fill) bool {
	return fill.TradeSide == trading.TradeSideRedemption ||
		isETFBusinessRedemption(fill.TradeSide, fill.BusinessType) ||
		strings.EqualFold(contributionString(fill.AdapterContext["trade_side"]), string(trading.TradeSideRedemption))
}

func isETFBusinessRedemption(side trading.TradeSide, businessType trading.BusinessType) bool {
	return side == trading.TradeSideRedemption && businessType == trading.BusinessTypeETF
}

func isETFInstrument(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "etf") || strings.Contains(value, "fund")
}

func isStockInstrument(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "stock") || strings.Contains(value, "equity")
}

func contributionOrderTime(order trading.Order) time.Time {
	for _, value := range []time.Time{order.AcceptedAt, order.CreatedAt, order.InsertedAt, order.LastUpdatedAt} {
		if !value.IsZero() {
			return value.In(timeutil.Location())
		}
	}
	return time.Time{}
}

func contributionFillTime(fill trading.Fill) time.Time {
	if !fill.MatchedAt.IsZero() {
		return fill.MatchedAt.In(timeutil.Location())
	}
	if fill.MatchTimestamp > 0 {
		timestamp := fill.MatchTimestamp
		if timestamp > 10_000_000_000 {
			return time.UnixMilli(timestamp).In(timeutil.Location())
		}
		return time.Unix(timestamp, 0).In(timeutil.Location())
	}
	return time.Time{}
}

func contributionRows(payload map[string]any) []map[string]any {
	raw, ok := payload["data"]
	if !ok {
		return nil
	}
	switch values := raw.(type) {
	case []any:
		rows := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if row, ok := value.(map[string]any); ok {
				rows = append(rows, row)
			}
		}
		return rows
	case []map[string]any:
		return values
	case map[string]any:
		for _, key := range []string{"items", "rows", "data"} {
			if nested, ok := values[key]; ok {
				return contributionRows(map[string]any{"data": nested})
			}
		}
		return []map[string]any{values}
	default:
		return nil
	}
}

func contributionFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func contributionString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func contributionBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed
	default:
		return false
	}
}

func firstContributionString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := contributionString(row[key]); value != "" {
			return value
		}
	}
	return ""
}

func contributionMarketTime(row map[string]any, tradeDate string) (time.Time, bool) {
	for _, key := range []string{"timestamp", "event_time", "quote_time", "datetime"} {
		value := strings.TrimSpace(contributionString(row[key]))
		if value == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
			if parsed, err := time.ParseInLocation(layout, value, timeutil.Location()); err == nil {
				return parsed.In(timeutil.Location()), true
			}
		}
	}
	for _, key := range []string{"time", "update_time"} {
		value := strings.TrimSpace(contributionString(row[key]))
		if value == "" {
			continue
		}
		date := tradeDate
		if normalized, _, err := parseTradeDate(tradeDate); err == nil {
			date = normalized
		}
		for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
			if parsed, err := time.ParseInLocation(layout, date+" "+value, timeutil.Location()); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

func splitContributionSecurityID(securityID string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(securityID), ".", 2)
	if len(parts) == 2 {
		return parts[0], strings.ToUpper(parts[1])
	}
	return securityID, ""
}

func floatPointer(value float64) *float64 {
	value = roundMoney(value)
	return &value
}

func contributionBPSPointer(value, openNAV float64) *float64 {
	if openNAV <= 0 {
		return nil
	}
	bps := roundRatio(value / openNAV * 10000)
	return &bps
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func mergeContributionFeeSource(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" || current == next {
		return current
	}
	return "mixed"
}

func contributionNetValue(item SecurityContribution) float64 {
	if item.NetContribution == nil {
		return 0
	}
	return *item.NetContribution
}
