ALTER TABLE asset_snapshots
    ADD COLUMN reverse_repo_receivable NUMERIC(20, 6) NOT NULL DEFAULT 0;
