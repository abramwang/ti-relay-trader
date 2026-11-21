package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

var (
	ErrCommandBacklog = errors.New("futures command response backlog capacity reached")
	ErrCommandWork    = errors.New("futures command workload evidence inconsistent")
)

const maxPendingCommands = 4096
const maxPendingCommandBytes = 64 * 1024 * 1024

// Registration holds the route lock. The partial index bounds this scan to
// outstanding intentions, not aliases, rejected attempts or completed history.
func checkCommandBacklog(ctx context.Context, tx *sql.Tx, key string) error {
	var count, size int64
	err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(work_bytes),0) FROM (
        SELECT work_bytes FROM futures_command_work WHERE route_key=$1 AND pending
        ORDER BY command_id LIMIT 4097) w`, key).Scan(&count, &size)
	if err != nil {
		return err
	}
	if count > maxPendingCommands || size > maxPendingCommandBytes {
		return ErrCommandBacklog
	}
	return nil
}

// This is response workload, never order finality or permission to resend.
// Late contradictory responses may restore work even above the intake limit.
func setCommandPending(ctx context.Context, tx *sql.Tx, id int64, pending bool) error {
	r, err := tx.ExecContext(ctx, `UPDATE futures_command_work w SET pending=$2
        FROM futures_commands c WHERE w.command_id=$1 AND c.command_id=w.command_id
        AND w.route_key=c.route_key
        AND w.work_bytes=octet_length(c.body)::bigint+octet_length(c.intent)+octet_length(c.evidence_json::text)`, id, pending)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return ErrCommandWork
	}
	return nil
}

func pendingReplyResult(r ReplyResult) bool {
	return !r.ResponseValidated || !oneOf(r.State, "ready", "failed", "accepted")
}

// Bounded local revalidation also handles pre-upgrade commands. No command is
// resent and no old session is made current. Unproven outcomes stay pending.
func (p *ReplyProjector) reconcileCommandWork(ctx context.Context, h StreamHandle, limit int) (int, error) {
	if limit < 1 || limit > 256 {
		return 0, ErrAuditCapacity
	}
	rows, err := p.store.db.QueryContext(ctx, `SELECT command_id FROM futures_command_work
        WHERE route_key=$1 AND pending AND command_id>$2 ORDER BY command_id LIMIT $3`, string(commandRouteBytes(p.scope.Route)), p.workAfter, limit)
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	released := 0
	for _, id := range ids {
		ok, err := p.reconcileOneCommand(ctx, h, id)
		if err != nil {
			return released, err
		}
		if ok {
			released++
		}
		p.workAfter = id
	}
	if len(ids) < limit {
		p.workAfter = 0
	}
	return released, nil
}

func (p *ReplyProjector) reconcileOneCommand(ctx context.Context, h StreamHandle, id int64) (bool, error) {
	tx, err := beginFuturesTransaction(ctx, p.store.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Same lock order as normal reply handoff; no route/command-row update is
	// needed here, avoiding an inversion against registration and close checks.
	if _, _, _, err := lockStream(ctx, tx, h); err != nil {
		return false, err
	}
	c, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(p.scope.Route)), id)
	if err != nil {
		return false, err
	}
	if c.Scope != p.scope {
		return false, ErrStreamIdentity
	}
	var batch string
	err = tx.QueryRowContext(ctx, `SELECT batch_id FROM futures_query_commands WHERE command_id=$1`, id).Scan(&batch)
	if err == nil {
		q := &QueryStore{db: p.store.db}
		saved, err := q.load(ctx, tx, batch)
		if err != nil {
			return false, err
		}
		if saved.batch.state != BatchReady && saved.batch.state != BatchFailed {
			return false, nil
		}
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		table, targetColumn := "futures_test_query_publications", "target_json"
		if queryResultType(c.Original.Action) == "" {
			if !oneOf(c.Original.Action, "order.submit", "order.cancel") {
				return false, nil
			}
			table, targetColumn = "futures_test_trade_publications", "target_json->'target'"
		}
		var state, reason string
		var entry sql.NullString
		var targetRaw []byte
		err = tx.QueryRowContext(ctx, `SELECT state,reason,entry_id,`+targetColumn+` FROM `+table+` WHERE command_id=$1`, id).Scan(&state, &reason, &entry, &targetRaw)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		target, err := DecodeTestQueryTarget(targetRaw)
		if err != nil || target.SourceID != h.config.SourceID {
			return false, ErrStreamIdentity
		}
		if state == "attempted" {
			return false, nil
		}
		if state == "not_published" {
			if reason != "atomic_gate_changed" || entry.Valid {
				return false, ErrStreamIdentity
			}
		} else {
			if state != "published" {
				return false, ErrStreamIdentity
			}
			if _, err := streamID(entry.String); err != nil || entry.String == "0-0" {
				return false, ErrStreamIdentity
			}
			previous, count, size, err := readReplyResult(ctx, tx, id, target)
			if err != nil || previous == nil {
				return false, err
			}
			pages, err := replyResultInputs(ctx, tx, h.stream, id, count, size)
			if err != nil {
				return false, err
			}
			derived := assembleReplyResult(c, target, pages)
			a, _ := json.Marshal(previous)
			b, _ := json.Marshal(derived)
			if !bytes.Equal(a, b) {
				return false, ErrStreamIdentity
			}
			if pendingReplyResult(derived) {
				return false, nil
			}
		}
	}
	if err := setCommandPending(ctx, tx, id, false); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
