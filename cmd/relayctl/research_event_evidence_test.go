package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/trading"
)

func researchEventFixture(t *testing.T, kind string) (researchArchivedEvent, researchBookDifference, config.AccountRouteConfig) {
	t.Helper()
	route := config.AccountRouteConfig{AccountID: "a", BrokerID: "huaxin", GatewayID: "g"}
	p := map[string]any{"account_id": "a01", "gateway_order_id": "g1", "trade_date": "20261012", "symbol": "600000", "exchange": "SH", "trade_side": "B", "business_type": "S", "qty": 100, "price": 10, "fill_id": "f1", "matched_at": "2026-10-12T10:00:00+08:00"}
	eventType := "fill.event"
	var row researchBookRow
	if kind == "orders" {
		eventType = "order.event"
		p["order_qty"], p["limit_price"], p["status"], p["is_terminal"] = 100, 10, "rejected", true
		row = researchOrderRow(trading.Order{AccountID: "a", GatewayOrderID: "g1", TradeDate: "20261012", Symbol: "600000", Exchange: "SH", TradeSide: "B", BusinessType: "S", OrderQty: 100, LimitPrice: 10, Status: "rejected", IsTerminal: true})
	} else {
		at, _ := time.Parse(time.RFC3339, "2026-10-12T10:00:00+08:00")
		row = researchFillRow(trading.Fill{AccountID: "a", GatewayOrderID: "g1", FillID: "f1", TradeDate: "20261012", Symbol: "600000", Exchange: "SH", TradeSide: "B", BusinessType: "S", Qty: 100, Price: 10, MatchedAt: at})
	}
	body, err := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "event", "event_type": eventType, "message_id": "ev1", "produced_at": "2026-10-12T10:00:01+08:00", "routing": map[string]any{"env": "prod", "broker_id": "huaxin", "gateway_id": "g", "account_id": "a"}, "payload": p})
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, "2026-10-12T10:00:02+08:00")
	raw := researchArchivedEvent{researchArchivedReply: researchArchivedReply{Body: body, Stream: "relay:prod:v1:huaxin:g:event", ReceivedAt: at}, StreamID: "1791770401000-0", GatewayOrderID: "g1"}
	return raw, researchBookDifference{Key: row.Key, Kind: "book_only_not_in_this_query", Book: row.Fields}, route
}

func TestResearchEventEvidence(t *testing.T) {
	for _, kind := range []string{"orders", "fills"} {
		t.Run(kind, func(t *testing.T) {
			raw, diff, route := researchEventFixture(t, kind)
			before := string(raw.Body)
			p, fill := reviewResearchEvents(diff, kind, []researchArchivedEvent{raw}, "production", "2026-10-12", route)
			if p.State != "matching_full_event_found" || len(p.Matches) != 1 || p.Matches[0].SHA == "" || (kind == "fills" && fill == nil) || string(raw.Body) != before {
				t.Fatalf("%+v", p)
			}
			p, _ = reviewResearchEvents(diff, kind, []researchArchivedEvent{raw, raw}, "production", "2026-10-12", route)
			if p.State != "matching_event_with_archive_warnings" {
				t.Fatal(p)
			}
			p, _ = reviewResearchEvents(diff, kind, nil, "production", "2026-10-12", route)
			if p.State != "matching_event_not_found" {
				t.Fatal(p)
			}
		})
	}
	for _, field := range []string{"price", "qty", "matched_at", "trade_date", "account_id", "symbol"} {
		t.Run(field, func(t *testing.T) {
			raw, diff, route := researchEventFixture(t, "fills")
			mutateEvidenceReply(t, &raw.researchArchivedReply, func(e map[string]any) {
				v := map[string]any{"price": 11, "qty": 200, "matched_at": "2026-10-12T09:00:00+08:00", "trade_date": "20261011", "account_id": "other", "symbol": "000001"}
				e["payload"].(map[string]any)[field] = v[field]
			})
			p, _ := reviewResearchEvents(diff, "fills", []researchArchivedEvent{raw}, "production", "2026-10-12", route)
			if len(p.Matches) != 0 {
				t.Fatal(p)
			}
		})
	}
	for name, mutate := range map[string]func(*researchArchivedEvent){
		"stream":       func(r *researchArchivedEvent) { r.Stream = "relay:test:v1:huaxin:g:event" },
		"stream ID":    func(r *researchArchivedEvent) { r.StreamID = "" },
		"gateway":      func(r *researchArchivedEvent) { r.GatewayOrderID = "other" },
		"parse error":  func(r *researchArchivedEvent) { r.ParseError = "invalid" },
		"received day": func(r *researchArchivedEvent) { r.ReceivedAt = r.ReceivedAt.AddDate(0, 0, 1) },
		"wire route": func(r *researchArchivedEvent) {
			mutateEvidenceReply(t, &r.researchArchivedReply, func(e map[string]any) { e["routing"].(map[string]any)["account_id"] = "other" })
		},
		"missing producer time": func(r *researchArchivedEvent) {
			mutateEvidenceReply(t, &r.researchArchivedReply, func(e map[string]any) { delete(e, "produced_at") })
		},
		"missing match time": func(r *researchArchivedEvent) {
			mutateEvidenceReply(t, &r.researchArchivedReply, func(e map[string]any) { delete(e["payload"].(map[string]any), "matched_at") })
		},
		"missing identity": func(r *researchArchivedEvent) {
			mutateEvidenceReply(t, &r.researchArchivedReply, func(e map[string]any) { delete(e, "message_id") })
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, diff, route := researchEventFixture(t, "fills")
			mutate(&raw)
			p, _ := reviewResearchEvents(diff, "fills", []researchArchivedEvent{raw}, "production", "2026-10-12", route)
			if len(p.Matches) != 0 {
				t.Fatal(p)
			}
		})
	}
	raw, diff, route := researchEventFixture(t, "orders")
	earlier := raw
	mutateEvidenceReply(t, &earlier.researchArchivedReply, func(e map[string]any) {
		e["message_id"] = "earlier"
		p := e["payload"].(map[string]any)
		p["status"], p["is_terminal"] = "working", false
	})
	p, _ := reviewResearchEvents(diff, "orders", []researchArchivedEvent{earlier, raw}, "prod", "2026-10-12", route)
	if p.State != "matching_full_event_found" || len(p.Rejected) != 1 {
		t.Fatal(p)
	}
	diff.Key = researchBookKey("other", "2026-10-12", "g1", "")
	p, _ = reviewResearchEvents(diff, "orders", []researchArchivedEvent{raw}, "prod", "2026-10-12", route)
	if p.State != "invalid_book_identity" {
		t.Fatal(p)
	}
	if err := runResearchQueryEvidence([]string{"--event-coverage", "--config", "must-not-read", "--account", "a", "--version", "v", "--output", "must-not-write"}); err == nil || !strings.Contains(err.Error(), "requires book-coverage") {
		t.Fatal(err)
	}
}

