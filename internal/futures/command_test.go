package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type intentVector struct {
	Name            string            `json:"name"`
	Raw             string            `json:"raw_body"`
	Stream          string            `json:"stream_suffix"`
	Scope           map[string]string `json:"scope"`
	Fields          []IntentField     `json:"fields"`
	Intent          string            `json:"intent_bytes"`
	IntentHash      string            `json:"intent_sha256"`
	Message         string            `json:"message_lookup_bytes"`
	MessageHash     string            `json:"message_lookup_sha256"`
	Idempotency     string            `json:"idempotency_lookup_bytes"`
	IdempotencyHash string            `json:"idempotency_lookup_sha256"`
	RawHash         string            `json:"raw_body_sha256"`
}

func (v intentVector) scope() CommandScope {
	s := v.Scope
	return CommandScope{QueryRoute{Env: s["env"], BrokerID: s["broker_id"], AccountID: s["account_id"], GatewayID: s["gateway_id"]}, s["native_broker_id"], s["investor_id"], s["user_id"]}
}

func intentVectors(t *testing.T) map[string]intentVector {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/fixtures/ctp_intent_vectors.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if commandDigest(raw) != "cd2e9f38e2d4216a17cfa0802bcff3093256dbd5920ee39fe5e9f5cddf0d69b4" {
		t.Fatal("OC fixture changed")
	}
	var doc struct {
		Cases []intentVector `json:"cases"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 12 {
		t.Fatal("missing vectors")
	}
	out := map[string]intentVector{}
	for _, v := range doc.Cases {
		out[v.Name] = v
	}
	return out
}

func TestCommandOCIntentVectors(t *testing.T) {
	for name, v := range intentVectors(t) {
		t.Run(name, func(t *testing.T) {
			c, err := DecodeCommand([]byte(v.Raw), v.scope(), v.Stream)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.Fields, v.Fields) {
				t.Fatal("field order/value mismatch", c.Fields)
			}
			for _, pair := range []struct {
				raw        []byte
				want, hash string
			}{{c.Intent, v.Intent, v.IntentHash}, {c.MessageLookup, v.Message, v.MessageHash}, {c.IdempotencyLookup, v.Idempotency, v.IdempotencyHash}, {c.Raw, v.Raw, v.RawHash}} {
				if string(pair.raw) != pair.want || commandDigest(pair.raw) != pair.hash {
					t.Fatal("bytes or hash mismatch")
				}
			}
		})
	}
}

func TestCommandQueriesAndCancelStrictSyntax(t *testing.T) {
	for _, name := range []string{"account.asset.query", "account.positions.query", "order.list.query", "fill.list.query", "cancel_target_day"} {
		v := intentVectors(t)[name]
		for _, change := range []func(map[string]any){
			func(m map[string]any) { m["routing"] = map[string]any{} },
			func(m map[string]any) { m["sent_at"] = "2026-11-05T00:00:00Z" },
			func(m map[string]any) { m["message_id"] = strings.Repeat("x", 129) },
			func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = "123456" },
			func(m map[string]any) { m["payload"].(map[string]any)["schema_version"] = nil },
			func(m map[string]any) { m["payload"].(map[string]any)["request_id"] = "injected" },
		} {
			if _, err := DecodeCommand(editBody(t, []byte(v.Raw), change), v.scope(), v.Stream); err == nil {
				t.Fatal("invalid command accepted", name)
			}
		}
		wrong := "cmd.trade"
		if v.Stream == wrong {
			wrong = "cmd.query"
		}
		if _, err := DecodeCommand([]byte(v.Raw), v.scope(), wrong); err == nil {
			t.Fatal("wrong stream accepted")
		}
	}
}

func TestCommandHistoricalSyntaxAndNativeBinding(t *testing.T) {
	v := intentVectors(t)["submit"]
	raw := editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = "20200102" })
	c, err := DecodeCommand(raw, v.scope(), v.Stream)
	if err != nil {
		t.Fatal("historical syntax rejected before lookup", err)
	}
	if c.Orders[0].TradeDate != "20200102" {
		t.Fatal(c)
	}
	for _, change := range []func(*CommandScope){func(s *CommandScope) { s.UserID = "OTHER" }, func(s *CommandScope) { s.InvestorID = "OTHER" }, func(s *CommandScope) { s.NativeBrokerID = "1234" }} {
		s := v.scope()
		change(&s)
		other, err := DecodeCommand(raw, s, v.Stream)
		if err != nil || bytes.Equal(c.Intent, other.Intent) || !bytes.Equal(c.IdempotencyLookup, other.IdempotencyLookup) || !bytes.Equal(c.MessageLookup, other.MessageLookup) {
			t.Fatal("native identity not bound", err)
		}
	}
}

func TestCommandRawAndMalformedJSON(t *testing.T) {
	v := intentVectors(t)["account.asset.query"]
	for _, raw := range [][]byte{[]byte(v.Raw + "{}"), []byte(strings.Replace(v.Raw, `"message_type":`, `"message_id":"duplicate","message_type":`, 1)), bytes.Replace([]byte(v.Raw), []byte("fixture"), []byte{0xff}, 1)} {
		if _, err := DecodeCommand(raw, v.scope(), v.Stream); err == nil {
			t.Fatal("malformed input accepted")
		}
	}
	raw := []byte(v.Raw)
	c, err := DecodeCommand(raw, v.scope(), v.Stream)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if c.Raw[0] == '!' {
		t.Fatal("aliased raw")
	}
}

func TestCommandBatchDayAndPriceBeforeNormalization(t *testing.T) {
	v := intentVectors(t)["ordered_batch"]
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["trade_date"] = "20261106" },
		func(m map[string]any) { m["trade_date"] = "20260230" },
		func(m map[string]any) { m["limit_price"] = "18446744073709551615.0" },
		func(m map[string]any) { m["limit_price"] = "1e3" },
		func(m map[string]any) { m["limit_price"] = 10 },
		func(m map[string]any) { m["order_qty"] = true },
		func(m map[string]any) { m["schema_version"] = nil },
	} {
		raw := editBody(t, []byte(v.Raw), func(m map[string]any) {
			change(m["payload"].(map[string]any)["orders"].([]any)[1].(map[string]any))
		})
		if _, err := DecodeCommand(raw, v.scope(), v.Stream); err == nil {
			t.Fatal("invalid child accepted")
		}
	}
}
