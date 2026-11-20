-- Local CFFEX quantity responsibility, not a trading permission or broker freeze.
-- A bucket pins one baseline. Replacement requires explicit coverage reconciliation.
CREATE TABLE futures_close_buckets (
    bucket_key TEXT PRIMARY KEY,
    route_key TEXT NOT NULL REFERENCES futures_command_routes(route_key),
    scope_json JSONB NOT NULL,
    target_json JSONB NOT NULL,
    position_command_id BIGINT NOT NULL REFERENCES futures_reply_results(command_id),
    baseline_sha256 TEXT NOT NULL CHECK(length(baseline_sha256)=64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE futures_close_reservations (
    command_id BIGINT PRIMARY KEY REFERENCES futures_commands(command_id),
    bucket_key TEXT NOT NULL REFERENCES futures_close_buckets(bucket_key),
    evidence BYTEA NOT NULL CHECK(octet_length(evidence)<=65536),
    evidence_sha256 TEXT NOT NULL CHECK(length(evidence_sha256)=64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX futures_close_reservations_bucket ON futures_close_reservations(bucket_key,command_id);

-- Append-only coverage evidence; the bucket points at the current version.
-- Reservations continue to reference their original position query.
CREATE TABLE futures_close_baselines (
    bucket_key TEXT NOT NULL REFERENCES futures_close_buckets(bucket_key),
    position_command_id BIGINT NOT NULL REFERENCES futures_reply_results(command_id),
    evidence BYTEA NOT NULL CHECK(octet_length(evidence)<=262144),
    evidence_sha256 TEXT NOT NULL CHECK(length(evidence_sha256)=64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(bucket_key,position_command_id)
);
