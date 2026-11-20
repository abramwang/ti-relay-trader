package futures

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

type closeReservationFixture struct {
	*replyFixture
	commands *CommandStore
	event    StreamHandle
}

func newCloseReservationFixture(t *testing.T) *closeReservationFixture {
	t.Helper()
	r := projectionReplyFixture(t, "positions")
	migrateFuturesTest(t, r.db, r.ctx, "000051_futures_close_reservations", "up")
	commands, _ := NewCommandStore(r.db)
	event, err := r.store.Open(r.ctx, StreamConfig{Scope: r.c.Scope, Role: "event", SourceID: r.target.SourceID})
	if err != nil {
		t.Fatal(err)
	}
	p := newCloseCapacityFixture(t).reply.Snapshot.Positions[0]
	p.AccountID = r.c.Scope.Route.AccountID
	p.CapturedAt = "2026-11-04T13:09:00+08:00"
	r.raw = editBody(t, r.raw, func(m map[string]any) {
		m["produced_at"] = "2026-11-04T13:10:00+08:00"
		m["payload"].(map[string]any)["items"] = []PositionRow{p}
	})
	r.append(t, r.raw)
	if page, err := r.p.Step(r.ctx, r.h, 32); err != nil || page.Delivered != 1 || page.Invalid != 0 {
		t.Fatal(page, err)
	}
	return &closeReservationFixture{r, commands, event}
}

func (f *closeReservationFixture) intent(key string, qty Count, side string) ([]byte, RegistrationEvidence) {
	at, _ := time.Parse(time.RFC3339Nano, "2026-11-04T13:11:00+08:00")
	r := OrderExecutionRequest{IdempotencyKey: key, Order: OrderPreviewRequest{f.c.Scope.Route.AccountID,
		"IF2701.CFFEX", f.target.TradeDate, side, "C", "speculation", qty, "3500.2"}}
	return buildServiceOrder(f.c.Scope, r, CatalogBinding{"IF2701.CFFEX", "IF2701", "CFFEX", "fixture-catalog", "fixture-checksum"}, at)
}

func (f *closeReservationFixture) reserve(raw []byte, e RegistrationEvidence) (CloseRegistration, error) {
	return f.commands.RegisterCFFEXClose(f.ctx, f.c.Scope, raw, e, f.target, f.c.CommandID)
}

func (f *closeReservationFixture) observe(t *testing.T, source, kind string, fact any) {
	t.Helper()
	raw, _ := json.Marshal(fact)
	store, _ := NewTradeStore(f.db)
	scope := TradeScope{f.c.Scope.Route, f.c.Scope.NativeBrokerID, f.c.Scope.InvestorID, f.target.TradeDate}
	if out, err := store.Observe(f.ctx, scope, source, kind, raw); err != nil || out.Blocked {
		t.Fatal(out, err)
	}
}

