CREATE TABLE test_legacy_order_resolutions (
    resolution_pk BIGSERIAL PRIMARY KEY,
    environment TEXT NOT NULL CHECK (environment = 'test'),
    order_pk BIGINT NOT NULL REFERENCES orders(order_pk) ON DELETE RESTRICT,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    gateway_order_id TEXT NOT NULL,
    counter_session_id TEXT NOT NULL CHECK (counter_session_id <> ''),
    confirmed_by TEXT NOT NULL CHECK (confirmed_by <> ''),
    reason TEXT NOT NULL CHECK (reason <> ''),
    order_snapshot JSONB NOT NULL,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_pk, counter_session_id)
);

CREATE INDEX test_legacy_order_resolution_scope_idx
ON test_legacy_order_resolutions(account_id, counter_session_id);
