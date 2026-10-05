package ledger

import (
	"context"
	"encoding/json"
	"fmt"
)

// Structure updates error identity without advancing the worker's cursor.
func (repo *Repository) StructureStreamCheckpointError(ctx context.Context, checkpoint StreamCheckpoint, active map[string]any) (bool, error) {
	metadata, err := json.Marshal(map[string]any{"active_error": active, "last_error_stream_id": active["stream_id"]})
	if err != nil {
		return false, err
	}
	result, err := repo.exec.ExecContext(ctx, `UPDATE stream_checkpoints
SET metadata = metadata || $4::jsonb, updated_at = now()
WHERE stream_key = $1 AND last_stream_id = $2 AND last_error = $3
AND NOT (metadata ? 'active_error' AND metadata->'active_error' = 'null'::jsonb)`, checkpoint.StreamKey, checkpoint.LastStreamID, checkpoint.LastError, metadata)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (repo *Repository) ReadCheckpointReply(ctx context.Context, stream, id string) (json.RawMessage, error) {
	q, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT body FROM raw_stream_messages
WHERE stream_key = $1 AND stream_id = $2 AND stream_role = 'reply' AND COALESCE(parse_error, '') = ''`, stream, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("archived reply %s/%s not found", stream, id)
	}
	var body json.RawMessage
	err = rows.Scan(&body)
	return body, err
}

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
	return repo.checkpointRecoveryBlocked(ctx, account, false)
}

func (repo *Repository) CheckpointReconciliationBlocked(ctx context.Context, account string) (bool, error) {
	return repo.checkpointRecoveryBlocked(ctx, account, true)
}

func (repo *Repository) checkpointRecoveryBlocked(ctx context.Context, account string, includeActiveOrders bool) (bool, error) {
	q, err := repo.queryer()
	if err != nil {
		return true, err
	}
	rows, err := q.QueryContext(ctx, `SELECT EXISTS (
SELECT 1 FROM raw_stream_messages WHERE account_id = $1
AND code = 'COMMAND_OUTCOME_UNKNOWN'
) OR EXISTS (
SELECT 1 FROM reconciliation_breaks WHERE account_id = $1 AND status = 'open'
) OR EXISTS (
SELECT 1 FROM orders WHERE account_id = $1 AND is_terminal = false AND $2
)`, account, includeActiveOrders)
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
