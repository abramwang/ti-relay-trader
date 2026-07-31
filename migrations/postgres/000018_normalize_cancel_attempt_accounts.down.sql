BEGIN;

UPDATE order_cancel_attempts AS attempt
SET
    account_id = attempt.raw_payload->>'account_id',
    updated_at = now()
WHERE COALESCE(attempt.raw_payload->>'account_id', '') <> ''
  AND attempt.account_id <> attempt.raw_payload->>'account_id'
  AND EXISTS (
      SELECT 1
      FROM accounts AS account
      WHERE account.account_id = attempt.raw_payload->>'account_id'
  );

COMMIT;
