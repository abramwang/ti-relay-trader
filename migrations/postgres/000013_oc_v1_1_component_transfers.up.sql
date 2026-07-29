DROP INDEX IF EXISTS fills_fallback_unique;

CREATE UNIQUE INDEX fills_fallback_unique
    ON fills(
        account_id,
        trade_date,
        order_stream_id,
        COALESCE(order_id, 0),
        symbol,
        exchange,
        match_timestamp,
        qty,
        price
    )
    WHERE fill_id IS NULL
        AND order_stream_id IS NOT NULL
        AND match_timestamp IS NOT NULL;

CREATE TABLE etf_component_transfers (
    transfer_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    fill_id TEXT,
    gateway_order_id TEXT NOT NULL,
    order_id BIGINT,
    order_stream_id TEXT,
    symbol TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL,
    price NUMERIC(20, 6) NOT NULL DEFAULT 0,
    qty BIGINT NOT NULL CHECK (qty > 0),
    trade_side TEXT NOT NULL,
    business_type TEXT NOT NULL,
    record_type TEXT NOT NULL DEFAULT 'etf_component_transfer',
    transfer_type TEXT,
    is_transfer BOOLEAN NOT NULL DEFAULT TRUE,
    component_symbol TEXT NOT NULL,
    component_name TEXT NOT NULL DEFAULT '',
    component_exchange TEXT NOT NULL,
    component_qty BIGINT NOT NULL CHECK (component_qty > 0),
    component_value NUMERIC(20, 6),
    cash_substitution BOOLEAN NOT NULL DEFAULT FALSE,
    broker_trade_side TEXT,
    broker_business_type TEXT,
    trade_date DATE NOT NULL,
    match_timestamp BIGINT,
    matched_at TIMESTAMPTZ,
    shareholder_id TEXT,
    strategy_type TEXT,
    strategy_id TEXT,
    basket_id TEXT,
    parent_order_id TEXT,
    t0_order_group_id TEXT,
    stream_key TEXT,
    stream_id TEXT,
    origin_message_id TEXT,
    request_id TEXT,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    adapter_context JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT etf_component_transfers_exchange_check
        CHECK (exchange IN ('SH', 'SZ', 'BJ')),
    CONSTRAINT etf_component_transfers_component_exchange_check
        CHECK (component_exchange IN ('SH', 'SZ', 'BJ')),
    CONSTRAINT etf_component_transfers_trade_side_check
        CHECK (trade_side IN ('B', 'S', 'P', 'R')),
    CONSTRAINT etf_component_transfers_business_type_check
        CHECK (business_type = 'E'),
    CONSTRAINT etf_component_transfers_record_type_check
        CHECK (record_type = 'etf_component_transfer'),
    CONSTRAINT etf_component_transfers_is_transfer_check
        CHECK (is_transfer = TRUE)
);

CREATE UNIQUE INDEX etf_component_transfers_fill_id_unique
    ON etf_component_transfers(account_id, trade_date, gateway_order_id, fill_id)
    WHERE fill_id IS NOT NULL;

CREATE UNIQUE INDEX etf_component_transfers_fallback_unique
    ON etf_component_transfers(
        account_id,
        trade_date,
        gateway_order_id,
        order_stream_id,
        order_id,
        component_symbol,
        component_exchange,
        component_qty,
        COALESCE(match_timestamp, 0)
    )
    WHERE fill_id IS NULL;

CREATE UNIQUE INDEX etf_component_transfers_stream_unique
    ON etf_component_transfers(stream_key, stream_id)
    WHERE stream_key IS NOT NULL AND stream_id IS NOT NULL;

CREATE INDEX etf_component_transfers_order_idx
    ON etf_component_transfers(account_id, trade_date, gateway_order_id);

CREATE INDEX etf_component_transfers_component_idx
    ON etf_component_transfers(component_symbol, component_exchange);

CREATE INDEX etf_component_transfers_trade_date_idx
    ON etf_component_transfers(trade_date, account_id);

CREATE INDEX raw_stream_messages_adapter_data_quality_idx
    ON raw_stream_messages(account_id, received_at)
    WHERE stream_role = 'dlq' AND action = 'adapter.data_quality';
