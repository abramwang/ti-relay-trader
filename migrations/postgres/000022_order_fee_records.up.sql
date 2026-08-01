CREATE TABLE order_fee_records (
    order_fee_record_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    fee_record_id TEXT NOT NULL,
    trade_date DATE NOT NULL,
    record_scope TEXT NOT NULL DEFAULT 'order',
    gateway_order_id TEXT NOT NULL DEFAULT '',
    order_id BIGINT NOT NULL DEFAULT 0,
    order_stream_id TEXT NOT NULL DEFAULT '',
    fill_id TEXT NOT NULL DEFAULT '',
    symbol TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL DEFAULT '',
    trade_side TEXT NOT NULL DEFAULT '',
    business_type TEXT NOT NULL DEFAULT '',
    order_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    turnover NUMERIC(20, 6) NOT NULL DEFAULT 0,
    commission NUMERIC(20, 6) NOT NULL DEFAULT 0,
    stamp_tax NUMERIC(20, 6) NOT NULL DEFAULT 0,
    transfer_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    handling_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    regulatory_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    settlement_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    other_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    total_fee NUMERIC(20, 6) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'CNY',
    fee_complete BOOLEAN NOT NULL DEFAULT FALSE,
    fee_source TEXT NOT NULL DEFAULT 'unavailable',
    fee_as_of TIMESTAMPTZ NOT NULL,
    settled_at TIMESTAMPTZ,
    association_complete BOOLEAN NOT NULL DEFAULT FALSE,
    adapter_context JSONB NOT NULL DEFAULT '{}'::jsonb,
    origin_message_id TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    stream_key TEXT NOT NULL DEFAULT '',
    stream_id TEXT NOT NULL DEFAULT '',
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT order_fee_records_identity_unique UNIQUE (account_id, fee_record_id),
    CONSTRAINT order_fee_records_scope_check CHECK (record_scope = 'order'),
    CONSTRAINT order_fee_records_amounts_check CHECK (
        order_amount >= 0
        AND turnover >= 0
        AND commission >= 0
        AND stamp_tax >= 0
        AND transfer_fee >= 0
        AND handling_fee >= 0
        AND regulatory_fee >= 0
        AND settlement_fee >= 0
        AND other_fee >= 0
        AND total_fee >= 0
    ),
    CONSTRAINT order_fee_records_source_check CHECK (
        btrim(fee_record_id) <> ''
        AND btrim(fee_source) <> ''
        AND btrim(currency) <> ''
    ),
    CONSTRAINT order_fee_records_association_check CHECK (
        NOT association_complete OR btrim(gateway_order_id) <> ''
    )
);

CREATE INDEX order_fee_records_account_date_idx
    ON order_fee_records(account_id, trade_date, fee_complete, association_complete);

CREATE INDEX order_fee_records_order_idx
    ON order_fee_records(account_id, trade_date, gateway_order_id)
    WHERE gateway_order_id <> '';

