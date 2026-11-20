-- TEST opt-in write-ahead evidence. No sender, retries or automatic recovery.
CREATE TABLE securities_command_intents (
    command_id BIGSERIAL PRIMARY KEY,
    environment TEXT NOT NULL CHECK (environment = 'test'),
    account_id TEXT NOT NULL,
    stream_key TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('order.submit','order.batch.submit','order.cancel')),
    message_id TEXT NOT NULL UNIQUE,
    idempotency_key TEXT NOT NULL,
    body_text TEXT NOT NULL,
    body_sha256 TEXT NOT NULL CHECK (length(body_sha256)=64),
    intent_sha256 TEXT NOT NULL CHECK (length(intent_sha256)=64),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(environment,account_id,action,idempotency_key),
    UNIQUE(command_id,account_id)
);
CREATE TABLE securities_command_dispatches (
    command_id BIGINT PRIMARY KEY,
    account_id TEXT NOT NULL,
    dispatched_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(command_id,account_id) REFERENCES securities_command_intents(command_id,account_id),
    UNIQUE(command_id,account_id)
);
CREATE TABLE securities_command_publications (
    command_id BIGINT PRIMARY KEY,
    account_id TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(command_id,account_id) REFERENCES securities_command_dispatches(command_id,account_id)
);
CREATE INDEX securities_command_account_idx ON securities_command_intents(account_id,command_id);
DO $$ DECLARE t TEXT; BEGIN
    FOREACH t IN ARRAY ARRAY['securities_command_intents','securities_command_dispatches','securities_command_publications'] LOOP
        EXECUTE format('CREATE TRIGGER securities_command_immutable BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation()',t);
        EXECUTE format('CREATE TRIGGER securities_command_no_truncate BEFORE TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION reject_account_baseline_mutation()',t);
        EXECUTE format('CREATE TRIGGER account_baseline_revision_watch AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION observe_account_baseline_revision()',t);
        EXECUTE format('CREATE TRIGGER account_baseline_revision_truncate AFTER TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION observe_account_baseline_revision()',t);
    END LOOP;
END $$;
