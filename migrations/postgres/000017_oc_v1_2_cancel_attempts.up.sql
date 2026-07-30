BEGIN;

CREATE TABLE order_cancel_attempts (
    cancel_attempt_pk BIGSERIAL PRIMARY KEY,
    attempt_id TEXT NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    gateway_order_id TEXT NOT NULL,
    order_id BIGINT,
    order_stream_id TEXT,
    origin_message_id TEXT,
    request_id TEXT,
    correlation_id TEXT,
    status TEXT NOT NULL,
    code TEXT,
    message TEXT,
    retry_safe BOOLEAN,
    order_state_changed BOOLEAN,
    reconciliation_required BOOLEAN NOT NULL DEFAULT FALSE,
    occurred_at TIMESTAMPTZ NOT NULL,
    stream_key TEXT,
    stream_id TEXT,
    raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    adapter_context JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT order_cancel_attempts_identity_unique UNIQUE (account_id, attempt_id),
    CONSTRAINT order_cancel_attempts_status_check
        CHECK (status IN ('accepted', 'rejected', 'timeout', 'outcome_unknown', 'not_ready', 'failed'))
);

CREATE INDEX order_cancel_attempts_order_idx
    ON order_cancel_attempts(account_id, trade_date DESC, gateway_order_id, occurred_at DESC);

CREATE INDEX order_cancel_attempts_reconciliation_idx
    ON order_cancel_attempts(account_id, occurred_at DESC)
    WHERE reconciliation_required = TRUE;

COMMIT;
