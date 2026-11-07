CREATE TABLE futures_heartbeat_windows (
    window_id TEXT PRIMARY KEY,
    stream_key TEXT NOT NULL REFERENCES futures_stream_receivers(stream_key),
    receiver_owner TEXT NOT NULL,
    context_json JSONB NOT NULL,
    review_id TEXT NOT NULL,
    boundary_cursor TEXT NOT NULL,
    boundary_receipt BIGINT NOT NULL CHECK(boundary_receipt >= 0),
    after_receipt BIGINT NOT NULL CHECK(after_receipt >= boundary_receipt),
    started_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('observing','stopped')),
    reason TEXT NOT NULL DEFAULT '',
    stopped_at TIMESTAMPTZ,
    CHECK ((state='observing' AND stopped_at IS NULL AND reason='') OR
           (state='stopped' AND stopped_at IS NOT NULL AND reason<>''))
);
CREATE UNIQUE INDEX futures_heartbeat_one_window ON futures_heartbeat_windows(stream_key) WHERE state='observing';
CREATE TABLE futures_heartbeat_observations (
    window_id TEXT NOT NULL REFERENCES futures_heartbeat_windows(window_id),
    receipt_id BIGINT NOT NULL REFERENCES futures_stream_inbox(receipt_id),
    outcome TEXT NOT NULL CHECK(outcome IN ('accepted','rejected')),
    reason TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(window_id,receipt_id),
    CHECK ((outcome='accepted' AND reason='') OR (outcome='rejected' AND reason<>''))
);
