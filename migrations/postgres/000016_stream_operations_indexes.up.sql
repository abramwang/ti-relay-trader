BEGIN;

CREATE INDEX raw_stream_messages_dlq_operations_idx
    ON raw_stream_messages(account_id, received_at DESC)
    WHERE stream_role = 'dlq';

CREATE INDEX raw_stream_messages_broker_not_ready_idx
    ON raw_stream_messages(account_id, received_at DESC)
    WHERE code = 'BROKER_NOT_READY';

COMMIT;
