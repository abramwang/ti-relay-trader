package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type FeeRule struct {
	RuleID                string         `json:"rule_id"`
	AccountID             string         `json:"account_id"`
	Version               int            `json:"version"`
	Status                string         `json:"status"`
	Name                  string         `json:"name,omitempty"`
	Market                string         `json:"market"`
	InstrumentType        string         `json:"instrument_type"`
	BusinessType          string         `json:"business_type"`
	TradeSide             string         `json:"trade_side"`
	CommissionRate        float64        `json:"commission_rate"`
	MinimumCommission     float64        `json:"minimum_commission"`
	StampDutyRate         float64        `json:"stamp_duty_rate"`
	TransferFeeRate       float64        `json:"transfer_fee_rate"`
	HandlingFeeRate       float64        `json:"handling_fee_rate"`
	OtherRate             float64        `json:"other_rate"`
	FixedFee              float64        `json:"fixed_fee"`
	RepoFeeRate           float64        `json:"repo_fee_rate"`
	EstimatedFrictionRate float64        `json:"estimated_friction_rate"`
	EffectiveFrom         string         `json:"effective_from"`
	EffectiveTo           string         `json:"effective_to,omitempty"`
	CreatedBy             string         `json:"created_by"`
	ActivatedBy           string         `json:"activated_by,omitempty"`
	ActivatedAt           time.Time      `json:"activated_at,omitempty"`
	RawPayload            map[string]any `json:"raw_payload,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type FeeRuleQuery struct {
	AccountID   string
	Status      string
	EffectiveOn string
	Limit       int
}

type CashLedgerEntry struct {
	EntryID            string         `json:"entry_id"`
	AccountID          string         `json:"account_id"`
	TradeDate          string         `json:"trade_date"`
	LedgerType         string         `json:"ledger_type"`
	FlowClass          string         `json:"flow_class"`
	Currency           string         `json:"currency"`
	Amount             float64        `json:"amount"`
	BalanceAfter       *float64       `json:"balance_after,omitempty"`
	CashBucket         string         `json:"cash_bucket"`
	CounterpartyBucket string         `json:"counterparty_bucket,omitempty"`
	EffectiveAt        time.Time      `json:"effective_at"`
	Status             string         `json:"status"`
	TransferGroupID    string         `json:"transfer_group_id,omitempty"`
	IdempotencyKey     string         `json:"idempotency_key,omitempty"`
	GatewayOrderID     string         `json:"gateway_order_id,omitempty"`
	FillID             string         `json:"fill_id,omitempty"`
	Description        string         `json:"description,omitempty"`
	Source             string         `json:"source"`
	CreatedBy          string         `json:"created_by"`
	ConfirmedBy        string         `json:"confirmed_by,omitempty"`
	ConfirmedAt        time.Time      `json:"confirmed_at,omitempty"`
	VoidedBy           string         `json:"voided_by,omitempty"`
	VoidedAt           time.Time      `json:"voided_at,omitempty"`
	ReversalOfEntryID  string         `json:"reversal_of_entry_id,omitempty"`
	RawPayload         map[string]any `json:"raw_payload,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
}

type CashLedgerQuery struct {
	AccountID string
	TradeDate string
	DateFrom  string
	DateTo    string
	FlowClass string
	Status    string
	Limit     int
}

