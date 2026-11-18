package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrCancelRejectionBinding = errors.New("cancel rejection command or publication is not yet bound")

func isCancelRejectionEvent(raw []byte) bool {
	o, err := parseReply(raw)
	return err == nil && textField(o, "event_type") == "order.cancel.event"
}

type boundCancelRejection struct {
	commandID int64
	target    TestQueryTarget
	rejection CancelRejection
}

// Resolve against the original registered command and its durable publication.
// Never obtain authorization/session/generation from the incoming event.
func resolveCancelRejection(ctx context.Context, tx *sql.Tx, scope eventScope, raw []byte) (boundCancelRejection, error) {
	var out boundCancelRejection
	o, err := parseReply(raw)
	if err != nil {
		return out, err
	}
	origin := textField(o, "origin_message_id")
	if !validID(origin) || len(origin) > 128 || scope.scope.Route.Env != "test" {
		return out, ErrStreamIdentity
	}
	key := string(commandRouteBytes(scope.scope.Route))
	err = tx.QueryRowContext(ctx, `SELECT command_id FROM futures_command_messages WHERE route_key=$1 AND message_id=$2`, key, origin).Scan(&out.commandID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrCancelRejectionBinding
	}
	if err != nil {
		return out, err
	}
	command, err := readRegisteredCommand(ctx, tx, key, out.commandID)
	if err != nil {
		return out, err
	}
	expectedScope := CommandScope{scope.scope.Route, scope.scope.NativeBrokerID, scope.scope.InvestorID, scope.userID}
	if command.Scope != expectedScope || command.Original.Action != "order.cancel" || command.Original.Cancel == nil || command.Original.MessageID != origin || command.Original.Cancel.TradeDate != scope.scope.TradeDate {
		return out, ErrStreamIdentity
	}
	var state, action string
	var binding []byte
	err = tx.QueryRowContext(ctx, `SELECT state,action,target_json FROM futures_test_trade_publications WHERE command_id=$1`, out.commandID).Scan(&state, &action, &binding)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state == "attempted") {
		return out, ErrCancelRejectionBinding
	}
	if err != nil {
		return out, err
	}
	if state != "published" || action != "order.cancel" {
		return out, ErrStreamIdentity
	}
	var b struct {
		Target TestQueryTarget `json:"target"`
	}
	if json.Unmarshal(binding, &b) != nil || b.Target.Validate() != nil || b.Target.TradeDate != scope.scope.TradeDate {
		return out, ErrStreamIdentity
	}
	out.target = b.Target
	out.rejection, err = DecodeCancelRejection(raw, CancelRejectionContext{expectedScope, command.Original.Raw, b.Target.CounterSessionID, b.Target.ConnectionGeneration})
	if err != nil {
		return out, fmt.Errorf("%w: invalid cancel rejection", ErrStreamIdentity)
	}
	return out, nil
}

func applyCancelRejection(ctx context.Context, tx *sql.Tx, scope eventScope, scopeKey string, eventID int64, raw []byte) (string, string, error) {
	b, err := resolveCancelRejection(ctx, tx, scope, raw)
	if errors.Is(err, ErrCancelRejectionBinding) {
		return "waiting_identity", err.Error(), nil
	}
	if errors.Is(err, ErrStreamIdentity) {
		return "rejected", "cancel rejection differs from registered command/publication", nil
	}
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(raw)
	_, err = tx.ExecContext(ctx, `INSERT INTO futures_cancel_rejections(command_id,scope_key,event_id,body_sha256,state) VALUES($1,$2,$3,$4,'rejected') ON CONFLICT DO NOTHING`, b.commandID, scopeKey, eventID, digest[:])
	if err != nil {
		return "", "", err
	}
	var oldDigest []byte
	var oldScope, state, sourceState string
	err = tx.QueryRowContext(ctx, `SELECT r.body_sha256,r.scope_key,r.state,i.state FROM futures_cancel_rejections r JOIN futures_event_inbox i USING(event_id) WHERE r.command_id=$1 FOR UPDATE OF r`, b.commandID).Scan(&oldDigest, &oldScope, &state, &sourceState)
	if err != nil {
		return "", "", err
	}
	if oldScope != scopeKey || !bytes.Equal(oldDigest, digest[:]) || state == "conflict" || sourceState == "conflict" {
		if _, err := tx.ExecContext(ctx, `UPDATE futures_cancel_rejections SET state='conflict' WHERE command_id=$1`, b.commandID); err != nil {
			return "", "", err
		}
		return "conflict", "cancel rejection evidence conflict; retry remains disabled", nil
	}
	return "applied", "cancel attempt rejected; original order unchanged", nil
}

// CancelRejectionStatus exposes a verified attempt outcome without changing the
// publication record, order state, cancel budget or permission to send another.
type CancelRejectionStatus struct {
	CommandID              int64  `json:"command_id"`
	EventID                int64  `json:"event_id"`
	State                  string `json:"state"`
	GatewayOrderID         string `json:"gateway_order_id"`
	CancelID               string `json:"cancel_id"`
	Code                   string `json:"code"`
	Message                string `json:"message"`
	OccurredAt             string `json:"occurred_at"`
	NativeErrorID          int64  `json:"native_error_id"`
	NativeErrorHex         string `json:"native_error_hex"`
	RetrySafe              bool   `json:"retry_safe"`
	ReconciliationRequired bool   `json:"reconciliation_required"`
	OrderStateChanged      bool   `json:"order_state_changed"`
}

func (s *EventStore) CancelRejection(ctx context.Context, scopeKey string, commandID int64) (CancelRejectionStatus, error) {
	var out CancelRejectionStatus
	if commandID <= 0 {
		return out, ErrStreamIdentity
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	scope, err := lockEventScope(ctx, tx, scopeKey)
	if err != nil {
		return out, err
	}
	var raw, digest, outcomeDigest []byte
	var sourceState string
	err = tx.QueryRowContext(ctx, `SELECT r.event_id,r.state,i.state,i.body,i.body_sha256,r.body_sha256 FROM futures_cancel_rejections r JOIN futures_event_inbox i ON i.event_id=r.event_id AND i.scope_key=r.scope_key WHERE r.scope_key=$1 AND r.command_id=$2`, scopeKey, commandID).Scan(&out.EventID, &out.State, &sourceState, &raw, &digest, &outcomeDigest)
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(raw)
	if !bytes.Equal(digest, hash[:]) || !bytes.Equal(outcomeDigest, hash[:]) {
		return out, ErrStreamIdentity
	}
	b, err := resolveCancelRejection(ctx, tx, scope, raw)
	if err != nil || b.commandID != commandID {
		return out, ErrStreamIdentity
	}
	if sourceState == "conflict" {
		out.State = "conflict"
	} else if sourceState != "applied" {
		return out, ErrStreamIdentity
	}
	r := b.rejection
	out.CommandID, out.GatewayOrderID, out.CancelID = commandID, r.GatewayOrderID, r.CancelID
	out.Code, out.Message, out.OccurredAt = r.Code, r.Message, r.OccurredAt
	out.NativeErrorID, out.NativeErrorHex, out.ReconciliationRequired = r.NativeErrorID, r.NativeErrorHex, true
	return out, tx.Commit()
}
