package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
)

// ReplyProjector uses the durable command/publication as its authority, never
// an unsolicited response. It records OC facts, not independently reviewed SDK
// evidence or external order/fill economic entries.
type ReplyProjector struct {
	store     *StreamStore
	scope     CommandScope
	target    TestQueryTarget
	after     int64
	workAfter int64
}

type ReplyProjectionPage struct {
	Scanned             int             `json:"scanned"`
	Delivered           int             `json:"delivered"`
	Invalid             int             `json:"invalid"`
	Deferred            []DispatchIssue `json:"deferred"`
	CycleComplete       bool            `json:"cycle_complete"`
	CommandWorkReleased int             `json:"command_work_released"`
}

func NewReplyProjector(store *StreamStore, scope CommandScope, target TestQueryTarget) (*ReplyProjector, error) {
	if store == nil || scope.validate() != nil || scope.Route.Env != "test" || target.Validate() != nil {
		return nil, ErrStreamIdentity
	}
	return &ReplyProjector{store: store, scope: scope, target: target}, nil
}

func (p *ReplyProjector) Step(ctx context.Context, h StreamHandle, limit int) (ReplyProjectionPage, error) {
	out := ReplyProjectionPage{Deferred: []DispatchIssue{}}
	if h.config.Role != "reply" || h.config.Scope != p.scope || h.config.SourceID != p.target.SourceID {
		return out, ErrStreamIdentity
	}
	var err error
	out.CommandWorkReleased, err = p.reconcileCommandWork(ctx, h, limit)
	if err != nil {
		return out, err
	}
	rows, err := p.store.Pending(ctx, h, p.after, limit)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		state, reason, err := p.apply(ctx, h, row.ID)
		if err != nil {
			return out, err
		}
		p.after = row.ID
		out.Scanned++
		if reason != "" {
			out.Deferred = append(out.Deferred, DispatchIssue{row.ID, reason})
		} else {
			out.Delivered++
		}
		if state == "invalid" {
			out.Invalid++
		}
	}
	if len(rows) < limit {
		p.after = 0
		out.CycleComplete = true
	}
	return out, nil
}

