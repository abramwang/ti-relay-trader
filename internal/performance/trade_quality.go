package performance

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const (
	tradeQualityFormulaVersion = "trade_quality.v3"
	maxTradeQualityAnomalies   = 500
	maxTerminalClockSkew       = 5 * time.Second
)

type TradeQualityResult struct {
	AccountID          string                `json:"account_id"`
	DateFrom           string                `json:"date_from"`
	DateTo             string                `json:"date_to"`
	FormulaVersion     string                `json:"formula_version"`
	Summary            TradeQualitySummary   `json:"summary"`
	StatusBreakdown    map[string]int        `json:"status_breakdown"`
	Anomalies          []TradeQualityAnomaly `json:"anomalies"`
	AnomaliesReturned  int                   `json:"anomalies_returned"`
	AnomaliesTruncated bool                  `json:"anomalies_truncated"`
	QualityFlags       []string              `json:"quality_flags,omitempty"`
	GeneratedAt        time.Time             `json:"generated_at"`
}

type TradeQualitySummary struct {
	Orders                      int     `json:"orders"`
	OrdersWithFills             int     `json:"orders_with_fills"`
	FullyFilledOrders           int     `json:"fully_filled_orders"`
	PartiallyFilledOrders       int     `json:"partially_filled_orders"`
	CancelledOrders             int     `json:"cancelled_orders"`
	RejectedOrders              int     `json:"rejected_orders"`
	RejectedOrdersWithReason    int     `json:"rejected_orders_with_reason"`
	RejectedOrdersMissingReason int     `json:"rejected_orders_missing_reason"`
	NonTerminalOrders           int     `json:"non_terminal_orders"`
	AbnormalOrders              int     `json:"abnormal_orders"`
	OrphanFillGroups            int     `json:"orphan_fill_groups"`
	AnomalyItems                int     `json:"anomaly_items"`
	Fills                       int     `json:"fills"`
	OrderQuantity               int64   `json:"order_quantity"`
	ExecutedQuantity            int64   `json:"executed_quantity"`
	FillQuantity                int64   `json:"fill_quantity"`
	ExecutedOrderRate           float64 `json:"executed_order_rate"`
	FullFillRate                float64 `json:"full_fill_rate"`
	QuantityFillRate            float64 `json:"quantity_fill_rate"`
	CancelRate                  float64 `json:"cancel_rate"`
	RejectRate                  float64 `json:"reject_rate"`
	Turnover                    float64 `json:"turnover"`
	Fee                         float64 `json:"fee"`
}

type TradeQualityAnomaly struct {
	TradeDate              string                `json:"trade_date,omitempty"`
	GatewayOrderID         string                `json:"gateway_order_id"`
	ClientOrderID          string                `json:"client_order_id,omitempty"`
	OrderStreamID          string                `json:"order_stream_id,omitempty"`
	SecurityID             string                `json:"security_id,omitempty"`
	Name                   string                `json:"name,omitempty"`
	TradeSide              trading.TradeSide     `json:"trade_side,omitempty"`
	BusinessType           trading.BusinessType  `json:"business_type,omitempty"`
	Status                 trading.OrderStatus   `json:"status,omitempty"`
	GatewayStatus          trading.GatewayStatus `json:"gateway_status,omitempty"`
	OrderQuantity          int64                 `json:"order_quantity"`
	ReportedFilledQuantity int64                 `json:"reported_filled_quantity"`
	LedgerFilledQuantity   int64                 `json:"ledger_filled_quantity"`
	FillQuantityDelta      int64                 `json:"fill_quantity_delta"`
	LeavesQuantity         int64                 `json:"leaves_quantity"`
	CancelledQuantity      int64                 `json:"cancelled_quantity"`
	InvalidQuantity        int64                 `json:"invalid_quantity"`
	RejectCode             trading.ErrorCode     `json:"reject_code,omitempty"`
	RejectMessage          string                `json:"reject_message,omitempty"`
	BrokerMessage          string                `json:"broker_message,omitempty"`
	AdapterStatusCode      int                   `json:"adapter_status_code,omitempty"`
	AdapterStatusName      string                `json:"adapter_status_name,omitempty"`
	Flags                  []string              `json:"flags"`
	CreatedAt              time.Time             `json:"created_at,omitempty"`
	TerminalAt             time.Time             `json:"terminal_at,omitempty"`
	LastUpdatedAt          time.Time             `json:"last_updated_at,omitempty"`
}