type NavBaseline struct {
	BaselineID         string         `json:"baseline_id"`
	AccountID          string         `json:"account_id"`
	EffectiveDate      string         `json:"effective_date"`
	InitialEconomicNAV float64        `json:"initial_economic_nav"`
	Status             string         `json:"status"`
	Source             string         `json:"source"`
	Description        string         `json:"description,omitempty"`
	CreatedBy          string         `json:"created_by"`
	ConfirmedBy        string         `json:"confirmed_by,omitempty"`
	ConfirmedAt        time.Time      `json:"confirmed_at,omitempty"`
	RawPayload         map[string]any `json:"raw_payload,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type PerformanceNAV struct {
	PerformanceNAVPK     int64          `json:"performance_nav_pk"`
	AccountID            string         `json:"account_id"`
	TradeDate            string         `json:"trade_date"`
	Version              int            `json:"version"`
	IsCurrent            bool           `json:"is_current"`
	Status               string         `json:"status"`
	FormulaVersion       string         `json:"formula_version"`
	OpenEconomicNAV      float64        `json:"open_economic_nav"`
	ExternalNetFlow      float64        `json:"external_net_flow"`
	AccountDayPnL        float64        `json:"account_day_pnl"`
	SettlementAdjustment float64        `json:"settlement_adjustment"`
	CloseEconomicNAV     float64        `json:"close_economic_nav"`
	ReturnDenominator    float64        `json:"return_denominator"`
	DailyReturn          float64        `json:"daily_return"`
	CumulativeNAV        float64        `json:"cumulative_nav"`
	PnLComponents        map[string]any `json:"pnl_components,omitempty"`
	QualityFlags         []string       `json:"quality_flags,omitempty"`
	Source               string         `json:"source"`
	FinalizedAt          time.Time      `json:"finalized_at,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type NAVReconciliation struct {
	ReconciliationID            string         `json:"reconciliation_id"`
	PerformanceNAVPK            int64          `json:"performance_nav_pk"`
	AccountID                   string         `json:"account_id"`
	TradeDate                   string         `json:"trade_date"`
	ObservedTradeDate           string         `json:"observed_trade_date"`
	Status                      string         `json:"status"`
	ObservedVisibleCash         float64        `json:"observed_visible_cash"`
	ObservedPositionValue       float64        `json:"observed_position_value"`
	InvisibleCounterCash        float64        `json:"invisible_counter_cash"`
	OutstandingSettlementAssets float64        `json:"outstanding_settlement_assets"`
	ObservedOpenAssets          float64        `json:"observed_open_assets"`
	ProvisionalCloseNAV         float64        `json:"provisional_close_nav"`
	OvernightExternalNetFlow    float64        `json:"overnight_external_net_flow"`
	KnownOvernightIncomeExpense float64        `json:"known_overnight_income_expense"`
	Residual                    float64        `json:"residual"`
	AutoThreshold               float64        `json:"auto_threshold"`
	WarningThreshold            float64        `json:"warning_threshold"`
	Details                     map[string]any `json:"details,omitempty"`
	ReviewedBy                  string         `json:"reviewed_by,omitempty"`
	ReviewedAt                  time.Time      `json:"reviewed_at,omitempty"`
	CreatedAt                   time.Time      `json:"created_at"`
	UpdatedAt                   time.Time      `json:"updated_at"`
}

type ReverseRepoAccrual struct {
	AccountID              string         `json:"account_id"`
	TradeDate              string         `json:"trade_date"`
	GatewayOrderID         string         `json:"gateway_order_id"`
	SecurityID             string         `json:"security_id"`
	Principal              float64        `json:"principal"`
	WeightedRatePct        float64        `json:"weighted_rate_pct"`
	ActualOccupationDays   int            `json:"actual_occupation_days"`
	FirstSettlementDate    string         `json:"first_settlement_date"`
	MaturitySettlementDate string         `json:"maturity_settlement_date"`
	GrossInterest          float64        `json:"gross_interest"`
	ActualFee              *float64       `json:"actual_fee,omitempty"`
	EstimatedFee           *float64       `json:"estimated_fee,omitempty"`
	EffectiveFee           float64        `json:"effective_fee"`
	NetInterest            float64        `json:"net_interest"`
	Receivable             float64        `json:"receivable"`
	Status                 string         `json:"status"`
	FeeSource              string         `json:"fee_source"`
	QualityFlags           []string       `json:"quality_flags,omitempty"`
	SourcePayload          map[string]any `json:"source_payload,omitempty"`
	CalculatedAt           time.Time      `json:"calculated_at"`
	SettledAt              time.Time      `json:"settled_at,omitempty"`
}

