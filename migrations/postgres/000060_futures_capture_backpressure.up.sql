-- Writers must be stopped for upgrade; a paused lane never renews readiness.
ALTER TABLE futures_capture_health
    ADD COLUMN reply_backpressured BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN event_backpressured BOOLEAN NOT NULL DEFAULT false,
    DROP CONSTRAINT futures_capture_health_state_check,
    ADD CHECK(state IN ('starting','running','backpressured','stopped'));
