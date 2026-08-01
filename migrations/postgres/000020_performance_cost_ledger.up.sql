CREATE TABLE performance_account_inceptions (
    account_id TEXT PRIMARY KEY REFERENCES accounts(account_id) ON DELETE RESTRICT,
    inception_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft',
    clean_start BOOLEAN NOT NULL DEFAULT FALSE,
    opening_cash NUMERIC(20, 6) NOT NULL DEFAULT 0,
    opening_position_source TEXT NOT NULL DEFAULT 'broker_open_snapshot',
    cost_source TEXT NOT NULL DEFAULT 'broker_open_snapshot',
    strategy_scope JSONB NOT NULL DEFAULT '[]'::jsonb,
    description TEXT NOT NULL DEFAULT '',
    confirmed_by TEXT,
    confirmed_at TIMESTAMPTZ,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_account_inceptions_status_check
        CHECK (status IN ('draft', 'confirmed', 'voided')),
    CONSTRAINT performance_account_inceptions_opening_cash_check
        CHECK (opening_cash >= 0)
);

CREATE INDEX performance_account_inceptions_status_idx
    ON performance_account_inceptions(status, inception_date);

CREATE TABLE performance_position_cost_states (
    position_cost_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    symbol TEXT NOT NULL,
    exchange TEXT NOT NULL,
    cost_bucket TEXT NOT NULL DEFAULT 'CORE',
    status TEXT NOT NULL DEFAULT 'calculated',
    formula_version TEXT NOT NULL DEFAULT 'performance_position_cost.v1',
    open_quantity BIGINT NOT NULL DEFAULT 0,
    open_total_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    buy_quantity BIGINT NOT NULL DEFAULT 0,
    buy_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    buy_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    sell_quantity BIGINT NOT NULL DEFAULT 0,
    sell_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    sell_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    realized_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    close_quantity BIGINT NOT NULL DEFAULT 0,
    close_total_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    average_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    broker_close_quantity BIGINT NOT NULL DEFAULT 0,
    quantity_residual BIGINT NOT NULL DEFAULT 0,
    close_price NUMERIC(20, 6) NOT NULL DEFAULT 0,
    close_market_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    fee_source TEXT NOT NULL DEFAULT 'none',
    opening_source TEXT NOT NULL DEFAULT '',
    quality_flags JSONB NOT NULL DEFAULT '[]'::jsonb,
    source TEXT NOT NULL DEFAULT 'relay.performance.cost_ledger',
    calculated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_position_cost_states_unique
        UNIQUE (account_id, trade_date, symbol, exchange, cost_bucket),
    CONSTRAINT performance_position_cost_states_exchange_check
        CHECK (exchange IN ('SH', 'SZ', 'BJ')),
    CONSTRAINT performance_position_cost_states_status_check
        CHECK (status IN ('calculated', 'estimated', 'blocked')),
    CONSTRAINT performance_position_cost_states_quantity_check
        CHECK (
            open_quantity >= 0
            AND buy_quantity >= 0
            AND sell_quantity >= 0
            AND close_quantity >= 0
            AND broker_close_quantity >= 0
        ),
    CONSTRAINT performance_position_cost_states_cost_check
        CHECK (open_total_cost >= 0 AND close_total_cost >= 0 AND average_cost >= 0)
);

CREATE INDEX performance_position_cost_states_account_date_idx
    ON performance_position_cost_states(account_id, trade_date, status);

CREATE INDEX performance_position_cost_states_security_idx
    ON performance_position_cost_states(symbol, exchange, trade_date);
