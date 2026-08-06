ALTER TABLE asset_snapshots
    DROP CONSTRAINT IF EXISTS asset_snapshots_type_check;

ALTER TABLE asset_snapshots
    ADD CONSTRAINT asset_snapshots_type_check
    CHECK (snapshot_type IN ('intraday', 'open', 'broker_close', 'close', 'reconcile'));

ALTER TABLE position_snapshots
    DROP CONSTRAINT IF EXISTS position_snapshots_type_check;

ALTER TABLE position_snapshots
    ADD CONSTRAINT position_snapshots_type_check
    CHECK (snapshot_type IN ('intraday', 'open', 'broker_close', 'close', 'reconcile'));
