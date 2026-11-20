package accountbaseline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
)

var fixtureStart = time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)

func fixtureSources(t *testing.T, count int) ledger.CurrentBaselineSources {
	t.Helper()
	n := int64(1)
	s := ledger.CurrentBaselineSources{Scope: ledger.CurrentBaselineScope{Environment: "test", AccountID: "00030484", BrokerID: "huaxin", GatewayID: "00030484", TradeDate: "20261119", CounterSessionID: "test-session", ConnectionGeneration: &n}, ReadAt: fixtureStart.Add(time.Minute), Queries: map[string]ledger.CurrentBaselineQuerySources{}}
	for _, kind := range []string{"asset", "positions", "orders", "fills"} {
		id := "query-" + kind
		command := map[string]any{"protocol": "relay.stream.v1", "message_type": "command", "message_id": id, "action": ledger.CurrentBaselineQueryActions[kind], "sent_at": fixtureStart, "payload": map[string]any{"account_id": s.Scope.AccountID, "trade_date": s.Scope.TradeDate}}
		b, _ := json.Marshal(command)
		q := ledger.CurrentBaselineQuerySources{MessageID: id, Commands: []ledger.CurrentBaselineArchive{{ArchiveID: "cmd-" + kind, Stream: "relay:test:v1:huaxin:00030484:cmd.query", EntryID: "1-0", Direction: "in", Role: "cmd.query", Body: string(b), ReceivedAt: fixtureStart}}}
		if kind == "positions" && count > 0 {
			for start := 0; start < count; start += 500 {
				items := []any{}
				for j := start; j < count && j < start+500; j++ {
					items = append(items, map[string]any{"symbol": fmt.Sprintf("%06d", j+1), "exchange": "SZ", "quantity": 100, "sellable_qty": 100})
				}
				q.Replies = append(q.Replies, fixturePage(kind, len(q.Replies), false, map[string]any{"items": items}))
			}
		}
		payload := map[string]any{"items": []any{}}
		if kind == "asset" {
			payload = map[string]any{"account": map[string]any{"account_id": s.Scope.AccountID, "cash_available": json.Number("9007199254740993.01"), "cash_total": json.Number("9007199254741093.01")}}
		}
		q.Replies = append(q.Replies, fixturePage(kind, len(q.Replies), true, payload))
		s.Queries[kind] = q
	}
	return s
}

func fixturePage(kind string, index int, last bool, payload map[string]any) ledger.CurrentBaselineArchive {
	status := "partial"
	if last {
		status = "completed"
	}
	w := map[string]any{"protocol": "relay.stream.v1", "message_type": "reply", "message_id": fmt.Sprintf("reply-%s-%d", kind, index), "origin_message_id": "query-" + kind, "action": ledger.CurrentBaselineQueryActions[kind], "result_type": map[string]string{"asset": "asset_page", "positions": "position_page", "orders": "order_page", "fills": "fill_page"}[kind], "status": status, "produced_at": fixtureStart.Add(time.Second), "routing": map[string]any{"env": "test", "broker_id": "huaxin", "gateway_id": "00030484", "account_id": "00030484"}, "chunk": map[string]any{"is_last": last}, "adapter_context": map[string]any{"counter_session_id": "test-session", "connection_generation": 1, "trade_date": "20261119"}, "payload": payload}
	b, _ := json.Marshal(w)
	return ledger.CurrentBaselineArchive{ArchiveID: fmt.Sprintf("archive-%s-%d", kind, index), Stream: "relay:test:v1:huaxin:00030484:reply", EntryID: fmt.Sprintf("%d-0", index+2), Direction: "out", Role: "reply", Body: string(b), ReceivedAt: fixtureStart.Add(2 * time.Second)}
}

func mutate(t *testing.T, r *ledger.CurrentBaselineArchive, fn func(map[string]any)) {
	t.Helper()
	var w map[string]any
	d := json.NewDecoder(strings.NewReader(r.Body))
	d.UseNumber()
	if err := d.Decode(&w); err != nil {
		t.Fatal(err)
	}
	fn(w)
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	r.Body = string(b)
}

func reports(t *testing.T, c ledger.CurrentBaselineCandidate) map[string]QueryReport {
	t.Helper()
	var p []QueryReport
	if err := json.Unmarshal(c.Queries, &p); err != nil {
		t.Fatal(err)
	}
	m := map[string]QueryReport{}
	for _, v := range p {
		m[v.Resource] = v
	}
	return m
}

func hasFlag(p QueryReport, s string) bool {
	for _, v := range p.Flags {
		if v == s {
			return true
		}
	}
	return false
}

