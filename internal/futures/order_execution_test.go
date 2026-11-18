package futures

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func orderServiceRequest() OrderExecutionRequest {
	return OrderExecutionRequest{"test-request-1", OrderPreviewRequest{"300052141", "RB2701.SHFE", "20261117", "B", "O", "speculation", 1, "3000.000"}}
}

func TestOrderExecutionRequestBoundary(t *testing.T) {
	raw, _ := json.Marshal(orderServiceRequest())
	if _, err := DecodeOrderExecutionRequest(raw); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"source_override": func(m map[string]any) { m["source"] = "counter" },
		"permission":      func(m map[string]any) { m["submit_enabled"] = true },
		"session":         func(m map[string]any) { m["counter_session_id"] = "arbitrary" },
		"raw_quote":       func(m map[string]any) { m["market"] = map[string]any{} },
		"empty_key":       func(m map[string]any) { m["idempotency_key"] = "" },
		"null_key":        func(m map[string]any) { m["idempotency_key"] = nil },
		"long_key":        func(m map[string]any) { m["idempotency_key"] = strings.Repeat("x", 129) },
		"control_key":     func(m map[string]any) { m["idempotency_key"] = "key\n" },
		"float_price":     func(m map[string]any) { m["order"].(map[string]any)["price"] = 3000.1 },
		"bad_symbol":      func(m map[string]any) { m["order"].(map[string]any)["security_id"] = "rb2701" },
		"order_id":        func(m map[string]any) { m["order"].(map[string]any)["gateway_order_id"] = "supplied" },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeOrderExecutionRequest(editBody(t, raw, mutate)); err == nil {
				t.Fatal("accepted client-controlled execution evidence")
			}
		})
	}
	for _, malformed := range [][]byte{append(raw, raw...), []byte(`{"idempotency_key":"x","idempotency_key":"y","order":{}}`), []byte(strings.Repeat(" ", 4097))} {
		if _, err := DecodeOrderExecutionRequest(malformed); err == nil {
			t.Fatal("accepted malformed request")
		}
	}
}

func TestOrderExecutionBuildPreservesPriceAndBusinessTime(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T01:30:00Z")
	v, _, _, _, _ := executionTradeFixture(t, now)
	r := orderServiceRequest()
	b := CatalogBinding{"RB2701.SHFE", "rb2701", "SHFE", "catalog", "checksum"}
	raw, e := buildServiceOrder(v.scope(), r, b, now)
	c, err := DecodeCommand(raw, v.scope(), "cmd.trade")
	if err != nil || checkRegistrationEvidence(c, v.scope(), e) != "" || c.Orders[0].LimitPrice != "3000.000" || !strings.Contains(string(raw), `"sent_at":"2026-11-17T09:30:00+08:00"`) {
		t.Fatal(string(raw), err)
	}
	other, _ := buildServiceOrder(v.scope(), r, b, now.Add(time.Second))
	c2, err := DecodeCommand(other, v.scope(), "cmd.trade")
	if err != nil || c.MessageID != c2.MessageID || c.Orders[0].GatewayOrderID != c2.Orders[0].GatewayOrderID || string(c.Raw) == string(c2.Raw) {
		t.Fatal("identity must be stable; newly prepared bytes must not replace a registered command", err)
	}
}

type orderMarketStub struct {
	catalog, quote []byte
	calls          atomic.Int64
	failed         atomic.Bool
}

func (m *orderMarketStub) FuturesPriceCatalog(context.Context, string) ([]byte, error) {
	m.calls.Add(1)
	if m.failed.Load() {
		return nil, errors.New("market unavailable")
	}
	return m.catalog, nil
}
func (m *orderMarketStub) FuturesPriceSnapshot(context.Context, string) ([]byte, error) {
	m.calls.Add(1)
	if m.failed.Load() {
		return nil, errors.New("market unavailable")
	}
	return m.quote, nil
}

