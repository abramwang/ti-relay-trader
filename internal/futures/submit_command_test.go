package futures

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSubmitCommandSharedFixtures(t *testing.T) {
	for _, name := range []string{"submit", "batch"} {
		raw := fixtureBody(t, "messages.json", name)
		cmd, err := DecodeSubmitCommand(raw, tradeScope())
		if err != nil {
			t.Fatal(err)
		}
		count := 1
		if name == "batch" {
			count = 2
		}
		if len(cmd.Orders) != count || !bytes.Equal(cmd.Raw, raw) {
			t.Fatal(cmd)
		}
		raw[0] = '!'
		if cmd.Raw[0] == '!' {
			t.Fatal("shared raw storage")
		}
	}
}

func TestSubmitCommandStrictEnvelopeAndIntent(t *testing.T) {
	raw := fixtureBody(t, "messages.json", "submit")
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["routing"] = map[string]any{} },
		func(m map[string]any) { m["action"] = "order.cancel" },
		func(m map[string]any) { m["message_type"] = "event" },
		func(m map[string]any) { m["sent_at"] = "2026-11-03T13:01:00Z" },
		func(m map[string]any) { m["idempotency_key"] = nil },
	} {
		if _, err := DecodeSubmitCommand(editBody(t, raw, change), tradeScope()); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	for _, change := range []func(map[string]any){
		func(p map[string]any) { p["account_id"] = "990000000002" },
		func(p map[string]any) { p["trade_date"] = "20261103" },
		func(p map[string]any) { p["exchange"] = "CFFEX" },
		func(p map[string]any) { p["gateway_order_id"] = "external-ctp-v1-test" },
		func(p map[string]any) { p["product_type"] = nil },
		func(p map[string]any) { delete(p, "client_order_id") },
		func(p map[string]any) { p["order_qty"] = json.RawMessage("1.0") },
		func(p map[string]any) { p["order_qty"] = true },
		func(p map[string]any) { p["order_qty"] = 0 },
		func(p map[string]any) { p["limit_price"] = "0" },
		func(p map[string]any) { p["limit_price"] = 3600 },
		func(p map[string]any) { p["limit_price"] = "1e3" },
		func(p map[string]any) { p["limit_price"] = "18446744073709551615.0" },
		func(p map[string]any) { p["offset_type"] = "BUY" },
	} {
		if _, err := DecodeSubmitCommand(editBody(t, raw, func(m map[string]any) { change(m["payload"].(map[string]any)) }), tradeScope()); err == nil {
			t.Fatal("invalid intent accepted")
		}
	}
	var obj map[string]json.RawMessage
	json.Unmarshal(raw, &obj)
	for key := range obj {
		if _, err := DecodeSubmitCommand(editBody(t, raw, func(m map[string]any) { delete(m, key) }), tradeScope()); err == nil {
			t.Fatal("missing envelope field", key)
		}
	}
}

func TestSubmitBatchInheritanceAndIdentity(t *testing.T) {
	raw := fixtureBody(t, "messages.json", "batch")
	for _, change := range []func(map[string]any){
		func(p map[string]any) { p["orders"] = nil },
		func(p map[string]any) { p["orders"] = []any{} },
		func(p map[string]any) { p["orders"] = []any{p["orders"].([]any)[0], p["orders"].([]any)[0]} },
		func(p map[string]any) { p["orders"].([]any)[0].(map[string]any)["product_type"] = nil },
		func(p map[string]any) { p["orders"].([]any)[0].(map[string]any)["schema_version"] = "other" },
		func(p map[string]any) { p["orders"].([]any)[0].(map[string]any)["account_id"] = "990000000002" },
		func(p map[string]any) { p["orders"].([]any)[0].(map[string]any)["trade_date"] = "20261103" },
		func(p map[string]any) {
			p["orders"].([]any)[0].(map[string]any)["client_order_id"] = "fixture-client-b2"
		},
	} {
		if _, err := DecodeSubmitCommand(editBody(t, raw, func(m map[string]any) { change(m["payload"].(map[string]any)) }), tradeScope()); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
}

func TestSubmitIntentEventMatch(t *testing.T) {
	cmd, err := DecodeSubmitCommand(fixtureBody(t, "messages.json", "submit"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"order", "fill"} {
		raw := fixtureBody(t, "messages.json", kind)
		event, err := DecodeTradeEvent(raw, EventContext{Scope: tradeScope()})
		if err != nil || !cmd.Orders[0].matches(event, cmd) {
			t.Fatal("original fixture mismatch", err)
		}
		event.OriginMessageID = "wrong"
		if cmd.Orders[0].matches(event, cmd) {
			t.Fatal("wrong origin accepted")
		}
	}
}
