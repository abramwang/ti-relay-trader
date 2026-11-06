package futures

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"
)

type TestConsumerOptions struct {
	PageSize                        int
	MinDelay, MaxDelay, StepTimeout time.Duration
}

func DefaultTestConsumerOptions() TestConsumerOptions {
	return TestConsumerOptions{32, 100 * time.Millisecond, 5 * time.Second, 30 * time.Second}
}

type ConsumerCycle struct {
	Captured, Delivered, StateChanges int
	Progress, Stopped                 bool
	Delay                             time.Duration
	Quality                           DeliveryQuality
	Failure                           string
}

type consumerQuery struct {
	QueryDispatchBinding
	action string
}

// TestConsumer is a single-use, TEST-only orchestration prototype. Run requires
// an explicit bounded window, not a claim of live heartbeat authentication.
// The caller must cancel that window when verified session authority is revoked.
type TestConsumer struct {
	source               *TestStreamSource
	event, reply         *StreamDispatcher
	events               *EventStore
	queries              *QueryStore
	expected             DeliveryQualityContext
	options              TestConsumerOptions
	eventKey             string
	requests             []consumerQuery
	eventScan, replyScan int64
	processScan          int64
	used                 atomic.Bool
}

func NewTestConsumer(ctx context.Context, source *TestStreamSource, event, reply *StreamDispatcher, events *EventStore, expected DeliveryQualityContext, options TestConsumerOptions) (*TestConsumer, error) {
	if source == nil || event == nil || reply == nil || events == nil || event.store != reply.store || events.db != event.store.db ||
		event.handle.config.Role != "event" || reply.handle.config.Role != "reply" || !source.matches(event.handle) || !source.matches(reply.handle) ||
		len(event.events) != 1 || len(reply.replies) < 1 || len(reply.replies) > 4 || reply.queries == nil ||
		options.PageSize < 1 || options.PageSize > 256 || options.MinDelay < 10*time.Millisecond || options.MaxDelay < options.MinDelay || options.MaxDelay > time.Minute ||
		options.StepTimeout < time.Second || options.StepTimeout > time.Minute {
		return nil, ErrStreamIdentity
	}
	target, ok := event.events[dispatchEventKey(expected.Scope.TradeDate, expected.CounterSessionID)]
	if !ok || target.scope != expected.Scope {
		return nil, ErrStreamIdentity
	}
	q := reply.queries
	if _, err := event.store.InspectDeliveryQuality(ctx, event.handle, reply.handle, q, expected); err != nil {
		return nil, err
	}
	c := &TestConsumer{source: source, event: event, reply: reply, events: events, queries: q, expected: expected, options: options, eventKey: target.key}
	tx, err := q.transaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	actions := map[string]bool{}
	for _, binding := range reply.replies {
		batch, err := q.load(ctx, tx, binding.BatchID)
		if err != nil {
			return nil, err
		}
		v := batch.batch.context
		if batch.retired || batch.owner != q.owner || v.commandScope() != source.scope || v.TradeDate != expected.Scope.TradeDate ||
			v.CounterSessionID != expected.CounterSessionID || v.ConnectionGeneration == nil || *v.ConnectionGeneration != expected.ConnectionGeneration || actions[v.Action] {
			return nil, ErrBatchIdentity
		}
		actions[v.Action] = true
		c.requests = append(c.requests, consumerQuery{binding, v.Action})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	sort.Slice(c.requests, func(i, j int) bool { return c.requests[i].BatchID < c.requests[j].BatchID })
	return c, nil
}

// Run never retries a failed read/application or renews its own session window.
// Idle/deferred work uses bounded backoff; valid work keeps a nonzero minimum
// delay. onCycle must return promptly and must not be used as trading authority.
func (c *TestConsumer) Run(ctx context.Context, onCycle func(ConsumerCycle)) (result error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Minute {
		return fmt.Errorf("TEST consumer requires an explicit window of at most five minutes")
	}
	if !c.used.CompareAndSwap(false, true) {
		return fmt.Errorf("TEST consumer cannot be restarted")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, request := range c.requests {
			if _, err := c.queries.Retire(cleanup, request.BatchID, "test_consumer_stopped"); err != nil {
				result = errors.Join(result, err)
			}
		}
		_ = c.source.Close()
		if onCycle != nil {
			stopped := ConsumerCycle{Stopped: true, Quality: DeliveryQuality{Blocked: true}}
			if result != nil {
				stopped.Failure = result.Error()
			}
			onCycle(stopped)
		}
	}()
	delay := c.options.MinDelay
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stepCtx, cancel := context.WithTimeout(ctx, c.options.StepTimeout)
		cycle, err := c.step(stepCtx)
		cancel()
		if err != nil {
			return err
		}
		delay = consumerDelay(delay, c.options, cycle.Progress)
		cycle.Delay = delay
		if onCycle != nil {
			onCycle(cycle)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func consumerDelay(previous time.Duration, options TestConsumerOptions, progress bool) time.Duration {
	if progress {
		return options.MinDelay
	}
	if previous >= options.MaxDelay/2 {
		return options.MaxDelay
	}
	return previous * 2
}

func (c *TestConsumer) step(ctx context.Context) (ConsumerCycle, error) {
	out := ConsumerCycle{Quality: DeliveryQuality{Blocked: true}}
	for _, dispatcher := range []*StreamDispatcher{c.event, c.reply} {
		captured, err := c.source.ReadOnce(ctx, dispatcher.store, dispatcher.handle, c.options.PageSize)
		if err != nil {
			return out, err
		}
		out.Captured += len(captured.ReceiptIDs)
	}
	for _, scan := range []struct {
		dispatcher *StreamDispatcher
		cursor     *int64
	}{{c.event, &c.eventScan}, {c.reply, &c.replyScan}} {
		page, err := scan.dispatcher.Dispatch(ctx, *scan.cursor, c.options.PageSize)
		*scan.cursor = page.NextAfterID
		if err != nil {
			return out, err
		}
		out.Delivered += page.Delivered
	}
	pending, err := c.events.Pending(ctx, c.eventKey, c.processScan, c.options.PageSize)
	if err != nil {
		return out, err
	}
	for _, item := range pending {
		var raw []byte
		err := c.events.db.QueryRowContext(ctx, `SELECT e.body FROM futures_event_inbox e WHERE e.event_id=$2 AND EXISTS(SELECT 1 FROM futures_stream_inbox s WHERE s.stream_key=$1 AND s.event_id=e.event_id AND s.state='delivered')`, c.event.handle.stream, item.EventID).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		current := false
		if err == nil {
			message, err := DecodeTradeEvent(raw, EventContext{Scope: c.expected.Scope})
			if err != nil {
				return out, err
			}
			if message.Order != nil {
				current = message.Order.CounterSessionID == c.expected.CounterSessionID
			} else {
				current = message.Fill.CounterSessionID == c.expected.CounterSessionID
			}
		}
		if current {
			processed, err := c.events.Process(ctx, item.EventID)
			if err != nil {
				return out, err
			}
			if processed.State != item.State {
				out.StateChanges++
			}
		}
		c.processScan = item.EventID
	}
	if len(pending) < c.options.PageSize {
		c.processScan = 0
	}
	for _, request := range c.requests {
		state, err := c.queries.Advance(ctx, request.BatchID, time.Since(request.StartedAt))
		if err != nil {
			return out, err
		}
		if state == BatchReady && isTradeQuery(request.action) {
			applied, err := c.queries.ApplyTradeQuery(ctx, request.BatchID)
			if err != nil {
				return out, err
			}
			if !applied.Replayed {
				out.StateChanges++
			}
		}
	}
	quality, err := c.event.store.InspectDeliveryQuality(ctx, c.event.handle, c.reply.handle, c.queries, c.expected)
	if err != nil {
		return out, err
	}
	out.Quality = quality
	out.Progress = out.Captured+out.Delivered+out.StateChanges > 0
	return out, nil
}
