package futures

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func guardFixture(t *testing.T) (*TestSessionGuard, time.Time) {
	t.Helper()
	s, err := NewTestStreamSource(sourceFixture())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	day := tradeScope()
	day.Route = s.scope.Route
	g, err := NewTestSessionGuard(s, DeliveryQualityContext{day, "ctpproc-fixture-1", 1}, DefaultTestHeartbeatPolicy())
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, "2026-11-06T21:01:00+08:00")
	return g, at
}

func guardSource(g *TestSessionGuard, entry string) HeartbeatSource {
	return HeartbeatSource{g.sourceID, g.stream, entry}
}

func guardBody(t *testing.T, g *TestSessionGuard, id string, at time.Time) []byte {
	t.Helper()
	return editBody(t, fixtureBody(t, "messages.json", "heartbeat_read_only"), func(m map[string]any) {
		m["message_id"], m["produced_at"] = id, at.In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339Nano)
		r := m["routing"].(map[string]any)
		r["broker_id"], r["gateway_id"], r["account_id"] = g.scope.Route.BrokerID, g.scope.Route.GatewayID, g.scope.Route.AccountID
		p := m["payload"].(map[string]any)
		p["trade_date"], p["counter_session_id"] = g.expected.Scope.TradeDate, g.expected.CounterSessionID
	})
}

func TestHeartbeatSharedFixtureAndIsolation(t *testing.T) {
	raw := fixtureBody(t, "messages.json", "heartbeat_read_only")
	route := QueryRoute{"test", "ctp_4900", "990000000001", "990000000001"}
	h, err := DecodeHeartbeat(raw, route)
	if err != nil || h.TradeDate != "20261104" || h.ProducedAt.Day() != 3 || !h.BrokerReady || h.OrderSnapshotReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
		t.Fatal(h, err)
	}
	copy := bytes.Clone(raw)
	raw[0] = 'x'
	if !bytes.Equal(copy, h.Raw) {
		t.Fatal("heartbeat aliases input bytes")
	}
}

func TestHeartbeatRejectsMalformedFields(t *testing.T) {
	g, at := guardFixture(t)
	raw := guardBody(t, g, "hb", at)
	for _, field := range []string{"redis_ready", "broker_ready", "settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"} {
		for _, invalid := range []any{nil, 1, "true"} {
			body := editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)[field] = invalid })
			if _, err := DecodeHeartbeat(body, g.scope.Route); err == nil {
				t.Fatal("non-boolean accepted", field)
			}
		}
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["produced_at"] = "2026-11-06T13:01:00Z" },
		func(m map[string]any) { m["routing"].(map[string]any)["account_id"] = "990000000099" },
		func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = "20260230" },
		func(m map[string]any) { m["payload"].(map[string]any)["connection_generation"] = 1 },
		func(m map[string]any) { m["adapter_context"] = nil },
		func(m map[string]any) { delete(m["payload"].(map[string]any), "broker_ready") },
		func(m map[string]any) { m["message_id"] = "" },
	} {
		if _, err := DecodeHeartbeat(editBody(t, raw, mutate), g.scope.Route); err == nil {
			t.Fatal("invalid heartbeat accepted")
		}
	}
	for _, bad := range [][]byte{[]byte(strings.Replace(string(raw), `"broker_ready":true`, `"broker_ready":true,"broker_ready":false`, 1)), append(bytes.Clone(raw), []byte(` {}`)...), bytes.Repeat([]byte(" "), maxHeartbeatBytes+1)} {
		if _, err := DecodeHeartbeat(bad, g.scope.Route); err == nil {
			t.Fatal("malformed input accepted")
		}
	}
}

