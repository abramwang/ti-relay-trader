-- Correlated OC responses, separate from independently reviewed SDK evidence
-- and economic order/fill accounting. No trading authority is stored here.
CREATE TABLE futures_reply_results (
    command_id BIGINT PRIMARY KEY REFERENCES futures_commands(command_id),
    target_json JSONB NOT NULL,
    result BYTEA NOT NULL CHECK(octet_length(result)<=8388608),
    result_sha256 BYTEA NOT NULL CHECK(octet_length(result_sha256)=32),
    input_count INTEGER NOT NULL CHECK(input_count BETWEEN 1 AND 256),
    input_bytes BIGINT NOT NULL CHECK(input_bytes BETWEEN 1 AND 8388608),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE futures_stream_inbox ADD COLUMN reply_command_id BIGINT REFERENCES futures_reply_results(command_id);
ALTER TABLE futures_stream_inbox DROP CONSTRAINT futures_stream_inbox_check;
ALTER TABLE futures_stream_inbox ADD CONSTRAINT futures_stream_inbox_check CHECK (
    (state='delivered' AND num_nonnulls(event_id,batch_id,reply_command_id)=1 AND delivered_at IS NOT NULL)
    OR (state<>'delivered' AND num_nonnulls(event_id,batch_id,reply_command_id)=0 AND delivered_at IS NULL)
);
CREATE INDEX futures_stream_inbox_reply_command ON futures_stream_inbox(reply_command_id,receipt_id) WHERE reply_command_id IS NOT NULL;
