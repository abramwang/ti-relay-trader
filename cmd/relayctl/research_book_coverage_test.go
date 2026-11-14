package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/trading"
)

func TestResearchBookMultisets(t *testing.T) {
	row := researchOrderRow(trading.Order{AccountID: "a", TradeDate: "20261012", GatewayOrderID: "g", OrderQty: 100, LimitPrice: 1.234567})
	for _, test := range []struct {
		name string
		q, b []researchBookRow
		kind string
	}{
		{"empty", nil, nil, ""},
		{"match", []researchBookRow{row}, []researchBookRow{row}, ""},
		{"missing", []researchBookRow{row}, nil, "missing_in_book"},
		{"later book row", nil, []researchBookRow{row}, "book_only_not_in_this_query"},
		{"query duplicate", []researchBookRow{row, row}, []researchBookRow{row}, "duplicate_identity_query_2_book_1"},
		{"both duplicates", []researchBookRow{row, row}, []researchBookRow{row, row}, "duplicate_identity_query_2_book_2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := compareResearchBooks(test.q, test.b)
			if test.kind == "" {
				if r.State != "exact_projection_match" || r.QuerySHA != r.BookSHA {
					t.Fatal(r)
				}
				return
			}
			if r.State != "differences_require_review" || len(r.Differences) != 1 || r.Differences[0].Kind != test.kind {
				t.Fatal(r)
			}
		})
	}
	changed := researchOrderRow(trading.Order{AccountID: "a", TradeDate: "2026-10-12", GatewayOrderID: "g", OrderQty: 100, LimitPrice: 1.234568})
	r := compareResearchBooks([]researchBookRow{row}, []researchBookRow{changed})
	if len(r.Differences) != 1 || !reflect.DeepEqual(r.Differences[0].Fields, []string{"limit_price"}) {
		t.Fatal(r)
	}
	if researchRowsSHA([]researchBookRow{row, changed}) != researchRowsSHA([]researchBookRow{changed, row}) {
		t.Fatal("order-sensitive multiset")
	}
	if row.Key != researchBookKey("a", "2026-10-12", "g", "") || row.Key == researchBookKey("other", "2026-10-12", "g", "") {
		t.Fatal("scope")
	}
	for _, n := range []int{501, 2001, 10001} {
		var all []researchBookRow
		for i := 0; i < n; i++ {
			all = append(all, researchOrderRow(trading.Order{AccountID: "a", TradeDate: "2026-10-12", GatewayOrderID: fmt.Sprint(i), OrderQty: 100}))
		}
		if r := compareResearchBooks(all, all); r.Matched != n || len(r.Differences) != 0 {
			t.Fatal(n, r.Matched)
		}
	}
}

