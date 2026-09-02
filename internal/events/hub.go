package events

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ti-relay-trader/internal/timeutil"
)

const (
	TypeConnected           = "relay.connected"
	TypeHeartbeat           = "relay.heartbeat"
	TypeGap                 = "relay.gap"
	TypeOrderChanged        = "order.changed"
	TypeOrderCancelRejected = "order.cancel.rejected"
	TypeFillChanged         = "fill.changed"
	TypeAssetChanged        = "asset.changed"
	TypePositionsChanged    = "positions.changed"
)

const DefaultReplayCapacity = 2048

var nextHubEpoch atomic.Uint64

type Event struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	AccountIDs   []string  `json:"account_ids,omitempty"`
	Time         time.Time `json:"time"`
	Source       string    `json:"source,omitempty"`
	Stream       string    `json:"stream,omitempty"`
	LastStreamID string    `json:"last_stream_id,omitempty"`
	Data         any       `json:"data,omitempty"`
}

type Subscription struct {
	AccountID string
	Buffer    int
}

type Resume struct {
	RequestedCursor        string
	CurrentCursor          string
	OldestCursor           string
	Status                 string
	Reason                 string
	Events                 []Event
	ReconciliationRequired bool
}

type Hub struct {
	mu             sync.RWMutex
	epoch          string
	nextSubID      uint64
	nextEventID    uint64
	replayCapacity int
	history        []Event
	subscribers    map[uint64]subscriber
}

type subscriber struct {
	accountID string
	ch        chan Event
}

func NewHub() *Hub {
	return NewHubWithReplayCapacity(DefaultReplayCapacity)
}

func NewHubWithReplayCapacity(capacity int) *Hub {
	if capacity < 0 {
		capacity = 0
	}
	epoch := fmt.Sprintf("%x-%x", time.Now().UTC().UnixNano(), nextHubEpoch.Add(1))
	return &Hub{
		epoch:          epoch,
		replayCapacity: capacity,
		history:        make([]Event, 0, capacity),
		subscribers:    map[uint64]subscriber{},
	}
}

func (hub *Hub) Publish(event Event) Event {
	if hub == nil {
		return event
	}
	if event.Type == "" {
		event.Type = "relay.event"
	}
	if event.Time.IsZero() {
		event.Time = timeutil.Now()
	} else {
		event.Time = timeutil.InBusinessLocation(event.Time)
	}
	event.AccountIDs = normalizeAccountIDs(event.AccountIDs)

	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.nextEventID++
	event.ID = hub.cursorLocked(hub.nextEventID)
	hub.appendHistoryLocked(event)
	for _, sub := range hub.subscribers {
		if !subscriberMatches(sub, event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			drainEvents(sub.ch)
			sub.ch <- slowConsumerGap(sub, event)
		}
	}
	return event
}

func (hub *Hub) Subscribe(ctx context.Context, filter Subscription) (<-chan Event, func()) {
	ch, _, unsubscribe := hub.SubscribeFrom(ctx, filter, "")
	return ch, unsubscribe
}

func (hub *Hub) SubscribeFrom(ctx context.Context, filter Subscription, lastEventID string) (<-chan Event, Resume, func()) {
	if hub == nil {
		return NewHub().SubscribeFrom(ctx, filter, lastEventID)
	}
	buffer := filter.Buffer
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan Event, buffer)
	sub := subscriber{
		accountID: strings.TrimSpace(filter.AccountID),
		ch:        ch,
	}

	hub.mu.Lock()
	resume := hub.resumeLocked(sub, strings.TrimSpace(lastEventID))
	hub.nextSubID++
	subID := hub.nextSubID
	hub.subscribers[subID] = sub
	hub.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			hub.mu.Lock()
			if _, ok := hub.subscribers[subID]; ok {
				delete(hub.subscribers, subID)
				close(ch)
			}
			hub.mu.Unlock()
		})
	}
	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			unsubscribe()
		}()
	}
	return ch, resume, unsubscribe
}

func (hub *Hub) CurrentCursor() string {
	if hub == nil {
		return ""
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.cursorLocked(hub.nextEventID)
}

func (hub *Hub) ReplayCapacity() int {
	if hub == nil {
		return 0
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.replayCapacity
}

func (hub *Hub) appendHistoryLocked(event Event) {
	if hub.replayCapacity <= 0 {
		return
	}
	if len(hub.history) == hub.replayCapacity {
		copy(hub.history, hub.history[1:])
		hub.history[len(hub.history)-1] = event
		return
	}
	hub.history = append(hub.history, event)
}

func (hub *Hub) resumeLocked(sub subscriber, requested string) Resume {
	resume := Resume{
		RequestedCursor: requested,
		CurrentCursor:   hub.cursorLocked(hub.nextEventID),
		Status:          "fresh",
	}
	if len(hub.history) > 0 {
		resume.OldestCursor = hub.history[0].ID
	}
	if requested == "" {
		return resume
	}
	resume.ReconciliationRequired = true
	epoch, sequence, ok := parseCursor(requested)
	if !ok {
		resume.Status = "gap"
		resume.Reason = "invalid_cursor"
		return resume
	}
	if epoch != hub.epoch {
		resume.Status = "gap"
		resume.Reason = "server_restart"
		return resume
	}
	if sequence > hub.nextEventID {
		resume.Status = "gap"
		resume.Reason = "cursor_ahead"
		return resume
	}
	earliestSequence := hub.nextEventID + 1
	if len(hub.history) > 0 {
		_, earliestSequence, _ = parseCursor(hub.history[0].ID)
	}
	if sequence+1 < earliestSequence {
		resume.Status = "gap"
		resume.Reason = "cursor_expired"
		return resume
	}
	resume.Status = "resumed"
	for _, event := range hub.history {
		_, eventSequence, valid := parseCursor(event.ID)
		if valid && eventSequence > sequence && subscriberMatches(sub, event) {
			resume.Events = append(resume.Events, event)
		}
	}
	return resume
}

func (hub *Hub) cursorLocked(sequence uint64) string {
	return fmt.Sprintf("evt-%s-%d", hub.epoch, sequence)
}

func parseCursor(cursor string) (string, uint64, bool) {
	cursor = strings.TrimSpace(cursor)
	if !strings.HasPrefix(cursor, "evt-") {
		return "", 0, false
	}
	separator := strings.LastIndex(cursor, "-")
	if separator <= len("evt-") || separator == len(cursor)-1 {
		return "", 0, false
	}
	sequence, err := strconv.ParseUint(cursor[separator+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return cursor[len("evt-"):separator], sequence, true
}

func drainEvents(ch chan Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func slowConsumerGap(sub subscriber, current Event) Event {
	accountIDs := current.AccountIDs
	if sub.accountID != "" {
		accountIDs = []string{sub.accountID}
	}
	return Event{
		ID:         current.ID,
		Type:       TypeGap,
		AccountIDs: accountIDs,
		Time:       current.Time,
		Source:     "relay-api",
		Data: map[string]any{
			"reason":                  "slow_consumer",
			"current_cursor":          current.ID,
			"reconciliation_required": true,
		},
	}
}

func subscriberMatches(sub subscriber, event Event) bool {
	if sub.accountID == "" || len(event.AccountIDs) == 0 {
		return true
	}
	for _, accountID := range event.AccountIDs {
		if accountID == sub.accountID {
			return true
		}
	}
	return false
}

func normalizeAccountIDs(accountIDs []string) []string {
	if len(accountIDs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		normalized = append(normalized, accountID)
	}
	return normalized
}
