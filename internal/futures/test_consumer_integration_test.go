package futures

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
)

func TestQueryRetirePreservesCompletedHistory(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
			bindQuery(t, q, ctx, "owned", r, c)
			if complete {
				if _, err := q.Ingest(ctx, "owned", QuerySource{queryReplyStream(c.Route), "1-0"}, boundReply(t, fixtureBody(t, "messages.json", "positions"), r), 0); err != nil {
					t.Fatal(err)
				}
			}
			want := BatchInterrupted
			if complete {
				want = BatchReady
			}
			for i := 0; i < 2; i++ {
				if state, err := q.Retire(ctx, "owned", "controlled stop"); err != nil || state != want {
					t.Fatal(state, err)
				}
			}
			if _, err := q.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
				t.Fatal("retired snapshot was current", err)
			}
			if complete {
				if _, err := q.Snapshot(ctx, "owned"); err != nil {
					t.Fatal("completed history lost", err)
				}
			}
			if _, err := q.Advance(ctx, "owned", time.Second); !errors.Is(err, ErrBatchOwner) {
				t.Fatal("retired receiver resumed", err)
			}
			other, _ := NewQueryStore(db)
			if _, err := other.Retire(ctx, "owned", "not owner"); !errors.Is(err, ErrBatchOwner) {
				t.Fatal(err)
			}
		})
	}
}

func TestTestSourcePersistenceRejectsEndpointDrift(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	c := sourceFixture()
	source, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	s, h := openStream(t, db, ctx, source.StreamConfig("event"))
	c.RedisURL = "redis://localhost:6380/0"
	other, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := s.Open(ctx, other.StreamConfig("event")); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("endpoint change reused checkpoint", err)
	}
	if _, err := other.ReadOnce(ctx, s, h, 1); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("wrong source reached Redis", err)
	}
}

