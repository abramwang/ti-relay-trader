package futures

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrCaptureUnavailable = errors.New("futures reply/event capture unavailable")

// Fifteen seconds covers the default five-second idle backoff cap, not
// an unlimited grace period. PG clock timestamps avoid process-clock renewal.
const captureHealthTTL = 15 * time.Second

type CaptureHealthView struct {
	Ready      bool      `json:"ready"`
	State      string    `json:"state"`
	ValidUntil time.Time `json:"valid_until"`
}

type captureHealthBinding struct {
	Scope    CommandScope `json:"scope"`
	SourceID string       `json:"source_id"`
	RunID    string       `json:"server_run_id"`
	Epoch    string       `json:"epoch"`
	Group    string       `json:"group"`
}

func captureBinding(scope CommandScope, t TestQueryTarget) []byte {
	raw, _ := json.Marshal(captureHealthBinding{scope, t.SourceID, t.ServerRunID, t.Epoch, t.Group})
	return raw
}

type captureHealthWriter struct {
	conn       *sql.Conn
	key, owner string
	streams    map[string]string
}

func startCaptureHealth(ctx context.Context, db *sql.DB, source *TestStreamSource, target TestQueryTarget) (*captureHealthWriter, error) {
	if source == nil || source.scope.Route.Env != "test" || target.Validate() != nil || source.String() != target.SourceID {
		return nil, ErrStreamIdentity
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := string(commandRouteBytes(source.scope.Route))
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "ctp.capture.health.v1/"+key).Scan(&locked); err != nil || !locked {
		if err != nil {
			discardCaptureConnection(conn)
		} else {
			conn.Close()
		}
		return nil, ErrCaptureUnavailable
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		discardCaptureConnection(conn)
		return nil, err
	}
	w := &captureHealthWriter{conn: conn, key: key, owner: hex.EncodeToString(nonce[:]), streams: map[string]string{}}
	for _, role := range []string{"reply", "event"} {
		w.streams[role], _ = source.StreamConfig(role).stream()
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO futures_capture_health(route_key,owner_id,binding_json,state) VALUES($1,$2,$3,'starting') ON CONFLICT(route_key) DO UPDATE SET owner_id=excluded.owner_id,binding_json=excluded.binding_json,state='starting',reply_checked_at=NULL,event_checked_at=NULL,reply_owner='',event_owner='',reply_backpressured=false,event_backpressured=false,updated_at=clock_timestamp()`, key, w.owner, captureBinding(source.scope, target))
	if err != nil {
		w.close()
		return nil, err
	}
	return w, nil
}

// Do not return a session holding advisory locks to the connection pool, even
// when unlock failed or the network outcome is unknown.
func discardCaptureConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func (w *captureHealthWriter) observe(ctx context.Context, p ResidentCaptureProgress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	role := ""
	for k, v := range w.streams {
		if v == p.Stream {
			role = k
		}
	}
	if role == "" || !p.CheckpointVerified || !oneOf(p.Phase, "captured", "idle", "waiting_stream", "backpressured") {
		return ErrCaptureUnavailable
	}
	// Column names come from the two hard-coded roles, never input SQL.
	other := "event"
	if role == "event" {
		other = "reply"
	}
	// SIGTERM must not tear down the connection holding our advisory lock in
	// the middle of a health write, before the final stopped update can commit.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	result, err := w.conn.ExecContext(writeCtx, `UPDATE futures_capture_health SET `+role+`_checked_at=CASE WHEN $4 THEN NULL ELSE clock_timestamp() END,`+role+`_owner=$3,`+role+`_backpressured=$4,state=CASE WHEN $4 OR `+other+`_backpressured THEN 'backpressured' WHEN `+other+`_checked_at IS NULL THEN 'starting' ELSE 'running' END,updated_at=clock_timestamp() WHERE route_key=$1 AND owner_id=$2 AND state<>'stopped'`, w.key, w.owner, p.Checkpoint.Owner, p.Phase == "backpressured")
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrCaptureUnavailable
	}
	return nil
}

func (w *captureHealthWriter) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := w.conn.ExecContext(ctx, `UPDATE futures_capture_health SET state='stopped',updated_at=clock_timestamp() WHERE route_key=$1 AND owner_id=$2`, w.key, w.owner)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return ErrCaptureUnavailable
	}
	return nil
}

func (w *captureHealthWriter) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var unlocked bool
	if w.conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "ctp.capture.health.v1/"+w.key).Scan(&unlocked) != nil || !unlocked {
		discardCaptureConnection(w.conn)
		return
	}
	w.conn.Close()
}

func readCaptureHealth(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope CommandScope, target TestQueryTarget) (CaptureHealthView, string, error) {
	v := CaptureHealthView{State: "unavailable"}
	if scope.Route.Env != "test" || target.Validate() != nil {
		return v, "", ErrCaptureUnavailable
	}
	var owner, state string
	var same, fresh, owners bool
	var until sql.NullTime
	prefix := queryReplyStream(scope.Route)
	event := prefix[:len(prefix)-len("reply")] + "event"
	err := db.QueryRowContext(ctx, `SELECT h.owner_id,h.state,h.binding_json=$2::jsonb,LEAST(h.reply_checked_at,h.event_checked_at)+interval '15 seconds',COALESCE(h.reply_checked_at<=clock_timestamp() AND h.event_checked_at<=clock_timestamp() AND h.reply_checked_at>clock_timestamp()-interval '15 seconds' AND h.event_checked_at>clock_timestamp()-interval '15 seconds',false),COALESCE((SELECT owner_id FROM futures_stream_receivers WHERE stream_key=$3),'')=h.reply_owner AND COALESCE((SELECT owner_id FROM futures_stream_receivers WHERE stream_key=$4),'')=h.event_owner FROM futures_capture_health h WHERE route_key=$1`, string(commandRouteBytes(scope.Route)), captureBinding(scope, target), prefix, event).Scan(&owner, &state, &same, &until, &fresh, &owners)
	if err != nil {
		return v, "", ErrCaptureUnavailable
	}
	v.State = state
	v.ValidUntil = until.Time
	v.Ready = same && fresh && owners && state == "running" && until.Valid
	if !v.Ready {
		if !same || !owners {
			v.State = "binding_changed"
		} else if state == "running" {
			v.State = "stale"
		}
		return v, "", ErrCaptureUnavailable
	}
	return v, owner, nil
}

type captureGuardTransport struct {
	inner  queryPublicationTransport
	db     *sql.DB
	scope  CommandScope
	target TestQueryTarget
	owner  string
}

type CapturePublishProof struct {
	Owner      string    `json:"owner"`
	ValidUntil time.Time `json:"valid_until"`
}

func (g *captureGuardTransport) inspect(ctx context.Context) (queryPublishProof, error) {
	v, owner, err := readCaptureHealth(ctx, g.db, g.scope, g.target)
	if err != nil {
		return queryPublishProof{}, err
	}
	p, err := g.inner.inspect(ctx)
	if err != nil {
		return p, err
	}
	g.owner = owner
	p.Capture = &CapturePublishProof{owner, v.ValidUntil}
	if v.ValidUntil.Before(p.ExpiresAt) {
		p.ExpiresAt = v.ValidUntil
	}
	return p, nil
}

func (g *captureGuardTransport) appendQuery(ctx context.Context, p queryPublishProof, raw []byte) (string, bool, error) {
	v, owner, err := readCaptureHealth(ctx, g.db, g.scope, g.target)
	if err != nil || owner != g.owner || p.Capture == nil || p.Capture.Owner != owner {
		return "", true, nil
	} // Proven no transport call, not an unknown send.
	if v.ValidUntil.Before(p.ExpiresAt) {
		p.ExpiresAt = v.ValidUntil
	}
	return g.inner.appendQuery(ctx, p, raw)
}
