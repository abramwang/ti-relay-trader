DROP INDEX IF EXISTS orders_idempotency_unique;

UPDATE orders
SET idempotency_key = adapter_context #>> '{relay_idempotency_cleanup,previous_key}',
    adapter_context = adapter_context - 'relay_idempotency_cleanup'
WHERE adapter_context #>> '{relay_idempotency_cleanup,migration}' = '000019'
  AND NULLIF(btrim(adapter_context #>> '{relay_idempotency_cleanup,previous_key}'), '') IS NOT NULL;

CREATE INDEX orders_idempotency_idx
    ON orders(account_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