func TestCFFEXCloseReservationConcurrentQuantity(t *testing.T) {
	f := newCloseReservationFixture(t)
	var wg sync.WaitGroup
	results := make(chan CloseRegistration, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		raw, e := f.intent(fmt.Sprint("request-", i), 3, "S")
		wg.Add(1)
		go func() { defer wg.Done(); r, err := f.reserve(raw, e); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	failed := 0
	for err := range errs {
		if err != nil {
			if !errors.Is(err, ErrCloseReservation) {
				t.Fatal(err)
			}
			failed++
		}
	}
	available := map[int64]bool{}
	for r := range results {
		if r.Reservation != nil {
			if r.Command.Outcome != "registered" || r.Reservation.Quantity != 3 {
				t.Fatal(r)
			}
			available[r.Reservation.Before.Available] = true
		}
	}
	if failed != 10 || len(available) != 2 || !available[8] || !available[5] {
		t.Fatal(failed, available)
	}
	if storedCount(t, f.db, "futures_commands") != 3 || storedCount(t, f.db, "futures_close_reservations") != 2 || storedCount(t, f.db, "futures_command_reservations") != 4 || storedCount(t, f.db, "futures_command_audit") != 3 {
		t.Fatal("partial/excess reservation")
	}
}

func TestCFFEXCloseReservationConcurrentReplayRestartAndConflict(t *testing.T) {
	f := newCloseReservationFixture(t)
	raw, e := f.intent("same", 8, "S")
	var wg sync.WaitGroup
	results := make(chan CloseRegistration, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := f.reserve(raw, e); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original *CloseReservation
	newCount := 0
	for r := range results {
		if r.Command.Outcome == "registered" {
			newCount++
		}
		if original == nil {
			original = r.Reservation
		}
		if !reflect.DeepEqual(original, r.Reservation) {
			t.Fatal("replay changed reservation")
		}
	}
	if newCount != 1 {
		t.Fatal(newCount)
	}
	// A new reader and an unusable current target still return the original
	// responsibility, not a new send authorization or a second hold.
	f.commands, _ = NewCommandStore(f.db)
	r, err := f.commands.RegisterCFFEXClose(f.ctx, f.c.Scope, raw, e, TestQueryTarget{}, 0)
	if err != nil || r.Command.Outcome != "replayed" || !reflect.DeepEqual(original, r.Reservation) {
		t.Fatal(r, err)
	}
	alias := editBody(t, raw, func(m map[string]any) { m["message_id"] = "alias-message" })
	r, err = f.reserve(alias, e)
	if err != nil || r.Command.Outcome != "replayed" || !reflect.DeepEqual(original, r.Reservation) {
		t.Fatal(r, err)
	}
	conflict := editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["order_qty"] = 1 })
	r, err = f.reserve(conflict, e)
	if err != nil || r.Command.Outcome != "conflict" || r.Reservation != nil {
		t.Fatal(r, err)
	}
	if storedCount(t, f.db, "futures_close_reservations") != 1 || storedCount(t, f.db, "futures_commands") != 2 {
		t.Fatal("duplicated reservation")
	}
}

func TestCFFEXCloseReservationTerminalReleasesOnlyRemainder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			f := newCloseReservationFixture(t)
			raw, e := f.intent("first", 6, "S")
			first, err := f.reserve(raw, e)
			if err != nil {
				t.Fatal(err)
			}
			raw2, e2 := f.intent("second", 3, "S")
			if _, err := f.reserve(raw2, e2); !errors.Is(err, ErrCloseReservation) {
				t.Fatal("unknown responsibility released", err)
			}
			working := closeCapacityOrder(first.Command, "working", 2)
			f.observe(t, "working", "order", working)
			if _, err := f.reserve(raw2, e2); !errors.Is(err, ErrCloseReservation) {
				t.Fatal("partial fill released remaining", err)
			}
			cancelled := closeCapacityOrder(first.Command, "cancelled", 2)
			fill := closeCapacityFill(TradeScope{f.c.Scope.Route, f.c.Scope.NativeBrokerID, f.c.Scope.InvestorID, f.target.TradeDate}, working, "fill-1", 2)
			if reverse {
				f.observe(t, "cancelled", "order", cancelled)
			}
			f.observe(t, "fill", "fill", fill)
			f.observe(t, "cancelled", "order", cancelled)
			f.observe(t, "fill", "fill", fill)
			f.commands, _ = NewCommandStore(f.db)
			raw3, e3 := f.intent("after-cancel", 6, "S")
			after, err := f.reserve(raw3, e3)
			if err != nil || after.Reservation.Before.Available != 6 || after.Reservation.Before.ClosedSinceQuery != 2 || after.Reservation.Before.LocalReserved != 0 || after.Reservation.Before.BrokerFrozen != 2 {
				t.Fatal(after, err)
			}
			if storedCount(t, f.db, "futures_close_reservations") != 2 || storedCount(t, f.db, "futures_execution_facts") != 1 {
				t.Fatal("facts duplicated or historical hold removed")
			}
		})
	}
}

