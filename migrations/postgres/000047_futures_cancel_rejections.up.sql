-- A cancel attempt outcome is not an order terminal state or retry permission.
CREATE TABLE futures_cancel_rejections (
    command_id BIGINT PRIMARY KEY REFERENCES futures_commands(command_id),
    scope_key TEXT NOT NULL REFERENCES futures_event_scopes(scope_key),
    event_id BIGINT NOT NULL REFERENCES futures_event_inbox(event_id),
    body_sha256 BYTEA NOT NULL CHECK(octet_length(body_sha256)=32),
    state TEXT NOT NULL CHECK(state IN ('rejected','conflict')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
