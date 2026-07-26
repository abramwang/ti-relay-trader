package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const (
	reverseRepoSymbol       = "204001"
	reverseRepoSecurityID   = "204001.SH"
	reverseRepoCashMultiple = 100.0
	reverseRepoYearDays     = 365.0
	fillPageLimit           = 500
	maxFillPages            = 40
	maxCalendarSearchDays   = 20
)

type Store interface {
	ListFills(ctx context.Context, query trading.FillQuery) ([]trading.Fill, error)
	CreateFeeRule(ctx context.Context, rule ledger.FeeRule) (ledger.FeeRule, error)
	ListFeeRules(ctx context.Context, query ledger.FeeRuleQuery) ([]ledger.FeeRule, error)
	EffectiveRepoFeeRule(ctx context.Context, accountID, tradeDate string) (ledger.FeeRule, error)
	CreateCashLedgerEntry(ctx context.Context, entry ledger.CashLedgerEntry) (ledger.CashLedgerEntry, error)
	ListCashLedgerEntries(ctx context.Context, query ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error)
	ConfirmCashLedgerEntry(ctx context.Context, accountID, entryID, operator string, confirmedAt time.Time) (ledger.CashLedgerEntry, error)
	VoidCashLedgerEntry(ctx context.Context, accountID, entryID, operator string, voidedAt time.Time) (ledger.CashLedgerEntry, error)
	CreateNavBaseline(ctx context.Context, baseline ledger.NavBaseline) (ledger.NavBaseline, error)
	ListNavBaselines(ctx context.Context, accountID string) ([]ledger.NavBaseline, error)
	UpsertReverseRepoAccrual(ctx context.Context, accrual ledger.ReverseRepoAccrual) error
	ListReverseRepoAccruals(ctx context.Context, accountID, tradeDate string) ([]ledger.ReverseRepoAccrual, error)
	ListPerformanceNAVs(ctx context.Context, accountID, dateFrom, dateTo string) ([]ledger.PerformanceNAV, error)
	ListNAVReconciliations(ctx context.Context, accountID, dateFrom, dateTo string) ([]ledger.NAVReconciliation, error)
}

type TradingCalendar interface {
	TradingDayStatus(ctx context.Context, date string) (market.TradingDayStatus, error)
}

type Options struct {
	Store    Store
	Calendar TradingCalendar
	Now      func() time.Time
}

type Service struct {
	store    Store
	calendar TradingCalendar
	now      func() time.Time
	ids      atomic.Uint64
}

type ReverseRepoResult struct {
	AccountID          string                      `json:"account_id"`
	TradeDate          string                      `json:"trade_date"`
	SecurityID         string                      `json:"security_id"`
	FirstSettlement    string                      `json:"first_settlement_date,omitempty"`
	MaturitySettlement string                      `json:"maturity_settlement_date,omitempty"`
	OccupationDays     int                         `json:"actual_occupation_days,omitempty"`
	Accruals           []ledger.ReverseRepoAccrual `json:"accruals"`
	Orders             int                         `json:"orders"`
	Fills              int                         `json:"fills"`
	Principal          float64                     `json:"principal"`
	GrossInterest      float64                     `json:"gross_interest"`
	EffectiveFee       float64                     `json:"effective_fee"`
	NetInterest        float64                     `json:"net_interest"`
	Receivable         float64                     `json:"receivable"`
	Persisted          bool                        `json:"persisted"`
	QualityFlags       []string                    `json:"quality_flags,omitempty"`
}

type repoFillGroup struct {
	gatewayOrderID string
	fills          []trading.Fill
}

func New(options Options) (*Service, error) {
	if options.Store == nil {
		return nil, errors.New("performance store is required")
	}
	if options.Now == nil {
		options.Now = timeutil.Now
	}
	return &Service{
		store:    options.Store,
		calendar: options.Calendar,
		now:      options.Now,
	}, nil
}

func (service *Service) CreateFeeRule(ctx context.Context, rule ledger.FeeRule) (ledger.FeeRule, error) {
	if strings.TrimSpace(rule.RuleID) == "" {
		rule.RuleID = service.newID("fee-rule")
	}
	return service.store.CreateFeeRule(ctx, rule)
}

