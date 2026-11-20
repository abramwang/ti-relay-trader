-- Optional securities receiver. Existing workers are not migrated or enabled.
CREATE TABLE ledger_stream_receivers (
    stream_key TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    binding JSONB NOT NULL,
    capture_epoch TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    initial_cursor TEXT NOT NULL,
    cursor_id TEXT NOT NULL,
    blocked_reason TEXT NOT NULL DEFAULT '',
    fault_count BIGINT NOT NULL DEFAULT 0 CHECK(fault_count >= 0),
    batch_count BIGINT NOT NULL DEFAULT 0 CHECK(batch_count >= 0),
    entry_count BIGINT NOT NULL DEFAULT 0 CHECK(entry_count >= 0),
    body_bytes BIGINT NOT NULL DEFAULT 0 CHECK(body_bytes >= 0),
    evidence_bytes BIGINT NOT NULL DEFAULT 0 CHECK(evidence_bytes >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE ledger_stream_read_batches (
    stream_key TEXT NOT NULL REFERENCES ledger_stream_receivers(stream_key),
    attempt_id TEXT NOT NULL,
    batch_sequence BIGINT NOT NULL,
    owner_id TEXT NOT NULL,
    start_cursor TEXT NOT NULL,
    end_cursor TEXT NOT NULL,
    input_sha256 TEXT NOT NULL,
    evidence TEXT NOT NULL,
    evidence_sha256 TEXT NOT NULL,
    application_report TEXT NOT NULL,
    application_report_sha256 TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('applied','read_failed','application_failed')),
    error_code TEXT NOT NULL,
    entry_count INTEGER NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(stream_key,attempt_id),
    UNIQUE(stream_key,batch_sequence)
);
CREATE TABLE ledger_stream_read_entries (
    stream_key TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    entry_id TEXT NOT NULL,
    body BYTEA NOT NULL,
    body_sha256 TEXT NOT NULL,
    PRIMARY KEY(stream_key,attempt_id,ordinal),
    FOREIGN KEY(stream_key,attempt_id) REFERENCES ledger_stream_read_batches(stream_key,attempt_id)
);
CREATE INDEX ledger_stream_read_entries_identity_idx ON ledger_stream_read_entries(stream_key,entry_id);
CREATE INDEX ledger_stream_receivers_account_idx ON ledger_stream_receivers(account_id);
CREATE TRIGGER ledger_read_batches_immutable BEFORE UPDATE OR DELETE
ON ledger_stream_read_batches FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation();
CREATE TRIGGER ledger_read_batches_no_truncate BEFORE TRUNCATE
ON ledger_stream_read_batches FOR EACH STATEMENT EXECUTE FUNCTION reject_account_baseline_mutation();
CREATE TRIGGER ledger_read_entries_immutable BEFORE UPDATE OR DELETE
ON ledger_stream_read_entries FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation();
CREATE TRIGGER ledger_read_entries_no_truncate BEFORE TRUNCATE
ON ledger_stream_read_entries FOR EACH STATEMENT EXECUTE FUNCTION reject_account_baseline_mutation();

CREATE FUNCTION observe_account_baseline_receiver() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' THEN
        IF NEW.account_id=OLD.account_id AND NEW.binding=OLD.binding
           AND NEW.capture_epoch=OLD.capture_epoch AND NEW.owner_id=OLD.owner_id
           AND NEW.blocked_reason=OLD.blocked_reason AND NEW.fault_count=OLD.fault_count THEN
            RETURN NULL;
        END IF;
    END IF;
    IF TG_OP='TRUNCATE' THEN
        PERFORM advance_account_baseline_revision('',TG_TABLE_NAME,TG_OP);
    ELSE
        IF TG_OP<>'INSERT' THEN
            PERFORM advance_account_baseline_revision(OLD.account_id,TG_TABLE_NAME,TG_OP);
        END IF;
        IF TG_OP<>'DELETE' THEN
            PERFORM advance_account_baseline_revision(NEW.account_id,TG_TABLE_NAME,TG_OP);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER account_baseline_receiver_watch AFTER INSERT OR UPDATE OR DELETE
ON ledger_stream_receivers FOR EACH ROW EXECUTE FUNCTION observe_account_baseline_receiver();
CREATE TRIGGER account_baseline_receiver_truncate AFTER TRUNCATE
ON ledger_stream_receivers FOR EACH STATEMENT EXECUTE FUNCTION observe_account_baseline_receiver();
SELECT advance_account_baseline_revision('','ledger_stream_receipts','INSTALL');
