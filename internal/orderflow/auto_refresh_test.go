package orderflow

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestAutoRefreshSchedulerCoalescesRequests(t *testing.T) {
	refresher := &fakeAccountRefresher{calls: make(chan string, 4)}
	scheduler := NewAutoRefreshScheduler(AutoRefreshSchedulerOptions{
		Refresher: refresher,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Debounce:  10 * time.Millisecond,
		Cooldown:  100 * time.Millisecond,
		Timeout:   time.Second,
	})
	defer scheduler.Stop()

	scheduler.Request("acct-1", "order")
	scheduler.Request("acct-1", "fill")

	waitCall(t, refresher.calls, "asset:acct-1")
	waitCall(t, refresher.calls, "positions:acct-1")
	select {
	case call := <-refresher.calls:
		t.Fatalf("unexpected extra refresh call: %s", call)
	case <-time.After(30 * time.Millisecond):
	}
}

func waitCall(t *testing.T, calls <-chan string, want string) {
	t.Helper()
	select {
	case got := <-calls:
		if got != want {
			t.Fatalf("call = %s, want %s", got, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", want)
	}
}

type fakeAccountRefresher struct {
	calls chan string
}

func (refresher *fakeAccountRefresher) RefreshAsset(_ context.Context, accountID string, _ RefreshOptions) (RefreshQueryResult, error) {
	refresher.calls <- "asset:" + accountID
	return RefreshQueryResult{AccountID: accountID, StreamID: "asset-stream"}, nil
}

func (refresher *fakeAccountRefresher) RefreshPositions(_ context.Context, accountID string, _ RefreshOptions) (RefreshQueryResult, error) {
	refresher.calls <- "positions:" + accountID
	return RefreshQueryResult{AccountID: accountID, StreamID: "positions-stream"}, nil
}
