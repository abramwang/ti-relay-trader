ALTER TABLE positions
    ADD COLUMN total_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    ADD COLUMN avg_cost_source TEXT NOT NULL DEFAULT '',
    ADD COLUMN cost_complete BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE position_snapshots
    ADD COLUMN total_cost NUMERIC(20, 6) NOT NULL DEFAULT 0,
    ADD COLUMN avg_cost_source TEXT NOT NULL DEFAULT '',
    ADD COLUMN cost_complete BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN positions.total_cost IS 'Broker-reported total position cost; not market value.';
COMMENT ON COLUMN positions.avg_cost_source IS 'OC cost source such as broker_total_position_cost, broker_history_position_price, or unavailable.';
COMMENT ON COLUMN positions.cost_complete IS 'True only when OC reports a usable broker cost source.';
COMMENT ON COLUMN position_snapshots.total_cost IS 'Broker-reported total position cost captured with this snapshot; not market value.';
COMMENT ON COLUMN position_snapshots.avg_cost_source IS 'OC cost source captured with this snapshot.';
COMMENT ON COLUMN position_snapshots.cost_complete IS 'True only when the captured OC position reports a usable broker cost source.';
