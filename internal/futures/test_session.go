package futures

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrTestSessionMissing = errors.New("TEST heartbeat not observed")
var ErrTestSessionStopped = errors.New("TEST session consumption stopped")

type HeartbeatSource struct{ SourceID, Stream, EntryID string }

// Local TEST freshness policy, not an OC lease or permission to query/trade.
type TestHeartbeatPolicy struct{ MaxAge, FutureSkew time.Duration }

func DefaultTestHeartbeatPolicy() TestHeartbeatPolicy {
	return TestHeartbeatPolicy{30 * time.Second, 2 * time.Second}
}

// TestSessionGuard is an in-memory, single-session negative guard. The caller
// must preserve raw ingress evidence and verify the expected native context.
// It never learns a session/day/generation from an incoming heartbeat.
type TestSessionGuard struct {
	mu                               sync.Mutex
	scope                            CommandScope
	expected                         DeliveryQualityContext
	sourceID, stream                 string
	policy                           TestHeartbeatPolicy
	seen                             map[string][32]byte
	lastID                           string
	lastDigest                       [32]byte
	producedAt, expiresAt, checkedAt time.Time
	failure                          error
	changed                          chan struct{}
	readOnlyMode                     bool
	readOnlyReconciliation           *ReadOnlyReconciliation
	assessmentLegacySource           *AssessmentLegacySource
}

func NewTestSessionGuard(source *TestStreamSource, expected DeliveryQualityContext, policy TestHeartbeatPolicy) (*TestSessionGuard, error) {
	if source == nil || expected.Scope.validate() != nil || !validID(expected.CounterSessionID) || len(expected.CounterSessionID) > 128 || expected.ConnectionGeneration < 1 ||
		(CommandScope{expected.Scope.Route, expected.Scope.NativeBrokerID, expected.Scope.InvestorID, source.scope.UserID}) != source.scope ||
		policy.MaxAge < time.Millisecond || policy.MaxAge > 5*time.Minute || policy.FutureSkew < 0 || policy.FutureSkew > 5*time.Second {
		return nil, ErrStreamIdentity
	}
	return &TestSessionGuard{scope: source.scope, expected: expected, sourceID: source.sourceID,
		stream: strings.TrimSuffix(queryReplyStream(source.scope.Route), "reply") + "hb", policy: policy,
		seen: map[string][32]byte{}, changed: make(chan struct{})}, nil
}

func (g *TestSessionGuard) stop(reason string) error {
	if g.failure == nil {
		g.failure = fmt.Errorf("%w: %s", ErrTestSessionStopped, reason)
		close(g.changed)
	}
	return g.failure
}

func (g *TestSessionGuard) Revoke() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stop("authority_revoked")
}

func (g *TestSessionGuard) checkAt(now time.Time) error {
	if g.failure != nil {
		return g.failure
	}
	if !g.checkedAt.IsZero() && now.Before(g.checkedAt) {
		return g.stop("local_clock_regressed")
	}
	g.checkedAt = now
	if g.expiresAt.IsZero() {
		return ErrTestSessionMissing
	}
	if !now.Before(g.expiresAt) {
		return g.stop("heartbeat_expired")
	}
	return nil
}

func (g *TestSessionGuard) Check() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkAt(time.Now())
}

func (g *TestSessionGuard) Observe(source HeartbeatSource, raw []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.observeLocked(source, raw, time.Now())
}

func (g *TestSessionGuard) observeAt(source HeartbeatSource, raw []byte, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.observeLocked(source, raw, now)
}

