-- Health transitions invalidate observations, but do not prove source coverage.
CREATE TABLE account_baseline_source_contexts (
    stream_key TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    context JSONB NOT NULL
);
CREATE TRIGGER account_baseline_revision_watch AFTER INSERT OR UPDATE OR DELETE
ON account_baseline_source_contexts FOR EACH ROW EXECUTE FUNCTION observe_account_baseline_revision();
CREATE TRIGGER account_baseline_revision_truncate AFTER TRUNCATE
ON account_baseline_source_contexts FOR EACH STATEMENT EXECUTE FUNCTION observe_account_baseline_revision();

CREATE FUNCTION account_baseline_heartbeat_context(doc JSONB) RETURNS JSONB
LANGUAGE sql IMMUTABLE AS $$
    SELECT coalesce(jsonb_object_agg(key,value),'{}'::jsonb)
    FROM jsonb_each(CASE WHEN jsonb_typeof(doc)='object' THEN doc ELSE '{}'::jsonb END)
    WHERE key IN ('counter_session_id','connection_generation','trade_date','trading_day',
        'state','state_text','redis_ready','broker_ready','query_collection_ready',
        'accepting_query_commands','order_snapshot_ready','credential_status');
$$;

CREATE FUNCTION observe_account_baseline_heartbeat() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE sig JSONB;
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.stream_role='hb' THEN
        -- Rewriting/removing a retained heartbeat is not a normal new arrival.
        IF TG_OP='DELETE' OR to_jsonb(OLD) IS DISTINCT FROM to_jsonb(NEW) THEN
            PERFORM advance_account_baseline_revision(coalesce(OLD.account_id,''),TG_TABLE_NAME,TG_OP);
        END IF;
    END IF;
    IF TG_OP='DELETE' THEN RETURN NULL; END IF;
    IF NEW.stream_role <> 'hb' THEN RETURN NULL; END IF;
    sig := jsonb_build_object(
        'direction',NEW.direction,'parse_error',coalesce(NEW.parse_error,''),
        'protocol',NEW.body->'protocol','message_type',NEW.body->'message_type',
        'routing',NEW.body->'routing',
        'top',account_baseline_heartbeat_context(NEW.body),
        'payload',account_baseline_heartbeat_context(NEW.body->'payload'),
        'adapter_context',account_baseline_heartbeat_context(NEW.body->'adapter_context'));
    INSERT INTO account_baseline_source_contexts(stream_key,account_id,context)
    VALUES(NEW.stream_key,coalesce(NEW.account_id,''),sig)
    ON CONFLICT(stream_key) DO UPDATE SET account_id=EXCLUDED.account_id,context=EXCLUDED.context
    WHERE account_baseline_source_contexts.account_id IS DISTINCT FROM EXCLUDED.account_id
       OR account_baseline_source_contexts.context IS DISTINCT FROM EXCLUDED.context;
    RETURN NULL;
END;
$$;
CREATE TRIGGER account_baseline_heartbeat_watch AFTER INSERT OR UPDATE OR DELETE
ON raw_stream_messages FOR EACH ROW EXECUTE FUNCTION observe_account_baseline_heartbeat();

CREATE FUNCTION observe_account_baseline_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_error JSONB;
    new_error JSONB;
    old_id NUMERIC[];
    new_id NUMERIC[];
BEGIN
    IF TG_OP IN ('INSERT','DELETE','TRUNCATE') THEN
        PERFORM advance_account_baseline_revision('',TG_TABLE_NAME,TG_OP);
        RETURN NULL;
    END IF;
    IF to_jsonb(OLD)=to_jsonb(NEW) THEN RETURN NULL; END IF;
    old_error := CASE WHEN OLD.metadata ? 'active_error' THEN OLD.metadata->'active_error'
        ELSE to_jsonb(OLD.last_error) END;
    new_error := CASE WHEN NEW.metadata ? 'active_error' THEN NEW.metadata->'active_error'
        ELSE to_jsonb(NEW.last_error) END;
    -- Numeric components, not lexicographic order or an assumed contiguous ID.
    IF OLD.last_stream_id ~ '^[0-9]+-[0-9]+$' AND NEW.last_stream_id ~ '^[0-9]+-[0-9]+$' THEN
        old_id := string_to_array(OLD.last_stream_id,'-')::NUMERIC[];
        new_id := string_to_array(NEW.last_stream_id,'-')::NUMERIC[];
    ELSIF OLD.last_stream_id='0' AND NEW.last_stream_id ~ '^[0-9]+-[0-9]+$' THEN
        old_id := ARRAY[0,0]::NUMERIC[];
        new_id := string_to_array(NEW.last_stream_id,'-')::NUMERIC[];
    END IF;
    IF OLD.stream_key IS DISTINCT FROM NEW.stream_key OR OLD.stream_role IS DISTINCT FROM NEW.stream_role
       OR old_error IS DISTINCT FROM new_error OR OLD.error_count IS DISTINCT FROM NEW.error_count
       OR NEW.processed_count < OLD.processed_count
       OR (OLD.last_processed_at IS NOT NULL AND (NEW.last_processed_at IS NULL OR NEW.last_processed_at < OLD.last_processed_at))
       OR (OLD.last_stream_id IS DISTINCT FROM NEW.last_stream_id AND (old_id IS NULL OR new_id IS NULL OR new_id < old_id)) THEN
        -- Existing checkpoints do not have a trusted account column.
        PERFORM advance_account_baseline_revision('',TG_TABLE_NAME,TG_OP);
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER account_baseline_checkpoint_watch AFTER INSERT OR UPDATE OR DELETE
ON stream_checkpoints FOR EACH ROW EXECUTE FUNCTION observe_account_baseline_checkpoint();
CREATE TRIGGER account_baseline_checkpoint_truncate AFTER TRUNCATE
ON stream_checkpoints FOR EACH STATEMENT EXECUTE FUNCTION observe_account_baseline_checkpoint();

CREATE INDEX raw_stream_messages_baseline_hb_idx
ON raw_stream_messages(stream_key,received_at DESC,raw_message_pk DESC) WHERE stream_role='hb';

-- Older publications were not observing these transitions.
SELECT advance_account_baseline_revision('','baseline_source_health','INSTALL');
