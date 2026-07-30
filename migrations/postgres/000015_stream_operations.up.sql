BEGIN;

CREATE TABLE stream_dlq_reviews (
    review_id BIGSERIAL PRIMARY KEY,
    stream_key TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    status TEXT NOT NULL,
    operator TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT stream_dlq_reviews_message_fk
        FOREIGN KEY (stream_key, stream_id)
        REFERENCES raw_stream_messages(stream_key, stream_id)
        ON DELETE CASCADE,
    CONSTRAINT stream_dlq_reviews_status_check
        CHECK (status IN ('acknowledged', 'ignored', 'replayed'))
);

CREATE INDEX stream_dlq_reviews_message_idx
    ON stream_dlq_reviews(stream_key, stream_id, review_id DESC);

CREATE INDEX stream_dlq_reviews_status_idx
    ON stream_dlq_reviews(status, created_at DESC);

COMMIT;