func TestCurrentAccountBaselineCheckpointIsNotCoverage(t *testing.T) {
	for _, cursor := range []string{"", "1-0", "10-0"} {
		t.Run(cursor, func(t *testing.T) {
			s := fixtureSources(t, 501)
			s.SourceHealth = &ledger.CurrentBaselineSourceHealth{
				ObservedAt: s.ReadAt, TrackingAvailable: true,
				Checkpoints: []ledger.CurrentBaselineCheckpoint{{Role: "reply", Found: cursor != "", LastStreamID: cursor}},
				Reasons:     []string{"source_continuity_unverified"},
			}
			c, err := Assemble(s, "checkpoint", fixtureStart)
			if err != nil {
				t.Fatal(err)
			}
			p := reports(t, c)["positions"]
			if hasFlag(p, "reply_checkpoint_not_caught_up") != (cursor != "10-0") {
				t.Fatal(p)
			}
			if p.ArchiveComplete || !p.TerminalSequenceValid || c.SourceHealth.ContinuityVerified {
				t.Fatal("checkpoint promoted to complete coverage", p, c.SourceHealth)
			}
			if len(c.Resources["positions"]) != 501 {
				t.Fatal("diagnostic discarded observed rows")
			}
		})
	}
}

func localOrder(id, date, gateway string) ledger.CurrentBaselineRecord {
	return record(id, map[string]any{"account_id": "00030484", "source": "ledger_unresolved", "gateway_order_id": gateway, "trade_date": date, "symbol": "000001", "exchange": "SZ", "trade_side": "B", "business_type": "S", "order_qty": 100, "cum_filled_qty": 0, "leaves_qty": 100, "limit_price": "10.000", "status": "created", "is_terminal": false, "execution_state": "reconciliation_required"})
}

func orderItem() map[string]any {
	return map[string]any{"gateway_order_id": "gw-1", "trade_date": "20261119", "symbol": "000001", "exchange": "SZ", "trade_side": "B", "business_type": "S", "limit_price": 10, "order_qty": 100, "cum_filled_qty": 0, "leaves_qty": 0, "status": "cancelled", "gateway_status": "cancelled", "is_terminal": true}
}

func TestCurrentAccountBaselineAssemblePagesAndUnresolved(t *testing.T) {
	s := fixtureSources(t, 501)
	s.UnresolvedOrders = []ledger.CurrentBaselineRecord{localOrder("ledger-order:1", "20261118", "prior-day"), localOrder("ledger-order:2", "20261119", "never-published")}
	c, err := Assemble(s, "collection", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources["positions"]) != 501 || len(c.Resources["orders"]) != 2 || !strings.Contains(string(c.Cash), `"9007199254740993.01"`) {
		t.Fatal("lost pages, local unknown orders or decimal precision")
	}
	for _, p := range reports(t, c) {
		if !p.RequestMatched || !p.TerminalSequenceValid || !p.ContextMatched || !p.ProjectionValid || p.ArchiveComplete || !hasFlag(p, "page_coverage_unverified") {
			t.Fatalf("invalid evidence conclusion: %+v", p)
		}
	}
	if reports(t, c)["positions"].Pages[0].BodySHA256 != digest([]byte(s.Queries["positions"].Replies[0].Body)) {
		t.Fatal("raw source digest changed")
	}
	if !strings.Contains(string(c.Cash), `"frozen_cash":null`) {
		t.Fatal("invented frozen cash")
	}
}