func realConsumer(t *testing.T, pageSize int) (*TestConsumer, *sql.DB, context.Context) {
	t.Helper()
	db, store, base, client, _, ctx := realStream(t)
	cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
	if err != nil {
		t.Fatal("TEST configuration unavailable")
	}
	scope := base.config.Scope
	scope.Route.GatewayID += "-loop"
	source, err := NewTestStreamSource(TestSourceConfig{scope, "synthetic-loop-deployment", cfg.Redis.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	_, event := openStream(t, db, ctx, source.StreamConfig("event"))
	_, reply := openStream(t, db, ctx, source.StreamConfig("reply"))
	day := tradeScope()
	day.Route = scope.Route
	events, _ := NewEventStore(db)
	key, err := events.RegisterScope(ctx, day, scope.UserID, event.stream)
	if err != nil {
		t.Fatal(err)
	}
	registerSubmit(t, events, ctx, key)
	routeBody := func(raw []byte) []byte {
		return editBody(t, raw, func(m map[string]any) {
			m["routing"].(map[string]any)["broker_id"] = scope.Route.BrokerID
			m["routing"].(map[string]any)["gateway_id"] = scope.Route.GatewayID
		})
	}
	for _, h := range []StreamHandle{event, reply} {
		if n, err := client.Exists(ctx, h.stream).Result(); err != nil || n != 0 {
			t.Fatal("development key already exists")
		}
		key := h.stream
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.Del(cleanup, key).Err(); err != nil {
				t.Error("development key cleanup failed", key, err)
			}
		})
	}
	fill := editBody(t, routeBody(fixtureBody(t, "messages.json", "fill")), func(m map[string]any) {
		m["payload"].(map[string]any)["fill_id"] = nativeIdentity("fill", day, "SHFE", "00005678")
	})
	appendRealStream(t, ctx, client, event, "1-0", "body", fill)
	if err := client.Expire(ctx, event.stream, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	appendRealStream(t, ctx, client, event, "2-0", "body", routeBody(fixtureBody(t, "messages.json", "order")))
	q, _ := NewQueryStore(db)
	bindings := []QueryDispatchBinding{}
	for i, kind := range []string{"asset", "positions", "order", "fill"} {
		started := time.Now()
		c := batchContext(kind)
		raw := fixtureBody(t, "messages.json", kind)
		if kind == "order" || kind == "fill" {
			c = tradeQueryContext(kind)
			raw = tradePage(t, kind, "runtime-"+kind, 0, true)
		}
		c.Route = scope.Route
		r, c := registeredQueryCommand(t, db, ctx, c)
		bindQuery(t, q, ctx, kind, r, c)
		bindings = append(bindings, QueryDispatchBinding{kind, started})
		appendRealStream(t, ctx, client, reply, fmt.Sprintf("%d-0", i+1), "body", routeBody(boundReply(t, raw, r)))
		if i == 0 {
			if err := client.Expire(ctx, reply.stream, time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	ed, err := NewStreamDispatcher(ctx, store, event, nil, []EventDispatchBinding{{key, "ctpproc-fixture-1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rd, err := NewStreamDispatcher(ctx, store, reply, q, nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	options := TestConsumerOptions{pageSize, 10 * time.Millisecond, 40 * time.Millisecond, 10 * time.Second}
	consumer, err := NewTestConsumer(ctx, source, ed, rd, events, DeliveryQualityContext{day, "ctpproc-fixture-1", 1}, options)
	if err != nil {
		t.Fatal(err)
	}
	return consumer, db, ctx
}

func TestTestConsumerRealRedisCompletionAndStop(t *testing.T) {
	c, db, ctx := realConsumer(t, 1)
	window, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cycles, clean, stopped := 0, false, false
	err := c.Run(window, func(cycle ConsumerCycle) {
		if cycle.Stopped {
			stopped = cycle.Quality.Blocked
			return
		}
		cycles++
		if !cycle.Quality.Blocked {
			clean = true
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || !clean || !stopped || cycles < 4 {
		t.Fatal(cycles, clean, stopped, err)
	}
	var ready, retired int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='ready'),count(*) FILTER(WHERE retired) FROM futures_query_batches`).Scan(&ready, &retired); err != nil || ready != 4 || retired != 4 {
		t.Fatal("shutdown changed completed history", ready, retired, err)
	}
	if storedCount(t, db, "futures_order_states") != 1 || storedCount(t, db, "futures_execution_facts") != 1 || storedCount(t, db, "futures_commands") != 5 {
		t.Fatal("runtime fabricated commands or duplicated trades")
	}
	again, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err := c.Run(again, nil); err == nil {
		t.Fatal("consumer restarted with retired queries")
	}
}

func TestTestConsumerStopPreservesPartialAndNoAutomaticRetry(t *testing.T) {
	for _, mode := range []string{"cancel", "connection_closed", "owner_changed"} {
		t.Run(mode, func(t *testing.T) {
			c, db, ctx := realConsumer(t, 1)
			window, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if mode == "connection_closed" {
				_ = c.source.Close()
			}
			cycles := 0
			stopped := false
			err := c.Run(window, func(cycle ConsumerCycle) {
				if cycle.Stopped {
					stopped = cycle.Quality.Blocked
					return
				}
				cycles++
				if mode == "cancel" {
					cancel()
				} else if mode == "owner_changed" {
					if _, err := c.event.store.Open(ctx, c.event.handle.config); err != nil {
						t.Fatal(err)
					}
				}
			})
			if err == nil || !stopped || cycles > 1 {
				t.Fatal("unexpected retry or success", cycles, stopped, err)
			}
			var ready, interrupted, retired int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE state='ready'),count(*) FILTER(WHERE state='interrupted'),count(*) FILTER(WHERE retired) FROM futures_query_batches`).Scan(&ready, &interrupted, &retired); err != nil {
				t.Fatal(err)
			}
			wantReady := 1
			if mode == "connection_closed" {
				wantReady = 0
			}
			if ready != wantReady || interrupted != 4-wantReady || retired != 4 {
				t.Fatal(ready, interrupted, retired)
			}
		})
	}
}

func TestTestConsumerWindowAndBindingGuards(t *testing.T) {
	c, _, ctx := realConsumer(t, 1)
	if err := c.Run(context.Background(), nil); err == nil {
		t.Fatal("unbounded run allowed")
	}
	long, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := c.Run(long, nil); err == nil {
		t.Fatal("oversized test window")
	}
	for _, mutate := range []func(*DeliveryQualityContext, *TestConsumerOptions){
		func(e *DeliveryQualityContext, o *TestConsumerOptions) { e.ConnectionGeneration++ },
		func(e *DeliveryQualityContext, o *TestConsumerOptions) { e.CounterSessionID = "other" },
		func(e *DeliveryQualityContext, o *TestConsumerOptions) { o.PageSize = 257 },
		func(e *DeliveryQualityContext, o *TestConsumerOptions) { o.MinDelay = 0 },
	} {
		expected, options := c.expected, c.options
		mutate(&expected, &options)
		if _, err := NewTestConsumer(ctx, c.source, c.event, c.reply, c.events, expected, options); err == nil {
			t.Fatal("invalid lifecycle binding accepted")
		}
	}
}

func TestTestConsumerIdleBackoffAndNoInputTimeout(t *testing.T) {
	c, db, ctx := realConsumer(t, 4)
	if cycle, err := c.step(ctx); err != nil || cycle.Quality.Blocked {
		t.Fatal(cycle, err)
	}
	refresh := batchContext("asset")
	refresh.Route, refresh.OriginMessageID = c.expected.Scope.Route, "timeout-without-reply"
	r, refresh := registeredQueryCommand(t, db, ctx, refresh)
	bindQuery(t, c.queries, ctx, "refresh", r, refresh)
	bindings := []QueryDispatchBinding{{"refresh", time.Now().Add(-2 * time.Minute)}}
	for _, request := range c.requests {
		if request.action != "account.asset.query" {
			bindings = append(bindings, request.QueryDispatchBinding)
		}
	}
	rd, err := NewStreamDispatcher(ctx, c.reply.store, c.reply.handle, c.queries, nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	loop, err := NewTestConsumer(ctx, c.source, c.event, rd, c.events, c.expected, c.options)
	if err != nil {
		t.Fatal(err)
	}
	window, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	delays := []time.Duration{}
	err = loop.Run(window, func(cycle ConsumerCycle) {
		if cycle.Stopped {
			return
		}
		if cycle.Progress || cycle.Captured != 0 || !cycle.Quality.Blocked {
			t.Fatal("idle work became progress or ready", cycle)
		}
		delays = append(delays, cycle.Delay)
		if len(delays) == 3 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || len(delays) != 3 || delays[0] != 20*time.Millisecond || delays[1] != 40*time.Millisecond || delays[2] != 40*time.Millisecond {
		t.Fatal(delays, err)
	}
	var state string
	var retired bool
	if err := db.QueryRowContext(ctx, `SELECT state,retired FROM futures_query_batches WHERE batch_id='refresh'`).Scan(&state, &retired); err != nil || state != string(BatchExpired) || !retired {
		t.Fatal(state, retired, err)
	}
	if storedCount(t, db, "futures_commands") != 6 || storedCount(t, db, "futures_query_snapshots") != 4 {
		t.Fatal("idle loop retried a command or manufactured a snapshot")
	}
}

func TestQueryRetireAtomicFailure(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, q, ctx, "owned", r, c)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_retire() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic retirement failure'; END $$; CREATE TRIGGER fail_retire BEFORE UPDATE OF retired ON futures_query_batches FOR EACH ROW EXECUTE FUNCTION fail_retire()`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Retire(ctx, "owned", "stop"); err == nil {
		t.Fatal("retirement failure hidden")
	}
	var state string
	var retired bool
	if err := db.QueryRowContext(ctx, `SELECT state,retired FROM futures_query_batches WHERE batch_id='owned'`).Scan(&state, &retired); err != nil || retired || state != string(BatchCollecting) {
		t.Fatal(state, retired, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_retire ON futures_query_batches`); err != nil {
		t.Fatal(err)
	}
	if state, err := q.Retire(ctx, "owned", "stop"); err != nil || state != BatchInterrupted {
		t.Fatal(state, err)
	}
}
