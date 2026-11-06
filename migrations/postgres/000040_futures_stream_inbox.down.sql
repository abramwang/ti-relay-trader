-- Prototype rollback is explicit; never automatically invoked by a consumer.
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_stream_inbox) THEN
        RAISE EXCEPTION 'stream inbox contains audit evidence; refuse destructive downgrade';
    END IF;
END $$;
DROP TABLE futures_stream_inbox;
DROP TABLE futures_stream_receivers;
