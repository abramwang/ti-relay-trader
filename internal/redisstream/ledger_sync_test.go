package redisstream

import (
	"context"
	"strings"
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func TestProcessLedgerEntryArchivesReplyOnly(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:reply", "1-0", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-1",
			"origin_message_id":"msg-1",
			"request_id":"req-1",
			"correlation_id":"corr-1",
			"idempotency_key":"idem-1",
			"status":"accepted",
			"action":"order.submit",
			"code":"OK",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"payload":{"gateway_order_id":"gw-1"}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.raw) != 1 {
		t.Fatalf("raw writes = %d, want 1", len(writer.raw))
	}
	if writer.raw[0].Role != SuffixReply || writer.raw[0].Direction != "out" {
		t.Fatalf("raw role/direction = %s/%s", writer.raw[0].Role, writer.raw[0].Direction)
	}
	if len(writer.orders) != 0 || len(writer.fills) != 0 {
		t.Fatalf("reply should not write orders/fills")
	}
}

func TestProcessLedgerEntryWritesOrderEvent(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:event", "2-0", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-1",
			"event_type":"order.event",
			"origin_message_id":"msg-1",
			"request_id":"req-1",
			"correlation_id":"corr-1",
			"gateway_order_id":"gw-1",
			"produced_at":"2026-06-13T10:00:00.123Z",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"payload":{
				"gateway_order_id":"gw-1",
				"account_id":"00030484",
				"symbol":"600000",
				"exchange":"SH",
				"trade_side":"B",
				"business_type":"S",
				"offset_type":"C",
				"order_qty":100,
				"cum_filled_qty":0,
				"leaves_qty":100,
				"limit_price":9.54,
				"gateway_status":"working",
				"is_terminal":false
			},
			"adapter_context":{"order_status_code":2,"order_status_name":"queued"}
		}`,
	})

	if result.Archived != 1 || result.Accounts != 1 || result.Orders != 1 || result.OrderEvents != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.accounts) != 1 || writer.accounts[0].BrokerID != "huaxin" {
		t.Fatalf("accounts = %#v", writer.accounts)
	}
	if len(writer.orders) != 1 {
		t.Fatalf("orders = %#v", writer.orders)
	}
	order := writer.orders[0]
	if order.Status != trading.OrderStatusWorking || order.GatewayStatus != trading.GatewayStatusWorking {
		t.Fatalf("order status = %s/%s", order.Status, order.GatewayStatus)
	}
	if order.AdapterStatusCode != 2 || order.AdapterStatusName != "queued" {
		t.Fatalf("adapter status = %d/%s", order.AdapterStatusCode, order.AdapterStatusName)
	}
	if len(writer.orderEvents) != 1 || writer.orderEvents[0].stream.ID != "2-0" {
		t.Fatalf("order events = %#v", writer.orderEvents)
	}
}

func TestProcessLedgerEntrySkipsIncompleteOrderEvent(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:event", "3-0", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-2",
			"event_type":"order.event",
			"routing":{"account_id":"00030484"},
			"payload":{"gateway_order_id":"gw-2","account_id":"00030484","symbol":"600000"}
		}`,
	})

	if result.Archived != 1 || result.Skipped != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.raw) != 1 {
		t.Fatalf("raw writes = %d, want 1", len(writer.raw))
	}
	if len(writer.orders) != 0 || len(writer.orderEvents) != 0 {
		t.Fatalf("incomplete event should not write order ledger")
	}
	if !strings.Contains(strings.Join(result.SkipReasons, ";"), "missing exchange") {
		t.Fatalf("skip reasons = %#v", result.SkipReasons)
	}
}

func TestProcessLedgerEntryWritesFillEvent(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:event", "4-0", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"fill-event-1",
			"event_type":"fill.event",
			"origin_message_id":"msg-1",
			"request_id":"req-1",
			"gateway_order_id":"gw-1",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"payload":{
				"gateway_order_id":"gw-1",
				"fill_id":"fill-1",
				"order_id":1680001,
				"order_stream_id":"110018100000001",
				"account_id":"00030484",
				"symbol":"600000",
				"exchange":"SH",
				"price":9.54,
				"qty":100,
				"match_timestamp":1777103459957,
				"trade_side":"B"
			},
			"adapter_context":{"fee":1.23}
		}`,
	})

	if result.Archived != 1 || result.Accounts != 1 || result.Fills != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.fills) != 1 {
		t.Fatalf("fills = %#v", writer.fills)
	}
	fill := writer.fills[0].fill
	if fill.FillID != "fill-1" || fill.Fee != 1.23 {
		t.Fatalf("fill = %#v", fill)
	}
}

func TestProcessLedgerEntryArchivesParseError(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:event", "bad-0", map[string]any{
		"body": `{bad-json`,
	})

	if result.Archived != 1 || result.ParseErrors != 1 || result.Skipped != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.raw) != 1 || writer.raw[0].ParseError == "" {
		t.Fatalf("raw parse archive = %#v", writer.raw)
	}
}

type fakeLedgerWriter struct {
	accounts    []trading.Account
	orders      []trading.Order
	orderEvents []recordedOrderEvent
	fills       []recordedFill
	raw         []ledger.RawStreamMessage
}

type recordedOrderEvent struct {
	event  trading.OrderEvent
	stream ledger.StreamRef
	source ledger.SourceRef
}

type recordedFill struct {
	fill   trading.Fill
	stream ledger.StreamRef
	source ledger.SourceRef
}

func (writer *fakeLedgerWriter) UpsertAccount(_ context.Context, account trading.Account) error {
	writer.accounts = append(writer.accounts, account)
	return nil
}

func (writer *fakeLedgerWriter) UpsertOrder(_ context.Context, order trading.Order) error {
	writer.orders = append(writer.orders, order)
	return nil
}

func (writer *fakeLedgerWriter) AppendOrderEvent(_ context.Context, event trading.OrderEvent, stream ledger.StreamRef, source ledger.SourceRef) error {
	writer.orderEvents = append(writer.orderEvents, recordedOrderEvent{event: event, stream: stream, source: source})
	return nil
}

func (writer *fakeLedgerWriter) InsertFill(_ context.Context, fill trading.Fill, stream ledger.StreamRef, source ledger.SourceRef) error {
	writer.fills = append(writer.fills, recordedFill{fill: fill, stream: stream, source: source})
	return nil
}

func (writer *fakeLedgerWriter) ArchiveRawStreamMessage(_ context.Context, message ledger.RawStreamMessage) error {
	writer.raw = append(writer.raw, message)
	return nil
}
