package redisstream

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestProcessLedgerEntryWritesAssetReply(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:reply", "1-1", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-asset-1",
			"action":"account.asset.query",
			"result_type":"asset_page",
			"status":"completed",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"produced_at":"2026-06-13T10:00:00Z",
			"payload":{"account":{"account_id":"00030484","cash_available":900000,"cash_total":1000000,"net_asset":1200000,"market_value":200000}}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Assets != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.assets) != 1 || writer.assets[0].asset.CashAvailable != 900000 {
		t.Fatalf("assets = %#v", writer.assets)
	}
}

func TestProcessLedgerEntryWritesPositionReply(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:reply", "1-2", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-position-1",
			"action":"account.positions.query",
			"result_type":"position_page",
			"status":"partial",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"produced_at":"2026-06-13T10:00:01Z",
			"payload":{"items":[{"account_id":"00030484","symbol":"600000","exchange":"SH","quantity":100,"sellable_qty":80,"avg_cost":9.54,"market_value":954,"shareholder_id":"A00030484"}]}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Positions != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.positions) != 1 || writer.positions[0].position.Symbol != "600000" {
		t.Fatalf("positions = %#v", writer.positions)
	}
}

func TestProcessLedgerEntryWritesOrderPageReply(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:reply", "1-3", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-order-page-1",
			"action":"order.list.query",
			"result_type":"order_page",
			"status":"completed",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"produced_at":"2026-06-13T10:00:02Z",
			"payload":{"items":[{
				"gateway_order_id":"gw-page-1",
				"client_order_id":"cli-page-1",
				"order_id":1680001,
				"order_stream_id":"110018100000001",
				"account_id":"00030484",
				"symbol":"600000",
				"exchange":"SH",
				"trade_side":"B",
				"business_type":"S",
				"offset_type":"C",
				"order_qty":100,
				"cum_filled_qty":100,
				"leaves_qty":0,
				"limit_price":9.54,
				"gateway_status":"filled",
				"adapter_status":"全部成交",
				"adapter_status_code":8,
				"update_time":"2026-06-13 10:00:01"
			}]}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Orders != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orders) != 1 {
		t.Fatalf("orders = %#v", writer.orders)
	}
	order := writer.orders[0]
	if order.GatewayOrderID != "gw-page-1" || order.Status != trading.OrderStatusFilled || order.AdapterStatusName != "全部成交" {
		t.Fatalf("order = %#v", order)
	}
	if order.LastUpdatedAt.IsZero() {
		t.Fatalf("last_updated_at was not parsed: %#v", order)
	}
}

