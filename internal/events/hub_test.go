package events

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHubPublishesToMatchingAccount(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	matched, unsubscribeMatched := hub.Subscribe(ctx, Subscription{AccountID: "acct-1"})
	defer unsubscribeMatched()
	other, unsubscribeOther := hub.Subscribe(ctx, Subscription{AccountID: "acct-2"})
	defer unsubscribeOther()

	published := hub.Publish(Event{
		Type:       TypeOrderChanged,
		AccountIDs: []string{"acct-1"},
	})

	select {
	case event := <-matched:
		if event.ID == "" || event.Type != TypeOrderChanged || event.Time.IsZero() {
			t.Fatalf("event = %#v, published = %#v", event, published)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matching event")
	}

	select {
	case event := <-other:
		t.Fatalf("unexpected event for other account: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHubReplaysMatchingEventsAfterCursor(t *testing.T) {
	hub := NewHubWithReplayCapacity(8)
	first := hub.Publish(Event{Type: TypeOrderChanged, AccountIDs: []string{"acct-1"}})
	hub.Publish(Event{Type: TypeFillChanged, AccountIDs: []string{"acct-2"}})
	third := hub.Publish(Event{Type: TypePositionsChanged, AccountIDs: []string{"acct-1"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, resume, unsubscribe := hub.SubscribeFrom(ctx, Subscription{AccountID: "acct-1"}, first.ID)
	defer unsubscribe()

	if resume.Status != "resumed" || !resume.ReconciliationRequired {
		t.Fatalf("resume = %#v", resume)
	}
	if len(resume.Events) != 1 || resume.Events[0].ID != third.ID {
		t.Fatalf("replayed events = %#v, want only %#v", resume.Events, third)
	}
	if resume.CurrentCursor != third.ID || resume.OldestCursor != first.ID {
		t.Fatalf("resume cursors = %#v", resume)
	}
}

func TestHubReportsRestartAndExpiredCursorGaps(t *testing.T) {
	oldHub := NewHubWithReplayCapacity(2)
	oldCursor := oldHub.CurrentCursor()
	newHub := NewHubWithReplayCapacity(2)
	_, restart, unsubscribeRestart := newHub.SubscribeFrom(context.Background(), Subscription{}, oldCursor)
	defer unsubscribeRestart()
	if restart.Status != "gap" || restart.Reason != "server_restart" {
		t.Fatalf("restart resume = %#v", restart)
	}

	expiredHub := NewHubWithReplayCapacity(2)
	expiredCursor := expiredHub.CurrentCursor()
	expiredHub.Publish(Event{Type: TypeOrderChanged})
	expiredHub.Publish(Event{Type: TypeFillChanged})
	expiredHub.Publish(Event{Type: TypeAssetChanged})
	_, expired, unsubscribeExpired := expiredHub.SubscribeFrom(context.Background(), Subscription{}, expiredCursor)
	defer unsubscribeExpired()
	if expired.Status != "gap" || expired.Reason != "cursor_expired" {
		t.Fatalf("expired resume = %#v", expired)
	}
}

func TestHubSignalsSlowConsumerGap(t *testing.T) {
	hub := NewHubWithReplayCapacity(8)
	ch, unsubscribe := hub.Subscribe(context.Background(), Subscription{AccountID: "acct-1", Buffer: 1})
	defer unsubscribe()
	hub.Publish(Event{Type: TypeOrderChanged, AccountIDs: []string{"acct-1"}})
	latest := hub.Publish(Event{Type: TypeFillChanged, AccountIDs: []string{"acct-1"}})

	event := <-ch
	if event.Type != TypeGap || event.ID != latest.ID {
		t.Fatalf("overflow event = %#v", event)
	}
	data, ok := event.Data.(map[string]any)
	if !ok || data["reason"] != "slow_consumer" || data["reconciliation_required"] != true {
		t.Fatalf("gap data = %#v", event.Data)
	}
}

func TestHubCursorIsProcessScopedAndMonotonic(t *testing.T) {
	hub := NewHubWithReplayCapacity(1)
	zero := hub.CurrentCursor()
	first := hub.Publish(Event{Type: TypeOrderChanged})
	second := hub.Publish(Event{Type: TypeFillChanged})
	if !strings.HasPrefix(first.ID, strings.TrimSuffix(zero, "0")) || first.ID == second.ID {
		t.Fatalf("cursors zero=%q first=%q second=%q", zero, first.ID, second.ID)
	}
	_, firstSequence, firstOK := parseCursor(first.ID)
	_, secondSequence, secondOK := parseCursor(second.ID)
	if !firstOK || !secondOK || secondSequence != firstSequence+1 {
		t.Fatalf("cursor sequences first=%d second=%d", firstSequence, secondSequence)
	}
}
