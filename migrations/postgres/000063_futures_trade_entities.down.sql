LOCK TABLE futures_trade_scopes,futures_order_states,futures_execution_facts,futures_trade_aliases IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_trade_scopes WHERE projection_version=2 OR projection_dirty)
        OR EXISTS(SELECT 1 FROM futures_trade_aliases) THEN
        RAISE EXCEPTION 'entity projections require an explicitly verified legacy rebuild before downgrade';
    END IF;
END $$;
DROP TABLE futures_trade_aliases;
ALTER TABLE futures_trade_scopes DROP COLUMN summary,DROP COLUMN summary_sha256;
DROP INDEX futures_orders_alias_idx;
DROP INDEX futures_fills_alias_idx;
ALTER TABLE futures_order_states DROP COLUMN native_alias,DROP COLUMN record_sha256;
ALTER TABLE futures_execution_facts DROP COLUMN native_alias,DROP COLUMN record_sha256,DROP COLUMN eligible_gateway;
ALTER TABLE futures_trade_scopes DROP CONSTRAINT futures_trade_scopes_projection_version_check;
ALTER TABLE futures_trade_scopes ADD CONSTRAINT futures_trade_scopes_projection_version_check CHECK(projection_version IN (0,1));
