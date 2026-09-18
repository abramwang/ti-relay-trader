BEGIN;

CREATE TABLE oc_credential_audit (
    audit_id BIGSERIAL PRIMARY KEY,
    operation_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    broker_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    action TEXT NOT NULL,
    status TEXT NOT NULL,
    credential_version BIGINT,
    key_id TEXT NOT NULL DEFAULT '',
    envelope_sha256 TEXT NOT NULL DEFAULT '',
    operator TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT oc_credential_audit_operation_check
        CHECK (action IN ('rotate', 'disable')),
    CONSTRAINT oc_credential_audit_status_check
        CHECK (status IN ('started', 'succeeded', 'failed')),
    CONSTRAINT oc_credential_audit_environment_check
        CHECK (environment IN ('test', 'prod')),
    CONSTRAINT oc_credential_audit_version_check
        CHECK (credential_version IS NULL OR credential_version > 0),
    CONSTRAINT oc_credential_audit_sha256_check
        CHECK (envelope_sha256 = '' OR envelope_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX oc_credential_audit_account_idx
    ON oc_credential_audit(environment, broker_id, account_id, audit_id DESC);

CREATE INDEX oc_credential_audit_operation_idx
    ON oc_credential_audit(operation_id, audit_id);

COMMIT;