func TestProcessLedgerEntryWritesFillPageReply(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:reply", "1-4", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-fill-page-1",
			"action":"fill.list.query",
			"result_type":"fill_page",
			"status":"partial",
			"origin_message_id":"msg-fill-query-1",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"00030484","account_id":"00030484"},
			"produced_at":"2026-06-13T10:00:03Z",
			"payload":{"items":[{
				"fill_id":"fill-page-1",
				"gateway_order_id":"gw-page-1",
				"order_id":1680001,
				"order_stream_id":"110018100000001",
				"account_id":"00030484",
				"symbol":"600000",
				"exchange":"SH",
				"price":9.54,
				"qty":100,
				"trade_side":"B",
				"matched_at":"2026-06-13 10:00:02"
			}]}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Fills != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.fills) != 1 {
		t.Fatalf("fills = %#v", writer.fills)
	}
	fill := writer.fills[0].fill
	if fill.FillID != "fill-page-1" || fill.GatewayOrderID != "gw-page-1" || fill.MatchedAt.IsZero() {
		t.Fatalf("fill = %#v", fill)
	}
	if writer.fills[0].stream.ID != "1-4" || writer.fills[0].source.OriginMessageID != "msg-fill-query-1" {
		t.Fatalf("fill source = %#v", writer.fills[0])
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

func TestProcessLedgerEntryInfersFilledOrderEventFromQuantities(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:sim:00030484:event", "2-1", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-filled-qty",
			"event_type":"order.event",
			"routing":{"env":"test","broker_id":"sim","gateway_id":"00030484","account_id":"00030484"},
			"payload":{
				"gateway_order_id":"gw-filled-qty",
				"account_id":"00030484",
				"symbol":"600000",
				"exchange":"SH",
				"trade_side":"B",
				"business_type":"S",
				"offset_type":"C",
				"order_qty":100,
				"cum_filled_qty":100,
				"leaves_qty":0,
				"limit_price":9.54,
				"status":"accepted",
				"gateway_status":"accepted",
				"adapter_status":"unAccept",
				"adapter_status_code":0,
				"is_terminal":false
			},
			"adapter_context":{"order_status_code":0,"order_status_name":"unAccept","broker_status_text":"VIP:正确"}
		}`,
	})

	if result.Archived != 1 || result.Orders != 1 || result.OrderEvents != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orders) != 1 {
		t.Fatalf("orders = %#v", writer.orders)
	}
	order := writer.orders[0]
	if order.Status != trading.OrderStatusFilled || order.GatewayStatus != trading.GatewayStatusFilled || !order.IsTerminal {
		t.Fatalf("order state = %s/%s terminal=%v", order.Status, order.GatewayStatus, order.IsTerminal)
	}
	if order.AdapterStatusName != "unAccept" {
		t.Fatalf("adapter status should preserve raw broker name: %#v", order)
	}
}

func TestProcessLedgerEntryUpdatesDraftForIncompleteOrderEvent(t *testing.T) {
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

	if result.Archived != 1 || result.Orders != 1 || result.OrderEvents != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.raw) != 1 {
		t.Fatalf("raw writes = %d, want 1", len(writer.raw))
	}
	if len(writer.orderUpdates) != 1 || len(writer.orderEvents) != 1 {
		t.Fatalf("incomplete event should update draft and append event: updates=%#v events=%#v", writer.orderUpdates, writer.orderEvents)
	}
}

func TestProcessLedgerEntrySkipsUnidentifiableOrderEvent(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:00030484:event", "3-1", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-3",
			"event_type":"order.event",
			"payload":{"symbol":"600000"}
		}`,
	})

	if result.Archived != 1 || result.Skipped != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orderUpdates) != 0 || len(writer.orderEvents) != 0 {
		t.Fatalf("unidentifiable event should not write order ledger")
	}
	if !strings.Contains(strings.Join(result.SkipReasons, ";"), "missing account_id") {
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
	accounts     []trading.Account
	orders       []trading.Order
	orderUpdates []trading.OrderEvent
	orderEvents  []recordedOrderEvent
	fills        []recordedFill
	assets       []recordedAsset
	positions    []recordedPosition
	raw          []ledger.RawStreamMessage
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

type recordedAsset struct {
	asset        trading.Asset
	snapshotType string
	source       string
}

type recordedPosition struct {
	position trading.Position
	source   string
}

func (writer *fakeLedgerWriter) UpsertAccount(_ context.Context, account trading.Account) error {
	writer.accounts = append(writer.accounts, account)
	return nil
}

func (writer *fakeLedgerWriter) UpsertOrder(_ context.Context, order trading.Order) error {
	writer.orders = append(writer.orders, order)
	return nil
}

func (writer *fakeLedgerWriter) UpdateOrderStatus(_ context.Context, event trading.OrderEvent) error {
	writer.orderUpdates = append(writer.orderUpdates, event)
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

func (writer *fakeLedgerWriter) UpsertAssetSnapshot(_ context.Context, asset trading.Asset, snapshotType string, source string, _ any, _ time.Time) error {
	writer.assets = append(writer.assets, recordedAsset{asset: asset, snapshotType: snapshotType, source: source})
	return nil
}

func (writer *fakeLedgerWriter) UpsertPosition(_ context.Context, position trading.Position, source string, _ any, _ time.Time) error {
	writer.positions = append(writer.positions, recordedPosition{position: position, source: source})
	return nil
}

func (writer *fakeLedgerWriter) ArchiveRawStreamMessage(_ context.Context, message ledger.RawStreamMessage) error {
	writer.raw = append(writer.raw, message)
	return nil
}
