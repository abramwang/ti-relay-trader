-- Full reports and original rows remain compatible with the old replay writer.
LOCK TABLE futures_trade_scopes,futures_trade_observations,futures_order_states,futures_execution_facts IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM futures_trade_scopes WHERE projection_dirty OR
        (projection_version=1 AND (projection_state IS NULL OR projection_sha256 IS NULL
        OR sha256(projection_state)<>projection_sha256 OR report IS NULL OR report_sha256 IS NULL
        OR sha256(report)<>report_sha256))) THEN
        RAISE EXCEPTION 'trade checkpoint invalid; downgrade must not hide damaged state';
    END IF;
    IF EXISTS(SELECT 1 FROM futures_trade_scopes s LEFT JOIN (
        SELECT scope_key,count(*) AS records,sum(octet_length(body)) AS bytes
        FROM futures_trade_observations GROUP BY scope_key) a USING(scope_key)
        WHERE s.audit_count<>COALESCE(a.records,0) OR s.audit_bytes<>COALESCE(a.bytes,0))
        OR EXISTS(SELECT 1 FROM futures_trade_observations WHERE sha256(body)<>body_sha256) THEN
        RAISE EXCEPTION 'trade evidence invalid; refusing checkpoint downgrade';
    END IF;
END $$;
DROP TRIGGER futures_trade_evidence_insert ON futures_trade_observations;
DROP TRIGGER futures_trade_evidence_update ON futures_trade_observations;
DROP TRIGGER futures_trade_evidence_delete ON futures_trade_observations;
DROP TRIGGER futures_trade_orders_insert ON futures_order_states;
DROP TRIGGER futures_trade_orders_update ON futures_order_states;
DROP TRIGGER futures_trade_orders_delete ON futures_order_states;
DROP TRIGGER futures_trade_fills_insert ON futures_execution_facts;
DROP TRIGGER futures_trade_fills_update ON futures_execution_facts;
DROP TRIGGER futures_trade_fills_delete ON futures_execution_facts;
DROP FUNCTION futures_trade_mark_dirty();
DROP TRIGGER futures_trade_evidence_truncated ON futures_trade_observations;
DROP TRIGGER futures_trade_orders_truncated ON futures_order_states;
DROP TRIGGER futures_trade_fills_truncated ON futures_execution_facts;
DROP FUNCTION futures_trade_truncate_dirty();
ALTER TABLE futures_trade_scopes DROP COLUMN projection_version,DROP COLUMN projection_state,
    DROP COLUMN projection_sha256,DROP COLUMN projection_dirty;