func TestResearchRequestScope(t *testing.T) {
	route := config.AccountRouteConfig{AccountID: "a", BrokerID: "huaxin", GatewayID: "g"}
	makeRaw := func() researchArchivedReply {
		return researchArchivedReply{Stream: "relay:prod:v1:huaxin:g:cmd.query", ReceivedAt: time.Date(2026, 10, 12, 7, 1, 1, 0, time.UTC), Body: []byte(`{"protocol":"relay.stream.v1","message_type":"command","message_id":"q","action":"order.list.query","sent_at":"2026-10-12T15:01:00+08:00","payload":{"account_id":"a"}}`)}
	}
	check := func(raw []researchArchivedReply) string {
		s, _, _ := reviewResearchRequest(raw, route, "production", "2026-10-12", "order.list.query", "q")
		return s
	}
	if check([]researchArchivedReply{makeRaw()}) != "account_day_unfiltered_request" {
		t.Fatal("valid command sent_at rejected")
	}
	if check(nil) != "request_scope_unproven" || check([]researchArchivedReply{makeRaw(), makeRaw()}) != "request_scope_unproven" {
		t.Fatal("missing/duplicate command")
	}
	for _, key := range []string{"symbol", "gateway_order_id", "limit", "cursor", "status", "date_from"} {
		raw := makeRaw()
		mutateEvidenceReply(t, &raw, func(e map[string]any) { e["payload"].(map[string]any)[key] = "1" })
		if check([]researchArchivedReply{raw}) != "request_scope_unproven" {
			t.Fatal("filtered request accepted", key)
		}
	}
	for name, mutate := range map[string]func(*researchArchivedReply){
		"stream":        func(r *researchArchivedReply) { r.Stream = "relay:test:v1:huaxin:g:cmd.query" },
		"archive error": func(r *researchArchivedReply) { r.ParseError = "bad" },
		"wrong message": func(r *researchArchivedReply) {
			mutateEvidenceReply(t, r, func(e map[string]any) { e["message_id"] = "x" })
		},
		"wrong payload account": func(r *researchArchivedReply) {
			mutateEvidenceReply(t, r, func(e map[string]any) { e["payload"].(map[string]any)["account_id"] = "x" })
		},
		"wrong route": func(r *researchArchivedReply) {
			mutateEvidenceReply(t, r, func(e map[string]any) { e["routing"] = map[string]any{"env": "test"} })
		},
		"wrong day": func(r *researchArchivedReply) {
			mutateEvidenceReply(t, r, func(e map[string]any) { e["sent_at"] = "2026-10-11T15:01:00+08:00" })
		},
		"missing sent time": func(r *researchArchivedReply) {
			mutateEvidenceReply(t, r, func(e map[string]any) { delete(e, "sent_at") })
		},
		"wrong trade date": func(r *researchArchivedReply) {
			mutateEvidenceReply(t, r, func(e map[string]any) { e["payload"].(map[string]any)["trade_date"] = "20261011" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := makeRaw()
			mutate(&raw)
			if check([]researchArchivedReply{raw}) != "request_scope_unproven" {
				t.Fatal(name)
			}
		})
	}
}

func TestResearchTradePageDiagnosticKeepsFailure(t *testing.T) {
	e := redisstream.EntryEnvelope{Protocol: "relay.stream.v1", MessageType: "reply", Action: redisstream.ActionFillList, ResultType: "fill_page", Status: "completed", MessageID: "reply", Routing: redisstream.Routing{AccountID: "a"}, ProducedAt: time.Date(2026, 10, 12, 7, 1, 0, 0, time.UTC)}
	good := map[string]any{"account_id": "a01", "gateway_order_id": "g", "fill_id": "f", "trade_date": "20261012", "symbol": "600000", "exchange": "SH", "trade_side": "B", "business_type": "S", "qty": 100, "price": 10, "matched_at": "2026-10-12T10:00:00+08:00"}
	bad := map[string]any{"gateway_order_id": "r", "fill_id": "cash", "symbol": "", "exchange": "SH", "trade_side": "R", "business_type": "E", "qty": 1, "price": 123456}
	transfer := map[string]any{"gateway_order_id": "r", "fill_id": "t", "trade_date": "20261012", "symbol": "600000", "exchange": "SH", "trade_side": "R", "business_type": "E", "qty": 100, "price": 0, "record_type": "etf_component_transfer", "is_transfer": true, "component_symbol": "600000", "component_exchange": "SH", "component_qty": 100}
	e.Payload, _ = json.Marshal(map[string]any{"items": []any{good, bad}, "component_transfers": []any{transfer}})
	before := string(e.Payload)
	_, fills, transfers, issues, err := inspectResearchTradePage(e)
	if err == nil || len(fills) != 1 || len(transfers) != 1 || len(issues) != 1 || issues[0].Index != 1 || issues[0].MessageID != "reply" || fills[0].AccountID != "a" || before != string(e.Payload) {
		t.Fatal(fills, transfers, issues, err)
	}
	if researchFillRow(fills[0]).Key == researchTransferRow(transfers[0]).Key {
		t.Fatal("ordinary/transfer identity mixed")
	}
	e.Payload, _ = json.Marshal(map[string]any{"items": []any{good}, "component_transfers": []any{transfer}})
	_, fills, transfers, issues, err = inspectResearchTradePage(e)
	if err != nil || len(issues) != 0 || len(fills) != 1 || len(transfers) != 1 {
		t.Fatal(err, issues)
	}
	// Time zones describe the same instant; monetary comparisons follow storage
	// precision, while fee and strategy enrichment are deliberately not assertions.
	f := fills[0]
	f.MatchedAt = f.MatchedAt.UTC()
	f.Fee = 123
	f.StrategyID = "later"
	if r := compareResearchBooks([]researchBookRow{researchFillRow(fills[0])}, []researchBookRow{researchFillRow(f)}); r.Matched != 1 {
		t.Fatal(r)
	}
}
