BEGIN;

DROP INDEX IF EXISTS raw_stream_messages_broker_not_ready_idx;
DROP INDEX IF EXISTS raw_stream_messages_dlq_operations_idx;

COMMIT;
