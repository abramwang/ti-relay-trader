package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

var ErrResidentCaptureBudget = errors.New("TEST resident capture record budget reached")

type ResidentCaptureOptions struct {
	Role                            string
	Checkpoint                      HeartbeatCheckpoint
	PageSize, MaxRecords            int
	MinDelay, MaxDelay, StepTimeout time.Duration
	ReportInterval                  time.Duration
}

func DefaultResidentCaptureOptions() ResidentCaptureOptions {
	return ResidentCaptureOptions{Role: "reply", Checkpoint: HeartbeatCheckpoint{Cursor: "0-0"},
		PageSize: 32, MaxRecords: maxTradeAuditCount, MinDelay: 100 * time.Millisecond,
		MaxDelay: 5 * time.Second, StepTimeout: 10 * time.Second, ReportInterval: 30 * time.Second}
}

type ResidentCaptureProgress struct {
	Mode               string               `json:"mode"`
	Phase              string               `json:"phase"`
	At                 time.Time            `json:"at"`
	Stream             string               `json:"stream"`
	SourceID           string               `json:"source_id"`
	Checkpoint         HeartbeatCheckpoint  `json:"checkpoint"`
	CheckpointVerified bool                 `json:"checkpoint_verified"`
	Captured           int                  `json:"captured"`
	StopReason         string               `json:"stop_reason,omitempty"`
	BusinessAuthorized bool                 `json:"business_authorized"`
	LocalProjection    *LocalProjectionPage `json:"local_projection,omitempty"`
	ReplyProjection    *ReplyProjectionPage `json:"reply_projection,omitempty"`
}

func (o ResidentCaptureOptions) validate() error {
	if !oneOf(o.Role, "reply", "event") || o.PageSize < 1 || o.PageSize > 256 || o.MaxRecords < 1 || o.MaxRecords > maxTradeAuditCount ||
		o.MinDelay < 10*time.Millisecond || o.MaxDelay < o.MinDelay || o.MaxDelay > time.Minute ||
		o.StepTimeout < time.Second || o.StepTimeout > time.Minute || o.ReportInterval < time.Second || o.ReportInterval > time.Minute {
		return ErrStreamIdentity
	}
	return validateCaptureCheckpoint(o.Checkpoint)
}

// FollowTestRawStream owns raw ingress only, never query projection, command
// publication or OC ACKs. It can retain late/stopping/foreign-session bodies;
// those bytes do not acquire business authority by being captured.
// The caller owns source/publisher closing and must stop any prior receiver.
func FollowTestRawStream(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, o ResidentCaptureOptions, emit func(ResidentCaptureProgress) error) (result error) {
	return followTestRawStream(ctx, source, store, verifier, o, emit, nil)
}

func followTestRawStream(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, o ResidentCaptureOptions, emit func(ResidentCaptureProgress) error, observe func(context.Context, StreamHandle, *ResidentCaptureProgress) error) (result error) {
	return followTestStream(ctx, source, store, verifier, o, emit, observe, nil)
}

