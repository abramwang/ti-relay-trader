package redisstream

import "testing"

func TestSummarizeEntry(t *testing.T) {
	body := `{
		"protocol":"relay.stream.v1",
		"message_type":"event",
		"message_id":"event-1",
		"event_type":"order.event",
		"origin_message_id":"msg-1",
		"request_id":"req-1",
		"gateway_order_id":"gw-1",
		"routing":{"account_id":"00030484"},
		"payload":{"gateway_order_id":"gw-1","status":"working"}
	}`

	summary := SummarizeEntry("relay:prod:v1:huaxin:00030484:event", "1-0", map[string]any{"body": body})
	if summary.ParseError != "" {
		t.Fatalf("parse error = %q", summary.ParseError)
	}
	if summary.EventType != "order.event" {
		t.Fatalf("event type = %q", summary.EventType)
	}
	if summary.GatewayOrderID != "gw-1" {
		t.Fatalf("gateway order id = %q", summary.GatewayOrderID)
	}
	if summary.AccountID != "00030484" {
		t.Fatalf("account id = %q", summary.AccountID)
	}
	if len(summary.PayloadKeys) != 2 {
		t.Fatalf("payload keys = %#v", summary.PayloadKeys)
	}
}

func TestSummarizeEntryMissingBody(t *testing.T) {
	summary := SummarizeEntry("stream", "1-0", map[string]any{"other": "value"})
	if summary.ParseError == "" {
		t.Fatal("expected parse error")
	}
}
