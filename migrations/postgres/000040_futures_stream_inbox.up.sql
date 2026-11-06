-- XREAD responsibility handoff, independent of broker TradingDay and OC XACK.
CREATE TABLE futures_stream_receivers (
    stream_key TEXT PRIMARY KEY,
    config_json JSONB NOT NULL,
    owner_id TEXT NOT NULL,
    cursor_id TEXT NOT NULL DEFAULT '0-0',
    audit_count INTEGER NOT NULL DEFAULT 0 CHECK(audit_count BETWEEN 0 AND 50000),
    audit_bytes BIGINT NOT NULL DEFAULT 0 CHECK(audit_bytes BETWEEN 0 AND 67108864)
);
CREATE TABLE futures_stream_inbox (
    receipt_id BIGSERIAL PRIMARY KEY,
    stream_key TEXT NOT NULL REFERENCES futures_stream_receivers(stream_key),
    entry_id TEXT NOT NULL,
    body BYTEA NOT NULL CHECK(octet_length(body)<=8388608),
    body_sha256 BYTEA NOT NULL CHECK(octet_length(body_sha256)=32),
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','quarantined','delivered')),
    reason TEXT NOT NULL DEFAULT '',
    event_id BIGINT REFERENCES futures_event_inbox(event_id),
    batch_id TEXT REFERENCES futures_query_batches(batch_id),
    received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    delivered_at TIMESTAMPTZ,
    UNIQUE(stream_key,entry_id),
    CHECK ((state='delivered' AND num_nonnulls(event_id,batch_id)=1 AND delivered_at IS NOT NULL)
        OR (state<>'delivered' AND num_nonnulls(event_id,batch_id)=0 AND delivered_at IS NULL))
);
CREATE INDEX futures_stream_inbox_pending ON futures_stream_inbox(stream_key,state,receipt_id);
