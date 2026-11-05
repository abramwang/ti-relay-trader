package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSubmitOCCommandByteBudget(t *testing.T) {
	raw := fixtureBody(t, "messages.json", "submit")
	boundary := append(bytes.Clone(raw), bytes.Repeat([]byte(" "), 65536-len(raw))...)
	command, err := DecodeSubmitCommand(boundary, tradeScope())
	if err != nil || !bytes.Equal(command.Raw, boundary) {
		t.Fatal("64KiB including whitespace", err)
	}
	if _, err := DecodeSubmitCommand(append(boundary, ' '), tradeScope()); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal("over-budget command accepted", err)
	}
}

func TestSubmitOCIdentifierByteBudget(t *testing.T) {
	for _, key := range []string{"message_id", "request_id", "correlation_id", "idempotency_key", "gateway_order_id", "client_order_id"} {
		for _, value := range []string{strings.Repeat("x", 128), strings.Repeat("\u754c", 42) + "ab", strings.Repeat(":", 128)} {
			build := func(id string) []byte {
				return editBody(t, fixtureBody(t, "messages.json", "submit"), func(m map[string]any) {
					target := m
					if key == "gateway_order_id" || key == "client_order_id" {
						target = m["payload"].(map[string]any)
					}
					target[key] = id
				})
			}
			if _, err := DecodeSubmitCommand(build(value), tradeScope()); err != nil {
				t.Fatal(key, err)
			}
			if _, err := DecodeSubmitCommand(build(value+"x"), tradeScope()); err == nil {
				t.Fatal("129-byte ID accepted", key)
			}
		}
	}
}

func TestSubmitOCBatchCountAndByteBudgets(t *testing.T) {
	build := func(count int, wide bool) []byte {
		return editBody(t, fixtureBody(t, "messages.json", "batch"), func(m map[string]any) {
			parent := m["payload"].(map[string]any)
			base := parent["orders"].([]any)[0].(map[string]any)
			rows := []any{}
			for i := 0; i < count; i++ {
				row := map[string]any{}
				for k, v := range base {
					row[k] = v
				}
				row["gateway_order_id"] = fmt.Sprint("g", i)
				row["client_order_id"] = fmt.Sprint("c", i)
				if wide {
					row["gateway_order_id"] = fmt.Sprintf("%0128d", i)
					row["client_order_id"] = strings.Repeat("\\", 125) + fmt.Sprintf("%03d", i)
					row["symbol"] = strings.Repeat("r", 80)
					row["product_type"] = "futures"
					row["schema_version"] = "ctp.f0.draft.2"
				}
				rows = append(rows, row)
			}
			parent["orders"] = rows
		})
	}
	if cmd, err := DecodeSubmitCommand(build(100, false), tradeScope()); err != nil || len(cmd.Orders) != 100 {
		t.Fatal(err)
	}
	if _, err := DecodeSubmitCommand(build(101, false), tradeScope()); err == nil {
		t.Fatal("101 children accepted")
	}
	wide := build(100, true)
	if len(wide) <= 65536 {
		t.Fatal("test did not exceed total byte limit", len(wide))
	}
	if _, err := DecodeSubmitCommand(wide, tradeScope()); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal("count-only budget bypass", err)
	}
}

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
