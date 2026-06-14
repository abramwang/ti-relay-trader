package orderflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type AccountRefresher interface {
	RefreshAsset(ctx context.Context, accountID string, opts RefreshOptions) (RefreshQueryResult, error)
	RefreshPositions(ctx context.Context, accountID string, opts RefreshOptions) (RefreshQueryResult, error)
}

type AutoRefreshSchedulerOptions struct {
	Refresher AccountRefresher
	Logger    *slog.Logger
	Debounce  time.Duration
	Cooldown  time.Duration
	Timeout   time.Duration
}

type AutoRefreshScheduler struct {
	refresher AccountRefresher
	logger    *slog.Logger
	debounce  time.Duration
	cooldown  time.Duration
	timeout   time.Duration

	mu          sync.Mutex
	timers      map[string]*time.Timer
	nextAllowed map[string]time.Time
	stopped     bool
}

func NewAutoRefreshScheduler(opts AutoRefreshSchedulerOptions) *AutoRefreshScheduler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Debounce < 0 {
		opts.Debounce = 0
	}
	if opts.Cooldown < 0 {
		opts.Cooldown = 0
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	return &AutoRefreshScheduler{
		refresher:   opts.Refresher,
		logger:      opts.Logger,
		debounce:    opts.Debounce,
		cooldown:    opts.Cooldown,
		timeout:     opts.Timeout,
		timers:      map[string]*time.Timer{},
		nextAllowed: map[string]time.Time{},
	}
}

func (scheduler *AutoRefreshScheduler) Request(accountID string, reason string) {
	if scheduler == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "trade-change"
	}

	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.stopped {
		return
	}

	delay := scheduler.debounce
	if next := scheduler.nextAllowed[accountID]; !next.IsZero() {
		cooldownDelay := time.Until(next)
		if cooldownDelay > delay {
			delay = cooldownDelay
		}
	}
	if delay < 0 {
		delay = 0
	}

	if timer := scheduler.timers[accountID]; timer != nil {
		timer.Stop()
	}
	scheduler.timers[accountID] = time.AfterFunc(delay, func() {
		scheduler.run(accountID, reason)
	})
	scheduler.logger.Debug("auto_refresh_scheduled",
		"account_id", accountID,
		"reason", reason,
		"delay", delay.String(),
	)
}

func (scheduler *AutoRefreshScheduler) RequestAccounts(accountIDs []string, reason string) {
	for _, accountID := range accountIDs {
		scheduler.Request(accountID, reason)
	}
}

func (scheduler *AutoRefreshScheduler) Stop() {
	if scheduler == nil {
		return
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.stopped = true
	for accountID, timer := range scheduler.timers {
		timer.Stop()
		delete(scheduler.timers, accountID)
	}
}

func (scheduler *AutoRefreshScheduler) run(accountID string, reason string) {
	scheduler.mu.Lock()
	if scheduler.stopped {
		scheduler.mu.Unlock()
		return
	}
	delete(scheduler.timers, accountID)
	scheduler.nextAllowed[accountID] = time.Now().Add(scheduler.cooldown)
	scheduler.mu.Unlock()

	if scheduler.refresher == nil {
		scheduler.logger.Warn("auto_refresh_skipped", "account_id", accountID, "reason", "refresher missing")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), scheduler.timeout)
	defer cancel()
	requestBase := fmt.Sprintf("auto-refresh-%d", time.Now().UTC().UnixNano())
	assetResult, assetErr := scheduler.refresher.RefreshAsset(ctx, accountID, RefreshOptions{RequestID: requestBase + "-asset"})
	positionsResult, positionsErr := scheduler.refresher.RefreshPositions(ctx, accountID, RefreshOptions{RequestID: requestBase + "-positions"})

	if assetErr != nil || positionsErr != nil {
		scheduler.logger.Warn("auto_refresh_failed",
			"account_id", accountID,
			"reason", reason,
			"asset_error", errorString(assetErr),
			"positions_error", errorString(positionsErr),
		)
		return
	}
	scheduler.logger.Info("auto_refresh_published",
		"account_id", accountID,
		"reason", reason,
		"asset_stream_id", assetResult.StreamID,
		"positions_stream_id", positionsResult.StreamID,
	)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
