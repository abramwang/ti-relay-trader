-- Existing unbound prototypes remain audit-only; no identity is inferred.
ALTER TABLE futures_event_scopes ADD COLUMN native_user_id TEXT;
CREATE TABLE futures_query_commands (
    batch_id TEXT PRIMARY KEY REFERENCES futures_query_batches(batch_id),
    command_id BIGINT NOT NULL UNIQUE REFERENCES futures_commands(command_id)
);
