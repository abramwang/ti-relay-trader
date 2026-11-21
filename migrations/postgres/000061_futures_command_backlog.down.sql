LOCK TABLE futures_command_routes, futures_commands, futures_command_audit, futures_command_work IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM futures_command_routes r LEFT JOIN (
            SELECT route_key,count(*) AS records,
                sum(octet_length(body)::bigint+octet_length(intent)+octet_length(evidence)) AS bytes
            FROM futures_command_audit GROUP BY route_key
        ) a USING(route_key)
        WHERE r.audit_count<>COALESCE(a.records,0) OR r.audit_bytes<>COALESCE(a.bytes,0)
    ) OR EXISTS (
        SELECT 1 FROM futures_commands c LEFT JOIN futures_command_work w USING(command_id)
        WHERE w.command_id IS NULL OR w.route_key<>c.route_key OR
            w.work_bytes<>octet_length(c.body)::bigint+octet_length(c.intent)+octet_length(c.evidence_json::text)
    ) THEN
        RAISE EXCEPTION 'command evidence or workload inconsistent; refusing downgrade';
    END IF;
    IF EXISTS(SELECT 1 FROM futures_command_routes WHERE audit_count>4096 OR audit_bytes>67108864) THEN
        RAISE EXCEPTION 'command history exceeds old limits; downgrade must not discard evidence';
    END IF;
END $$;
DROP TRIGGER futures_command_audit_usage ON futures_command_audit;
DROP FUNCTION futures_command_add_audit_usage();
DROP TRIGGER futures_command_work_insert ON futures_commands;
DROP FUNCTION futures_command_add_work();
DROP TABLE futures_command_work;
ALTER TABLE futures_command_routes
    DROP CONSTRAINT futures_command_routes_audit_count_check,
    DROP CONSTRAINT futures_command_routes_audit_bytes_check,
    ALTER COLUMN audit_count TYPE INTEGER,
    ADD CHECK(audit_count BETWEEN 0 AND 4096),
    ADD CHECK(audit_bytes BETWEEN 0 AND 67108864);
