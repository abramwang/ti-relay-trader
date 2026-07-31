BEGIN;

UPDATE order_cancel_attempts AS attempt
SET
    account_id = split_part(attempt.stream_key, ':', 5),
    updated_at = now()
WHERE attempt.stream_key LIKE 'relay:%:v1:%:%:event'
  AND split_part(attempt.stream_key, ':', 5) <> ''
  AND attempt.account_id <> split_part(attempt.stream_key, ':', 5)
  AND EXISTS (
      SELECT 1
      FROM accounts AS account
      WHERE account.account_id = split_part(attempt.stream_key, ':', 5)
  );

COMMIT;
