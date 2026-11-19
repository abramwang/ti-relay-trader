package futures

import (
	"encoding/json"
	"os"
	"testing"
)

func resultQueryFixture(t *testing.T, kind string) (CommandRegistration, TestQueryTarget, []byte) {
	t.Helper()
	q := batchContext(kind)
	var body []byte
	if kind == "order" || kind == "fill" {
		q = tradeQueryContext(kind)
		body = tradePage(t, kind, "result-query", 0, true, tradeFixture(t, kind))
	} else {
		body = fixtureBody(t, "messages.json", kind)
	}
	q.NativeInvestorID, q.NativeUserID = "FIXTURE_INV", "FIXTURE_USER"
	raw, _ := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "command", "message_id": q.OriginMessageID, "request_id": "result-request", "correlation_id": "result-correlation", "idempotency_key": "result-key", "action": q.Action, "sent_at": "2026-11-04T13:00:00+08:00", "payload": map[string]any{"account_id": q.Route.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2"}})
	c, err := DecodeCommand(raw, q.commandScope(), "cmd.query")
	if err != nil {
		t.Fatal(err)
	}
	r := CommandRegistration{CommandID: 1, Scope: q.commandScope(), Original: c}
	target := publicationTarget()
	target.CounterSessionID = q.CounterSessionID
	target.TradeDate = q.TradeDate
	target.ConnectionGeneration = *q.ConnectionGeneration
	return r, target, boundReply(t, body, r)
}

func resultTradeFixture(t *testing.T) (CommandRegistration, TestQueryTarget, []byte) {
	t.Helper()
	v, _, target, _ := assessmentFixture(t)
	c, err := DecodeCommand([]byte(v.Raw), v.scope(), "cmd.trade")
	if err != nil {
		t.Fatal(err)
	}
	r := CommandRegistration{CommandID: 1, Scope: v.scope(), Original: c}
	return r, target, resultTradeBody(t, r, target)
}

func resultTradeBody(t *testing.T, r CommandRegistration, target TestQueryTarget) []byte {
	t.Helper()
	gateway := ""
	if len(r.Original.Orders) > 0 {
		gateway = r.Original.Orders[0].GatewayOrderID
	} else {
		gateway = r.Original.Cancel.GatewayOrderID
	}
	raw, err := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "reply", "message_id": "synthetic-receipt", "origin_message_id": r.Original.MessageID, "request_id": r.Original.RequestID, "correlation_id": r.Original.CorrelationID, "idempotency_key": r.Original.IdempotencyKey, "routing": r.Scope.Route, "action": r.Original.Action, "status": "accepted", "code": "OK", "result_type": "order_action_receipt", "produced_at": "2026-11-19T10:00:00+08:00", "payload": map[string]any{"account_id": r.Scope.Route.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2", "counter_session_id": target.CounterSessionID, "trade_date": target.TradeDate, "gateway_order_id": gateway}, "adapter_context": map[string]any{"connection_generation": target.ConnectionGeneration, "sdk_request_id": 5, "sdk_return_code": 0}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReplyResultFourQueries(t *testing.T) {
	for _, kind := range []string{"asset", "positions", "order", "fill"} {
		t.Run(kind, func(t *testing.T) {
			r, target, raw := resultQueryFixture(t, kind)
			got := assembleReplyResult(r, target, [][]byte{raw, raw})
			if got.State != "ready" || got.Snapshot == nil || !got.ResponseValidated || got.NativeAuditVerified || got.RetrySafe || got.OrderStateChanged {
				t.Fatal(got)
			}
			empty := editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} })
			got = assembleReplyResult(r, target, [][]byte{empty})
			if kind == "asset" {
				if got.State != "invalid" || got.Snapshot != nil {
					t.Fatal(got)
				}
				return // An empty asset response cannot prove a zero balance.
			}
			if got.State != "ready" || got.Snapshot == nil {
				t.Fatal(got)
			}
		})
	}
}

func TestReplyResultOCOriginalConstraints(t *testing.T) {
	var doc struct {
		Cases []struct {
			Name    string          `json:"name"`
			Request json.RawMessage `json:"request"`
			Outputs []struct {
				Body string `json:"body"`
			} `json:"outputs"`
		} `json:"cases"`
	}
	queryRuntimeFixture(t, "native_facts_runtime.json", &doc)
	if len(doc.Cases) != 9 {
		t.Fatal("unexpected cases")
	}
	for _, v := range doc.Cases {
		t.Run(v.Name, func(t *testing.T) {
			raw := []byte(v.Outputs[0].Body)
			q := runtimeFactsContext(t, raw)
			c, err := DecodeCommand(v.Request, q.commandScope(), "cmd.query")
			if err != nil {
				t.Fatal(err)
			}
			r := CommandRegistration{CommandID: 1, Scope: q.commandScope(), Original: c}
			target := publicationTarget()
			target.CounterSessionID, target.TradeDate, target.ConnectionGeneration = q.CounterSessionID, q.TradeDate, *q.ConnectionGeneration
			got := assembleReplyResult(r, target, [][]byte{raw, raw})
			want := "failed"
			if oneOf(v.Name, "complete", "null_price", "prefix_option") {
				want = "ready"
			}
			if got.State != want || !got.ResponseValidated || got.NativeAuditVerified {
				t.Fatal(got)
			}
		})
	}
}

