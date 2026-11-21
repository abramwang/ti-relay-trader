-- Stop command writers before upgrading. Historical audit is not a lifetime
-- quota; command work is released only by verified local result processing.
LOCK TABLE futures_command_routes, futures_commands, futures_command_audit IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM futures_command_routes r LEFT JOIN (
            SELECT route_key,count(*) AS records,
                sum(octet_length(body)::bigint+octet_length(intent)+octet_length(evidence)) AS bytes
            FROM futures_command_audit GROUP BY route_key
        ) a USING(route_key)
        WHERE r.audit_count<>COALESCE(a.records,0) OR r.audit_bytes<>COALESCE(a.bytes,0)
    ) THEN
        RAISE EXCEPTION 'command audit counters inconsistent; migration will not repair evidence';
    END IF;
END $$;
ALTER TABLE futures_command_routes
    DROP CONSTRAINT futures_command_routes_audit_count_check,
    DROP CONSTRAINT futures_command_routes_audit_bytes_check,
    ALTER COLUMN audit_count TYPE BIGINT,
    ADD CHECK(audit_count>=0), ADD CHECK(audit_bytes>=0);

CREATE TABLE futures_command_work (
    command_id BIGINT PRIMARY KEY,
    route_key TEXT NOT NULL,
    work_bytes BIGINT NOT NULL CHECK(work_bytes>0),
    pending BOOLEAN NOT NULL DEFAULT true,
    FOREIGN KEY(route_key,command_id) REFERENCES futures_commands(route_key,command_id)
);
CREATE INDEX futures_command_work_pending ON futures_command_work(route_key,command_id)
    INCLUDE(work_bytes) WHERE pending;
-- Existing commands start conservatively pending. The normal reply worker
-- revalidates retained results; SQL does not infer completion from a status name.
INSERT INTO futures_command_work(command_id,route_key,work_bytes)
    SELECT command_id,route_key,octet_length(body)::bigint+octet_length(intent)+octet_length(evidence_json::text)
    FROM futures_commands;

CREATE FUNCTION futures_command_add_work() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO futures_command_work(command_id,route_key,work_bytes) VALUES
        (NEW.command_id,NEW.route_key,octet_length(NEW.body)::bigint+octet_length(NEW.intent)+octet_length(NEW.evidence_json::text));
    RETURN NULL;
END $$;
CREATE TRIGGER futures_command_work_insert AFTER INSERT ON futures_commands
    FOR EACH ROW EXECUTE FUNCTION futures_command_add_work();

CREATE FUNCTION futures_command_add_audit_usage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE futures_command_routes SET audit_count=audit_count+1,
        audit_bytes=audit_bytes+octet_length(NEW.body)::bigint+octet_length(NEW.intent)+octet_length(NEW.evidence)
        WHERE route_key=NEW.route_key;
    IF NOT FOUND THEN RAISE EXCEPTION 'missing command route'; END IF;
    RETURN NULL;
END $$;
-- Never normalize counters to deleted or rewritten evidence.
CREATE TRIGGER futures_command_audit_usage AFTER INSERT ON futures_command_audit
    FOR EACH ROW EXECUTE FUNCTION futures_command_add_audit_usage();
