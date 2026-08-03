ALTER TABLE position_snapshots
    DROP COLUMN IF EXISTS cost_complete,
    DROP COLUMN IF EXISTS avg_cost_source,
    DROP COLUMN IF EXISTS total_cost;

ALTER TABLE positions
    DROP COLUMN IF EXISTS cost_complete,
    DROP COLUMN IF EXISTS avg_cost_source,
    DROP COLUMN IF EXISTS total_cost;