func TestCFFEXCloseReservationAtomicRollback(t *testing.T) {
	for _, table := range []string{"futures_close_reservations", "futures_command_audit"} {
		t.Run(table, func(t *testing.T) {
			f := newCloseReservationFixture(t)
			_, err := f.db.ExecContext(f.ctx, `CREATE FUNCTION reject_close() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic close transaction fault'; END $$;
				CREATE TRIGGER reject_close BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION reject_close()`)
			if err != nil {
				t.Fatal(err)
			}
			raw, e := f.intent("rollback", 8, "S")
			if _, err := f.reserve(raw, e); err == nil {
				t.Fatal("injected failure hidden")
			}
			for _, name := range []string{"futures_close_buckets", "futures_close_baselines", "futures_close_reservations", "futures_command_reservations", "futures_trade_scopes"} {
				if storedCount(t, f.db, name) != 0 {
					t.Fatal("partial commit", name)
				}
			}
			if storedCount(t, f.db, "futures_commands") != 1 || storedCount(t, f.db, "futures_command_messages") != 1 || storedCount(t, f.db, "futures_command_audit") != 1 {
				t.Fatal("dangling command")
			}
			if _, err := f.db.ExecContext(f.ctx, `DROP TRIGGER reject_close ON `+table); err != nil {
				t.Fatal(err)
			}
			if r, err := f.reserve(raw, e); err != nil || r.Command.Outcome != "registered" {
				t.Fatal(r, err)
			}
		})
	}
}

