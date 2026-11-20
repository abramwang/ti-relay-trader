package futures

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Synthetic policies/data only; these sessions and quantities are not venue rules.
func marketPolicyFixture(exchange string) MarketExecutionPolicy {
	return MarketExecutionPolicy{Exchange: exchange, MaxOrderQty: 10, CloseModel: "combined", DaySessions: []DaySession{{"09:30", "11:30"}, {"13:30", "15:00"}}}
}

func marketFixture(t *testing.T, m *orderMarketStub, exchange, symbol string) {
	t.Helper()
	security := strings.ToUpper(symbol) + "." + exchange
	m.catalog = editBody(t, m.catalog, func(v map[string]any) {
		r := v["data"].([]any)[0].(map[string]any)
		r["security_id"], r["source_instrument_id"], r["exchange_id"] = security, symbol, exchange
		r["ctp_product_id"] = nil
		v["meta"].(map[string]any)["exchange_coverage"] = map[string]any{exchange: map[string]any{"status": "production_ready"}}
	})
	m.quote = editBody(t, m.quote, func(v map[string]any) {
		r := v["data"].([]any)[0].(map[string]any)
		r["security_id"], r["source_instrument_id"], r["exchange_id"] = security, symbol, exchange
	})
}

func TestMarketPolicyExplicitAndDayOnly(t *testing.T) {
	p := marketPolicyFixture("DCE")
	if !p.allows("DCE", "O", "S", "speculation", 2) || !p.allows("DCE", "C", "B", "speculation", 10) {
		t.Fatal("valid explicit intent refused")
	}
	for _, offset := range []string{"CT", "CY", "unknown"} {
		if p.allows("DCE", offset, "S", "speculation", 1) {
			t.Fatal(offset)
		}
	}
	if p.allows("SHFE", "O", "B", "speculation", 1) || p.allows("DCE", "O", "B", "speculation", 11) {
		t.Fatal("policy bypass")
	}
	for clock, open := range map[string]bool{"09:29:59": false, "09:30:00": true, "11:29:59": true, "11:30:00": false, "13:00:00": false, "13:30:00": true, "15:00:00": false, "21:00:00": false} {
		now, _ := time.Parse(time.RFC3339, "2026-11-19T"+clock+"+08:00")
		if _, ok := p.sessionEnd(now); ok != open {
			t.Fatal(clock, ok)
		}
	}
	for _, change := range []func(*MarketExecutionPolicy){
		func(p *MarketExecutionPolicy) { p.DaySessions = nil },
		func(p *MarketExecutionPolicy) { p.DaySessions = []DaySession{{"21:00", "23:00"}} },
		func(p *MarketExecutionPolicy) { p.DaySessions = []DaySession{{"09:00", "11:30"}, {"11:00", "15:00"}} },
		func(p *MarketExecutionPolicy) { p.Exchange = "DCE " },
		func(p *MarketExecutionPolicy) { p.CloseModel = "guess" },
		func(p *MarketExecutionPolicy) { p.MaxOrderQty = 0 },
	} {
		bad := *cloneMarketPolicy(&p)
		change(&bad)
		if bad.validate() == nil {
			t.Fatal("invalid policy", bad)
		}
	}
	a := TestOrderAccount{Markets: []MarketExecutionPolicy{p}}
	copy := a.marketPolicy("DCE")
	copy.DaySessions[0].Start = "00:00"
	if a.Markets[0].DaySessions[0].Start != "09:30" || a.marketPolicy("SHFE") != nil {
		t.Fatal("mutable or implicit policy")
	}
	copy.CloseModel = "none"
	if copy.allows("DCE", "C", "S", "speculation", 1) {
		t.Fatal("unsupported close")
	}
}

