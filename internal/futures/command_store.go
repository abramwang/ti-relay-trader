package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// CatalogBinding retains explicit Meridian names/version evidence. This store
// never invents security_id or verifies freshness/market permission itself.
type CatalogBinding struct {
	SecurityID            string `json:"security_id"`
	SourceInstrumentID    string `json:"source_instrument_id"`
	ExchangeID            string `json:"exchange_id"`
	PublicationSnapshotID string `json:"publication_snapshot_id"`
	LogicalTableChecksum  string `json:"logical_table_checksum"`
}

type CancelTarget struct {
	Scope                     CommandScope
	GatewayOrderID, TradeDate string
}

// RegistrationEvidence is caller-verified evidence for NEW intentions only.
// It does not stand for authorization, a current session or dynamic constraints.
type RegistrationEvidence struct {
	CurrentTradeDate string                    `json:"current_trade_date,omitempty"`
	CancelTarget     *CancelTarget             `json:"cancel_target,omitempty"`
	Mappings         map[string]CatalogBinding `json:"mappings,omitempty"`
}

type CommandRegistration struct {
	Outcome, Reason string
	CommandID       int64
	Scope           CommandScope
	Original        Command
	Evidence        RegistrationEvidence
}

// CommandStore keeps a route-wide namespace across days and actions. A registered intention
// must NEVER be treated as a broker receipt or permission to send/ACK anything.
type CommandStore struct{ db *sql.DB }

func NewCommandStore(db *sql.DB) (*CommandStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database required")
	}
	return &CommandStore{db}, nil
}

type reservation struct{ day, kind, id string }

func commandReservations(c Command) []reservation {
	var out []reservation
	for _, row := range c.Orders {
		out = append(out, reservation{row.TradeDate, "gateway", row.GatewayOrderID}, reservation{row.TradeDate, "client", row.ClientOrderID})
	}
	if c.Cancel != nil {
		out = append(out, reservation{c.Cancel.TradeDate, "cancel", c.Cancel.CancelID})
	}
	return out
}

func checkRegistrationEvidence(c Command, scope CommandScope, e RegistrationEvidence) string {
	if len(c.Orders) > 0 {
		if !validDay(e.CurrentTradeDate) || c.Orders[0].TradeDate != e.CurrentTradeDate {
			return "new_intent_trade_date_mismatch"
		}
		if len(e.Mappings) != len(c.Orders) || e.CancelTarget != nil {
			return "mapping_evidence_required"
		}
		for _, row := range c.Orders {
			m, ok := e.Mappings[row.GatewayOrderID]
			if !ok || !validID(m.SecurityID) || len(m.SecurityID) > 128 || m.SourceInstrumentID != row.Symbol || m.ExchangeID != row.Exchange || !validID(m.PublicationSnapshotID) || len(m.PublicationSnapshotID) > 128 || !validID(m.LogicalTableChecksum) || len(m.LogicalTableChecksum) > 128 {
				return "mapping_evidence_mismatch"
			}
		}
	} else if c.Cancel != nil {
		t := e.CancelTarget
		if t == nil || t.Scope != scope || t.GatewayOrderID != c.Cancel.GatewayOrderID || t.TradeDate != c.Cancel.TradeDate || len(e.Mappings) > 0 {
			return "cancel_target_evidence_mismatch"
		}
	} else if len(e.Mappings) > 0 || e.CancelTarget != nil {
		return "unexpected_query_evidence"
	}
	return ""
}

// Register durably records every distinct syntax-valid attempt and decision.
// Syntax/scope/stream failures and capacity/DB errors are NOT acknowledged or
// recorded here; the future ingress must retain responsibility for those bytes.
func (s *CommandStore) Register(ctx context.Context, scope CommandScope, stream string, raw []byte, e RegistrationEvidence) (CommandRegistration, error) {
	return s.register(ctx, scope, stream, raw, e, nil)
}

// replay is evaluated under the same route lock as ordinary command
// registration. A concurrent service retry must use the original bytes, not
// manufacture another sent_at or replace its catalog evidence.
func (s *CommandStore) register(ctx context.Context, scope CommandScope, stream string, raw []byte, e RegistrationEvidence, replay func(*sql.Tx) (*CommandRegistration, error)) (CommandRegistration, error) {
	return s.registerAtomic(ctx, scope, stream, raw, e, replay, nil)
}