func TestCFFEXCloseReservationRejectsUnmanagedFactsAndIntentions(t *testing.T) {
	for _, scenario := range []string{"unreserved", "external_order", "pending_fill", "pending_stream", "lost_stream_input", "corrupt_trade", "corrupt_reply", "scope_changed", "epoch_changed", "unconfirmed_query", "incomplete_query"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCloseReservationFixture(t)
			raw, e := f.intent("unmanaged", 1, "S")
			c, _ := DecodeCommand(raw, f.c.Scope, "cmd.trade")
			reg := CommandRegistration{CommandID: 987, Scope: f.c.Scope, Original: c}
			switch scenario {
			case "unreserved":
				if _, err := f.commands.Register(f.ctx, f.c.Scope, "cmd.trade", raw, e); err != nil {
					t.Fatal(err)
				}
			case "external_order", "pending_fill", "corrupt_trade":
				order := closeCapacityOrder(reg, "working", 0)
				if scenario == "external_order" {
					f.observe(t, "external", "order", order)
				}
				if scenario == "pending_fill" {
					scope := TradeScope{f.c.Scope.Route, f.c.Scope.NativeBrokerID, f.c.Scope.InvestorID, f.target.TradeDate}
					x, _ := json.Marshal(closeCapacityFill(scope, order, "unknown", 1))
					trades, _ := NewTradeStore(f.db)
					if out, err := trades.Observe(f.ctx, scope, "pending", "fill", x); err != nil || !out.Blocked {
						t.Fatal(out, err)
					}
				}
				if scenario == "corrupt_trade" {
					f.observe(t, "external", "order", order)
					if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_trade_observations SET body='{}'::bytea`); err != nil {
						t.Fatal(err)
					}
				}
			case "pending_stream", "lost_stream_input":
				captureStream(t, f.store, f.ctx, f.event, "0-0", StreamInput{"1-0", []byte(`{"unprocessed":true}`)})
				if scenario == "lost_stream_input" {
					if _, err := f.db.ExecContext(f.ctx, `DELETE FROM futures_stream_inbox WHERE stream_key=$1`, f.event.stream); err != nil {
						t.Fatal(err)
					}
				}
			case "corrupt_reply":
				if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_stream_inbox SET body='{}'::bytea`); err != nil {
					t.Fatal(err)
				}
			case "scope_changed":
				f.target.CounterSessionID = "new-session"
			case "epoch_changed":
				f.target.Epoch = "other-epoch"
			case "unconfirmed_query":
				if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_test_query_publications SET state='attempted',entry_id=NULL,finished_at=NULL`); err != nil {
					t.Fatal(err)
				}
			case "incomplete_query":
				bad := editBody(t, f.raw, func(m map[string]any) { m["message_id"] = "different-final" })
				f.append(t, bad)
				if _, err := f.p.Step(f.ctx, f.h, 32); err != nil {
					t.Fatal(err)
				}
			}
			raw, e = f.intent("candidate", 1, "S")
			if _, err := f.reserve(raw, e); err == nil {
				t.Fatal("unsafe close admitted")
			}
			if storedCount(t, f.db, "futures_close_reservations") != 0 || storedCount(t, f.db, "futures_close_buckets") != 0 {
				t.Fatal("unsafe reservation persisted")
			}
		})
	}
}

func TestCFFEXCloseReservationBaselineCannotBeReplaced(t *testing.T) {
	f := newCloseReservationFixture(t)
	raw, e := f.intent("first", 8, "S")
	if _, err := f.reserve(raw, e); err != nil {
		t.Fatal(err)
	}
	// A valid newer query must not erase outstanding local responsibility or
	// deduct the same broker freeze and local order again under a new baseline.
	queryRaw := editBody(t, f.c.Original.Raw, func(m map[string]any) {
		m["message_id"], m["idempotency_key"], m["request_id"], m["correlation_id"] = "new-position-query", "new-position-idem", "new-position-request", "new-position-correlation"
	})
	query, err := f.commands.Register(f.ctx, f.c.Scope, "cmd.query", queryRaw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	seedPublishedReview(t, f.db, f.ctx, query, f.target)
	body := boundReply(t, f.raw, query)
	f.append(t, body)
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.Invalid != 0 {
		t.Fatal(page, err)
	}
	raw2, e2 := f.intent("second", 1, "S")
	if _, err := f.commands.RegisterCFFEXClose(f.ctx, f.c.Scope, raw2, e2, f.target, query.CommandID); !errors.Is(err, ErrCloseReservation) {
		t.Fatal(err)
	}
	if storedCount(t, f.db, "futures_close_buckets") != 1 || storedCount(t, f.db, "futures_close_reservations") != 1 {
		t.Fatal("baseline switched")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_close_reservations SET evidence='{}'::bytea`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reserve(raw, e); err == nil {
		t.Fatal("corrupt reservation replayed")
	}
}

func TestCFFEXCloseReservationMigrationGuard(t *testing.T) {
	f := newCloseReservationFixture(t)
	migrateFuturesTest(t, f.db, f.ctx, "000051_futures_close_reservations", "down")
	migrateFuturesTest(t, f.db, f.ctx, "000051_futures_close_reservations", "up")
	raw, e := f.intent("hold", 1, "S")
	if _, err := f.reserve(raw, e); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/postgres/000051_futures_close_reservations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, string(down)); err == nil {
		t.Fatal("migration discarded quantity responsibility")
	}
	if storedCount(t, f.db, "futures_close_reservations") != 1 {
		t.Fatal("reservation lost")
	}
}

func TestCFFEXCloseReservationDoesNotEnableOtherScopes(t *testing.T) {
	for _, scenario := range []string{"prod", "open", "close_today", "close_yesterday", "SHFE", "hedge"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCloseCapacityFixture(t)
			reg := f.add(t, 1, "S")
			raw := editBody(t, reg.Original.Raw, func(m map[string]any) {
				p := m["payload"].(map[string]any)
				switch scenario {
				case "prod":
					f.scope.Route.Env = "prod"
				case "open":
					p["offset_type"] = "O"
				case "close_today":
					p["offset_type"] = "CT"
				case "close_yesterday":
					p["offset_type"] = "CY"
				case "SHFE":
					p["exchange"] = "SHFE"
				case "hedge":
					p["hedge_type"] = "hedge"
				}
			})
			// No database is available: rejection must precede all side effects.
			if _, err := (&CommandStore{}).RegisterCFFEXClose(t.Context(), f.scope, raw, reg.Evidence, f.target, 1); err == nil {
				t.Fatal("unsupported scope admitted")
			}
		})
	}
}

