package futures

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// These bindings must come from verified lifecycle/request evidence, not from
// the message being dispatched. They neither authenticate OC nor enable orders.
type EventDispatchBinding struct{ ScopeKey, CounterSessionID string }
type QueryDispatchBinding struct {
	BatchID   string
	StartedAt time.Time // Original local request clock, never the reply's timestamp.
}

type eventDispatchTarget struct {
	key   string
	scope TradeScope
}

// StreamDispatcher owns an immutable allowlist. It does not open connections,
// learn sessions, register commands, process trade facts or start a goroutine.
type StreamDispatcher struct {
	store   *StreamStore
	handle  StreamHandle
	queries *QueryStore
	events  map[string]eventDispatchTarget
	replies map[string]QueryDispatchBinding
}

func NewStreamDispatcher(ctx context.Context, store *StreamStore, h StreamHandle, queries *QueryStore, events []EventDispatchBinding, replies []QueryDispatchBinding) (*StreamDispatcher, error) {
	if store == nil || !oneOf(h.config.Role, "event", "reply") || len(events)+len(replies) == 0 || len(events)+len(replies) > 128 ||
		(h.config.Role == "event" && len(replies) != 0) || (h.config.Role == "reply" && len(events) != 0) {
		return nil, ErrStreamIdentity
	}
	if len(replies) > 0 && (queries == nil || queries.db != store.db) {
		return nil, ErrStreamIdentity
	}
	tx, err := beginFuturesTransaction(ctx, store.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, _, err = lockStream(ctx, tx, h); err != nil {
		return nil, err
	}
	d := &StreamDispatcher{store, h, queries, map[string]eventDispatchTarget{}, map[string]QueryDispatchBinding{}}
	for _, binding := range events {
		if !validID(binding.CounterSessionID) || len(binding.CounterSessionID) > 128 {
			return nil, ErrStreamIdentity
		}
		s, err := lockEventScope(ctx, tx, binding.ScopeKey)
		if err != nil {
			return nil, err
		}
		if (CommandScope{s.scope.Route, s.scope.NativeBrokerID, s.scope.InvestorID, s.userID}) != h.config.Scope || s.stream != h.stream {
			return nil, ErrStreamIdentity
		}
		key := dispatchEventKey(s.scope.TradeDate, binding.CounterSessionID)
		if _, exists := d.events[key]; exists {
			return nil, ErrStreamIdentity
		}
		d.events[key] = eventDispatchTarget{binding.ScopeKey, s.scope}
	}
	now := time.Now()
	for _, binding := range replies {
		b, err := queries.load(ctx, tx, binding.BatchID)
		if err != nil {
			return nil, err
		}
		if b.retired || b.owner != queries.owner {
			return nil, ErrBatchOwner
		}
		c := b.batch.context
		if c.Command == nil || c.commandScope() != h.config.Scope || binding.StartedAt.IsZero() || binding.StartedAt.After(now) || now.Sub(binding.StartedAt) < b.batch.lastNow {
			return nil, ErrStreamIdentity
		}
		if _, exists := d.replies[c.OriginMessageID]; exists {
			return nil, ErrStreamIdentity
		}
		d.replies[c.OriginMessageID] = binding
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

func dispatchEventKey(day, session string) string {
	key, _ := json.Marshal([]string{day, session})
	return string(key)
}

type DispatchIssue struct {
	ReceiptID int64
	Reason    string
}
type DispatchPage struct {
	NextAfterID        int64
	Scanned, Delivered int
	CycleComplete      bool
	Deferred           []DispatchIssue
}

// Dispatch visits at most limit pending records. Deferred input remains durable
// and does not starve later entries. A complete cycle resets NextAfterID to zero;
// the caller must back off between cycles. Previously committed work survives an
// error; the failing record is not skipped by the returned scan cursor.
func (d *StreamDispatcher) Dispatch(ctx context.Context, afterID int64, limit int) (DispatchPage, error) {
	out := DispatchPage{NextAfterID: afterID, Deferred: []DispatchIssue{}}
	rows, err := d.store.Pending(ctx, d.handle, afterID, limit)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		reason, err := d.forward(ctx, row)
		if err != nil {
			return out, err
		}
		out.Scanned++
		out.NextAfterID = row.ID
		if reason != "" {
			out.Deferred = append(out.Deferred, DispatchIssue{row.ID, reason})
		} else {
			out.Delivered++
		}
	}
	if len(rows) < limit {
		out.NextAfterID, out.CycleComplete = 0, true
	}
	return out, nil
}

func (d *StreamDispatcher) forward(ctx context.Context, row StreamRecord) (string, error) {
	obj, err := parseReply(row.Raw)
	if err != nil {
		return "malformed_envelope", nil
	}
	route, err := shape(obj["routing"], []string{"env", "broker_id", "gateway_id", "account_id"}, nil)
	if err != nil {
		return "malformed_routing", nil
	}
	r := d.handle.config.Scope.Route
	if textField(route, "env") != r.Env || textField(route, "broker_id") != r.BrokerID || textField(route, "gateway_id") != r.GatewayID || textField(route, "account_id") != r.AccountID {
		return "foreign_routing", nil
	}
	if d.handle.config.Role == "event" {
		var payload rawObject
		if json.Unmarshal(obj["payload"], &payload) != nil || payload == nil {
			return "malformed_event_payload", nil
		}
		target, found := d.events[dispatchEventKey(textField(payload, "trade_date"), textField(payload, "counter_session_id"))]
		if !found {
			return "unbound_event_day_or_session", nil
		}
		if isCancelRejectionEvent(row.Raw) {
			_, err := d.store.ForwardEvent(ctx, d.handle, row.ID, target.key)
			if errors.Is(err, ErrCancelRejectionBinding) {
				return "unbound_cancel_command_or_publication", nil
			}
			if errors.Is(err, ErrStreamIdentity) {
				return "invalid_bound_cancel_rejection", nil
			}
			return "", err
		}
		if _, err := DecodeTradeEvent(row.Raw, EventContext{Scope: target.scope}); err != nil {
			return "invalid_bound_event", nil
		}
		_, err = d.store.ForwardEvent(ctx, d.handle, row.ID, target.key)
		return "", err
	}
	if d.handle.config.Role == "reply" {
		target, found := d.replies[textField(obj, "origin_message_id")]
		if !found {
			return "unbound_query_origin", nil
		}
		elapsed := time.Since(target.StartedAt)
		if elapsed < 0 {
			return "", fmt.Errorf("query request clock moved backwards")
		}
		_, err = d.store.ForwardQuery(ctx, d.handle, row.ID, d.queries, target.BatchID, elapsed)
		return "", err
	}
	return "", ErrStreamIdentity
}
