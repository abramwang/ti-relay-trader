package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrCommandIdentity = errors.New("immutable futures submit identity conflict")
var redisEntryPattern = regexp.MustCompile(`^[0-9]{1,20}-[0-9]{1,20}$`)

// EventStore is a local durable inbox. Callers must authenticate configured
// sources and local submitters. No Redis client, publisher, ACK or clock exists.
type EventStore struct{ db *sql.DB }

func NewEventStore(db *sql.DB) (*EventStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database required")
	}
	return &EventStore{db: db}, nil
}

// RegisterScope uses trusted configuration, never routing learned from an event.
// An empty ledger is initialized for quality reporting, not as a broker snapshot.
func (s *EventStore) RegisterScope(ctx context.Context, scope TradeScope, nativeUserID, eventStream string) (string, error) {
	if err := scope.validate(); err != nil {
		return "", err
	}
	if err := (CommandScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, nativeUserID}).validate(); err != nil {
		return "", err
	}
	expectedStream := fmt.Sprintf("relay:%s:v1:%s:%s:event", scope.Route.Env, scope.Route.BrokerID, scope.Route.GatewayID)
	if !validID(eventStream) || len(eventStream) > 512 || eventStream != expectedStream {
		return "", fmt.Errorf("configured event stream required")
	}
	raw, _ := json.Marshal(scope)
	if len(raw) > 65536 {
		return "", ErrAuditCapacity
	}
	key := tradeScopeKey(scope)
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_event_scopes(scope_key,context_json,event_stream,native_user_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, key, raw, eventStream, nativeUserID); err != nil {
		return "", err
	}
	var same bool
	if err := tx.QueryRowContext(ctx, `SELECT context_json=$2::jsonb AND event_stream=$3 AND COALESCE(native_user_id,'')=$4 FROM futures_event_scopes WHERE scope_key=$1 FOR UPDATE`, key, raw, eventStream, nativeUserID).Scan(&same); err != nil {
		return "", err
	}
	if !same {
		return "", fmt.Errorf("immutable event scope conflict")
	}
	if _, err := observeTradesTx(ctx, tx, scope, nil); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return key, nil
}

type eventScope struct {
	scope                  TradeScope
	stream, userID         string
	eventCount, eventBytes int
}