func (f *closeReservationFixture) otherRoute(t *testing.T, gateway string) *closeReservationFixture {
	t.Helper()
	scope := f.c.Scope
	scope.Route.GatewayID = gateway
	c, err := f.commands.Register(f.ctx, scope, "cmd.query", f.c.Original.Raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	seedPublishedReview(t, f.db, f.ctx, c, f.target)
	h, err := f.store.OpenReplyCapture(f.ctx, StreamConfig{Scope: scope, Role: "reply", SourceID: f.target.SourceID}, HeartbeatCheckpoint{Cursor: "0-0"}, f.target)
	if err != nil {
		t.Fatal(err)
	}
	event, err := f.store.Open(f.ctx, StreamConfig{Scope: scope, Role: "event", SourceID: f.target.SourceID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewReplyProjector(f.store, scope, f.target)
	if err != nil {
		t.Fatal(err)
	}
	r := &replyFixture{ctx: f.ctx, db: f.db, store: f.store, h: h, p: p, c: c, target: f.target, raw: boundReply(t, f.raw, c)}
	r.raw = editBody(t, r.raw, func(m map[string]any) { m["routing"] = scope.Route })
	r.append(t, r.raw)
	if page, err := p.Step(f.ctx, h, 32); err != nil || page.Delivered != 1 || page.Invalid != 0 {
		t.Fatal(page, err)
	}
	return &closeReservationFixture{r, f.commands, event}
}

func TestCFFEXCloseReservationCrossGatewayContenders(t *testing.T) {
	a := newCloseReservationFixture(t)
	b := a.otherRoute(t, "different-gateway")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, f := range []*closeReservationFixture{a, b} {
		raw, e := f.intent("first", 8, "S")
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.reserve(raw, e); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrCloseReservation) {
			t.Fatal(err)
		}
	}
	if success != 1 || storedCount(t, a.db, "futures_close_reservations") != 1 || storedCount(t, a.db, "futures_commands") != 3 {
		t.Fatal("native investor capacity doubled", success)
	}
}

func TestCFFEXCloseReservationRejectAndCancelRequest(t *testing.T) {
	for _, status := range []string{"accepted", "rejected", "filled"} {
		t.Run(status, func(t *testing.T) {
			f := newCloseReservationFixture(t)
			raw, e := f.intent("first", 8, "S")
			first, err := f.reserve(raw, e)
			if err != nil {
				t.Fatal(err)
			}
			filled := Count(0)
			if status == "filled" {
				filled = 8
			}
			f.observe(t, "order", "order", closeCapacityOrder(first.Command, status, filled))
			if status == "accepted" {
				// A registered cancel intention alone does not release any lots.
				cancelRaw := editBody(t, raw, func(m map[string]any) {
					m["action"], m["message_id"], m["idempotency_key"] = "order.cancel", "cancel-message", "cancel-idem"
					m["payload"] = map[string]any{"account_id": f.c.Scope.Route.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2", "gateway_order_id": first.Command.Original.Orders[0].GatewayOrderID, "trade_date": f.target.TradeDate, "cancel_id": "cancel-attempt"}
				})
				evidence := RegistrationEvidence{CancelTarget: &CancelTarget{f.c.Scope, first.Command.Original.Orders[0].GatewayOrderID, f.target.TradeDate}}
				if r, err := f.commands.Register(f.ctx, f.c.Scope, "cmd.trade", cancelRaw, evidence); err != nil || r.Outcome != "registered" {
					t.Fatal(r, err)
				}
			}
			raw2, e2 := f.intent("second", 8, "S")
			r, err := f.reserve(raw2, e2)
			if status == "rejected" {
				if err != nil || r.Reservation.Before.Available != 8 || r.Reservation.Before.ClosedSinceQuery != 0 {
					t.Fatal(r, err)
				}
			} else if !errors.Is(err, ErrCloseReservation) {
				t.Fatal("released without unfilled terminal", r, err)
			}
		})
	}
}
