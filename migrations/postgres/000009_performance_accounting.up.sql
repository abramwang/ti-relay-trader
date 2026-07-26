CREATE TABLE performance_fee_rules (
    fee_rule_pk BIGSERIAL PRIMARY KEY,
    rule_id TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    version INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft',
    name TEXT NOT NULL DEFAULT '',
    market TEXT NOT NULL DEFAULT '*',
    instrument_type TEXT NOT NULL DEFAULT '*',
    business_type TEXT NOT NULL DEFAULT '*',
    trade_side TEXT NOT NULL DEFAULT '*',
    commission_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    minimum_commission NUMERIC(20, 6) NOT NULL DEFAULT 0,
    stamp_duty_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    transfer_fee_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    handling_fee_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    other_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    fixed_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    repo_fee_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    estimated_friction_rate NUMERIC(20, 10) NOT NULL DEFAULT 0,
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_by TEXT NOT NULL DEFAULT 'system',
    activated_by TEXT,
    activated_at TIMESTAMPTZ,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_fee_rules_account_version_unique UNIQUE (account_id, version),
    CONSTRAINT performance_fee_rules_status_check CHECK (status IN ('draft', 'active', 'retired')),
    CONSTRAINT performance_fee_rules_dates_check CHECK (effective_to IS NULL OR effective_to >= effective_from),
    CONSTRAINT performance_fee_rules_rates_check CHECK (
        commission_rate >= 0
        AND minimum_commission >= 0
        AND stamp_duty_rate >= 0
        AND transfer_fee_rate >= 0
        AND handling_fee_rate >= 0
        AND other_rate >= 0
        AND fixed_fee >= 0
        AND repo_fee_rate >= 0
        AND estimated_friction_rate >= 0
    )
);

CREATE INDEX performance_fee_rules_effective_idx
    ON performance_fee_rules(account_id, effective_from, effective_to, status);

ALTER TABLE cash_ledger
    DROP CONSTRAINT IF EXISTS cash_ledger_type_check;

ALTER TABLE cash_ledger
    ADD COLUMN entry_id TEXT,
    ADD COLUMN flow_class TEXT NOT NULL DEFAULT 'operational',
    ADD COLUMN cash_bucket TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN counterparty_bucket TEXT,
    ADD COLUMN effective_at TIMESTAMPTZ,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'confirmed',
    ADD COLUMN transfer_group_id TEXT,
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN created_by TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN confirmed_by TEXT,
    ADD COLUMN confirmed_at TIMESTAMPTZ,
    ADD COLUMN voided_by TEXT,
    ADD COLUMN voided_at TIMESTAMPTZ,
    ADD COLUMN reversal_of_entry_id TEXT;

UPDATE cash_ledger
SET
    entry_id = 'legacy-' || cash_ledger_pk::text,
    effective_at = created_at,
    flow_class = CASE
        WHEN ledger_type IN ('deposit', 'withdraw') THEN 'external_flow'
        WHEN ledger_type = 'fee' THEN 'income_expense'
        WHEN ledger_type IN ('settlement', 'adjustment') THEN 'settlement_adjustment'
        ELSE 'operational'
    END,
    confirmed_at = created_at
WHERE entry_id IS NULL OR effective_at IS NULL;

ALTER TABLE cash_ledger
    ALTER COLUMN entry_id SET NOT NULL,
    ALTER COLUMN effective_at SET NOT NULL,
    ADD CONSTRAINT cash_ledger_entry_id_unique UNIQUE (entry_id),
    ADD CONSTRAINT cash_ledger_type_check CHECK (
        ledger_type IN (
            'freeze',
            'unfreeze',
            'trade',
            'fee',
            'deposit',
            'withdraw',
            'internal_transfer_in',
            'internal_transfer_out',
            'interest',
            'dividend',
            'reverse_repo_repayment',
            'settlement',
            'adjustment'
        )
    ),
    ADD CONSTRAINT cash_ledger_flow_class_check CHECK (
        flow_class IN ('external_flow', 'internal_transfer', 'income_expense', 'settlement_adjustment', 'operational')
    ),
    ADD CONSTRAINT cash_ledger_status_check CHECK (status IN ('draft', 'confirmed', 'voided'));

