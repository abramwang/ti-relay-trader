package futures

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"
)

// Synthetic quotes, heartbeat and transport only; no Feitai acceptance evidence.
type configuredExecutionFixture struct {
	account   TestOrderAccount
	request   OrderExecutionRequest
	target    TestQueryTarget
	approval  TestTradeApproval
	heartbeat StreamInput
	market    *orderMarketStub
}

func configuredDCEFixture(t *testing.T, now time.Time) configuredExecutionFixture {
	t.Helper()
	_, _, target, approval, hb := executionTradeFixture(t, now)
	scope := CommandScope{Route: QueryRoute{"test", "ctp_1022", "600102001", "600102001"}, NativeBrokerID: "1022", UserID: "600102001", InvestorID: "600102001"}
	policy := marketPolicyFixture("DCE")
	a := TestOrderAccount{Source: TestSourceConfig{Scope: scope}, SubmitEnabled: true, CancelEnabled: true,
		AuthorizationExpiresAt: now.Add(time.Minute), Markets: []MarketExecutionPolicy{policy}}
	catalog, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := meridianPriceFixture(t, now)
	m := &orderMarketStub{catalog: catalog, quote: quote}
	marketFixture(t, m, "DCE", "m2701")
	r := orderServiceRequest()
	r.Order.AccountID, r.Order.SecurityID, r.Order.Qty = scope.Route.AccountID, "M2701.DCE", 5
	binding, tick, err := ResolveMeridianPriceCatalog(m.catalog, r.Order.SecurityID)
	if err != nil {
		t.Fatal(err)
	}
	price, err := PrepareMeridianPrice(m.quote, binding, tick, r.Order.TradeDate, now)
	if err != nil {
		t.Fatal(err)
	}
	approval.Market, approval.MarketPolicy = &price.Market, &policy
	target.CounterSessionID, target.Group = "synthetic-feitai-session", "synthetic-feitai-group"
	hb.Raw = editBody(t, hb.Raw, func(m map[string]any) {
		m["routing"], m["counter_session_id"] = scope.Route, target.CounterSessionID
		m["payload"].(map[string]any)["counter_session_id"] = target.CounterSessionID
	})
	return configuredExecutionFixture{a, r, target, approval, hb, m}
}

func TestConfiguredTestExecutionScopes(t *testing.T) {
	f := configuredDCEFixture(t, time.Now())
	store := &CommandStore{db: &sql.DB{}}
	target := func(context.Context, CommandScope) (TestQueryTarget, error) { return f.target, nil }
	for name, change := range map[string]func(*TestOrderAccount){
		"explicit_feitai": func(*TestOrderAccount) {},
		"another_configured_identity": func(a *TestOrderAccount) {
			a.Source.Scope = CommandScope{Route: QueryRoute{"test", "ctp_synthetic", "synthetic-gateway", "990000000111"}, NativeBrokerID: "7777", UserID: "operator1", InvestorID: "investor1"}
		},
		"production":             func(a *TestOrderAccount) { a.Source.Scope.Route.Env = "prod" },
		"securities":             func(a *TestOrderAccount) { a.Source.Scope.Route.BrokerID = "huaxin" },
		"missing_user":           func(a *TestOrderAccount) { a.Source.Scope.UserID = "" },
		"missing_investor":       func(a *TestOrderAccount) { a.Source.Scope.InvestorID = "" },
		"missing_native_broker":  func(a *TestOrderAccount) { a.Source.Scope.NativeBrokerID = "" },
		"missing_gateway":        func(a *TestOrderAccount) { a.Source.Scope.Route.GatewayID = "" },
		"implicit_legacy_market": func(a *TestOrderAccount) { a.Markets = nil },
		"empty_markets":          func(a *TestOrderAccount) { a.Markets = []MarketExecutionPolicy{} },
	} {
		t.Run(name, func(t *testing.T) {
			a := f.account
			change(&a)
			s, err := NewTestOrderService(store, []TestOrderAccount{a}, f.market, target)
			if oneOf(name, "explicit_feitai", "another_configured_identity") {
				if err != nil || s.accounts[a.Source.Scope.Route.AccountID].Source.Scope != a.Source.Scope {
					t.Fatal("explicit native identity changed", err)
				}
			} else if !errors.Is(err, ErrOrderExecutionOff) {
				t.Fatal("invalid account accepted", err)
			}
		})
	}
	if _, err := NewTestOrderService(store, []TestOrderAccount{f.account, f.account}, f.market, target); !errors.Is(err, ErrOrderExecutionOff) {
		t.Fatal("duplicate account binding accepted", err)
	}
}

