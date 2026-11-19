package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strings"
)

// This is explicit TEST configuration, never discovered from active-config or
// an incoming heartbeat. InitialCheckpoints authorize the first adoption only.
type CaptureServiceConfig struct {
	Version            string              `json:"version"`
	ServiceID          string              `json:"service_id"`
	Database           string              `json:"database"`
	DeploymentID       string              `json:"deployment_id"`
	Scope              CommandScope        `json:"scope"`
	Target             TestQueryTarget     `json:"target"`
	InitialCheckpoints ResidentCheckpoints `json:"initial_checkpoints"`
	ProjectLocalEvents bool                `json:"project_local_events"`
	ProjectReplies     bool                `json:"project_replies"`
}

func DecodeCaptureServiceConfig(raw []byte) (CaptureServiceConfig, error) {
	var c CaptureServiceConfig
	if len(raw) > 16384 {
		return c, ErrStreamIdentity
	}
	if _, err := parseReply(raw); err != nil { // Includes duplicate-key rejection.
		return c, ErrStreamIdentity
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return CaptureServiceConfig{}, ErrStreamIdentity
	}
	return c, nil
}

func (c CaptureServiceConfig) Validate() error {
	if c.Version != "ctp.capture.service.v1" || !validID(c.ServiceID) || len(c.ServiceID) > 128 ||
		!validID(c.Database) || len(c.Database) > 63 || !strings.HasSuffix(c.Database, "_test") ||
		!validID(c.DeploymentID) || len(c.DeploymentID) > 128 || c.Scope.validate() != nil || c.Scope.Route.Env != "test" ||
		!(c.Scope.Route.BrokerID == "ctp" || strings.HasPrefix(c.Scope.Route.BrokerID, "ctp_")) || c.Target.Validate() != nil ||
		validateCaptureCheckpoint(c.InitialCheckpoints.Reply) != nil || validateCaptureCheckpoint(c.InitialCheckpoints.Event) != nil {
		return ErrStreamIdentity
	}
	return nil
}

// RunTestCaptureService uses the same bounded reads/transactions and health
// lease as the operator CLI. Only the process deadline and per-run record
// budget differ. Storage capacity and all source/identity checks remain.
// Any failure stops both lanes; this entry does not reconnect/retry or publish.
func RunTestCaptureService(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, config CaptureServiceConfig, emit func(ResidentCaptureProgress) error) error {
	if config.Validate() != nil || ctx.Done() == nil || source == nil || verifier == nil ||
		source.scope != config.Scope || verifier.target != config.Target {
		return ErrStreamIdentity
	}
	return followTestPairMode(ctx, source, store, verifier, DefaultResidentCaptureOptions(), config.InitialCheckpoints, emit, config.ProjectLocalEvents, &config)
}

type captureServiceOwner struct {
	key, id string
	binding []byte
}

// Called while the route's existing PG advisory lock is held. The service row
// records owner tokens, while the authoritative cursors stay in the inbox's
// receiver rows and advance atomically with retained originals.
func (s *StreamStore) prepareCaptureService(ctx context.Context, source *TestStreamSource, c CaptureServiceConfig) (*captureServiceOwner, ResidentCheckpoints, error) {
	var checkpoints ResidentCheckpoints
	var database string
	if err := s.db.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return nil, checkpoints, err
	}
	if database != c.Database {
		return nil, checkpoints, ErrStreamIdentity
	}
	w := &captureServiceOwner{string(commandRouteBytes(c.Scope.Route)), c.ServiceID, captureBinding(c.Scope, c.Target)}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return nil, checkpoints, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO futures_capture_services(route_key,service_id,binding_json,reply_owner,event_owner)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(route_key) DO NOTHING`, w.key, w.id, w.binding, c.InitialCheckpoints.Reply.Owner, c.InitialCheckpoints.Event.Owner)
	if err != nil {
		return nil, checkpoints, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return nil, checkpoints, err
	}
	var same bool
	var replyOwner, eventOwner string
	err = tx.QueryRowContext(ctx, `SELECT service_id=$2 AND binding_json=$3::jsonb,reply_owner,event_owner
		FROM futures_capture_services WHERE route_key=$1 FOR UPDATE`, w.key, w.id, w.binding).Scan(&same, &replyOwner, &eventOwner)
	if err != nil {
		return nil, checkpoints, err
	}
	if !same {
		return nil, checkpoints, ErrStreamIdentity
	}
	for _, lane := range []struct {
		role, owner string
		initial     HeartbeatCheckpoint
		out         *HeartbeatCheckpoint
	}{{"reply", replyOwner, c.InitialCheckpoints.Reply, &checkpoints.Reply}, {"event", eventOwner, c.InitialCheckpoints.Event, &checkpoints.Event}} {
		actual, err := s.captureCheckpoint(ctx, source.StreamConfig(lane.role))
		if err != nil {
			return nil, checkpoints, err
		}
		if actual.Owner != lane.owner {
			return nil, checkpoints, ErrStreamOwner
		}
		if created == 1 && actual.Cursor != lane.initial.Cursor {
			return nil, checkpoints, ErrStreamCursor
		}
		*lane.out = actual
	}
	if err := tx.Commit(); err != nil {
		return nil, checkpoints, err
	}
	return w, checkpoints, nil
}

// Rotating receiver ownership and saving the service's expected token must
// share one commit. A crash cannot leave a new owner known only to stdout.
func (w *captureServiceOwner) advance(ctx context.Context, tx *sql.Tx, role, old, next string) error {
	if !oneOf(role, "reply", "event") {
		return ErrStreamIdentity
	}
	r, err := tx.ExecContext(ctx, `UPDATE futures_capture_services SET `+role+`_owner=$4,updated_at=clock_timestamp()
		WHERE route_key=$1 AND service_id=$2 AND binding_json=$3::jsonb AND `+role+`_owner=$5`, w.key, w.id, w.binding, next, old)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return ErrStreamOwner
	}
	return nil
}