func lockEventScope(ctx context.Context, tx *sql.Tx, key string) (eventScope, error) {
	var s eventScope
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT context_json,event_stream,COALESCE(native_user_id,''),event_count,event_bytes FROM futures_event_scopes WHERE scope_key=$1 FOR UPDATE`, key).Scan(&raw, &s.stream, &s.userID, &s.eventCount, &s.eventBytes)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s.scope); err != nil {
		return s, err
	}
	if err := s.scope.validate(); err != nil {
		return s, err
	}
	if err := (CommandScope{s.scope.Route, s.scope.NativeBrokerID, s.scope.InvestorID, s.userID}).validate(); err != nil {
		return s, err
	}
	if tradeScopeKey(s.scope) != key {
		return s, fmt.Errorf("event scope integrity failure")
	}
	return s, nil
}

type EventReceipt struct {
	EventID  int64
	State    string
	Reason   string
	Replayed bool
}

// Receive commits the full raw envelope before processing. Invalid bytes are
// retained; scope/source errors and capacity failures return error, not durability.
// Matching message IDs must keep exact bytes even across different Stream entries.
func (s *EventStore) Receive(ctx context.Context, key string, source QuerySource, raw []byte) (EventReceipt, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return EventReceipt{}, err
	}
	defer tx.Rollback()
	out, err := receiveEventTx(ctx, tx, key, source, raw)
	if err != nil {
		return EventReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return EventReceipt{}, err
	}
	return out, nil
}

func receiveEventTx(ctx context.Context, tx *sql.Tx, key string, source QuerySource, raw []byte) (EventReceipt, error) {
	if len(raw) > maxSnapshotBytes {
		return EventReceipt{}, ErrAuditCapacity
	}
	if !redisEntryPattern.MatchString(source.EntryID) {
		return EventReceipt{}, fmt.Errorf("invalid Redis entry ID")
	}
	if raw == nil {
		raw = []byte{}
	}
	scope, err := lockEventScope(ctx, tx, key)
	if err != nil {
		return EventReceipt{}, err
	}
	if source.StreamKey != scope.stream {
		return EventReceipt{}, fmt.Errorf("event stream differs from registered source")
	}
	hash := sha256.Sum256(raw)
	var result EventReceipt
	var old []byte
	err = tx.QueryRowContext(ctx, `SELECT event_id,state,reason,body FROM futures_event_inbox WHERE scope_key=$1 AND entry_id=$2 AND body_sha256=$3`, key, source.EntryID, hash[:]).Scan(&result.EventID, &result.State, &result.Reason, &old)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return EventReceipt{}, fmt.Errorf("event checksum mismatch")
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EventReceipt{}, err
	}
	if tradeCapacity(scope.eventCount, scope.eventBytes, 1, len(raw)) != nil {
		return EventReceipt{}, ErrAuditCapacity
	}
	messageID := ""
	if obj, err := parseReply(raw); err == nil {
		id := textField(obj, "message_id")
		if validID(id) && len(id) <= 512 {
			messageID = id
		}
	}
	var conflict bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_event_inbox WHERE scope_key=$1 AND (entry_id=$2 OR ($3<>'' AND message_id=$3)) AND (body_sha256<>$4 OR state='conflict'))`, key, source.EntryID, messageID, hash[:]).Scan(&conflict); err != nil {
		return EventReceipt{}, err
	}
	result.State = "pending"
	if conflict {
		result.State = "conflict"
		result.Reason = "event source/message identity changed bytes"
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO futures_event_inbox(scope_key,entry_id,message_id,body,body_sha256,state,reason) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING event_id`, key, source.EntryID, messageID, raw, hash[:], result.State, result.Reason).Scan(&result.EventID); err != nil {
		return EventReceipt{}, err
	}
	if conflict {
		// Also quarantine earlier copies of the old message when a Stream entry
		// changes its message ID; those copies must not apply after this conflict.
		if _, err := tx.ExecContext(ctx, `UPDATE futures_event_inbox SET state='conflict',reason=$4 WHERE scope_key=$1
            AND (entry_id=$2 OR (message_id<>'' AND message_id IN
                (SELECT message_id FROM futures_event_inbox WHERE scope_key=$1 AND (entry_id=$2 OR ($3<>'' AND message_id=$3)))))`, key, source.EntryID, messageID, result.Reason); err != nil {
			return EventReceipt{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_event_scopes SET event_count=event_count+1,event_bytes=event_bytes+$2 WHERE scope_key=$1`, key, len(raw)); err != nil {
		return EventReceipt{}, err
	}
	return result, nil
}

// Process applies registered local intent or a trusted OC external claim.
// Unknown local identity waits durably, never becomes external by lookup failure.
// The original envelope and processing state remain separate from trade facts.
func (s *EventStore) Process(ctx context.Context, id int64) (EventReceipt, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return EventReceipt{}, err
	}
	defer tx.Rollback()
	result, err := processEventTx(ctx, tx, id)
	if err != nil {
		return EventReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return EventReceipt{}, err
	}
	return result, nil
}

func processEventTx(ctx context.Context, tx *sql.Tx, id int64) (EventReceipt, error) {
	var key string
	if err := tx.QueryRowContext(ctx, `SELECT scope_key FROM futures_event_inbox WHERE event_id=$1`, id).Scan(&key); err != nil {
		return EventReceipt{}, err
	}
	scope, err := lockEventScope(ctx, tx, key)
	if err != nil {
		return EventReceipt{}, err
	}
	result := EventReceipt{EventID: id}
	var raw, digest []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,reason,body,body_sha256 FROM futures_event_inbox WHERE event_id=$1 FOR UPDATE`, id).Scan(&result.State, &result.Reason, &raw, &digest); err != nil {
		return EventReceipt{}, err
	}
	hash := sha256.Sum256(raw)
	if !bytes.Equal(hash[:], digest) {
		return EventReceipt{}, fmt.Errorf("event checksum mismatch")
	}
	if !oneOf(result.State, "pending", "waiting_identity") {
		result.Replayed = true
		return result, nil
	}
	if isCancelRejectionEvent(raw) {
		result.State, result.Reason, err = applyCancelRejection(ctx, tx, scope, key, id, raw)
		if err != nil {
			return EventReceipt{}, err
		}
	} else {
		event, decodeErr := DecodeTradeEvent(raw, EventContext{Scope: scope.scope})
		if decodeErr != nil {
			result.State = "rejected"
			result.Reason = decodeErr.Error()
		} else {
			state, reason, err := authorizeEvent(ctx, tx, scope, event)
			if err != nil {
				return EventReceipt{}, err
			}
			result.State, result.Reason = state, reason
			if state == "applied" {
				// The original full message ID deduplicates republished exact bytes.
				source, _ := json.Marshal([]string{key, event.MessageID})
				sum := sha256.Sum256(source)
				kind := "order"
				if event.Fill != nil {
					kind = "fill"
				}
				if _, err := observeTradesTx(ctx, tx, scope.scope, []tradeObservation{{"ctp-event:" + hex.EncodeToString(sum[:]), kind, event.RawPayload}}); err != nil {
					return EventReceipt{}, err
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_event_inbox SET state=$2,reason=$3,processed_at=clock_timestamp() WHERE event_id=$1`, id, result.State, result.Reason); err != nil {
		return EventReceipt{}, err
	}
	return result, nil
}

