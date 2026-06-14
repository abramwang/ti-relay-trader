package events

import (
	"context"
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
