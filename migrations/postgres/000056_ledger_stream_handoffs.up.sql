-- Explicit local receiver handoffs, not broker commands or coverage proofs.
CREATE TABLE ledger_stream_handoffs (
    handoff_id TEXT PRIMARY KEY,
    plan_sha256 TEXT NOT NULL,
    plan TEXT NOT NULL,
    receipt TEXT NOT NULL,
    receipt_sha256 TEXT NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER ledger_stream_handoffs_immutable BEFORE UPDATE OR DELETE
ON ledger_stream_handoffs FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation();
CREATE TRIGGER ledger_stream_handoffs_no_truncate BEFORE TRUNCATE
ON ledger_stream_handoffs FOR EACH STATEMENT EXECUTE FUNCTION reject_account_baseline_mutation();
