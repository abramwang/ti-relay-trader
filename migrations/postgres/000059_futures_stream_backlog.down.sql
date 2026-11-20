LOCK TABLE futures_stream_receivers, futures_stream_inbox IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM futures_stream_receivers r
        LEFT JOIN (
            SELECT stream_key,count(*) AS records,COALESCE(sum(octet_length(body)),0) AS bytes
            FROM futures_stream_inbox GROUP BY stream_key
        ) i USING(stream_key)
        WHERE r.audit_count<>COALESCE(i.records,0) OR r.audit_bytes<>COALESCE(i.bytes,0)
            OR r.audit_count<>r.pending_count+r.quarantined_count+r.delivered_count
            OR r.audit_bytes<>r.pending_bytes+r.quarantined_bytes+r.delivered_bytes
    ) THEN
        RAISE EXCEPTION 'futures stream counters inconsistent; downgrade will not hide the mismatch';
    END IF;
    IF EXISTS(SELECT 1 FROM futures_stream_receivers WHERE audit_count>50000 OR audit_bytes>67108864) THEN
        RAISE EXCEPTION 'historical stream receipts exceed old limits; downgrade must not delete evidence';
    END IF;
END $$;
DROP TRIGGER futures_stream_usage ON futures_stream_inbox;
DROP FUNCTION futures_stream_update_usage();
ALTER TABLE futures_stream_receivers
    DROP CONSTRAINT futures_stream_receivers_audit_count_check,
    DROP CONSTRAINT futures_stream_receivers_audit_bytes_check,
    ALTER COLUMN audit_count TYPE INTEGER,
    ADD CHECK(audit_count BETWEEN 0 AND 50000),
    ADD CHECK(audit_bytes BETWEEN 0 AND 67108864),
    DROP COLUMN pending_count,
    DROP COLUMN pending_bytes,
    DROP COLUMN quarantined_count,
    DROP COLUMN quarantined_bytes,
    DROP COLUMN delivered_count,
    DROP COLUMN delivered_bytes;
