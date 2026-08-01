CREATE TABLE performance_nav_gold_versions (
    performance_nav_gold_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    version INTEGER NOT NULL,
    is_current BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL DEFAULT 'draft',
    carried_open_asset NUMERIC(20, 6) NOT NULL,
    observed_open_asset NUMERIC(20, 6)
        GENERATED ALWAYS AS (close_asset - daily_pnl) STORED,
    overnight_adjustment NUMERIC(20, 6)
        GENERATED ALWAYS AS (close_asset - daily_pnl - carried_open_asset) STORED,
    close_asset NUMERIC(20, 6) NOT NULL,
    daily_pnl NUMERIC(20, 6) NOT NULL,
    asset_scope TEXT NOT NULL DEFAULT 'excluding_fund_occupancy',
    source TEXT NOT NULL DEFAULT 'manual_user_confirmed',
    source_ref TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    confirmed_by TEXT,
    confirmed_at TIMESTAMPTZ,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_nav_gold_versions_unique
        UNIQUE (account_id, trade_date, source, version),
    CONSTRAINT performance_nav_gold_versions_version_check
        CHECK (version > 0),
    CONSTRAINT performance_nav_gold_versions_status_check
        CHECK (status IN ('draft', 'confirmed', 'voided')),
    CONSTRAINT performance_nav_gold_versions_asset_check
        CHECK (
            carried_open_asset >= 0
            AND close_asset >= 0
            AND close_asset - daily_pnl >= 0
        ),
    CONSTRAINT performance_nav_gold_versions_source_check
        CHECK (btrim(source) <> '' AND btrim(source_ref) <> '' AND btrim(content_hash) <> ''),
    CONSTRAINT performance_nav_gold_versions_confirmation_check
        CHECK (status <> 'confirmed' OR (confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL))
);

CREATE UNIQUE INDEX performance_nav_gold_versions_current_unique
    ON performance_nav_gold_versions(account_id, trade_date, source)
    WHERE is_current;

CREATE INDEX performance_nav_gold_versions_series_idx
    ON performance_nav_gold_versions(account_id, trade_date, status)
    WHERE is_current;

