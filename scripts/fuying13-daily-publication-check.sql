BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT jsonb_build_object(
  'database', current_database(),
  'read_only', current_setting('transaction_read_only'),
  'orders', (SELECT count(*) FROM orders),
  'fills', (SELECT count(*) FROM fills),
  'nav_versions', (SELECT count(*) FROM performance_nav_versions),
  'nav_content_md5', (SELECT md5(string_agg(row_to_json(n)::text, '' ORDER BY performance_nav_pk)) FROM performance_nav_versions n),
  'previous_cost_md5', (SELECT md5(string_agg(row_to_json(c)::text, '' ORDER BY symbol, exchange, cost_bucket)) FROM performance_position_cost_states c WHERE account_id='314000045768' AND trade_date='2026-11-12'),
  'target_cost_rows', (SELECT count(*) FROM performance_position_cost_states WHERE account_id='314000045768' AND trade_date='2026-11-13'),
  'target_cost_origins', (SELECT jsonb_agg(DISTINCT opening_source) FROM performance_position_cost_states WHERE account_id='314000045768' AND trade_date='2026-11-13'),
  'review_versions', (SELECT jsonb_agg(jsonb_build_object('version_id', version_id, 'sha256', payload_sha256) ORDER BY version_id) FROM performance_cycle_reviews WHERE account_id='314000045768'),
  'schema_versions', (SELECT jsonb_agg(version ORDER BY version) FROM relay_schema_migrations)
);
COMMIT;