func followTestStream(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, o ResidentCaptureOptions, emit func(ResidentCaptureProgress) error, observe func(context.Context, StreamHandle, *ResidentCaptureProgress) error, service *captureServiceOwner) (result error) {
	deadline, ok := ctx.Deadline()
	if (service == nil && (!ok || time.Until(deadline) <= 0 || time.Until(deadline) > 24*time.Hour)) || ctx.Done() == nil || o.validate() != nil ||
		source == nil || store == nil || verifier == nil || emit == nil || source.scope != verifier.scope || source.scope.Route.Env != "test" || source.sourceID != verifier.target.SourceID {
		return ErrStreamIdentity
	}
	cfg := source.StreamConfig(o.Role)
	key, err := cfg.stream()
	if err != nil {
		return err
	}
	p := ResidentCaptureProgress{Mode: "test_resident_raw_capture", Stream: key, SourceID: source.String(), Checkpoint: HeartbeatCheckpoint{Cursor: "0-0"}}
	var h StreamHandle
	var opened bool
	lastReport := time.Time{}
	report := func(phase string) error {
		p.Phase, p.At = phase, time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600))
		lastReport = time.Now()
		return emit(p)
	}
	defer func() {
		p.CheckpointVerified = false
		if opened {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Recover the durable checkpoint even if the last commit response or
			// progress output was lost. Never steal another owner's checkpoint.
			if status, err := store.Status(cleanup, h); err == nil {
				cp, err := store.captureCheckpoint(cleanup, cfg)
				if err == nil && cp.Owner == h.owner && cp.Cursor == status.Cursor {
					p.Checkpoint, p.CheckpointVerified = cp, true
				} else {
					result = errors.Join(result, ErrStreamOwner)
				}
			} else {
				result = errors.Join(result, err)
			}
		}
		switch {
		case result == ErrResidentCaptureBudget:
			p.StopReason = "record_budget"
		case result == context.DeadlineExceeded:
			p.StopReason = "duration_elapsed"
		case result == context.Canceled:
			p.StopReason = "cancelled"
		default:
			p.StopReason = "capture_failed"
		}
		if err := report("stopped"); err != nil {
			result = errors.Join(result, err)
		}
	}()

	stepCtx, cancel := context.WithTimeout(ctx, o.StepTimeout)
	actual, err := store.captureCheckpoint(stepCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	if actual.Owner != o.Checkpoint.Owner {
		return ErrStreamOwner
	}
	if actual.Cursor != o.Checkpoint.Cursor {
		return ErrStreamCursor
	}
	p.Checkpoint, p.CheckpointVerified = actual, true
	delay := o.MinDelay
	var backlog *streamBackpressure
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stepCtx, cancel := context.WithTimeout(ctx, o.StepTimeout)
		newRecords, waiting, err := func() (int, bool, error) {
			suffix := "cmd.query"
			if o.Role == "event" {
				suffix = "cmd.trade"
			}
			if err := verifier.verifyTransportStream(stepCtx, suffix); err != nil {
				return 0, false, err
			}
			if !opened {
				kind, err := source.reader.client.Type(stepCtx, key).Result()
				if err != nil {
					return 0, false, err
				}
				if kind == "none" {
					if actual.Registered {
						return 0, false, ErrStreamHistory
					}
					// OC creates output streams lazily. Waiting does not register
					// a receiver or create a stream/group in Redis.
					return 0, true, nil
				}
				if kind != "stream" {
					return 0, false, ErrStreamHistory
				}
				h, err = store.openBoundManaged(stepCtx, cfg, &o.Checkpoint, &verifier.target, service)
				if err != nil {
					return 0, false, err
				}
				opened = true
				p.Checkpoint.Owner, p.Checkpoint.Registered = h.owner, true
			}
			if err := verifyResidentTail(stepCtx, source, store, h); err != nil {
				return 0, false, err
			}
			if backlog != nil {
				// Keep validating the source while paused, without repeatedly
				// reading a page that cannot fit or retaining its body in memory.
				info, err := source.reader.client.XInfoStream(stepCtx, key).Result()
				if err != nil {
					return 0, false, err
				}
				if err := checkStreamHistory(info, p.Checkpoint.Cursor); err != nil {
					return 0, false, err
				}
				usage, err := store.Status(stepCtx, h)
				if err != nil {
					return 0, false, err
				}
				if usage.Cursor != p.Checkpoint.Cursor {
					return 0, false, ErrStreamCursor
				}
				if err := usage.admitCapture(backlog.count, backlog.bytes); err != nil {
					if errors.Is(err, ErrStreamBacklog) {
						return 0, false, nil
					}
					return 0, false, err
				}
			}
			limit := o.PageSize
			if service == nil {
				limit = min(limit, o.MaxRecords-p.Captured)
			}
			captured, err := source.ReadOnce(stepCtx, store, h, limit)
			if err != nil {
				if errors.As(err, &backlog) {
					return 0, false, nil
				}
				return 0, false, err
			}
			backlog = nil
			p.Captured += len(captured.ReceiptIDs)
			p.Checkpoint.Cursor = captured.Cursor
			if err := verifier.verifyTransportStream(stepCtx, suffix); err != nil {
				return 0, false, err
			}
			if err := verifyResidentTail(stepCtx, source, store, h); err != nil {
				return 0, false, err
			}
			cp, err := store.captureCheckpoint(stepCtx, cfg)
			if err != nil {
				return 0, false, err
			}
			if cp.Owner != h.owner {
				return 0, false, ErrStreamOwner
			}
			p.Checkpoint, p.CheckpointVerified = cp, true
			return len(captured.ReceiptIDs), false, nil
		}()
		cancel()
		if err != nil {
			if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				return ctx.Err()
			}
			return err
		}
		phase := "idle"
		if backlog != nil {
			phase = "backpressured"
		} else if waiting {
			phase = "waiting_stream"
		} else if newRecords > 0 {
			phase = "captured"
		}
		phaseChanged := p.Phase != phase
		p.Phase = phase
		if observe != nil {
			stepCtx, cancel := context.WithTimeout(ctx, o.StepTimeout)
			err := observe(stepCtx, h, &p)
			cancel()
			if err != nil {
				return err
			}
		}
		if phaseChanged || newRecords > 0 || (p.LocalProjection != nil && (p.LocalProjection.Applied+p.LocalProjection.Conflicts) > 0) || (p.ReplyProjection != nil && p.ReplyProjection.Delivered+p.ReplyProjection.CommandWorkReleased > 0) || time.Since(lastReport) >= o.ReportInterval {
			if err := report(phase); err != nil {
				return fmt.Errorf("resident capture output failed: %w", err)
			}
		}
		if service == nil && p.Captured >= o.MaxRecords {
			return ErrResidentCaptureBudget
		}
		projectionProgress := p.LocalProjection != nil && (p.LocalProjection.Applied > 0 || (!p.LocalProjection.CycleComplete && p.LocalProjection.Scanned > 0))
		projectionProgress = projectionProgress || (p.ReplyProjection != nil && (p.ReplyProjection.Delivered+p.ReplyProjection.CommandWorkReleased > 0 || (!p.ReplyProjection.CycleComplete && p.ReplyProjection.Scanned > 0)))
		delay = consumerDelay(delay, TestConsumerOptions{MinDelay: o.MinDelay, MaxDelay: o.MaxDelay}, newRecords > 0 || projectionProgress)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Compare the exact retained frontier before and after reads. This detects a
