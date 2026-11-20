LOCK TABLE futures_capture_health IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_capture_health WHERE state<>'stopped') THEN
        RAISE EXCEPTION 'stop capture writers before downgrading backpressure health';
    END IF;
END $$;
ALTER TABLE futures_capture_health
    DROP CONSTRAINT futures_capture_health_state_check,
    ADD CHECK(state IN ('starting','running','stopped')),
    DROP COLUMN reply_backpressured,
    DROP COLUMN event_backpressured;
