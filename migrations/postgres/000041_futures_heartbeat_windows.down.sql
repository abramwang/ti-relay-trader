DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_heartbeat_windows) THEN
        RAISE EXCEPTION 'heartbeat windows contain audit evidence; refuse destructive downgrade';
    END IF;
END $$;
DROP TABLE futures_heartbeat_observations;
DROP TABLE futures_heartbeat_windows;
