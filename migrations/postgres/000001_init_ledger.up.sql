-- relay PostgreSQL ledger schema.
-- This migration is intentionally plain SQL so it can be applied by psql,
-- golang-migrate, goose, or a deployment wrapper.

CREATE TABLE gateways (
    gateway_pk BIGSERIAL PRIMARY KEY,
    env TEXT NOT NULL,
    broker_id TEXT NOT NULL,
    gateway_id TEXT NOT NULL,
    stream_prefix TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    state_text TEXT NOT NULL DEFAULT '',
    pending_trade_count INTEGER NOT NULL DEFAULT 0 CHECK (pending_trade_count >= 0),
    pending_query_count INTEGER NOT NULL DEFAULT 0 CHECK (pending_query_count >= 0),
    last_heartbeat_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT gateways_status_check CHECK (status IN ('unknown', 'up', 'down', 'degraded')),
    CONSTRAINT gateways_identity_unique UNIQUE (env, broker_id, gateway_id),
    CONSTRAINT gateways_stream_prefix_unique UNIQUE (stream_prefix)
);

CREATE TABLE accounts (
    account_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL,
    broker_id TEXT NOT NULL,
    account_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'disabled',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    trading_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    simulated BOOLEAN NOT NULL DEFAULT FALSE,
    tags JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT accounts_account_id_unique UNIQUE (account_id),
    CONSTRAINT accounts_status_check CHECK (status IN ('enabled', 'disabled', 'readonly'))
);

CREATE TABLE account_gateway_routes (
    route_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    env TEXT NOT NULL,
    broker_id TEXT NOT NULL,
    gateway_id TEXT NOT NULL,
    stream_prefix TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT account_gateway_routes_account_unique UNIQUE (account_id),
    CONSTRAINT account_gateway_routes_gateway_fk FOREIGN KEY (env, broker_id, gateway_id)
        REFERENCES gateways(env, broker_id, gateway_id) ON DELETE RESTRICT
);

CREATE TABLE orders (
    order_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    client_order_id TEXT,
    gateway_order_id TEXT NOT NULL,
    order_id BIGINT,
    order_stream_id TEXT,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL,
    trade_side TEXT NOT NULL,
    business_type TEXT NOT NULL,
    offset_type TEXT,
    limit_price NUMERIC(20, 6) NOT NULL CHECK (limit_price > 0),
    order_qty BIGINT NOT NULL CHECK (order_qty > 0),
    submitted_qty BIGINT NOT NULL DEFAULT 0 CHECK (submitted_qty >= 0),
    cum_filled_qty BIGINT NOT NULL DEFAULT 0 CHECK (cum_filled_qty >= 0),
    leaves_qty BIGINT NOT NULL DEFAULT 0 CHECK (leaves_qty >= 0),
    cancelled_qty BIGINT NOT NULL DEFAULT 0 CHECK (cancelled_qty >= 0),
    invalid_qty BIGINT NOT NULL DEFAULT 0 CHECK (invalid_qty >= 0),
    avg_fill_price NUMERIC(20, 6),
    fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'created',
    gateway_status TEXT NOT NULL DEFAULT 'accepted',
    adapter_status_code INTEGER,
    adapter_status_name TEXT,
    is_terminal BOOLEAN NOT NULL DEFAULT FALSE,
    reject_code TEXT,
    reject_message TEXT,
    origin_message_id TEXT,
    request_id TEXT,
    idempotency_key TEXT,
    shareholder_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    inserted_at TIMESTAMPTZ,
    last_updated_at TIMESTAMPTZ,
    terminal_at TIMESTAMPTZ,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    adapter_context JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT orders_gateway_order_unique UNIQUE (account_id, gateway_order_id),
    CONSTRAINT orders_exchange_check CHECK (exchange IN ('SH', 'SZ', 'BJ')),
    CONSTRAINT orders_trade_side_check CHECK (trade_side IN ('B', 'S', 'P', 'R')),
    CONSTRAINT orders_business_type_check CHECK (business_type IN ('S', 'E')),
    CONSTRAINT orders_offset_type_check CHECK (offset_type IS NULL OR offset_type IN ('O', 'C')),
    CONSTRAINT orders_status_check CHECK (status IN ('created', 'accepted', 'working', 'partially_filled', 'filled', 'cancelled', 'rejected')),
    CONSTRAINT orders_gateway_status_check CHECK (gateway_status IN ('accepted', 'working', 'filled', 'cancelled', 'rejected')),
    CONSTRAINT orders_terminal_consistency_check CHECK (
        (is_terminal = TRUE AND status IN ('filled', 'cancelled', 'rejected'))
        OR is_terminal = FALSE
    )
);

CREATE UNIQUE INDEX orders_client_order_unique
    ON orders(account_id, client_order_id)
    WHERE client_order_id IS NOT NULL;

