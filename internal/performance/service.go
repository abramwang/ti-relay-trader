package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
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
	defaultFormulaVersion   = "performance_economic_nav.v2.1"
	defaultAutoToleranceCNY = 50.0
	defaultAutoToleranceBP  = 0.1
	defaultWarnToleranceCNY = 500.0
	defaultWarnToleranceBP  = 1.0
)

type Store interface {
	ListOrders(ctx context.Context, query trading.OrderQuery) ([]trading.Order, error)
	ListFills(ctx context.Context, query trading.FillQuery) ([]trading.Fill, error)
	ListPositionSnapshots(ctx context.Context, query trading.PositionQuery) ([]trading.Position, error)
	GetDailyPerformance(ctx context.Context, accountID string, tradeDate string) (ledger.DailyPerformance, error)
	GetAssetPositionObservation(ctx context.Context, accountID string, tradeDate string, snapshotType string) (ledger.AssetPositionObservation, error)
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
	UpsertPerformanceNAV(ctx context.Context, nav ledger.PerformanceNAV) (ledger.PerformanceNAV, error)
	UpdatePerformanceNAVStatus(ctx context.Context, nav ledger.PerformanceNAV) (ledger.PerformanceNAV, error)
	ListNAVReconciliations(ctx context.Context, accountID, dateFrom, dateTo string) ([]ledger.NAVReconciliation, error)
	UpsertNAVReconciliation(ctx context.Context, item ledger.NAVReconciliation) (ledger.NAVReconciliation, error)
	GetPerformanceInception(ctx context.Context, accountID string) (ledger.PerformanceInception, error)
	UpsertPerformanceInception(ctx context.Context, item ledger.PerformanceInception) (ledger.PerformanceInception, error)
	ListPositionCostStates(ctx context.Context, query ledger.PositionCostStateQuery) ([]ledger.PositionCostState, error)
	UpsertPositionCostState(ctx context.Context, item ledger.PositionCostState) (ledger.PositionCostState, error)
}

type TradingCalendar interface {
	TradingDayStatus(ctx context.Context, date string) (market.TradingDayStatus, error)
}

type ContributionMarket interface {
	MetadataInstruments(ctx context.Context, values url.Values) (market.MeridianResponse, error)
	MarketBars(ctx context.Context, values url.Values) (market.MeridianResponse, error)
	MarketSnapshots(ctx context.Context, values url.Values) (market.MeridianResponse, error)
	MarketETFCashComponents(ctx context.Context, values url.Values) (market.MeridianResponse, error)
}

type Options struct {
	Store               Store
	Calendar            TradingCalendar
	Market              ContributionMarket
	Now                 func() time.Time
	FormulaVersion      string
	ETFT0FrictionRate   float64
	AutoToleranceCNY    float64
	AutoToleranceBP     float64
	WarningToleranceCNY float64
	WarningToleranceBP  float64
}

type Service struct {
	store               Store
	calendar            TradingCalendar
	market              ContributionMarket
	now                 func() time.Time
	formulaVersion      string
	etfT0FrictionRate   float64
	autoToleranceCNY    float64
	autoToleranceBP     float64
	warningToleranceCNY float64
	warningToleranceBP  float64
	ids                 atomic.Uint64
}

type EconomicNAVOptions struct {
	Persist bool   `json:"persist"`
	Status  string `json:"status,omitempty"`
}

type EconomicNAVCashFlowSummary struct {
	ExternalNetFlow    float64 `json:"external_net_flow"`
	SettlementAdjust   float64 `json:"settlement_adjustment"`
	IncomeExpense      float64 `json:"income_expense"`
	InternalTransfer   float64 `json:"internal_transfer"`
	ExternalFlowCount  int     `json:"external_flow_count"`
	SettlementCount    int     `json:"settlement_count"`
	IncomeExpenseCount int     `json:"income_expense_count"`
	InternalFlowCount  int     `json:"internal_flow_count"`
}

type EconomicNAVReverseRepoSummary struct {
	Orders                int     `json:"orders"`
	Principal             float64 `json:"principal"`
	PrincipalCashOverlap  float64 `json:"principal_cash_overlap"`
	PrincipalReceivable   float64 `json:"principal_receivable"`
	NetInterest           float64 `json:"net_interest"`
	EstimatedNetInterest  float64 `json:"estimated_net_interest"`
	RecognizedNetInterest float64 `json:"recognized_net_interest"`
	Receivable            float64 `json:"receivable"`
	EstimatedReceivable   float64 `json:"estimated_receivable"`
	Fee                   float64 `json:"fee"`
	Source                string  `json:"source,omitempty"`
	PrincipalTreatment    string  `json:"principal_treatment,omitempty"`
	InterestRecognition   string  `json:"interest_recognition,omitempty"`
	ResolutionResidual    float64 `json:"resolution_residual"`
	AlternateResidual     float64 `json:"alternate_residual"`
}

type EconomicNAVValuationSummary struct {
	OpenVisibleCash          float64 `json:"open_visible_cash"`
	OpenPositionValue        float64 `json:"open_position_value"`
	CloseVisibleCash         float64 `json:"close_visible_cash"`
	ClosePositionValue       float64 `json:"close_position_value"`
	BrokerOpenPositionValue  float64 `json:"broker_open_position_value"`
	BrokerClosePositionValue float64 `json:"broker_close_position_value"`
	PriceSource              string  `json:"price_source"`
	CostSource               string  `json:"cost_source"`
}

type EconomicNAVResult struct {
	AccountID        string                        `json:"account_id"`
	TradeDate        string                        `json:"trade_date"`
	Status           string                        `json:"status"`
	FormulaVersion   string                        `json:"formula_version"`
	Persisted        bool                          `json:"persisted"`
	NAV              ledger.PerformanceNAV         `json:"nav"`
	Reconciliation   ledger.NAVReconciliation      `json:"reconciliation,omitempty"`
	DailyPerformance ledger.DailyPerformance       `json:"daily_performance"`
	CashFlows        EconomicNAVCashFlowSummary    `json:"cash_flows"`
	ReverseRepo      EconomicNAVReverseRepoSummary `json:"reverse_repo"`
	Valuation        EconomicNAVValuationSummary   `json:"valuation"`
	QualityFlags     []string                      `json:"quality_flags,omitempty"`
}

type EconomicNAVReconcileOptions struct {
	Persist           bool   `json:"persist"`
	ObservedTradeDate string `json:"observed_trade_date,omitempty"`
}

