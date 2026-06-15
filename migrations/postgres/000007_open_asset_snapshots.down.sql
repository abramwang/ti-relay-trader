ALTER TABLE asset_snapshots
    DROP CONSTRAINT IF EXISTS asset_snapshots_type_check;

ALTER TABLE asset_snapshots
    ADD CONSTRAINT asset_snapshots_type_check
    CHECK (snapshot_type IN ('intraday', 'close', 'reconcile'));