func TestCurrentAccountBaselineUnresolvedMergeRetainsResponsibility(t *testing.T) {
	s := fixtureSources(t, 0)
	q := s.Queries["orders"]
	q.Replies = []ledger.CurrentBaselineArchive{fixturePage("orders", 0, true, map[string]any{"items": []any{orderItem()}})}
	s.Queries["orders"] = q
	s.UnresolvedOrders = []ledger.CurrentBaselineRecord{localOrder("ledger-order:1", "20261119", "gw-1")}
	c, err := Assemble(s, "merge", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources["orders"]) != 1 {
		t.Fatalf("double counted same order: %+v", reports(t, c)["orders"])
	}
	raw := string(c.Resources["orders"][0].Document)
	for _, want := range []string{`"status":"cancelled"`, `"status":"created"`, `"has_unresolved_ledger_observation":true`, `"execution_state":"reconciliation_required"`, `"origin_message_id":null`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
	// A different immutable intent must not be merged just because the ID agrees.
	s.UnresolvedOrders[0].Document = json.RawMessage(strings.ReplaceAll(string(s.UnresolvedOrders[0].Document), `"10.000"`, `"11.000"`))
	c, err = Assemble(s, "conflict", fixtureStart)
	if err != nil || len(c.Resources["orders"]) != 2 {
		t.Fatal("intent conflict collapsed", err)
	}
	found := false
	for _, v := range c.Limitations {
		found = found || v.Code == "order_observation_conflict"
	}
	if !found {
		t.Fatal("unlabelled intent conflict")
	}
}

func TestCurrentAccountBaselineRejectsInvalidArchives(t *testing.T) {
	cases := map[string]func(*testing.T, *ledger.CurrentBaselineQuerySources){
		"no_final": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) { q.Replies = q.Replies[:1] },
		"missing_items": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[1], func(w map[string]any) { delete(w["payload"].(map[string]any), "items") })
		},
		"null_items": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[1], func(w map[string]any) { w["payload"].(map[string]any)["items"] = nil })
		},
		"wrong_stream": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) {
			q.Replies[0].Stream = "relay:prod:v1:huaxin:00030484:reply"
		},
		"wrong_direction": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) { q.Replies[0].Direction = "in" },
		"missing_body":    func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) { q.Replies[0].Body = "" },
		"parse_error":     func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) { q.Replies[0].ParseError = "archive error" },
		"duplicate_json_key": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) {
			q.Replies[0].Body = strings.Replace(q.Replies[0].Body, `"status":"partial"`, `"status":"completed","status":"partial"`, 1)
		},
		"failed_final": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[1], func(w map[string]any) { w["status"] = "failed" })
		},
		"no_last_flag": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[1], func(w map[string]any) { delete(w, "chunk") })
		},
		"old_session": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) { w["adapter_context"].(map[string]any)["counter_session_id"] = "old-session" })
		},
		"wrong_generation": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) { w["adapter_context"].(map[string]any)["connection_generation"] = 2 })
		},
		"wrong_day": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) { w["adapter_context"].(map[string]any)["trade_date"] = "20261118" })
		},
		"wrong_account": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) { w["routing"].(map[string]any)["account_id"] = "other" })
		},
		"filtered_request": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Commands[0], func(w map[string]any) { w["payload"].(map[string]any)["symbol"] = "000001" })
		},
		"partial_bad_command_routing": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Commands[0], func(w map[string]any) { w["routing"] = map[string]any{"broker_id": "other"} })
		},
		"missing_quantity": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) {
				delete(w["payload"].(map[string]any)["items"].([]any)[0].(map[string]any), "quantity")
			})
		},
		"null_quantity": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) {
				w["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)["quantity"] = nil
			})
		},
		"bad_sellable": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) {
				w["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)["sellable_qty"] = 101
			})
		},
		"duplicate_rows": func(t *testing.T, q *ledger.CurrentBaselineQuerySources) {
			mutate(t, &q.Replies[0], func(w map[string]any) {
				p := w["payload"].(map[string]any)
				items := p["items"].([]any)
				p["items"] = append(items, items[0])
			})
		},
		"duplicate_reply": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) {
			q.Replies = append([]ledger.CurrentBaselineArchive{q.Replies[0]}, q.Replies...)
		},
		"final_not_last": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) {
			q.Replies[0], q.Replies[1] = q.Replies[1], q.Replies[0]
		},
		"late_archive": func(_ *testing.T, q *ledger.CurrentBaselineQuerySources) {
			q.Replies[1].ReceivedAt = fixtureStart.Add(time.Hour)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := fixtureSources(t, 1)
			q := s.Queries["positions"]
			change(t, &q)
			s.Queries["positions"] = q
			s.UnresolvedOrders = []ledger.CurrentBaselineRecord{localOrder("ledger-order:1", "20261118", "old")}
			c, err := Assemble(s, name, fixtureStart)
			if err != nil {
				t.Fatal(err)
			}
			p := reports(t, c)["positions"]
			if p.ProjectionValid || p.ArchiveComplete || len(c.Resources["positions"]) != 0 || len(c.Resources["orders"]) != 1 {
				t.Fatalf("invalid source accepted or local risk lost: %+v", p)
			}
		})
	}
}

