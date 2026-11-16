package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// QueryRequestReview is local operator-reviewed context, not an OC wire schema
// or an automatically authenticated statement. Evidence is retained separately.
type QueryRequestReview struct {
	CommandID      int64           `json:"command_id"`
	BatchID        string          `json:"batch_id"`
	ReviewID       string          `json:"review_id"`
	EvidenceSHA256 string          `json:"evidence_sha256"`
	Target         TestQueryTarget `json:"target"`
	Query          QueryContext    `json:"query_context"`
	TimeoutMS      int64           `json:"timeout_ms"`
}

func DecodeQueryRequestReview(raw, evidence []byte) (QueryRequestReview, error) {
	var r QueryRequestReview
	if len(raw) > 16384 || len(evidence) == 0 || len(evidence) > 262144 {
		return r, ErrBatchIdentity
	}
	if _, err := parseReply(raw); err != nil {
		return r, ErrBatchIdentity
	}
	obj, err := shape(raw, []string{"command_id", "batch_id", "review_id", "evidence_sha256", "target", "query_context", "timeout_ms"}, nil)
	if err != nil {
		return r, ErrBatchIdentity
	}
	qc, err := shape(obj["query_context"], []string{"Route", "Action", "OriginMessageID", "Ticket", "CounterSessionID", "ConnectionGeneration", "SDKRequestID", "TradeDate", "NativeBrokerID", "NativeInvestorID", "NativeUserID"}, nil)
	if err != nil {
		return r, ErrBatchIdentity
	}
	if _, err = shape(qc["Route"], []string{"env", "broker_id", "gateway_id", "account_id"}, nil); err != nil {
		return r, ErrBatchIdentity
	}
	if _, err = DecodeTestQueryTarget(obj["target"]); err != nil {
		return r, ErrBatchIdentity
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, ErrBatchIdentity
	}
	sum := sha256.Sum256(evidence)
	c := r.Query
	if r.CommandID <= 0 || !validID(r.BatchID) || len(r.BatchID) > 256 || !validID(r.ReviewID) || len(r.ReviewID) > 128 ||
		r.EvidenceSHA256 != hex.EncodeToString(sum[:]) || r.Target.Validate() != nil || c.Command != nil || c.commandScope().validate() != nil ||
		c.Route.Env != "test" || !(c.Route.BrokerID == "ctp" || strings.HasPrefix(c.Route.BrokerID, "ctp_")) ||
		c.CounterSessionID != r.Target.CounterSessionID || c.TradeDate != r.Target.TradeDate ||
		c.ConnectionGeneration == nil || *c.ConnectionGeneration != r.Target.ConnectionGeneration || r.TimeoutMS < 1 || r.TimeoutMS > 300000 {
		return r, ErrBatchIdentity
	}
	if _, err := NewQueryBatch(c, 0, time.Duration(r.TimeoutMS)*time.Millisecond, DefaultBatchLimits()); err != nil {
		return r, err
	}
	return r, nil
}

