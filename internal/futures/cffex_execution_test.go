package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func cffexMarket(t *testing.T, m *orderMarketStub) {
	t.Helper()
	m.catalog = editBody(t, m.catalog, func(v map[string]any) {
		r := v["data"].([]any)[0].(map[string]any)
		r["security_id"], r["source_instrument_id"], r["exchange_id"] = "IF2701.CFFEX", "IF2701", "CFFEX"
		r["price_tick"], r["volume_multiple"] = json.Number("0.2"), 300
		v["meta"].(map[string]any)["exchange_coverage"] = map[string]any{"CFFEX": map[string]any{"status": "production_ready"}}
	})
	m.quote = editBody(t, m.quote, func(v map[string]any) {
		r := v["data"].([]any)[0].(map[string]any)
		r["security_id"], r["source_instrument_id"], r["exchange_id"], r["product_id"] = "IF2701.CFFEX", "IF2701", "CFFEX", "IF"
	})
}

func cffexRequest() OrderExecutionRequest {
	r := orderServiceRequest()
	r.Order.SecurityID, r.Order.TradeSide, r.Order.Qty, r.Order.Price = "IF2701.CFFEX", "S", 3, "3000.200"
	return r
}

func cffexService(t *testing.T, store *CommandStore, f *fakeQueryPublisher) (*TestOrderService, *orderMarketStub) {
	t.Helper()
	s, m, _ := serviceFixture(t, store, f)
	a := s.accounts["300052141"]
	a.CFFEX = &CFFEXExecutionPolicy{[]string{"IF", "IH", "IC", "IM"}, 10}
	s.accounts["300052141"] = a
	cffexMarket(t, m)
	return s, m
}

func TestCFFEXExecutionPolicyAndClock(t *testing.T) {
	p := &CFFEXExecutionPolicy{[]string{"IF", "IH", "IC", "IM"}, 10}
	for _, symbol := range []string{"IF2701", "IH2712", "IC2606", "IM2611"} {
		if !p.allows(symbol, 10) || p.allows(symbol, 11) || p.allows(symbol, 0) {
			t.Fatal(symbol)
		}
	}
	for _, symbol := range []string{"", "I", "if2701", "IF2700", "IF2713", "IF2701-C-3000", "T2701", "RB2701"} {
		if p.allows(symbol, 1) {
			t.Fatal("unsupported contract", symbol)
		}
	}
	for _, bad := range []*CFFEXExecutionPolicy{nil, {}, {[]string{"IF"}, 0}, {[]string{"IF", "IF"}, 1}, {[]string{"T"}, 1}, {[]string{"if"}, 1}} {
		if bad.validate() == nil || bad.allows("IF2701", 1) {
			t.Fatal("invalid/default policy")
		}
	}
	copy := cloneCFFEXPolicy(p)
	p.Products[0] = "T"
	if !copy.allows("IF2701", 1) {
		t.Fatal("caller can mutate policy")
	}
	for clock, open := range map[string]bool{"09:05:00": false, "09:29:59": false, "09:30:00": true, "10:15:00": true, "11:29:59": true, "11:30:00": false, "12:59:59": false, "13:00:00": true, "14:59:59": true, "15:00:00": false, "21:00:00": false} {
		now, _ := time.Parse(time.RFC3339, "2026-11-17T"+clock+"+08:00")
		v, _, target, a, row := executionTradeFixture(t, now)
		a.CFFEX = copy
		proof, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now.UTC())
		if (err == nil) != open {
			t.Fatal(clock, err)
		}
		if open && now.Second() == 59 && !proof.ExpiresAt.Equal(now.Add(time.Second)) {
			t.Fatal("crossed session end", proof)
		}
	}
	weekend, _ := time.Parse(time.RFC3339, "2026-11-21T10:00:00+08:00")
	if _, open := cffexIndexSessionEnd(weekend); open {
		t.Fatal("weekend trading")
	}
}