func TestTestSessionFreshnessReplayAndRenewal(t *testing.T) {
	g, at := guardFixture(t)
	if !errors.Is(g.checkAt(at), ErrTestSessionMissing) {
		t.Fatal("missing heartbeat is live")
	}
	raw := guardBody(t, g, "first", at)
	if err := g.observeAt(guardSource(g, "1-0"), raw, at); err != nil {
		t.Fatal(err)
	}
	deadline := g.expiresAt
	for i, entry := range []string{"1-0", "2-0"} {
		if err := g.observeAt(guardSource(g, entry), raw, at.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if g.expiresAt != deadline {
			t.Fatal("redelivery renewed freshness")
		}
	}
	renew := at.Add(5 * time.Second)
	if err := g.observeAt(guardSource(g, "3-0"), guardBody(t, g, "new", renew), renew); err != nil {
		t.Fatal(err)
	}
	if !g.expiresAt.Equal(renew.Add(g.policy.MaxAge)) {
		t.Fatal("fresh heartbeat did not renew")
	}
	if err := g.checkAt(g.expiresAt); !errors.Is(err, ErrTestSessionStopped) {
		t.Fatal("expiry not latched", err)
	}
	if err := g.observeAt(guardSource(g, "4-0"), guardBody(t, g, "late", renew.Add(time.Minute)), renew.Add(time.Minute)); !errors.Is(err, ErrTestSessionStopped) {
		t.Fatal("expired guard revived", err)
	}
}

func TestTestSessionRejectsSourceSessionAndClockChanges(t *testing.T) {
	for _, kind := range []string{"source", "stream", "entry", "session", "day", "broker", "redis", "future", "stale", "entry_conflict", "message_conflict", "regressed", "same_time", "local_clock"} {
		t.Run(kind, func(t *testing.T) {
			g, at := guardFixture(t)
			raw := guardBody(t, g, "first", at)
			if err := g.observeAt(guardSource(g, "1-0"), raw, at); err != nil {
				t.Fatal(err)
			}
			now := at.Add(time.Second)
			src := guardSource(g, "2-0")
			body := guardBody(t, g, "second", now)
			switch kind {
			case "source":
				src.SourceID = "different"
			case "stream":
				src.Stream = strings.TrimSuffix(src.Stream, "hb") + "event"
			case "entry":
				src.EntryID = "0-0"
			case "session", "day", "broker", "redis":
				body = editBody(t, body, func(m map[string]any) {
					p := m["payload"].(map[string]any)
					switch kind {
					case "session":
						p["counter_session_id"] = "other"
					case "day":
						p["trade_date"] = "20261109"
					case "broker":
						p["broker_ready"] = false
					case "redis":
						p["redis_ready"] = false
					}
				})
			case "future":
				body = guardBody(t, g, "second", now.Add(3*time.Second))
			case "stale":
				body = guardBody(t, g, "second", now.Add(-time.Minute))
			case "entry_conflict":
				src.EntryID = "1-0"
			case "message_conflict":
				body = guardBody(t, g, "first", now)
			case "regressed":
				src.EntryID = "0-1"
			case "same_time":
				body = guardBody(t, g, "second", at)
			case "local_clock":
				now = at.Add(-time.Second)
			}
			if err := g.observeAt(src, body, now); !errors.Is(err, ErrTestSessionStopped) {
				t.Fatal(kind, err)
			}
			if err := g.observeAt(guardSource(g, "3-0"), guardBody(t, g, "recover", at.Add(2*time.Second)), at.Add(2*time.Second)); !errors.Is(err, ErrTestSessionStopped) {
				t.Fatal("invalid context auto-recovered", err)
			}
		})
	}
}

func TestTestSessionReadOnlyFlagsAndFutureSkew(t *testing.T) {
	g, at := guardFixture(t)
	body := editBody(t, guardBody(t, g, "hb", at.Add(time.Second)), func(m map[string]any) { m["payload"].(map[string]any)["settlement_confirmed"] = false })
	if err := g.observeAt(guardSource(g, "1-0"), body, at); err != nil {
		t.Fatal("read observation required trading readiness", err)
	}
	if !g.expiresAt.Equal(at.Add(g.policy.MaxAge)) {
		t.Fatal("future source clock extended TTL")
	}
}

func TestTestSessionCapacity(t *testing.T) {
	g, at := guardFixture(t)
	for i := 1; i <= 513; i++ {
		now := at.Add(time.Duration(i))
		err := g.observeAt(guardSource(g, fmt.Sprintf("%d-0", i)), guardBody(t, g, fmt.Sprint(i), now), now)
		if i <= 512 && err != nil {
			t.Fatal(i, err)
		}
		if i == 513 && !errors.Is(err, ErrTestSessionStopped) {
			t.Fatal("unbounded heartbeat window", err)
		}
	}
}

func TestTestSessionConcurrentCheckAndRevocation(t *testing.T) {
	g, _ := guardFixture(t)
	raw := guardBody(t, g, "hb", time.Now())
	if err := g.Observe(guardSource(g, "1-0"), raw); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = g.Observe(guardSource(g, "1-0"), raw)
				_ = g.Check()
			}
		}()
	}
	wg.Wait()
	g.Revoke()
	if !errors.Is(g.Check(), ErrTestSessionStopped) {
		t.Fatal("revocation lost")
	}
}

func TestTestSessionWatchExpiry(t *testing.T) {
	g, _ := guardFixture(t)
	if err := g.Observe(guardSource(g, "1-0"), guardBody(t, g, "hb", time.Now())); err != nil {
		t.Fatal(err)
	}
	g.expiresAt = time.Now().Add(30 * time.Millisecond)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	done := make(chan struct{})
	go func() { defer close(done); g.watch(ctx, cancel) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel(context.Canceled)
		<-done
		t.Fatal("watcher did not expire")
	}
	if !errors.Is(context.Cause(ctx), ErrTestSessionStopped) {
		t.Fatal(context.Cause(ctx))
	}
}

func TestTestSessionPolicyAndBindingValidation(t *testing.T) {
	s, err := NewTestStreamSource(sourceFixture())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	day := tradeScope()
	day.Route = s.scope.Route
	expected := DeliveryQualityContext{day, "session", 1}
	for _, policy := range []TestHeartbeatPolicy{{0, 0}, {6 * time.Minute, 0}, {time.Second, -time.Second}, {time.Second, 6 * time.Second}} {
		if _, err := NewTestSessionGuard(s, expected, policy); err == nil {
			t.Fatal("invalid freshness policy")
		}
	}
	for _, mutate := range []func(*DeliveryQualityContext){
		func(e *DeliveryQualityContext) { e.ConnectionGeneration = 0 },
		func(e *DeliveryQualityContext) { e.CounterSessionID = "" },
		func(e *DeliveryQualityContext) { e.Scope.NativeBrokerID = "other" },
		func(e *DeliveryQualityContext) { e.Scope.Route.Env = "prod" },
	} {
		other := expected
		mutate(&other)
		if _, err := NewTestSessionGuard(s, other, DefaultTestHeartbeatPolicy()); err == nil {
			t.Fatal("invalid expected identity")
		}
	}
	if _, err := NewTestSessionGuard(nil, expected, DefaultTestHeartbeatPolicy()); err == nil {
		t.Fatal("nil source accepted")
	}
}
