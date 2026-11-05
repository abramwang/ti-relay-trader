-- Isolated CTP history application, not trading readiness or Redis ACK state.
CREATE TABLE futures_query_trade_applications (
    batch_id TEXT PRIMARY KEY REFERENCES futures_query_batches(batch_id),
    scope_key TEXT NOT NULL REFERENCES futures_trade_scopes(scope_key),
    row_count INTEGER NOT NULL CHECK (row_count >= 0 AND row_count <= 1024),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX futures_query_trade_application_scope ON futures_query_trade_applications(scope_key);
