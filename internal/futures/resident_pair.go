package futures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// The two positions are independent CAS tokens, not evidence that an old
// receiver has stopped. Operators must stop both readers before resumption.
type ResidentCheckpoints struct {
	Reply HeartbeatCheckpoint `json:"reply"`
	Event HeartbeatCheckpoint `json:"event"`
}

func DecodeResidentCheckpoints(raw []byte) (ResidentCheckpoints, error) {
	var c ResidentCheckpoints
	if len(raw) > 4096 {
		return c, ErrStreamIdentity
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || validateCaptureCheckpoint(c.Reply) != nil || validateCaptureCheckpoint(c.Event) != nil {
		return ResidentCheckpoints{}, ErrStreamIdentity
	}
	return c, nil
}

// FollowTestRawPair keeps reply and event capture in one process without
// granting query, projection, ACK or trade authority. An error/budget on either
// side cancels its peer; both finish checkpoint reporting before return.
func FollowTestRawPair(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, options ResidentCaptureOptions, checkpoints ResidentCheckpoints, emit func(ResidentCaptureProgress) error) error {
	return followTestPair(ctx, source, store, verifier, options, checkpoints, emit, false)
}

// FollowTestLocalEventPair additionally projects confirmed local order/fill and
// cancel-rejection events. Queries and external events remain raw-only.
func FollowTestLocalEventPair(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, options ResidentCaptureOptions, checkpoints ResidentCheckpoints, emit func(ResidentCaptureProgress) error) error {
	return followTestPair(ctx, source, store, verifier, options, checkpoints, emit, true)
}

func followTestPair(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, options ResidentCaptureOptions, checkpoints ResidentCheckpoints, emit func(ResidentCaptureProgress) error, project bool) error {
	return followTestPairMode(ctx, source, store, verifier, options, checkpoints, emit, project, nil)
}

func followTestPairMode(ctx context.Context, source *TestStreamSource, store *StreamStore, verifier *TestQueryPublisher, options ResidentCaptureOptions, checkpoints ResidentCheckpoints, emit func(ResidentCaptureProgress) error, project bool, service *CaptureServiceConfig) (result error) {
	deadline, bounded := ctx.Deadline()
	if (service == nil && (!bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 24*time.Hour)) || ctx.Done() == nil || source == nil || store == nil || verifier == nil || source.scope != verifier.scope || source.String() != verifier.target.SourceID || emit == nil || options.validate() != nil || validateCaptureCheckpoint(checkpoints.Reply) != nil || validateCaptureCheckpoint(checkpoints.Event) != nil {
		return ErrStreamIdentity
	}
	// Check both expected positions before changing either owner. The existing
	// openBound CAS still closes races with another receiver after this check.
	checkCtx, done := context.WithTimeout(ctx, options.StepTimeout)
	defer done()
	for _, item := range []struct {
		role string
		cp   HeartbeatCheckpoint
	}{{"reply", checkpoints.Reply}, {"event", checkpoints.Event}} {
		if service != nil {
			break // Durable service ownership is checked under the singleton lock.
		}
		actual, err := store.captureCheckpoint(checkCtx, source.StreamConfig(item.role))
		if err != nil {
			return err
		}
		if actual.Owner != item.cp.Owner {
			return ErrStreamOwner
		}
		if actual.Cursor != item.cp.Cursor {
			return ErrStreamCursor
		}
	}
	health, err := startCaptureHealth(checkCtx, store.db, source, verifier.target)
	if err != nil {
		return err
	}
	defer health.close()
	defer func() {
		if err := health.stop(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	var managed *captureServiceOwner
	if service != nil {
		for _, role := range []string{"cmd.query", "cmd.trade"} {
			if err := verifier.verifyTransportStream(checkCtx, role); err != nil {
				return err
			}
		}
		managed, checkpoints, err = store.prepareCaptureService(checkCtx, source, *service)
		if err != nil {
			return err
		}
	}
	var projector *LocalEventProjector
	var replies *ReplyProjector
	if service != nil && service.ProjectReplies {
		replies, err = NewReplyProjector(store, source.scope, verifier.target)
		if err != nil {
			return err
		}
	}
	if project {
		projector, err = NewLocalEventProjector(checkCtx, store, source.scope, verifier.target)
		if err != nil {
			_ = health.stop()
			return err
		}
	}
	var output sync.Mutex
	var heartbeat sync.Mutex
	err = runCapturePair(ctx, func(run context.Context, role string) error {
		o := options
		o.Role, o.Checkpoint = role, checkpoints.Reply
		if role == "event" {
			o.Checkpoint = checkpoints.Event
		}
		result := followTestStream(run, source, store, verifier, o, func(p ResidentCaptureProgress) error {
			output.Lock()
			defer output.Unlock()
			if project {
				p.Mode = "test_resident_local_event_projection"
			}
			if service != nil {
				p.Mode = "test_capture_service_raw"
				if project {
					p.Mode = "test_capture_service_local_events"
				}
			}
			return emit(p)
		}, func(check context.Context, h StreamHandle, p *ResidentCaptureProgress) error {
			heartbeat.Lock()
			defer heartbeat.Unlock()
			if err := check.Err(); err != nil {
				return err
			}
			if p.Phase == "backpressured" {
				// Close publication readiness before draining. Projection alone
				// must not renew ingress health without a verified subsequent read.
				if err := health.observe(check, *p); err != nil {
					return err
				}
			}
			if projector != nil {
				if role == "event" && h.owner != "" {
					page, err := projector.Step(check, h, options.PageSize)
					if err != nil {
						return err
					}
					p.LocalProjection = &page
					if page.Conflicts > 0 {
						return ErrLocalProjectionConflict
					}
				}
			}
			if replies != nil && role == "reply" && h.owner != "" {
				page, err := replies.Step(check, h, options.PageSize)
				if err != nil {
					return err
				}
				p.ReplyProjection = &page
			}
			if p.Phase == "backpressured" {
				return nil
			}
			return health.observe(check, *p)
		}, managed)
		// Close readiness as soon as one lane exits, before waiting for its
		// peer's cleanup. Never let a successful late cycle reopen it.
		heartbeat.Lock()
		stopErr := health.stop()
		heartbeat.Unlock()
		if stopErr != nil {
			return errors.Join(result, stopErr)
		}
		return result
	})
	return err
}

func runCapturePair(ctx context.Context, follow func(context.Context, string) error) error {
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	for _, role := range []string{"reply", "event"} {
		go func(role string) { results <- follow(run, role) }(role)
	}
	var failures []error
	for range 2 {
		err := <-results
		cancel()
		// Only exact cancellation is normal. Joined checkpoint/output errors
		// must survive even when they also contain context.Canceled.
		if err != context.Canceled && err != context.DeadlineExceeded {
			if err == nil {
				err = errors.New("resident capture stopped unexpectedly")
			}
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("resident capture stopped without caller cancellation")
}