func TestReplyResultPagesAndContradictions(t *testing.T) {
	r, target, base := resultQueryFixture(t, "positions")
	first := editBody(t, base, func(m map[string]any) {
		m["message_id"] = "first"
		m["status"] = "partial"
		m["chunk"] = map[string]any{"index": 0, "is_last": false}
	})
	last := editBody(t, base, func(m map[string]any) {
		m["message_id"] = "last"
		m["chunk"] = map[string]any{"index": 1, "is_last": true}
		m["payload"].(map[string]any)["items"] = []any{}
	})
	if got := assembleReplyResult(r, target, [][]byte{last}); got.State != "collecting" || got.Snapshot != nil {
		t.Fatal(got)
	}
	if got := assembleReplyResult(r, target, [][]byte{last, first, last}); got.State != "ready" || len(got.Snapshot.Positions) != 1 {
		t.Fatal(got)
	}
	for name, change := range map[string]func(map[string]any){
		"sdk":                  func(m map[string]any) { evidence(m)["sdk_request_id"] = 999 },
		"ticket":               func(m map[string]any) { evidence(m)["ticket"] = "different" },
		"generation":           func(m map[string]any) { evidence(m)["connection_generation"] = 9 },
		"session":              func(m map[string]any) { evidence(m)["counter_session_id"] = "other" },
		"request":              func(m map[string]any) { m["request_id"] = "foreign" },
		"same_message_changed": func(m map[string]any) { m["message"] = "changed bytes" },
		"new_terminal":         func(m map[string]any) { m["message_id"] = "new-terminal" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := editBody(t, base, change)
			got := assembleReplyResult(r, target, [][]byte{base, bad})
			if got.State != "invalid" || got.Snapshot != nil || got.ResponseValidated {
				t.Fatal(got)
			}
		})
	}
	fail := failureBody(t, base)
	if got := assembleReplyResult(r, target, [][]byte{fail}); got.State != "failed" || got.Snapshot != nil {
		t.Fatal(got)
	}
	if got := assembleReplyResult(r, target, [][]byte{base, fail}); got.State != "invalid" || got.Snapshot != nil {
		t.Fatal(got)
	}
}

func TestReplyResultTradeReceiptNotOrderState(t *testing.T) {
	r, target, raw := resultTradeFixture(t)
	got := assembleReplyResult(r, target, [][]byte{raw, raw})
	if got.State != "accepted" || !got.ResponseValidated || got.OrderStateChanged || got.RetrySafe || got.Snapshot != nil {
		t.Fatal(got)
	}
	failed := editBody(t, raw, func(m map[string]any) {
		m["status"] = "failed"
		m["code"] = "ORDER_SUBMIT_REJECTED"
		delete(m, "result_type")
		a := m["adapter_context"].(map[string]any)
		a["sdk_request_id"], a["sdk_return_code"], a["sdk_calls"], a["failure_stage"] = nil, nil, 0, "command_validation"
	})
	if got := assembleReplyResult(r, target, [][]byte{failed}); got.State != "failed" || got.OrderStateChanged || got.RetrySafe {
		t.Fatal(got)
	}
	if got := assembleReplyResult(r, target, [][]byte{raw, failed}); got.State != "invalid" {
		t.Fatal(got)
	}
	for _, edit := range []func(map[string]any){
		func(m map[string]any) { m["status"] = "completed" },
		func(m map[string]any) { delete(m, "result_type") },
		func(m map[string]any) { m["payload"].(map[string]any)["gateway_order_id"] = "other" },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["sdk_request_id"] = nil },
		func(m map[string]any) { m["payload"].(map[string]any)["counter_session_id"] = "other" },
	} {
		if got := assembleReplyResult(r, target, [][]byte{editBody(t, raw, edit)}); got.State != "invalid" {
			t.Fatal(got)
		}
	}
}

func TestReplyResultOCOriginalReceipts(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/oc_query_runtime_review_20261118/command_rejections.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		t.Fatal("fixture")
	}
	checked := 0
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, i := range x {
				visit(i)
			}
		case map[string]any:
			if body, ok := x["body"].(string); ok {
				o, e := parseReply([]byte(body))
				if e == nil && textField(o, "message_type") == "reply" && oneOf(textField(o, "action"), "order.submit", "order.cancel") {
					r, target, _ := resultTradeFixture(t)
					var route QueryRoute
					_ = json.Unmarshal(o["routing"], &route)
					r.Scope.Route = route
					c := &r.Original
					c.Action = textField(o, "action")
					c.MessageID = textField(o, "origin_message_id")
					c.RequestID = textField(o, "request_id")
					c.CorrelationID = textField(o, "correlation_id")
					c.IdempotencyKey = textField(o, "idempotency_key")
					var p, a rawObject
					_ = json.Unmarshal(o["payload"], &p)
					_ = json.Unmarshal(o["adapter_context"], &a)
					target.CounterSessionID = textField(p, "counter_session_id")
					target.TradeDate = textField(p, "trade_date")
					generation, _ := nullableInteger(a["connection_generation"])
					target.ConnectionGeneration = *generation
					if c.Action == "order.cancel" {
						c.Orders = nil
						c.Cancel = &CancelIntent{GatewayOrderID: textField(p, "gateway_order_id"), TradeDate: target.TradeDate, CancelID: "fixture-cancel"}
					} else {
						c.Orders[0].GatewayOrderID = textField(p, "gateway_order_id")
						c.Orders[0].ClientOrderID = textField(p, "client_order_id")
					}
					got := assembleReplyResult(r, target, [][]byte{[]byte(body)})
					if got.State != "failed" {
						t.Fatal(got)
					}
					checked++
				}
			}
			for _, i := range x {
				visit(i)
			}
		}
	}
	visit(doc)
	if checked < 6 {
		t.Fatal(checked)
	}
}
