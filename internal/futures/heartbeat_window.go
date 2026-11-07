package futures

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Boundary must be explicitly reviewed with independent session evidence.
// It cannot be inferred from the newest ready message. ReviewID is a reference,
// not authentication; neither this API nor stored observations authorize trades.
type HeartbeatBoundary struct {
	Cursor    string
	ReceiptID int64
	ReviewID  string
}

type TestHeartbeatWindow struct {
	mu        sync.Mutex
	store     *StreamStore
	source    *TestStreamSource
	handle    StreamHandle
	guard     *TestSessionGuard
	id        string
	startedAt time.Time
	deadline  time.Time
	binding   []byte
	boundary  HeartbeatBoundary
	after     int64
	failure   error
	running   bool
}

func NewTestHeartbeatWindow(ctx context.Context, store *StreamStore, source *TestStreamSource, handle StreamHandle, expected DeliveryQualityContext, policy TestHeartbeatPolicy, boundary HeartbeatBoundary) (*TestHeartbeatWindow, error) {
	deadline, ok := ctx.Deadline()
	if store == nil || !source.matches(handle) || handle.config.Role != "hb" || boundary.ReceiptID < 0 || !validID(boundary.ReviewID) || len(boundary.ReviewID) > 128 {
		return nil, ErrStreamIdentity
	}
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Minute {
		return nil, ErrStreamIdentity
	}
	guard, err := NewTestSessionGuard(source, expected, policy)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	tx, err := beginFuturesTransaction(ctx, store.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	cursor, _, _, err := lockStream(ctx, tx, handle)
	if err != nil {
		return nil, err
	}
	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(receipt_id),0) FROM futures_stream_inbox WHERE stream_key=$1`, handle.stream).Scan(&last); err != nil {
		return nil, err
	}
	if cursor != boundary.Cursor || last != boundary.ReceiptID {
		return nil, ErrStreamCursor
	}
	var count, owned int
	if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE state='observing' AND receiver_owner=$2) FROM futures_heartbeat_windows WHERE stream_key=$1`, handle.stream, handle.owner).Scan(&count, &owned); err != nil {
		return nil, err
	}
	if owned != 0 || count >= 128 {
		return nil, ErrAuditCapacity
	}
	// Only an explicit receiver takeover can retire a crashed owner's window.
	if _, err := tx.ExecContext(ctx, `UPDATE futures_heartbeat_windows SET state='stopped',reason='receiver_replaced',stopped_at=clock_timestamp() WHERE stream_key=$1 AND state='observing'`, handle.stream); err != nil {
		return nil, err
	}
	w := &TestHeartbeatWindow{store: store, source: source, handle: handle, guard: guard, id: hex.EncodeToString(nonce[:]), startedAt: time.Now(), deadline: deadline, boundary: boundary, after: last}
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	raw, _ := json.Marshal(struct {
		Expected          DeliveryQualityContext
		Policy            TestHeartbeatPolicy
		Boundary          HeartbeatBoundary
		Started, Deadline time.Time
	}{expected, policy, boundary, w.startedAt.In(zone), deadline.In(zone)})
	w.binding = raw
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_heartbeat_windows(window_id,stream_key,receiver_owner,context_json,review_id,boundary_cursor,boundary_receipt,after_receipt,started_at,state) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$8,'observing')`, w.id, handle.stream, handle.owner, raw, boundary.ReviewID, cursor, last, w.startedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *TestHeartbeatWindow) fail(err error) error {
	if w.failure == nil {
		w.failure = err
	}
	w.guard.Revoke()
	return w.failure
}

// Poll captures raw input before observing it. A true result means this bounded
// read found no further backlog, not that Redis can no longer receive new data.
func (w *TestHeartbeatWindow) Poll(ctx context.Context, limit int) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return false, w.failure
	}
	capture, err := w.source.ReadOnce(ctx, w.store, w.handle, limit)
	if err != nil {
		return false, w.fail(err)
	}
	caughtUp, err := w.observe(ctx, limit)
	if err != nil {
		return false, w.fail(err)
	}
	return caughtUp && len(capture.ReceiptIDs) < limit, nil
}

// The caller owns w.mu. Evaluate a private candidate and install only after
// commit. Never hold the guard lock over DB I/O: expiry must cancel a slow DB.
func (w *TestHeartbeatWindow) observe(ctx context.Context, limit int) (caughtUp bool, result error) {
	g := w.guard
	defer func() {
		if result != nil {
			g.mu.Lock()
			g.stop("heartbeat_observation_failed")
			g.mu.Unlock()
		}
	}()
	if limit < 1 || limit > 256 {
		return false, ErrAuditCapacity
	}
	tx, err := beginFuturesTransaction(ctx, w.store.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, _, _, err := lockStream(ctx, tx, w.handle); err != nil {
		return false, err
	}
	var after int64
	var state, owner string
	var same bool
	if err := tx.QueryRowContext(ctx, `SELECT after_receipt,state,receiver_owner,context_json=$2::jsonb AND stream_key=$3 AND started_at=$4 AND boundary_cursor=$5 AND boundary_receipt=$6 AND review_id=$7 FROM futures_heartbeat_windows WHERE window_id=$1 FOR UPDATE`, w.id, w.binding, w.handle.stream, w.startedAt, w.boundary.Cursor, w.boundary.ReceiptID, w.boundary.ReviewID).Scan(&after, &state, &owner, &same); err != nil {
		return false, err
	}
	if state != "observing" || owner != w.handle.owner || after != w.after || !same {
		return false, ErrStreamOwner
	}
	rows, err := tx.QueryContext(ctx, `SELECT receipt_id FROM futures_stream_inbox WHERE stream_key=$1 AND receipt_id>$2 ORDER BY receipt_id LIMIT $3`, w.handle.stream, after, limit)
	if err != nil {
		return false, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	g.mu.Lock()
	candidate := &TestSessionGuard{scope: g.scope, expected: g.expected, sourceID: g.sourceID, stream: g.stream, policy: g.policy,
		seen: make(map[string][32]byte, len(g.seen)), lastID: g.lastID, lastDigest: g.lastDigest, producedAt: g.producedAt,
		expiresAt: g.expiresAt, checkedAt: g.checkedAt, changed: make(chan struct{})}
	for id, hash := range g.seen {
		candidate.seen[id] = hash
	}
	if g.failure != nil {
		candidate.stop(g.failure.Error())
	}
	g.mu.Unlock()
	now := time.Now()
	if !now.Before(w.deadline) {
		candidate.stop("heartbeat_window_expired")
	}
	if err := candidate.checkAt(now); err != nil && !errors.Is(err, ErrTestSessionMissing) {
		result = err
	}
	for _, id := range ids {
		if result != nil {
			break
		}
		r, err := readStreamRecord(ctx, tx, w.handle, id)
		if err != nil {
			return false, err
		}
		hb, decodeErr := DecodeHeartbeat(r.Raw, g.scope.Route)
		if r.State != "pending" {
			result = candidate.stop("heartbeat_receipt_not_pending")
		} else if decodeErr == nil && !hb.ProducedAt.After(w.startedAt) {
			result = candidate.stop("heartbeat_precedes_window")
		} else {
			result = candidate.observeLocked(HeartbeatSource{g.sourceID, w.handle.stream, r.EntryID}, r.Raw, now)
		}
		outcome, reason := "accepted", ""
		if result != nil {
			outcome, reason = "rejected", result.Error()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO futures_heartbeat_observations(window_id,receipt_id,outcome,reason,observed_at) VALUES($1,$2,$3,$4,$5)`, w.id, id, outcome, reason, now); err != nil {
			return false, err
		}
		after = id
	}
	if result != nil {
		_, err = tx.ExecContext(ctx, `UPDATE futures_heartbeat_windows SET after_receipt=$2,state='stopped',reason=$3,stopped_at=clock_timestamp() WHERE window_id=$1`, w.id, after, result.Error())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE futures_heartbeat_windows SET after_receipt=$2 WHERE window_id=$1`, w.id, after)
	}
	if err != nil {
		return false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM futures_stream_inbox WHERE stream_key=$1 AND receipt_id>$2)`, w.handle.stream, after).Scan(&caughtUp); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	w.after = after
	g.mu.Lock()
	defer g.mu.Unlock()
	now = time.Now()
	if err := g.checkAt(now); err != nil && !errors.Is(err, ErrTestSessionMissing) {
		return false, err
	}
	if err := candidate.checkAt(now); err != nil && !errors.Is(err, ErrTestSessionMissing) {
		result = err
	}
	if g.failure == nil {
		close(g.changed)
	}
	g.seen, g.lastID, g.lastDigest = candidate.seen, candidate.lastID, candidate.lastDigest
	g.producedAt, g.expiresAt, g.checkedAt, g.failure = candidate.producedAt, candidate.expiresAt, candidate.checkedAt, candidate.failure
	if g.failure == nil {
		g.changed = make(chan struct{})
	}
	return caughtUp, result
}

