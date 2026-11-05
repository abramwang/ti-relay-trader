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
func (s *EventStore) RegisterScope(ctx context.Context, scope TradeScope, eventStream string) (string, error) {
	if err := scope.validate(); err != nil {
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_event_scopes(scope_key,context_json,event_stream) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, key, raw, eventStream); err != nil {
		return "", err
	}
	var same bool
	if err := tx.QueryRowContext(ctx, `SELECT context_json=$2::jsonb AND event_stream=$3 FROM futures_event_scopes WHERE scope_key=$1 FOR UPDATE`, key, raw, eventStream).Scan(&same); err != nil {
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
	scope                                                           TradeScope
	stream                                                          string
	eventCount, eventBytes, commandCount, commandBytes, intentCount int
}

func lockEventScope(ctx context.Context, tx *sql.Tx, key string) (eventScope, error) {
	var s eventScope
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT context_json,event_stream,event_count,event_bytes,command_count,command_bytes,intent_count FROM futures_event_scopes WHERE scope_key=$1 FOR UPDATE`, key).Scan(&raw, &s.stream, &s.eventCount, &s.eventBytes, &s.commandCount, &s.commandBytes, &s.intentCount)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s.scope); err != nil {
		return s, err
	}
	if err := s.scope.validate(); err != nil {
		return s, err
	}
	if tradeScopeKey(s.scope) != key {
		return s, fmt.Errorf("event scope integrity failure")
	}
	return s, nil
}

// RegisterCommand must precede publication by a trusted local submit path.
// Only byte-identical re-registration is a replay; this is not a business-level
// idempotency API, send authorization, outbox or evidence that SDK was invoked.
func (s *EventStore) RegisterCommand(ctx context.Context, key string, raw []byte) (bool, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	scope, err := lockEventScope(ctx, tx, key)
	if err != nil {
		return false, err
	}
	command, err := DecodeSubmitCommand(raw, scope.scope)
	if err != nil {
		return false, err
	}
	var old, digest []byte
	err = tx.QueryRowContext(ctx, `SELECT body,body_sha256 FROM futures_submit_commands WHERE scope_key=$1 AND (message_id=$2 OR idempotency_key=$3) LIMIT 1`, key, command.MessageID, command.IdempotencyKey).Scan(&old, &digest)
	if err == nil {
		hash := sha256.Sum256(old)
		if !bytes.Equal(hash[:], digest) {
			return false, fmt.Errorf("command checksum mismatch")
		}
		if !bytes.Equal(old, raw) {
			return false, ErrCommandIdentity
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if scope.commandCount >= maxAuditCount || len(raw) > maxAuditBytes-scope.commandBytes || len(command.Orders) > maxAuditCount-scope.intentCount {
		return false, ErrAuditCapacity
	}
	for _, order := range command.Orders {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_submit_intents WHERE scope_key=$1 AND (gateway_order_id=$2 OR client_order_id=$3))`, key, order.GatewayOrderID, order.ClientOrderID).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, ErrCommandIdentity
		}
	}
	hash := sha256.Sum256(raw)
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_submit_commands(scope_key,message_id,idempotency_key,body,body_sha256) VALUES($1,$2,$3,$4,$5)`, key, command.MessageID, command.IdempotencyKey, raw, hash[:]); err != nil {
		return false, err
	}
	for _, order := range command.Orders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO futures_submit_intents(scope_key,gateway_order_id,client_order_id,message_id) VALUES($1,$2,$3,$4)`, key, order.GatewayOrderID, order.ClientOrderID, command.MessageID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_event_scopes SET command_count=command_count+1,command_bytes=command_bytes+$2,intent_count=intent_count+$3 WHERE scope_key=$1`, key, len(raw), len(command.Orders)); err != nil {
		return false, err
	}
	return false, tx.Commit()
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
	if len(raw) > maxSnapshotBytes {
		return EventReceipt{}, ErrAuditCapacity
	}
	if !redisEntryPattern.MatchString(source.EntryID) {
		return EventReceipt{}, fmt.Errorf("invalid Redis entry ID")
	}
	if raw == nil {
		raw = []byte{}
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return EventReceipt{}, err
	}
	defer tx.Rollback()
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
		if err := tx.Commit(); err != nil {
			return EventReceipt{}, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EventReceipt{}, err
	}
	if scope.eventCount >= maxAuditCount || len(raw) > maxAuditBytes-scope.eventBytes {
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
	if err := tx.Commit(); err != nil {
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
		if err := tx.Commit(); err != nil {
			return EventReceipt{}, err
		}
		return result, nil
	}
	event, decodeErr := DecodeTradeEvent(raw, EventContext{Scope: scope.scope})
	if decodeErr != nil {
		result.State = "rejected"
		result.Reason = decodeErr.Error()
	} else {
		state, reason, err := authorizeEvent(ctx, tx, key, scope.scope, event)
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
	if _, err := tx.ExecContext(ctx, `UPDATE futures_event_inbox SET state=$2,reason=$3,processed_at=clock_timestamp() WHERE event_id=$1`, id, result.State, result.Reason); err != nil {
		return EventReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return EventReceipt{}, err
	}
	return result, nil
}

func authorizeEvent(ctx context.Context, tx *sql.Tx, key string, scope TradeScope, event TradeEvent) (string, string, error) {
	gateway := ""
	if event.Order != nil {
		gateway = event.Order.GatewayOrderID
	} else {
		gateway = event.Fill.GatewayOrderID
	}
	var raw, digest []byte
	err := tx.QueryRowContext(ctx, `SELECT c.body,c.body_sha256 FROM futures_submit_intents i JOIN futures_submit_commands c USING(scope_key,message_id) WHERE i.scope_key=$1 AND i.gateway_order_id=$2`, key, gateway).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		if strings.HasPrefix(gateway, "external-ctp-v1-") && event.OriginMessageID == "" {
			return "applied", "", nil
		}
		return "waiting_identity", "local submit identity not registered", nil
	}
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	if !bytes.Equal(sum[:], digest) {
		return "", "", fmt.Errorf("command checksum mismatch")
	}
	command, err := DecodeSubmitCommand(raw, scope)
	if err != nil {
		return "", "", err
	}
	for _, order := range command.Orders {
		if order.GatewayOrderID == gateway {
			if !order.matches(event, command) {
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
