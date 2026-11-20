-- Separate, non-additive command observations; never fabricated order rows.
CREATE INDEX raw_stream_messages_account_trade_archive_idx
    ON raw_stream_messages(account_id, raw_message_pk) WHERE stream_role = 'cmd.trade';
ALTER TABLE account_baseline_rows DROP CONSTRAINT account_baseline_rows_resource_check;
ALTER TABLE account_baseline_rows ADD CONSTRAINT account_baseline_rows_resource_check
    CHECK (resource IN ('positions', 'orders', 'fills', 'commands'));
