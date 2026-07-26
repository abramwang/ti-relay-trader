package ledger

const feeRuleColumns = `
    rule_id,
    account_id,
    version,
    status,
    name,
    market,
    instrument_type,
    business_type,
    trade_side,
    commission_rate,
    minimum_commission,
    stamp_duty_rate,
    transfer_fee_rate,
    handling_fee_rate,
    other_rate,
    fixed_fee,
    repo_fee_rate,
    estimated_friction_rate,
    effective_from,
    effective_to,
    created_by,
    activated_by,
    activated_at,
    raw_payload,
    created_at,
    updated_at
`

const createFeeRuleSQL = `
WITH next_version AS (
    SELECT COALESCE(max(version), 0) + 1 AS version
    FROM performance_fee_rules
    WHERE account_id = $2
)
INSERT INTO performance_fee_rules (
    rule_id,
    account_id,
    version,
    status,
    name,
    market,
    instrument_type,
    business_type,
    trade_side,
    commission_rate,
    minimum_commission,
    stamp_duty_rate,
    transfer_fee_rate,
    handling_fee_rate,
    other_rate,
    fixed_fee,
    repo_fee_rate,
    estimated_friction_rate,
    effective_from,
    effective_to,
    created_by,
    activated_by,
    activated_at,
    raw_payload
)
SELECT
    $1,
    $2,
    CASE WHEN $3::integer > 0 THEN $3::integer ELSE next_version.version END,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9,
    $10,
    $11,
    $12,
    $13,
    $14,
    $15,
    $16,
    $17,
    $18,
    $19::date,
    $20::date,
    $21,
    $22,
    $23,
    $24::jsonb
FROM next_version
RETURNING ` + feeRuleColumns

const listFeeRulesSQL = `
SELECT ` + feeRuleColumns + `
FROM performance_fee_rules
WHERE ($1 = '' OR account_id = $1)
    AND ($2 = '' OR status = $2)
    AND (
        $3::date IS NULL
        OR (effective_from <= $3::date AND (effective_to IS NULL OR effective_to >= $3::date))
    )
ORDER BY account_id, effective_from DESC, version DESC
LIMIT $4
`

const effectiveRepoFeeRuleSQL = `
SELECT ` + feeRuleColumns + `
FROM performance_fee_rules
WHERE account_id = $1
    AND status = 'active'
    AND effective_from <= $2::date
    AND (effective_to IS NULL OR effective_to >= $2::date)
    AND business_type IN ('REPO', 'reverse_repo', '*')
ORDER BY
    CASE WHEN business_type IN ('REPO', 'reverse_repo') THEN 0 ELSE 1 END,
    effective_from DESC,
    version DESC
LIMIT 1
`

const cashLedgerColumns = `
    entry_id,
    account_id,
    trade_date,
    ledger_type,
    flow_class,
    currency,
    amount,
    balance_after,
    cash_bucket,
    counterparty_bucket,
    effective_at,
    status,
    transfer_group_id,
    idempotency_key,
    gateway_order_id,
    fill_id,
    description,
    source,
    created_by,
    confirmed_by,
    confirmed_at,
    voided_by,
    voided_at,
    reversal_of_entry_id,
    raw_payload,
    created_at
`

const createCashLedgerEntrySQL = `
INSERT INTO cash_ledger (
    entry_id,
    account_id,
    trade_date,
    ledger_type,
    flow_class,
    currency,
    amount,
    balance_after,
    cash_bucket,
    counterparty_bucket,
    effective_at,
    status,
    transfer_group_id,
    idempotency_key,
    gateway_order_id,
    fill_id,
    description,
    source,
    created_by,
    reversal_of_entry_id,
    raw_payload
) VALUES (
    $1,
    $2,
    $3::date,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9,
    $10,
    $11,
    $12,
    $13,
    $14,
    $15,
    $16,
    $17,
    $18,
    $19,
    $20,
    $21::jsonb
)
RETURNING ` + cashLedgerColumns

const listCashLedgerEntriesSQL = `
SELECT ` + cashLedgerColumns + `
FROM cash_ledger
WHERE ($1 = '' OR account_id = $1)
    AND ($2::date IS NULL OR trade_date = $2::date)
    AND ($3::date IS NULL OR trade_date >= $3::date)
    AND ($4::date IS NULL OR trade_date <= $4::date)
    AND ($5 = '' OR flow_class = $5)
    AND ($6 = '' OR status = $6)
ORDER BY effective_at DESC, cash_ledger_pk DESC
LIMIT $7
`

const confirmCashLedgerEntrySQL = `
UPDATE cash_ledger
SET
    status = 'confirmed',
    confirmed_by = $2,
    confirmed_at = $3
WHERE entry_id = $1
    AND account_id = $4
    AND status = 'draft'
RETURNING ` + cashLedgerColumns

const voidCashLedgerEntrySQL = `
UPDATE cash_ledger
SET
    status = 'voided',
    voided_by = $2,
    voided_at = $3
WHERE entry_id = $1
    AND account_id = $4
    AND status IN ('draft', 'confirmed')
RETURNING ` + cashLedgerColumns