func (service *Service) ListFeeRules(ctx context.Context, query ledger.FeeRuleQuery) ([]ledger.FeeRule, error) {
	return service.store.ListFeeRules(ctx, query)
}

func (service *Service) CreateCashLedgerEntry(ctx context.Context, entry ledger.CashLedgerEntry) (ledger.CashLedgerEntry, error) {
	if strings.TrimSpace(entry.EntryID) == "" {
		entry.EntryID = service.newID("cash")
	}
	return service.store.CreateCashLedgerEntry(ctx, entry)
}

func (service *Service) ListCashLedgerEntries(ctx context.Context, query ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error) {
	return service.store.ListCashLedgerEntries(ctx, query)
}

func (service *Service) ConfirmCashLedgerEntry(ctx context.Context, accountID, entryID, operator string) (ledger.CashLedgerEntry, error) {
	return service.store.ConfirmCashLedgerEntry(ctx, accountID, entryID, operator, service.now())
}

func (service *Service) VoidCashLedgerEntry(ctx context.Context, accountID, entryID, operator string) (ledger.CashLedgerEntry, error) {
	return service.store.VoidCashLedgerEntry(ctx, accountID, entryID, operator, service.now())
}

func (service *Service) CreateNavBaseline(ctx context.Context, baseline ledger.NavBaseline) (ledger.NavBaseline, error) {
	if strings.TrimSpace(baseline.BaselineID) == "" {
		baseline.BaselineID = service.newID("nav-base")
	}
	return service.store.CreateNavBaseline(ctx, baseline)
}

func (service *Service) ListNavBaselines(ctx context.Context, accountID string) ([]ledger.NavBaseline, error) {
	return service.store.ListNavBaselines(ctx, accountID)
}

func (service *Service) ListPerformanceNAVs(ctx context.Context, accountID, dateFrom, dateTo string) ([]ledger.PerformanceNAV, error) {
	return service.store.ListPerformanceNAVs(ctx, accountID, dateFrom, dateTo)
}

func (service *Service) ListNAVReconciliations(ctx context.Context, accountID, dateFrom, dateTo string) ([]ledger.NAVReconciliation, error) {
	return service.store.ListNAVReconciliations(ctx, accountID, dateFrom, dateTo)
}

func (service *Service) ListReverseRepoAccruals(ctx context.Context, accountID, tradeDate string) ([]ledger.ReverseRepoAccrual, error) {
	return service.store.ListReverseRepoAccruals(ctx, accountID, tradeDate)
}

