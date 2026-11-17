package futures

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// All economics in these tests are synthetic fixtures, not the OC raw callbacks.
func asV2Fill(t *testing.T, raw []byte) []byte {
	return editBody(t, raw, func(m map[string]any) {
		ids := m["native_ids"].(map[string]any)
		m["fill_id"] = nativeFillIdentityV2(tradeScope(), m["exchange"].(string), ids["trade_id"].(string), ids["order_sys_id"].(string), m["trade_side"].(string))
	})
}

func v2Pair(t *testing.T) ([][]byte, [][]byte) {
	t.Helper()
	orders, fills := [][]byte{}, [][]byte{}
	for i, sys := range []string{"31174", "31234"} {
		side := []string{"B", "S"}[i]
		mutate := func(m map[string]any) {
			m["gateway_order_id"] = nativeIdentity("order", tradeScope(), "SHFE", sys)
			m["trade_side"] = side
			ids := m["native_ids"].(map[string]any)
			ids["order_sys_id"] = sys
		}
		orders = append(orders, editBody(t, tradeFixture(t, "order"), func(m map[string]any) {
			mutate(m)
			m["order_qty"], m["cum_filled_qty"], m["leaves_qty"], m["gateway_status"] = 1, 1, 0, "filled"
		}))
		fills = append(fills, asV2Fill(t, editBody(t, tradeFixture(t, "fill"), func(m map[string]any) {
			mutate(m)
			m["native_ids"].(map[string]any)["trade_id"] = "5013"
		})))
	}
	return orders, fills
}

func TestFillV2StrictIdentity(t *testing.T) {
	base := asV2Fill(t, tradeFixture(t, "fill"))
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["fill_id"] = "ctp-fill-v2-wrong" },
		func(m map[string]any) { m["trade_side"] = "S" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["order_sys_id"] = "1234" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["trade_id"] = "5678" },
		func(m map[string]any) { m["native_ids"].(map[string]any)["trade_id"] = " 00005678" },
		func(m map[string]any) { m["trade_side"] = "0" },
	} {
		if _, err := DecodeFill(editBody(t, base, change), tradeScope()); err == nil {
			t.Fatal("identity mismatch accepted")
		}
	}
	other := asV2Fill(t, editBody(t, base, func(m map[string]any) { m["exchange"] = "CFFEX" }))
	if _, err := DecodeFill(other, tradeScope()); err == nil {
		t.Fatal("v2 enabled outside SHFE")
	}
	// Session/reception time do not change identity or count; raw source time does
	// remain an immutable execution fact once known.
	row, err := DecodeFill(editBody(t, base, func(m map[string]any) {
		m["counter_session_id"] = "reconnected"
		m["fee"], m["fee_complete"] = "1.2", true
	}), tradeScope())
	if err != nil || row.FillID != nativeFillIdentityV2(tradeScope(), "SHFE", "00005678", "00001234", "B") {
		t.Fatal("stable identity", err)
	}
}

func TestFillV2QueryAndEventPaths(t *testing.T) {
	_, fills := v2Pair(t)
	b, _ := NewQueryBatch(tradeQueryContext("fill"), 0, time.Minute, DefaultBatchLimits())
	if _, err := b.Accept(tradePage(t, "fill", "last", 1, true, fills[1]), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accept(tradePage(t, "fill", "first", 0, false, fills[0]), 0); err != nil {
		t.Fatal(err)
	}
	c, err := b.Candidate()
	if err != nil || len(c.Fills) != 2 || c.Fills[0].FillID == c.Fills[1].FillID {
		t.Fatal(c, err)
	}
	for _, fill := range fills {
		raw := editBody(t, fixtureBody(t, "messages.json", "fill"), func(m map[string]any) {
			var p map[string]any
			if err := json.Unmarshal(fill, &p); err != nil {
				t.Fatal(err)
			}
			p["product_type"], p["schema_version"] = "futures", "ctp.f0.draft.2"
			m["payload"] = p
			delete(m, "origin_message_id")
		})
		e, err := DecodeTradeEvent(raw, EventContext{Scope: tradeScope()})
		if err != nil || e.Fill == nil || !bytes.Equal(e.Raw, raw) {
			t.Fatal("event decoding/raw", err)
		}
	}
}

func TestFillV2MixedQueryAndDuplicateRejected(t *testing.T) {
	old := tradeFixture(t, "fill")
	new := asV2Fill(t, old)
	for _, pair := range [][][]byte{{old, new}, {new, old}, {new, new}} {
		raw, _ := json.Marshal([]json.RawMessage{pair[0], pair[1]})
		if _, err := DecodeFills(raw, tradeScope()); err == nil {
			t.Fatal("mixed/duplicate page accepted")
		}
		b, _ := NewQueryBatch(tradeQueryContext("fill"), 0, time.Minute, DefaultBatchLimits())
		if _, err := b.Accept(tradePage(t, "fill", "first", 0, false, pair[0]), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Accept(tradePage(t, "fill", "last", 1, true, pair[1]), 0); err == nil {
			t.Fatal("mixed/duplicate across pages accepted")
		}
		assertState(t, b, BatchInvalid)
	}
}

func TestFillV2ProjectionReplayAndConflicts(t *testing.T) {
	orders, fills := v2Pair(t)
	for _, reverse := range []bool{false, true} {
		p := newTradeProjection(tradeScope())
		for _, i := range []int{0, 1} {
			if reverse {
				i = 1 - i
			}
			p.observe("fill", "fill", fills[i])
			p.observe("order", "order", orders[i])
			p.observe("repeat", "fill", fills[i])
		}
		if p.report.Blocked || p.report.FillIdentityVersion != "v2" || len(p.report.Executions) != 2 || p.report.PendingCount != 0 {
			t.Fatal(p.report)
		}
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["price"] = "3601" },
		func(m map[string]any) { m["qty"] = 2 },
		func(m map[string]any) { m["symbol"] = "rb2701" },
		func(m map[string]any) { m["offset_type"] = "C" },
		func(m map[string]any) { m["matched_at"] = "2026-11-03T21:02:01+08:00" },
	} {
		p := newTradeProjection(tradeScope())
		p.observe("first", "fill", fills[0])
		p.observe("conflict", "fill", editBody(t, fills[0], change))
		if !p.report.Blocked || len(p.report.Executions) != 1 {
			t.Fatal("conflict not preserved")
		}
		for _, r := range p.report.Executions {
			if !r.FactConflict {
				t.Fatal("economic conflict ignored")
			}
		}
	}
}