type qualityFillGroup struct {
	fills    []trading.Fill
	quantity int64
	turnover float64
	fee      float64
}

// CalculateTradeQuality summarizes execution outcomes using only the local
// order and fill ledgers. It never refreshes the broker counter.
func (service *Service) CalculateTradeQuality(ctx context.Context, accountID, dateFrom, dateTo string) (TradeQualityResult, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return TradeQualityResult{}, fmt.Errorf("%w: account_id is required", ledger.ErrInvalidLedgerInput)
	}
	normalizedFrom, normalizedTo, err := service.resolveTradeQualityRange(ctx, dateFrom, dateTo)
	if err != nil {
		return TradeQualityResult{}, err
	}
	orders, err := service.listTradeQualityOrders(ctx, accountID, normalizedFrom, normalizedTo)
	if err != nil {
		return TradeQualityResult{}, err
	}
	fills, err := service.listTradeQualityFills(ctx, accountID, normalizedFrom, normalizedTo)
	if err != nil {
		return TradeQualityResult{}, err
	}
	fills = dedupeContributionFills(fills)
	feeRecords, err := service.listTradeQualityOrderFees(ctx, accountID, normalizedFrom, normalizedTo)
	if err != nil {
		return TradeQualityResult{}, err
	}

	result := TradeQualityResult{
		AccountID:       accountID,
		DateFrom:        normalizedFrom,
		DateTo:          normalizedTo,
		FormulaVersion:  tradeQualityFormulaVersion,
		StatusBreakdown: make(map[string]int),
		Anomalies:       make([]TradeQualityAnomaly, 0),
		GeneratedAt:     service.now().In(timeutil.Location()),
	}
	fillGroups, fillGroupKeysByOrder := buildTradeQualityFillGroups(fills)
	authoritativeFees, feeKeysByOrder := buildTradeQualityOrderFees(feeRecords)
	usedFillGroups := make(map[string]bool)

	for _, order := range orders {
		status := strings.TrimSpace(string(order.Status))
		if status == "" {
			status = "unknown"
		}
		result.StatusBreakdown[status]++
		result.Summary.Orders++
		if order.OrderQty > 0 {
			result.Summary.OrderQuantity += order.OrderQty
		}

		orderDate := tradeQualityOrderDate(order)
		groupKey := tradeQualityKey(orderDate, order.GatewayOrderID)
		group, matchedKey := matchTradeQualityFillGroup(fillGroups, fillGroupKeysByOrder, groupKey, order.GatewayOrderID)
		if matchedKey != "" {
			usedFillGroups[matchedKey] = true
		}
		executedQuantity := group.quantity
		if order.OrderQty > 0 && executedQuantity > order.OrderQty {
			executedQuantity = order.OrderQty
		}
		if executedQuantity > 0 {
			result.Summary.OrdersWithFills++
			result.Summary.ExecutedQuantity += executedQuantity
		}

		fullyFilled := order.Status == trading.OrderStatusFilled ||
			(order.OrderQty > 0 && group.quantity >= order.OrderQty && order.LeavesQty == 0)
		if fullyFilled {
			result.Summary.FullyFilledOrders++
		} else if group.quantity > 0 {
			result.Summary.PartiallyFilledOrders++
		}
		switch order.Status {
		case trading.OrderStatusCancelled:
			result.Summary.CancelledOrders++
		case trading.OrderStatusRejected:
			result.Summary.RejectedOrders++
			if tradeQualityBrokerMessage(order) != "" {
				result.Summary.RejectedOrdersWithReason++
			} else {
				result.Summary.RejectedOrdersMissingReason++
			}
		}
		if !order.Status.Terminal() {
			result.Summary.NonTerminalOrders++
		}

		anomaly := tradeQualityOrderAnomaly(order, orderDate, group)
		if len(anomaly.Flags) > 0 {
			result.Summary.AbnormalOrders++
			result.Anomalies = append(result.Anomalies, anomaly)
			result.QualityFlags = appendUnique(result.QualityFlags, anomaly.Flags...)
		}
	}

	for key, group := range fillGroups {
		result.Summary.Fills += len(group.fills)
		result.Summary.FillQuantity += group.quantity
		result.Summary.Turnover += group.turnover
		effectiveFee := group.fee
		if orderFee, ok := matchTradeQualityOrderFee(authoritativeFees, feeKeysByOrder, key); ok {
			effectiveFee = orderFee.TotalFee
		}
		result.Summary.Fee += effectiveFee
		if usedFillGroups[key] {
			continue
		}
		result.Summary.OrphanFillGroups++
		result.QualityFlags = appendUnique(result.QualityFlags, "orphan_fill")
		result.Anomalies = append(result.Anomalies, tradeQualityOrphanFillAnomaly(key, group))
	}

	result.Summary.AnomalyItems = len(result.Anomalies)
	result.Summary.ExecutedOrderRate = qualityRate(result.Summary.OrdersWithFills, result.Summary.Orders)
	result.Summary.FullFillRate = qualityRate(result.Summary.FullyFilledOrders, result.Summary.Orders)
	result.Summary.QuantityFillRate = qualityRatio(result.Summary.ExecutedQuantity, result.Summary.OrderQuantity)
	result.Summary.CancelRate = qualityRate(result.Summary.CancelledOrders, result.Summary.Orders)
	result.Summary.RejectRate = qualityRate(result.Summary.RejectedOrders, result.Summary.Orders)
	result.Summary.Turnover = roundMoney(result.Summary.Turnover)
	result.Summary.Fee = roundMoney(result.Summary.Fee)
	if result.Summary.Orders == 0 {
		result.QualityFlags = appendUnique(result.QualityFlags, "no_orders")
	}
	sortTradeQualityAnomalies(result.Anomalies)
	if len(result.Anomalies) > maxTradeQualityAnomalies {
		result.Anomalies = result.Anomalies[:maxTradeQualityAnomalies]
		result.AnomaliesTruncated = true
		result.QualityFlags = appendUnique(result.QualityFlags, "anomalies_truncated")
	}
	result.AnomaliesReturned = len(result.Anomalies)
	return result, nil
}