type EconomicNAVReconcileResult struct {
	AccountID         string                          `json:"account_id"`
	TradeDate         string                          `json:"trade_date"`
	ObservedTradeDate string                          `json:"observed_trade_date"`
	Status            string                          `json:"status"`
	FormulaVersion    string                          `json:"formula_version"`
	Persisted         bool                            `json:"persisted"`
	NAV               ledger.PerformanceNAV           `json:"nav"`
	Reconciliation    ledger.NAVReconciliation        `json:"reconciliation"`
	Observation       ledger.AssetPositionObservation `json:"observation"`
	QualityFlags      []string                        `json:"quality_flags,omitempty"`
}

type NAVReconciliationReviewOptions struct {
	Action           string `json:"action,omitempty"`
	ReconciliationID string `json:"reconciliation_id,omitempty"`
	Operator         string `json:"operator,omitempty"`
	Note             string `json:"note,omitempty"`
	Force            bool   `json:"force,omitempty"`
}

type NAVReconciliationReviewResult struct {
	AccountID      string                   `json:"account_id"`
	TradeDate      string                   `json:"trade_date"`
	Action         string                   `json:"action"`
	Status         string                   `json:"status"`
	Persisted      bool                     `json:"persisted"`
	NAV            ledger.PerformanceNAV    `json:"nav"`
	Reconciliation ledger.NAVReconciliation `json:"reconciliation"`
	QualityFlags   []string                 `json:"quality_flags,omitempty"`
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
	if strings.TrimSpace(options.FormulaVersion) == "" {
		options.FormulaVersion = defaultFormulaVersion
	}
	if options.ETFT0FrictionRate <= 0 {
		options.ETFT0FrictionRate = 0.0015
	}
	if options.AutoToleranceCNY == 0 {
		options.AutoToleranceCNY = defaultAutoToleranceCNY
	}
	if options.AutoToleranceBP == 0 {
		options.AutoToleranceBP = defaultAutoToleranceBP
	}
	if options.WarningToleranceCNY == 0 {
		options.WarningToleranceCNY = defaultWarnToleranceCNY
	}
	if options.WarningToleranceBP == 0 {
		options.WarningToleranceBP = defaultWarnToleranceBP
	}
	return &Service{
		store:               options.Store,
		calendar:            options.Calendar,
		market:              options.Market,
		now:                 options.Now,
		formulaVersion:      strings.TrimSpace(options.FormulaVersion),
		etfT0FrictionRate:   options.ETFT0FrictionRate,
		autoToleranceCNY:    options.AutoToleranceCNY,
		autoToleranceBP:     options.AutoToleranceBP,
		warningToleranceCNY: options.WarningToleranceCNY,
		warningToleranceBP:  options.WarningToleranceBP,
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

func (service *Service) CalculateEconomicNAV(ctx context.Context, accountID, tradeDate string, options EconomicNAVOptions) (EconomicNAVResult, error) {
	accountID = strings.TrimSpace(accountID)
	normalizedDate, parsedDate, err := parseTradeDate(tradeDate)
	if err != nil {
		return EconomicNAVResult{}, err
	}
	if accountID == "" {
		return EconomicNAVResult{}, errors.New("account_id is required")
	}
	status := strings.TrimSpace(options.Status)
	if status == "" {
		status = "provisional"
	}
	switch status {
	case "provisional", "finalized", "blocked":
	default:
		return EconomicNAVResult{}, fmt.Errorf("invalid economic nav status %q", status)
	}

	daily, err := service.store.GetDailyPerformance(ctx, accountID, normalizedDate)
	if err != nil {
		return EconomicNAVResult{}, err
	}
	result := EconomicNAVResult{
		AccountID:        accountID,
		TradeDate:        normalizedDate,
		Status:           status,
		FormulaVersion:   service.formulaVersion,
		DailyPerformance: daily,
	}
	for _, flag := range daily.QualityFlags {
		if flag == "overnight_adjustment_unclassified" {
			continue
		}
		result.QualityFlags = appendUnique(result.QualityFlags, flag)
	}

	openEconomicNAV, baselineFlags, err := service.openEconomicNAV(ctx, accountID, normalizedDate, daily)
	if err != nil {
		return EconomicNAVResult{}, err
	}
	result.QualityFlags = appendUnique(result.QualityFlags, baselineFlags...)

	contribution, err := service.CalculateContributions(ctx, accountID, normalizedDate)
	if err != nil {
		return EconomicNAVResult{}, fmt.Errorf("calculate market valuation: %w", err)
	}
	for _, flag := range contribution.QualityFlags {
		if flag == "account_day_pnl_unavailable" || flag == "attribution_residual_exceeds_warning" {
			continue
		}
		result.QualityFlags = appendUnique(result.QualityFlags, flag)
	}
	openObservation, openObservationErr := service.store.GetAssetPositionObservation(ctx, accountID, normalizedDate, "open")
	closeObservation, closeObservationErr := service.store.GetAssetPositionObservation(ctx, accountID, normalizedDate, "close")
	openVisibleCash := firstPositiveFloat(daily.OpenNetAsset, daily.PreviousNetAsset)
	closeVisibleCash := firstPositiveFloat(daily.CashTotal, daily.NetAsset)
	brokerOpenPositionValue := 0.0
	brokerClosePositionValue := daily.PositionMarketValue
	if openObservationErr == nil && (openObservation.CashTotal != 0 || openObservation.NetAsset != 0) {
		openVisibleCash = openObservation.CashTotal
		brokerOpenPositionValue = openObservation.PositionMarketValue
	} else {
		result.QualityFlags = appendUnique(result.QualityFlags, "open_asset_observation_unavailable")
	}
	if closeObservationErr == nil && (closeObservation.CashTotal != 0 || closeObservation.NetAsset != 0) {
		closeVisibleCash = closeObservation.CashTotal
		brokerClosePositionValue = closeObservation.PositionMarketValue
	} else {
		result.QualityFlags = appendUnique(result.QualityFlags, "close_asset_observation_unavailable")
	}
	if openVisibleCash > 0 || contribution.Summary.OpenPositionValue > 0 {
		openEconomicNAV = roundMoney(openVisibleCash + contribution.Summary.OpenPositionValue)
	}
	result.Valuation = EconomicNAVValuationSummary{
		OpenVisibleCash:          roundMoney(openVisibleCash),
		OpenPositionValue:        contribution.Summary.OpenPositionValue,
		CloseVisibleCash:         roundMoney(closeVisibleCash),
		ClosePositionValue:       contribution.Summary.ClosePositionValue,
		BrokerOpenPositionValue:  roundMoney(brokerOpenPositionValue),
		BrokerClosePositionValue: roundMoney(brokerClosePositionValue),
		PriceSource:              "meridian_pre_close_and_close",
		CostSource:               "excluded_from_nav",
	}
	result.QualityFlags = appendUnique(result.QualityFlags, "research_position_valuation", "broker_position_cost_excluded")
	if contribution.Summary.MissingItems > 0 {
		status = "blocked"
		result.QualityFlags = appendUnique(result.QualityFlags, "incomplete_position_valuation")
	}

	externalFlows, err := service.listConfirmedCash(ctx, accountID, normalizedDate, "external_flow")
	if err != nil {
		return EconomicNAVResult{}, err
	}
	settlementFlows, err := service.listConfirmedCash(ctx, accountID, normalizedDate, "settlement_adjustment")
	if err != nil {
		return EconomicNAVResult{}, err
	}
	incomeExpenseFlows, err := service.listConfirmedCash(ctx, accountID, normalizedDate, "income_expense")
	if err != nil {
		return EconomicNAVResult{}, err
	}
	internalFlows, err := service.listConfirmedCash(ctx, accountID, normalizedDate, "internal_transfer")
	if err != nil {
		return EconomicNAVResult{}, err
	}

	externalNetFlow := sumCashAmounts(externalFlows)
	settlementAdjustment := sumCashAmounts(settlementFlows)
	incomeExpense := sumCashAmounts(incomeExpenseFlows)
	internalTransfer := sumCashAmounts(internalFlows)
	result.CashFlows = EconomicNAVCashFlowSummary{
		ExternalNetFlow:    roundMoney(externalNetFlow),
		SettlementAdjust:   roundMoney(settlementAdjustment),
		IncomeExpense:      roundMoney(incomeExpense),
		InternalTransfer:   roundMoney(internalTransfer),
		ExternalFlowCount:  len(externalFlows),
		SettlementCount:    len(settlementFlows),
		IncomeExpenseCount: len(incomeExpenseFlows),
		InternalFlowCount:  len(internalFlows),
	}
	if math.Abs(internalTransfer) > 0.000001 {
		result.QualityFlags = appendUnique(result.QualityFlags, "internal_transfer_unbalanced")
	}

	repoSummary, repoFlags := service.reverseRepoForEconomicNAV(ctx, accountID, normalizedDate)
	estimatedCashManagementPnL := strategyNetContribution(contribution, StrategyCashManagement)
	formalStrategyPnL := roundMoney(contribution.Summary.NetContribution - estimatedCashManagementPnL)
	formalAttributedPnL := roundMoney(formalStrategyPnL + incomeExpense)
	attributionWarningThreshold := roundMoney(math.Max(service.warningToleranceCNY, math.Abs(openEconomicNAV)*service.warningToleranceBP/10000))
	principalFlags := resolveReverseRepoPrincipal(
		&repoSummary,
		roundMoney(closeVisibleCash+contribution.Summary.ClosePositionValue),
		openEconomicNAV,
		externalNetFlow,
		settlementAdjustment,
		formalAttributedPnL,
		attributionWarningThreshold,
	)
	result.ReverseRepo = repoSummary
	result.QualityFlags = appendUnique(result.QualityFlags, repoFlags...)
	result.QualityFlags = appendUnique(result.QualityFlags, principalFlags...)
	if repoSummary.PrincipalTreatment == "ambiguous" {
		status = "blocked"
	} else if repoSummary.Orders > 0 && status == "finalized" {
		status = "provisional"
	}

	closeEconomicNAV := roundMoney(closeVisibleCash + contribution.Summary.ClosePositionValue + repoSummary.Receivable)
	if openEconomicNAV <= 0 || closeEconomicNAV <= 0 {
		result.Status = "blocked"
		result.QualityFlags = appendUnique(result.QualityFlags, "missing_positive_economic_nav")
		result.NAV = ledger.PerformanceNAV{
			AccountID:        accountID,
			TradeDate:        normalizedDate,
			Status:           "blocked",
			FormulaVersion:   service.formulaVersion,
			OpenEconomicNAV:  roundMoney(openEconomicNAV),
			CloseEconomicNAV: closeEconomicNAV,
			Source:           "relay.economic_nav.preview",
			QualityFlags:     result.QualityFlags,
		}
		return result, nil
	}

	accountDayPnL := roundMoney(closeEconomicNAV - openEconomicNAV - externalNetFlow - settlementAdjustment)
	accountedContribution := formalAttributedPnL
	attributionResidual := roundMoney(accountDayPnL - accountedContribution)
	if math.Abs(attributionResidual) > attributionWarningThreshold {
		status = "blocked"
		result.QualityFlags = appendUnique(result.QualityFlags, "nav_contribution_residual_exceeds_warning")
	}
	if contribution.Summary.MissingFeeItems > 0 {
		result.QualityFlags = appendUnique(result.QualityFlags, "net_performance_fee_incomplete")
		if status == "finalized" {
			status = "provisional"
		}
	}
	returnDenominator, weightedFlowDetails, denominatorFlags := calculateReturnDenominator(openEconomicNAV, externalFlows, parsedDate)
	result.QualityFlags = appendUnique(result.QualityFlags, denominatorFlags...)
	dailyReturn := 0.0
	if returnDenominator > 0 {
		dailyReturn = accountDayPnL / returnDenominator
	}
	previousNAV, sameDateVersion, navFlags, err := service.previousNAVContext(ctx, accountID, normalizedDate)
	if err != nil {
		return EconomicNAVResult{}, err
	}
	result.QualityFlags = appendUnique(result.QualityFlags, navFlags...)
	previousCumulative := 1.0
	if previousNAV.CumulativeNAV > 0 {
		previousCumulative = previousNAV.CumulativeNAV
	}
	cumulativeNAV := roundRatio(previousCumulative * (1 + dailyReturn))
	cashManagementPnL := roundMoney(repoSummary.RecognizedNetInterest + incomeExpense)
	unattributedPnL := attributionResidual
	if math.Abs(unattributedPnL) > 0.000001 {
		result.QualityFlags = appendUnique(result.QualityFlags, "strategy_attribution_pending")
	}

	now := service.now()
	nav := ledger.PerformanceNAV{
		AccountID:            accountID,
		TradeDate:            normalizedDate,
		Version:              sameDateVersion + 1,
		IsCurrent:            true,
		Status:               status,
		FormulaVersion:       service.formulaVersion,
		OpenEconomicNAV:      roundMoney(openEconomicNAV),
		ExternalNetFlow:      roundMoney(externalNetFlow),
		AccountDayPnL:        accountDayPnL,
		SettlementAdjustment: roundMoney(settlementAdjustment),
		CloseEconomicNAV:     closeEconomicNAV,
		ReturnDenominator:    roundMoney(returnDenominator),
		DailyReturn:          roundRatio(dailyReturn),
		CumulativeNAV:        cumulativeNAV,
		PnLComponents: map[string]any{
			"securities_and_strategy": map[string]any{
				"pnl":                                    formalStrategyPnL,
				"reported_contribution":                  contribution.Summary.NetContribution,
				"estimated_cash_management_contribution": estimatedCashManagementPnL,
				"gross_contribution":                     contribution.Summary.GrossContribution,
				"actual_fee":                             contribution.Summary.ActualFee,
				"estimated_fee":                          contribution.Summary.EstimatedFee,
				"effective_fee":                          contribution.Summary.EffectiveFee,
				"missing_items":                          contribution.Summary.MissingItems,
				"missing_fee_items":                      contribution.Summary.MissingFeeItems,
				"attribution_residual":                   attributionResidual,
			},
			"unattributed": map[string]any{
				"pnl":   unattributedPnL,
				"scope": "strategy_components_pending",
			},
			"cash_management": map[string]any{
				"pnl":                                  cashManagementPnL,
				"known_income_expense":                 roundMoney(incomeExpense),
				"reverse_repo_net_interest":            repoSummary.NetInterest,
				"reverse_repo_estimated_net_interest":  repoSummary.EstimatedNetInterest,
				"reverse_repo_recognized_net_interest": repoSummary.RecognizedNetInterest,
				"reverse_repo_receivable":              repoSummary.Receivable,
				"reverse_repo_estimated_receivable":    repoSummary.EstimatedReceivable,
				"reverse_repo_principal":               repoSummary.Principal,
				"reverse_repo_principal_cash_overlap":  repoSummary.PrincipalCashOverlap,
				"reverse_repo_principal_receivable":    repoSummary.PrincipalReceivable,
				"reverse_repo_principal_treatment":     repoSummary.PrincipalTreatment,
				"reverse_repo_interest_recognition":    repoSummary.InterestRecognition,
				"reverse_repo_resolution_residual":     repoSummary.ResolutionResidual,
				"reverse_repo_alternate_residual":      repoSummary.AlternateResidual,
				"reverse_repo_effective_fee":           repoSummary.Fee,
				"reverse_repo_orders":                  repoSummary.Orders,
				"reverse_repo_accrual_source":          repoSummary.Source,
			},
			"trading_observation": map[string]any{
				"fills_count": daily.FillsCount,
				"buy_amount":  roundMoney(daily.BuyAmount),
				"sell_amount": roundMoney(daily.SellAmount),
				"turnover":    roundMoney(daily.Turnover),
				"fee_total":   roundMoney(daily.FeeTotal),
			},
			"market_valuation": map[string]any{
				"open_visible_cash":           roundMoney(openVisibleCash),
				"open_position_value":         contribution.Summary.OpenPositionValue,
				"close_visible_cash":          roundMoney(closeVisibleCash),
				"close_position_value":        contribution.Summary.ClosePositionValue,
				"broker_open_position_value":  roundMoney(brokerOpenPositionValue),
				"broker_close_position_value": roundMoney(brokerClosePositionValue),
				"price_source":                "meridian_pre_close_and_close",
				"broker_cost_usage":           "excluded",
			},
			"cash_bridge": map[string]any{
				"visible_close_net_asset":        roundMoney(daily.NetAsset),
				"open_economic_nav":              roundMoney(openEconomicNAV),
				"external_net_flow":              roundMoney(externalNetFlow),
				"settlement_adjustment":          roundMoney(settlementAdjustment),
				"internal_transfer_net":          roundMoney(internalTransfer),
				"weighted_external_flow_details": weightedFlowDetails,
			},
		},
		QualityFlags: result.QualityFlags,
		Source:       "relay.economic_nav.preview",
	}
	if status == "finalized" {
		nav.FinalizedAt = now
	}
	reconciliation := service.buildNAVReconciliation(accountID, normalizedDate, nav, daily, incomeExpense)
	result.NAV = nav
	result.Reconciliation = reconciliation

	if options.Persist {
		nav.Source = "relay.economic_nav.rebuild"
		savedNAV, err := service.store.UpsertPerformanceNAV(ctx, nav)
		if err != nil {
			return EconomicNAVResult{}, err
		}
		result.NAV = savedNAV
		reconciliation.PerformanceNAVPK = savedNAV.PerformanceNAVPK
		savedReconciliation, err := service.store.UpsertNAVReconciliation(ctx, reconciliation)
		if err != nil {
			return EconomicNAVResult{}, err
		}
		result.Reconciliation = savedReconciliation
		result.Persisted = true
	}
	result.Status = result.NAV.Status
	result.QualityFlags = result.NAV.QualityFlags
	return result, nil
}

func (service *Service) ReconcileEconomicNAV(ctx context.Context, accountID, tradeDate string, options EconomicNAVReconcileOptions) (EconomicNAVReconcileResult, error) {
	accountID = strings.TrimSpace(accountID)
	normalizedDate, parsedDate, err := parseTradeDate(tradeDate)
	if err != nil {
		return EconomicNAVReconcileResult{}, err
	}
	if accountID == "" {
		return EconomicNAVReconcileResult{}, errors.New("account_id is required")
	}

	observedDate := strings.TrimSpace(options.ObservedTradeDate)
	var parsedObservedDate time.Time
	if observedDate == "" {
		if service.calendar == nil {
			return EconomicNAVReconcileResult{}, errors.New("meridian trading calendar is unavailable")
		}
		parsedObservedDate, err = service.nextTradingDay(ctx, parsedDate)
		if err != nil {
			return EconomicNAVReconcileResult{}, fmt.Errorf("resolve observed trade date: %w", err)
		}
		observedDate = parsedObservedDate.Format("2006-01-02")
	} else {
		observedDate, parsedObservedDate, err = parseTradeDate(observedDate)
		if err != nil {
			return EconomicNAVReconcileResult{}, err
		}
	}
	if !parsedObservedDate.After(parsedDate) {
		return EconomicNAVReconcileResult{}, fmt.Errorf("observed_trade_date must be after trade_date")
	}

	nav, found, err := service.currentEconomicNAV(ctx, accountID, normalizedDate)
	if err != nil {
		return EconomicNAVReconcileResult{}, err
	}
	flags := make([]string, 0)
	if !found {
		if options.Persist {
			return EconomicNAVReconcileResult{}, fmt.Errorf("current economic nav not found for %s/%s; rebuild economic NAV before persisting reconciliation", accountID, normalizedDate)
		}
		preview, err := service.CalculateEconomicNAV(ctx, accountID, normalizedDate, EconomicNAVOptions{Persist: false})
		if err != nil {
			return EconomicNAVReconcileResult{}, err
		}
		nav = preview.NAV
		flags = appendUnique(flags, preview.QualityFlags...)
		flags = appendUnique(flags, "economic_nav_preview_source")
	}
	flags = appendUnique(flags, nav.QualityFlags...)

	observation, err := service.store.GetAssetPositionObservation(ctx, accountID, observedDate, "open")
	if err != nil {
		return EconomicNAVReconcileResult{}, err
	}
	if observation.PositionsCount == 0 {
		flags = appendUnique(flags, "observed_open_positions_empty")
	}

	externalFlows, err := service.listConfirmedCash(ctx, accountID, observedDate, "external_flow")
	if err != nil {
		return EconomicNAVReconcileResult{}, err
	}
	incomeExpenseFlows, err := service.listConfirmedCash(ctx, accountID, observedDate, "income_expense")
	if err != nil {
		return EconomicNAVReconcileResult{}, err
	}
	overnightExternalNetFlow, externalCount, externalFlags := overnightCashAmount(externalFlows, parsedObservedDate, "external_flow")
	knownIncomeExpense, incomeExpenseCount, incomeExpenseFlags := overnightCashAmount(incomeExpenseFlows, parsedObservedDate, "income_expense")
	flags = appendUnique(flags, externalFlags...)
	flags = appendUnique(flags, incomeExpenseFlags...)

	reconciliation := service.buildObservedNAVReconciliation(accountID, normalizedDate, observedDate, nav, observation, overnightExternalNetFlow, knownIncomeExpense, externalCount, incomeExpenseCount, flags)
	result := EconomicNAVReconcileResult{
		AccountID:         accountID,
		TradeDate:         normalizedDate,
		ObservedTradeDate: observedDate,
		Status:            reconciliation.Status,
		FormulaVersion:    nav.FormulaVersion,
		NAV:               nav,
		Reconciliation:    reconciliation,
		Observation:       observation,
		QualityFlags:      flags,
	}
	if options.Persist {
		savedReconciliation, err := service.store.UpsertNAVReconciliation(ctx, reconciliation)
		if err != nil {
			return EconomicNAVReconcileResult{}, err
		}
		result.Reconciliation = savedReconciliation
		result.Status = savedReconciliation.Status
		result.Persisted = true
	}
	return result, nil
}

func (service *Service) ReviewNAVReconciliation(ctx context.Context, accountID, tradeDate string, options NAVReconciliationReviewOptions) (NAVReconciliationReviewResult, error) {
	accountID = strings.TrimSpace(accountID)
	normalizedDate, _, err := parseTradeDate(tradeDate)
	if err != nil {
		return NAVReconciliationReviewResult{}, err
	}
	if accountID == "" {
		return NAVReconciliationReviewResult{}, errors.New("account_id is required")
	}
	action := strings.TrimSpace(options.Action)
	if action == "" {
		action = "confirm"
	}
	switch action {
	case "confirm", "block":
	default:
		return NAVReconciliationReviewResult{}, fmt.Errorf("%w: review action must be confirm or block", ledger.ErrInvalidLedgerInput)
	}
	operator := strings.TrimSpace(options.Operator)
	if operator == "" {
		operator = "operator"
	}

	nav, found, err := service.currentEconomicNAV(ctx, accountID, normalizedDate)
	if err != nil {
		return NAVReconciliationReviewResult{}, err
	}
	if !found || nav.PerformanceNAVPK <= 0 {
		return NAVReconciliationReviewResult{}, fmt.Errorf("%w: current economic nav %s/%s", sql.ErrNoRows, accountID, normalizedDate)
	}
	reconciliation, found, err := service.currentNAVReconciliation(ctx, accountID, normalizedDate, nav.PerformanceNAVPK, options.ReconciliationID)
	if err != nil {
		return NAVReconciliationReviewResult{}, err
	}
	if !found {
		return NAVReconciliationReviewResult{}, fmt.Errorf("%w: nav reconciliation %s/%s", sql.ErrNoRows, accountID, normalizedDate)
	}
	if action == "confirm" && !options.Force {
		if nav.Status == "blocked" || reconciliation.Status == "blocked" {
			return NAVReconciliationReviewResult{}, fmt.Errorf("%w: blocked economic NAV requires force=true to confirm", ledger.ErrInvalidLedgerInput)
		}
		if math.Abs(reconciliation.Residual) > reconciliation.WarningThreshold && reconciliation.WarningThreshold > 0 {
			return NAVReconciliationReviewResult{}, fmt.Errorf("%w: reconciliation residual exceeds warning threshold; use force=true only after manual review", ledger.ErrInvalidLedgerInput)
		}
	}

	now := service.now()
	note := strings.TrimSpace(options.Note)
	nextReconciliation := reconciliation
	nextReconciliation.ReviewedBy = operator
	nextReconciliation.ReviewedAt = now
	nextReconciliation.Details = reviewDetails(reconciliation.Details, action, operator, note, options.Force, now)
	nextNAV := nav
	nextNAV.PnLComponents = reviewDetails(nav.PnLComponents, action, operator, note, options.Force, now)
	nextNAV.Source = "relay.economic_nav.review_" + action
	reviewFlag := "nav_reconciliation_blocked"
	if action == "confirm" {
		reviewFlag = "nav_reconciliation_confirmed"
	}
	flags := appendUnique(nav.QualityFlags, reviewFlag)
	if options.Force {
		flags = appendUnique(flags, "manual_force_"+action+"ed")
	}
	if action == "confirm" && reconciliation.Status == "review_required" {
		flags = appendUnique(flags, "manual_review_confirmed")
	}

	switch action {
	case "confirm":
		nextReconciliation.Status = "confirmed"
		nextNAV.Status = "finalized"
		nextNAV.FinalizedAt = now
	case "block":
		nextReconciliation.Status = "blocked"
		nextNAV.Status = "blocked"
		nextNAV.FinalizedAt = time.Time{}
		flags = appendUnique(flags, "nav_finalization_blocked")
	}
	nextNAV.QualityFlags = flags

	savedReconciliation, err := service.store.UpsertNAVReconciliation(ctx, nextReconciliation)
	if err != nil {
		return NAVReconciliationReviewResult{}, err
	}
	savedNAV, err := service.store.UpdatePerformanceNAVStatus(ctx, nextNAV)
	if err != nil {
		return NAVReconciliationReviewResult{}, err
	}
	return NAVReconciliationReviewResult{
		AccountID:      accountID,
		TradeDate:      normalizedDate,
		Action:         action,
		Status:         savedReconciliation.Status,
		Persisted:      true,
		NAV:            savedNAV,
		Reconciliation: savedReconciliation,
		QualityFlags:   savedNAV.QualityFlags,
	}, nil
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

func (service *Service) openEconomicNAV(ctx context.Context, accountID, tradeDate string, daily ledger.DailyPerformance) (float64, []string, error) {
	flags := make([]string, 0)
	openNAV := daily.OpenNetAsset
	if daily.OpenSnapshotSource != "open" {
		flags = appendUnique(flags, "open_nav_not_from_open_snapshot")
	}

	baselines, err := service.store.ListNavBaselines(ctx, accountID)
	if err != nil {
		return 0, nil, err
	}
	baseline, hasBaseline := latestConfirmedBaseline(baselines, tradeDate)
	if !hasBaseline {
		inception, inceptionErr := service.store.GetPerformanceInception(ctx, accountID)
		if inceptionErr != nil || inception.Status != "confirmed" || inception.InceptionDate > tradeDate {
			flags = appendUnique(flags, "missing_nav_baseline")
		} else {
			flags = appendUnique(flags, "performance_inception_baseline")
		}
	}
	if openNAV <= 0 && hasBaseline {
		openNAV = baseline.InitialEconomicNAV
		flags = appendUnique(flags, "open_nav_from_baseline")
	}
	if hasBaseline && baseline.EffectiveDate == tradeDate && openNAV > 0 {
		diff := math.Abs(openNAV - baseline.InitialEconomicNAV)
		threshold := math.Max(service.autoToleranceCNY, openNAV*service.autoToleranceBP/10000)
		if diff > threshold {
			flags = appendUnique(flags, "baseline_open_nav_diff")
		}
	}
	return roundMoney(openNAV), flags, nil
}

func latestConfirmedBaseline(items []ledger.NavBaseline, tradeDate string) (ledger.NavBaseline, bool) {
	var latest ledger.NavBaseline
	found := false
	for _, item := range items {
		if item.Status != "confirmed" || item.EffectiveDate == "" || item.EffectiveDate > tradeDate {
			continue
		}
		if !found || item.EffectiveDate > latest.EffectiveDate {
			latest = item
			found = true
		}
	}
	return latest, found
}

func (service *Service) listConfirmedCash(ctx context.Context, accountID, tradeDate, flowClass string) ([]ledger.CashLedgerEntry, error) {
	return service.store.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{
		AccountID: accountID,
		TradeDate: tradeDate,
		FlowClass: flowClass,
		Status:    "confirmed",
		Limit:     1000,
	})
}

func sumCashAmounts(items []ledger.CashLedgerEntry) float64 {
	total := 0.0
	for _, item := range items {
		total += item.Amount
	}
	return roundMoney(total)
}

func strategyNetContribution(result ContributionResult, strategyType string) float64 {
	for _, strategy := range result.Strategies {
		if strategy.StrategyType == strategyType {
			return roundMoney(strategy.NetContribution)
		}
	}
	return 0
}

func resolveReverseRepoPrincipal(
	summary *EconomicNAVReverseRepoSummary,
	baseCloseNAV float64,
	openEconomicNAV float64,
	externalNetFlow float64,
	settlementAdjustment float64,
	formalAttributedPnL float64,
	warningThreshold float64,
) []string {
	if summary == nil || summary.Principal <= 0 {
		if summary != nil {
			summary.PrincipalTreatment = "none"
			summary.InterestRecognition = "none"
		}
		return nil
	}

	withoutPrincipalPnL := baseCloseNAV - openEconomicNAV - externalNetFlow - settlementAdjustment
	withPrincipalPnL := withoutPrincipalPnL + summary.Principal
	withoutPrincipalResidual := roundMoney(withoutPrincipalPnL - formalAttributedPnL)
	withPrincipalResidual := roundMoney(withPrincipalPnL - formalAttributedPnL)

	treatment := "embedded"
	selectedResidual := withoutPrincipalResidual
	alternateResidual := withPrincipalResidual
	principalReceivable := 0.0
	principalCashOverlap := summary.Principal
	if math.Abs(withPrincipalResidual) < math.Abs(withoutPrincipalResidual) {
		treatment = "separate"
		selectedResidual = withPrincipalResidual
		alternateResidual = withoutPrincipalResidual
		principalReceivable = summary.Principal
		principalCashOverlap = 0
	}

	confidenceGap := math.Abs(alternateResidual) - math.Abs(selectedResidual)
	confidenceFloor := math.Max(warningThreshold, math.Abs(summary.Principal)*0.25)
	flags := []string{"reverse_repo_principal_treatment_inferred"}
	if confidenceGap <= confidenceFloor {
		treatment = "ambiguous"
		flags = append(flags, "reverse_repo_principal_treatment_ambiguous")
	} else if treatment == "embedded" {
		flags = append(flags, "reverse_repo_principal_embedded_in_cash")
	} else {
		flags = append(flags, "reverse_repo_principal_separate_from_cash")
	}
	if math.Abs(summary.EstimatedNetInterest) > 0.000001 {
		flags = append(flags, "reverse_repo_estimated_interest_excluded")
	}

	summary.PrincipalTreatment = treatment
	summary.PrincipalCashOverlap = roundMoney(principalCashOverlap)
	summary.PrincipalReceivable = roundMoney(principalReceivable)
	summary.RecognizedNetInterest = 0
	summary.Receivable = summary.PrincipalReceivable
	summary.InterestRecognition = "deferred_to_cash_ledger"
	summary.ResolutionResidual = selectedResidual
	summary.AlternateResidual = alternateResidual
	return flags
}

func (service *Service) reverseRepoForEconomicNAV(ctx context.Context, accountID, tradeDate string) (EconomicNAVReverseRepoSummary, []string) {
	flags := make([]string, 0)
	accruals, err := service.store.ListReverseRepoAccruals(ctx, accountID, tradeDate)
	source := "ledger"
	if err != nil {
		return EconomicNAVReverseRepoSummary{Source: "unavailable"}, appendUnique(flags, "reverse_repo_accrual_list_failed")
	}
	if len(accruals) == 0 {
		result, err := service.CalculateReverseRepo(ctx, accountID, tradeDate, false)
		if err != nil {
			return EconomicNAVReverseRepoSummary{Source: "unavailable"}, appendUnique(flags, "reverse_repo_accrual_preview_failed")
		}
		accruals = result.Accruals
		if len(accruals) > 0 {
			source = "preview"
			flags = appendUnique(flags, "reverse_repo_accrual_preview")
			flags = appendUnique(flags, result.QualityFlags...)
		} else {
			source = "none"
		}
	}
	summary := EconomicNAVReverseRepoSummary{Orders: len(accruals), Source: source}
	for _, accrual := range accruals {
		if accrual.Status == "voided" {
			continue
		}
		summary.Principal += accrual.Principal
		summary.NetInterest += accrual.NetInterest
		summary.EstimatedReceivable += accrual.Receivable
		summary.Fee += accrual.EffectiveFee
		flags = appendUnique(flags, accrual.QualityFlags...)
	}
	summary.Principal = roundMoney(summary.Principal)
	summary.NetInterest = roundMoney(summary.NetInterest)
	summary.EstimatedNetInterest = summary.NetInterest
	summary.EstimatedReceivable = roundMoney(summary.EstimatedReceivable)
	summary.PrincipalReceivable = summary.Principal
	summary.Receivable = summary.Principal
	summary.Fee = roundMoney(summary.Fee)
	if summary.Orders == 0 {
		summary.PrincipalTreatment = "none"
		summary.InterestRecognition = "none"
	} else {
		summary.PrincipalTreatment = "unresolved"
		summary.InterestRecognition = "deferred_to_cash_ledger"
	}
	return summary, flags
}

func (service *Service) previousNAVContext(ctx context.Context, accountID, tradeDate string) (ledger.PerformanceNAV, int, []string, error) {
	items, err := service.store.ListPerformanceNAVs(ctx, accountID, "", tradeDate)
	if err != nil {
		return ledger.PerformanceNAV{}, 0, nil, err
	}
	var previous ledger.PerformanceNAV
	sameDateVersion := 0
	for _, item := range items {
		if item.TradeDate == tradeDate && item.Version > sameDateVersion {
			sameDateVersion = item.Version
			continue
		}
		if item.TradeDate < tradeDate && item.CumulativeNAV > 0 && item.Status != "blocked" && item.FormulaVersion == service.formulaVersion {
			previous = item
		}
	}
	flags := make([]string, 0)
	if previous.AccountID == "" {
		flags = appendUnique(flags, "missing_previous_economic_nav")
	}
	return previous, sameDateVersion, flags, nil
}

func (service *Service) currentEconomicNAV(ctx context.Context, accountID, tradeDate string) (ledger.PerformanceNAV, bool, error) {
	items, err := service.store.ListPerformanceNAVs(ctx, accountID, tradeDate, tradeDate)
	if err != nil {
		return ledger.PerformanceNAV{}, false, err
	}
	for _, item := range items {
		if item.TradeDate == tradeDate {
			return item, true, nil
		}
	}
	return ledger.PerformanceNAV{}, false, nil
}

func (service *Service) currentNAVReconciliation(ctx context.Context, accountID, tradeDate string, performanceNAVPK int64, reconciliationID string) (ledger.NAVReconciliation, bool, error) {
	items, err := service.store.ListNAVReconciliations(ctx, accountID, tradeDate, tradeDate)
	if err != nil {
		return ledger.NAVReconciliation{}, false, err
	}
	reconciliationID = strings.TrimSpace(reconciliationID)
	for _, item := range items {
		if item.TradeDate != tradeDate {
			continue
		}
		if reconciliationID != "" {
			if item.ReconciliationID == reconciliationID {
				if item.PerformanceNAVPK != performanceNAVPK {
					return ledger.NAVReconciliation{}, false, fmt.Errorf("%w: reconciliation_id does not match current economic nav", ledger.ErrInvalidLedgerInput)
				}
				return item, true, nil
			}
			continue
		}
		if item.PerformanceNAVPK == performanceNAVPK {
			return item, true, nil
		}
	}
	if reconciliationID != "" {
		return ledger.NAVReconciliation{}, false, fmt.Errorf("%w: reconciliation_id does not match current economic nav", ledger.ErrInvalidLedgerInput)
	}
	return ledger.NAVReconciliation{}, false, nil
}

func reviewDetails(details map[string]any, action, operator, note string, force bool, reviewedAt time.Time) map[string]any {
	next := make(map[string]any, len(details)+1)
	for key, value := range details {
		next[key] = value
	}
	next["review"] = map[string]any{
		"action":      action,
		"operator":    operator,
		"note":        note,
		"force":       force,
		"reviewed_at": reviewedAt,
	}
	return next
}

func calculateReturnDenominator(openNAV float64, flows []ledger.CashLedgerEntry, tradeDate time.Time) (float64, []map[string]any, []string) {
	denominator := openNAV
	details := make([]map[string]any, 0, len(flows))
	flags := make([]string, 0)
	if len(flows) > 0 {
		flags = appendUnique(flags, "modified_dietz_external_flow_weighting")
	}
	sessionStart := time.Date(tradeDate.Year(), tradeDate.Month(), tradeDate.Day(), 9, 30, 0, 0, timeutil.Location())
	sessionEnd := time.Date(tradeDate.Year(), tradeDate.Month(), tradeDate.Day(), 15, 0, 0, 0, timeutil.Location())
	sessionSeconds := sessionEnd.Sub(sessionStart).Seconds()
	for _, flow := range flows {
		weight := 0.5
		if cashFlowHasDateOnlyPrecision(flow) {
			flags = appendUnique(flags, "external_flow_time_estimated_mid_session")
		} else if !flow.EffectiveAt.IsZero() {
			effectiveAt := flow.EffectiveAt.In(timeutil.Location())
			switch {
			case !effectiveAt.After(sessionStart):
				weight = 1
			case !effectiveAt.Before(sessionEnd):
				weight = 0
			default:
				weight = sessionEnd.Sub(effectiveAt).Seconds() / sessionSeconds
			}
		} else {
			flags = appendUnique(flags, "external_flow_missing_effective_at")
		}
		weighted := flow.Amount * weight
		denominator += weighted
		details = append(details, map[string]any{
			"entry_id":     flow.EntryID,
			"amount":       roundMoney(flow.Amount),
			"weight":       roundRatio(weight),
			"weighted":     roundMoney(weighted),
			"effective_at": flow.EffectiveAt,
		})
	}
	if denominator <= 0 {
		flags = appendUnique(flags, "return_denominator_fallback_open_nav")
		denominator = openNAV
	}
	return roundMoney(denominator), details, flags
}

func cashFlowHasDateOnlyPrecision(flow ledger.CashLedgerEntry) bool {
	if flow.RawPayload == nil {
		return false
	}
	precision, _ := flow.RawPayload["effective_time_precision"].(string)
	return strings.EqualFold(strings.TrimSpace(precision), "date")
}

func overnightCashAmount(items []ledger.CashLedgerEntry, observedDate time.Time, flowClass string) (float64, int, []string) {
	total := 0.0
	count := 0
	flags := make([]string, 0)
	cutoff := time.Date(observedDate.Year(), observedDate.Month(), observedDate.Day(), 9, 30, 0, 0, timeutil.Location())
	for _, item := range items {
		if item.EffectiveAt.IsZero() {
			total += item.Amount
			count++
			flags = appendUnique(flags, flowClass+"_missing_effective_at_assumed_overnight")
			continue
		}
		effectiveAt := item.EffectiveAt.In(timeutil.Location())
		if effectiveAt.After(cutoff) {
			flags = appendUnique(flags, flowClass+"_after_open_excluded")
			continue
		}
		total += item.Amount
		count++
	}
	return roundMoney(total), count, flags
}

func (service *Service) buildNAVReconciliation(accountID, tradeDate string, nav ledger.PerformanceNAV, daily ledger.DailyPerformance, knownIncomeExpense float64) ledger.NAVReconciliation {
	outstandingSettlementAssets := roundMoney(nav.CloseEconomicNAV - daily.NetAsset)
	if math.Abs(outstandingSettlementAssets) < 0.000001 {
		outstandingSettlementAssets = 0
	}
	residual := roundMoney(nav.CloseEconomicNAV - daily.NetAsset - outstandingSettlementAssets)
	autoThreshold := roundMoney(math.Max(service.autoToleranceCNY, math.Abs(nav.CloseEconomicNAV)*service.autoToleranceBP/10000))
	warningThreshold := roundMoney(math.Max(service.warningToleranceCNY, math.Abs(nav.CloseEconomicNAV)*service.warningToleranceBP/10000))
	status := "auto_completed"
	if nav.Status == "blocked" {
		status = "blocked"
	} else if math.Abs(residual) > warningThreshold {
		status = "blocked"
	} else if math.Abs(residual) > autoThreshold {
		status = "review_required"
	}
	observedPositionValue := daily.PositionMarketValue
	if observedPositionValue == 0 {
		observedPositionValue = daily.MarketValue
	}
	version := nav.Version
	if version <= 0 {
		version = 1
	}
	reconciliationID := fmt.Sprintf("nav-recon-%s-%s-v%d", sanitizeIDPart(accountID), strings.ReplaceAll(tradeDate, "-", ""), version)
	return ledger.NAVReconciliation{
		ReconciliationID:            reconciliationID,
		PerformanceNAVPK:            nav.PerformanceNAVPK,
		AccountID:                   accountID,
		TradeDate:                   tradeDate,
		ObservedTradeDate:           tradeDate,
		Status:                      status,
		ObservedVisibleCash:         roundMoney(daily.CashTotal),
		ObservedPositionValue:       roundMoney(observedPositionValue),
		OutstandingSettlementAssets: outstandingSettlementAssets,
		ObservedOpenAssets:          roundMoney(nav.OpenEconomicNAV),
		ProvisionalCloseNAV:         roundMoney(nav.CloseEconomicNAV),
		KnownOvernightIncomeExpense: roundMoney(knownIncomeExpense),
		Residual:                    residual,
		AutoThreshold:               autoThreshold,
		WarningThreshold:            warningThreshold,
		Details: map[string]any{
			"formula_version":          nav.FormulaVersion,
			"observed_close_net_asset": roundMoney(daily.NetAsset),
			"quality_flags":            nav.QualityFlags,
			"note":                     "same-day provisional reconciliation; T+1 observed open asset can update this record later",
		},
	}
}

func (service *Service) buildObservedNAVReconciliation(
	accountID string,
	tradeDate string,
	observedTradeDate string,
	nav ledger.PerformanceNAV,
	observation ledger.AssetPositionObservation,
	overnightExternalNetFlow float64,
	knownIncomeExpense float64,
	externalFlowCount int,
	incomeExpenseCount int,
	qualityFlags []string,
) ledger.NAVReconciliation {
	observedPositionValue := observation.PositionMarketValue
	if observedPositionValue == 0 {
		observedPositionValue = observation.MarketValue
	}
	observedOpenAssets := roundMoney(observation.CashTotal + observedPositionValue)
	invisibleCounterCash := 0.0
	outstandingSettlementAssets := 0.0
	residual := roundMoney(observedOpenAssets - nav.CloseEconomicNAV - overnightExternalNetFlow - knownIncomeExpense)
	autoThreshold := roundMoney(math.Max(service.autoToleranceCNY, math.Abs(nav.CloseEconomicNAV)*service.autoToleranceBP/10000))
	warningThreshold := roundMoney(math.Max(service.warningToleranceCNY, math.Abs(nav.CloseEconomicNAV)*service.warningToleranceBP/10000))
	status := "auto_completed"
	if nav.Status == "blocked" {
		status = "blocked"
	} else if math.Abs(residual) > warningThreshold {
		status = "blocked"
	} else if math.Abs(residual) > autoThreshold {
		status = "review_required"
	}
	version := nav.Version
	if version <= 0 {
		version = 1
	}
	reconciliationID := fmt.Sprintf("nav-recon-%s-%s-v%d", sanitizeIDPart(accountID), strings.ReplaceAll(tradeDate, "-", ""), version)
	details := map[string]any{
		"formula_version":       nav.FormulaVersion,
		"source":                "relay.economic_nav.t1_observed_open",
		"observed_net_asset":    roundMoney(observation.NetAsset),
		"observed_market_value": roundMoney(observation.MarketValue),
		"observed_stock_value":  roundMoney(observation.StockValue),
		"observed_fund_value":   roundMoney(observation.FundValue),
		"external_flow_count":   externalFlowCount,
		"income_expense_count":  incomeExpenseCount,
		"quality_flags":         qualityFlags,
		"note":                  "T+1 observed open asset reconciliation; finalized/manual review flow is kept explicit",
	}
	if !observation.CapturedAt.IsZero() {
		details["asset_snapshot_captured_at"] = observation.CapturedAt
	}
	if observation.PositionCapturedAt != nil {
		details["position_snapshot_captured_at"] = *observation.PositionCapturedAt
	}
	return ledger.NAVReconciliation{
		ReconciliationID:            reconciliationID,
		PerformanceNAVPK:            nav.PerformanceNAVPK,
		AccountID:                   accountID,
		TradeDate:                   tradeDate,
		ObservedTradeDate:           observedTradeDate,
		Status:                      status,
		ObservedVisibleCash:         roundMoney(observation.CashTotal),
		ObservedPositionValue:       roundMoney(observedPositionValue),
		InvisibleCounterCash:        invisibleCounterCash,
		OutstandingSettlementAssets: outstandingSettlementAssets,
		ObservedOpenAssets:          observedOpenAssets,
		ProvisionalCloseNAV:         roundMoney(nav.CloseEconomicNAV),
		OvernightExternalNetFlow:    roundMoney(overnightExternalNetFlow),
		KnownOvernightIncomeExpense: roundMoney(knownIncomeExpense),
		Residual:                    residual,
		AutoThreshold:               autoThreshold,
		WarningThreshold:            warningThreshold,
		Details:                     details,
	}
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

func roundRatio(value float64) float64 {
	return math.Round(value*1_000_000_000_000) / 1_000_000_000_000
}

func sanitizeIDPart(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
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
