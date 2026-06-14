-- Persist Redis Stream consumer checkpoints for relay worker/docs-api sync loops.

CREATE TABLE stream_checkpoints (
    stream_key TEXT PRIMARY KEY,
    stream_role TEXT NOT NULL,
    last_stream_id TEXT NOT NULL DEFAULT '0',
    last_seen_at TIMESTAMPTZ,
    last_processed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    processed_count BIGINT NOT NULL DEFAULT 0 CHECK (processed_count >= 0),
    error_count BIGINT NOT NULL DEFAULT 0 CHECK (error_count >= 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT stream_checkpoints_role_check CHECK (stream_role IN ('reply', 'event', 'hb', 'dlq'))
);

CREATE INDEX stream_checkpoints_role_idx ON stream_checkpoints(stream_role, updated_at);
