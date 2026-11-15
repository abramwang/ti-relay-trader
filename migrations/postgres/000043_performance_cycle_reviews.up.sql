CREATE TABLE performance_cycle_reviews (
    review_pk BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(account_id),
    version_id TEXT NOT NULL CHECK(length(version_id) BETWEEN 1 AND 160),
    date_from DATE NOT NULL,
    observed_through DATE NOT NULL CHECK(observed_through >= date_from),
    payload_sha256 TEXT NOT NULL CHECK(payload_sha256 ~ '^[0-9a-f]{64}$'),
    payload_text TEXT NOT NULL CHECK(octet_length(payload_text) BETWEEN 2 AND 2097152),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(account_id, version_id),
    CHECK(jsonb_typeof(payload_text::jsonb) = 'object')
);
CREATE INDEX performance_cycle_reviews_latest ON performance_cycle_reviews(account_id,review_pk DESC);
CREATE FUNCTION reject_performance_cycle_review_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'cycle reviews are immutable; append a new version';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER performance_cycle_review_immutable BEFORE UPDATE OR DELETE ON performance_cycle_reviews
FOR EACH ROW EXECUTE FUNCTION reject_performance_cycle_review_mutation();