func serviceFixture(t *testing.T, store *CommandStore, transport *fakeQueryPublisher) (*TestOrderService, *orderMarketStub, *atomic.Int64) {
	t.Helper()
	v, _, target, _, _ := executionTradeFixture(t, time.Now())
	catalog, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := meridianPriceFixture(t, time.Now())
	m := &orderMarketStub{catalog: catalog, quote: quote}
	calls := &atomic.Int64{}
	s, err := NewTestOrderService(store, []TestOrderAccount{{Source: TestSourceConfig{Scope: v.scope()}, SubmitEnabled: true, AuthorizationExpiresAt: time.Now().Add(time.Minute)}}, m, func(context.Context, CommandScope) (TestQueryTarget, error) {
		calls.Add(1)
		return target, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.transport = func(TestSourceConfig, TestQueryTarget, TestTradeApproval) (queryPublicationTransport, func(), error) {
		return transport, func() {}, nil
	}
	return s, m, calls
}

func TestOrderServiceDurableReplayAndUnknown(t *testing.T) {
	for _, scenario := range []string{"published", "unknown", "gate_closed", "atomic_gate"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			transport := &fakeQueryPublisher{appendError: scenario == "unknown", gateError: scenario == "gate_closed", rejected: scenario == "atomic_gate"}
			s, market, targets := serviceFixture(t, store, transport)
			r := orderServiceRequest()
			first, err := s.Submit(ctx, r)
			if scenario == "published" && err != nil {
				t.Fatal(err)
			}
			if scenario != "published" && err == nil {
				t.Fatal("missing failed publication result")
			}
			want := map[string]string{"published": "published", "unknown": "attempted", "gate_closed": "registered", "atomic_gate": "not_published"}[scenario]
			if first.Publication.State != want || first.BrokerStateKnown || first.RetrySafe || first.Publication.BrokerAccepted || first.Order.Price != "3000.000" {
				t.Fatal(first)
			}
			market.failed.Store(true)
			beforeMarket, beforeTargets := market.calls.Load(), targets.Load()
			// Restart the service/store with permissions revoked and a failed
			// market/session provider. Replay must still be read-only.
			fresh, _ := NewCommandStore(db)
			s.store = fresh
			a := s.accounts[r.Order.AccountID]
			a.SubmitEnabled = false
			a.AuthorizationExpiresAt = time.Now().Add(-time.Hour)
			s.accounts[r.Order.AccountID] = a
			s.target = func(context.Context, CommandScope) (TestQueryTarget, error) {
				t.Fatal("replay fetched a session")
				return TestQueryTarget{}, ErrQueryPublishGate
			}
			r.Order.Price = "3000"
			again, err := s.Submit(ctx, r)
			if err != nil || !again.Publication.Replayed || again.Publication.State != want || again.MessageID != first.MessageID || again.Order.Price != first.Order.Price {
				t.Fatal(again, err)
			}
			status, err := s.Lookup(ctx, r.Order.AccountID, r.IdempotencyKey)
			if err != nil || status.Publication.CommandID != first.Publication.CommandID || status.Publication.State != want {
				t.Fatal(status, err)
			}
			if market.calls.Load() != beforeMarket || targets.Load() != beforeTargets || transport.inspections.Load() != 1 {
				t.Fatal("replay required live evidence")
			}
			wantAppends := int32(1)
			if scenario == "gate_closed" {
				wantAppends = 0
			}
			if transport.appends.Load() != wantAppends || storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_audit") != 1 || storedCount(t, db, "futures_command_messages") != 1 {
				t.Fatal("retry mutated command history or repeated publication")
			}
			for _, change := range []func(*OrderExecutionRequest){
				func(r *OrderExecutionRequest) { r.Order.Price = "3001" },
				func(r *OrderExecutionRequest) { r.Order.Qty = 2 },
				func(r *OrderExecutionRequest) { r.Order.TradeDate = "20261118" },
				func(r *OrderExecutionRequest) { r.Order.TradeSide = "S" },
				func(r *OrderExecutionRequest) { r.Order.SecurityID = "AU2701.SHFE" },
			} {
				changed := r
				change(&changed)
				if _, err := s.Submit(ctx, changed); !errors.Is(err, ErrOrderRequestConflict) {
					t.Fatal("changed intention reused key", err)
				}
			}
			if _, err := s.Lookup(ctx, "314000045768", r.IdempotencyKey); !errors.Is(err, ErrOrderRequestMissing) {
				t.Fatal("cross-account lookup", err)
			}
			var raw []byte
			if err := db.QueryRowContext(ctx, `SELECT body FROM futures_commands WHERE command_id=$1`, first.Publication.CommandID).Scan(&raw); err != nil || !strings.Contains(string(raw), `"limit_price":"3000.000"`) || !strings.Contains(string(raw), `"symbol":"rb2701"`) {
				t.Fatal("lost original price/native identity", err)
			}
		})
	}
}

