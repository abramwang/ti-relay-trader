package futures

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func tradeScope() TradeScope {
	return TradeScope{Route: batchContext("positions").Route, NativeBrokerID: "4900", InvestorID: "FIXTURE_INV", TradeDate: "20261104"}
}

func tradeFixture(t testing.TB, kind string) []byte {
	t.Helper()
	name := "orders"
	if kind == "fill" {
		name = "fills"
	}
	body := fixtureBody(t, "query_pages.json", name)
	var doc struct {
		Payload struct {
			Items []json.RawMessage `json:"items"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Payload.Items[0]
}

func TestTradeSharedRowsAndNativeIdentity(t *testing.T) {
	s := tradeScope()
	o, err := DecodeOrder(tradeFixture(t, "order"), s)
	if err != nil || o.NativeIDs.OrderSysID != "00001234" || o.LimitPrice != "3600" || o.Symbol != "rb2612" {
		t.Fatal(o, err)
	}
	f, err := DecodeFill(tradeFixture(t, "fill"), s)
	if err != nil || f.NativeIDs.TradeID != "00005678" || f.Fee != nil || f.FeeComplete {
		t.Fatal(f, err)
	}
	if f.FillID != "ctp-fill-v1-5ad40797043cdb61172b14339c29986a866ac1cc606199b02b942f12c0b87393" {
		t.Fatal("seven-part identity changed")
	}
	for _, change := range []func(*TradeScope){func(s *TradeScope) { s.Route.Env = "prod" }, func(s *TradeScope) { s.TradeDate = "20261105" }, func(s *TradeScope) { s.Route.AccountID = "990000000002" }, func(s *TradeScope) { s.Route.GatewayID = "other" }, func(s *TradeScope) { s.Route.BrokerID = "other" }} {
		c := s
		change(&c)
		if nativeIdentity("fill", c, "SHFE", "00005678") == f.FillID {
			t.Fatal("identity omitted scope dimension")
		}
	}
}

func TestTradeStrictRequiredFields(t *testing.T) {
	for _, kind := range []string{"order", "fill"} {
		base := tradeFixture(t, kind)
		var obj map[string]json.RawMessage
		json.Unmarshal(base, &obj)
		for key := range obj {
			if key == "captured_at" {
				continue
			}
			raw := editBody(t, base, func(m map[string]any) { delete(m, key) })
			if kind == "order" {
				if _, err := DecodeOrder(raw, tradeScope()); err == nil {
					t.Fatalf("missing order %s accepted", key)
				}
			} else {
				if _, err := DecodeFill(raw, tradeScope()); err == nil {
					t.Fatalf("missing fill %s accepted", key)
				}
			}
		}
	}
}

func TestTradeStrictTypesAndSourceScope(t *testing.T) {
	changes := []func(map[string]any){
		func(m map[string]any) { m["fee_complete"] = nil }, func(m map[string]any) { m["fee_complete"] = true },
		func(m map[string]any) { m["price"] = 3600 }, func(m map[string]any) { m["price"] = "0" },
		func(m map[string]any) { m["price"] = "3600e0" }, func(m map[string]any) { m["price"] = "18446744073709551615.0" },
		func(m map[string]any) { m["price"] = "1.0000000000" }, func(m map[string]any) { m["qty"] = json.RawMessage("1.0") },
		func(m map[string]any) { m["qty"] = nil }, func(m map[string]any) { m["qty"] = 0 },
		func(m map[string]any) { m["native_ids"].(map[string]any)["sdk_request_id"] = nil },
		func(m map[string]any) { m["native_ids"].(map[string]any)["investor_id"] = "OTHER" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["broker_id"] = "04900" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["order_ref"] = false },
		func(m map[string]any) { m["native_ids"].(map[string]any)["order_ref"] = nil },
		func(m map[string]any) { m["native_ids"].(map[string]any)["order_sys_id"] = "" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["trade_id"] = "5678" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["front_id"] = json.RawMessage("1e0") },
		func(m map[string]any) { m["offset_type"] = "CLOSE" }, func(m map[string]any) { m["hedge_type"] = "hedging" },
		func(m map[string]any) { m["business_type"] = "S" }, func(m map[string]any) { m["trade_side"] = "BUY" },
		func(m map[string]any) { m["account_id"] = "990000000002" }, func(m map[string]any) { m["trade_date"] = "20261103" },
		func(m map[string]any) { m["matched_at"] = "2026-11-03T13:01:01Z" }, func(m map[string]any) { m["captured_at"] = nil },
		func(m map[string]any) { m["fee"] = 1.2 }, func(m map[string]any) { m["extra"] = 0 },
	}
	base := tradeFixture(t, "fill")
	for i, change := range changes {
		if _, err := DecodeFill(editBody(t, base, change), tradeScope()); err == nil {
			t.Fatalf("invalid mutation %d accepted", i)
		}
	}
	for _, raw := range [][]byte{[]byte("null"), append(bytes.Clone(base), []byte("{}")...), []byte(strings.Replace(string(base), `"qty":`, `"qty":2,"qty":`, 1)), {0xff}} {
		if _, err := DecodeFill(raw, tradeScope()); err == nil {
			t.Fatal("malformed row accepted")
		}
	}
	for _, value := range []string{"0", "0.1", "18446744073709551615"} {
		var p Price
		if err := json.Unmarshal([]byte(`"`+value+`"`), &p); err != nil || string(p) != value {
			t.Fatal(value, err)
		}
	}
}

func TestTradeOrderQuantitiesAndNullableTime(t *testing.T) {
	base := tradeFixture(t, "order")
	for _, change := range []func(map[string]any){func(m map[string]any) { m["order_qty"] = 0 }, func(m map[string]any) { m["cum_filled_qty"] = 1 }, func(m map[string]any) { m["gateway_status"] = "filled" }, func(m map[string]any) { m["gateway_status"] = "queued" }, func(m map[string]any) { m["submitted_at"] = "today" }, func(m map[string]any) { m["reject_message"] = false }} {
		if _, err := DecodeOrder(editBody(t, base, change), tradeScope()); err == nil {
			t.Fatal("invalid order accepted")
		}
	}
	o, err := DecodeOrder(editBody(t, base, func(m map[string]any) { m["submitted_at"] = nil }), tradeScope())
	if err != nil || o.SubmittedAt != nil {
		t.Fatal("unknown source time synthesized", err)
	}
	if !equalInstant("2026-11-03T21:01:01+08:00", "2026-11-03T21:01:01.000000000+08:00") || equalInstant("2026-11-03T21:01:01.000000001+08:00", "2026-11-03T21:01:01.000000002+08:00") {
		t.Fatal("nanosecond time semantics")
	}
}

func TestTradeFillFirstLinkAndFeeEvidence(t *testing.T) {
	p := newTradeProjection(tradeScope())
	fill := tradeFixture(t, "fill")
	p.observe("1", "fill", fill)
	if len(p.report.Orders) != 0 || len(p.report.Executions) != 1 || p.report.PendingCount != 1 || !p.report.Blocked {
		t.Fatal("fill first manufactured order", p.report)
	}
	p.observe("2", "order", tradeFixture(t, "order"))
	p.observe("3", "fill", editBody(t, fill, func(m map[string]any) {
		m["fee"] = "1.23"
		m["fee_complete"] = true
		m["price"] = "3600.0"
		m["counter_session_id"] = "reconnected"
	}))
	if len(p.report.Executions) != 1 || p.report.Blocked || p.report.PendingCount != 0 {
		t.Fatal(p.report)
	}
	for _, r := range p.report.Executions {
		if r.State != "linked" || len(r.FeeEvidence) != 2 || r.Fill.Fee != nil || *r.FeeEvidence[1].Fee != "1.23" {
			t.Fatal("fee evidence counted as execution or final fee", r)
		}
	}
}

func TestTradeExternalHintAndWrongLocalHint(t *testing.T) {
	for _, good := range []bool{true, false} {
		p := newTradeProjection(tradeScope())
		id := "different-local"
		if good {
			id = nativeIdentity("order", tradeScope(), "SHFE", "00001234")
		}
		raw := editBody(t, tradeFixture(t, "fill"), func(m map[string]any) { m["gateway_order_id"] = id })
		p.observe("1", "fill", raw)
		p.observe("2", "order", tradeFixture(t, "order"))
		if p.report.Blocked == good {
			t.Fatal("gateway hint gate", p.report)
		}
	}
	if _, err := DecodeFill(editBody(t, tradeFixture(t, "fill"), func(m map[string]any) { m["gateway_order_id"] = "external-ctp-v1-wrong" }), tradeScope()); err == nil {
		t.Fatal("forged external ID accepted")
	}
}

func TestTradeNativeAliasCollisionAndExecutionConflicts(t *testing.T) {
	p := newTradeProjection(tradeScope())
	p.observe("1", "order", tradeFixture(t, "order"))
	p.observe("2", "fill", tradeFixture(t, "fill"))
	p.observe("3", "order", editBody(t, tradeFixture(t, "order"), func(m map[string]any) { m["gateway_order_id"] = "other-order" }))
	for _, r := range p.report.Executions {
		if r.State != "conflict" || r.LinkedGateway != "" {
			t.Fatal("ambiguous alias still linked")
		}
	}
	for _, change := range []func(map[string]any){func(m map[string]any) { m["qty"] = 2 }, func(m map[string]any) { m["price"] = "3601" }, func(m map[string]any) { m["matched_at"] = "2026-11-03T21:01:01.000000001+08:00" }, func(m map[string]any) { m["native_ids"].(map[string]any)["order_sys_id"] = "00001235" }} {
		p := newTradeProjection(tradeScope())
		p.observe("1", "fill", tradeFixture(t, "fill"))
		p.observe("2", "fill", editBody(t, tradeFixture(t, "fill"), change))
		if len(p.report.Executions) != 1 || !p.report.Blocked {
			t.Fatal("conflicting execution replaced fact", p.report)
		}
		for _, r := range p.report.Executions {
			if !r.FactConflict || r.State != "conflict" {
				t.Fatal(r)
			}
		}
	}
}

func TestTradeUniqueExecutionOverfill(t *testing.T) {
	p := newTradeProjection(tradeScope())
	p.observe("o", "order", tradeFixture(t, "order"))
	p.observe("f1", "fill", tradeFixture(t, "fill"))
	p.observe("f2", "fill", editBody(t, tradeFixture(t, "fill"), func(m map[string]any) {
		m["native_ids"].(map[string]any)["trade_id"] = "00005679"
		m["fill_id"] = nativeIdentity("fill", tradeScope(), "SHFE", "00005679")
	}))
	if len(p.report.Executions) != 2 || !p.report.Blocked {
		t.Fatal("overfill discarded", p.report)
	}
	for _, r := range p.report.Executions {
		if r.State != "conflict" {
			t.Fatal("overfilled order still exposed as linked")
		}
	}
}

func stateRow(t *testing.T, state string, filled, leaves int) []byte {
	return editBody(t, tradeFixture(t, "order"), func(m map[string]any) {
		m["order_qty"] = 3
		m["cum_filled_qty"] = filled
		m["leaves_qty"] = leaves
		m["gateway_status"] = state
	})
}

func TestTradeOrderLifecycleArrivalPermutations(t *testing.T) {
	rows := [][]byte{stateRow(t, "accepted", 0, 3), stateRow(t, "working", 0, 3), stateRow(t, "working", 1, 2), stateRow(t, "filled", 3, 0)}
	var visit func([]int)
	visit = func(order []int) {
		if len(order) == 4 {
			p := newTradeProjection(tradeScope())
			for i, index := range order {
				p.observe(string(rune('a'+i)), "order", rows[index])
			}
			if p.report.Blocked || p.report.Orders["fixture-order-1"].Order.GatewayStatus != "filled" {
				t.Fatal(order, p.report)
			}
			return
		}
		for i := 0; i < 4; i++ {
			used := false
			for _, n := range order {
				used = used || n == i
			}
			if !used {
				visit(append(append([]int{}, order...), i))
			}
		}
	}
	visit(nil)
	for _, pair := range [][2][]byte{{stateRow(t, "cancelled", 1, 0), stateRow(t, "filled", 3, 0)}, {stateRow(t, "working", 2, 1), stateRow(t, "cancelled", 1, 0)}, {stateRow(t, "working", 0, 3), stateRow(t, "accepted", 1, 2)}} {
		p := newTradeProjection(tradeScope())
		p.observe("1", "order", pair[0])
		p.observe("2", "order", pair[1])
		if !p.report.Blocked || !p.report.Orders["fixture-order-1"].Blocked {
			t.Fatal("order lifecycle conflict accepted")
		}
	}
}

func TestTradeMissingSysIDAndSourceTimeEnrichment(t *testing.T) {
	p := newTradeProjection(tradeScope())
	p.observe("1", "fill", editBody(t, tradeFixture(t, "fill"), func(m map[string]any) { m["matched_at"] = nil }))
	p.observe("2", "order", editBody(t, tradeFixture(t, "order"), func(m map[string]any) {
		delete(m["native_ids"].(map[string]any), "order_sys_id")
		m["submitted_at"] = nil
	}))
	if p.report.PendingCount != 1 {
		t.Fatal("linked without native proof")
	}
	p.observe("3", "order", tradeFixture(t, "order"))
	p.observe("4", "fill", tradeFixture(t, "fill"))
	if p.report.Blocked || p.report.Orders["fixture-order-1"].Order.SubmittedAt == nil {
		t.Fatal(p.report)
	}
	for _, r := range p.report.Executions {
		if r.Fill.MatchedAt == nil {
			t.Fatal("source time not enriched")
		}
	}
}

func FuzzTradeDecoders(f *testing.F) {
	f.Add(tradeFixture(f, "order"))
	f.Add(tradeFixture(f, "fill"))
	f.Add([]byte("null"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeOrder(raw, tradeScope())
		_, _ = DecodeFill(raw, tradeScope())
	})
}