// Quantity reservations use the same route lock and commit as the intention,
// identity aliases and audit. A failed reservation rolls all of them back.
func (s *CommandStore) registerAtomic(ctx context.Context, scope CommandScope, stream string, raw []byte, e RegistrationEvidence, replay func(*sql.Tx) (*CommandRegistration, error), reserve func(*sql.Tx, CommandRegistration) error) (CommandRegistration, error) {
	c, err := DecodeCommand(raw, scope, stream)
	if err != nil {
		return CommandRegistration{}, err
	}
	evidence, err := json.Marshal(e)
	if err != nil {
		return CommandRegistration{}, err
	}
	if len(evidence) > 65536 {
		return CommandRegistration{}, ErrAuditCapacity
	}
	key := string(commandRouteBytes(scope.Route))
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return CommandRegistration{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO futures_command_routes(route_key) VALUES($1) ON CONFLICT DO NOTHING`, key); err != nil {
		return CommandRegistration{}, err
	}
	var count, total int64
	if err = tx.QueryRowContext(ctx, `SELECT audit_count,audit_bytes FROM futures_command_routes WHERE route_key=$1 FOR UPDATE`, key).Scan(&count, &total); err != nil {
		return CommandRegistration{}, err
	}
	if count < 0 || total < 0 {
		return CommandRegistration{}, ErrCommandWork
	}
	if replay != nil {
		existing, err := replay(tx)
		if err != nil {
			return CommandRegistration{}, err
		}
		if existing != nil {
			if reserve != nil {
				if err := reserve(tx, *existing); err != nil {
					return CommandRegistration{}, err
				}
			}
			return *existing, tx.Commit()
		}
	}
	out, err := resolveCommand(ctx, tx, key, scope, stream, c, e, evidence)
	if err != nil {
		return CommandRegistration{}, err
	}
	if out.Outcome == "registered" {
		if err := checkCommandBacklog(ctx, tx, key); err != nil {
			return CommandRegistration{}, err
		}
	}
	if reserve != nil {
		if err := reserve(tx, out); err != nil {
			return CommandRegistration{}, err
		}
	}
	bodyHash, intentHash, evidenceHash := commandDigest(raw), commandDigest(c.Intent), commandDigest(evidence)
	var oldBody, oldIntent, oldEvidence []byte
	err = tx.QueryRowContext(ctx, `SELECT body,intent,evidence FROM futures_command_audit WHERE route_key=$1 AND message_id=$2 AND body_sha256=$3 AND intent_sha256=$4 AND evidence_sha256=$5 AND outcome=$6 AND reason=$7`, key, c.MessageID, bodyHash, intentHash, evidenceHash, out.Outcome, out.Reason).Scan(&oldBody, &oldIntent, &oldEvidence)
	if err == nil {
		if !bytes.Equal(oldBody, raw) || !bytes.Equal(oldIntent, c.Intent) || !bytes.Equal(oldEvidence, evidence) {
			return CommandRegistration{}, fmt.Errorf("command audit integrity failure")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		var id any
		if out.CommandID != 0 {
			id = out.CommandID
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO futures_command_audit(route_key,message_id,body,body_sha256,intent,intent_sha256,evidence,evidence_sha256,outcome,reason,command_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, key, c.MessageID, raw, bodyHash, c.Intent, intentHash, evidence, evidenceHash, out.Outcome, out.Reason, id); err != nil {
			return CommandRegistration{}, err
		}
	} else {
		return CommandRegistration{}, err
	}
	if err = tx.Commit(); err != nil {
		return CommandRegistration{}, err
	}
	return out, nil
}

func resolveCommand(ctx context.Context, tx *sql.Tx, key string, scope CommandScope, stream string, c Command, e RegistrationEvidence, evidence []byte) (CommandRegistration, error) {
	var id int64
	var alias []byte
	var hashText string
	err := tx.QueryRowContext(ctx, `SELECT command_id,body,body_sha256 FROM futures_command_messages WHERE route_key=$1 AND message_id=$2`, key, c.MessageID).Scan(&id, &alias, &hashText)
	byMessage := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CommandRegistration{}, err
	}
	if byMessage && commandDigest(alias) != hashText {
		return CommandRegistration{}, fmt.Errorf("message evidence integrity failure")
	}
	if !byMessage {
		err = tx.QueryRowContext(ctx, `SELECT command_id FROM futures_commands WHERE route_key=$1 AND idempotency_key=$2`, key, c.IdempotencyKey).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return CommandRegistration{}, err
		}
	}
	if id != 0 {
		out, err := readRegisteredCommand(ctx, tx, key, id)
		if err != nil {
			return CommandRegistration{}, err
		}
		out.Outcome = "conflict"
		if byMessage && !bytes.Equal(alias, c.Raw) {
			out.Reason = "message_bytes_conflict"
			return out, nil
		}
		if !bytes.Equal(out.Original.Intent, c.Intent) {
			out.Reason = "idempotency_intent_conflict"
			return out, nil
		}
		if len(e.Mappings) > 0 && !reflect.DeepEqual(e.Mappings, out.Evidence.Mappings) {
			out.Reason = "mapping_binding_conflict"
			return out, nil
		}
		if !byMessage {
			if _, err = tx.ExecContext(ctx, `INSERT INTO futures_command_messages(route_key,message_id,command_id,body,body_sha256) VALUES($1,$2,$3,$4,$5)`, key, c.MessageID, id, c.Raw, commandDigest(c.Raw)); err != nil {
				return CommandRegistration{}, err
			}
		}
		out.Outcome = "replayed"
		return out, nil
	}
	if reason := checkRegistrationEvidence(c, scope, e); reason != "" {
		return CommandRegistration{Outcome: "rejected", Reason: reason}, nil
	}
	for _, r := range commandReservations(c) {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_command_reservations WHERE route_key=$1 AND trade_date=$2 AND kind=$3 AND identity=$4)`, key, r.day, r.kind, r.id).Scan(&exists); err != nil {
			return CommandRegistration{}, err
		}
		if exists {
			return CommandRegistration{Outcome: "conflict", Reason: r.kind + "_identity_conflict"}, nil
		}
	}
	scopeRaw, _ := json.Marshal(scope)
	fields, _ := json.Marshal(c.Fields)
	if err = tx.QueryRowContext(ctx, `INSERT INTO futures_commands(route_key,idempotency_key,scope_json,stream_suffix,body,body_sha256,intent,intent_sha256,fields_json,evidence_json,evidence_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING command_id`, key, c.IdempotencyKey, scopeRaw, stream, c.Raw, commandDigest(c.Raw), c.Intent, commandDigest(c.Intent), fields, evidence, commandDigest(evidence)).Scan(&id); err != nil {
		return CommandRegistration{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO futures_command_messages(route_key,message_id,command_id,body,body_sha256) VALUES($1,$2,$3,$4,$5)`, key, c.MessageID, id, c.Raw, commandDigest(c.Raw)); err != nil {
		return CommandRegistration{}, err
	}
	for _, r := range commandReservations(c) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO futures_command_reservations(route_key,trade_date,kind,identity,command_id) VALUES($1,$2,$3,$4,$5)`, key, r.day, r.kind, r.id, id); err != nil {
			return CommandRegistration{}, err
		}
	}
	return readRegisteredCommand(ctx, tx, key, id)
}

