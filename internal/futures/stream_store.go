package futures

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrStreamOwner    = errors.New("futures stream receiver retired")
	ErrStreamCursor   = errors.New("futures stream checkpoint changed")
	ErrStreamIdentity = errors.New("futures stream identity or target mismatch")
)

// SourceID is an operator-owned persistent Redis deployment identity, NOT a
// process run_id. A restored/recreated Stream must not silently reuse a cursor.
type StreamConfig struct {
	Scope          CommandScope
	Role, SourceID string
}

func (c StreamConfig) stream() (string, error) {
	if c.Scope.validate() != nil || !oneOf(c.Role, "event", "reply", "hb") || !validID(c.SourceID) || len(c.SourceID) > 128 {
		return "", ErrStreamIdentity
	}
	return strings.TrimSuffix(queryReplyStream(c.Scope.Route), "reply") + c.Role, nil
}

// Handle only fences local DB writes. It does not authenticate an OC process or
// authorize trading. Open must be called after explicitly stopping the old reader.
type StreamHandle struct {
	config        StreamConfig
	stream, owner string
}
type StreamStore struct{ db *sql.DB }
type StreamInput struct {
	EntryID string
	Raw     []byte
}
type StreamRecord struct {
	ID            int64
	EntryID       string
	Raw           []byte
	State, Reason string
	EventID       sql.NullInt64
	BatchID       sql.NullString
}
type StreamCapture struct {
	ReceiptIDs []int64
	Cursor     string
	Replayed   bool
}

type StreamStatus struct {
	Cursor                          string
	Pending, Quarantined, Delivered int
}

// Counts describe transport responsibility only. Delivered work can still be
// waiting_identity/invalid in the event or query store; this is not readiness.
func (s *StreamStore) Status(ctx context.Context, h StreamHandle) (StreamStatus, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return StreamStatus{}, err
	}
	defer tx.Rollback()
	cursor, count, size, err := lockStream(ctx, tx, h)
	if err != nil {
		return StreamStatus{}, err
	}
	out := StreamStatus{Cursor: cursor}
	var totalBytes int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='quarantined'),count(*) FILTER(WHERE state='delivered'),COALESCE(sum(octet_length(body)),0) FROM futures_stream_inbox WHERE stream_key=$1`, h.stream).Scan(&out.Pending, &out.Quarantined, &out.Delivered, &totalBytes)
	if err != nil {
		return StreamStatus{}, err
	}
	if out.Pending+out.Quarantined+out.Delivered != count || totalBytes != size {
		return StreamStatus{}, fmt.Errorf("stream inbox counters inconsistent")
	}
	if err = tx.Commit(); err != nil {
		return StreamStatus{}, err
	}
	return out, nil
}

func (s *StreamStore) Record(ctx context.Context, h StreamHandle, id int64) (StreamRecord, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return StreamRecord{}, err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return StreamRecord{}, err
	}
	r, err := readStreamRecord(ctx, tx, h, id)
	if err != nil {
		return StreamRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return StreamRecord{}, err
	}
	return r, nil
}

func NewStreamStore(db *sql.DB) (*StreamStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database required")
	}
	return &StreamStore{db}, nil
}

func streamID(id string) ([2]uint64, error) {
	var out [2]uint64
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		return out, ErrStreamCursor
	}
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil || strconv.FormatUint(v, 10) != p {
			return out, ErrStreamCursor
		}
		out[i] = v
	}
	return out, nil
}
func compareStreamID(a, b string) (int, error) {
	x, err := streamID(a)
	if err != nil {
		return 0, err
	}
	y, err := streamID(b)
	if err != nil {
		return 0, err
	}
	for i := range x {
		if x[i] < y[i] {
			return -1, nil
		}
		if x[i] > y[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func (s *StreamStore) Open(ctx context.Context, c StreamConfig) (StreamHandle, error) {
	key, err := c.stream()
	if err != nil {
		return StreamHandle{}, err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return StreamHandle{}, err
	}
	h := StreamHandle{c, key, hex.EncodeToString(nonce[:])}
	raw, _ := json.Marshal(c)
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return StreamHandle{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO futures_stream_receivers(stream_key,config_json,owner_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, key, raw, h.owner); err != nil {
		return StreamHandle{}, err
	}
	var same bool
	if err = tx.QueryRowContext(ctx, `SELECT config_json=$2::jsonb FROM futures_stream_receivers WHERE stream_key=$1 FOR UPDATE`, key, raw).Scan(&same); err != nil {
		return StreamHandle{}, err
	}
	if !same {
		return StreamHandle{}, ErrStreamIdentity
	}
	if _, err = tx.ExecContext(ctx, `UPDATE futures_stream_receivers SET owner_id=$2 WHERE stream_key=$1`, key, h.owner); err != nil {
		return StreamHandle{}, err
	}
	if err = tx.Commit(); err != nil {
		return StreamHandle{}, err
	}
	return h, nil
}

func lockStream(ctx context.Context, tx *sql.Tx, h StreamHandle) (string, int, int, error) {
	key, err := h.config.stream()
	if err != nil || key != h.stream || h.owner == "" {
		return "", 0, 0, ErrStreamIdentity
	}
	raw, _ := json.Marshal(h.config)
	var owner, cursor string
	var count, size int
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT owner_id,cursor_id,audit_count,audit_bytes,config_json=$2::jsonb FROM futures_stream_receivers WHERE stream_key=$1 FOR UPDATE`, h.stream, raw).Scan(&owner, &cursor, &count, &size, &same)
	if err != nil {
		return "", 0, 0, err
	}
	if !same {
		return "", 0, 0, ErrStreamIdentity
	}
	if owner != h.owner {
		return "", 0, 0, ErrStreamOwner
	}
	if _, err = streamID(cursor); err != nil {
		return "", 0, 0, err
	}
	if err = tradeCapacity(count, size, 0, 0); err != nil {
		return "", 0, 0, err
	}
	return cursor, count, size, nil
}

