-- Local securities-ledger changes only. This is not a broker/event watermark.
-- The securities ledger is deployment-isolated and has no per-row environment.
CREATE TABLE account_baseline_revision_epoch (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    epoch TEXT NOT NULL CHECK (epoch ~ '^[0-9a-f]{32}$')
);
INSERT INTO account_baseline_revision_epoch(epoch)
VALUES (md5(random()::text || clock_timestamp()::text));
CREATE TRIGGER account_baseline_epoch_immutable BEFORE UPDATE OR DELETE
ON account_baseline_revision_epoch FOR EACH ROW EXECUTE FUNCTION reject_account_baseline_mutation();

CREATE TABLE account_baseline_revisions (
    -- Empty account is the conservative invalidation counter for unscoped writes.
    account_id TEXT PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision > 0),
    source_table TEXT NOT NULL,
    source_operation TEXT NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL
);
CREATE FUNCTION guard_account_baseline_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RAISE EXCEPTION 'account baseline revisions cannot be removed';
    END IF;
    IF NEW.account_id <> OLD.account_id OR NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'account baseline revision must advance by one';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER account_baseline_revision_monotone BEFORE UPDATE OR DELETE
ON account_baseline_revisions FOR EACH ROW EXECUTE FUNCTION guard_account_baseline_revision();
CREATE TRIGGER account_baseline_revision_no_truncate BEFORE TRUNCATE
ON account_baseline_revisions FOR EACH STATEMENT EXECUTE FUNCTION guard_account_baseline_revision();
CREATE TRIGGER account_baseline_epoch_no_truncate BEFORE TRUNCATE
ON account_baseline_revision_epoch FOR EACH STATEMENT EXECUTE FUNCTION reject_account_baseline_mutation();

CREATE FUNCTION advance_account_baseline_revision(a TEXT, source TEXT, operation TEXT)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    -- Publication takes the same account lock and a shared unscoped lock.
    PERFORM pg_advisory_xact_lock(hashtextextended('relay.account-baseline:' || a, 0));
    INSERT INTO account_baseline_revisions(account_id,revision,source_table,source_operation,changed_at)
    VALUES(a,1,source,operation,clock_timestamp())
    ON CONFLICT(account_id) DO UPDATE SET revision=account_baseline_revisions.revision+1,
        source_table=EXCLUDED.source_table,source_operation=EXCLUDED.source_operation,
        changed_at=EXCLUDED.changed_at;
END;
$$;

CREATE FUNCTION observe_account_baseline_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    before_row JSONB;
    after_row JSONB;
    accounts_to_touch TEXT[] := ARRAY[]::TEXT[];
    a TEXT;
BEGIN
    IF TG_OP = 'TRUNCATE' THEN
        PERFORM advance_account_baseline_revision('',TG_TABLE_NAME,TG_OP);
        RETURN NULL;
    END IF;
    IF TG_OP <> 'INSERT' THEN before_row := to_jsonb(OLD); END IF;
    IF TG_OP <> 'DELETE' THEN after_row := to_jsonb(NEW); END IF;
    IF TG_OP = 'UPDATE' AND before_row = after_row THEN RETURN NULL; END IF;
    -- Ordinary heartbeat arrival is not a business revision. Session/source
    -- health and missing heartbeats need separate durable validity evidence.
    IF before_row IS NOT NULL AND (TG_TABLE_NAME <> 'raw_stream_messages'
       OR before_row->>'stream_role' IS DISTINCT FROM 'hb') THEN
        accounts_to_touch := array_append(accounts_to_touch,coalesce(before_row->>'account_id',''));
    END IF;
    IF after_row IS NOT NULL AND (TG_TABLE_NAME <> 'raw_stream_messages'
       OR after_row->>'stream_role' IS DISTINCT FROM 'hb') THEN
        accounts_to_touch := array_append(accounts_to_touch,coalesce(after_row->>'account_id',''));
    END IF;
    FOR a IN SELECT DISTINCT x FROM unnest(accounts_to_touch) x ORDER BY x LOOP
        PERFORM advance_account_baseline_revision(a,TG_TABLE_NAME,TG_OP);
    END LOOP;
    RETURN NULL;
END;
$$;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['accounts','account_gateway_routes','orders','order_events','fills',
        'positions','asset_snapshots','cash_ledger','etf_component_transfers','order_cancel_attempts',
        'test_legacy_order_resolutions','raw_stream_messages'] LOOP
        EXECUTE format('CREATE TRIGGER account_baseline_revision_watch AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION observe_account_baseline_revision()',t);
        EXECUTE format('CREATE TRIGGER account_baseline_revision_truncate AFTER TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION observe_account_baseline_revision()',t);
    END LOOP;
END;
$$;
