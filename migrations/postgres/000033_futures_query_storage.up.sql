-- Isolated CTP query storage. No stock ledger tables or production routing change.
CREATE TABLE futures_query_heads (
    scope_key TEXT PRIMARY KEY,
    target_batch TEXT,
    current_batch TEXT
);

CREATE TABLE futures_query_batches (
    batch_id TEXT PRIMARY KEY,
    scope_key TEXT NOT NULL REFERENCES futures_query_heads(scope_key),
    origin_message_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    retired BOOLEAN NOT NULL DEFAULT FALSE,
    format_version INTEGER NOT NULL DEFAULT 1 CHECK (format_version = 1),
    context_json JSONB NOT NULL CHECK (jsonb_typeof(context_json) = 'object'),
    limits_json JSONB NOT NULL CHECK (jsonb_typeof(limits_json) = 'object'),
    timeout_ns BIGINT NOT NULL CHECK (timeout_ns > 0),
    elapsed_ns BIGINT NOT NULL DEFAULT 0 CHECK (elapsed_ns >= 0),
    state TEXT NOT NULL CHECK (state IN ('collecting','ready','failed','invalid','expired','interrupted')),
    reason TEXT NOT NULL DEFAULT '',
    audit_count INTEGER NOT NULL DEFAULT 0 CHECK (audit_count BETWEEN 0 AND 1024),
    audit_bytes BIGINT NOT NULL DEFAULT 0 CHECK (audit_bytes BETWEEN 0 AND 33554432),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (scope_key, origin_message_id),
    UNIQUE (scope_key, batch_id)
);

ALTER TABLE futures_query_heads ADD CONSTRAINT futures_target_scope_fk
    FOREIGN KEY (scope_key, target_batch) REFERENCES futures_query_batches(scope_key, batch_id);
ALTER TABLE futures_query_heads ADD CONSTRAINT futures_current_scope_fk
    FOREIGN KEY (scope_key, current_batch) REFERENCES futures_query_batches(scope_key, batch_id);

CREATE TABLE futures_query_observations (
    observation_id BIGSERIAL PRIMARY KEY,
    batch_id TEXT NOT NULL REFERENCES futures_query_batches(batch_id),
    stream_key TEXT NOT NULL,
    entry_id TEXT NOT NULL,
    body_sha256 BYTEA NOT NULL CHECK (octet_length(body_sha256) = 32),
    body BYTEA NOT NULL CHECK (octet_length(body) <= 33554432),
    elapsed_ns BIGINT NOT NULL CHECK (elapsed_ns >= 0),
    disposition TEXT NOT NULL CHECK (disposition IN ('accepted','replayed','foreign','rejected','source_conflict')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (batch_id, stream_key, entry_id, body_sha256)
);
CREATE INDEX futures_query_observations_batch_idx ON futures_query_observations(batch_id, observation_id);

-- Complete, bounded typed candidates only; money remains decimal text in JSON.
-- BYTEA retains exact serialization for integrity verification, not binary floats.
CREATE TABLE futures_query_snapshots (
    batch_id TEXT PRIMARY KEY REFERENCES futures_query_batches(batch_id),
    candidate BYTEA NOT NULL CHECK (octet_length(candidate) <= 9437184),
    candidate_sha256 BYTEA NOT NULL CHECK (octet_length(candidate_sha256) = 32),
    stored_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
