SELECT advance_account_baseline_revision('','baseline_source_health','UNINSTALL');
DROP INDEX raw_stream_messages_baseline_hb_idx;
DROP TRIGGER account_baseline_checkpoint_truncate ON stream_checkpoints;
DROP TRIGGER account_baseline_checkpoint_watch ON stream_checkpoints;
DROP FUNCTION observe_account_baseline_checkpoint();
DROP TRIGGER account_baseline_heartbeat_watch ON raw_stream_messages;
DROP FUNCTION observe_account_baseline_heartbeat();
DROP FUNCTION account_baseline_heartbeat_context(JSONB);
DROP TABLE account_baseline_source_contexts;
