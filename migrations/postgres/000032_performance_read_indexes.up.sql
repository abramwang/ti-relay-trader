-- On a live database, pre-create these indexes CONCURRENTLY before migration.
-- The migration then records the version without blocking the ledger writer.
SET LOCAL lock_timeout = '2s';

CREATE INDEX IF NOT EXISTS position_snapshots_performance_cover_idx
    ON position_snapshots (account_id, trade_date)
    INCLUDE (market_value, unrealized_pnl, day_unrealized_pnl, settled_profit)
    WHERE snapshot_type = 'close';

CREATE INDEX IF NOT EXISTS fills_performance_cover_idx
    ON fills (account_id, trade_date)
    INCLUDE (trade_side, price, qty, fee)
    WHERE trade_date IS NOT NULL;

CREATE INDEX IF NOT EXISTS fills_performance_legacy_cover_idx
    ON fills (account_id, (COALESCE(matched_at, created_at)))
    INCLUDE (trade_side, price, qty, fee)
    WHERE trade_date IS NULL;