func (s *StreamStore) Cursor(ctx context.Context, h StreamHandle) (string, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	c, _, _, err := lockStream(ctx, tx, h)
	if err != nil {
		return "", err
	}
	return c, tx.Commit()
}

// Capture is for one bounded, ordered XREAD result. The reader, not a message,
// supplies the trusted stream. Raw bytes, pending responsibility and cursor
// commit together. It cannot prove that an arbitrary caller omitted no entries.
func (s *StreamStore) Capture(ctx context.Context, h StreamHandle, expected string, inputs []StreamInput) (StreamCapture, error) {
	if len(inputs) == 0 || len(inputs) > 256 {
		return StreamCapture{}, ErrAuditCapacity
	}
	if _, err := streamID(expected); err != nil {
		return StreamCapture{}, err
	}
	previous := "0-0"
	bytesTotal := 0
	for _, in := range inputs {
		cmp, err := compareStreamID(in.EntryID, previous)
		if err != nil || cmp <= 0 {
			return StreamCapture{}, ErrStreamCursor
		}
		if len(in.Raw) > maxSnapshotBytes-bytesTotal {
			return StreamCapture{}, ErrAuditCapacity
		}
		bytesTotal += len(in.Raw)
		previous = in.EntryID
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return StreamCapture{}, err
	}
	defer tx.Rollback()
	cursor, count, size, err := lockStream(ctx, tx, h)
	if err != nil {
		return StreamCapture{}, err
	}
	if cursor != expected {
		return StreamCapture{}, ErrStreamCursor
	}
	out := StreamCapture{Cursor: cursor, Replayed: true, ReceiptIDs: []int64{}}
	for _, in := range inputs {
		raw := in.Raw
		if raw == nil {
			raw = []byte{}
		}
		sum := sha256.Sum256(raw)
		var id int64
		var old, digest []byte
		err = tx.QueryRowContext(ctx, `SELECT receipt_id,body,body_sha256 FROM futures_stream_inbox WHERE stream_key=$1 AND entry_id=$2`, h.stream, in.EntryID).Scan(&id, &old, &digest)
		if err == nil {
			cmp, _ := compareStreamID(in.EntryID, cursor)
			if cmp > 0 || !bytes.Equal(old, raw) || !bytes.Equal(digest, sum[:]) {
				return StreamCapture{}, ErrStreamIdentity
			}
			out.ReceiptIDs = append(out.ReceiptIDs, id)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return StreamCapture{}, err
		}
		cmp, _ := compareStreamID(in.EntryID, cursor)
		if cmp <= 0 {
			return StreamCapture{}, ErrStreamCursor
		}
		if err = tradeCapacity(count, size, 1, len(raw)); err != nil {
			return StreamCapture{}, err
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO futures_stream_inbox(stream_key,entry_id,body,body_sha256) VALUES($1,$2,$3,$4) RETURNING receipt_id`, h.stream, in.EntryID, raw, sum[:]).Scan(&id); err != nil {
			return StreamCapture{}, err
		}
		out.ReceiptIDs = append(out.ReceiptIDs, id)
		out.Cursor = in.EntryID
		out.Replayed = false
		count++
		size += len(raw)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE futures_stream_receivers SET cursor_id=$2,audit_count=$3,audit_bytes=$4 WHERE stream_key=$1`, h.stream, out.Cursor, count, size); err != nil {
		return StreamCapture{}, err
	}
	if err = tx.Commit(); err != nil {
		return StreamCapture{}, err
	}
	return out, nil
}

func readStreamRecord(ctx context.Context, tx *sql.Tx, h StreamHandle, id int64) (StreamRecord, error) {
	var r StreamRecord
	var digest []byte
	err := tx.QueryRowContext(ctx, `SELECT receipt_id,entry_id,body,body_sha256,state,reason,event_id,batch_id FROM futures_stream_inbox WHERE stream_key=$1 AND receipt_id=$2 FOR UPDATE`, h.stream, id).Scan(&r.ID, &r.EntryID, &r.Raw, &digest, &r.State, &r.Reason, &r.EventID, &r.BatchID)
	if err != nil {
		return r, err
	}
	sum := sha256.Sum256(r.Raw)
	if !bytes.Equal(sum[:], digest) {
		return r, fmt.Errorf("stream inbox checksum mismatch")
	}
	return r, nil
}

// Pending is a DB work cursor, not a Redis checkpoint. Start another sweep at
// zero after reaching the end so waiting identities do not disappear forever.
func (s *StreamStore) Pending(ctx context.Context, h StreamHandle, after int64, limit int) ([]StreamRecord, error) {
	return s.streamRecords(ctx, h, after, limit, true)
}

func (s *StreamStore) streamRecords(ctx context.Context, h StreamHandle, after int64, limit int, pendingOnly bool) ([]StreamRecord, error) {
	if after < 0 || limit < 1 || limit > 256 {
		return nil, ErrAuditCapacity
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT receipt_id FROM futures_stream_inbox WHERE stream_key=$1 AND (NOT $4 OR state='pending') AND receipt_id>$2 ORDER BY receipt_id LIMIT $3`, h.stream, after, limit, pendingOnly)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []StreamRecord{}
	for _, id := range ids {
		r, err := readStreamRecord(ctx, tx, h, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// Quarantine is explicit durable investigation responsibility, not resolution or
// readiness. Raw bytes stay queryable; there is no delete/unblock/replay shortcut.
func (s *StreamStore) Quarantine(ctx context.Context, h StreamHandle, id int64, reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return ErrStreamIdentity
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return err
	}
	r, err := readStreamRecord(ctx, tx, h, id)
	if err != nil {
		return err
	}
	if r.State == "delivered" || (r.State == "quarantined" && r.Reason != reason) {
		return ErrStreamIdentity
	}
	if _, err = tx.ExecContext(ctx, `UPDATE futures_stream_inbox SET state='quarantined',reason=$2 WHERE receipt_id=$1`, id, reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *StreamStore) ForwardEvent(ctx context.Context, h StreamHandle, id int64, target string) (EventReceipt, error) {
	if h.config.Role != "event" {
		return EventReceipt{}, ErrStreamIdentity
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return EventReceipt{}, err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return EventReceipt{}, err
	}
	r, err := readStreamRecord(ctx, tx, h, id)
	if err != nil {
		return EventReceipt{}, err
	}
	registered, err := lockEventScope(ctx, tx, target)
	if err != nil {
		return EventReceipt{}, err
	}
	if (CommandScope{registered.scope.Route, registered.scope.NativeBrokerID, registered.scope.InvestorID, registered.userID}) != h.config.Scope || registered.stream != h.stream || r.State == "quarantined" {
		return EventReceipt{}, ErrStreamIdentity
	}
	// Never infer a trading-day scope from malformed or foreign JSON. Bad bodies
	// remain pending here for explicit quarantine, without poisoning another day.
	if _, err = DecodeTradeEvent(r.Raw, EventContext{Scope: registered.scope}); err != nil {
		return EventReceipt{}, err
	}
	out, err := receiveEventTx(ctx, tx, target, QuerySource{h.stream, r.EntryID}, r.Raw)
	if err != nil {
		return EventReceipt{}, err
	}
	if r.State == "delivered" && (!r.EventID.Valid || r.EventID.Int64 != out.EventID) {
		return EventReceipt{}, ErrStreamIdentity
	}
	if _, err = tx.ExecContext(ctx, `UPDATE futures_stream_inbox SET state='delivered',event_id=$2,delivered_at=clock_timestamp() WHERE receipt_id=$1`, id, out.EventID); err != nil {
		return EventReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return EventReceipt{}, err
	}
	return out, nil
}

func (s *StreamStore) ForwardQuery(ctx context.Context, h StreamHandle, id int64, q *QueryStore, target string, elapsed time.Duration) (IngestResult, error) {
	if h.config.Role != "reply" || q == nil || q.db != s.db {
		return IngestResult{}, ErrStreamIdentity
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return IngestResult{}, err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return IngestResult{}, err
	}
	r, err := readStreamRecord(ctx, tx, h, id)
	if err != nil {
		return IngestResult{}, err
	}
	b, err := q.load(ctx, tx, target)
	if err != nil {
		return IngestResult{}, err
	}
	if b.owner != q.owner || b.retired {
		return IngestResult{}, ErrBatchOwner
	}
	c := b.batch.context
	obj, err := parseReply(r.Raw)
	if err != nil {
		return IngestResult{}, err
	}
	if c.Command == nil || c.commandScope() != h.config.Scope || textField(obj, "origin_message_id") != c.OriginMessageID || r.State == "quarantined" || (r.State == "delivered" && (!r.BatchID.Valid || r.BatchID.String != target)) {
		return IngestResult{}, ErrStreamIdentity
	}
	out, err := q.ingestInputTx(ctx, tx, b, QueryInput{QuerySource{h.stream, r.EntryID}, r.Raw, elapsed})
	if err != nil {
		return IngestResult{}, err
	}
	if out.Disposition == "foreign" {
		return IngestResult{}, ErrStreamIdentity
	}
	out.Published, err = q.save(ctx, tx, b)
	if err != nil {
		return IngestResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE futures_stream_inbox SET state='delivered',batch_id=$2,delivered_at=clock_timestamp() WHERE receipt_id=$1`, id, target); err != nil {
		return IngestResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return IngestResult{}, err
	}
	return out, nil
}
