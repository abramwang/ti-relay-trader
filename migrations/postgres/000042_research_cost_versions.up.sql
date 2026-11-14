-- Research snapshots are deliberately separate from the published cost/NAV tables.
CREATE TABLE performance_research_cost_versions (
    account_id TEXT NOT NULL REFERENCES accounts(account_id),
    version_id TEXT NOT NULL CHECK(length(version_id) BETWEEN 1 AND 160),
    anchor_date DATE NOT NULL,
    date_from DATE NOT NULL,
    date_to DATE NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK(payload_sha256 ~ '^[0-9a-f]{64}$'),
    payload_text TEXT NOT NULL CHECK(octet_length(payload_text) BETWEEN 2 AND 16777216),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(account_id, version_id),
    CHECK(anchor_date < date_from AND date_from <= date_to),
    CHECK(jsonb_typeof(payload_text::jsonb) = 'object')
);

CREATE FUNCTION reject_research_cost_version_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'research cost versions are immutable; append a new version';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER research_cost_version_immutable
BEFORE UPDATE OR DELETE ON performance_research_cost_versions
FOR EACH ROW EXECUTE FUNCTION reject_research_cost_version_mutation();
