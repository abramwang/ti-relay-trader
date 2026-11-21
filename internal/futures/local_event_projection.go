package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrLocalProjectionConflict = errors.New("local futures event conflict requires reconciliation")

// LocalEventProjector only consumes retained events for a caller-selected TEST
// session and confirmed local publications. It never learns authority from the
// input, repairs an unknown publication, or claims a full account snapshot.
type LocalEventProjector struct {
	store  *StreamStore
	scope  CommandScope
	target TestQueryTarget
	key    string
	after  int64
}

type LocalProjectionPage struct {
	Scanned       int             `json:"scanned"`
	Applied       int             `json:"applied"`
	Conflicts     int             `json:"conflicts"`
	Deferred      []DispatchIssue `json:"deferred"`
	CycleComplete bool            `json:"cycle_complete"`
}

func NewLocalEventProjector(ctx context.Context, store *StreamStore, scope CommandScope, target TestQueryTarget) (*LocalEventProjector, error) {
	if store == nil || scope.validate() != nil || scope.Route.Env != "test" || target.Validate() != nil {
		return nil, ErrStreamIdentity
	}
	events, _ := NewEventStore(store.db)
	ts := TradeScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, target.TradeDate}
	key, err := events.RegisterScope(ctx, ts, scope.UserID, eventStreamForRoute(scope.Route))
	if err != nil {
		return nil, err
	}
	return &LocalEventProjector{store: store, scope: scope, target: target, key: key}, nil
}

func eventStreamForRoute(route QueryRoute) string {
	key := queryReplyStream(route)
	return key[:len(key)-len("reply")] + "event"
}

// Each handoff and business application commits together. On process death or
// transaction failure the original raw inbox remains pending for the next scan.
// Deferred records cannot starve later rows; every bounded cycle starts over.
func (p *LocalEventProjector) Step(ctx context.Context, h StreamHandle, limit int) (LocalProjectionPage, error) {
	out := LocalProjectionPage{Deferred: []DispatchIssue{}}
	if h.config.Role != "event" || h.config.Scope != p.scope || h.config.SourceID != p.target.SourceID {
		return out, ErrStreamIdentity
	}
	var conflict, dirty bool
	var report, digest []byte
	err := p.store.db.QueryRowContext(ctx, `SELECT CASE WHEN projection_version=2 THEN summary ELSE report END,
        CASE WHEN projection_version=2 THEN summary_sha256 ELSE report_sha256 END,projection_dirty,
        EXISTS(SELECT 1 FROM futures_stream_inbox s JOIN futures_event_inbox e USING(event_id) WHERE s.stream_key=$1 AND s.state='delivered' AND e.scope_key=$2 AND e.state='conflict') FROM futures_trade_scopes WHERE scope_key=$2`, h.stream, p.key).Scan(&report, &digest, &dirty, &conflict)
	if err != nil {
		return out, err
	}
	if dirty {
		return out, ErrTradeCheckpoint
	}
	ledgerConflict, err := localReportConflict(report, digest)
	if err != nil {
		return out, err
	}
	if conflict || ledgerConflict {
		return out, ErrLocalProjectionConflict
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
		}
		if state == "applied" {
			out.Applied++
		}
		if state == "conflict" {
			out.Conflicts++
			break
		}
	}
	if len(rows) < limit && out.Scanned == len(rows) {
		p.after = 0
		out.CycleComplete = true
	}
	return out, nil
}

