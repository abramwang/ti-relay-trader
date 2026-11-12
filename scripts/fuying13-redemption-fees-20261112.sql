-- Read-only evidence for the frozen 27 redemption parents, not a fee repair.
SELECT current_database() AS database_name,
       current_setting('transaction_read_only') AS transaction_read_only,
       '314000045768' AS account_id, now() AS captured_at,
       coalesce(jsonb_agg(jsonb_build_object(
           'account_id', o.account_id, 'trade_date', o.trade_date::text,
           'gateway_order_id', o.gateway_order_id,
           'security_id', o.symbol || '.' || o.exchange,
           'fee_record_id', f.fee_record_id,
           'fee_as_of', f.fee_as_of,
           'record_scope', f.record_scope,
           'total_fee', f.total_fee::text,
           'fee_complete', f.fee_complete,
           'association_complete', f.association_complete,
           'fee_source', f.fee_source
       ) ORDER BY o.trade_date, o.gateway_order_id, f.fee_as_of, f.fee_record_id), '[]'::jsonb) AS records,
       (SELECT coalesce(jsonb_agg(jsonb_build_object(
           'account_id', r.account_id, 'trade_date', r.trade_date::text,
           'gateway_order_id', r.gateway_order_id, 'order_stream_id', r.order_stream_id,
           'symbol', r.symbol, 'exchange', r.exchange, 'trade_side', r.trade_side,
           'business_type', r.business_type, 'cum_filled_qty', r.cum_filled_qty,
           'is_terminal', r.is_terminal
       ) ORDER BY r.trade_date, r.gateway_order_id), '[]'::jsonb)
        FROM orders r WHERE r.account_id='314000045768'
          AND r.symbol='204001' AND r.exchange='SH' AND r.cum_filled_qty>0
          AND r.trade_date BETWEEN '2026-09-01' AND '2026-11-09') AS repo_orders
FROM orders o LEFT JOIN order_fee_records f
  ON f.account_id=o.account_id AND f.trade_date=o.trade_date
 AND f.gateway_order_id=o.gateway_order_id
WHERE o.account_id='314000045768' AND o.trade_side='R'
  AND o.cum_filled_qty>0
  AND o.trade_date BETWEEN '2026-10-12' AND '2026-11-09';
