package redisstream

import (
	"context"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
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
					"created_at":"2026-06-13T09:30:00+08:00",
					"update_time":"2026-06-13 10:00:01"
				}]}
			}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Orders != 1 || result.Fills != 1 {
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
	if len(writer.fills) != 1 {
		t.Fatalf("summary fills = %#v", writer.fills)
	}
	fill := writer.fills[0].fill
	if fill.FillID != "relay-summary:gw-page-1" || fill.Qty != 100 || fill.Price != 9.54 {
		t.Fatalf("summary fill = %#v", fill)
	}
	if got := fill.MatchedAt.In(timeutil.Location()).Format("15:04:05"); got != "09:30:00" {
		t.Fatalf("summary fill matched_at = %s, want order created_at", got)
	}
	if fill.AdapterContext["relay_synthesis_time_source"] != "created_at" {
		t.Fatalf("summary fill time source = %#v", fill.AdapterContext)
	}
	if fill.AdapterContext["relay_synthesized"] != true || fill.AdapterContext["relay_synthesis_source"] != "order_page" {
		t.Fatalf("summary fill context = %#v", fill.AdapterContext)
	}
	if writer.fills[0].stream.ID != "1-3:summary:gw-page-1" {
		t.Fatalf("summary fill stream = %#v", writer.fills[0].stream)
	}
}

func TestProcessLedgerEntryNormalizesRoutedOrderPageAccount(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:314000046830:reply", "1-31", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-order-page-routed-account",
			"action":"order.list.query",
			"result_type":"order_page",
			"status":"completed",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"314000046830","account_id":"314000046830"},
			"produced_at":"2026-06-15T13:00:00+08:00",
			"payload":{"items":[{
				"gateway_order_id":"external-huaxin-31400004683001-12001A180002899",
				"order_id":884,
				"order_stream_id":"12001A180002899",
				"account_id":"31400004683001",
				"symbol":"000767",
				"exchange":"SZ",
				"trade_side":"B",
				"business_type":"S",
				"order_qty":200,
				"cum_filled_qty":200,
				"leaves_qty":0,
				"limit_price":4.8,
				"gateway_status":"filled"
			}]}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Orders != 1 || result.Fills != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orders) != 1 {
		t.Fatalf("orders = %#v", writer.orders)
	}
	order := writer.orders[0]
	if order.AccountID != "314000046830" {
		t.Fatalf("order account_id = %q", order.AccountID)
	}
	if order.AdapterContext["relay_raw_account_id"] != "31400004683001" {
		t.Fatalf("order context = %#v", order.AdapterContext)
	}
	fill := writer.fills[0].fill
	if fill.AccountID != "314000046830" || fill.AdapterContext["relay_raw_account_id"] != "31400004683001" {
		t.Fatalf("summary fill = %#v", fill)
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
			},{
				"fill_id":"fill-page-2",
				"gateway_order_id":"gw-page-2",
				"order_id":1680002,
				"order_stream_id":"110018100000002",
				"account_id":"00030484",
				"symbol":"600001",
				"exchange":"SH",
				"price":10.01,
				"qty":200,
				"trade_side":"S",
				"matched_at":"2026-06-13T10:00:05+08:00"
			}]}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Accounts != 1 || result.Fills != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.fills) != 2 {
		t.Fatalf("fills = %#v", writer.fills)
	}
	fill := writer.fills[0].fill
	if fill.FillID != "fill-page-1" || fill.GatewayOrderID != "gw-page-1" || fill.MatchedAt.IsZero() {
		t.Fatalf("fill = %#v", fill)
	}
	if fill.TradeDate != "2026-06-13" {
		t.Fatalf("fill trade_date = %q", fill.TradeDate)
	}
	if got := fill.MatchedAt.In(timeutil.Location()).Format(time.RFC3339); got != "2026-06-13T10:00:02+08:00" {
		t.Fatalf("fill matched_at = %s, want Asia/Shanghai local parse", got)
	}
	if writer.fills[0].stream.ID != "1-4:fill:fill-page-1" || writer.fills[0].source.OriginMessageID != "msg-fill-query-1" {
		t.Fatalf("fill source = %#v", writer.fills[0])
	}
	if writer.fills[1].stream.ID != "1-4:fill:fill-page-2" {
		t.Fatalf("second fill source = %#v", writer.fills[1])
	}
}