func TestCFFEXExecutionPreparation(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:01:00+08:00")
	v, _, target, approval, _ := executionTradeFixture(t, now)
	catalog, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := meridianPriceFixture(t, now)
	m := &orderMarketStub{catalog: catalog, quote: quote}
	cffexMarket(t, m)
	binding, tick, err := ResolveMeridianPriceCatalog(m.catalog, "IF2701.CFFEX")
	if err != nil {
		t.Fatal(err)
	}
	price, err := PrepareMeridianPrice(m.quote, binding, tick, target.TradeDate, now)
	if err != nil {
		t.Fatal(err)
	}
	approval.Market, approval.CFFEX = &price.Market, &CFFEXExecutionPolicy{[]string{"IF"}, 10}
	for _, side := range []string{"B", "S"} {
		for _, offset := range []string{"O", "C", "CT", "CY"} {
			r := cffexRequest()
			r.Order.TradeSide, r.Order.OffsetType = side, offset
			raw, e := buildServiceOrder(v.scope(), r, binding, now)
			a := approval
			a.CommandSHA256 = commandDigest(raw)
			got, err := ValidateTestTradePreparation(raw, v.scope(), target, e, a)
			if (err == nil) != oneOf(offset, "O", "C") {
				t.Fatal(side, offset, err)
			}
			if err == nil && (got.Orders[0].LimitPrice != r.Order.Price || got.Orders[0].OrderQty != r.Order.Qty || got.Orders[0].OffsetType != offset) {
				t.Fatal("request rewritten", got)
			}
			a.CFFEX = nil
			if _, err := ValidateTestTradePreparation(raw, v.scope(), target, e, a); err == nil {
				t.Fatal("no opt-in")
			}
		}
	}
	raw, _ := json.Marshal(approval)
	if _, err := DecodeTestTradeApproval(raw); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, map[string]any{"products": []string{"IF"}}, map[string]any{"products": []string{"IF"}, "max_order_qty": 0}, map[string]any{"products": []string{"IF"}, "max_order_qty": 1, "ignore_margin": true}} {
		if _, err := DecodeTestTradeApproval(editBody(t, raw, func(m map[string]any) { m["cffex"] = value })); err == nil {
			t.Fatal("invalid operator grant accepted", value)
		}
	}
}

func TestCFFEXOrderServiceOptInOpenAndReplay(t *testing.T) {
	for _, scenario := range []string{"open", "unknown", "default_off", "qty_limit", "unsupported_offset", "wrong_product"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			f := &fakeQueryPublisher{appendError: scenario == "unknown"}
			s, m := cffexService(t, store, f)
			r := cffexRequest()
			a := s.accounts[r.Order.AccountID]
			switch scenario {
			case "default_off":
				a.CFFEX = nil
			case "qty_limit":
				r.Order.Qty = 11
			case "unsupported_offset":
				r.Order.OffsetType = "CT"
			case "wrong_product":
				a.CFFEX.Products = []string{"IM"}
			}
			s.accounts[r.Order.AccountID] = a
			out, err := s.Submit(ctx, r)
			if !oneOf(scenario, "open", "unknown") {
				if err == nil || f.appends.Load() != 0 || storedCount(t, db, "futures_commands") != 0 {
					t.Fatal(out, err)
				}
				return
			}
			want := "published"
			if scenario == "unknown" {
				want = "attempted"
			}
			if (err == nil) != (scenario == "open") || out.Publication.State != want || out.Order.Price != "3000.200" || out.Order.Qty != 3 || out.Order.TradeSide != "S" {
				t.Fatal(out, err)
			}
			m.failed.Store(true)
			a.CFFEX, a.SubmitEnabled = nil, false
			s.accounts[r.Order.AccountID] = a
			again, err := s.Submit(ctx, r)
			if err != nil || !again.Publication.Replayed || again.MessageID != out.MessageID || f.appends.Load() != 1 {
				t.Fatal("replayed order resent or needs new permission", again, err)
			}
		})
	}
}

