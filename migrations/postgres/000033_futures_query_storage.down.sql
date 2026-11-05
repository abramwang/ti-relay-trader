DROP TABLE futures_query_snapshots;
DROP TABLE futures_query_observations;
ALTER TABLE futures_query_heads DROP CONSTRAINT futures_current_scope_fk;
ALTER TABLE futures_query_heads DROP CONSTRAINT futures_target_scope_fk;
DROP TABLE futures_query_batches;
DROP TABLE futures_query_heads;
