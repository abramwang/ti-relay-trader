UPDATE orders
SET origin_message_id = CASE
        WHEN adapter_context #> '{relay_submission_identity_backfill,previous_origin_message_id}' = 'null'::jsonb
            THEN NULL
        ELSE adapter_context #>> '{relay_submission_identity_backfill,previous_origin_message_id}'
    END,
    request_id = CASE
        WHEN adapter_context #> '{relay_submission_identity_backfill,previous_request_id}' = 'null'::jsonb
            THEN NULL
        ELSE adapter_context #>> '{relay_submission_identity_backfill,previous_request_id}'
    END,
    idempotency_key = CASE
        WHEN adapter_context #> '{relay_submission_identity_backfill,previous_idempotency_key}' = 'null'::jsonb
            THEN NULL
        ELSE adapter_context #>> '{relay_submission_identity_backfill,previous_idempotency_key}'
    END,
    adapter_context = adapter_context - 'relay_submission_identity_backfill',
    updated_at = now()
WHERE adapter_context #>> '{relay_submission_identity_backfill,migration}' = '000027';
