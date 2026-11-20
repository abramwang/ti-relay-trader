package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type DeliveryQualityContext struct {
	Scope                TradeScope
	CounterSessionID     string
	ConnectionGeneration int64
}

type SnapshotQuality struct {
	Action, BatchID, State, Reason string
	Usable                         bool
}

// Blocked=false means only that captured local evidence passed this check.
// It is NOT trading readiness: heartbeat freshness, Redis tail continuity,
// authenticated OC identity, permissions and dynamic limits are separate gates.
type DeliveryQuality struct {
	Blocked                                bool
	TransportPending, TransportQuarantined int
	EventsPending, EventsRejected          int
	LedgerMissing, LedgerBlocked           bool
	LedgerPending, InvalidAppliedQueries   int
	Snapshots                              []SnapshotQuality
}

// InspectDeliveryQuality takes one repeatable-read DB snapshot. It does not
// clear errors, repair evidence, query OC, refresh a clock or authorize writes.
func (s *StreamStore) InspectDeliveryQuality(ctx context.Context, event, reply StreamHandle, queries *QueryStore, expected DeliveryQualityContext) (DeliveryQuality, error) {
	out := DeliveryQuality{Blocked: true, Snapshots: []SnapshotQuality{}}
	if queries == nil || queries.db != s.db || event.config.Role != "event" || reply.config.Role != "reply" ||
		event.config.Scope != reply.config.Scope || event.config.SourceID != reply.config.SourceID ||
		expected.Scope.validate() != nil || !validID(expected.CounterSessionID) || len(expected.CounterSessionID) > 128 || expected.ConnectionGeneration < 1 ||
		(CommandScope{expected.Scope.Route, expected.Scope.NativeBrokerID, expected.Scope.InvestorID, event.config.Scope.UserID}) != event.config.Scope {
		return out, ErrStreamIdentity
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, h := range []StreamHandle{event, reply} {
		key, err := h.config.stream()
		if err != nil || key != h.stream || h.owner == "" {
			return out, ErrStreamIdentity
		}
		raw, _ := json.Marshal(h.config)
		var same bool
		var owner, cursor string
		var count, size, actual, actualBytes, pending, quarantined int
		if err := tx.QueryRowContext(ctx, `SELECT config_json=$2::jsonb,owner_id,cursor_id,audit_count,audit_bytes FROM futures_stream_receivers WHERE stream_key=$1`, key, raw).Scan(&same, &owner, &cursor, &count, &size); err != nil {
			return out, err
		}
		if !same {
			return out, ErrStreamIdentity
		}
		if owner != h.owner {
			return out, ErrStreamOwner
		}
		if _, err := streamID(cursor); err != nil {
			return out, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(octet_length(body)),0),count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='quarantined') FROM futures_stream_inbox WHERE stream_key=$1`, key).Scan(&actual, &actualBytes, &pending, &quarantined); err != nil {
			return out, err
		}
		if count < 0 || size < 0 || count != actual || size != actualBytes {
			return out, fmt.Errorf("stream audit counters inconsistent")
		}
		out.TransportPending += pending
		out.TransportQuarantined += quarantined
	}
	key := tradeScopeKey(expected.Scope)
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state IN ('pending','waiting_identity')),count(*) FILTER(WHERE state IN ('rejected','conflict')) FROM futures_event_inbox WHERE scope_key=$1`, key).Scan(&out.EventsPending, &out.EventsRejected); err != nil {
		return out, err
	}
	var raw, digest []byte
	err = tx.QueryRowContext(ctx, `SELECT report,report_sha256 FROM futures_trade_scopes WHERE scope_key=$1`, key).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		out.LedgerMissing = true
	} else if err != nil {
		return out, err
	} else {
		sum := sha256.Sum256(raw)
		var report TradeReport
		if !bytes.Equal(sum[:], digest) || json.Unmarshal(raw, &report) != nil || report.Scope != expected.Scope {
			return out, fmt.Errorf("trade projection integrity failure")
		}
		out.LedgerBlocked = report.Blocked || len(report.Conflicts) > 0
		out.LedgerPending = report.PendingCount
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM futures_query_trade_applications a JOIN futures_query_batches b USING(batch_id) WHERE a.scope_key=$1 AND b.state<>'ready'`, key).Scan(&out.InvalidAppliedQueries); err != nil {
		return out, err
	}
	allSnapshots := true
	for _, action := range []string{"account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"} {
		quality, err := inspectSnapshotQuality(ctx, tx, queries, event.config.Scope, expected, action)
		if err != nil {
			return out, err
		}
		out.Snapshots = append(out.Snapshots, quality)
		allSnapshots = allSnapshots && quality.Usable
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	out.Blocked = out.TransportPending > 0 || out.TransportQuarantined > 0 || out.EventsPending > 0 || out.EventsRejected > 0 ||
		out.LedgerMissing || out.LedgerBlocked || out.LedgerPending > 0 || out.InvalidAppliedQueries > 0 || !allSnapshots
	return out, nil
}

func inspectSnapshotQuality(ctx context.Context, tx *sql.Tx, queries *QueryStore, scope CommandScope, expected DeliveryQualityContext, action string) (SnapshotQuality, error) {
	out := SnapshotQuality{Action: action, Reason: "snapshot_missing"}
	var current, owner string
	var retired bool
	var contextJSON, raw, digest []byte
	err := tx.QueryRowContext(ctx, `SELECT b.batch_id,COALESCE(h.current_batch,''),b.owner_id,b.retired,b.state,b.context_json,s.candidate,s.candidate_sha256
        FROM futures_query_heads h JOIN futures_query_batches b ON b.batch_id=h.target_batch
        LEFT JOIN futures_query_snapshots s ON s.batch_id=b.batch_id WHERE h.scope_key=$1`, queryScope(QueryContext{Route: scope.Route, Action: action})).Scan(&out.BatchID, &current, &owner, &retired, &out.State, &contextJSON, &raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var c QueryContext
	if json.Unmarshal(contextJSON, &c) != nil || !validQueryCommandContext(c) {
		return out, fmt.Errorf("query context integrity failure")
	}
	if err := verifyQueryCommand(ctx, tx, out.BatchID, c); err != nil {
		return out, err
	}
	if retired || owner != queries.owner || c.commandScope() != scope || c.Action != action || c.TradeDate != expected.Scope.TradeDate ||
		c.CounterSessionID != expected.CounterSessionID || c.ConnectionGeneration == nil || *c.ConnectionGeneration != expected.ConnectionGeneration {
		out.Reason = "snapshot_context_not_current"
		return out, nil
	}
	if out.State != string(BatchReady) || current != out.BatchID || raw == nil {
		out.Reason = "latest_query_not_ready"
		return out, nil
	}
	snapshot, err := decodeStoredSnapshot(out.BatchID, raw, digest)
	if err != nil {
		return out, err
	}
	if !reflect.DeepEqual(snapshot.Candidate.Context, c) {
		return out, fmt.Errorf("snapshot context differs from registered query")
	}
	if isTradeQuery(action) {
		var applied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_query_trade_applications WHERE batch_id=$1 AND scope_key=$2)`, out.BatchID, tradeScopeKey(expected.Scope)).Scan(&applied); err != nil {
			return out, err
		}
		if !applied {
			out.Reason = "trade_query_unapplied"
			return out, nil
		}
	}
	out.Usable, out.Reason = true, ""
	return out, nil
}
