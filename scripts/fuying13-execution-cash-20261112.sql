-- Use a read-only repeatable-read transaction. No order, fee or cash changes.
WITH scoped AS (
  SELECT * FROM fills
  WHERE account_id='314000045768'
    AND trade_date BETWEEN '2026-10-12' AND '2026-11-09'
    AND NOT (symbol='204001' AND exchange='SH')
), grouped AS (
  SELECT account_id, trade_date, gateway_order_id, symbol, exchange,
         trade_side, business_type,
         sum(qty) AS qty, sum(price*qty)::text AS gross_amount,
         jsonb_agg(jsonb_build_object('fill_pk',fill_pk,'fill_id',fill_id,
                   'qty',qty,'price',price::text) ORDER BY fill_pk) AS fills
  FROM scoped
  GROUP BY account_id, trade_date, gateway_order_id, symbol, exchange,
           trade_side, business_type
)
SELECT current_database() AS database_name,
       current_setting('transaction_read_only') AS transaction_read_only,
       '314000045768' AS account_id, now() AS captured_at,
       (SELECT count(*) FROM scoped) AS fill_count,
       count(*) AS order_group_count,
       coalesce(jsonb_agg(jsonb_build_object(
           'account_id',g.account_id,'trade_date',g.trade_date::text,
           'gateway_order_id',g.gateway_order_id,'symbol',g.symbol,'exchange',g.exchange,
           'trade_side',g.trade_side,'business_type',g.business_type,
           'quantity',g.qty,'gross_amount',g.gross_amount,'fills',g.fills,
           'fee_records',(SELECT coalesce(jsonb_agg(jsonb_build_object(
               'fee_record_id',f.fee_record_id,'record_scope',f.record_scope,
               'fee_as_of',f.fee_as_of,'total_fee',f.total_fee::text,
               'fee_complete',f.fee_complete,'association_complete',f.association_complete,
               'fee_source',f.fee_source,'currency',f.currency
           ) ORDER BY f.fee_as_of DESC,f.fee_record_id), '[]'::jsonb)
           FROM order_fee_records f
           WHERE f.account_id=g.account_id AND f.trade_date=g.trade_date
             AND f.gateway_order_id=g.gateway_order_id)
       ) ORDER BY g.trade_date,g.gateway_order_id),'[]'::jsonb) AS orders
FROM grouped g;
