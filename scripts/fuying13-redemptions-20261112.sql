-- Execute inside a read-only repeatable-read transaction. No broker queries.
SELECT current_database() AS database_name,
       current_setting('transaction_read_only') AS transaction_read_only,
       '314000045768' AS account_id,
       '20260601' AS date_from, '20261109' AS date_to,
       now() AS captured_at,
       count(*) AS count,
       coalesce(jsonb_agg(jsonb_build_object(
           'account_id', account_id, 'trade_date', trade_date::text,
           'symbol', symbol, 'exchange', exchange,
           'gateway_order_id', gateway_order_id, 'order_stream_id', order_stream_id,
           'trade_side', trade_side, 'business_type', business_type,
           'cum_filled_qty', cum_filled_qty, 'is_terminal', is_terminal
       ) ORDER BY trade_date, gateway_order_id), '[]'::jsonb) AS orders
FROM orders
WHERE account_id='314000045768' AND trade_side='R' AND cum_filled_qty>0
  AND trade_date BETWEEN '2026-06-01' AND '2026-11-09';
