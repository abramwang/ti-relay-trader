-- Historical receipts are not a lifetime quota. Keep workload counters in the
-- same transaction as inserts and delivery, without discarding any originals.
LOCK TABLE futures_stream_receivers, futures_stream_inbox IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM futures_stream_receivers r
        LEFT JOIN (
            SELECT stream_key,count(*) AS records,COALESCE(sum(octet_length(body)),0) AS bytes
            FROM futures_stream_inbox GROUP BY stream_key
        ) i USING(stream_key)
        WHERE r.audit_count<>COALESCE(i.records,0) OR r.audit_bytes<>COALESCE(i.bytes,0)
    ) THEN
        RAISE EXCEPTION 'futures stream counters inconsistent; migration will not repair evidence';
    END IF;
END $$;

ALTER TABLE futures_stream_receivers
    DROP CONSTRAINT futures_stream_receivers_audit_count_check,
    DROP CONSTRAINT futures_stream_receivers_audit_bytes_check,
    ALTER COLUMN audit_count TYPE BIGINT,
    ADD CHECK(audit_count>=0),
    ADD CHECK(audit_bytes>=0),
    ADD COLUMN pending_count BIGINT NOT NULL DEFAULT 0 CHECK(pending_count>=0),
    ADD COLUMN pending_bytes BIGINT NOT NULL DEFAULT 0 CHECK(pending_bytes>=0),
    ADD COLUMN quarantined_count BIGINT NOT NULL DEFAULT 0 CHECK(quarantined_count>=0),
    ADD COLUMN quarantined_bytes BIGINT NOT NULL DEFAULT 0 CHECK(quarantined_bytes>=0),
    ADD COLUMN delivered_count BIGINT NOT NULL DEFAULT 0 CHECK(delivered_count>=0),
    ADD COLUMN delivered_bytes BIGINT NOT NULL DEFAULT 0 CHECK(delivered_bytes>=0);

UPDATE futures_stream_receivers r SET
    pending_count=i.pending_count,pending_bytes=i.pending_bytes,
    quarantined_count=i.quarantined_count,quarantined_bytes=i.quarantined_bytes,
    delivered_count=i.delivered_count,delivered_bytes=i.delivered_bytes
FROM (
    SELECT stream_key,
        count(*) FILTER(WHERE state='pending') AS pending_count,
        COALESCE(sum(octet_length(body)) FILTER(WHERE state='pending'),0) AS pending_bytes,
        count(*) FILTER(WHERE state='quarantined') AS quarantined_count,
        COALESCE(sum(octet_length(body)) FILTER(WHERE state='quarantined'),0) AS quarantined_bytes,
        count(*) FILTER(WHERE state='delivered') AS delivered_count,
        COALESCE(sum(octet_length(body)) FILTER(WHERE state='delivered'),0) AS delivered_bytes
    FROM futures_stream_inbox GROUP BY stream_key
) i WHERE r.stream_key=i.stream_key;

CREATE FUNCTION futures_stream_update_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_state TEXT := '';
    new_state TEXT := '';
    old_count BIGINT := 0;
    new_count BIGINT := 0;
    old_bytes BIGINT := 0;
    new_bytes BIGINT := 0;
    target_key TEXT;
BEGIN
    IF TG_OP<>'INSERT' THEN
        old_state:=OLD.state; old_count:=1; old_bytes:=octet_length(OLD.body);
        target_key:=OLD.stream_key;
    END IF;
    new_state:=NEW.state; new_count:=1; new_bytes:=octet_length(NEW.body);
    target_key:=NEW.stream_key;
    IF TG_OP='UPDATE' AND (NEW.stream_key<>OLD.stream_key OR NEW.entry_id<>OLD.entry_id
        OR NEW.body<>OLD.body OR NEW.body_sha256<>OLD.body_sha256) THEN
        RAISE EXCEPTION 'state handoff must preserve the original receipt';
    END IF;
    UPDATE futures_stream_receivers SET
        audit_count=audit_count+new_count-old_count,
        audit_bytes=audit_bytes+new_bytes-old_bytes,
        pending_count=pending_count+(CASE WHEN new_state='pending' THEN 1 ELSE 0 END)-(CASE WHEN old_state='pending' THEN 1 ELSE 0 END),
        pending_bytes=pending_bytes+(CASE WHEN new_state='pending' THEN new_bytes ELSE 0 END)-(CASE WHEN old_state='pending' THEN old_bytes ELSE 0 END),
        quarantined_count=quarantined_count+(CASE WHEN new_state='quarantined' THEN 1 ELSE 0 END)-(CASE WHEN old_state='quarantined' THEN 1 ELSE 0 END),
        quarantined_bytes=quarantined_bytes+(CASE WHEN new_state='quarantined' THEN new_bytes ELSE 0 END)-(CASE WHEN old_state='quarantined' THEN old_bytes ELSE 0 END),
        delivered_count=delivered_count+(CASE WHEN new_state='delivered' THEN 1 ELSE 0 END)-(CASE WHEN old_state='delivered' THEN 1 ELSE 0 END),
        delivered_bytes=delivered_bytes+(CASE WHEN new_state='delivered' THEN new_bytes ELSE 0 END)-(CASE WHEN old_state='delivered' THEN old_bytes ELSE 0 END)
    WHERE stream_key=target_key;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'missing stream receiver';
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER futures_stream_usage
    -- No deletion/rewriting path: never normalize counters to missing evidence.
    AFTER INSERT OR UPDATE OF state ON futures_stream_inbox
    FOR EACH ROW EXECUTE FUNCTION futures_stream_update_usage();
