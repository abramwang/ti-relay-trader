package ledger

import (
	"context"
	"encoding/json"
)

// Recovery changes health metadata only; the cursor and historical error remain intact.
func (repo *Repository) RecoverStreamCheckpoint(ctx context.Context, stream, cursor, lastError string, evidence map[string]any) (bool, error) {
	metadata, err := json.Marshal(map[string]any{"active_error": nil, "last_recovered_error": evidence})
	if err != nil {
		return false, err
	}
	result, err := repo.exec.ExecContext(ctx, `UPDATE stream_checkpoints
SET metadata = metadata || $4::jsonb, updated_at = now()
WHERE stream_key = $1 AND last_stream_id = $2 AND last_error = $3
AND NOT (metadata ? 'active_error' AND metadata->'active_error' = 'null'::jsonb)`, stream, cursor, lastError, metadata)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (repo *Repository) CheckpointRecoveryBlocked(ctx context.Context, account string) (bool, error) {
	q, err := repo.queryer()
	if err != nil {
		return true, err
	}
	rows, err := q.QueryContext(ctx, `SELECT EXISTS (
SELECT 1 FROM raw_stream_messages WHERE account_id = $1
AND code = 'COMMAND_OUTCOME_UNKNOWN'
) OR EXISTS (
SELECT 1 FROM reconciliation_breaks WHERE account_id = $1 AND status = 'open'
)`, account)
	if err != nil {
		return true, err
	}
	defer rows.Close()
	if !rows.Next() {
		return true, rows.Err()
	}
	var blocked bool
	err = rows.Scan(&blocked)
	return blocked, err
}
