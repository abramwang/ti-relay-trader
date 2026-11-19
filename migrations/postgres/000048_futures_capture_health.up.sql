-- Raw receipt liveness only; never business projection or trading permission.
CREATE TABLE futures_capture_health (
    route_key TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    binding_json JSONB NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('starting','running','stopped')),
    reply_checked_at TIMESTAMPTZ,
    event_checked_at TIMESTAMPTZ,
    reply_owner TEXT NOT NULL DEFAULT '',
    event_owner TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
