DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_close_reservations) OR EXISTS(SELECT 1 FROM futures_close_buckets)
        OR EXISTS(SELECT 1 FROM futures_close_baselines) THEN
        RAISE EXCEPTION 'close quantity responsibility exists; cannot discard reservations';
    END IF;
END $$;
DROP TABLE futures_close_baselines;
DROP TABLE futures_close_reservations;
DROP TABLE futures_close_buckets;
