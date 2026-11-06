package futures

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTestSessionGuardedConsumerStopsAndRetires(t *testing.T) {
	for _, mode := range []string{"missing", "revoke_partial", "revoke_complete", "expire"} {
		t.Run(mode, func(t *testing.T) {
			c, db, ctx := realConsumer(t, 1)
			c.options.MinDelay, c.options.MaxDelay = time.Second, time.Second
			g, err := NewTestSessionGuard(c.source, c.expected, DefaultTestHeartbeatPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "expire" {
				g.policy.MaxAge = 500 * time.Millisecond
			}
			if mode == "revoke_complete" {
				c.options.PageSize = 4
			}
			if mode != "missing" {
				if err := g.Observe(guardSource(g, "1-0"), guardBody(t, g, "hb", time.Now())); err != nil {
					t.Fatal(err)
				}
			}
			window, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			cycles, stopped := 0, false
			err = c.RunGuarded(window, g, func(cycle ConsumerCycle) {
				if cycle.Stopped {
					stopped = cycle.Quality.Blocked
					return
				}
				cycles++
				if mode == "revoke_partial" || mode == "revoke_complete" {
					g.Revoke()
				}
			})
			want := ErrTestSessionStopped
			if mode == "missing" {
				want = ErrTestSessionMissing
			}
			if !errors.Is(err, want) || !stopped {
				t.Fatal(mode, err, stopped)
			}
			var ready, retired int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='ready'),count(*) FILTER(WHERE retired) FROM futures_query_batches`).Scan(&ready, &retired); err != nil || retired != 4 {
				t.Fatal(ready, retired, err)
			}
			if mode == "missing" && (cycles != 0 || ready != 0 || storedCount(t, db, "futures_stream_inbox") != 0) {
				t.Fatal("missing heartbeat allowed consumption")
			}
			if mode == "revoke_partial" && (cycles != 1 || ready != 1) {
				t.Fatal("partial stop changed completed history", cycles, ready)
			}
			if mode == "revoke_complete" && (cycles != 1 || ready != 4) {
				t.Fatal("completed history lost", cycles, ready)
			}
			if mode == "expire" && cycles > 1 {
				t.Fatal("expired between cycles but resumed", cycles)
			}
			if storedCount(t, db, "futures_commands") != 5 {
				t.Fatal("guard created commands")
			}
		})
	}
}

func TestTestSessionGuardedConsumerBinding(t *testing.T) {
	c, _, ctx := realConsumer(t, 1)
	for _, mutate := range []func(*DeliveryQualityContext){
		func(e *DeliveryQualityContext) { e.ConnectionGeneration++ },
		func(e *DeliveryQualityContext) { e.CounterSessionID = "other" },
		func(e *DeliveryQualityContext) { e.Scope.TradeDate = "20261109" },
	} {
		expected := c.expected
		mutate(&expected)
		g, err := NewTestSessionGuard(c.source, expected, DefaultTestHeartbeatPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.RunGuarded(ctx, g, nil); !errors.Is(err, ErrStreamIdentity) {
			t.Fatal("guard binding drift accepted", err)
		}
	}
	if c.used.Load() {
		t.Fatal("rejected binding consumed the loop")
	}
}
