package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrQueryPublishUnknown = errors.New("query publication attempted or unknown; automatic resend forbidden")
var ErrQueryPublishGate = errors.New("TEST query publication gate is closed")

// These are independently reviewed operator bindings, not learned from replies.
// SourceID is the existing configuration fingerprint, not the deployment label.
type TestQueryTarget struct {
	SourceID             string `json:"source_id"`
	ServerRunID          string `json:"server_run_id"`
	Epoch                string `json:"epoch"`
	Group                string `json:"group"`
	CounterSessionID     string `json:"counter_session_id"`
	TradeDate            string `json:"trade_date"`
	ConnectionGeneration int64  `json:"connection_generation"`
}

func DecodeTestQueryTarget(raw []byte) (TestQueryTarget, error) {
	var target TestQueryTarget
	if len(raw) > 4096 {
		return target, ErrStreamIdentity
	}
	if _, err := parseReply(raw); err != nil {
		return target, ErrStreamIdentity
	}
	if _, err := shape(raw, []string{"source_id", "server_run_id", "epoch", "group", "counter_session_id", "trade_date", "connection_generation"}, nil); err != nil {
		return target, ErrStreamIdentity
	}
	if err := json.Unmarshal(raw, &target); err != nil {
		return target, ErrStreamIdentity
	}
	return target, target.Validate()
}

func (t TestQueryTarget) Validate() error {
	for _, value := range []string{t.SourceID, t.ServerRunID, t.Epoch, t.Group, t.CounterSessionID} {
		if !validID(value) || len(value) > 128 {
			return ErrStreamIdentity
		}
	}
	if len(t.ServerRunID) != 40 || strings.Trim(t.ServerRunID, "0123456789abcdef") != "" || !validDay(t.TradeDate) || t.ConnectionGeneration < 1 {
		return ErrStreamIdentity
	}
	return nil
}

type queryPublishProof struct {
	EntryID    string               `json:"entry_id"`
	Body       []byte               `json:"body"`
	CheckedAt  time.Time            `json:"checked_at"`
	ExpiresAt  time.Time            `json:"expires_at"`
	ProducedAt time.Time            `json:"produced_at"`
	Capture    *CapturePublishProof `json:"capture,omitempty"`
}

type queryPublicationTransport interface {
	inspect(context.Context) (queryPublishProof, error)
	appendQuery(context.Context, queryPublishProof, []byte) (string, bool, error)
}

