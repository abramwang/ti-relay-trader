DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['accounts','account_gateway_routes','orders','order_events','fills',
        'positions','asset_snapshots','cash_ledger','etf_component_transfers','order_cancel_attempts',
        'test_legacy_order_resolutions','raw_stream_messages'] LOOP
        EXECUTE format('DROP TRIGGER account_baseline_revision_watch ON %I',t);
        EXECUTE format('DROP TRIGGER account_baseline_revision_truncate ON %I',t);
    END LOOP;
END;
$$;
DROP FUNCTION observe_account_baseline_revision();
DROP FUNCTION advance_account_baseline_revision(TEXT,TEXT,TEXT);
DROP TABLE account_baseline_revisions;
DROP TABLE account_baseline_revision_epoch;
DROP FUNCTION guard_account_baseline_revision();