func TestOrderServiceConcurrentRegisterOnce(t *testing.T) {
	db, store, ctx := assessmentDB(t)
	f := &fakeQueryPublisher{}
	s, _, _ := serviceFixture(t, store, f)
	var wg sync.WaitGroup
	ids := make(chan int64, 12)
	for i := 0; i < cap(ids); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Submit(ctx, orderServiceRequest())
			if err != nil {
				t.Error(err)
			}
			ids <- r.Publication.CommandID
		}()
	}
	wg.Wait()
	close(ids)
	var first int64
	for id := range ids {
		if id <= 0 || (first != 0 && id != first) {
			t.Fatal("different durable identities", id, first)
		}
		first = id
	}
	if f.appends.Load() != 1 || f.inspections.Load() != 1 || storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_command_audit") != 1 {
		t.Fatal("concurrent request duplicated responsibility")
	}
}

func TestOrderServiceRegisteredOnlyNeverResumes(t *testing.T) {
	db, store, ctx := assessmentDB(t)
	f := &fakeQueryPublisher{}
	s, m, calls := serviceFixture(t, store, f)
	r := orderServiceRequest()
	a := s.accounts[r.Order.AccountID]
	b, _, err := ResolveMeridianPriceCatalog(m.catalog, r.Order.SecurityID)
	if err != nil {
		t.Fatal(err)
	}
	raw, evidence := buildServiceOrder(a.Source.Scope, r, b, time.Now().Add(-time.Hour))
	reg, err := store.Register(ctx, a.Source.Scope, "cmd.trade", raw, evidence)
	if err != nil {
		t.Fatal(err)
	}
	// Simulates exit immediately after registration, before publication.
	result, err := s.Submit(ctx, r)
	if err != nil || result.Publication.State != "registered" || result.Publication.CommandID != reg.CommandID || result.Publication.Published != nil {
		t.Fatal(result, err)
	}
	if f.inspections.Load() != 0 || f.appends.Load() != 0 || calls.Load() != 0 || m.calls.Load() != 0 || storedCount(t, db, "futures_test_trade_publications") != 0 {
		t.Fatal("registration treated as restart permission")
	}
}

func TestOrderServiceRejectsBeforeRegistration(t *testing.T) {
	for _, scenario := range []string{"permission", "expired", "off_tick", "oversize", "wrong_day", "stale", "wrong_account", "market_failed"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			f := &fakeQueryPublisher{}
			s, m, _ := serviceFixture(t, store, f)
			r := orderServiceRequest()
			a := s.accounts[r.Order.AccountID]
			switch scenario {
			case "permission":
				a.SubmitEnabled = false
			case "expired":
				a.AuthorizationExpiresAt = time.Now().Add(-time.Second)
			case "off_tick":
				r.Order.Price = "3000.1"
			case "oversize":
				r.Order.Qty = 2
			case "wrong_day":
				r.Order.TradeDate = "20261118"
			case "stale":
				m.quote, _ = meridianPriceFixture(t, time.Now().Add(-time.Minute))
			case "wrong_account":
				r.Order.AccountID = "314000045768"
			case "market_failed":
				m.failed.Store(true)
			}
			s.accounts[a.Source.Scope.Route.AccountID] = a
			if _, err := s.Submit(ctx, r); err == nil {
				t.Fatal("invalid order admitted")
			}
			if f.appends.Load() != 0 || storedCount(t, db, "futures_commands") != 0 {
				t.Fatal("invalid order registered/published")
			}
		})
	}
}
