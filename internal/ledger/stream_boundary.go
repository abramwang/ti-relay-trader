package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Caller holds the stream transition lock. Operators must first stop any
// pre-guard binary; a database cursor is not proof of its in-flight work.
func reviewLedgerStreamBoundary(ctx context.Context, tx *sql.Tx, key, role, initial string) (json.RawMessage, error) {
	checkpoint, err := NewRepository(tx).GetStreamCheckpoint(ctx, key)
	found := err == nil
	if err != nil && !errors.Is(err, ErrStreamCheckpointNotFound) {
		return nil, err
	}
	if found {
		cmp, e := CompareCurrentBaselineStreamIDs(checkpoint.LastStreamID, initial)
		if e != nil || cmp != 0 || checkpoint.Role != role || checkpoint.ErrorCount != 0 || checkpoint.LastError != "" {
			return nil, ErrLedgerCaptureCursor
		}
	} else if initial != "0-0" {
		return nil, ErrLedgerCaptureCursor
	}
	var maxID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT stream_id FROM raw_stream_messages WHERE stream_key=$1
ORDER BY split_part(stream_id,'-',1)::numeric DESC,split_part(stream_id,'-',2)::numeric DESC LIMIT 1`, key).Scan(&maxID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if maxID.Valid {
		cmp, e := CompareCurrentBaselineStreamIDs(maxID.String, initial)
		if e != nil || cmp > 0 || !found {
			return nil, ErrLedgerCaptureCursor
		}
	}
	var checkpointValue *StreamCheckpoint
	var archiveTail *string
	if found {
		checkpointValue = &checkpoint
	}
	if maxID.Valid {
		archiveTail = &maxID.String
	}
	return json.Marshal(struct {
		Kind               string            `json:"kind"`
		CheckpointFound    bool              `json:"checkpoint_found"`
		Checkpoint         *StreamCheckpoint `json:"checkpoint"`
		ArchiveTail        *string           `json:"archive_tail"`
		InitialCursor      string            `json:"initial_cursor"`
		ContinuityVerified bool              `json:"continuity_verified"`
	}{"local_boundary_review_not_source_coverage", found, checkpointValue, archiveTail, initial, false})
}