func (p *LocalEventProjector) apply(ctx context.Context, h StreamHandle, id int64) (string, string, error) {
	tx, err := beginFuturesTransaction(ctx, p.store.db)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if _, _, _, err := lockStream(ctx, tx, h); err != nil {
		return "", "", err
	}
	// Verify retained bytes and current transport binding under the owner lock.
	var same bool
	transport, _ := json.Marshal(struct{ SourceID, RunID, Epoch, Group string }{p.target.SourceID, p.target.ServerRunID, p.target.Epoch, p.target.Group})
	if err := tx.QueryRowContext(ctx, `SELECT transport_json=$2::jsonb FROM futures_test_reply_sources WHERE stream_key=$1`, h.stream, transport).Scan(&same); err != nil {
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
	registered, err := lockEventScope(ctx, tx, p.key)
	if err != nil {
		return "", "", err
	}
	reason, err := p.authorize(ctx, tx, registered, row.Raw)
	if err != nil || reason != "" {
		return "", reason, err
	}
	receipt, err := forwardEventTx(ctx, tx, h, id, p.key)
	if err != nil {
		return "", "", err
	}
	receipt, err = processEventTx(ctx, tx, receipt.EventID)
	if err != nil {
		return "", "", err
	}
	if !oneOf(receipt.State, "applied", "conflict") {
		return "", "business_identity_unresolved", nil // rollback the handoff too
	}
	var report, digest []byte
	var dirty bool
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN projection_version=2 THEN summary ELSE report END,
        CASE WHEN projection_version=2 THEN summary_sha256 ELSE report_sha256 END,projection_dirty FROM futures_trade_scopes WHERE scope_key=$1`, p.key).Scan(&report, &digest, &dirty); err != nil {
		return "", "", err
	}
	if dirty {
		return "", "", ErrTradeCheckpoint
	}
	conflict, err := localReportConflict(report, digest)
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	if conflict {
		return "conflict", "", nil
	}
	return receipt.State, "", nil
}

func localReportConflict(raw, digest []byte) (bool, error) {
	sum := sha256.Sum256(raw)
	var report struct{ Conflicts []string }
	if !bytes.Equal(sum[:], digest) || json.Unmarshal(raw, &report) != nil {
		return false, ErrStreamIdentity
	}
	return len(report.Conflicts) > 0, nil
}

func (p *LocalEventProjector) authorize(ctx context.Context, tx *sql.Tx, scope eventScope, raw []byte) (string, error) {
	if isCancelRejectionEvent(raw) {
		b, err := resolveCancelRejection(ctx, tx, scope, raw)
		if errors.Is(err, ErrCancelRejectionBinding) || errors.Is(err, ErrStreamIdentity) {
			return "unbound_cancel_attempt", nil
		}
		if err != nil {
			return "", err
		}
		if b.target != p.target {
			return "foreign_cancel_target", nil
		}
		return "", nil
	}
	event, err := DecodeTradeEvent(raw, EventContext{Scope: scope.scope})
	if err != nil {
		return "invalid_or_foreign_event", nil
	}
	session := ""
	if event.Order != nil {
		session = event.Order.CounterSessionID
	} else {
		session = event.Fill.CounterSessionID
	}
	if session != p.target.CounterSessionID {
		return "unbound_event_session", nil
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT command_id FROM futures_command_reservations WHERE route_key=$1 AND trade_date=$2 AND kind='gateway' AND identity=$3`, string(commandRouteBytes(p.scope.Route)), p.target.TradeDate, tradeEventGateway(event)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "unregistered_local_order", nil
	}
	if err != nil {
		return "", err
	}
	command, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(p.scope.Route)), id)
	if err != nil {
		return "", err
	}
	state, _, err := authorizeResolvedTrade(scope.scope, scope.userID, event, true, &command)
	if err != nil {
		return "", err
	}
	if state != "applied" {
		return "local_intent_mismatch", nil
	}
	var published, action string
	var binding []byte
	err = tx.QueryRowContext(ctx, `SELECT state,action,target_json FROM futures_test_trade_publications WHERE command_id=$1`, id).Scan(&published, &action, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return "publication_not_confirmed", nil
	}
	if err != nil {
		return "", err
	}
	var b struct {
		Target TestQueryTarget `json:"target"`
	}
	if json.Unmarshal(binding, &b) != nil || b.Target.Validate() != nil {
		return "", ErrStreamIdentity
	}
	if published != "published" || action != "order.submit" || command.Original.Action != action {
		return "publication_not_confirmed", nil
	}
	if b.Target != p.target {
		return "foreign_publication_target", nil
	}
	return "", nil
}
