-- Make reconciliation inputs and breaks idempotent per run.

CREATE UNIQUE INDEX reconciliation_inputs_unique
    ON reconciliation_inputs(run_id, source, input_type);

CREATE UNIQUE INDEX reconciliation_breaks_unique
    ON reconciliation_breaks(
        run_id,
        COALESCE(account_id, ''),
        break_type,
        object_type,
        COALESCE(object_id, '')
    );
