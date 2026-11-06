-- Isolated CTP command intentions; not a publisher, receipt or ACK ledger.
CREATE TABLE futures_command_routes (
    route_key TEXT PRIMARY KEY,
    audit_count INTEGER NOT NULL DEFAULT 0 CHECK(audit_count BETWEEN 0 AND 4096),
    audit_bytes BIGINT NOT NULL DEFAULT 0 CHECK(audit_bytes BETWEEN 0 AND 67108864)
);
CREATE TABLE futures_commands (
    command_id BIGSERIAL PRIMARY KEY,
    route_key TEXT NOT NULL REFERENCES futures_command_routes(route_key),
    idempotency_key TEXT NOT NULL,
    scope_json JSONB NOT NULL,
    stream_suffix TEXT NOT NULL CHECK(stream_suffix IN ('cmd.trade','cmd.query')),
    body BYTEA NOT NULL CHECK(octet_length(body)<=65536),
    body_sha256 TEXT NOT NULL,
    intent BYTEA NOT NULL,
    intent_sha256 TEXT NOT NULL,
    fields_json JSONB NOT NULL,
    evidence_json JSONB NOT NULL CHECK(octet_length(evidence_json::text)<=131072),
    evidence_sha256 TEXT NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(route_key,idempotency_key),
    UNIQUE(route_key,command_id)
);
CREATE TABLE futures_command_messages (
    route_key TEXT NOT NULL,
    message_id TEXT NOT NULL,
    command_id BIGINT NOT NULL,
    body BYTEA NOT NULL CHECK(octet_length(body)<=65536),
    body_sha256 TEXT NOT NULL,
    PRIMARY KEY(route_key,message_id),
    FOREIGN KEY(route_key,command_id) REFERENCES futures_commands(route_key,command_id)
);
CREATE TABLE futures_command_reservations (
    route_key TEXT NOT NULL,
    trade_date TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('gateway','client','cancel')),
    identity TEXT NOT NULL,
    command_id BIGINT NOT NULL,
    PRIMARY KEY(route_key,trade_date,kind,identity),
    FOREIGN KEY(route_key,command_id) REFERENCES futures_commands(route_key,command_id)
);
CREATE TABLE futures_command_audit (
    audit_id BIGSERIAL PRIMARY KEY,
    route_key TEXT NOT NULL REFERENCES futures_command_routes(route_key),
    message_id TEXT NOT NULL,
    body BYTEA NOT NULL CHECK(octet_length(body)<=65536),
    body_sha256 TEXT NOT NULL,
    intent BYTEA NOT NULL,
    intent_sha256 TEXT NOT NULL,
    evidence BYTEA NOT NULL CHECK(octet_length(evidence)<=65536),
    evidence_sha256 TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('registered','replayed','conflict','rejected')),
    reason TEXT NOT NULL,
    command_id BIGINT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(route_key,command_id) REFERENCES futures_commands(route_key,command_id),
    UNIQUE(route_key,message_id,body_sha256,intent_sha256,evidence_sha256,outcome,reason)
);