func researchRepoTimingFixture(t *testing.T) (researchQueryDay, []trading.Fill) {
	t.Helper()
	at, _ := time.Parse(time.RFC3339, "2026-10-26T15:01:01+08:00")
	a := trading.Asset{AccountID: "a", CashTotal: 14736745, CashAvailable: 14736745, UpdatedAt: at}
	marshal := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	d := researchQueryDay{Date: "2026-10-26", SnapshotTimes: []json.RawMessage{
		marshal(map[string]any{"snapshot_type": "broker_close", "captured_at": at.Add(14 * time.Second), "asset": a}),
		marshal(map[string]any{"snapshot_type": "close", "captured_at": at, "asset": a, "reverse_repo": map[string]any{"principal_receivable": 0}}),
	}, Attempts: []researchQueryAttempt{{LinkedToClose: true, Queries: []researchQueryProof{{Step: "asset", State: "terminal_archive_consistent", AssetReply: marshal(map[string]any{"routing": map[string]any{"account_id": "a"}, "produced_at": at, "payload": map[string]any{"account": a}})}}}}}
	return d, []trading.Fill{{AccountID: "a", GatewayOrderID: "g", FillID: "f", TradeDate: "20261026", Symbol: "204001", Exchange: "SH", TradeSide: "S", BusinessType: "S", Qty: 137360, Price: 1.01, MatchedAt: at.Add(11 * time.Second)}}
}

func TestResearchRepoCashTiming(t *testing.T) {
	d, fills := researchRepoTimingFixture(t)
	r := reviewResearchRepoCashTiming(d, "a", fills)
	if r["state"] != "reviewed_repo_cash_timing_proven" || r["reviewed_principal_as_of_cash"] != float64(0) || r["reviewed_principal_as_of_batch"] != float64(13736000) || r["batch_clock_would_include_later_principal"] != true {
		t.Fatal(r)
	}
	for _, delta := range []time.Duration{-12 * time.Second, -11 * time.Second, 5 * time.Second} {
		d, f := researchRepoTimingFixture(t)
		f[0].MatchedAt = f[0].MatchedAt.Add(delta)
		r := reviewResearchRepoCashTiming(d, "a", f)
		want := float64(0)
		if delta <= -11*time.Second {
			want = 13736000
		}
		if r["reviewed_principal_as_of_cash"] != want || r["batch_clock_would_include_later_principal"] != false {
			t.Fatal(delta, r)
		}
	}
	for _, name := range []string{"missing snapshot", "duplicate snapshot", "duplicate query", "missing query", "wrong cash", "wrong route", "duplicate fill", "wrong fill account", "wrong fill day", "missing fill time"} {
		t.Run(name, func(t *testing.T) {
			d, f := researchRepoTimingFixture(t)
			switch name {
			case "missing snapshot":
				d.SnapshotTimes = d.SnapshotTimes[:1]
			case "duplicate snapshot":
				d.SnapshotTimes = append(d.SnapshotTimes, d.SnapshotTimes[0])
			case "duplicate query":
				d.Attempts = append(d.Attempts, d.Attempts[0])
			case "missing query":
				d.Attempts = nil
			case "wrong cash", "wrong route":
				var e map[string]any
				_ = json.Unmarshal(d.Attempts[0].Queries[0].AssetReply, &e)
				if name == "wrong cash" {
					e["payload"].(map[string]any)["account"].(map[string]any)["cash_total"] = 1
				} else {
					e["routing"].(map[string]any)["account_id"] = "other"
				}
				d.Attempts[0].Queries[0].AssetReply, _ = json.Marshal(e)
			case "duplicate fill":
				f = append(f, f[0])
			case "wrong fill account":
				f[0].AccountID = "other"
			case "wrong fill day":
				f[0].TradeDate = "20261027"
			case "missing fill time":
				f[0].MatchedAt = time.Time{}
			}
			if r := reviewResearchRepoCashTiming(d, "a", f); r["state"] == "reviewed_repo_cash_timing_proven" {
				t.Fatal(r)
			}
		})
	}
	d, fills = researchRepoTimingFixture(t)
	fills[0].Symbol = "600000"
	if r := reviewResearchRepoCashTiming(d, "a", fills); r["state"] != "no_reviewed_reverse_repo" {
		t.Fatal(r)
	}
}
