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
