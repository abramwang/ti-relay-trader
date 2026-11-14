package main

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/trading"
)

func openingPageFixture() (researchQueryProof, researchArchivedReply) {
	return researchQueryProof{State: "terminal_archive_consistent", MessageID: "q", ArchivedReplies: 1, Finals: 1, Items: 1}, researchArchivedReply{Stream: "relay:prod:v1:huaxin:g:reply", ReceivedAt: time.Date(2026, 10, 12, 1, 1, 3, 0, time.UTC), Body: []byte(`{"protocol":"relay.stream.v1","message_type":"reply","message_id":"p","origin_message_id":"q","action":"account.positions.query","result_type":"position_page","status":"completed","produced_at":"2026-10-12T09:01:02+08:00","routing":{"env":"prod","broker_id":"huaxin","gateway_id":"g","account_id":"a"},"chunk":{"is_last":true},"payload":{"items":[{"account_id":"a01","symbol":"510310","exchange":"SH","quantity":5900,"sellable_qty":5900,"avg_cost":999}]}}`)}
}

func TestResearchOpeningPages(t *testing.T) {
	p, raw := openingPageFixture()
	before := string(raw.Body)
	ps, err := reviewResearchOpeningPages("a", "2026-10-12", p, []researchArchivedReply{raw})
	if err != nil || len(ps) != 1 || ps[0].Quantity != 5900 || ps[0].AccountID != "a" || string(raw.Body) != before {
		t.Fatal(ps, err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"wrong account": func(e map[string]any) { e["routing"].(map[string]any)["account_id"] = "other" },
		"wrong payload account": func(e map[string]any) {
			e["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)["account_id"] = "other"
		},
		"wrong origin":  func(e map[string]any) { e["origin_message_id"] = "other" },
		"partial":       func(e map[string]any) { e["status"] = "partial"; e["chunk"].(map[string]any)["is_last"] = false },
		"final flag":    func(e map[string]any) { e["chunk"].(map[string]any)["is_last"] = false },
		"missing time":  func(e map[string]any) { delete(e, "produced_at") },
		"auction":       func(e map[string]any) { e["produced_at"] = "2026-10-12T09:15:00+08:00" },
		"wrong date":    func(e map[string]any) { e["produced_at"] = "2026-10-11T09:01:02+08:00" },
		"missing items": func(e map[string]any) { delete(e["payload"].(map[string]any), "items") },
		"missing quantity": func(e map[string]any) {
			delete(e["payload"].(map[string]any)["items"].([]any)[0].(map[string]any), "quantity")
		},
		"duplicate": func(e map[string]any) {
			payload := e["payload"].(map[string]any)
			items := payload["items"].([]any)
			payload["items"] = append(items, items[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, raw := openingPageFixture()
			mutateEvidenceReply(t, &raw, mutate)
			if _, err := reviewResearchOpeningPages("a", "2026-10-12", p, []researchArchivedReply{raw}); err == nil {
				t.Fatal("invalid opening accepted")
			}
		})
	}
	p.Items = 0
	mutateEvidenceReply(t, &raw, func(e map[string]any) { e["payload"].(map[string]any)["items"] = []any{} })
	if ps, err := reviewResearchOpeningPages("a", "2026-10-12", p, []researchArchivedReply{raw}); err != nil || len(ps) != 0 {
		t.Fatal("complete empty", ps, err)
	}
	if _, err := reviewResearchOpeningPages("a", "2026-10-12", p, nil); err == nil {
		t.Fatal("missing treated as empty")
	}
	p.State = "not_proven"
	if _, err := reviewResearchOpeningPages("a", "2026-10-12", p, []researchArchivedReply{raw}); err == nil {
		t.Fatal("unproven treated as empty")
	}
	p, raw = openingPageFixture()
	final := raw
	mutateEvidenceReply(t, &raw, func(e map[string]any) { e["status"] = "partial"; e["chunk"].(map[string]any)["is_last"] = false })
	mutateEvidenceReply(t, &final, func(e map[string]any) { e["message_id"] = "last"; e["payload"].(map[string]any)["items"] = []any{} })
	p.ArchivedReplies = 2
	if _, err := reviewResearchOpeningPages("a", "2026-10-12", p, []researchArchivedReply{raw, final}); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewResearchOpeningPages("a", "2026-10-12", p, []researchArchivedReply{final, raw}); err == nil {
		t.Fatal("final out of order")
	}
}

func TestResearchOpeningQuantityAndCosts(t *testing.T) {
	p := trading.Position{AccountID: "a", Symbol: "510310", Exchange: "SH", Quantity: 6000, SellableQty: 0}
	rows, err := researchOpeningQuantities("a", "2026-10-12", []trading.Position{p})
	if err != nil {
		t.Fatal(err)
	}
	p.SellableQty = 6000
	p.AvgCost = 999
	other, err := researchOpeningQuantities("a", "2026-10-12", []trading.Position{p})
	if err != nil || compareResearchBooks(rows, other).Matched != 1 {
		t.Fatal("sellability or broker cost leaked")
	}
	for _, invalid := range [][]trading.Position{{p, p}, {{AccountID: "other", Symbol: "510310", Exchange: "SH", Quantity: 6000}}, {{AccountID: "a", Symbol: "510310", Exchange: "SH", Quantity: -1}}, {{AccountID: "a", Symbol: "510310", Exchange: "SH", Quantity: 1, SellableQty: 2}}} {
		if _, err := researchOpeningQuantities("a", "2026-10-12", invalid); err == nil {
			t.Fatal("bad positions")
		}
	}
	base := ledger.PositionCostState{AccountID: "a", TradeDate: "2026-10-12", Symbol: "510310", Exchange: "SH", CostBucket: "CORE", OpenQuantity: 100, OpenTotalCost: 200}
	residual := base
	residual.CostBucket = "ETF_T0_RESIDUAL:x"
	residual.OpenQuantity = 5900
	residual.OpenTotalCost = 25076.253750
	costs, lots, err := researchOpeningCostRows("a", "2026-10-12", []ledger.PositionCostState{base, residual})
	if err != nil || len(lots) != 2 || compareResearchBooks(costs, rows).Matched != 1 || lots[1].Cost != "25076.253750" {
		t.Fatal(lots, err)
	}
	changed := p
	changed.Quantity = 6001
	changedRows, _ := researchOpeningQuantities("a", "2026-10-12", []trading.Position{changed})
	if compareResearchBooks(costs, changedRows).State != "differences_require_review" {
		t.Fatal("difference hidden")
	}
	for _, mutate := range []func(*ledger.PositionCostState){func(p *ledger.PositionCostState) { p.AccountID = "other" }, func(p *ledger.PositionCostState) { p.TradeDate = "2026-10-13" }, func(p *ledger.PositionCostState) { p.CorporateActionType = "split" }, func(p *ledger.PositionCostState) { p.CorporateActionQuantityDelta = 1 }, func(p *ledger.PositionCostState) { p.OpenTotalCost = math.NaN() }, func(p *ledger.PositionCostState) { p.OpenQuantity = -1 }} {
		bad := base
		mutate(&bad)
		if _, _, err := researchOpeningCostRows("a", "2026-10-12", []ledger.PositionCostState{bad}); err == nil {
			t.Fatal("invalid cost")
		}
	}
	if _, _, err := researchOpeningCostRows("a", "2026-10-12", []ledger.PositionCostState{base, base}); err == nil {
		t.Fatal("duplicate bucket")
	}
	base.OpenQuantity = math.MaxInt64
	if _, _, err := researchOpeningCostRows("a", "2026-10-12", []ledger.PositionCostState{base, residual}); err == nil {
		t.Fatal("overflow")
	}
	zero := p
	zero.Quantity = 0
	zero.SellableQty = 0
	if rows, err := researchOpeningQuantities("a", "2026-10-12", []trading.Position{zero}); err != nil || len(rows) != 0 {
		t.Fatal("zero set", err)
	}
}

func TestResearchOpeningScopeBeforeDatabase(t *testing.T) {
	for _, env := range []string{"test", "production"} {
		if _, err := readResearchOpeningEvidence(context.Background(), nil, env, config.AccountRouteConfig{AccountID: "a", BrokerID: "huaxin"}, performance.ResearchCostBundle{}, nil); err == nil {
			t.Fatal("unverified research scope")
		}
	}
	// Every fixture keeps its original serialized input available for audit.
	_, raw := openingPageFixture()
	if !json.Valid(raw.Body) {
		t.Fatal("invalid fixture")
	}
}