func readRegisteredCommand(ctx context.Context, tx *sql.Tx, key string, id int64) (CommandRegistration, error) {
	var raw, scopeRaw, intent, fieldsRaw, evidence []byte
	var stream, rawHash, intentHash, evidenceHash, idem string
	var workValid bool
	err := tx.QueryRowContext(ctx, `SELECT c.scope_json,c.stream_suffix,c.body,c.body_sha256,c.intent,c.intent_sha256,c.fields_json,c.evidence_json,c.idempotency_key,c.evidence_sha256,
        COALESCE(w.route_key=c.route_key AND w.work_bytes=octet_length(c.body)::bigint+octet_length(c.intent)+octet_length(c.evidence_json::text),false)
        FROM futures_commands c LEFT JOIN futures_command_work w USING(command_id) WHERE c.route_key=$1 AND c.command_id=$2`, key, id).Scan(&scopeRaw, &stream, &raw, &rawHash, &intent, &intentHash, &fieldsRaw, &evidence, &idem, &evidenceHash, &workValid)
	if err != nil {
		return CommandRegistration{}, err
	}
	if !workValid {
		return CommandRegistration{}, ErrCommandWork
	}
	var scope CommandScope
	var fields []IntentField
	out := CommandRegistration{Outcome: "registered", CommandID: id}
	if json.Unmarshal(scopeRaw, &scope) != nil || json.Unmarshal(fieldsRaw, &fields) != nil || json.Unmarshal(evidence, &out.Evidence) != nil {
		return CommandRegistration{}, fmt.Errorf("invalid persisted command metadata")
	}
	c, err := DecodeCommand(raw, scope, stream)
	if err != nil {
		return CommandRegistration{}, err
	}
	canonicalEvidence, _ := json.Marshal(out.Evidence)
	if commandDigest(raw) != rawHash || commandDigest(intent) != intentHash || commandDigest(canonicalEvidence) != evidenceHash || !bytes.Equal(c.Intent, intent) || !reflect.DeepEqual(c.Fields, fields) || string(commandRouteBytes(scope.Route)) != key || c.IdempotencyKey != idem || checkRegistrationEvidence(c, scope, out.Evidence) != "" {
		return CommandRegistration{}, fmt.Errorf("registered command integrity failure")
	}
	out.Original = c
	out.Scope = scope
	return out, nil
}