func (w *TestHeartbeatWindow) Close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	reason := w.guard.Check()
	if reason == nil || errors.Is(reason, ErrTestSessionMissing) {
		reason = ErrTestSessionStopped
	}
	w.fail(reason)
	tx, err := beginFuturesTransaction(ctx, w.store.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, err := lockStream(ctx, tx, w.handle); err != nil {
		return err
	}
	var owner, stream string
	if err := tx.QueryRowContext(ctx, `SELECT receiver_owner,stream_key FROM futures_heartbeat_windows WHERE window_id=$1 FOR UPDATE`, w.id).Scan(&owner, &stream); err != nil {
		return err
	}
	if owner != w.handle.owner || stream != w.handle.stream {
		return ErrStreamIdentity
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_heartbeat_windows SET state='stopped',reason=$2,stopped_at=clock_timestamp() WHERE window_id=$1 AND state='observing'`, w.id, w.failure.Error()); err != nil {
		return err
	}
	return tx.Commit()
}

// RunHeartbeatWindow never starts OC or publishes commands. Raw heartbeat
// polling continues independently of event/reply processing; both are bounded
// by the supplied TEST deadline, and every worker is joined before returning.
func (c *TestConsumer) RunHeartbeatWindow(ctx context.Context, w *TestHeartbeatWindow, interval time.Duration, onCycle func(ConsumerCycle)) (result error) {
	deadline, ok := ctx.Deadline()
	if w == nil || c.source != w.source || c.event.store != w.store || c.expected != w.guard.expected ||
		!ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Minute || deadline.After(w.deadline) || interval < 10*time.Millisecond || interval > w.guard.policy.MaxAge/3 {
		return ErrStreamIdentity
	}
	w.mu.Lock()
	if w.running || w.failure != nil || !c.used.CompareAndSwap(false, true) {
		w.mu.Unlock()
		return fmt.Errorf("TEST consumer cannot be restarted")
	}
	w.running = true
	w.mu.Unlock()
	run, cancel := context.WithCancelCause(ctx)
	ready, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		signaled := false
		for run.Err() == nil {
			step, stop := context.WithTimeout(run, c.options.StepTimeout)
			drained, err := w.Poll(step, c.options.PageSize)
			stop()
			if err != nil {
				cancel(err)
				return
			}
			err = w.guard.Check()
			if err != nil && !errors.Is(err, ErrTestSessionMissing) {
				cancel(err)
				return
			}
			if drained && err == nil && !signaled {
				close(ready)
				signaled = true
			}
			if !drained {
				continue
			}
			timer := time.NewTimer(interval)
			select {
			case <-run.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}()
	defer func() {
		cancel(context.Canceled)
		<-done
		w.mu.Lock()
		failure := w.failure
		w.mu.Unlock()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		result = errors.Join(result, failure, w.Close(cleanup), c.source.Close())
	}()
	select {
	case <-ready:
	case <-run.Done():
	}
	return c.runGuarded(run, w.guard, onCycle, true)
}
