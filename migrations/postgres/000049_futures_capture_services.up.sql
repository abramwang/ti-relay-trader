-- Durable receiver ownership, not trading permission or broker readiness.
CREATE TABLE futures_capture_services (
    route_key TEXT PRIMARY KEY,
    service_id TEXT NOT NULL UNIQUE,
    binding_json JSONB NOT NULL,
    reply_owner TEXT NOT NULL,
    event_owner TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