func TestMarketCatalogPriceCommandsAndFillIdentity(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:31:00+08:00")
	ids := map[string]bool{}
	for exchange, symbol := range map[string]string{"SHFE": "rb2701", "CFFEX": "IF2701", "DCE": "l2612F", "CZCE": "TA701", "INE": "sc2701", "GFEX": "si2701", "TESTEX": "x2701"} {
		t.Run(exchange, func(t *testing.T) {
			catalog, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
			if err != nil {
				t.Fatal(err)
			}
			quote, _ := meridianPriceFixture(t, now)
			m := &orderMarketStub{catalog: catalog, quote: quote}
			marketFixture(t, m, exchange, symbol)
			security := strings.ToUpper(symbol) + "." + exchange
			binding, tick, err := ResolveMeridianPriceCatalog(m.catalog, security)
			if err != nil {
				t.Fatal(err)
			}
			price, err := PrepareMeridianPrice(m.quote, binding, tick, "20261117", now)
			if err != nil {
				t.Fatal(err)
			}
			v, _, target, a, _ := executionTradeFixture(t, now)
			policy := marketPolicyFixture(exchange)
			a.MarketPolicy = &policy
			a.Market = &price.Market
			r := orderServiceRequest()
			r.Order.SecurityID = security
			r.Order.TradeSide = "S"
			r.Order.Qty = 3
			raw, e := buildServiceOrder(v.scope(), r, binding, now)
			a.CommandSHA256 = commandDigest(raw)
			c, err := ValidateTestTradePreparation(raw, v.scope(), target, e, a)
			if err != nil || c.Orders[0].Symbol != symbol || c.Orders[0].Exchange != exchange {
				t.Fatal(c, err)
			}
			encoded, _ := json.Marshal(a)
			if _, err := DecodeTestTradeApproval(encoded); err != nil {
				t.Fatal(err)
			}
			a.MarketPolicy = nil
			if _, err := ValidateTestTradePreparation(raw, v.scope(), target, e, a); err == nil {
				t.Fatal("implicit market authorization")
			}
			bad := editBody(t, m.catalog, func(v map[string]any) {
				v["meta"].(map[string]any)["exchange_coverage"] = map[string]any{exchange: map[string]any{"status": "pending"}}
			})
			if _, _, err := ResolveMeridianPriceCatalog(bad, security); err == nil {
				t.Fatal("unpublished coverage")
			}
			bad = editBody(t, m.catalog, func(v map[string]any) { v["data"].([]any)[0].(map[string]any)["source_instrument_id"] = "other2701" })
			if _, _, err := ResolveMeridianPriceCatalog(bad, security); err == nil {
				t.Fatal("invented native mapping")
			}
			fill := asV2Fill(t, editBody(t, tradeFixture(t, "fill"), func(v map[string]any) { v["exchange"] = exchange; v["symbol"] = symbol }))
			f, err := DecodeFill(fill, tradeScope())
			if err != nil || ids[f.FillID] {
				t.Fatal(f, err)
			}
			ids[f.FillID] = true
			if !(ConstraintsTarget{Exchange: exchange, Symbol: symbol}).valid() {
				t.Fatal("constraints target rejected")
			}
		})
	}
}

func TestMarketOrderServicePublicationAndCloseReservation(t *testing.T) {
	for _, scenario := range []string{"open", "close", "unknown", "unconfigured", "overclose", "split_offset", "close_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			f := &fakeQueryPublisher{appendError: scenario == "unknown"}
			s, m, _ := serviceFixture(t, store, f)
			marketFixture(t, m, "DCE", "l2612F")
			a := s.accounts["300052141"]
			a.Markets = []MarketExecutionPolicy{marketPolicyFixture("DCE")}
			if scenario == "unconfigured" {
				a.Markets[0].Exchange = "CZCE"
			}
			if scenario == "close_disabled" {
				a.Markets[0].CloseModel = "none"
			}
			s.accounts["300052141"] = a
			seedMarketServicePosition(t, s, ctx, "DCE", "l2612F")
			r := orderServiceRequest()
			r.Order.SecurityID = "L2612F.DCE"
			r.Order.TradeSide = "S"
			r.Order.Qty = 8
			r.Order.OffsetType = "C"
			if scenario == "open" {
				r.Order.OffsetType = "O"
			}
			if scenario == "overclose" {
				r.Order.Qty = 9
			}
			if scenario == "split_offset" {
				r.Order.OffsetType = "CT"
			}
			before := storedCount(t, db, "futures_commands")
			out, err := s.Submit(ctx, r)
			if !oneOf(scenario, "open", "close", "unknown") {
				if err == nil || f.appends.Load() != 0 || storedCount(t, db, "futures_commands") != before {
					t.Fatal(out, err)
				}
				return
			}
			if (err == nil) != (scenario != "unknown") || f.appends.Load() != 1 {
				t.Fatal(out, err)
			}
			if scenario != "open" && storedCount(t, db, "futures_close_reservations") != 1 {
				t.Fatal("close not reserved")
			}
			again, err := s.Submit(ctx, r)
			if err != nil || !again.Publication.Replayed || f.appends.Load() != 1 {
				t.Fatal("repeated publication", again, err)
			}
			if scenario != "open" {
				r.IdempotencyKey = "excess-close"
				r.Order.Qty = 1
				if _, err := s.Submit(ctx, r); !errors.Is(err, ErrCloseReservation) || f.appends.Load() != 1 {
					t.Fatal("reserved capacity lost", err)
				}
			}
		})
	}
}