func (repo *Repository) CreateFeeRule(ctx context.Context, rule FeeRule) (FeeRule, error) {
	if repo == nil || repo.exec == nil {
		return FeeRule{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeFeeRule(rule)
	if err != nil {
		return FeeRule{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return FeeRule{}, err
	}
	rawPayload, err := marshalJSONObject(normalized.RawPayload)
	if err != nil {
		return FeeRule{}, err
	}
	rows, err := queryer.QueryContext(ctx, createFeeRuleSQL,
		normalized.RuleID,
		normalized.AccountID,
		normalized.Version,
		normalized.Status,
		normalized.Name,
		normalized.Market,
		normalized.InstrumentType,
		normalized.BusinessType,
		normalized.TradeSide,
		normalized.CommissionRate,
		normalized.MinimumCommission,
		normalized.StampDutyRate,
		normalized.TransferFeeRate,
		normalized.HandlingFeeRate,
		normalized.OtherRate,
		normalized.FixedFee,
		normalized.RepoFeeRate,
		normalized.EstimatedFrictionRate,
		normalized.EffectiveFrom,
		nullString(normalized.EffectiveTo),
		normalized.CreatedBy,
		nullString(normalized.ActivatedBy),
		nullableTime(normalized.ActivatedAt),
		rawPayload,
	)
	if err != nil {
		return FeeRule{}, fmt.Errorf("create fee rule %s: %w", normalized.RuleID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return FeeRule{}, fmt.Errorf("create fee rule %s: no row returned", normalized.RuleID)
	}
	created, err := scanFeeRule(rows)
	if err != nil {
		return FeeRule{}, fmt.Errorf("scan created fee rule %s: %w", normalized.RuleID, err)
	}
	return created, nil
}

func (repo *Repository) ListFeeRules(ctx context.Context, query FeeRuleQuery) ([]FeeRule, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeFeeRuleQuery(query)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listFeeRulesSQL,
		normalized.AccountID,
		normalized.Status,
		nullString(normalized.EffectiveOn),
		normalized.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list fee rules: %w", err)
	}
	defer rows.Close()
	items := make([]FeeRule, 0)
	for rows.Next() {
		item, err := scanFeeRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fee rule: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repo *Repository) EffectiveRepoFeeRule(ctx context.Context, accountID, tradeDate string) (FeeRule, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return FeeRule{}, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	normalizedDate, err := normalizeTradeDate(tradeDate)
	if err != nil {
		return FeeRule{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return FeeRule{}, err
	}
	rows, err := queryer.QueryContext(ctx, effectiveRepoFeeRuleSQL, accountID, normalizedDate)
	if err != nil {
		return FeeRule{}, fmt.Errorf("get effective repo fee rule %s/%s: %w", accountID, normalizedDate, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return FeeRule{}, sql.ErrNoRows
	}
	return scanFeeRule(rows)
}

func (repo *Repository) CreateCashLedgerEntry(ctx context.Context, entry CashLedgerEntry) (CashLedgerEntry, error) {
	if repo == nil || repo.exec == nil {
		return CashLedgerEntry{}, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeCashLedgerEntry(entry)
	if err != nil {
		return CashLedgerEntry{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return CashLedgerEntry{}, err
	}
	rawPayload, err := marshalJSONObject(normalized.RawPayload)
	if err != nil {
		return CashLedgerEntry{}, err
	}
	rows, err := queryer.QueryContext(ctx, createCashLedgerEntrySQL,
		normalized.EntryID,
		normalized.AccountID,
		normalized.TradeDate,
		normalized.LedgerType,
		normalized.FlowClass,
		normalized.Currency,
		normalized.Amount,
		nullableFloat(normalized.BalanceAfter),
		normalized.CashBucket,
		nullString(normalized.CounterpartyBucket),
		normalized.EffectiveAt,
		normalized.Status,
		nullString(normalized.TransferGroupID),
		nullString(normalized.IdempotencyKey),
		nullString(normalized.GatewayOrderID),
		nullString(normalized.FillID),
		normalized.Description,
		normalized.Source,
		normalized.CreatedBy,
		nullString(normalized.ReversalOfEntryID),
		rawPayload,
	)
	if err != nil {
		return CashLedgerEntry{}, fmt.Errorf("create cash ledger entry %s: %w", normalized.EntryID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return CashLedgerEntry{}, fmt.Errorf("create cash ledger entry %s: no row returned", normalized.EntryID)
	}
	return scanCashLedgerEntry(rows)
}

func (repo *Repository) ListCashLedgerEntries(ctx context.Context, query CashLedgerQuery) ([]CashLedgerEntry, error) {
	if repo == nil || repo.exec == nil {
		return nil, fmt.Errorf("%w: repository executor is nil", ErrInvalidLedgerInput)
	}
	normalized, err := normalizeCashLedgerQuery(query)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listCashLedgerEntriesSQL,
		normalized.AccountID,
		nullString(normalized.TradeDate),
		nullString(normalized.DateFrom),
		nullString(normalized.DateTo),
		normalized.FlowClass,
		normalized.Status,
		normalized.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list cash ledger entries: %w", err)
	}
	defer rows.Close()
	items := make([]CashLedgerEntry, 0)
	for rows.Next() {
		item, err := scanCashLedgerEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan cash ledger entry: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repo *Repository) ConfirmCashLedgerEntry(ctx context.Context, accountID, entryID, operator string, confirmedAt time.Time) (CashLedgerEntry, error) {
	return repo.transitionCashLedgerEntry(ctx, confirmCashLedgerEntrySQL, accountID, entryID, operator, confirmedAt, "confirm")
}

func (repo *Repository) VoidCashLedgerEntry(ctx context.Context, accountID, entryID, operator string, voidedAt time.Time) (CashLedgerEntry, error) {
	return repo.transitionCashLedgerEntry(ctx, voidCashLedgerEntrySQL, accountID, entryID, operator, voidedAt, "void")
}

func (repo *Repository) transitionCashLedgerEntry(ctx context.Context, query, accountID, entryID, operator string, at time.Time, action string) (CashLedgerEntry, error) {
	accountID = strings.TrimSpace(accountID)
	entryID = strings.TrimSpace(entryID)
	operator = strings.TrimSpace(operator)
	if accountID == "" || entryID == "" || operator == "" {
		return CashLedgerEntry{}, fmt.Errorf("%w: account_id, entry_id and operator are required", ErrInvalidLedgerInput)
	}
	if at.IsZero() {
		at = repo.now()
	}
	queryer, err := repo.queryer()
	if err != nil {
		return CashLedgerEntry{}, err
	}
	rows, err := queryer.QueryContext(ctx, query, entryID, operator, at, accountID)
	if err != nil {
		return CashLedgerEntry{}, fmt.Errorf("%s cash ledger entry %s: %w", action, entryID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return CashLedgerEntry{}, sql.ErrNoRows
	}
	return scanCashLedgerEntry(rows)
}

func (repo *Repository) CreateNavBaseline(ctx context.Context, baseline NavBaseline) (NavBaseline, error) {
	normalized, err := normalizeNavBaseline(baseline)
	if err != nil {
		return NavBaseline{}, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return NavBaseline{}, err
	}
	rawPayload, err := marshalJSONObject(normalized.RawPayload)
	if err != nil {
		return NavBaseline{}, err
	}
	rows, err := queryer.QueryContext(ctx, createNavBaselineSQL,
		normalized.BaselineID,
		normalized.AccountID,
		normalized.EffectiveDate,
		normalized.InitialEconomicNAV,
		normalized.Status,
		normalized.Source,
		normalized.Description,
		normalized.CreatedBy,
		nullString(normalized.ConfirmedBy),
		nullableTime(normalized.ConfirmedAt),
		rawPayload,
	)
	if err != nil {
		return NavBaseline{}, fmt.Errorf("create nav baseline %s: %w", normalized.BaselineID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return NavBaseline{}, fmt.Errorf("create nav baseline %s: no row returned", normalized.BaselineID)
	}
	return scanNavBaseline(rows)
}

func (repo *Repository) ListNavBaselines(ctx context.Context, accountID string) ([]NavBaseline, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listNavBaselinesSQL, accountID)
	if err != nil {
		return nil, fmt.Errorf("list nav baselines %s: %w", accountID, err)
	}
	defer rows.Close()
	items := make([]NavBaseline, 0)
	for rows.Next() {
		item, err := scanNavBaseline(rows)
		if err != nil {
			return nil, fmt.Errorf("scan nav baseline: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repo *Repository) UpsertReverseRepoAccrual(ctx context.Context, accrual ReverseRepoAccrual) error {
	normalized, err := normalizeReverseRepoAccrual(accrual)
	if err != nil {
		return err
	}
	qualityFlags, err := json.Marshal(normalized.QualityFlags)
	if err != nil {
		return fmt.Errorf("marshal reverse repo quality flags: %w", err)
	}
	sourcePayload, err := marshalJSONObject(normalized.SourcePayload)
	if err != nil {
		return err
	}
	_, err = repo.exec.ExecContext(ctx, upsertReverseRepoAccrualSQL,
		normalized.AccountID,
		normalized.TradeDate,
		normalized.GatewayOrderID,
		normalized.SecurityID,
		normalized.Principal,
		normalized.WeightedRatePct,
		normalized.ActualOccupationDays,
		normalized.FirstSettlementDate,
		normalized.MaturitySettlementDate,
		normalized.GrossInterest,
		nullableFloat(normalized.ActualFee),
		nullableFloat(normalized.EstimatedFee),
		normalized.EffectiveFee,
		normalized.NetInterest,
		normalized.Receivable,
		normalized.Status,
		normalized.FeeSource,
		qualityFlags,
		sourcePayload,
		normalized.CalculatedAt,
		nullableTime(normalized.SettledAt),
	)
	if err != nil {
		return fmt.Errorf("upsert reverse repo accrual %s/%s: %w", normalized.AccountID, normalized.GatewayOrderID, err)
	}
	return nil
}

func (repo *Repository) ListReverseRepoAccruals(ctx context.Context, accountID, tradeDate string) ([]ReverseRepoAccrual, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	normalizedDate, err := normalizeTradeDate(tradeDate)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listReverseRepoAccrualsSQL, accountID, normalizedDate)
	if err != nil {
		return nil, fmt.Errorf("list reverse repo accruals: %w", err)
	}
	defer rows.Close()
	items := make([]ReverseRepoAccrual, 0)
	for rows.Next() {
		item, err := scanReverseRepoAccrual(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reverse repo accrual: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repo *Repository) ListPerformanceNAVs(ctx context.Context, accountID, dateFrom, dateTo string) ([]PerformanceNAV, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	_, normalizedFrom, normalizedTo, err := normalizeQueryDates("", dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listPerformanceNAVsSQL, accountID, nullString(normalizedFrom), nullString(normalizedTo))
	if err != nil {
		return nil, fmt.Errorf("list performance navs: %w", err)
	}
	defer rows.Close()
	items := make([]PerformanceNAV, 0)
	for rows.Next() {
		item, err := scanPerformanceNAV(rows)
		if err != nil {
			return nil, fmt.Errorf("scan performance nav: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repo *Repository) ListNAVReconciliations(ctx context.Context, accountID, dateFrom, dateTo string) ([]NAVReconciliation, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalidLedgerInput)
	}
	_, normalizedFrom, normalizedTo, err := normalizeQueryDates("", dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	queryer, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, listNAVReconciliationsSQL, accountID, nullString(normalizedFrom), nullString(normalizedTo))
	if err != nil {
		return nil, fmt.Errorf("list nav reconciliations: %w", err)
	}
	defer rows.Close()
	items := make([]NAVReconciliation, 0)
	for rows.Next() {
		item, err := scanNAVReconciliation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan nav reconciliation: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeFeeRule(rule FeeRule) (FeeRule, error) {
	rule.RuleID = strings.TrimSpace(rule.RuleID)
	rule.AccountID = strings.TrimSpace(rule.AccountID)
	rule.Status = strings.TrimSpace(rule.Status)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Market = wildcardDefault(rule.Market)
	rule.InstrumentType = wildcardDefault(rule.InstrumentType)
	rule.BusinessType = wildcardDefault(rule.BusinessType)
	rule.TradeSide = wildcardDefault(rule.TradeSide)
	rule.EffectiveFrom = strings.TrimSpace(rule.EffectiveFrom)
	rule.EffectiveTo = strings.TrimSpace(rule.EffectiveTo)
	rule.CreatedBy = strings.TrimSpace(rule.CreatedBy)
	if rule.RuleID == "" || rule.AccountID == "" || rule.EffectiveFrom == "" {
		return FeeRule{}, fmt.Errorf("%w: rule_id, account_id and effective_from are required", ErrInvalidLedgerInput)
	}
	if _, err := normalizeTradeDate(rule.EffectiveFrom); err != nil {
		return FeeRule{}, err
	}
	if rule.EffectiveTo != "" {
		if _, err := normalizeTradeDate(rule.EffectiveTo); err != nil {
			return FeeRule{}, err
		}
	}
	if rule.Status == "" {
		rule.Status = "draft"
	}
	if rule.Status != "draft" && rule.Status != "active" && rule.Status != "retired" {
		return FeeRule{}, fmt.Errorf("%w: invalid fee rule status", ErrInvalidLedgerInput)
	}
	if rule.CreatedBy == "" {
		rule.CreatedBy = "system"
	}
	if rule.Status == "active" && rule.ActivatedAt.IsZero() {
		rule.ActivatedAt = time.Now()
		rule.ActivatedBy = rule.CreatedBy
	}
	for _, value := range []float64{
		rule.CommissionRate,
		rule.MinimumCommission,
		rule.StampDutyRate,
		rule.TransferFeeRate,
		rule.HandlingFeeRate,
		rule.OtherRate,
		rule.FixedFee,
		rule.RepoFeeRate,
		rule.EstimatedFrictionRate,
	} {
		if value < 0 {
			return FeeRule{}, fmt.Errorf("%w: fee values must be non-negative", ErrInvalidLedgerInput)
		}
	}
	return rule, nil
}

func normalizeFeeRuleQuery(query FeeRuleQuery) (FeeRuleQuery, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.Status = strings.TrimSpace(query.Status)
	query.EffectiveOn = strings.TrimSpace(query.EffectiveOn)
	if query.EffectiveOn != "" {
		normalized, err := normalizeTradeDate(query.EffectiveOn)
		if err != nil {
			return FeeRuleQuery{}, err
		}
		query.EffectiveOn = normalized
	}
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 200
	}
	return query, nil
}

func normalizeCashLedgerEntry(entry CashLedgerEntry) (CashLedgerEntry, error) {
	entry.EntryID = strings.TrimSpace(entry.EntryID)
	entry.AccountID = strings.TrimSpace(entry.AccountID)
	entry.TradeDate = strings.TrimSpace(entry.TradeDate)
	entry.LedgerType = strings.TrimSpace(entry.LedgerType)
	entry.FlowClass = strings.TrimSpace(entry.FlowClass)
	entry.Currency = strings.ToUpper(strings.TrimSpace(entry.Currency))
	entry.CashBucket = strings.TrimSpace(entry.CashBucket)
	entry.Status = strings.TrimSpace(entry.Status)
	entry.Source = strings.TrimSpace(entry.Source)
	entry.CreatedBy = strings.TrimSpace(entry.CreatedBy)
	if entry.EntryID == "" || entry.AccountID == "" || entry.LedgerType == "" || entry.FlowClass == "" {
		return CashLedgerEntry{}, fmt.Errorf("%w: entry_id, account_id, ledger_type and flow_class are required", ErrInvalidLedgerInput)
	}
	normalizedDate, err := normalizeTradeDate(entry.TradeDate)
	if err != nil {
		return CashLedgerEntry{}, err
	}
	entry.TradeDate = normalizedDate
	if entry.Currency == "" {
		entry.Currency = "CNY"
	}
	if entry.CashBucket == "" {
		entry.CashBucket = "unknown"
	}
	if entry.EffectiveAt.IsZero() {
		entry.EffectiveAt = time.Now()
	}
	if entry.Status == "" {
		entry.Status = "draft"
	}
	if entry.Source == "" {
		entry.Source = "manual"
	}
	if entry.CreatedBy == "" {
		entry.CreatedBy = "operator"
	}
	return entry, nil
}

func normalizeCashLedgerQuery(query CashLedgerQuery) (CashLedgerQuery, error) {
	query.AccountID = strings.TrimSpace(query.AccountID)
	query.FlowClass = strings.TrimSpace(query.FlowClass)
	query.Status = strings.TrimSpace(query.Status)
	var err error
	query.TradeDate, query.DateFrom, query.DateTo, err = normalizeQueryDates(query.TradeDate, query.DateFrom, query.DateTo)
	if err != nil {
		return CashLedgerQuery{}, err
	}
	if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 200
	}
	return query, nil
}

func normalizeNavBaseline(baseline NavBaseline) (NavBaseline, error) {
	baseline.BaselineID = strings.TrimSpace(baseline.BaselineID)
	baseline.AccountID = strings.TrimSpace(baseline.AccountID)
	baseline.EffectiveDate = strings.TrimSpace(baseline.EffectiveDate)
	baseline.Status = strings.TrimSpace(baseline.Status)
	baseline.Source = strings.TrimSpace(baseline.Source)
	baseline.CreatedBy = strings.TrimSpace(baseline.CreatedBy)
	if baseline.BaselineID == "" || baseline.AccountID == "" || baseline.EffectiveDate == "" || baseline.InitialEconomicNAV <= 0 {
		return NavBaseline{}, fmt.Errorf("%w: baseline identity and positive initial nav are required", ErrInvalidLedgerInput)
	}
	normalizedDate, err := normalizeTradeDate(baseline.EffectiveDate)
	if err != nil {
		return NavBaseline{}, err
	}
	baseline.EffectiveDate = normalizedDate
	if baseline.Status == "" {
		baseline.Status = "confirmed"
	}
	if baseline.Source == "" {
		baseline.Source = "manual"
	}
	if baseline.CreatedBy == "" {
		baseline.CreatedBy = "operator"
	}
	if baseline.Status == "confirmed" && baseline.ConfirmedAt.IsZero() {
		baseline.ConfirmedAt = time.Now()
		baseline.ConfirmedBy = baseline.CreatedBy
	}
	return baseline, nil
}

func normalizeReverseRepoAccrual(accrual ReverseRepoAccrual) (ReverseRepoAccrual, error) {
	accrual.AccountID = strings.TrimSpace(accrual.AccountID)
	accrual.GatewayOrderID = strings.TrimSpace(accrual.GatewayOrderID)
	accrual.SecurityID = strings.TrimSpace(accrual.SecurityID)
	if accrual.AccountID == "" || accrual.GatewayOrderID == "" || accrual.SecurityID == "" ||
		accrual.Principal <= 0 || accrual.ActualOccupationDays <= 0 {
		return ReverseRepoAccrual{}, fmt.Errorf("%w: incomplete reverse repo accrual", ErrInvalidLedgerInput)
	}
	var err error
	if accrual.TradeDate, err = normalizeTradeDate(accrual.TradeDate); err != nil {
		return ReverseRepoAccrual{}, err
	}
	if accrual.FirstSettlementDate, err = normalizeTradeDate(accrual.FirstSettlementDate); err != nil {
		return ReverseRepoAccrual{}, err
	}
	if accrual.MaturitySettlementDate, err = normalizeTradeDate(accrual.MaturitySettlementDate); err != nil {
		return ReverseRepoAccrual{}, err
	}
	if accrual.Status == "" {
		accrual.Status = "estimated"
	}
	if accrual.FeeSource == "" {
		accrual.FeeSource = "missing"
	}
	if accrual.CalculatedAt.IsZero() {
		accrual.CalculatedAt = time.Now()
	}
	return accrual, nil
}

func wildcardDefault(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "*"
	}
	return value
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func scanFeeRule(row rowScanner) (FeeRule, error) {
	var item FeeRule
	var effectiveFrom time.Time
	var effectiveTo sql.NullTime
	var activatedBy sql.NullString
	var activatedAt sql.NullTime
	var rawPayload []byte
	err := row.Scan(
		&item.RuleID,
		&item.AccountID,
		&item.Version,
		&item.Status,
		&item.Name,
		&item.Market,
		&item.InstrumentType,
		&item.BusinessType,
		&item.TradeSide,
		&item.CommissionRate,
		&item.MinimumCommission,
		&item.StampDutyRate,
		&item.TransferFeeRate,
		&item.HandlingFeeRate,
		&item.OtherRate,
		&item.FixedFee,
		&item.RepoFeeRate,
		&item.EstimatedFrictionRate,
		&effectiveFrom,
		&effectiveTo,
		&item.CreatedBy,
		&activatedBy,
		&activatedAt,
		&rawPayload,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return FeeRule{}, err
	}
	item.EffectiveFrom = effectiveFrom.Format("2006-01-02")
	if effectiveTo.Valid {
		item.EffectiveTo = effectiveTo.Time.Format("2006-01-02")
	}
	item.ActivatedBy = activatedBy.String
	if activatedAt.Valid {
		item.ActivatedAt = activatedAt.Time
	}
	_ = json.Unmarshal(rawPayload, &item.RawPayload)
	return item, nil
}

func scanCashLedgerEntry(row rowScanner) (CashLedgerEntry, error) {
	var item CashLedgerEntry
	var tradeDate time.Time
	var balanceAfter sql.NullFloat64
	var counterpartyBucket, transferGroupID, idempotencyKey sql.NullString
	var gatewayOrderID, fillID, confirmedBy, voidedBy, reversalOf sql.NullString
	var confirmedAt, voidedAt sql.NullTime
	var rawPayload []byte
	err := row.Scan(
		&item.EntryID,
		&item.AccountID,
		&tradeDate,
		&item.LedgerType,
		&item.FlowClass,
		&item.Currency,
		&item.Amount,
		&balanceAfter,
		&item.CashBucket,
		&counterpartyBucket,
		&item.EffectiveAt,
		&item.Status,
		&transferGroupID,
		&idempotencyKey,
		&gatewayOrderID,
		&fillID,
		&item.Description,
		&item.Source,
		&item.CreatedBy,
		&confirmedBy,
		&confirmedAt,
		&voidedBy,
		&voidedAt,
		&reversalOf,
		&rawPayload,
		&item.CreatedAt,
	)
	if err != nil {
		return CashLedgerEntry{}, err
	}
	item.TradeDate = tradeDate.Format("2006-01-02")
	if balanceAfter.Valid {
		item.BalanceAfter = &balanceAfter.Float64
	}
	item.CounterpartyBucket = counterpartyBucket.String
	item.TransferGroupID = transferGroupID.String
	item.IdempotencyKey = idempotencyKey.String
	item.GatewayOrderID = gatewayOrderID.String
	item.FillID = fillID.String
	item.ConfirmedBy = confirmedBy.String
	if confirmedAt.Valid {
		item.ConfirmedAt = confirmedAt.Time
	}
	item.VoidedBy = voidedBy.String
	if voidedAt.Valid {
		item.VoidedAt = voidedAt.Time
	}
	item.ReversalOfEntryID = reversalOf.String
	_ = json.Unmarshal(rawPayload, &item.RawPayload)
	return item, nil
}

func scanNavBaseline(row rowScanner) (NavBaseline, error) {
	var item NavBaseline
	var effectiveDate time.Time
	var confirmedBy sql.NullString
	var confirmedAt sql.NullTime
	var rawPayload []byte
	err := row.Scan(
		&item.BaselineID,
		&item.AccountID,
		&effectiveDate,
		&item.InitialEconomicNAV,
		&item.Status,
		&item.Source,
		&item.Description,
		&item.CreatedBy,
		&confirmedBy,
		&confirmedAt,
		&rawPayload,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return NavBaseline{}, err
	}
	item.EffectiveDate = effectiveDate.Format("2006-01-02")
	item.ConfirmedBy = confirmedBy.String
	if confirmedAt.Valid {
		item.ConfirmedAt = confirmedAt.Time
	}
	_ = json.Unmarshal(rawPayload, &item.RawPayload)
	return item, nil
}

func scanReverseRepoAccrual(row rowScanner) (ReverseRepoAccrual, error) {
	var item ReverseRepoAccrual
	var tradeDate, firstSettlement, maturitySettlement time.Time
	var actualFee, estimatedFee sql.NullFloat64
	var qualityFlags, sourcePayload []byte
	var settledAt sql.NullTime
	err := row.Scan(
		&item.AccountID,
		&tradeDate,
		&item.GatewayOrderID,
		&item.SecurityID,
		&item.Principal,
		&item.WeightedRatePct,
		&item.ActualOccupationDays,
		&firstSettlement,
		&maturitySettlement,
		&item.GrossInterest,
		&actualFee,
		&estimatedFee,
		&item.EffectiveFee,
		&item.NetInterest,
		&item.Receivable,
		&item.Status,
		&item.FeeSource,
		&qualityFlags,
		&sourcePayload,
		&item.CalculatedAt,
		&settledAt,
	)
	if err != nil {
		return ReverseRepoAccrual{}, err
	}
	item.TradeDate = tradeDate.Format("2006-01-02")
	item.FirstSettlementDate = firstSettlement.Format("2006-01-02")
	item.MaturitySettlementDate = maturitySettlement.Format("2006-01-02")
	if actualFee.Valid {
		item.ActualFee = &actualFee.Float64
	}
	if estimatedFee.Valid {
		item.EstimatedFee = &estimatedFee.Float64
	}
	_ = json.Unmarshal(qualityFlags, &item.QualityFlags)
	_ = json.Unmarshal(sourcePayload, &item.SourcePayload)
	if settledAt.Valid {
		item.SettledAt = settledAt.Time
	}
	return item, nil
}

func scanPerformanceNAV(row rowScanner) (PerformanceNAV, error) {
	var item PerformanceNAV
	var tradeDate time.Time
	var pnlComponents, qualityFlags []byte
	var finalizedAt sql.NullTime
	err := row.Scan(
		&item.PerformanceNAVPK,
		&item.AccountID,
		&tradeDate,
		&item.Version,
		&item.IsCurrent,
		&item.Status,
		&item.FormulaVersion,
		&item.OpenEconomicNAV,
		&item.ExternalNetFlow,
		&item.AccountDayPnL,
		&item.SettlementAdjustment,
		&item.CloseEconomicNAV,
		&item.ReturnDenominator,
		&item.DailyReturn,
		&item.CumulativeNAV,
		&pnlComponents,
		&qualityFlags,
		&item.Source,
		&finalizedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return PerformanceNAV{}, err
	}
	item.TradeDate = tradeDate.Format("2006-01-02")
	_ = json.Unmarshal(pnlComponents, &item.PnLComponents)
	_ = json.Unmarshal(qualityFlags, &item.QualityFlags)
	if finalizedAt.Valid {
		item.FinalizedAt = finalizedAt.Time
	}
	return item, nil
}

func scanNAVReconciliation(row rowScanner) (NAVReconciliation, error) {
	var item NAVReconciliation
	var tradeDate, observedTradeDate time.Time
	var details []byte
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	err := row.Scan(
		&item.ReconciliationID,
		&item.PerformanceNAVPK,
		&item.AccountID,
		&tradeDate,
		&observedTradeDate,
		&item.Status,
		&item.ObservedVisibleCash,
		&item.ObservedPositionValue,
		&item.InvisibleCounterCash,
		&item.OutstandingSettlementAssets,
		&item.ObservedOpenAssets,
		&item.ProvisionalCloseNAV,
		&item.OvernightExternalNetFlow,
		&item.KnownOvernightIncomeExpense,
		&item.Residual,
		&item.AutoThreshold,
		&item.WarningThreshold,
		&details,
		&reviewedBy,
		&reviewedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return NAVReconciliation{}, err
	}
	item.TradeDate = tradeDate.Format("2006-01-02")
	item.ObservedTradeDate = observedTradeDate.Format("2006-01-02")
	_ = json.Unmarshal(details, &item.Details)
	item.ReviewedBy = reviewedBy.String
	if reviewedAt.Valid {
		item.ReviewedAt = reviewedAt.Time
	}
	return item, nil
}