func (p *ReplyProjector) apply(ctx context.Context, h StreamHandle, id int64) (string, string, error) {
	tx, err := beginFuturesTransaction(ctx, p.store.db)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return "", "", err
	}
	var same bool
	transport, _ := json.Marshal(struct{ SourceID, RunID, Epoch, Group string }{p.target.SourceID, p.target.ServerRunID, p.target.Epoch, p.target.Group})
	if err = tx.QueryRowContext(ctx, `SELECT transport_json=$2::jsonb FROM futures_test_reply_sources WHERE stream_key=$1`, h.stream, transport).Scan(&same); err != nil {
		return "", "", err
	}
	if !same {
		return "", "", ErrStreamIdentity
	}
	row, err := readStreamRecord(ctx, tx, h, id)
	if err != nil {
		return "", "", err
	}
	if row.State != "pending" {
		return "", "already_handled", nil
	}
	o, err := parseReply(row.Raw)
	if err != nil || !validID(textField(o, "origin_message_id")) {
		return "", "unroutable_reply", nil
	}
	key := string(commandRouteBytes(p.scope.Route))
	var commandID int64
	err = tx.QueryRowContext(ctx, `SELECT command_id FROM futures_command_messages WHERE route_key=$1 AND message_id=$2`, key, textField(o, "origin_message_id")).Scan(&commandID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "unregistered_command", nil
	}
	if err != nil {
		return "", "", err
	}
	c, err := readRegisteredCommand(ctx, tx, key, commandID)
	if err != nil {
		return "", "", err
	}
	if c.Scope != p.scope || c.Original.MessageID != textField(o, "origin_message_id") {
		return "", "original_command_mismatch", nil
	}
	if queryResultType(c.Original.Action) == "" && !oneOf(c.Original.Action, "order.submit", "order.cancel") {
		return "", "unsupported_receipt_action", nil
	}
	var publication string
	var targetRaw []byte
	if queryResultType(c.Original.Action) != "" {
		err = tx.QueryRowContext(ctx, `SELECT state,target_json FROM futures_test_query_publications WHERE command_id=$1`, commandID).Scan(&publication, &targetRaw)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT state,target_json->'target' FROM futures_test_trade_publications WHERE command_id=$1`, commandID).Scan(&publication, &targetRaw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", "publication_not_confirmed", nil
	}
	if err != nil {
		return "", "", err
	}
	if publication != "published" {
		return "", "publication_not_confirmed", nil
	}
	target, err := DecodeTestQueryTarget(targetRaw)
	if err != nil {
		return "", "", err
	}
	if target != p.target {
		return "", "foreign_publication_target", nil
	}
	// All response writes for this physical stream share the receiver lock.
	// Existing reviewed batches retain their independent ownership and evidence.
	var reviewed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_query_commands WHERE command_id=$1)`, commandID).Scan(&reviewed); err != nil {
		return "", "", err
	}
	if reviewed {
		return "", "independent_query_batch_owned", nil
	}
	previous, count, size, err := readReplyResult(ctx, tx, commandID, target)
	if err != nil {
		return "", "", err
	}
	pages, err := replyResultInputs(ctx, tx, h.stream, commandID, count, size)
	if err != nil {
		return "", "", err
	}
	if previous != nil {
		derived := assembleReplyResult(c, target, pages)
		a, _ := json.Marshal(previous)
		b, _ := json.Marshal(derived)
		if !bytes.Equal(a, b) {
			return "", "", ErrStreamIdentity
		}
	}
	if count >= 256 || len(row.Raw) > (8<<20)-size {
		return "", "", ErrAuditCapacity
	}
	pages = append(pages, row.Raw)
	result := assembleReplyResult(c, target, pages)
	raw, err := json.Marshal(result)
	if err != nil {
		return "", "", err
	}
	if len(raw) > 8<<20 {
		return "", "", ErrAuditCapacity
	}
	sum := sha256.Sum256(raw)
	_, err = tx.ExecContext(ctx, `INSERT INTO futures_reply_results(command_id,target_json,result,result_sha256,input_count,input_bytes)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(command_id) DO UPDATE SET result=excluded.result,result_sha256=excluded.result_sha256,input_count=excluded.input_count,input_bytes=excluded.input_bytes,updated_at=clock_timestamp()`, commandID, targetRaw, raw, sum[:], count+1, size+len(row.Raw))
	if err != nil {
		return "", "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE futures_stream_inbox SET state='delivered',reply_command_id=$2,delivered_at=clock_timestamp() WHERE receipt_id=$1`, id, commandID)
	if err != nil {
		return "", "", err
	}
	if err := setCommandPending(ctx, tx, commandID, pendingReplyResult(result)); err != nil {
		return "", "", err
	}
	return result.State, "", tx.Commit()
}

func readReplyResult(ctx context.Context, tx *sql.Tx, id int64, target TestQueryTarget) (*ReplyResult, int, int, error) {
	var raw, digest, binding []byte
	var count, size int
	err := tx.QueryRowContext(ctx, `SELECT result,result_sha256,target_json,input_count,input_bytes FROM futures_reply_results WHERE command_id=$1`, id).Scan(&raw, &digest, &binding, &count, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, err
	}
	actual, err := DecodeTestQueryTarget(binding)
	if err != nil || actual != target {
		return nil, 0, 0, ErrStreamIdentity
	}
	sum := sha256.Sum256(raw)
	var result ReplyResult
	if count < 1 || count > 256 || size < 1 || size > 8<<20 || len(raw) > 8<<20 || !bytes.Equal(sum[:], digest) || json.Unmarshal(raw, &result) != nil || result.CommandID != id || result.NativeAuditVerified || result.OrderStateChanged || result.RetrySafe || !oneOf(result.State, "collecting", "ready", "failed", "invalid", "accepted") {
		return nil, 0, 0, ErrStreamIdentity
	}
	return &result, count, size, nil
}

func replyResultInputs(ctx context.Context, tx *sql.Tx, stream string, id int64, count, size int) ([][]byte, error) {
	rows, err := tx.QueryContext(ctx, `SELECT body,body_sha256 FROM futures_stream_inbox WHERE reply_command_id=$1 AND stream_key=$2 AND state='delivered' ORDER BY receipt_id LIMIT 257`, id, stream)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := [][]byte{}
	total := 0
	for rows.Next() {
		var raw, digest []byte
		if err = rows.Scan(&raw, &digest); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		total += len(raw)
		if len(pages) >= 256 || total > 8<<20 || !bytes.Equal(sum[:], digest) {
			return nil, ErrStreamIdentity
		}
		pages = append(pages, raw)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(pages) != count || total != size {
		return nil, ErrStreamIdentity
	}
	return pages, nil
}