CREATE INDEX orders_status_idx ON orders(account_id, status);
CREATE INDEX orders_symbol_idx ON orders(symbol, exchange);
CREATE INDEX orders_origin_message_idx ON orders(origin_message_id);
CREATE INDEX orders_idempotency_idx ON orders(account_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE order_events (
    order_event_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL,
    gateway_order_id TEXT NOT NULL,
    event_id TEXT,
    event_type TEXT NOT NULL DEFAULT 'order.event',
    status TEXT NOT NULL,
    gateway_status TEXT NOT NULL,
    is_terminal BOOLEAN NOT NULL DEFAULT FALSE,
    stream_key TEXT,
    stream_id TEXT,
    origin_message_id TEXT,
    request_id TEXT,
    correlation_id TEXT,
    produced_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    adapter_context JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT order_events_order_fk FOREIGN KEY (account_id, gateway_order_id)
        REFERENCES orders(account_id, gateway_order_id) ON DELETE CASCADE,
    CONSTRAINT order_events_event_type_check CHECK (event_type = 'order.event'),
    CONSTRAINT order_events_status_check CHECK (status IN ('created', 'accepted', 'working', 'partially_filled', 'filled', 'cancelled', 'rejected')),
    CONSTRAINT order_events_gateway_status_check CHECK (gateway_status IN ('accepted', 'working', 'filled', 'cancelled', 'rejected'))
);

CREATE UNIQUE INDEX order_events_event_id_unique
    ON order_events(account_id, event_id)
    WHERE event_id IS NOT NULL;

CREATE UNIQUE INDEX order_events_stream_unique
    ON order_events(stream_key, stream_id)
    WHERE stream_key IS NOT NULL AND stream_id IS NOT NULL;

CREATE INDEX order_events_order_idx ON order_events(account_id, gateway_order_id, received_at);

CREATE TABLE fills (
    fill_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    fill_id TEXT,
    gateway_order_id TEXT NOT NULL,
    order_id BIGINT,
    order_stream_id TEXT,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL,
    trade_side TEXT NOT NULL,
    price NUMERIC(20, 6) NOT NULL CHECK (price > 0),
    qty BIGINT NOT NULL CHECK (qty > 0),
    fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    trade_date DATE,
    match_timestamp BIGINT,
    matched_at TIMESTAMPTZ,
    shareholder_id TEXT,
    stream_key TEXT,
    stream_id TEXT,
    origin_message_id TEXT,
    request_id TEXT,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    adapter_context JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fills_order_fk FOREIGN KEY (account_id, gateway_order_id)
        REFERENCES orders(account_id, gateway_order_id) ON DELETE RESTRICT,
    CONSTRAINT fills_exchange_check CHECK (exchange IN ('SH', 'SZ', 'BJ')),
    CONSTRAINT fills_trade_side_check CHECK (trade_side IN ('B', 'S', 'P', 'R'))
);

CREATE UNIQUE INDEX fills_fill_id_unique
    ON fills(account_id, fill_id)
    WHERE fill_id IS NOT NULL;

CREATE UNIQUE INDEX fills_fallback_unique
    ON fills(account_id, order_stream_id, match_timestamp, qty, price)
    WHERE fill_id IS NULL AND order_stream_id IS NOT NULL AND match_timestamp IS NOT NULL;

CREATE UNIQUE INDEX fills_stream_unique
    ON fills(stream_key, stream_id)
    WHERE stream_key IS NOT NULL AND stream_id IS NOT NULL;

CREATE INDEX fills_order_idx ON fills(account_id, gateway_order_id);
CREATE INDEX fills_symbol_idx ON fills(symbol, exchange);
CREATE INDEX fills_trade_date_idx ON fills(trade_date, account_id);

CREATE TABLE raw_stream_messages (
    raw_message_pk BIGSERIAL PRIMARY KEY,
    stream_key TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    direction TEXT NOT NULL,
    stream_role TEXT NOT NULL,
    message_type TEXT,
    action TEXT,
    event_type TEXT,
    status TEXT,
    code TEXT,
    account_id TEXT,
    gateway_order_id TEXT,
    origin_message_id TEXT,
    request_id TEXT,
    correlation_id TEXT,
    idempotency_key TEXT,
    body JSONB,
    body_text TEXT,
    parse_error TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT raw_stream_messages_unique UNIQUE (stream_key, stream_id),
    CONSTRAINT raw_stream_messages_direction_check CHECK (direction IN ('in', 'out')),
    CONSTRAINT raw_stream_messages_role_check CHECK (stream_role IN ('cmd.trade', 'cmd.query', 'reply', 'event', 'hb', 'dlq'))
);

CREATE INDEX raw_stream_messages_lookup_idx ON raw_stream_messages(account_id, gateway_order_id, received_at);
CREATE INDEX raw_stream_messages_origin_idx ON raw_stream_messages(origin_message_id);
CREATE INDEX raw_stream_messages_type_idx ON raw_stream_messages(message_type, event_type, action);

CREATE TABLE positions (
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL,
    quantity BIGINT NOT NULL DEFAULT 0,
    sellable_qty BIGINT NOT NULL DEFAULT 0,
    initial_qty BIGINT NOT NULL DEFAULT 0,
    today_qty BIGINT NOT NULL DEFAULT 0,
    avg_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    last_price NUMERIC(20, 6),
    market_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    day_unrealized_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    settled_profit NUMERIC(20, 6) NOT NULL DEFAULT 0,
    shareholder_id TEXT,
    source TEXT NOT NULL DEFAULT 'query',
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, symbol, exchange),
    CONSTRAINT positions_exchange_check CHECK (exchange IN ('SH', 'SZ', 'BJ')),
    CONSTRAINT positions_qty_check CHECK (quantity >= 0 AND sellable_qty >= 0 AND initial_qty >= 0 AND today_qty >= 0)
);