// A complete synthetic OC response is captured/projected through the normal
// durable path. Neither a client-supplied position ID nor injected capacity is used.
func seedCFFEXServicePosition(t *testing.T, s *TestOrderService, ctx context.Context) *replyFixture {
	t.Helper()
	store, db := s.store, s.store.db
	migrateFuturesTest(t, db, ctx, "000051_futures_close_reservations", "up")
	scope := s.accounts["300052141"].Source.Scope
	target, _ := s.target(ctx, scope)
	original, _, raw := resultQueryFixture(t, "positions")
	command := editBody(t, original.Original.Raw, func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = scope.Route.AccountID })
	c, err := store.Register(ctx, scope, "cmd.query", command, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	seedPublishedReview(t, db, ctx, c, target)
	p := newCloseCapacityFixture(t).reply.Snapshot.Positions[0]
	p.AccountID, p.CapturedAt = scope.Route.AccountID, "2026-11-17T09:29:00+08:00"
	raw = editBody(t, boundReply(t, raw, c), func(m map[string]any) {
		m["routing"], m["produced_at"] = scope.Route, "2026-11-17T09:29:01+08:00"
		v := m["payload"].(map[string]any)
		v["trade_date"], v["items"] = target.TradeDate, []PositionRow{p}
		evidence(m)["counter_session_id"], evidence(m)["connection_generation"] = target.CounterSessionID, target.ConnectionGeneration
	})
	streams, _ := NewStreamStore(db)
	h, err := streams.OpenReplyCapture(ctx, StreamConfig{Scope: scope, Role: "reply", SourceID: target.SourceID}, HeartbeatCheckpoint{Cursor: "0-0"}, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := streams.Open(ctx, StreamConfig{Scope: scope, Role: "event", SourceID: target.SourceID}); err != nil {
		t.Fatal(err)
	}
	projector, _ := NewReplyProjector(streams, scope, target)
	f := &replyFixture{ctx: ctx, db: db, store: streams, h: h, p: projector, c: c, target: target, raw: raw}
	f.append(t, raw)
	if page, err := projector.Step(ctx, h, 32); err != nil || page.Delivered != 1 || page.Invalid != 0 || f.result(t).State != "ready" {
		t.Fatal(page, f.result(t), err)
	}
	seedServiceCapture(t, store, scope, target)
	return f
}

func TestCFFEXOrderServiceCloseAtomicAndLatestQuery(t *testing.T) {
	for _, scenario := range []string{"close", "close_unknown", "capacity", "latest_failed", "latest_unknown", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			f := &fakeQueryPublisher{appendError: scenario == "close_unknown"}
			s, _ := cffexService(t, store, f)
			base := seedCFFEXServicePosition(t, s, ctx)
			if scenario == "missing" {
				if _, err := db.ExecContext(ctx, `DELETE FROM futures_test_query_publications`); err != nil {
					t.Fatal(err)
				}
			}
			if oneOf(scenario, "latest_failed", "latest_unknown") {
				raw := editBody(t, base.c.Original.Raw, func(m map[string]any) {
					m["message_id"], m["request_id"], m["correlation_id"], m["idempotency_key"] = "new-position", "new-request", "new-correlation", "new-key"
				})
				c, err := store.Register(ctx, base.c.Scope, "cmd.query", raw, RegistrationEvidence{})
				if err != nil {
					t.Fatal(err)
				}
				seedPublishedReview(t, db, ctx, c, base.target)
				if scenario == "latest_failed" {
					base.append(t, boundReply(t, failureBody(t, base.raw), c))
					if page, err := base.p.Step(ctx, base.h, 32); err != nil || page.Delivered != 1 {
						t.Fatal(page, err)
					}
				} else if _, err := db.ExecContext(ctx, `UPDATE futures_test_query_publications SET state='attempted',entry_id=NULL,finished_at=NULL WHERE command_id=$1`, c.CommandID); err != nil {
					t.Fatal(err)
				}
			}
			r := cffexRequest()
			r.Order.OffsetType, r.Order.Qty = "C", 8
			if scenario == "capacity" {
				r.Order.Qty = 9
			}
			before := storedCount(t, db, "futures_commands")
			out, err := s.Submit(ctx, r)
			if !oneOf(scenario, "close", "close_unknown") {
				if !errors.Is(err, ErrCloseReservation) || f.appends.Load() != 0 || storedCount(t, db, "futures_commands") != before || storedCount(t, db, "futures_close_reservations") != 0 {
					t.Fatal("failed close left partial state or used old snapshot", out, err)
				}
				return
			}
			want := "published"
			if scenario == "close_unknown" {
				want = "attempted"
			}
			if (err == nil) != (scenario == "close") || out.Publication.State != want || storedCount(t, db, "futures_close_reservations") != 1 {
				t.Fatal(out, err)
			}
			again, err := s.Submit(ctx, r)
			if err != nil || !again.Publication.Replayed || f.appends.Load() != 1 {
				t.Fatal(again, err)
			}
			r.IdempotencyKey, r.Order.Qty = "excess-close", 1
			if _, err := s.Submit(ctx, r); !errors.Is(err, ErrCloseReservation) || f.appends.Load() != 1 {
				t.Fatal("published close did not hold capacity", err)
			}
		})
	}
}

