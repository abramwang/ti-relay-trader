package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Owner is a CAS token, not proof a process is alive or has stopped.
type HeartbeatCheckpoint struct {
	Registered bool   `json:"registered"`
	Owner      string `json:"owner"`
	Cursor     string `json:"cursor"`
	Records    int    `json:"records"`
	Bytes      int64  `json:"bytes"`
}

func validateHeartbeatCapture(c StreamConfig) error {
	if c.Role != "hb" || !emptyNativeScope(c.Scope) {
		return ErrStreamIdentity
	}
	_, err := c.stream()
	return err
}

// HeartbeatCheckpoint reads without opening/taking over a receiver or writing.
func (s *StreamStore) HeartbeatCheckpoint(ctx context.Context, c StreamConfig) (HeartbeatCheckpoint, error) {
	if err := validateHeartbeatCapture(c); err != nil {
		return HeartbeatCheckpoint{}, err
	}
	key, _ := c.stream()
	raw, _ := json.Marshal(c)
	out := HeartbeatCheckpoint{Cursor: "0-0"}
	var same bool
	err := s.db.QueryRowContext(ctx, `SELECT owner_id,cursor_id,audit_count,audit_bytes,config_json=$2::jsonb FROM futures_stream_receivers WHERE stream_key=$1`, key, raw).Scan(&out.Owner, &out.Cursor, &out.Records, &out.Bytes, &same)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return HeartbeatCheckpoint{}, err
	}
	if !same {
		return HeartbeatCheckpoint{}, ErrStreamIdentity
	}
	if _, err := streamID(out.Cursor); err != nil {
		return HeartbeatCheckpoint{}, err
	}
	out.Registered = true
	return out, nil
}

// New receivers require empty owner and 0-0. Resumption requires an explicit
// previous owner/cursor pair AND the caller must have stopped its old reader.
// Registration uses a row-lock CAS, never a read-then-unconditional takeover.
func (s *StreamStore) OpenHeartbeatCapture(ctx context.Context, c StreamConfig, expected HeartbeatCheckpoint) (StreamHandle, error) {
	if err := validateHeartbeatCapture(c); err != nil {
		return StreamHandle{}, err
	}
	if expected.Owner != "" && (!validID(expected.Owner) || len(expected.Owner) != 64) {
		return StreamHandle{}, ErrStreamOwner
	}
	if _, err := streamID(expected.Cursor); err != nil {
		return StreamHandle{}, err
	}
	if expected.Owner == "" && expected.Cursor != "0-0" {
		return StreamHandle{}, ErrStreamCursor
	}
	return s.open(ctx, c, &expected)
}

type HeartbeatCaptureOptions struct{ PageSize, MaxPages int }

type HeartbeatCaptureProgress struct {
	Phase              string    `json:"phase"`
	At                 time.Time `json:"at"`
	Stream             string    `json:"stream"`
	SourceID           string    `json:"source_id"`
	Owner              string    `json:"owner"`
	Cursor             string    `json:"cursor"`
	Captured           int       `json:"captured"`
	Pages              int       `json:"pages"`
	BusinessAuthorized bool      `json:"business_authorized"`
}

// CaptureTestHeartbeats performs a bounded raw capture, not a daemon, query
// dispatch, or heartbeat observer. It stops on idle, page budget, or any error.
// Successfully committed pages survive later errors; output is not a checkpoint.
// The synchronous emit callback must return promptly; it must not authorize work.
func CaptureTestHeartbeats(ctx context.Context, source *TestStreamSource, store *StreamStore, expected HeartbeatCheckpoint, options HeartbeatCaptureOptions, emit func(HeartbeatCaptureProgress) error) error {
	deadline, ok := ctx.Deadline()
	if source == nil || store == nil || emit == nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Minute ||
		options.PageSize < 1 || options.PageSize > 256 || options.MaxPages < 1 || options.MaxPages > 256 {
		return ErrStreamIdentity
	}
	c := source.StreamConfig("hb")
	h, err := store.OpenHeartbeatCapture(ctx, c, expected)
	if err != nil {
		return err
	}
	p := HeartbeatCaptureProgress{Stream: h.stream, SourceID: c.SourceID, Owner: h.owner, Cursor: expected.Cursor}
	report := func(phase string) error {
		p.Phase, p.At = phase, time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600))
		return emit(p)
	}
	if err := report("opened"); err != nil {
		return fmt.Errorf("capture output failed: %w", err)
	}
	for p.Pages < options.MaxPages {
		if err := ctx.Err(); err != nil {
			return err
		}
		captured, err := source.ReadOnce(ctx, store, h, options.PageSize)
		if err != nil {
			return err
		}
		p.Pages++
		p.Captured += len(captured.ReceiptIDs)
		p.Cursor = captured.Cursor
		phase := "captured"
		if len(captured.ReceiptIDs) < options.PageSize {
			phase = "idle"
		} else if p.Pages == options.MaxPages {
			phase = "page_budget"
		}
		if err := report(phase); err != nil {
			return fmt.Errorf("capture output failed: %w", err)
		}
		if phase != "captured" {
			return nil
		}
	}
	return nil
}