CREATE TABLE position_snapshots (
    position_snapshot_pk BIGSERIAL PRIMARY KEY,
    trade_date DATE NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL,
    quantity BIGINT NOT NULL DEFAULT 0,
    sellable_qty BIGINT NOT NULL DEFAULT 0,
    initial_qty BIGINT NOT NULL DEFAULT 0,
    today_qty BIGINT NOT NULL DEFAULT 0,
    avg_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    last_price NUMERIC(20, 6),
    market_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    unrealized_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    day_unrealized_pnl NUMERIC(20, 6) NOT NULL DEFAULT 0,
    settled_profit NUMERIC(20, 6) NOT NULL DEFAULT 0,
    shareholder_id TEXT,
    source TEXT NOT NULL DEFAULT 'close',
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT position_snapshots_unique UNIQUE (trade_date, account_id, symbol, exchange),
    CONSTRAINT position_snapshots_exchange_check CHECK (exchange IN ('SH', 'SZ', 'BJ'))
);

CREATE INDEX position_snapshots_account_date_idx ON position_snapshots(account_id, trade_date);

CREATE TABLE asset_snapshots (
    asset_snapshot_pk BIGSERIAL PRIMARY KEY,
    trade_date DATE NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    snapshot_type TEXT NOT NULL DEFAULT 'intraday',
    cash_available NUMERIC(20, 6) NOT NULL DEFAULT 0,
    cash_total NUMERIC(20, 6) NOT NULL DEFAULT 0,
    net_asset NUMERIC(20, 6) NOT NULL DEFAULT 0,
    market_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    stock_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    fund_value NUMERIC(20, 6) NOT NULL DEFAULT 0,
    commission NUMERIC(20, 6) NOT NULL DEFAULT 0,
    day_profit NUMERIC(20, 6) NOT NULL DEFAULT 0,
    position_profit NUMERIC(20, 6) NOT NULL DEFAULT 0,
    close_profit NUMERIC(20, 6) NOT NULL DEFAULT 0,
    credit NUMERIC(20, 6) NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT 'query',
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT asset_snapshots_unique UNIQUE (trade_date, account_id, snapshot_type),
    CONSTRAINT asset_snapshots_type_check CHECK (snapshot_type IN ('intraday', 'open', 'close', 'reconcile'))
);

CREATE INDEX asset_snapshots_account_date_idx ON asset_snapshots(account_id, trade_date);

CREATE TABLE cash_ledger (
    cash_ledger_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    ledger_type TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'CNY',
    amount NUMERIC(20, 6) NOT NULL,
    balance_after NUMERIC(20, 6),
    gateway_order_id TEXT,
    fill_id TEXT,
    description TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'system',
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cash_ledger_type_check CHECK (ledger_type IN ('freeze', 'unfreeze', 'trade', 'fee', 'deposit', 'withdraw', 'settlement', 'adjustment'))
);

CREATE INDEX cash_ledger_account_date_idx ON cash_ledger(account_id, trade_date);
CREATE INDEX cash_ledger_order_idx ON cash_ledger(account_id, gateway_order_id) WHERE gateway_order_id IS NOT NULL;

CREATE TABLE reconciliation_runs (
    reconciliation_run_pk BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL,
    trade_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    source TEXT NOT NULL DEFAULT 'cron',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_message TEXT,
    CONSTRAINT reconciliation_runs_run_id_unique UNIQUE (run_id),
    CONSTRAINT reconciliation_runs_status_check CHECK (status IN ('running', 'completed', 'failed'))
);

CREATE TABLE reconciliation_inputs (
    reconciliation_input_pk BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES reconciliation_runs(run_id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    input_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX reconciliation_inputs_run_idx ON reconciliation_inputs(run_id, input_type);

CREATE TABLE reconciliation_breaks (
    reconciliation_break_pk BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES reconciliation_runs(run_id) ON DELETE CASCADE,
    account_id TEXT,
    break_type TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'warning',
    status TEXT NOT NULL DEFAULT 'open',
    object_type TEXT NOT NULL,
    object_id TEXT,
    internal_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    external_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    CONSTRAINT reconciliation_breaks_severity_check CHECK (severity IN ('info', 'warning', 'critical')),
    CONSTRAINT reconciliation_breaks_status_check CHECK (status IN ('open', 'resolved', 'ignored'))
);

CREATE INDEX reconciliation_breaks_run_idx ON reconciliation_breaks(run_id, status);
CREATE INDEX reconciliation_breaks_account_idx ON reconciliation_breaks(account_id, object_type);