type QueryPublication struct {
	CommandID        int64  `json:"command_id"`
	State            string `json:"state"`
	StreamID         string `json:"stream_id,omitempty"`
	ObservedStreamID string `json:"observed_stream_id,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Replayed         bool   `json:"replayed"`
	// nil means unresolved; false must never be used to imply safe resend.
	Published      *bool `json:"command_published"`
	BrokerAccepted bool  `json:"broker_accepted"`
}

func publicationResult(id int64, state, entry, reason string, replay bool) QueryPublication {
	r := QueryPublication{CommandID: id, State: state, StreamID: entry, Reason: reason, Replayed: replay}
	if state != "attempted" {
		published := state == "published"
		r.Published = &published
	}
	return r
}

// PublishTestQuery is one explicit publication attempt for an original durable
// query. Registration/replay never authorizes a second publication. No SDK calls.
func (s *CommandStore) PublishTestQuery(ctx context.Context, id int64, messageID string, p *TestQueryPublisher) (QueryPublication, error) {
	if p == nil {
		return QueryPublication{}, ErrStreamIdentity
	}
	return s.publishTestQuery(ctx, p.scope, id, messageID, p.target, p)
}

func (s *CommandStore) publishTestQuery(ctx context.Context, scope CommandScope, id int64, messageID string, target TestQueryTarget, transport queryPublicationTransport) (QueryPublication, error) {
	binding, _ := json.Marshal(target)
	return s.publishTestRegistered(ctx, scope, id, messageID, target, transport, "futures_test_query_publications", binding, func(c CommandRegistration) error {
		if queryResultType(c.Original.Action) == "" {
			return ErrStreamIdentity
		}
		return nil
	}, nil)
}

// The same durable attempt protocol is used for queries and explicit TEST trades.
// The policy and table are internal constants, never caller-supplied SQL.
func (s *CommandStore) publishTestRegistered(ctx context.Context, scope CommandScope, id int64, messageID string, target TestQueryTarget, transport queryPublicationTransport, table string, targetRaw []byte, validate func(CommandRegistration) error, insert func(context.Context, *sql.Tx, CommandRegistration, []byte, []byte) error) (QueryPublication, error) {
	if !oneOf(table, "futures_test_query_publications", "futures_test_trade_publications") || validate == nil {
		return QueryPublication{}, ErrStreamIdentity
	}
	if scope.validate() != nil || scope.Route.Env != "test" || !(scope.Route.BrokerID == "ctp" || strings.HasPrefix(scope.Route.BrokerID, "ctp_")) || id <= 0 || target.Validate() != nil || transport == nil {
		return QueryPublication{}, ErrStreamIdentity
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Minute {
		return QueryPublication{}, ErrStreamIdentity
	}
	key := string(commandRouteBytes(scope.Route))
	// Resolve an existing outcome before requiring fresh network evidence.
	lookup := func(tx *sql.Tx) (CommandRegistration, *QueryPublication, error) {
		var found int64
		if err := tx.QueryRowContext(ctx, `SELECT command_id FROM futures_commands WHERE route_key=$1 AND command_id=$2 FOR UPDATE`, key, id).Scan(&found); err != nil {
			return CommandRegistration{}, nil, err
		}
		c, err := readRegisteredCommand(ctx, tx, key, id)
		if err != nil {
			return c, nil, err
		}
		if c.Scope != scope || c.Original.MessageID != messageID {
			return c, nil, ErrStreamIdentity
		}
		if err := validate(c); err != nil {
			return c, nil, err
		}
		var state, reason string
		var entry sql.NullString
		var same bool
		err = tx.QueryRowContext(ctx, `SELECT state,entry_id,reason,target_json=$2::jsonb FROM `+table+` WHERE command_id=$1`, id, targetRaw).Scan(&state, &entry, &reason, &same)
		if errors.Is(err, sql.ErrNoRows) {
			return c, nil, nil
		}
		if err != nil {
			return c, nil, err
		}
		if !same {
			return c, nil, ErrStreamIdentity
		}
		if state == "published" {
			if _, err := streamID(entry.String); err != nil || entry.String == "0-0" {
				return c, nil, ErrStreamCursor
			}
		}
		r := publicationResult(id, state, entry.String, reason, true)
		return c, &r, nil
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return QueryPublication{}, err
	}
	_, existing, err := lookup(tx)
	tx.Rollback()
	if err != nil {
		return QueryPublication{}, err
	}
	if existing != nil {
		return publicationOutcome(*existing)
	}
	proof, err := transport.inspect(ctx)
	if err != nil {
		return QueryPublication{}, ErrQueryPublishGate
	}
	if proof.ExpiresAt.Before(time.Now()) || proof.EntryID == "" || len(proof.Body) > maxHeartbeatBytes {
		return QueryPublication{}, ErrQueryPublishGate
	}
	proofRaw, _ := json.Marshal(proof)
	tx, err = beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return QueryPublication{}, err
	}
	defer tx.Rollback()
	command, existing, err := lookup(tx)
	if err != nil {
		return QueryPublication{}, err
	}
	if existing != nil {
		return publicationOutcome(*existing)
	}
	if table == "futures_test_trade_publications" {
		if err := recheckClosePublication(ctx, tx, command, target); err != nil {
			return QueryPublication{}, err
		}
	}
	if insert != nil {
		err = insert(ctx, tx, command, targetRaw, proofRaw)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO `+table+`(command_id,target_json,proof_json,state) VALUES($1,$2,$3,'attempted')`, id, targetRaw, proofRaw)
	}
	if err != nil {
		return QueryPublication{}, err
	}
	attempt := publicationResult(id, "attempted", "", "", false)
	if err = tx.Commit(); err != nil {
		return attempt, ErrQueryPublishUnknown
	}
	// No network call can occur before the attempted state is durably committed.
	entry, rejected, err := transport.appendQuery(ctx, proof, command.Original.Raw)
	if err != nil {
		return attempt, ErrQueryPublishUnknown
	}
	state, reason := "published", ""
	var storedEntry any = entry
	if rejected {
		state, reason, storedEntry = "not_published", "atomic_gate_changed", nil
	} else if _, e := streamID(entry); e != nil || entry == "0-0" {
		return attempt, ErrQueryPublishUnknown
	}
	finish, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		attempt.ObservedStreamID = entry
		return attempt, ErrQueryPublishUnknown
	}
	defer finish.Rollback()
	result, err := finish.ExecContext(ctx, `UPDATE `+table+` SET state=$2,entry_id=$3,reason=$4,finished_at=clock_timestamp() WHERE command_id=$1 AND state='attempted' AND target_json=$5::jsonb`, id, state, storedEntry, reason, targetRaw)
	if err != nil {
		attempt.ObservedStreamID = entry
		return attempt, ErrQueryPublishUnknown
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		attempt.ObservedStreamID = entry
		return attempt, ErrQueryPublishUnknown
	}
	if rejected {
		if err := setCommandPending(ctx, finish, id, false); err != nil {
			return attempt, ErrQueryPublishUnknown
		}
	}
	if err := finish.Commit(); err != nil {
		attempt.ObservedStreamID = entry
		return attempt, ErrQueryPublishUnknown
	}
	return publicationOutcome(publicationResult(id, state, entry, reason, false))
}

func publicationOutcome(r QueryPublication) (QueryPublication, error) {
	switch r.State {
	case "published":
		return r, nil
	case "not_published":
		return r, ErrQueryPublishGate
	case "attempted":
		return r, ErrQueryPublishUnknown
	default:
		return r, fmt.Errorf("invalid publication state")
	}
}