func (g *TestSessionGuard) observeLocked(source HeartbeatSource, raw []byte, now time.Time) error {
	if err := g.checkAt(now); err != nil && !errors.Is(err, ErrTestSessionMissing) {
		return err
	}
	if source.SourceID != g.sourceID || source.Stream != g.stream {
		return g.stop("heartbeat_source_mismatch")
	}
	if _, err := streamID(source.EntryID); err != nil || source.EntryID == "0-0" {
		return g.stop("heartbeat_entry_invalid")
	}
	h, err := DecodeHeartbeat(raw, g.scope.Route)
	if err != nil {
		return g.stop("heartbeat_invalid")
	}
	if h.Recovery != nil {
		return g.stop("heartbeat_transport_recovery_only")
	}
	if !g.producedAt.IsZero() && g.readOnlyMode != (h.ReadOnly != nil) {
		return g.stop("heartbeat_runtime_mode_changed")
	}
	if h.ReadOnly != nil {
		if !h.readOnlyNativeScopeMatches(g.scope) {
			return g.stop("heartbeat_native_scope_changed")
		}
		if !g.producedAt.IsZero() {
			r, prior := h.ReadOnly.Reconciliation, g.readOnlyReconciliation
			if (r == nil) != (prior == nil) || (r != nil && (r.SourceManifestSHA256 != prior.SourceManifestSHA256 || r.SourceScope != prior.SourceScope ||
				r.Legacy.Retained != prior.Legacy.Retained || r.Legacy.Pending != prior.Legacy.Pending || r.Legacy.AuditOnly != prior.Legacy.AuditOnly)) {
				return g.stop("heartbeat_reconciliation_binding_changed")
			}
		}
		if !h.ReadOnly.QueryCollectionReady || !h.ReadOnly.TransportRecoveryClear {
			return g.stop("heartbeat_query_collection_not_ready")
		}
		if h.ReadOnly.ConnectionGeneration == nil || *h.ReadOnly.ConnectionGeneration != g.expected.ConnectionGeneration {
			return g.stop("heartbeat_connection_generation_changed")
		}
	}
	if h.CounterSessionID != g.expected.CounterSessionID || h.TradeDate != g.expected.Scope.TradeDate {
		return g.stop("heartbeat_session_or_day_changed")
	}
	var legacy *AssessmentLegacySource
	if h.Assessment != nil {
		legacy = h.Assessment.LegacySource
		if !h.assessmentNativeScopeMatches(g.scope) || (legacy != nil && legacy.Diagnostics.BlockingConflict) {
			return g.stop("heartbeat_assessment_source_invalid")
		}
	}
	if !g.producedAt.IsZero() && !sameAssessmentLegacyBinding(legacy, g.assessmentLegacySource) {
		return g.stop("heartbeat_assessment_source_changed")
	}
	if !h.RedisReady || !h.BrokerReady {
		return g.stop("heartbeat_disconnected")
	}
	if h.ProducedAt.After(now.Add(g.policy.FutureSkew)) || now.Sub(h.ProducedAt) >= g.policy.MaxAge {
		return g.stop("heartbeat_time_invalid")
	}
	digest := sha256.Sum256(raw)
	if g.lastID != "" {
		cmp, _ := compareStreamID(source.EntryID, g.lastID)
		if cmp < 0 || (cmp == 0 && digest != g.lastDigest) {
			return g.stop("heartbeat_entry_conflict")
		}
		if cmp == 0 {
			return nil
		}
	}
	if prior, ok := g.seen[h.MessageID]; ok {
		if prior != digest {
			return g.stop("heartbeat_message_conflict")
		}
		g.lastID, g.lastDigest = source.EntryID, digest
		return nil // Redelivery never renews freshness, even at a new Stream ID.
	}
	if !g.producedAt.IsZero() && !h.ProducedAt.After(g.producedAt) {
		return g.stop("heartbeat_clock_not_advancing")
	}
	if len(g.seen) >= 512 {
		return g.stop("heartbeat_window_capacity")
	}
	g.seen[h.MessageID] = digest
	g.readOnlyMode = h.ReadOnly != nil
	g.assessmentLegacySource = legacy
	if h.ReadOnly != nil {
		g.readOnlyReconciliation = h.ReadOnly.Reconciliation
	}
	g.lastID, g.lastDigest, g.producedAt = source.EntryID, digest, h.ProducedAt
	age := now.Sub(h.ProducedAt)
	if age < 0 {
		age = 0
	}
	// Add to the local receipt clock so an accepted small future skew cannot
	// extend TTL, and the deadline keeps a monotonic component in real runs.
	g.expiresAt = now.Add(g.policy.MaxAge - age)
	close(g.changed)
	g.changed = make(chan struct{})
	return nil
}

func (g *TestSessionGuard) watch(ctx context.Context, cancel context.CancelCauseFunc) {
	for {
		g.mu.Lock()
		err := g.checkAt(time.Now())
		deadline, changed := g.expiresAt, g.changed
		g.mu.Unlock()
		if err != nil {
			cancel(err)
			return
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// RunGuarded adds revocation/expiry cancellation to the bounded prototype.
// It does not read heartbeat Redis, authorize commands or install a daemon.
func (c *TestConsumer) RunGuarded(ctx context.Context, guard *TestSessionGuard, onCycle func(ConsumerCycle)) error {
	return c.runGuarded(ctx, guard, onCycle, false)
}

// A claimed caller owns the single-use bootstrap and the client's final close.
func (c *TestConsumer) runGuarded(ctx context.Context, guard *TestSessionGuard, onCycle func(ConsumerCycle), claimed bool) error {
	if guard == nil || guard.scope != c.source.scope || guard.sourceID != c.source.sourceID || guard.expected != c.expected {
		return ErrStreamIdentity
	}
	window, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() { defer close(done); guard.watch(window, cancel) }()
	defer func() { cancel(context.Canceled); <-done }()
	if claimed {
		return c.runStarted(window, guard, onCycle, false)
	}
	return c.run(window, guard, onCycle)
}
