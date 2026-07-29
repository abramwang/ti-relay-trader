DROP INDEX IF EXISTS raw_stream_messages_adapter_data_quality_idx;
DROP TABLE IF EXISTS etf_component_transfers;

DROP INDEX IF EXISTS fills_fallback_unique;

CREATE UNIQUE INDEX fills_fallback_unique
    ON fills(account_id, trade_date, order_stream_id, match_timestamp, qty, price)
    WHERE fill_id IS NULL
        AND order_stream_id IS NOT NULL
        AND match_timestamp IS NOT NULL;
