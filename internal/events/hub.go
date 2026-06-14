package events

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TypeConnected        = "relay.connected"
	TypeHeartbeat        = "relay.heartbeat"
	TypeOrderChanged     = "order.changed"
	TypeFillChanged      = "fill.changed"
	TypeAssetChanged     = "asset.changed"
	TypePositionsChanged = "positions.changed"
)

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

type Hub struct {
	mu          sync.RWMutex
	nextSubID   uint64
	nextEventID atomic.Uint64
	subscribers map[uint64]subscriber
}

type subscriber struct {
	accountID string
	ch        chan Event
}

func NewHub() *Hub {
	return &Hub{subscribers: map[uint64]subscriber{}}
}

func (hub *Hub) Publish(event Event) Event {
	if hub == nil {
		return event
	}
	if event.Type == "" {
		event.Type = "relay.event"
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt-%d", hub.nextEventID.Add(1))
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	event.AccountIDs = normalizeAccountIDs(event.AccountIDs)

	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, sub := range hub.subscribers {
		if !subscriberMatches(sub, event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			select {
			case <-sub.ch:
			default:
			}
			select {
			case sub.ch <- event:
			default:
			}
		}
	}
	return event
}

func (hub *Hub) Subscribe(ctx context.Context, filter Subscription) (<-chan Event, func()) {
	if hub == nil {
		hub = NewHub()
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
	if ctx != nil {
		go func() {
			<-ctx.Done()
			unsubscribe()
		}()
	}
	return ch, unsubscribe
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
