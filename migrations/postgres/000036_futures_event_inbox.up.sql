-- Local CTP evidence only. Registration never sends commands or enables trading.
CREATE TABLE futures_event_scopes (
    scope_key TEXT PRIMARY KEY,
    context_json JSONB NOT NULL,
    event_stream TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0 CHECK(event_count BETWEEN 0 AND 1024),
    event_bytes BIGINT NOT NULL DEFAULT 0 CHECK(event_bytes BETWEEN 0 AND 33554432),
    command_count INTEGER NOT NULL DEFAULT 0 CHECK(command_count BETWEEN 0 AND 1024),
    command_bytes BIGINT NOT NULL DEFAULT 0 CHECK(command_bytes BETWEEN 0 AND 33554432),
    intent_count INTEGER NOT NULL DEFAULT 0 CHECK(intent_count BETWEEN 0 AND 1024)
);
CREATE TABLE futures_submit_commands (
    scope_key TEXT NOT NULL REFERENCES futures_event_scopes(scope_key),
    message_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    body BYTEA NOT NULL CHECK(octet_length(body)<=8388608),
    body_sha256 BYTEA NOT NULL CHECK(octet_length(body_sha256)=32),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(scope_key,message_id),
    UNIQUE(scope_key,idempotency_key)
);
CREATE TABLE futures_submit_intents (
    scope_key TEXT NOT NULL,
    gateway_order_id TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    PRIMARY KEY(scope_key,gateway_order_id),
    UNIQUE(scope_key,client_order_id),
    FOREIGN KEY(scope_key,message_id) REFERENCES futures_submit_commands(scope_key,message_id)
);
CREATE TABLE futures_event_inbox (
    event_id BIGSERIAL PRIMARY KEY,
    scope_key TEXT NOT NULL REFERENCES futures_event_scopes(scope_key),
    entry_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    body BYTEA NOT NULL CHECK(octet_length(body)<=8388608),
    body_sha256 BYTEA NOT NULL CHECK(octet_length(body_sha256)=32),
    state TEXT NOT NULL CHECK(state IN ('pending','waiting_identity','applied','rejected','conflict')),
    reason TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    processed_at TIMESTAMPTZ,
    UNIQUE(scope_key,entry_id,body_sha256)
);
CREATE INDEX futures_event_inbox_message ON futures_event_inbox(scope_key,message_id);
CREATE INDEX futures_event_inbox_work ON futures_event_inbox(scope_key,state,event_id);
