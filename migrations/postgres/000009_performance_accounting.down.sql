DROP TABLE IF EXISTS reverse_repo_accruals;
DROP TABLE IF EXISTS performance_nav_reconciliations;
DROP INDEX IF EXISTS performance_nav_versions_current_unique;
DROP TABLE IF EXISTS performance_nav_versions;
DROP TABLE IF EXISTS performance_nav_baselines;

DROP INDEX IF EXISTS cash_ledger_transfer_group_idx;
DROP INDEX IF EXISTS cash_ledger_effective_idx;
DROP INDEX IF EXISTS cash_ledger_idempotency_unique;

ALTER TABLE cash_ledger
    DROP CONSTRAINT IF EXISTS cash_ledger_status_check,
    DROP CONSTRAINT IF EXISTS cash_ledger_flow_class_check,
    DROP CONSTRAINT IF EXISTS cash_ledger_type_check,
    DROP CONSTRAINT IF EXISTS cash_ledger_entry_id_unique;

UPDATE cash_ledger
SET ledger_type = CASE
    WHEN ledger_type IN ('internal_transfer_in', 'internal_transfer_out', 'interest', 'dividend', 'reverse_repo_repayment') THEN 'adjustment'
    ELSE ledger_type
END;

ALTER TABLE cash_ledger
    DROP COLUMN IF EXISTS reversal_of_entry_id,
    DROP COLUMN IF EXISTS voided_at,
    DROP COLUMN IF EXISTS voided_by,
    DROP COLUMN IF EXISTS confirmed_at,
    DROP COLUMN IF EXISTS confirmed_by,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS transfer_group_id,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS effective_at,
    DROP COLUMN IF EXISTS counterparty_bucket,
    DROP COLUMN IF EXISTS cash_bucket,
    DROP COLUMN IF EXISTS flow_class,
    DROP COLUMN IF EXISTS entry_id,
    ADD CONSTRAINT cash_ledger_type_check CHECK (
        ledger_type IN ('freeze', 'unfreeze', 'trade', 'fee', 'deposit', 'withdraw', 'settlement', 'adjustment')
    );

DROP TABLE IF EXISTS performance_fee_rules;