// RegisterReviewedQuery commits the original request binding and immutable
// review together. It accepts only a confirmed publication to the exact target.
// A new process cannot silently take over an old query batch.
func (s *QueryStore) RegisterReviewedQuery(ctx context.Context, raw, evidence []byte) (QueryRequestReview, time.Time, error) {
	r, err := DecodeQueryRequestReview(raw, evidence)
	if err != nil {
		return r, time.Time{}, err
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return r, time.Time{}, err
	}
	defer tx.Rollback()
	var started time.Time
	var state string
	var same bool
	target, _ := json.Marshal(r.Target)
	// Use the same lock order as registration/publication.
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT command_id FROM futures_commands WHERE command_id=$1 AND route_key=$2 FOR UPDATE`, r.CommandID, string(commandRouteBytes(r.Query.Route))).Scan(&id)
	if err != nil {
		return r, started, err
	}
	err = tx.QueryRowContext(ctx, `SELECT state,attempted_at,target_json=$2::jsonb FROM futures_test_query_publications WHERE command_id=$1`, r.CommandID, target).Scan(&state, &started, &same)
	if err != nil {
		return r, started, err
	}
	if state != "published" || !same || started.After(time.Now()) {
		return r, started, ErrBatchIdentity
	}
	timeout := time.Duration(r.TimeoutMS) * time.Millisecond
	if time.Since(started) >= timeout {
		return r, started, fmt.Errorf("original query receive deadline elapsed; retain raw replies for review")
	}
	if err = s.registerCommandTx(ctx, tx, r.BatchID, r.CommandID, r.Query, timeout, DefaultBatchLimits()); err != nil {
		return r, started, err
	}
	mHash, eHash := sha256.Sum256(raw), sha256.Sum256(evidence)
	_, err = tx.ExecContext(ctx, `INSERT INTO futures_test_query_reviews(command_id,batch_id,manifest,manifest_sha256,evidence,evidence_sha256) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, r.CommandID, r.BatchID, raw, mHash[:], evidence, eHash[:])
	if err != nil {
		return r, started, err
	}
	err = tx.QueryRowContext(ctx, `SELECT batch_id=$2 AND manifest=$3 AND manifest_sha256=$4 AND evidence=$5 AND evidence_sha256=$6 FROM futures_test_query_reviews WHERE command_id=$1`, r.CommandID, r.BatchID, raw, mHash[:], evidence, eHash[:]).Scan(&same)
	if err != nil || !same {
		return r, started, ErrBatchIdentity
	}
	return r, started, tx.Commit()
}

func validateReplyCapture(c StreamConfig) error {
	if c.Role != "reply" || c.Scope.Route.Env != "test" || !(c.Scope.Route.BrokerID == "ctp" || strings.HasPrefix(c.Scope.Route.BrokerID, "ctp_")) {
		return ErrStreamIdentity
	}
	_, err := c.stream()
	return err
}

func (s *StreamStore) ReplyCheckpoint(ctx context.Context, c StreamConfig) (HeartbeatCheckpoint, error) {
	if err := validateReplyCapture(c); err != nil {
		return HeartbeatCheckpoint{}, err
	}
	return s.captureCheckpoint(ctx, c)
}

func (s *StreamStore) OpenReplyCapture(ctx context.Context, c StreamConfig, expected HeartbeatCheckpoint, target TestQueryTarget) (StreamHandle, error) {
	if err := validateReplyCapture(c); err != nil {
		return StreamHandle{}, err
	}
	if target.Validate() != nil || target.SourceID != c.SourceID {
		return StreamHandle{}, ErrStreamIdentity
	}
	if err := validateCaptureCheckpoint(expected); err != nil {
		return StreamHandle{}, err
	}
	return s.openBound(ctx, c, &expected, &target)
}

type QueryReceiveProgress struct {
	Phase              string          `json:"phase"`
	At                 time.Time       `json:"at"`
	Owner              string          `json:"owner"`
	Cursor             string          `json:"cursor"`
	Captured           int             `json:"captured"`
	Delivered          int             `json:"delivered"`
	Pending            int             `json:"pending"`
	State              BatchState      `json:"query_state,omitempty"`
	Snapshot           *StoredSnapshot `json:"snapshot,omitempty"`
	BusinessAuthorized bool            `json:"business_authorized"`
}

type TestQueryReceiveOptions struct {
	Checkpoint         HeartbeatCheckpoint
	PageSize, MaxPages int
	Review, Evidence   []byte
}