func TestConfiguredTestExecutionPreparationAndHeartbeat(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	f := configuredDCEFixture(t, now)
	scope := f.account.Source.Scope
	binding, _, err := ResolveMeridianPriceCatalog(f.market.catalog, f.request.Order.SecurityID)
	if err != nil {
		t.Fatal(err)
	}
	for _, qty := range []Count{2, 5, 3, 10, 1} {
		r := f.request
		r.Order.Qty = qty
		raw, evidence := buildServiceOrder(scope, r, binding, now)
		a := f.approval
		a.CommandSHA256 = commandDigest(raw)
		c, err := ValidateTestTradePreparation(raw, scope, f.target, evidence, a)
		if err != nil || c.Orders[0].OrderQty != qty || c.Orders[0].LimitPrice != r.Order.Price || c.Orders[0].Symbol != "m2701" {
			t.Fatal("configured order changed", qty, c, err)
		}
		if _, err := testTradeHeartbeatProof(f.heartbeat, scope.Route, f.target, a, now); err != nil {
			t.Fatal("configured execution heartbeat refused", err)
		}
		for _, badScope := range []CommandScope{
			{Route: QueryRoute{"prod", "ctp_1022", "600102001", "600102001"}, NativeBrokerID: "1022", UserID: "600102001", InvestorID: "600102001"},
			{Route: QueryRoute{"test", "huaxin", "600102001", "600102001"}, NativeBrokerID: "1022", UserID: "600102001", InvestorID: "600102001"},
			{Route: QueryRoute{"test", "ctp_1022", "600102001", "600102002"}, NativeBrokerID: "1022", UserID: "600102001", InvestorID: "600102001"},
		} {
			if _, err := ValidateTestTradePreparation(raw, badScope, f.target, evidence, a); err == nil {
				t.Fatal("wrong execution scope accepted", badScope)
			}
		}
		a.MarketPolicy = nil
		if _, err := ValidateTestTradePreparation(raw, scope, f.target, evidence, a); err == nil {
			t.Fatal("legacy assessment policy leaked to new account")
		}
	}
	for _, change := range []func(*QueryRoute, *TestQueryTarget, *TestTradeApproval){
		func(r *QueryRoute, _ *TestQueryTarget, _ *TestTradeApproval) { r.GatewayID = "other" },
		func(_ *QueryRoute, t *TestQueryTarget, _ *TestTradeApproval) { t.CounterSessionID = "old-session" },
		func(_ *QueryRoute, t *TestQueryTarget, _ *TestTradeApproval) { t.ConnectionGeneration++ },
		func(_ *QueryRoute, t *TestQueryTarget, _ *TestTradeApproval) { t.TradeDate = "20261118" },
		func(_ *QueryRoute, _ *TestQueryTarget, a *TestTradeApproval) {
			a.ExecutionPermission = &TestExecutionPermission{}
		},
		func(_ *QueryRoute, _ *TestQueryTarget, a *TestTradeApproval) { a.MarketPolicy = nil },
	} {
		route, target, a := scope.Route, f.target, f.approval
		change(&route, &target, &a)
		if _, err := testTradeHeartbeatProof(f.heartbeat, route, target, a, now); err == nil {
			t.Fatal("wrong scope/session or implicit permission accepted")
		}
	}
}

func TestConfiguredTestExecutionServiceReplayAndCancel(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "published_cancel", true: "unknown"}[unknown], func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			f := configuredDCEFixture(t, time.Now())
			scope := f.account.Source.Scope
			transport := &fakeQueryPublisher{appendError: unknown}
			s, err := NewTestOrderService(store, []TestOrderAccount{f.account}, f.market, func(_ context.Context, got CommandScope) (TestQueryTarget, error) {
				if got != scope {
					t.Error("target scope changed", got)
				}
				return f.target, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			s.transport = func(source TestSourceConfig, target TestQueryTarget, a TestTradeApproval) (queryPublicationTransport, func(), error) {
				if source.Scope != scope || target != f.target || a.MarketPolicy == nil || a.MarketPolicy.Exchange != "DCE" {
					t.Error("publication binding changed")
				}
				return transport, func() {}, nil
			}
			seedServiceCapture(t, store, scope, f.target)
			wrong := f.request
			wrong.Order.AccountID = "600102002"
			if _, err := s.Submit(ctx, wrong); !errors.Is(err, ErrOrderExecutionOff) || f.market.calls.Load() != 0 {
				t.Fatal("unconfigured account queried market or published", err)
			}
			out, err := s.Submit(ctx, f.request)
			if (err == nil) == unknown || transport.appends.Load() != 1 {
				t.Fatal(out, err)
			}
			if unknown && (!errors.Is(err, ErrQueryPublishUnknown) || out.Publication.State != "attempted") {
				t.Fatal("unknown execution lost", out, err)
			}
			reg := serviceRegistration(t, db, ctx, scope, out.Publication.CommandID)
			if reg.Scope != scope || reg.Original.Orders[0].OrderQty != 5 || reg.Original.Orders[0].LimitPrice != f.request.Order.Price {
				t.Fatal("persisted intention changed", reg)
			}
			f.market.failed.Store(true)
			account := f.account
			account.SubmitEnabled = false
			s.accounts[scope.Route.AccountID] = account
			again, err := s.Submit(ctx, f.request)
			if err != nil || !again.Publication.Replayed || again.MessageID != out.MessageID || transport.appends.Load() != 1 {
				t.Fatal("replayed command sent again", again, err)
			}
			if unknown {
				return
			}
			assessmentOrderReceipt(t, db, ctx, reg, f.target, func(m map[string]any) {
				p := m["payload"].(map[string]any)
				p["exchange"], p["order_qty"], p["cum_filled_qty"], p["leaves_qty"] = "DCE", 5, 2, 3
			})
			seedServiceCapture(t, store, scope, f.target)
			r := CancelExecutionRequest{"synthetic-feitai-cancel", scope.Route.AccountID, out.GatewayOrderID, f.request.Order.TradeDate}
			cancelled, err := s.Cancel(ctx, r)
			if err != nil || cancelled.Publication.State != "published" || transport.appends.Load() != 2 {
				t.Fatal("partial-order cancel needs new quote or submit permission", cancelled, err)
			}
			if replay, err := s.Cancel(ctx, r); err != nil || !replay.Publication.Replayed || transport.appends.Load() != 2 {
				t.Fatal("cancel republished", replay, err)
			}
		})
	}
}