// missing/rewritten cursor even if the Redis run_id and numeric tail survived.
// It is not a proof that every older entry or a perfectly restored copy survived.
func verifyResidentTail(ctx context.Context, source *TestStreamSource, store *StreamStore, h StreamHandle) error {
	tx, err := beginFuturesTransaction(ctx, store.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cursor, count, _, err := lockStream(ctx, tx, h)
	if err != nil {
		return err
	}
	if cursor == "0-0" {
		if count != 0 {
			return ErrStreamCursor
		}
		return tx.Commit()
	}
	var raw, digest []byte
	if err := tx.QueryRowContext(ctx, `SELECT body,body_sha256 FROM futures_stream_inbox WHERE stream_key=$1 AND entry_id=$2`, h.stream, cursor).Scan(&raw, &digest); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if !bytes.Equal(digest, sum[:]) {
		return ErrStreamIdentity
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	response, err := source.reader.client.Do(ctx, "XRANGE", h.stream, cursor, cursor, "COUNT", 1).Result()
	if err != nil {
		return err
	}
	rows, err := decodeStreamRead(h.stream, "0-0", 1, []any{[]any{h.stream, response}})
	if err != nil || len(rows) != 1 || rows[0].EntryID != cursor || !bytes.Equal(rows[0].Raw, raw) {
		return ErrStreamHistory
	}
	return nil
}