// ReceiveTestQueryReplies captures before applying, never sends commands or
// ACKs OC groups. Raw-only mode is deliberately independent of heartbeat ready.
// Reviewed mode uses the original publication clock, not a new timeout on restart.
func ReceiveTestQueryReplies(ctx context.Context, source *TestStreamSource, store *StreamStore, publisher *TestQueryPublisher, options TestQueryReceiveOptions, emit func(QueryReceiveProgress) error) (result error) {
	deadline, ok := ctx.Deadline()
	if source == nil || store == nil || publisher == nil || source.scope != publisher.scope || source.sourceID != publisher.target.SourceID || emit == nil ||
		!ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Minute || options.PageSize < 1 || options.PageSize > 256 || options.MaxPages < 1 || options.MaxPages > 256 {
		return ErrStreamIdentity
	}
	var review QueryRequestReview
	var queries *QueryStore
	var started time.Time
	var err error
	reviewed := len(options.Review) > 0
	if reviewed {
		review, err = DecodeQueryRequestReview(options.Review, options.Evidence)
		if err != nil || review.Query.commandScope() != source.scope || review.Target != publisher.target {
			return ErrBatchIdentity
		}
	} else if len(options.Evidence) > 0 {
		return ErrBatchIdentity
	}
	// Physical source identity is checked even when a stopped OC's raw replies
	// are being archived. Unlike query publishing, no live-ready flag is required.
	if err = publisher.verifyTransport(ctx); err != nil {
		return err
	}
	// OC creates reply lazily. Do not bind/retire a query merely because the
	// first response stream has not appeared yet; leave the durable intent alone.
	if _, err = source.reader.client.XInfoStream(ctx, queryReplyStream(source.scope.Route)).Result(); err != nil {
		return fmt.Errorf("TEST reply stream unavailable; receiver and query binding not opened")
	}
	h, err := store.OpenReplyCapture(ctx, source.StreamConfig("reply"), options.Checkpoint, publisher.target)
	if err != nil {
		return err
	}
	p := QueryReceiveProgress{Owner: h.owner, Cursor: options.Checkpoint.Cursor}
	report := func(phase string) error {
		p.Phase = phase
		p.At = time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600))
		return emit(p)
	}
	if err = report("opened"); err != nil {
		return err
	}
	var dispatch *StreamDispatcher
	if reviewed {
		if _, err = publisher.inspect(ctx); err != nil {
			return err
		}
		queries, _ = NewQueryStore(store.db)
		review, started, err = queries.RegisterReviewedQuery(ctx, options.Review, options.Evidence)
		if err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := queries.Retire(cleanup, review.BatchID, "test_query_receiver_stopped")
			result = errors.Join(result, err)
		}()
		dispatch, err = NewStreamDispatcher(ctx, store, h, queries, nil, []QueryDispatchBinding{{BatchID: review.BatchID, StartedAt: started}})
		if err != nil {
			return err
		}
	}
	var after int64
	for page := 0; page < options.MaxPages; page++ {
		if err = publisher.verifyTransport(ctx); err != nil {
			return err
		}
		capture, err := source.ReadOnce(ctx, store, h, options.PageSize)
		if err != nil {
			return err
		}
		p.Captured += len(capture.ReceiptIDs)
		p.Cursor = capture.Cursor
		if err = publisher.verifyTransport(ctx); err != nil {
			return err
		}
		if reviewed {
			if _, err = publisher.inspect(ctx); err != nil {
				return err
			}
			d, err := dispatch.Dispatch(ctx, after, options.PageSize)
			if err != nil {
				return err
			}
			after = d.NextAfterID
			p.Delivered += d.Delivered
			p.State, err = queries.Advance(ctx, review.BatchID, time.Since(started))
			if err != nil {
				return err
			}
			if _, err = publisher.inspect(ctx); err != nil {
				return err
			}
			// Finish a complete pending scan before reporting a result, so later
			// retained contradictions in this capture are not silently ignored.
			if p.State != BatchCollecting && d.CycleComplete && len(capture.ReceiptIDs) < options.PageSize {
				if p.State == BatchReady {
					if isTradeQuery(review.Query.Action) {
						if _, err = queries.ApplyTradeQuery(ctx, review.BatchID); err != nil {
							return err
						}
					}
					snapshot, err := queries.Snapshot(ctx, review.BatchID)
					if err != nil {
						return err
					}
					p.Snapshot = &snapshot
				}
				status, err := store.Status(ctx, h)
				if err != nil {
					return err
				}
				p.Pending = status.Pending
				if err = report("query_terminal"); err != nil {
					return err
				}
				if p.State != BatchReady {
					return fmt.Errorf("query ended in %s; not a complete snapshot", p.State)
				}
				return nil
			}
		}
		status, err := store.Status(ctx, h)
		if err != nil {
			return err
		}
		p.Pending = status.Pending
		if err = report("captured"); err != nil {
			return err
		}
		if !reviewed && len(capture.ReceiptIDs) < options.PageSize {
			return report("idle")
		}
		if reviewed {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err = report("page_budget"); err != nil {
		return err
	}
	if reviewed {
		return fmt.Errorf("query receive page budget exhausted; incomplete result retained")
	}
	return nil
}