CREATE UNIQUE INDEX cash_ledger_idempotency_unique
    ON cash_ledger(account_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX cash_ledger_effective_idx
    ON cash_ledger(account_id, effective_at, status);

CREATE INDEX cash_ledger_transfer_group_idx
    ON cash_ledger(account_id, transfer_group_id)
    WHERE transfer_group_id IS NOT NULL AND transfer_group_id <> '';

CREATE TABLE performance_nav_baselines (
    baseline_pk BIGSERIAL PRIMARY KEY,
    baseline_id TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    effective_date DATE NOT NULL,
    initial_economic_nav NUMERIC(20, 6) NOT NULL,
    status TEXT NOT NULL DEFAULT 'confirmed',
    source TEXT NOT NULL DEFAULT 'manual',
    description TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT 'system',
    confirmed_by TEXT,
    confirmed_at TIMESTAMPTZ,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_nav_baselines_account_date_unique UNIQUE (account_id, effective_date),
    CONSTRAINT performance_nav_baselines_value_check CHECK (initial_economic_nav > 0),
    CONSTRAINT performance_nav_baselines_status_check CHECK (status IN ('draft', 'confirmed', 'voided'))
);

CREATE TABLE performance_nav_versions (
    performance_nav_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    version INTEGER NOT NULL,
    is_current BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL DEFAULT 'provisional',
    formula_version TEXT NOT NULL DEFAULT 'performance_economic_nav.v1',
    open_economic_nav NUMERIC(20, 6) NOT NULL,
    external_net_flow NUMERIC(20, 6) NOT NULL DEFAULT 0,
    account_day_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    settlement_adjustment NUMERIC(20, 6) NOT NULL DEFAULT 0,
    close_economic_nav NUMERIC(20, 6) NOT NULL,
    return_denominator NUMERIC(20, 6) NOT NULL DEFAULT 0,
    daily_return NUMERIC(24, 12) NOT NULL DEFAULT 0,
    cumulative_nav NUMERIC(24, 12) NOT NULL DEFAULT 1,
    pnl_components JSONB NOT NULL DEFAULT '{}'::jsonb,
    quality_flags JSONB NOT NULL DEFAULT '[]'::jsonb,
    source TEXT NOT NULL DEFAULT 'relay',
    finalized_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_nav_versions_unique UNIQUE (account_id, trade_date, version),
    CONSTRAINT performance_nav_versions_status_check CHECK (status IN ('provisional', 'finalized', 'blocked')),
    CONSTRAINT performance_nav_versions_values_check CHECK (
        open_economic_nav > 0
        AND close_economic_nav > 0
        AND return_denominator >= 0
    )
);

CREATE UNIQUE INDEX performance_nav_versions_current_unique
    ON performance_nav_versions(account_id, trade_date)
    WHERE is_current;

CREATE INDEX performance_nav_versions_series_idx
    ON performance_nav_versions(account_id, trade_date, status);

CREATE TABLE performance_nav_reconciliations (
    nav_reconciliation_pk BIGSERIAL PRIMARY KEY,
    reconciliation_id TEXT NOT NULL UNIQUE,
    performance_nav_pk BIGINT NOT NULL REFERENCES performance_nav_versions(performance_nav_pk) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    observed_trade_date DATE NOT NULL,
    status TEXT NOT NULL,
    observed_visible_cash NUMERIC(20, 6) NOT NULL DEFAULT 0,
    observed_position_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    invisible_counter_cash NUMERIC(20, 6) NOT NULL DEFAULT 0,
    outstanding_settlement_assets NUMERIC(20, 6) NOT NULL DEFAULT 0,
    observed_open_assets NUMERIC(20, 6) NOT NULL DEFAULT 0,
    provisional_close_nav NUMERIC(20, 6) NOT NULL DEFAULT 0,
    overnight_external_net_flow NUMERIC(20, 6) NOT NULL DEFAULT 0,
    known_overnight_income_expense NUMERIC(20, 6) NOT NULL DEFAULT 0,
    residual NUMERIC(20, 6) NOT NULL DEFAULT 0,
    auto_threshold NUMERIC(20, 6) NOT NULL DEFAULT 0,
    warning_threshold NUMERIC(20, 6) NOT NULL DEFAULT 0,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_nav_reconciliations_nav_unique UNIQUE (performance_nav_pk),
    CONSTRAINT performance_nav_reconciliations_status_check CHECK (
        status IN ('auto_completed', 'review_required', 'blocked', 'confirmed')
    )
);

CREATE INDEX performance_nav_reconciliations_account_date_idx
    ON performance_nav_reconciliations(account_id, trade_date);

CREATE TABLE reverse_repo_accruals (
    reverse_repo_accrual_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    gateway_order_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    principal NUMERIC(20, 6) NOT NULL,
    weighted_rate_pct NUMERIC(20, 10) NOT NULL,
    actual_occupation_days INTEGER NOT NULL,
    first_settlement_date DATE NOT NULL,
    maturity_settlement_date DATE NOT NULL,
    gross_interest NUMERIC(20, 6) NOT NULL,
    actual_fee NUMERIC(20, 6),
    estimated_fee NUMERIC(20, 6),
    effective_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    net_interest NUMERIC(20, 6) NOT NULL,
    receivable NUMERIC(20, 6) NOT NULL,
    status TEXT NOT NULL DEFAULT 'estimated',
    fee_source TEXT NOT NULL DEFAULT 'missing',
    quality_flags JSONB NOT NULL DEFAULT '[]'::jsonb,
    source_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    calculated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at TIMESTAMPTZ,
    CONSTRAINT reverse_repo_accruals_unique UNIQUE (account_id, trade_date, gateway_order_id),
    CONSTRAINT reverse_repo_accruals_status_check CHECK (status IN ('estimated', 'settled', 'unsupported')),
    CONSTRAINT reverse_repo_accruals_values_check CHECK (
        principal > 0
        AND weighted_rate_pct >= 0
        AND actual_occupation_days > 0
        AND gross_interest >= 0
        AND effective_fee >= 0
        AND receivable > 0
    )
);

CREATE INDEX reverse_repo_accruals_account_date_idx
    ON reverse_repo_accruals(account_id, trade_date);
