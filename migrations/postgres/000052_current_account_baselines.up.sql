-- Immutable, reference-only publications. No live account revision or execution proof yet.
CREATE TABLE account_baseline_heads (
    environment TEXT NOT NULL CHECK (environment IN ('test', 'production')),
    account_id TEXT NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
    PRIMARY KEY (environment, account_id)
);
CREATE TABLE account_baseline_publications (
    baseline_id TEXT PRIMARY KEY,
    environment TEXT NOT NULL,
    account_id TEXT NOT NULL,
    broker_id TEXT NOT NULL,
    gateway_id TEXT NOT NULL,
    trade_date TEXT NOT NULL CHECK (trade_date ~ '^[0-9]{8}$'),
    collection_id TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    candidate_sha256 TEXT NOT NULL CHECK (length(candidate_sha256) = 64),
    manifest TEXT NOT NULL,
    manifest_sha256 TEXT NOT NULL CHECK (length(manifest_sha256) = 64),
    FOREIGN KEY (environment, account_id) REFERENCES account_baseline_heads,
    UNIQUE (environment, account_id, collection_id),
    UNIQUE (environment, account_id, generation)
);
CREATE INDEX account_baseline_latest ON account_baseline_publications
    (environment, account_id, broker_id, gateway_id, trade_date, generation DESC);
CREATE TABLE account_baseline_rows (
    baseline_id TEXT NOT NULL REFERENCES account_baseline_publications,
    resource TEXT NOT NULL CHECK (resource IN ('positions', 'orders', 'fills')),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    record_key TEXT NOT NULL,
    document TEXT NOT NULL,
    document_sha256 TEXT NOT NULL CHECK (length(document_sha256) = 64),
    PRIMARY KEY (baseline_id, resource, ordinal),
    UNIQUE (baseline_id, resource, record_key)
);
CREATE FUNCTION reject_account_baseline_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'account baseline publications are immutable';
END;
$$;
CREATE TRIGGER account_baseline_publication_immutable BEFORE UPDATE OR DELETE
    ON account_baseline_publications FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation();
CREATE TRIGGER account_baseline_rows_immutable BEFORE UPDATE OR DELETE
    ON account_baseline_rows FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation();