func TestCFFEXOrderServiceConcurrentClose(t *testing.T) {
	db, store, ctx := assessmentDB(t)
	f := &fakeQueryPublisher{}
	s, _ := cffexService(t, store, f)
	seedCFFEXServicePosition(t, s, ctx)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		r := cffexRequest()
		r.IdempotencyKey, r.Order.OffsetType, r.Order.Qty = fmt.Sprint("close-", i), "C", 3
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Submit(ctx, r); err != nil && !errors.Is(err, ErrCloseReservation) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.appends.Load() != 2 || storedCount(t, db, "futures_close_reservations") != 2 || storedCount(t, db, "futures_commands") != 3 {
		t.Fatal("concurrent oversell or lost reservation", f.appends.Load())
	}
}

func serviceRegistration(t *testing.T, db *sql.DB, ctx context.Context, scope CommandScope, id int64) CommandRegistration {
	t.Helper()
	tx, err := beginFuturesTransaction(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(scope.Route)), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCFFEXCancelMultipleFillsAndIndependentPermission(t *testing.T) {
	for _, scenario := range []string{"partial", "duplicate", "conflicting_fill", "fully_filled", "late_fill"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			f := &fakeQueryPublisher{}
			s, m := cffexService(t, store, f)
			r := cffexRequest()
			out, err := s.Submit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			a := s.accounts[r.Order.AccountID]
			target, _ := s.cancelTarget(ctx, a.Source.Scope)
			reg := serviceRegistration(t, db, ctx, a.Source.Scope, out.Publication.CommandID)
			receipt := assessmentOrderReceipt(t, db, ctx, reg, target, func(m map[string]any) {
				p := m["payload"].(map[string]any)
				p["order_qty"], p["leaves_qty"], p["exchange"] = 3, 3, "CFFEX"
			})
			order := closeCapacityOrder(reg, "working", 1)
			order.CounterSessionID = target.CounterSessionID
			order.NativeIDs.OrderSysID = "00000123"
			appendFill := func(entry, tradeID string, qty Count) {
				fill := closeCapacityFill(TradeScope{a.Source.Scope.Route, a.Source.Scope.NativeBrokerID, a.Source.Scope.InvestorID, target.TradeDate}, order, tradeID, qty)
				appendCancelOrderObservation(t, db, ctx, receipt, entry, func(m map[string]any) {
					b, _ := json.Marshal(fill)
					var p map[string]any
					_ = json.Unmarshal(b, &p)
					p["product_type"], p["schema_version"] = "futures", "ctp.f0.draft.2"
					m["event_type"], m["message_id"], m["payload"] = "fill.event", "fill-"+entry, p
				})
			}
			appendFill("2-0", "f1", 1)
			appendCancelOrderObservation(t, db, ctx, receipt, "3-0", func(m map[string]any) {
				p := m["payload"].(map[string]any)
				p["cum_filled_qty"], p["leaves_qty"] = 1, 2
			})
			switch scenario {
			case "duplicate":
				appendFill("4-0", "f1", 1)
			case "conflicting_fill":
				appendFill("4-0", "f1", 2)
			case "fully_filled":
				appendFill("4-0", "f2", 2)
			case "late_fill":
				appendFill("4-0", "f2", 1)
			}
			a.SubmitEnabled, a.CancelEnabled, a.CFFEX = false, true, nil
			s.accounts[r.Order.AccountID] = a
			m.failed.Store(true)
			seedServiceCapture(t, store, a.Source.Scope, target)
			result, err := s.Cancel(ctx, CancelExecutionRequest{"cancel-cffex", r.Order.AccountID, out.GatewayOrderID, r.Order.TradeDate})
			if oneOf(scenario, "partial", "duplicate") {
				if err != nil || result.Publication.State != "published" || f.appends.Load() != 2 {
					t.Fatal(result, err)
				}
			} else if err == nil || f.appends.Load() != 1 || storedCount(t, db, "futures_commands") != 1 {
				t.Fatal("unaccounted fill allowed cancel", result, err)
			}
		})
	}
}