func authorizeEvent(ctx context.Context, tx *sql.Tx, registered eventScope, event TradeEvent) (string, string, error) {
	return authorizeTradeIdentity(ctx, tx, registered.scope, registered.userID, event, true)
}

// Query rows carry the QUERY origin, not the original order-command origin.
// Their payload must still match the same local order intention. No synthetic
// origin is written back to either query audit or broker facts.
func authorizeTradeIdentity(ctx context.Context, tx *sql.Tx, scope TradeScope, userID string, event TradeEvent, checkOrigin bool) (string, string, error) {
	gateway := tradeEventGateway(event)
	key := string(commandRouteBytes(scope.Route))
	var commandID int64
	err := tx.QueryRowContext(ctx, `SELECT command_id FROM futures_command_reservations WHERE route_key=$1 AND trade_date=$2 AND kind='gateway' AND identity=$3`, key, scope.TradeDate, gateway).Scan(&commandID)
	if errors.Is(err, sql.ErrNoRows) {
		return authorizeResolvedTrade(scope, userID, event, checkOrigin, nil)
	}
	if err != nil {
		return "", "", err
	}
	original, err := readRegisteredCommand(ctx, tx, key, commandID)
	if err != nil {
		return "", "", err
	}
	return authorizeResolvedTrade(scope, userID, event, checkOrigin, &original)
}

func tradeEventGateway(event TradeEvent) string {
	if event.Order != nil {
		return event.Order.GatewayOrderID
	}
	return event.Fill.GatewayOrderID
}

func authorizeResolvedTrade(scope TradeScope, userID string, event TradeEvent, checkOrigin bool, original *CommandRegistration) (string, string, error) {
	gateway := tradeEventGateway(event)
	if original == nil {
		if strings.HasPrefix(gateway, "external-ctp-v1-") && event.OriginMessageID == "" {
			return "applied", "", nil
		}
		return "waiting_identity", "local submit identity not registered", nil
	}
	// Native row UserID is broker-origin audit, not the collector login identity.
	if original.Scope != (CommandScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, userID}) {
		return "rejected", "trade scope differs from original native command identity", nil
	}
	command := original.Original.SubmitCommand
	for _, order := range command.Orders {
		if order.GatewayOrderID == gateway {
			if !order.matchesPayload(event) || (checkOrigin && !order.matches(event, command)) {
				return "rejected", "event differs from registered submit origin/intent", nil
			}
			return "applied", "", nil
		}
	}
	return "", "", fmt.Errorf("intent registry differs from original command")
}

// Pending discovers durable work after a receiver restart; it never resends a
// command or clears quarantined evidence. The caller owns scheduling/backoff.
func (s *EventStore) Pending(ctx context.Context, key string, afterID int64, limit int) ([]EventReceipt, error) {
	if afterID < 0 || limit < 1 || limit > 256 {
		return nil, fmt.Errorf("after ID must be nonnegative and limit must be 1..256")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,state,reason FROM futures_event_inbox WHERE scope_key=$1 AND state IN ('pending','waiting_identity') AND event_id>$2 ORDER BY event_id LIMIT $3`, key, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []EventReceipt{}
	for rows.Next() {
		var r EventReceipt
		if err := rows.Scan(&r.EventID, &r.State, &r.Reason); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