func (service *Service) resolveTradeQualityRange(ctx context.Context, dateFrom, dateTo string) (string, string, error) {
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	if dateFrom == "" && dateTo == "" {
		tradeDate, err := service.resolveContributionDate(ctx, "")
		return tradeDate, tradeDate, err
	}
	if dateFrom == "" {
		dateFrom = dateTo
	}
	if dateTo == "" {
		dateTo = dateFrom
	}
	normalizedFrom, parsedFrom, err := parseTradeDate(dateFrom)
	if err != nil {
		return "", "", fmt.Errorf("%w: invalid date_from: %v", ledger.ErrInvalidLedgerInput, err)
	}
	normalizedTo, parsedTo, err := parseTradeDate(dateTo)
	if err != nil {
		return "", "", fmt.Errorf("%w: invalid date_to: %v", ledger.ErrInvalidLedgerInput, err)
	}
	if parsedFrom.After(parsedTo) {
		return "", "", fmt.Errorf("%w: date_from must not be after date_to", ledger.ErrInvalidLedgerInput)
	}
	return normalizedFrom, normalizedTo, nil
}

func (service *Service) listTradeQualityOrders(ctx context.Context, accountID, dateFrom, dateTo string) ([]trading.Order, error) {
	items := make([]trading.Order, 0)
	for page := 0; page < maxContributionPages; page++ {
		batch, err := service.store.ListOrders(ctx, trading.OrderQuery{
			AccountID: accountID,
			DateFrom:  dateFrom,
			DateTo:    dateTo,
			History:   true,
			Limit:     contributionPageLimit,
			Cursor:    strconv.Itoa(page * contributionPageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list trade quality orders: %w", err)
		}
		items = append(items, batch...)
		if len(batch) < contributionPageLimit {
			return items, nil
		}
	}
	return nil, fmt.Errorf("trade quality orders exceed %d rows", contributionPageLimit*maxContributionPages)
}

func (service *Service) listTradeQualityFills(ctx context.Context, accountID, dateFrom, dateTo string) ([]trading.Fill, error) {
	items := make([]trading.Fill, 0)
	for page := 0; page < maxContributionPages; page++ {
		batch, err := service.store.ListFills(ctx, trading.FillQuery{
			AccountID: accountID,
			DateFrom:  dateFrom,
			DateTo:    dateTo,
			History:   true,
			Limit:     contributionPageLimit,
			Cursor:    strconv.Itoa(page * contributionPageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list trade quality fills: %w", err)
		}
		items = append(items, batch...)
		if len(batch) < contributionPageLimit {
			return items, nil
		}
	}
	return nil, fmt.Errorf("trade quality fills exceed %d rows", contributionPageLimit*maxContributionPages)
}

func (service *Service) listTradeQualityOrderFees(ctx context.Context, accountID, dateFrom, dateTo string) ([]ledger.OrderFeeRecord, error) {
	items := make([]ledger.OrderFeeRecord, 0)
	for page := 0; page < maxFeePages; page++ {
		batch, err := service.store.ListOrderFeeRecords(ctx, ledger.OrderFeeRecordQuery{
			AccountID: accountID,
			DateFrom:  dateFrom,
			DateTo:    dateTo,
			Limit:     feePageLimit,
			Cursor:    strconv.Itoa(page * feePageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list trade quality order fees: %w", err)
		}
		items = append(items, batch...)
		if len(batch) < feePageLimit {
			return items, nil
		}
	}
	return nil, fmt.Errorf("trade quality order fees exceed %d rows", feePageLimit*maxFeePages)
}

func buildTradeQualityFillGroups(fills []trading.Fill) (map[string]qualityFillGroup, map[string][]string) {
	groups := make(map[string]qualityFillGroup)
	keysByOrder := make(map[string][]string)
	for _, fill := range fills {
		key := tradeQualityKey(tradeQualityFillDate(fill), fill.GatewayOrderID)
		group := groups[key]
		group.fills = append(group.fills, fill)
		if fill.Qty > 0 {
			group.quantity += fill.Qty
			group.turnover += math.Abs(fill.Price * float64(fill.Qty))
		}
		group.fee += fill.Fee
		groups[key] = group
		if !tradeQualityContainsString(keysByOrder[fill.GatewayOrderID], key) {
			keysByOrder[fill.GatewayOrderID] = append(keysByOrder[fill.GatewayOrderID], key)
		}
	}
	return groups, keysByOrder
}

func buildTradeQualityOrderFees(records []ledger.OrderFeeRecord) (map[string]ledger.OrderFeeRecord, map[string][]string) {
	fees := make(map[string]ledger.OrderFeeRecord)
	keysByOrder := make(map[string][]string)
	for _, record := range records {
		gatewayOrderID := strings.TrimSpace(record.GatewayOrderID)
		if gatewayOrderID == "" || !record.FeeComplete || !record.AssociationComplete || strings.TrimSpace(record.FeeSource) == "unavailable" {
			continue
		}
		key := tradeQualityKey(normalizedQualityDate(record.TradeDate), gatewayOrderID)
		current, exists := fees[key]
		if !exists || record.FeeAsOf.After(current.FeeAsOf) {
			fees[key] = record
		}
		if !tradeQualityContainsString(keysByOrder[gatewayOrderID], key) {
			keysByOrder[gatewayOrderID] = append(keysByOrder[gatewayOrderID], key)
		}
	}
	return fees, keysByOrder
}

func matchTradeQualityOrderFee(fees map[string]ledger.OrderFeeRecord, keysByOrder map[string][]string, fillGroupKey string) (ledger.OrderFeeRecord, bool) {
	if fee, ok := fees[fillGroupKey]; ok {
		return fee, true
	}
	_, gatewayOrderID, _ := strings.Cut(fillGroupKey, "\x00")
	if tradeQualityKeyDate(fillGroupKey) == "" && len(keysByOrder[gatewayOrderID]) == 1 {
		fee, ok := fees[keysByOrder[gatewayOrderID][0]]
		return fee, ok
	}
	return ledger.OrderFeeRecord{}, false
}

func matchTradeQualityFillGroup(groups map[string]qualityFillGroup, keysByOrder map[string][]string, exactKey, gatewayOrderID string) (qualityFillGroup, string) {
	if group, ok := groups[exactKey]; ok {
		return group, exactKey
	}
	keys := keysByOrder[gatewayOrderID]
	exactDate := tradeQualityKeyDate(exactKey)
	if exactDate == "" && len(keys) == 1 {
		return groups[keys[0]], keys[0]
	}
	if exactDate != "" {
		undatedKey := tradeQualityKey("", gatewayOrderID)
		if group, ok := groups[undatedKey]; ok {
			return group, undatedKey
		}
	}
	return qualityFillGroup{}, ""
}

func tradeQualityOrderAnomaly(order trading.Order, tradeDate string, fillGroup qualityFillGroup) TradeQualityAnomaly {
	flags := make([]string, 0)
	ledgerFilledQuantity := fillGroup.quantity
	brokerMessage := tradeQualityBrokerMessage(order)
	if order.Status == trading.OrderStatusRejected && brokerMessage == "" {
		flags = append(flags, "rejected_order_missing_reason")
	}
	if !order.Status.Terminal() {
		flags = append(flags, "non_terminal_order")
	}
	if order.OrderQty <= 0 {
		flags = append(flags, "invalid_order_quantity")
	}
	if order.InvalidQty > 0 {
		flags = append(flags, "invalid_quantity")
	}
	if order.CumFilledQty != ledgerFilledQuantity {
		flags = append(flags, "order_fill_quantity_mismatch")
	}
	if order.OrderQty > 0 && (order.CumFilledQty > order.OrderQty || ledgerFilledQuantity > order.OrderQty) {
		flags = append(flags, "filled_quantity_exceeds_order")
	}
	statusTerminal := order.Status.Terminal()
	if order.Status != "" && statusTerminal != order.IsTerminal {
		flags = append(flags, "terminal_flag_conflict")
	}
	if terminalGatewayConflict(order.Status, order.GatewayStatus) {
		flags = append(flags, "status_gateway_conflict")
	}
	if order.Status == trading.OrderStatusFilled &&
		(order.OrderQty <= 0 || maxInt64(order.CumFilledQty, ledgerFilledQuantity) < order.OrderQty || order.LeavesQty > 0) {
		flags = append(flags, "filled_status_quantity_conflict")
	}
	for _, fill := range fillGroup.fills {
		if strings.TrimSpace(fill.Symbol) != strings.TrimSpace(order.Symbol) || fill.Exchange != order.Exchange {
			flags = appendUnique(flags, "fill_order_security_mismatch")
		}
		if order.TradeSide != "" && fill.TradeSide != "" && fill.TradeSide != order.TradeSide {
			flags = appendUnique(flags, "fill_order_side_mismatch")
		}
		if order.BusinessType != "" && fill.BusinessType != "" && fill.BusinessType != order.BusinessType {
			flags = appendUnique(flags, "fill_order_business_type_mismatch")
		}
	}
	if order.IsTerminal && order.TerminalAt.IsZero() {
		flags = append(flags, "terminal_time_missing")
	}
	if !order.TerminalAt.IsZero() && !order.CreatedAt.IsZero() &&
		order.TerminalAt.Add(maxTerminalClockSkew).Before(order.CreatedAt) {
		flags = append(flags, "terminal_before_created")
	}
	if !order.TerminalAt.IsZero() && tradeDate != "" &&
		order.TerminalAt.In(timeutil.Location()).Format("2006-01-02") != tradeDate {
		flags = append(flags, "terminal_trade_date_mismatch")
	}
	if brokerMessage != "" && order.Status != trading.OrderStatusRejected {
		flags = append(flags, "broker_error_message")
	}
	return TradeQualityAnomaly{
		TradeDate:              tradeDate,
		GatewayOrderID:         order.GatewayOrderID,
		ClientOrderID:          order.ClientOrderID,
		OrderStreamID:          order.OrderStreamID,
		SecurityID:             contributionSecurityID(order.Symbol, order.Exchange),
		Name:                   order.Name,
		TradeSide:              order.TradeSide,
		BusinessType:           order.BusinessType,
		Status:                 order.Status,
		GatewayStatus:          order.GatewayStatus,
		OrderQuantity:          order.OrderQty,
		ReportedFilledQuantity: order.CumFilledQty,
		LedgerFilledQuantity:   ledgerFilledQuantity,
		FillQuantityDelta:      ledgerFilledQuantity - order.CumFilledQty,
		LeavesQuantity:         order.LeavesQty,
		CancelledQuantity:      order.CancelledQty,
		InvalidQuantity:        order.InvalidQty,
		RejectCode:             order.RejectCode,
		RejectMessage:          order.RejectMessage,
		BrokerMessage:          brokerMessage,
		AdapterStatusCode:      order.AdapterStatusCode,
		AdapterStatusName:      order.AdapterStatusName,
		Flags:                  flags,
		CreatedAt:              order.CreatedAt,
		TerminalAt:             order.TerminalAt,
		LastUpdatedAt:          tradeQualityOrderTime(order),
	}
}

func tradeQualityOrphanFillAnomaly(key string, group qualityFillGroup) TradeQualityAnomaly {
	item := TradeQualityAnomaly{
		Flags:                []string{"orphan_fill"},
		LedgerFilledQuantity: group.quantity,
		FillQuantityDelta:    group.quantity,
	}
	if len(group.fills) == 0 {
		return item
	}
	fill := group.fills[0]
	item.TradeDate = tradeQualityFillDate(fill)
	item.GatewayOrderID = fill.GatewayOrderID
	item.OrderStreamID = fill.OrderStreamID
	item.SecurityID = contributionSecurityID(fill.Symbol, fill.Exchange)
	item.Name = fill.Name
	item.TradeSide = fill.TradeSide
	item.BusinessType = fill.BusinessType
	item.LastUpdatedAt = fill.MatchedAt
	if item.GatewayOrderID == "" {
		item.GatewayOrderID = key
	}
	return item
}

func tradeQualityKey(tradeDate, gatewayOrderID string) string {
	return strings.TrimSpace(tradeDate) + "\x00" + strings.TrimSpace(gatewayOrderID)
}

func tradeQualityKeyDate(key string) string {
	date, _, _ := strings.Cut(key, "\x00")
	return date
}

func tradeQualityOrderDate(order trading.Order) string {
	if value := normalizedQualityDate(order.TradeDate); value != "" {
		return value
	}
	return qualityDateFromTimes(order.LastUpdatedAt, order.CreatedAt, order.AcceptedAt, order.InsertedAt, order.TerminalAt)
}

func tradeQualityFillDate(fill trading.Fill) string {
	if value := normalizedQualityDate(fill.TradeDate); value != "" {
		return value
	}
	return qualityDateFromTimes(fill.MatchedAt)
}

func normalizedQualityDate(value string) string {
	normalized, _, err := parseTradeDate(value)
	if err != nil {
		return ""
	}
	return normalized
}

func qualityDateFromTimes(values ...time.Time) string {
	for _, value := range values {
		if !value.IsZero() {
			return value.In(timeutil.Location()).Format("2006-01-02")
		}
	}
	return ""
}

func tradeQualityOrderTime(order trading.Order) time.Time {
	for _, value := range []time.Time{order.LastUpdatedAt, order.TerminalAt, order.AcceptedAt, order.CreatedAt, order.InsertedAt} {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func terminalGatewayConflict(status trading.OrderStatus, gatewayStatus trading.GatewayStatus) bool {
	if status == "" || gatewayStatus == "" {
		return false
	}
	switch status {
	case trading.OrderStatusFilled:
		return gatewayStatus != trading.GatewayStatusFilled
	case trading.OrderStatusCancelled:
		return gatewayStatus != trading.GatewayStatusCancelled
	case trading.OrderStatusRejected:
		return gatewayStatus != trading.GatewayStatusRejected
	default:
		return false
	}
}

func tradeQualityBrokerMessage(order trading.Order) string {
	if value := strings.TrimSpace(order.RejectMessage); value != "" {
		return value
	}
	if order.Status != trading.OrderStatusRejected && !adapterStatusLooksError(order.AdapterStatusName) {
		return ""
	}
	return findAdapterMessage(order.AdapterContext, 0)
}

func adapterStatusLooksError(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, token := range []string{"reject", "fail", "error", "invalid", "拒绝", "失败", "错误", "无效"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func findAdapterMessage(value any, depth int) string {
	if depth > 3 {
		return ""
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"reject_message", "error_message", "err_msg", "errmsg", "status_message", "reason", "message", "msg"} {
			if item, ok := typed[key]; ok {
				if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
					return text
				}
			}
		}
		for _, item := range typed {
			if text := findAdapterMessage(item, depth+1); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range typed {
			if text := findAdapterMessage(item, depth+1); text != "" {
				return text
			}
		}
	}
	return ""
}

func sortTradeQualityAnomalies(items []TradeQualityAnomaly) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].TradeDate != items[j].TradeDate {
			return items[i].TradeDate > items[j].TradeDate
		}
		if !items[i].LastUpdatedAt.Equal(items[j].LastUpdatedAt) {
			return items[i].LastUpdatedAt.After(items[j].LastUpdatedAt)
		}
		return items[i].GatewayOrderID > items[j].GatewayOrderID
	})
}

func qualityRate(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return math.Round(float64(numerator)/float64(denominator)*1e8) / 1e8
}

func qualityRatio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return math.Round(float64(numerator)/float64(denominator)*1e8) / 1e8
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func tradeQualityContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
