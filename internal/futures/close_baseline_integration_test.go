package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func newCloseBaselineFixture(t *testing.T) *closeReservationFixture {
	t.Helper()
	f := newCloseReservationFixture(t)
	migrateFuturesTest(t, f.db, f.ctx, "000046_futures_test_trade_publication", "up")
	return f
}

// Exercise the common durable publisher with a synthetic transport. This does
// not broaden the public SHFE-only TEST policy or touch Redis/broker services.
func publishCloseFixture(f *closeReservationFixture, c CommandRegistration, transport *fakeQueryPublisher) (QueryPublication, error) {
	binding, _ := json.Marshal(map[string]any{"target": f.target})
	return f.commands.publishTestRegistered(f.ctx, c.Scope, c.CommandID, c.Original.MessageID, f.target, transport,
		"futures_test_trade_publications", binding, func(CommandRegistration) error { return nil },
		func(ctx context.Context, tx *sql.Tx, c CommandRegistration, binding, proof []byte) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO futures_test_trade_publications(command_id,assessment_key,action,target_json,proof_json,state)
				VALUES($1,$2,'order.submit',$3,$4,'attempted')`, c.CommandID, fmt.Sprint("synthetic-", c.CommandID), binding, proof)
			return err
		})
}

func closeFixtureTerminal(t *testing.T, f *closeReservationFixture, c CommandRegistration, status string, filled Count) {
	t.Helper()
	o := closeCapacityOrder(c, status, filled)
	o.CounterSessionID = f.target.CounterSessionID
	f.observe(t, fmt.Sprint("terminal-", c.CommandID), "order", o)
}

func closeFixturePosition(t *testing.T, f *closeReservationFixture, minute int, position, today, frozen Count) CommandRegistration {
	t.Helper()
	id := fmt.Sprint("position-", minute)
	raw := editBody(t, f.c.Original.Raw, func(m map[string]any) {
		m["message_id"], m["idempotency_key"], m["request_id"], m["correlation_id"] = id, id, id, id
		m["sent_at"] = fmt.Sprintf("2026-11-04T13:%02d:00+08:00", minute)
	})
	c, err := f.commands.Register(f.ctx, f.c.Scope, "cmd.query", raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	seedPublishedReview(t, f.db, f.ctx, c, f.target)
	p := newCloseCapacityFixture(t).reply.Snapshot.Positions[0]
	p.AccountID = f.c.Scope.Route.AccountID
	p.CapturedAt = fmt.Sprintf("2026-11-04T13:%02d:10+08:00", minute)
	p.Futures.Position, p.Futures.TodayPosition, p.Futures.ShortFrozen = position, today, frozen
	body := editBody(t, boundReply(t, f.raw, c), func(m map[string]any) {
		m["produced_at"] = fmt.Sprintf("2026-11-04T13:%02d:20+08:00", minute)
		m["payload"].(map[string]any)["items"] = []PositionRow{p}
	})
	f.append(t, body)
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.Delivered != 1 || page.Invalid != 0 {
		t.Fatal(page, err)
	}
	return c
}

func closeFixtureIntentAt(t *testing.T, f *closeReservationFixture, key string, qty Count, minute int) ([]byte, RegistrationEvidence) {
	t.Helper()
	raw, e := f.intent(key, qty, "S")
	return editBody(t, raw, func(m map[string]any) { m["sent_at"] = fmt.Sprintf("2026-11-04T13:%02d:00+08:00", minute) }), e
}

func TestCFFEXCloseBaselineRebaseAndHistoricalReplay(t *testing.T) {
	f := newCloseBaselineFixture(t)
	raw, e := f.intent("first", 6, "S")
	first, err := f.reserve(raw, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCloseFixture(f, first.Command, &fakeQueryPublisher{}); err != nil {
		t.Fatal(err)
	}
	closeFixtureTerminal(t, f, first.Command, "cancelled", 2)
	next := closeFixturePosition(t, f, 20, 8, 1, 2)
	v, err := f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, f.target, "IF2701", "S", next.CommandID)
	if err != nil || v.Absorbed[first.Command.CommandID] != 2 || v.PreviousPositionCommandID != f.c.CommandID {
		t.Fatal(v, err)
	}
	f.commands, _ = NewCommandStore(f.db)
	again, err := f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, f.target, "IF2701", "S", next.CommandID)
	if err != nil || !reflect.DeepEqual(v, again) || storedCount(t, f.db, "futures_close_baselines") != 2 {
		t.Fatal(again, err)
	}
	old, err := f.reserve(raw, e)
	if err != nil || old.Command.Outcome != "replayed" || !reflect.DeepEqual(first.Reservation, old.Reservation) {
		t.Fatal("historical reservation overwritten", old, err)
	}
	raw2, e2 := closeFixtureIntentAt(t, f, "second", 6, 21)
	second, err := f.commands.RegisterCFFEXClose(f.ctx, f.c.Scope, raw2, e2, f.target, next.CommandID)
	if err != nil || second.Reservation.Before.Available != 6 || second.Reservation.Before.ClosedSinceQuery != 0 || second.Reservation.Before.BrokerFrozen != 2 {
		t.Fatal("old fill deducted twice", second, err)
	}
	if _, err := publishCloseFixture(f, second.Command, &fakeQueryPublisher{}); err != nil {
		t.Fatal(err)
	}
	closeFixtureTerminal(t, f, second.Command, "cancelled", 1)
	third := closeFixturePosition(t, f, 30, 7, 0, 2)
	v, err = f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, f.target, "IF2701", "S", third.CommandID)
	if err != nil || len(v.Absorbed) != 2 || v.Absorbed[second.Command.CommandID] != 1 {
		t.Fatal(v, err)
	}
	raw3, e3 := closeFixtureIntentAt(t, f, "third", 5, 31)
	last, err := f.commands.RegisterCFFEXClose(f.ctx, f.c.Scope, raw3, e3, f.target, third.CommandID)
	if err != nil || last.Reservation.Before.Available != 5 || last.Reservation.Before.ClosedSinceQuery != 0 {
		t.Fatal(last, err)
	}
	if _, err := f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, f.target, "IF2701", "S", next.CommandID); !errors.Is(err, ErrCloseReservation) {
		t.Fatal("baseline regressed", err)
	}
	// Absorption is not permission to discard or ignore late conflicting facts.
	o := closeCapacityOrder(first.Command, "cancelled", 3)
	o.CounterSessionID = f.target.CounterSessionID
	b, _ := json.Marshal(o)
	ts, _ := NewTradeStore(f.db)
	_, err = ts.Observe(f.ctx, TradeScope{f.c.Scope.Route, f.c.Scope.NativeBrokerID, f.c.Scope.InvestorID, f.target.TradeDate}, "late-contradiction", "order", b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCloseFixture(f, last.Command, &fakeQueryPublisher{}); err == nil {
		t.Fatal("late contradiction ignored at publication")
	}
}

func TestCFFEXCloseBaselineRefusesAmbiguousCoverage(t *testing.T) {
	for _, scenario := range []string{"working", "unpublished", "unknown_publish", "overlap", "position", "freeze_release", "today_increase", "wrong_session", "changed_binding", "history_corrupt", "history_missing"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCloseBaselineFixture(t)
			raw, e := f.intent("first", 6, "S")
			first, err := f.reserve(raw, e)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "unpublished" {
				_, err := publishCloseFixture(f, first.Command, &fakeQueryPublisher{appendError: scenario == "unknown_publish"})
				if scenario == "unknown_publish" {
					if !errors.Is(err, ErrQueryPublishUnknown) {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			status := "cancelled"
			if scenario == "working" {
				status = "working"
			}
			o := closeCapacityOrder(first.Command, status, 2)
			o.CounterSessionID = f.target.CounterSessionID
			if scenario == "wrong_session" {
				o.CounterSessionID = "foreign-session"
			}
			f.observe(t, "terminal", "order", o)
			position, today, frozen := Count(8), Count(1), Count(2)
			switch scenario {
			case "position":
				position = 9
			case "freeze_release":
				frozen = 1
			case "today_increase":
				today = 4
			}
			next := closeFixturePosition(t, f, 20, position, today, frozen)
			target := f.target
			switch scenario {
			case "overlap":
				_, err = f.db.ExecContext(f.ctx, `UPDATE futures_test_query_publications SET attempted_at=clock_timestamp()-interval '1 hour' WHERE command_id=$1`, next.CommandID)
			case "changed_binding":
				target.CounterSessionID = "next-session"
			case "history_corrupt":
				_, err = f.db.ExecContext(f.ctx, `UPDATE futures_close_baselines SET evidence='{}'::bytea`)
			case "history_missing":
				_, err = f.db.ExecContext(f.ctx, `DELETE FROM futures_close_baselines`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, target, "IF2701", "S", next.CommandID); !errors.Is(err, ErrCloseReservation) {
				t.Fatal("unsafe rebase", err)
			}
			var head int64
			if err := f.db.QueryRowContext(f.ctx, `SELECT position_command_id FROM futures_close_buckets`).Scan(&head); err != nil || head != f.c.CommandID {
				t.Fatal("partial head advance", head, err)
			}
		})
	}
}

func TestCFFEXCloseBaselineTransactionAndRace(t *testing.T) {
	f := newCloseBaselineFixture(t)
	raw, e := f.intent("first", 6, "S")
	first, err := f.reserve(raw, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCloseFixture(f, first.Command, &fakeQueryPublisher{}); err != nil {
		t.Fatal(err)
	}
	closeFixtureTerminal(t, f, first.Command, "cancelled", 2)
	next := closeFixturePosition(t, f, 20, 8, 1, 2)
	_, err = f.db.ExecContext(f.ctx, `CREATE FUNCTION reject_rebase() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rebase fault'; END $$;
		CREATE TRIGGER reject_rebase BEFORE UPDATE ON futures_close_buckets FOR EACH ROW EXECUTE FUNCTION reject_rebase()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, f.target, "IF2701", "S", next.CommandID); err == nil || storedCount(t, f.db, "futures_close_baselines") != 1 {
		t.Fatal("partial history commit", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `DROP TRIGGER reject_rebase ON futures_close_buckets`); err != nil {
		t.Fatal(err)
	}
	// A registration on the old baseline and a rebase must not both succeed.
	raw2, e2 := closeFixtureIntentAt(t, f, "competing", 6, 21)
	var wg sync.WaitGroup
	var registerErr, rebaseErr error
	wg.Add(2)
	go func() { defer wg.Done(); _, registerErr = f.reserve(raw2, e2) }()
	go func() {
		defer wg.Done()
		_, rebaseErr = f.commands.RebaseCFFEXClose(f.ctx, f.c.Scope, f.target, "IF2701", "S", next.CommandID)
	}()
	wg.Wait()
	// The old baseline is now stale even if registration wins the route lock.
	if registerErr == nil || rebaseErr != nil {
		t.Fatal(registerErr, rebaseErr)
	}
}

func TestCFFEXClosePublicationRechecksAndNeverResends(t *testing.T) {
	for _, scenario := range []string{"ok", "unreserved", "pending", "external", "order_already_seen", "corrupt", "newer_snapshot", "lost_response"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCloseBaselineFixture(t)
			raw, e := f.intent("first", 6, "S")
			var c CommandRegistration
			if scenario == "unreserved" {
				var err error
				c, err = f.commands.Register(f.ctx, f.c.Scope, "cmd.trade", raw, e)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				r, err := f.reserve(raw, e)
				if err != nil {
					t.Fatal(err)
				}
				c = r.Command
			}
			switch scenario {
			case "pending":
				captureStream(t, f.store, f.ctx, f.event, "0-0", StreamInput{"1-0", []byte("unprocessed original")})
			case "external", "order_already_seen":
				o := closeCapacityOrder(c, "working", 0)
				if scenario == "external" {
					o.GatewayOrderID = "external-order"
				}
				f.observe(t, "new-order", "order", o)
			case "corrupt":
				if _, err := f.db.ExecContext(f.ctx, `UPDATE futures_close_reservations SET evidence='{}'::bytea`); err != nil {
					t.Fatal(err)
				}
			case "newer_snapshot":
				closeFixturePosition(t, f, 20, 4, 1, 2)
			}
			transport := &fakeQueryPublisher{appendError: scenario == "lost_response"}
			result, err := publishCloseFixture(f, c, transport)
			if scenario != "ok" && scenario != "lost_response" {
				if err == nil || transport.appends.Load() != 0 || storedCount(t, f.db, "futures_test_trade_publications") != 0 {
					t.Fatal(result, err)
				}
				return
			}
			if scenario == "ok" && err != nil {
				t.Fatal(err)
			}
			if scenario == "lost_response" && !errors.Is(err, ErrQueryPublishUnknown) {
				t.Fatal(err)
			}
			if transport.appends.Load() != 1 {
				t.Fatal("unexpected append count")
			}
			// Recovery resolves existing attempts before new capacity checks.
			captureStream(t, f.store, f.ctx, f.event, "0-0", StreamInput{"1-0", []byte("later unprocessed input")})
			f.commands, _ = NewCommandStore(f.db)
			again, againErr := publishCloseFixture(f, c, transport)
			if !again.Replayed || again.State != result.State || transport.appends.Load() != 1 || (againErr == nil) != (err == nil) {
				t.Fatal(again, againErr)
			}
		})
	}
}
