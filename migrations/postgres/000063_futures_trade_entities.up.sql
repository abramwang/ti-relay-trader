-- Coordinate reader/writer upgrades. Legacy days activate on their next verified
-- write; old readers then fail instead of seeing a stale or partial report.
ALTER TABLE futures_trade_scopes DROP CONSTRAINT futures_trade_scopes_projection_version_check;
ALTER TABLE futures_trade_scopes ADD CONSTRAINT futures_trade_scopes_projection_version_check CHECK(projection_version IN (0,1,2));
ALTER TABLE futures_trade_scopes ADD COLUMN summary BYTEA,
    ADD COLUMN summary_sha256 BYTEA CHECK(octet_length(summary_sha256)=32);
ALTER TABLE futures_order_states ADD COLUMN native_alias TEXT NOT NULL DEFAULT '',
    ADD COLUMN record_sha256 BYTEA CHECK(octet_length(record_sha256)=32);
ALTER TABLE futures_execution_facts ADD COLUMN native_alias TEXT NOT NULL DEFAULT '',
    ADD COLUMN record_sha256 BYTEA CHECK(octet_length(record_sha256)=32),
    ADD COLUMN eligible_gateway TEXT NOT NULL DEFAULT '';
CREATE INDEX futures_orders_alias_idx ON futures_order_states(scope_key,native_alias);
CREATE INDEX futures_fills_alias_idx ON futures_execution_facts(scope_key,native_alias);
CREATE TABLE futures_trade_aliases (
    scope_key TEXT NOT NULL REFERENCES futures_trade_scopes(scope_key),
    native_alias TEXT NOT NULL,
    record JSONB NOT NULL,
    record_sha256 BYTEA NOT NULL CHECK(octet_length(record_sha256)=32),
    PRIMARY KEY(scope_key,native_alias)
);
CREATE TRIGGER futures_trade_aliases_insert AFTER INSERT ON futures_trade_aliases
    REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_aliases_update AFTER UPDATE ON futures_trade_aliases
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_aliases_delete AFTER DELETE ON futures_trade_aliases
    REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_aliases_truncated AFTER TRUNCATE ON futures_trade_aliases
    FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_truncate_dirty();
