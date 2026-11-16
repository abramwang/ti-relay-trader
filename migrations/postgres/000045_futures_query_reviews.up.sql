-- Independent, operator-reviewed OC request evidence; never learned from reply.
CREATE TABLE futures_test_reply_sources (
    stream_key TEXT PRIMARY KEY REFERENCES futures_stream_receivers(stream_key),
    transport_json JSONB NOT NULL
);
CREATE TABLE futures_test_query_reviews (
    command_id BIGINT PRIMARY KEY REFERENCES futures_test_query_publications(command_id),
    batch_id TEXT NOT NULL UNIQUE REFERENCES futures_query_batches(batch_id),
    manifest BYTEA NOT NULL,
    manifest_sha256 BYTEA NOT NULL CHECK (octet_length(manifest_sha256)=32),
    evidence BYTEA NOT NULL,
    evidence_sha256 BYTEA NOT NULL CHECK (octet_length(evidence_sha256)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
