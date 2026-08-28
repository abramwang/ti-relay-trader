CREATE TABLE performance_etf_settlement_versions (
    etf_settlement_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    source_trade_date DATE NOT NULL,
    security_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    is_current BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL,
    settlement_complete BOOLEAN NOT NULL DEFAULT FALSE,
    redemption_quantity BIGINT NOT NULL,
    redemption_unit BIGINT NOT NULL,
    buy_gross_amount NUMERIC(20, 6) NOT NULL,
    component_sale_gross_amount NUMERIC(20, 6) NOT NULL,
    actual_cash_component NUMERIC(20, 6) NOT NULL DEFAULT 0,
    actual_cash_substitution NUMERIC(20, 6) NOT NULL DEFAULT 0,
    actual_total_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    source_close_settlement_carry NUMERIC(20, 6) NOT NULL DEFAULT 0,
    gross_contribution NUMERIC(20, 6) NOT NULL,
    net_contribution NUMERIC(20, 6) NOT NULL,
    pcf_trade_date DATE,
    pcf_schema_version TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL,
    confirmed_by TEXT NOT NULL DEFAULT '',
    confirmed_at TIMESTAMPTZ,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_etf_settlement_versions_unique
        UNIQUE (account_id, source_trade_date, security_id, version),
    CONSTRAINT performance_etf_settlement_status_check
        CHECK (status IN ('pending', 'confirmed', 'voided')),
    CONSTRAINT performance_etf_settlement_identity_check
        CHECK (
            btrim(security_id) <> ''
            AND btrim(source) <> ''
            AND redemption_quantity > 0
            AND redemption_unit > 0
            AND redemption_quantity % redemption_unit = 0
            AND buy_gross_amount >= 0
            AND component_sale_gross_amount >= 0
            AND actual_cash_substitution >= 0
            AND actual_total_fee >= 0
            AND abs(gross_contribution - (
                component_sale_gross_amount
                + actual_cash_component
                + actual_cash_substitution
                - buy_gross_amount
            )) <= 0.01
            AND abs(net_contribution - (gross_contribution - actual_total_fee)) <= 0.01
        ),
    CONSTRAINT performance_etf_settlement_confirmation_check
        CHECK (
            status <> 'confirmed'
            OR (
                settlement_complete
                AND btrim(confirmed_by) <> ''
                AND confirmed_at IS NOT NULL
                AND pcf_trade_date IS NOT NULL
                AND btrim(pcf_schema_version) <> ''
            )
        )
);

CREATE UNIQUE INDEX performance_etf_settlement_current_unique
    ON performance_etf_settlement_versions(account_id, source_trade_date, security_id)
    WHERE is_current;

CREATE INDEX performance_etf_settlement_series_idx
    ON performance_etf_settlement_versions(account_id, source_trade_date, status);
