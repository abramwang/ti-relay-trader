-- Explicit TEST assessment attempts; never a broker execution receipt.
CREATE TABLE futures_test_trade_publications (
    command_id BIGINT PRIMARY KEY REFERENCES futures_commands(command_id),
    assessment_key TEXT NOT NULL,
    action TEXT NOT NULL CHECK(action IN ('order.submit','order.cancel')),
    target_json JSONB NOT NULL,
    proof_json JSONB NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('attempted','published','not_published')),
    entry_id TEXT,
    reason TEXT NOT NULL DEFAULT '',
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ,
    UNIQUE(assessment_key,action),
    CHECK ((state='published' AND entry_id IS NOT NULL AND finished_at IS NOT NULL)
        OR (state='not_published' AND entry_id IS NULL AND finished_at IS NOT NULL)
        OR (state='attempted' AND entry_id IS NULL AND finished_at IS NULL))
);
