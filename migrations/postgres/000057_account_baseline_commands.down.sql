-- Even an empty published commands resource requires the new reader. Refuse
-- downgrade rather than remove immutable evidence or misread it as absent.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM account_baseline_publications
               WHERE manifest::jsonb->'resources' ? 'commands') THEN
        RAISE EXCEPTION 'command baseline publications exist; downgrade refused';
    END IF;
END $$;
DROP INDEX raw_stream_messages_account_trade_archive_idx;
ALTER TABLE account_baseline_rows DROP CONSTRAINT account_baseline_rows_resource_check;
ALTER TABLE account_baseline_rows ADD CONSTRAINT account_baseline_rows_resource_check
    CHECK (resource IN ('positions', 'orders', 'fills'));
