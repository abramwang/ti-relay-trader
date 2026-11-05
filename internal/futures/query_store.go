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
	"time"
)

var (
	ErrBatchIdentity = errors.New("query batch identity conflict")
	ErrBatchOwner    = errors.New("query belongs to another receiver process; recover explicitly")
	ErrNoSnapshot    = errors.New("no eligible complete futures snapshot")
	ErrAuditCapacity = errors.New("query audit capacity exceeded; input not committed")
)

const maxAuditBytes = 32 * 1024 * 1024
const maxAuditCount = 1024

// QueryStore is isolated from live ingestion. Create once per receiver lifetime;
// its random owner fence must not be reused after a process restart. The DB pool
// is caller-owned. No migrations, Redis ACK or broker queries run here.
type QueryStore struct {
	db    *sql.DB
	owner string
}

func NewQueryStore(db *sql.DB) (*QueryStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database required")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &QueryStore{db: db, owner: hex.EncodeToString(nonce[:])}, nil
}

func queryScope(c QueryContext) string {
	raw, _ := json.Marshal(struct {
		Route  QueryRoute
		Action string
	}{c.Route, c.Action})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (s *QueryStore) transaction(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	// Do not inherit asynchronous commit settings when acknowledging durable input.
	if _, err := tx.ExecContext(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Register creates an immutable request and makes it the next publication target.
// Registration order, not callback arrival time, fences obsolete results. Retrying
// an existing registration is idempotent and never reselects an older target.
func (s *QueryStore) Register(ctx context.Context, id string, c QueryContext, timeout time.Duration, limits BatchLimits) error {
	if !validID(id) || len(id) > 256 {
		return ErrBatchIdentity
	}
	if _, err := NewQueryBatch(c, 0, timeout, limits); err != nil {
		return err
	}
	contextJSON, _ := json.Marshal(c)
	limitsJSON, _ := json.Marshal(limits)
	if len(contextJSON) > 64*1024 {
		return fmt.Errorf("query context exceeds local storage limit")
	}
	scope := queryScope(c)
	tx, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_query_heads(scope_key) VALUES($1) ON CONFLICT DO NOTHING`, scope); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scope_key FROM futures_query_heads WHERE scope_key=$1 FOR UPDATE`, scope); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO futures_query_batches
        (batch_id,scope_key,origin_message_id,owner_id,context_json,limits_json,timeout_ns,state)
        VALUES($1,$2,$3,$4,$5,$6,$7,'collecting') ON CONFLICT DO NOTHING`, id, scope, c.OriginMessageID, s.owner, contextJSON, limitsJSON, int64(timeout))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var same bool
		err = tx.QueryRowContext(ctx, `SELECT scope_key=$2 AND owner_id=$3 AND NOT retired AND context_json=$4::jsonb AND limits_json=$5::jsonb AND timeout_ns=$6
            FROM futures_query_batches WHERE batch_id=$1`, id, scope, s.owner, contextJSON, limitsJSON, int64(timeout)).Scan(&same)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !same) {
			return ErrBatchIdentity
		}
		if err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE futures_query_heads SET target_batch=$2 WHERE scope_key=$1`, scope, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type storedBatch struct {
	id, scope, owner       string
	retired                bool
	batch                  *QueryBatch
	auditCount, auditBytes int
}

// All writers lock scope then batch, including registration and recovery.
func (s *QueryStore) load(ctx context.Context, tx *sql.Tx, id string) (*storedBatch, error) {
	r := &storedBatch{id: id}
	if err := tx.QueryRowContext(ctx, `SELECT scope_key FROM futures_query_batches WHERE batch_id=$1`, id).Scan(&r.scope); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scope_key FROM futures_query_heads WHERE scope_key=$1 FOR UPDATE`, r.scope); err != nil {
		return nil, err
	}
	var contextJSON, limitsJSON []byte
	var timeout, elapsed int64
	var state BatchState
	var reason string
	err := tx.QueryRowContext(ctx, `SELECT owner_id,context_json,limits_json,timeout_ns,elapsed_ns,state,reason,audit_count,audit_bytes,retired
        FROM futures_query_batches WHERE batch_id=$1 FOR UPDATE`, id).Scan(&r.owner, &contextJSON, &limitsJSON, &timeout, &elapsed, &state, &reason, &r.auditCount, &r.auditBytes, &r.retired)
	if err != nil {
		return nil, err
	}
	var c QueryContext
	var limits BatchLimits
	if err := json.Unmarshal(contextJSON, &c); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(limitsJSON, &limits); err != nil {
		return nil, err
	}
	r.batch, err = NewQueryBatch(c, 0, time.Duration(timeout), limits)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT body,body_sha256,elapsed_ns,disposition FROM futures_query_observations WHERE batch_id=$1 ORDER BY observation_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count, size := 0, 0
	for rows.Next() {
		var raw, digest []byte
		var at int64
		var disposition string
		if err := rows.Scan(&raw, &digest, &at, &disposition); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], digest) {
			return nil, fmt.Errorf("stored query evidence checksum mismatch")
		}
		count++
		size += len(raw)
		if count > maxAuditCount || size > maxAuditBytes {
			return nil, ErrAuditCapacity
		}
		_, _ = r.batch.Accept(raw, time.Duration(at))
		if disposition == "source_conflict" {
			r.batch.invalidate(fmt.Errorf("stream entry identity changed bytes"))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count != r.auditCount || size != r.auditBytes {
		return nil, fmt.Errorf("stored query evidence count mismatch")
	}
	// Lifecycle transitions also persist when no incoming page exists.
	switch state {
	case BatchInterrupted:
		r.batch.Interrupt(reason)
	case BatchInvalid:
		r.batch.invalidate(errors.New(reason))
	}
	_ = r.batch.Advance(time.Duration(elapsed))
	if r.batch.state != state {
		return nil, fmt.Errorf("stored query state does not match evidence")
	}
	return r, nil
}

type QuerySource struct{ StreamKey, EntryID string }

// IngestResult is returned only after commit. Disposition may be rejected or
// foreign: durability is not business success. Redis dispatch/checkpoints and
// ACK policy remain the caller's responsibility; errors never authorize ACK.
type IngestResult struct {
	State       BatchState
	Disposition string
	Replayed    bool
	Published   bool // Selected as current by this transaction; not a new-publication event.
}

func (s *QueryStore) Ingest(ctx context.Context, id string, source QuerySource, raw []byte, elapsed time.Duration) (IngestResult, error) {
	if !validID(source.StreamKey) || !validID(source.EntryID) || len(source.StreamKey) > 512 || len(source.EntryID) > 128 || elapsed < 0 {
		return IngestResult{}, fmt.Errorf("invalid input source or elapsed time")
	}
	if len(raw) > maxAuditBytes {
		return IngestResult{}, ErrAuditCapacity
	}
	if raw == nil {
		raw = []byte{}
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return IngestResult{}, err
	}
	defer tx.Rollback()
	r, err := s.load(ctx, tx, id)
	if err != nil {
		return IngestResult{}, err
	}
	if r.owner != s.owner || r.retired {
		return IngestResult{}, ErrBatchOwner
	}
	var prior []byte
	err = tx.QueryRowContext(ctx, `SELECT body FROM futures_query_observations
        WHERE batch_id=$1 AND stream_key=$2 AND entry_id=$3 ORDER BY observation_id LIMIT 1`, id, source.StreamKey, source.EntryID).Scan(&prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IngestResult{}, err
	}
	hasPrior := err == nil
	digest := sha256.Sum256(raw)
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_query_observations
        WHERE batch_id=$1 AND stream_key=$2 AND entry_id=$3 AND body_sha256=$4 AND body=$5)`, id, source.StreamKey, source.EntryID, digest[:], raw).Scan(&exists); err != nil {
		return IngestResult{}, err
	}
	out := IngestResult{State: r.batch.state, Replayed: exists}
	if exists {
		// A commit response may have been lost. Never insert or reapply the entry.
		out.Disposition = "replayed"
		_ = r.batch.Advance(elapsed)
	} else {
		if r.auditCount >= maxAuditCount || len(raw) > maxAuditBytes-r.auditBytes {
			return IngestResult{}, ErrAuditCapacity
		}
		replay, acceptErr := r.batch.Accept(raw, elapsed)
		out.Replayed, out.Disposition = replay, "accepted"
		if replay {
			out.Disposition = "replayed"
		}
		if acceptErr != nil {
			out.Disposition = "rejected"
			if errors.Is(acceptErr, ErrForeignQuery) {
				out.Disposition = "foreign"
			}
		}
		if hasPrior && !bytes.Equal(prior, raw) {
			r.batch.invalidate(fmt.Errorf("stream entry identity changed bytes"))
			out.Disposition = "source_conflict"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO futures_query_observations
            (batch_id,stream_key,entry_id,body_sha256,body,elapsed_ns,disposition) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			id, source.StreamKey, source.EntryID, digest[:], raw, int64(elapsed), out.Disposition)
		if err != nil {
			return IngestResult{}, err
		}
		r.auditCount++
		r.auditBytes += len(raw)
	}
	out.State = r.batch.state
	out.Published, err = s.save(ctx, tx, r)
	if err != nil {
		return IngestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IngestResult{}, err
	}
	return out, nil
}

func (s *QueryStore) save(ctx context.Context, tx *sql.Tx, r *storedBatch) (bool, error) {
	_, err := tx.ExecContext(ctx, `UPDATE futures_query_batches SET state=$2,reason=$3,elapsed_ns=$4,audit_count=$5,audit_bytes=$6,updated_at=clock_timestamp() WHERE batch_id=$1`,
		r.id, r.batch.state, r.batch.reason, int64(r.batch.lastNow), r.auditCount, r.auditBytes)
	if err != nil {
		return false, err
	}
	if r.batch.state != BatchReady {
		return false, nil
	}
	candidate, err := r.batch.Candidate()
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(raw)
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_query_snapshots(batch_id,candidate,candidate_sha256) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, r.id, raw, digest[:]); err != nil {
		return false, err
	}
	var saved, savedDigest []byte
	if err := tx.QueryRowContext(ctx, `SELECT candidate,candidate_sha256 FROM futures_query_snapshots WHERE batch_id=$1`, r.id).Scan(&saved, &savedDigest); err != nil {
		return false, err
	}
	if !bytes.Equal(saved, raw) || !bytes.Equal(savedDigest, digest[:]) {
		return false, fmt.Errorf("immutable snapshot conflict")
	}
	result, err := tx.ExecContext(ctx, `UPDATE futures_query_heads SET current_batch=$2 WHERE scope_key=$1 AND target_batch=$2`, r.scope, r.id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// Advance expires stalled queries without inventing an incoming reply.
func (s *QueryStore) Advance(ctx context.Context, id string, elapsed time.Duration) (BatchState, error) {
	return s.lifecycle(ctx, id, elapsed, "", false)
}

// Interrupt is explicit disconnect/cancellation evidence, not a broker rejection.
func (s *QueryStore) Interrupt(ctx context.Context, id, reason string) (BatchState, error) {
	if reason == "" {
		return "", fmt.Errorf("interruption reason required")
	}
	return s.lifecycle(ctx, id, 0, reason, false)
}

// Recover fences an old receiver's incomplete batch after the caller has stopped
// that receiver. Complete results remain historical; they are NOT republished.
// No pending SDK request or partial page set resumes across a receiver restart.
func (s *QueryStore) Recover(ctx context.Context, id string) (BatchState, error) {
	return s.lifecycle(ctx, id, 0, "receiver_restart", true)
}

func (s *QueryStore) lifecycle(ctx context.Context, id string, elapsed time.Duration, reason string, recover bool) (BatchState, error) {
	if elapsed < 0 {
		return "", fmt.Errorf("invalid elapsed time")
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	r, err := s.load(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if recover {
		if r.owner == s.owner {
			return "", fmt.Errorf("cannot recover own active receiver")
		}
		if r.batch.state == BatchCollecting {
			r.batch.Interrupt(reason)
		}
		// Never call save for ready history: that could move the publication head.
		_, err = tx.ExecContext(ctx, `UPDATE futures_query_batches SET state=$2,reason=$3,retired=TRUE,updated_at=clock_timestamp() WHERE batch_id=$1`, id, r.batch.state, r.batch.reason)
	} else {
		if r.owner != s.owner || r.retired {
			return "", ErrBatchOwner
		}
		if reason != "" {
			r.batch.Interrupt(reason)
		} else {
			_ = r.batch.Advance(elapsed)
		}
		_, err = s.save(ctx, tx, r)
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return r.batch.state, nil
}

type StoredSnapshot struct {
	BatchID   string
	Candidate SnapshotCandidate
}

// Snapshot reads verified complete history. It never proves current readiness.
// Invalidated batches retain their raw data but cannot masquerade as snapshots.
func (s *QueryStore) Snapshot(ctx context.Context, id string) (StoredSnapshot, error) {
	var raw, digest []byte
	err := s.db.QueryRowContext(ctx, `SELECT s.candidate,s.candidate_sha256 FROM futures_query_snapshots s
        JOIN futures_query_batches b USING(batch_id) WHERE b.batch_id=$1 AND b.state='ready'`, id).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSnapshot{}, ErrNoSnapshot
	}
	if err != nil {
		return StoredSnapshot{}, err
	}
	return decodeStoredSnapshot(id, raw, digest)
}

func decodeStoredSnapshot(id string, raw, digest []byte) (StoredSnapshot, error) {
	sum := sha256.Sum256(raw)
	if !bytes.Equal(sum[:], digest) {
		return StoredSnapshot{}, fmt.Errorf("snapshot checksum mismatch")
	}
	var c SnapshotCandidate
	if err := json.Unmarshal(raw, &c); err != nil {
		return StoredSnapshot{}, err
	}
	return StoredSnapshot{BatchID: id, Candidate: c}, nil
}

// Current requires explicit expected TradingDay, native broker and live session
// context. A newer failed refresh retains the old valid same-session snapshot;
// a new session/day/receiver cannot mistake that history for current data.
func (s *QueryStore) Current(ctx context.Context, expected QueryContext) (StoredSnapshot, error) {
	if _, err := NewQueryBatch(expected, 0, time.Minute, DefaultBatchLimits()); err != nil {
		return StoredSnapshot{}, err
	}
	var id string
	var raw, digest, targetJSON []byte
	err := s.db.QueryRowContext(ctx, `SELECT b.batch_id,s.candidate,s.candidate_sha256,target.context_json FROM futures_query_heads h
        JOIN futures_query_batches b ON b.batch_id=h.current_batch
        JOIN futures_query_batches target ON target.batch_id=h.target_batch
        JOIN futures_query_snapshots s ON s.batch_id=b.batch_id
        WHERE h.scope_key=$1 AND b.state='ready' AND b.owner_id=$2 AND target.owner_id=$2 AND NOT b.retired AND NOT target.retired`, queryScope(expected), s.owner).Scan(&id, &raw, &digest, &targetJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSnapshot{}, ErrNoSnapshot
	}
	if err != nil {
		return StoredSnapshot{}, err
	}
	result, err := decodeStoredSnapshot(id, raw, digest)
	if err != nil {
		return StoredSnapshot{}, err
	}
	c := result.Candidate.Context
	var target QueryContext
	if err := json.Unmarshal(targetJSON, &target); err != nil {
		return StoredSnapshot{}, err
	}
	for _, actual := range []QueryContext{c, target} {
		if actual.TradeDate != expected.TradeDate || actual.CounterSessionID != expected.CounterSessionID ||
			!sameInteger(actual.ConnectionGeneration, expected.ConnectionGeneration) || actual.NativeBrokerID != expected.NativeBrokerID {
			return StoredSnapshot{}, ErrNoSnapshot
		}
	}
	return result, nil
}
