package redisstream

import (
	"encoding/json"
	"testing"
)

func TestDecodePositionQueryRecordsReadOnly(t *testing.T) {
	e := EntryEnvelope{Protocol: "relay.stream.v1", MessageType: "reply", Action: ActionAccountPositions, ResultType: "position_page", Status: "completed", Routing: Routing{AccountID: "a"}, Payload: json.RawMessage(`{"items":[{"account_id":"a01","symbol":"600000","exchange":"SH","quantity":100,"sellable_qty":0}]}`)}
	before := string(e.Payload)
	ps, err := DecodePositionQueryRecords(e)
	if err != nil || len(ps) != 1 || ps[0].AccountID != "a" || ps[0].Quantity != 100 || string(e.Payload) != before {
		t.Fatal(ps, err)
	}
	for _, mutate := range []func(*EntryEnvelope){func(e *EntryEnvelope) { e.Status = "failed" }, func(e *EntryEnvelope) { e.Action = ActionOrderList }, func(e *EntryEnvelope) { e.ResultType = "fill_page" }, func(e *EntryEnvelope) { e.MessageType = "event" }, func(e *EntryEnvelope) { e.Payload = json.RawMessage(`{"items":[{}]}`) }} {
		bad := e
		mutate(&bad)
		if _, err := DecodePositionQueryRecords(bad); err == nil {
			t.Fatal("invalid page")
		}
	}
}

func TestDecodeTradeQueryRecordsReadOnly(t *testing.T) {
	e := EntryEnvelope{Protocol: "relay.stream.v1", MessageType: "reply", Status: "completed", Action: ActionOrderList, ResultType: "order_page", Payload: json.RawMessage(`{"items":[]}`)}
	o, f, tr, err := DecodeTradeQueryRecords(e)
	if err != nil || len(o)+len(f)+len(tr) != 0 {
		t.Fatal(err)
	}
	for _, bad := range []EntryEnvelope{
		{Protocol: "bad", MessageType: "reply", Status: "completed", Action: ActionOrderList, ResultType: "order_page"},
		{Protocol: "relay.stream.v1", MessageType: "reply", Status: "failed", Action: ActionOrderList, ResultType: "order_page"},
		{Protocol: "relay.stream.v1", MessageType: "command", Status: "completed", Action: ActionOrderList, ResultType: "order_page"},
		{Protocol: "relay.stream.v1", MessageType: "reply", Status: "completed", Action: ActionFillList, ResultType: "order_page"},
	} {
		if _, _, _, err := DecodeTradeQueryRecords(bad); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
}

func TestDecodeTradeEventRecordReadOnly(t *testing.T) {
	e := EntryEnvelope{Protocol: "relay.stream.v1", MessageType: "event", EventType: "order.event", Payload: json.RawMessage(`{"account_id":"a","gateway_order_id":"g","symbol":"600000","exchange":"SH","trade_side":"B","business_type":"S","order_qty":100,"limit_price":10,"status":"rejected"}`)}
	before := string(e.Payload)
	o, f, err := DecodeTradeEventRecord(e)
	if err != nil || o == nil || f != nil || o.OrderQty != 100 || string(e.Payload) != before {
		t.Fatal(o, f, err)
	}
	for _, bad := range []EntryEnvelope{
		{Protocol: "bad", MessageType: "event", EventType: "order.event", Payload: e.Payload},
		{Protocol: "relay.stream.v1", MessageType: "reply", EventType: "order.event", Payload: e.Payload},
		{Protocol: "relay.stream.v1", MessageType: "event", EventType: "transfer.event", Payload: e.Payload},
		{Protocol: "relay.stream.v1", MessageType: "event", EventType: "order.event", Payload: json.RawMessage(`{"account_id":"a","gateway_order_id":"g","status":"filled"}`)},
		{Protocol: "relay.stream.v1", MessageType: "event", EventType: "fill.event", Payload: json.RawMessage(`{"account_id":"a"}`)},
	} {
		if _, _, err := DecodeTradeEventRecord(bad); err == nil {
			t.Fatal("invalid or partial event accepted")
		}
	}
}