func (service *Service) CalculateReverseRepo(ctx context.Context, accountID, tradeDate string, persist bool) (ReverseRepoResult, error) {
	accountID = strings.TrimSpace(accountID)
	normalizedDate, parsedDate, err := parseTradeDate(tradeDate)
	if err != nil {
		return ReverseRepoResult{}, err
	}
	if accountID == "" {
		return ReverseRepoResult{}, errors.New("account_id is required")
	}

	fills, err := service.listTradeDateFills(ctx, accountID, normalizedDate)
	if err != nil {
		return ReverseRepoResult{}, err
	}
	groups := reverseRepoGroups(fills)
	result := ReverseRepoResult{
		AccountID:  accountID,
		TradeDate:  normalizedDate,
		SecurityID: reverseRepoSecurityID,
		Accruals:   make([]ledger.ReverseRepoAccrual, 0, len(groups)),
	}
	if len(groups) == 0 {
		return result, nil
	}
	if service.calendar == nil {
		return ReverseRepoResult{}, errors.New("meridian trading calendar is unavailable")
	}

	firstSettlement, err := service.nextTradingDay(ctx, parsedDate)
	if err != nil {
		return ReverseRepoResult{}, fmt.Errorf("resolve first settlement date: %w", err)
	}
	maturitySettlement, err := service.nextTradingDay(ctx, firstSettlement)
	if err != nil {
		return ReverseRepoResult{}, fmt.Errorf("resolve maturity settlement date: %w", err)
	}
	occupationDays := int(maturitySettlement.Sub(firstSettlement).Hours() / 24)
	if occupationDays <= 0 {
		return ReverseRepoResult{}, fmt.Errorf("invalid reverse repo occupation days %d", occupationDays)
	}
	result.FirstSettlement = firstSettlement.Format("2006-01-02")
	result.MaturitySettlement = maturitySettlement.Format("2006-01-02")
	result.OccupationDays = occupationDays

	rule, ruleErr := service.store.EffectiveRepoFeeRule(ctx, accountID, normalizedDate)
	if ruleErr != nil && !errors.Is(ruleErr, sql.ErrNoRows) {
		return ReverseRepoResult{}, ruleErr
	}

	for _, group := range groups {
		accrual := calculateRepoGroup(accountID, normalizedDate, occupationDays, firstSettlement, maturitySettlement, group, rule, ruleErr == nil, service.now())
		if persist {
			if err := service.store.UpsertReverseRepoAccrual(ctx, accrual); err != nil {
				return ReverseRepoResult{}, err
			}
		}
		result.Accruals = append(result.Accruals, accrual)
		result.Orders++
		result.Fills += len(group.fills)
		result.Principal += accrual.Principal
		result.GrossInterest += accrual.GrossInterest
		result.EffectiveFee += accrual.EffectiveFee
		result.NetInterest += accrual.NetInterest
		result.Receivable += accrual.Receivable
		result.QualityFlags = appendUnique(result.QualityFlags, accrual.QualityFlags...)
	}
	result.Persisted = persist
	result.Principal = roundMoney(result.Principal)
	result.GrossInterest = roundMoney(result.GrossInterest)
	result.EffectiveFee = roundMoney(result.EffectiveFee)
	result.NetInterest = roundMoney(result.NetInterest)
	result.Receivable = roundMoney(result.Receivable)
	return result, nil
}

func (service *Service) listTradeDateFills(ctx context.Context, accountID, tradeDate string) ([]trading.Fill, error) {
	items := make([]trading.Fill, 0)
	for page := 0; page < maxFillPages; page++ {
		batch, err := service.store.ListFills(ctx, trading.FillQuery{
			AccountID: accountID,
			TradeDate: tradeDate,
			History:   true,
			Limit:     fillPageLimit,
			Cursor:    strconv.Itoa(page * fillPageLimit),
		})
		if err != nil {
			return nil, fmt.Errorf("list reverse repo fills: %w", err)
		}
		items = append(items, batch...)
		if len(batch) < fillPageLimit {
			return items, nil
		}
	}
	return nil, fmt.Errorf("reverse repo fill scan exceeded %d rows", fillPageLimit*maxFillPages)
}