const navBaselineColumns = `
    baseline_id,
    account_id,
    effective_date,
    initial_economic_nav,
    status,
    source,
    description,
    created_by,
    confirmed_by,
    confirmed_at,
    raw_payload,
    created_at,
    updated_at
`

const createNavBaselineSQL = `
INSERT INTO performance_nav_baselines (
    baseline_id,
    account_id,
    effective_date,
    initial_economic_nav,
    status,
    source,
    description,
    created_by,
    confirmed_by,
    confirmed_at,
    raw_payload
) VALUES (
    $1,
    $2,
    $3::date,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9,
    $10,
    $11::jsonb
)
RETURNING ` + navBaselineColumns

const listNavBaselinesSQL = `
SELECT ` + navBaselineColumns + `
FROM performance_nav_baselines
WHERE account_id = $1
ORDER BY effective_date DESC, baseline_pk DESC
`

const reverseRepoAccrualColumns = `
    account_id,
    trade_date,
    gateway_order_id,
    security_id,
    principal,
    weighted_rate_pct,
    actual_occupation_days,
    first_settlement_date,
    maturity_settlement_date,
    gross_interest,
    actual_fee,
    estimated_fee,
    effective_fee,
    net_interest,
    receivable,
    status,
    fee_source,
    quality_flags,
    source_payload,
    calculated_at,
    settled_at
`

const upsertReverseRepoAccrualSQL = `
INSERT INTO reverse_repo_accruals (
    account_id,
    trade_date,
    gateway_order_id,
    security_id,
    principal,
    weighted_rate_pct,
    actual_occupation_days,
    first_settlement_date,
    maturity_settlement_date,
    gross_interest,
    actual_fee,
    estimated_fee,
    effective_fee,
    net_interest,
    receivable,
    status,
    fee_source,
    quality_flags,
    source_payload,
    calculated_at,
    settled_at
) VALUES (
    $1,
    $2::date,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8::date,
    $9::date,
    $10,
    $11,
    $12,
    $13,
    $14,
    $15,
    $16,
    $17,
    $18::jsonb,
    $19::jsonb,
    $20,
    $21
)
ON CONFLICT (account_id, trade_date, gateway_order_id) DO UPDATE SET
    security_id = EXCLUDED.security_id,
    principal = EXCLUDED.principal,
    weighted_rate_pct = EXCLUDED.weighted_rate_pct,
    actual_occupation_days = EXCLUDED.actual_occupation_days,
    first_settlement_date = EXCLUDED.first_settlement_date,
    maturity_settlement_date = EXCLUDED.maturity_settlement_date,
    gross_interest = EXCLUDED.gross_interest,
    actual_fee = EXCLUDED.actual_fee,
    estimated_fee = EXCLUDED.estimated_fee,
    effective_fee = EXCLUDED.effective_fee,
    net_interest = EXCLUDED.net_interest,
    receivable = EXCLUDED.receivable,
    status = EXCLUDED.status,
    fee_source = EXCLUDED.fee_source,
    quality_flags = EXCLUDED.quality_flags,
    source_payload = EXCLUDED.source_payload,
    calculated_at = EXCLUDED.calculated_at,
    settled_at = EXCLUDED.settled_at
`

const listReverseRepoAccrualsSQL = `
SELECT ` + reverseRepoAccrualColumns + `
FROM reverse_repo_accruals
WHERE account_id = $1
    AND trade_date = $2::date
ORDER BY gateway_order_id
`

const performanceNAVColumns = `
    performance_nav_pk,
    account_id,
    trade_date,
    version,
    is_current,
    status,
    formula_version,
    open_economic_nav,
    external_net_flow,
    account_day_pnl,
    settlement_adjustment,
    close_economic_nav,
    return_denominator,
    daily_return,
    cumulative_nav,
    pnl_components,
    quality_flags,
    source,
    finalized_at,
    created_at,
    updated_at
`

const listPerformanceNAVsSQL = `
SELECT ` + performanceNAVColumns + `
FROM performance_nav_versions
WHERE account_id = $1
    AND is_current
    AND ($2::date IS NULL OR trade_date >= $2::date)
    AND ($3::date IS NULL OR trade_date <= $3::date)
ORDER BY trade_date
`

const navReconciliationColumns = `
    reconciliation_id,
    performance_nav_pk,
    account_id,
    trade_date,
    observed_trade_date,
    status,
    observed_visible_cash,
    observed_position_value,
    invisible_counter_cash,
    outstanding_settlement_assets,
    observed_open_assets,
    provisional_close_nav,
    overnight_external_net_flow,
    known_overnight_income_expense,
    residual,
    auto_threshold,
    warning_threshold,
    details,
    reviewed_by,
    reviewed_at,
    created_at,
    updated_at
`

const listNAVReconciliationsSQL = `
SELECT ` + navReconciliationColumns + `
FROM performance_nav_reconciliations
WHERE account_id = $1
    AND ($2::date IS NULL OR trade_date >= $2::date)
    AND ($3::date IS NULL OR trade_date <= $3::date)
ORDER BY trade_date DESC
`
