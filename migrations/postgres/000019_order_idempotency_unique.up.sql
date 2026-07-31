UPDATE orders
SET idempotency_key = NULL
WHERE idempotency_key IS NOT NULL
  AND btrim(idempotency_key) = '';

UPDATE orders
SET adapter_context = adapter_context || jsonb_build_object(
        'relay_idempotency_cleanup',
        jsonb_build_object(
            'migration', '000019',
            'reason', 'query_key_is_not_order_idempotency',
            'previous_key', idempotency_key
        )
    ),
    idempotency_key = NULL
WHERE idempotency_key LIKE 'orders:query:%';

WITH ranked_duplicates AS (
    SELECT
        order_pk,
        idempotency_key,
        row_number() OVER (
            PARTITION BY account_id, idempotency_key
            ORDER BY created_at, order_pk
        ) AS duplicate_rank
    FROM orders
    WHERE idempotency_key IS NOT NULL
)
UPDATE orders
SET adapter_context = orders.adapter_context || jsonb_build_object(
        'relay_idempotency_cleanup',
        jsonb_build_object(
            'migration', '000019',
            'reason', 'historical_duplicate_key',
            'previous_key', ranked_duplicates.idempotency_key
        )
    ),
    idempotency_key = NULL
FROM ranked_duplicates
WHERE orders.order_pk = ranked_duplicates.order_pk
  AND ranked_duplicates.duplicate_rank > 1;

DROP INDEX IF EXISTS orders_idempotency_idx;

CREATE UNIQUE INDEX orders_idempotency_unique
    ON orders(account_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