func (service *Service) nextTradingDay(ctx context.Context, after time.Time) (time.Time, error) {
	for day := 1; day <= maxCalendarSearchDays; day++ {
		candidate := after.AddDate(0, 0, day)
		status, err := service.calendar.TradingDayStatus(ctx, candidate.Format("20060102"))
		if err != nil {
			return time.Time{}, err
		}
		if status.IsTradingDayKnown && status.IsTradingDay {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("no trading day found after %s", after.Format("2006-01-02"))
}

func reverseRepoGroups(fills []trading.Fill) []repoFillGroup {
	grouped := make(map[string][]trading.Fill)
	for _, fill := range fills {
		if strings.TrimSpace(fill.Symbol) != reverseRepoSymbol ||
			fill.Exchange != trading.ExchangeSH ||
			fill.TradeSide != trading.TradeSideSell ||
			fill.Qty <= 0 ||
			fill.Price < 0 {
			continue
		}
		key := strings.TrimSpace(fill.GatewayOrderID)
		if key == "" {
			key = "fill:" + strings.TrimSpace(fill.FillID)
		}
		grouped[key] = append(grouped[key], fill)
	}

	groups := make([]repoFillGroup, 0, len(grouped))
	for key, entries := range grouped {
		hasActual := false
		for _, fill := range entries {
			if !strings.HasPrefix(strings.TrimSpace(fill.FillID), "relay-summary:") {
				hasActual = true
				break
			}
		}
		filtered := make([]trading.Fill, 0, len(entries))
		for _, fill := range entries {
			if hasActual && strings.HasPrefix(strings.TrimSpace(fill.FillID), "relay-summary:") {
				continue
			}
			filtered = append(filtered, fill)
		}
		if len(filtered) > 0 {
			groups = append(groups, repoFillGroup{gatewayOrderID: key, fills: filtered})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].gatewayOrderID < groups[j].gatewayOrderID
	})
	return groups
}

func calculateRepoGroup(
	accountID string,
	tradeDate string,
	occupationDays int,
	firstSettlement time.Time,
	maturitySettlement time.Time,
	group repoFillGroup,
	rule ledger.FeeRule,
	hasRule bool,
	calculatedAt time.Time,
) ledger.ReverseRepoAccrual {
	var qtyTotal int64
	var rateQty float64
	var principal float64
	var grossInterest float64
	var actualFee float64
	hasActualFee := false
	fillIDs := make([]string, 0, len(group.fills))
	for _, fill := range group.fills {
		fillPrincipal := float64(fill.Qty) * reverseRepoCashMultiple
		qtyTotal += fill.Qty
		rateQty += fill.Price * float64(fill.Qty)
		principal += fillPrincipal
		grossInterest += fillPrincipal * (fill.Price / 100) * float64(occupationDays) / reverseRepoYearDays
		if fill.Fee != 0 {
			actualFee += fill.Fee
			hasActualFee = true
		}
		fillIDs = append(fillIDs, fill.FillID)
	}
	weightedRate := 0.0
	if qtyTotal > 0 {
		weightedRate = rateQty / float64(qtyTotal)
	}

	accrual := ledger.ReverseRepoAccrual{
		AccountID:              accountID,
		TradeDate:              tradeDate,
		GatewayOrderID:         group.gatewayOrderID,
		SecurityID:             reverseRepoSecurityID,
		Principal:              roundMoney(principal),
		WeightedRatePct:        weightedRate,
		ActualOccupationDays:   occupationDays,
		FirstSettlementDate:    firstSettlement.Format("2006-01-02"),
		MaturitySettlementDate: maturitySettlement.Format("2006-01-02"),
		GrossInterest:          roundMoney(grossInterest),
		Status:                 "estimated",
		CalculatedAt:           calculatedAt,
		SourcePayload: map[string]any{
			"fill_ids":        fillIDs,
			"fill_count":      len(group.fills),
			"cash_multiplier": reverseRepoCashMultiple,
			"year_days":       reverseRepoYearDays,
		},
	}
	switch {
	case hasActualFee:
		fee := roundMoney(actualFee)
		accrual.ActualFee = &fee
		accrual.EffectiveFee = fee
		accrual.FeeSource = "actual_fill"
	case hasRule:
		fee := roundMoney(principal * rule.RepoFeeRate)
		accrual.EstimatedFee = &fee
		accrual.EffectiveFee = fee
		accrual.FeeSource = "fee_rule:" + rule.RuleID
	default:
		accrual.FeeSource = "missing"
		accrual.QualityFlags = append(accrual.QualityFlags, "missing_repo_fee")
	}
	accrual.NetInterest = roundMoney(accrual.GrossInterest - accrual.EffectiveFee)
	accrual.Receivable = roundMoney(accrual.Principal + accrual.NetInterest)
	return accrual
}

func parseTradeDate(value string) (string, time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		parsed, err := time.ParseInLocation(layout, value, timeutil.Location())
		if err == nil {
			return parsed.Format("2006-01-02"), parsed, nil
		}
	}
	return "", time.Time{}, fmt.Errorf("invalid trade_date %q", value)
}

func (service *Service) newID(prefix string) string {
	sequence := service.ids.Add(1)
	return fmt.Sprintf("%s-%d-%d", prefix, service.now().UTC().UnixNano(), sequence)
}

func roundMoney(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}

func appendUnique(values []string, extras ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(extras))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range extras {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}
