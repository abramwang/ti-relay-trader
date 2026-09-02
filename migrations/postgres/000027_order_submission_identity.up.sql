WITH archived_submit_children AS (
    SELECT
        raw.raw_message_pk,
        raw.account_id,
        (raw.received_at AT TIME ZONE 'Asia/Shanghai')::date AS trade_date,
        raw.action,
        NULLIF(raw.body->>'message_id', '') AS submit_message_id,
        NULLIF(raw.body->>'request_id', '') AS submit_request_id,
        NULLIF(child.value->>'gateway_order_id', '') AS gateway_order_id,
        NULLIF(child.value->>'idempotency_key', '') AS child_idempotency_key
    FROM raw_stream_messages AS raw
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE
            WHEN jsonb_typeof(raw.body #> '{payload,orders}') = 'array'
                THEN raw.body #> '{payload,orders}'
            ELSE '[]'::jsonb
        END
    ) AS child(value)
    WHERE raw.stream_role = 'cmd.trade'
        AND raw.action = 'order.batch.submit'

    UNION ALL

    SELECT
        raw.raw_message_pk,
        raw.account_id,
        (raw.received_at AT TIME ZONE 'Asia/Shanghai')::date AS trade_date,
        raw.action,
        NULLIF(raw.body->>'message_id', '') AS submit_message_id,
        NULLIF(raw.body->>'request_id', '') AS submit_request_id,
        NULLIF(raw.body #>> '{payload,gateway_order_id}', '') AS gateway_order_id,
        NULLIF(raw.body #>> '{payload,idempotency_key}', '') AS child_idempotency_key
    FROM raw_stream_messages AS raw
    WHERE raw.stream_role = 'cmd.trade'
        AND raw.action = 'order.submit'
), ranked_submit_children AS (
    SELECT
        archived_submit_children.*,
        row_number() OVER (
            PARTITION BY account_id, trade_date, gateway_order_id
            ORDER BY raw_message_pk
        ) AS submit_rank
    FROM archived_submit_children
    WHERE account_id IS NOT NULL
        AND account_id <> ''
        AND gateway_order_id IS NOT NULL
        AND submit_message_id IS NOT NULL
), archived AS (
    SELECT *
    FROM ranked_submit_children
    WHERE submit_rank = 1
)
UPDATE orders
SET adapter_context = orders.adapter_context || jsonb_build_object(
        'relay_submission_identity_backfill',
        jsonb_build_object(
            'migration', '000027',
            'source_action', archived.action,
            'previous_origin_message_id', orders.origin_message_id,
            'previous_request_id', orders.request_id,
            'previous_idempotency_key', orders.idempotency_key,
            'idempotency_conflict_skipped', archived.child_idempotency_key IS NOT NULL
                AND EXISTS (
                    SELECT 1
                    FROM orders AS idempotency_owner
                    WHERE idempotency_owner.account_id = orders.account_id
                        AND idempotency_owner.idempotency_key = archived.child_idempotency_key
                        AND idempotency_owner.order_pk <> orders.order_pk
                )
        )
    ),
    origin_message_id = archived.submit_message_id,
    request_id = COALESCE(archived.submit_request_id, orders.request_id),
    idempotency_key = CASE
        WHEN archived.child_idempotency_key IS NULL THEN orders.idempotency_key
        WHEN EXISTS (
            SELECT 1
            FROM orders AS idempotency_owner
            WHERE idempotency_owner.account_id = orders.account_id
                AND idempotency_owner.idempotency_key = archived.child_idempotency_key
                AND idempotency_owner.order_pk <> orders.order_pk
        ) THEN orders.idempotency_key
        ELSE archived.child_idempotency_key
    END,
    updated_at = now()
FROM archived
WHERE orders.account_id = archived.account_id
    AND orders.trade_date = archived.trade_date
    AND orders.gateway_order_id = archived.gateway_order_id
    AND (
        orders.origin_message_id IS DISTINCT FROM archived.submit_message_id
        OR (
            archived.submit_request_id IS NOT NULL
            AND orders.request_id IS DISTINCT FROM archived.submit_request_id
        )
        OR (
            archived.child_idempotency_key IS NOT NULL
            AND orders.idempotency_key IS DISTINCT FROM archived.child_idempotency_key
        )
    );
