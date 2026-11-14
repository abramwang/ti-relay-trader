package redisstream

import (
	"encoding/json"
	"testing"
)

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
