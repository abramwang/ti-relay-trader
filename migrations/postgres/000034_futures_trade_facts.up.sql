CREATE TABLE futures_trade_scopes (
    scope_key TEXT PRIMARY KEY,
    context_json JSONB NOT NULL,
    format_version INTEGER NOT NULL DEFAULT 1 CHECK (format_version=1),
    audit_count INTEGER NOT NULL DEFAULT 0 CHECK (audit_count BETWEEN 0 AND 1024),
    audit_bytes BIGINT NOT NULL DEFAULT 0 CHECK (audit_bytes BETWEEN 0 AND 33554432),
    report BYTEA,
    report_sha256 BYTEA CHECK (octet_length(report_sha256)=32),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE futures_trade_observations (
    observation_id BIGSERIAL PRIMARY KEY,
    scope_key TEXT NOT NULL REFERENCES futures_trade_scopes(scope_key),
    source_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('order','fill')),
    body BYTEA NOT NULL CHECK (octet_length(body)<=8388608),
    body_sha256 BYTEA NOT NULL CHECK (octet_length(body_sha256)=32),
    source_conflict BOOLEAN NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(scope_key,source_id,kind,body_sha256)
);
CREATE INDEX futures_trade_observations_scope_idx ON futures_trade_observations(scope_key,observation_id);
CREATE TABLE futures_order_states (
    scope_key TEXT NOT NULL REFERENCES futures_trade_scopes(scope_key),
    gateway_order_id TEXT NOT NULL,
    record JSONB NOT NULL,
    blocked BOOLEAN NOT NULL,
    PRIMARY KEY(scope_key,gateway_order_id)
);
CREATE TABLE futures_execution_facts (
    scope_key TEXT NOT NULL REFERENCES futures_trade_scopes(scope_key),
    fill_id TEXT NOT NULL,
    record JSONB NOT NULL,
    link_status TEXT NOT NULL CHECK (link_status IN ('pending','linked','conflict')),
    linked_gateway TEXT,
    PRIMARY KEY(scope_key,fill_id),
    -- Only an established link references an order. Pending facts need no order.
    FOREIGN KEY(scope_key,linked_gateway) REFERENCES futures_order_states(scope_key,gateway_order_id),
    CHECK ((link_status='linked') = (linked_gateway IS NOT NULL))
);