func TestProcessLedgerEntryNormalizesRoutedFillPageAccount(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:prod:v1:huaxin:314000046830:reply", "1-41", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-fill-page-routed-account",
			"action":"fill.list.query",
			"result_type":"fill_page",
			"status":"completed",
			"routing":{"env":"prod","broker_id":"huaxin","gateway_id":"314000046830","account_id":"314000046830"},
			"produced_at":"2026-06-15T13:00:01+08:00",
			"payload":{"items":[{
				"fill_id":"fill-page-routed-1",
				"gateway_order_id":"external-huaxin-31400004683001-12001A180002899",
				"order_id":884,
				"order_stream_id":"12001A180002899",
				"account_id":"31400004683001",
				"symbol":"000767",
				"exchange":"SZ",
				"price":4.8,
				"qty":200,
				"trade_side":"B",
				"matched_at":"2026-06-15 13:00:01"
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
	if fill.AccountID != "314000046830" {
		t.Fatalf("fill account_id = %q", fill.AccountID)
	}
	if fill.AdapterContext["relay_raw_account_id"] != "31400004683001" {
		t.Fatalf("fill context = %#v", fill.AdapterContext)
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

	if result.Archived != 1 || result.Orders != 1 || result.OrderEvents != 1 || result.Fills != 1 {
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
	if order.RejectCode != "" || order.RejectMessage != "" {
		t.Fatalf("accepted order should not expose normal broker text as reject info: %s/%q", order.RejectCode, order.RejectMessage)
	}
	if _, ok := order.AdapterContext["relay_error_message"]; ok {
		t.Fatalf("accepted order should not expose relay_error_message: %#v", order.AdapterContext)
	}
	if len(writer.fills) != 1 {
		t.Fatalf("summary fills = %#v", writer.fills)
	}
	fill := writer.fills[0].fill
	if fill.FillID != "relay-summary:gw-filled-qty" || fill.Qty != 100 || fill.Price != 9.54 {
		t.Fatalf("summary fill = %#v", fill)
	}
	if fill.AdapterContext["relay_synthesized"] != true || fill.AdapterContext["relay_synthesis_source"] != "order.event" {
		t.Fatalf("summary fill context = %#v", fill.AdapterContext)
	}
}

func TestProcessLedgerEntrySynthesizesOnlyMissingFillQty(t *testing.T) {
	writer := &fakeLedgerWriter{
		fills: []recordedFill{{
			fill: trading.Fill{
				FillID:         "fill-real-1",
				AccountID:      "00030484",
				GatewayOrderID: "gw-partial-ledger",
				Symbol:         "000333",
				Exchange:       trading.ExchangeSZ,
				TradeSide:      trading.TradeSideBuy,
				Price:          83.57,
				Qty:            500,
			},
		}},
	}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:sim:00030484:event", "2-1", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-filled-partial-ledger",
			"event_type":"order.event",
			"routing":{"env":"test","broker_id":"sim","gateway_id":"00030484","account_id":"00030484"},
			"payload":{
				"gateway_order_id":"gw-partial-ledger",
				"account_id":"00030484",
				"symbol":"000333",
				"exchange":"SZ",
				"trade_side":"B",
				"business_type":"S",
				"offset_type":"C",
				"order_qty":1000,
				"cum_filled_qty":1000,
				"leaves_qty":0,
				"limit_price":84.89,
				"gateway_status":"filled",
				"is_terminal":true
			}
		}`,
	})

	if result.Fills != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.fills) != 2 {
		t.Fatalf("fills = %#v", writer.fills)
	}
	fill := writer.fills[1].fill
	if fill.FillID != "relay-summary:gw-partial-ledger" || fill.Qty != 500 || fill.Price != 84.89 {
		t.Fatalf("summary fill = %#v", fill)
	}
}

func TestProcessLedgerEntrySkipsSummaryFillWhenFilledQtyAlreadyCovered(t *testing.T) {
	writer := &fakeLedgerWriter{
		fills: []recordedFill{{
			fill: trading.Fill{
				FillID:         "fill-real-1",
				AccountID:      "00030484",
				GatewayOrderID: "gw-covered",
				Symbol:         "600000",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideBuy,
				Price:          9.54,
				Qty:            100,
			},
		}},
	}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:sim:00030484:event", "2-1", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-filled-covered",
			"event_type":"order.event",
			"routing":{"env":"test","broker_id":"sim","gateway_id":"00030484","account_id":"00030484"},
			"payload":{
				"gateway_order_id":"gw-covered",
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
				"is_terminal":true
			}
		}`,
	})

	if result.Fills != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.fills) != 1 {
		t.Fatalf("fills = %#v", writer.fills)
	}
}

func TestProcessLedgerEntryKeepsCounterRejectMessage(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:sim:00030484:event", "2-2", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"event",
			"message_id":"event-rejected",
			"event_type":"order.event",
			"routing":{"env":"test","broker_id":"sim","gateway_id":"00030484","account_id":"00030484"},
			"payload":{
				"gateway_order_id":"gw-rejected",
				"account_id":"00030484",
				"symbol":"600000",
				"exchange":"SH",
				"trade_side":"B",
				"business_type":"S",
				"offset_type":"C",
				"order_qty":100,
				"cum_filled_qty":0,
				"leaves_qty":0,
				"limit_price":99.99,
				"gateway_status":"rejected",
				"reject_code":"COUNTER_REJECT",
				"is_terminal":true
			},
			"adapter_context":{"error_text":"VIP:超过涨停价","broker_status_text":"废单"}
		}`,
	})

	if result.Archived != 1 || result.Orders != 1 || result.OrderEvents != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orders) != 1 {
		t.Fatalf("orders = %#v", writer.orders)
	}
	order := writer.orders[0]
	if order.RejectCode != "COUNTER_REJECT" || order.RejectMessage != "VIP:超过涨停价" {
		t.Fatalf("reject = %s/%q", order.RejectCode, order.RejectMessage)
	}
	if order.AdapterContext["relay_error_message"] != "VIP:超过涨停价" {
		t.Fatalf("adapter context = %#v", order.AdapterContext)
	}
}

func TestProcessLedgerEntryUpdatesDraftForRejectedOrderReply(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:sim:00030484:reply", "2-3", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-rejected",
			"origin_message_id":"msg-submit-1",
			"request_id":"req-submit-1",
			"action":"order.submit",
			"status":"rejected",
			"code":"COUNTER_REJECT",
			"message":"柜台拒绝",
			"routing":{"env":"test","broker_id":"sim","gateway_id":"00030484","account_id":"00030484"},
			"payload":{"gateway_order_id":"gw-reply-rejected","message":"VIP:无效的报单类别"}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Orders != 1 || result.OrderEvents != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orderUpdates) != 1 || len(writer.orderEvents) != 1 {
		t.Fatalf("order updates/events = %#v / %#v", writer.orderUpdates, writer.orderEvents)
	}
	event := writer.orderUpdates[0]
	if event.Status != trading.OrderStatusRejected || event.GatewayStatus != trading.GatewayStatusRejected || !event.IsTerminal {
		t.Fatalf("event = %#v", event)
	}
	if event.Order.RejectCode != "COUNTER_REJECT" || event.Order.RejectMessage != "VIP:无效的报单类别" {
		t.Fatalf("reject = %s/%q", event.Order.RejectCode, event.Order.RejectMessage)
	}
}

func TestProcessLedgerEntryDoesNotRejectOrderForRejectedCancelReply(t *testing.T) {
	writer := &fakeLedgerWriter{}
	result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:sim:00030484:reply", "2-4", map[string]any{
		"body": `{
			"protocol":"relay.stream.v1",
			"message_type":"reply",
			"message_id":"reply-cancel-rejected",
			"origin_message_id":"msg-cancel-1",
			"request_id":"req-cancel-1",
			"action":"order.cancel",
			"status":"rejected",
			"code":"CANCEL_REJECT",
			"message":"撤单失败",
			"routing":{"env":"test","broker_id":"sim","gateway_id":"00030484","account_id":"00030484"},
			"payload":{"gateway_order_id":"gw-cancel-rejected","message":"柜台未找到可撤委托"}
		}`,
	})

	if result.Archived != 1 || result.Replies != 1 || result.Orders != 0 || result.OrderEvents != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.orderUpdates) != 0 || len(writer.orderEvents) != 0 {
		t.Fatalf("cancel rejection should not change order state: %#v / %#v", writer.orderUpdates, writer.orderEvents)
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

func (writer *fakeLedgerWriter) ListFills(_ context.Context, query trading.FillQuery) ([]trading.Fill, error) {
	fills := make([]trading.Fill, 0, len(writer.fills))
	for _, recorded := range writer.fills {
		fill := recorded.fill
		if query.AccountID != "" && fill.AccountID != query.AccountID {
			continue
		}
		if query.GatewayOrderID != "" && fill.GatewayOrderID != query.GatewayOrderID {
			continue
		}
		fills = append(fills, fill)
	}
	return fills, nil
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
