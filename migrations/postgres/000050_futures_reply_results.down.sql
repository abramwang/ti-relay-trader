-- Never silently erase the owner of already handed-off replies.
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_reply_results) THEN
        RAISE EXCEPTION 'reply results exist; cannot discard response responsibility';
    END IF;
END $$;
ALTER TABLE futures_stream_inbox DROP CONSTRAINT futures_stream_inbox_check;
ALTER TABLE futures_stream_inbox DROP COLUMN reply_command_id;
ALTER TABLE futures_stream_inbox ADD CONSTRAINT futures_stream_inbox_check CHECK (
    (state='delivered' AND num_nonnulls(event_id,batch_id)=1 AND delivered_at IS NOT NULL)
    OR (state<>'delivered' AND num_nonnulls(event_id,batch_id)=0 AND delivered_at IS NULL)
);
DROP TABLE futures_reply_results;