func TestCurrentAccountBaselineUnknownsAreNotInvented(t *testing.T) {
	s := fixtureSources(t, 1)
	q := s.Queries["positions"]
	for i := range q.Replies {
		mutate(t, &q.Replies[i], func(w map[string]any) {
			delete(w, "adapter_context")
			for _, v := range w["payload"].(map[string]any)["items"].([]any) {
				delete(v.(map[string]any), "sellable_qty")
			}
		})
	}
	s.Queries["positions"] = q
	c, err := Assemble(s, "no-context", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	p := reports(t, c)["positions"]
	if !p.ProjectionValid || p.ContextMatched || !hasFlag(p, "query_context_unverified") || !strings.Contains(string(c.Resources["positions"][0].Document), `"sellable_qty":null`) {
		t.Fatal(p, string(c.Resources["positions"][0].Document))
	}
	// A lost middle data page cannot be detected by Stream ID gaps. Even a
	// syntactically final-only collection remains coverage-unverified.
	q.Replies = q.Replies[1:]
	s.Queries["positions"] = q
	c, err = Assemble(s, "lost-data-page", fixtureStart)
	if err != nil || !reports(t, c)["positions"].TerminalSequenceValid || reports(t, c)["positions"].ArchiveComplete || len(c.Resources["positions"]) != 0 {
		t.Fatal("invented completeness", err)
	}
}

func TestCurrentAccountBaselineFillsAndNoSummarySynthesis(t *testing.T) {
	s := fixtureSources(t, 0)
	q := s.Queries["orders"]
	item := orderItem()
	item["status"] = "filled"
	item["gateway_status"] = "filled"
	item["cum_filled_qty"] = 100
	q.Replies = []ledger.CurrentBaselineArchive{fixturePage("orders", 0, true, map[string]any{"items": []any{item}})}
	s.Queries["orders"] = q
	c, err := Assemble(s, "no-summary", fixtureStart)
	if err != nil || len(c.Resources["orders"]) != 1 || len(c.Resources["fills"]) != 0 {
		t.Fatal("synthesized fill", err)
	}
	q = s.Queries["fills"]
	fill := map[string]any{"gateway_order_id": "gw-1", "fill_id": "fill-1", "trade_date": "20261119", "symbol": "000001", "exchange": "SZ", "trade_side": "B", "price": json.Number("10.123456789"), "qty": 100}
	q.Replies = []ledger.CurrentBaselineArchive{fixturePage("fills", 0, true, map[string]any{"items": []any{fill}})}
	s.Queries["fills"] = q
	c, err = Assemble(s, "fill", fixtureStart)
	if err != nil || len(c.Resources["fills"]) != 1 {
		t.Fatal(err, reports(t, c)["fills"])
	}
	raw := string(c.Resources["fills"][0].Document)
	if !strings.Contains(raw, `"fee":null`) || !strings.Contains(raw, `"matched_at":null`) || !strings.Contains(raw, `"price":"10.123456789"`) {
		t.Fatal("invented fee/time or rounded price", raw)
	}
}

func TestCurrentAccountBaselineDecimalAndJSONValidation(t *testing.T) {
	for in, want := range map[string]string{`9007199254740993.01`: "9007199254740993.01", `1.2300e2`: "123", `-0.000`: "0", `1e-8`: "0.00000001", `"10.1000"`: "10.1"} {
		got, err := decimal(json.RawMessage(in))
		if err != nil || got != want {
			t.Fatal(in, got, err)
		}
	}
	for _, bad := range []string{`null`, `true`, `[]`, `1e100000`, `0.00000000001e-99`, `"1/2"`, `"NaN"`, `" 1 "`} {
		if _, err := decimal(json.RawMessage(bad)); err == nil {
			t.Fatal("invalid decimal", bad)
		}
	}
	if err := ledger.ValidateCurrentBaselineSourceJSON([]byte(`{"price":1.23456789,"qty":9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"a":1,"a":2}`, `{"a":"\ud800"}`, `{"a":1} {}`} {
		if ledger.ValidateCurrentBaselineSourceJSON([]byte(bad)) == nil {
			t.Fatal("invalid source JSON", bad)
		}
	}
	s := fixtureSources(t, 1)
	original, _ := json.Marshal(s)
	_, err := Assemble(s, "immutable", fixtureStart)
	after, _ := json.Marshal(s)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatal("mutated source", err)
	}
}

func TestCurrentAccountBaselineProjectionFailures(t *testing.T) {
	cases := []struct {
		kind    string
		payload map[string]any
	}{
		{"asset", map[string]any{"account": map[string]any{"account_id": "00030484", "cash_total": 10}}},
		{"asset", map[string]any{"account": map[string]any{"account_id": "other", "cash_total": 10, "cash_available": 10}}},
		{"fills", map[string]any{"items": []any{}, "component_transfers": []any{map[string]any{"qty": 100}}}},
		{"fills", map[string]any{"items": []any{}, "item_count": 1}},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := fixtureSources(t, 0)
			q := s.Queries[tc.kind]
			q.Replies = []ledger.CurrentBaselineArchive{fixturePage(tc.kind, 0, true, tc.payload)}
			s.Queries[tc.kind] = q
			c, err := Assemble(s, "invalid-projection", fixtureStart)
			if err != nil {
				t.Fatal(err)
			}
			p := reports(t, c)[tc.kind]
			if p.ProjectionValid || p.ProjectionError == "" || p.ObservedRows != nil {
				t.Fatal("failed projection represented as complete zero", p)
			}
		})
	}
}