func TestMarketCombinedCloseSeparatesVenueAndDateModel(t *testing.T) {
	f := newCloseCapacityFixture(t)
	f.reply.Snapshot.Positions[0].Exchange = "DCE"
	other := f.reply.Snapshot.Positions[0]
	other.Exchange = "CZCE"
	other.Futures.Position = 100
	f.reply.Snapshot.Positions = append(f.reply.Snapshot.Positions, other)
	for exchange, want := range map[string]int64{"DCE": 8, "CZCE": 98, "SHFE": 0} {
		got, err := calculateCombinedCloseCapacity(f.scope, f.target, f.reply, exchange, "IF2701", "S", nil, f.report, nil)
		if err != nil || got.Available != want {
			t.Fatal(exchange, got, err)
		}
	}
	f.reply.Snapshot.Positions[0].Futures.PositionDate = "2"
	if _, err := calculateCombinedCloseCapacity(f.scope, f.target, f.reply, "DCE", "IF2701", "S", nil, f.report, nil); err == nil {
		t.Fatal("split-date treated as combined")
	}
}

func TestMarketCancelIndependentOfNewOrderPolicy(t *testing.T) {
	db, store, ctx := assessmentDB(t)
	f := &fakeQueryPublisher{}
	s, m, _ := serviceFixture(t, store, f)
	marketFixture(t, m, "DCE", "l2612F")
	a := s.accounts["300052141"]
	a.Markets = []MarketExecutionPolicy{marketPolicyFixture("DCE")}
	s.accounts["300052141"] = a
	r := orderServiceRequest()
	r.Order.SecurityID, r.Order.Qty = "L2612F.DCE", 3
	out, err := s.Submit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := s.cancelTarget(ctx, a.Source.Scope)
	reg := serviceRegistration(t, db, ctx, a.Source.Scope, out.Publication.CommandID)
	assessmentOrderReceipt(t, db, ctx, reg, target, func(m map[string]any) {
		p := m["payload"].(map[string]any)
		p["order_qty"], p["leaves_qty"], p["exchange"] = 3, 3, "DCE"
	})
	a.SubmitEnabled, a.CancelEnabled = false, true
	a.Markets[0].MaxOrderQty, a.Markets[0].CloseModel = 1, "none"
	s.accounts["300052141"] = a
	m.failed.Store(true)
	seedServiceCapture(t, store, a.Source.Scope, target)
	result, err := s.Cancel(ctx, CancelExecutionRequest{"cancel-dce", r.Order.AccountID, out.GatewayOrderID, r.Order.TradeDate})
	if err != nil || result.Publication.State != "published" || f.appends.Load() != 2 {
		t.Fatal(result, err)
	}
}

func TestMarketTerminalPermissionNeverAggregatesVenues(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-19T13:15:00+08:00")
	b, g, day := true, int64(1), "20261119"
	scope := CommandScope{Route: QueryRoute{"test", "ctp_4900", "synthetic", "300052141"}}
	p := TestOrderAccount{Source: TestSourceConfig{Scope: scope}, SubmitEnabled: true, CancelEnabled: true, AuthorizationExpiresAt: now.Add(time.Hour), Markets: []MarketExecutionPolicy{marketPolicyFixture("DCE"), marketPolicyFixture("CFFEX")}}
	p.Markets[1].DaySessions[1].Start = "13:00"
	h := &AdvertisedHeartbeat{ProducedAt: now, SessionID: "test", TradeDate: &day, ConnectionGeneration: &g, AuthorizationOwner: "relay", RedisReady: true, BrokerReady: true, SettlementConfirmed: true, RecoveryClear: &b, AcceptingTradeCommands: true, AcceptingCancelCommands: true}
	a := TerminalAccount{AccountID: scope.Route.AccountID, Route: scope.Route, Heartbeat: h, ConnectionState: "execution", Capture: &CaptureHealthView{Ready: true, ValidUntil: now.Add(time.Second)}}
	s := &TestOrderService{accounts: map[string]TestOrderAccount{a.AccountID: p}}
	v := s.TerminalExecution(a, now)
	if v.SubmitEnabled || v.CancelEnabled || len(v.Markets) != 2 || v.Markets[0].SubmitEnabled || !v.Markets[1].SubmitEnabled || !v.Markets[1].ValidUntil.Equal(a.Capture.ValidUntil) {
		t.Fatal(v)
	}
	v.Markets[1].DaySessions[1].Start = "00:00"
	if p.Markets[1].DaySessions[1].Start != "13:00" {
		t.Fatal("mutable view")
	}
	h.ProducedAt = now.Add(-6 * time.Second)
	v = s.TerminalExecution(a, now)
	for _, m := range v.Markets {
		if m.SubmitEnabled || m.CancelEnabled {
			t.Fatal("stale heartbeat enabled", v)
		}
	}
}
