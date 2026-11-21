-- Stop trade writers before applying. Existing days are verified once by the
-- new writer before checkpoint activation; original evidence is not rewritten.
ALTER TABLE futures_trade_scopes
    ADD COLUMN projection_version INTEGER NOT NULL DEFAULT 0 CHECK(projection_version IN (0,1)),
    ADD COLUMN projection_state BYTEA,
    ADD COLUMN projection_sha256 BYTEA CHECK(octet_length(projection_sha256)=32),
    ADD COLUMN projection_dirty BOOLEAN NOT NULL DEFAULT false;

-- Normal writes clear dirty only after committing a matching report/checkpoint.
-- Unexpected edits/deletes/inserts remain detectable without rereading all bodies.
CREATE FUNCTION futures_trade_mark_dirty() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE futures_trade_scopes SET projection_dirty=true WHERE NOT projection_dirty
            AND scope_key IN (SELECT scope_key FROM new_rows);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE futures_trade_scopes SET projection_dirty=true WHERE NOT projection_dirty
            AND scope_key IN (SELECT scope_key FROM old_rows);
    ELSE
        UPDATE futures_trade_scopes SET projection_dirty=true WHERE NOT projection_dirty
            AND scope_key IN (SELECT scope_key FROM new_rows UNION SELECT scope_key FROM old_rows);
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER futures_trade_evidence_insert AFTER INSERT ON futures_trade_observations
    REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_evidence_update AFTER UPDATE ON futures_trade_observations
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_evidence_delete AFTER DELETE ON futures_trade_observations
    REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_orders_insert AFTER INSERT ON futures_order_states
    REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_orders_update AFTER UPDATE ON futures_order_states
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_orders_delete AFTER DELETE ON futures_order_states
    REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_fills_insert AFTER INSERT ON futures_execution_facts
    REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_fills_update AFTER UPDATE ON futures_execution_facts
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();
CREATE TRIGGER futures_trade_fills_delete AFTER DELETE ON futures_execution_facts
    REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_mark_dirty();

CREATE FUNCTION futures_trade_truncate_dirty() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE futures_trade_scopes SET projection_dirty=true;
    RETURN NULL;
END $$;
CREATE TRIGGER futures_trade_evidence_truncated AFTER TRUNCATE ON futures_trade_observations
    FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_truncate_dirty();
CREATE TRIGGER futures_trade_orders_truncated AFTER TRUNCATE ON futures_order_states
    FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_truncate_dirty();
CREATE TRIGGER futures_trade_fills_truncated AFTER TRUNCATE ON futures_execution_facts
    FOR EACH STATEMENT EXECUTE FUNCTION futures_trade_truncate_dirty();
