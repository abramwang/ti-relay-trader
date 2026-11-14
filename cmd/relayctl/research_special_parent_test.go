package main

import (
	"encoding/json"
	"testing"

	"ti-relay-trader/internal/trading"
)

func TestResearchSpecialParent(t *testing.T) {
	raw := map[string]any{"account_id": "a01", "trade_date": "20261012", "gateway_order_id": "g", "order_stream_id": "s", "order_id": 6, "symbol": "", "exchange": "SH", "trade_side": "R", "business_type": "E", "qty": 1, "price": 100.01, "basket_id": "etfarb#510310"}
	order := researchOrderRow(trading.Order{AccountID: "a", TradeDate: "20261012", GatewayOrderID: "g", OrderStreamID: "s", OrderID: 6, Symbol: "510310", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSide("R"), BusinessType: trading.BusinessType("E"), Status: trading.OrderStatus("filled"), IsTerminal: true})
	makeIssue := func() researchDecodeIssue {
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		return researchDecodeIssue{Collection: "items", Raw: b}
	}
	issue := makeIssue()
	if p := reviewResearchSpecialParent(issue, "a", "2026-10-12", []researchBookRow{order}); p.State != "unique_redemption_parent_identity" {
		t.Fatal(p)
	}
	for _, rows := range [][]researchBookRow{nil, {order, order}} {
		if p := reviewResearchSpecialParent(issue, "a", "2026-10-12", rows); p.State != "unproven" {
			t.Fatal(p)
		}
	}
	for field, bad := range map[string]any{"account_id": "other", "trade_date": "20261013", "gateway_order_id": "wrong", "order_stream_id": "wrong", "order_id": 7, "symbol": "510310", "exchange": "SZ", "trade_side": "S", "business_type": "S", "qty": 900000, "price": 0, "basket_id": "etfarb#wrong"} {
		t.Run(field, func(t *testing.T) {
			old := raw[field]
			raw[field] = bad
			defer func() { raw[field] = old }()
			if p := reviewResearchSpecialParent(makeIssue(), "a", "2026-10-12", []researchBookRow{order}); p.State != "unproven" {
				t.Fatal(p)
			}
		})
	}
	if string(issue.Raw) != string(makeIssue().Raw) {
		t.Fatal("raw issue changed")
	}
	for field, bad := range map[string]any{"status": trading.OrderStatus("working"), "is_terminal": false} {
		fields := make(map[string]any, len(order.Fields))
		for k, v := range order.Fields {
			fields[k] = v
		}
		fields[field] = bad
		if p := reviewResearchSpecialParent(issue, "a", "2026-10-12", []researchBookRow{{Key: order.Key, Fields: fields}}); p.State != "unproven" {
			t.Fatal(p)
		}
	}
}
