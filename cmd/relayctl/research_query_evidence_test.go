package main

import (
	"encoding/json"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

func queryEvidenceFixture(t *testing.T) (config.AccountRouteConfig, ledger.CommandStatus, time.Time, time.Time, []researchArchivedReply) {
	t.Helper()
	start := time.Date(2026, 10, 12, 15, 1, 0, 0, timeutil.Location())
	route := config.AccountRouteConfig{AccountID: "a", BrokerID: "huaxin", GatewayID: "g"}
	terminal := ledger.CommandStatus{OriginMessageID: "q", AccountID: "a", Action: "order.list.query", ExpectedResultType: "order_page", State: "completed", Terminal: true, Success: true, ReplyCount: 2, TerminalCount: 1}
	var replies []researchArchivedReply
	for i := 0; i < 2; i++ {
		state := "partial"
		if i == 1 {
			state = "completed"
		}
		body, err := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "reply", "message_id": []string{"p1", "p2"}[i], "origin_message_id": "q", "action": "order.list.query", "result_type": "order_page", "status": state, "produced_at": start.Add(time.Second), "routing": map[string]any{"env": "prod", "account_id": "a", "broker_id": "huaxin", "gateway_id": "g"}, "chunk": map[string]any{"is_last": i == 1}, "payload": map[string]any{"items": []any{}}})
		if err != nil {
			t.Fatal(err)
		}
		replies = append(replies, researchArchivedReply{Body: body, Stream: "relay:prod:v1:huaxin:g:reply", ReceivedAt: start.Add(time.Duration(i+1) * time.Second)})
	}
	return route, terminal, start, start.Add(5 * time.Second), replies
}

func mutateEvidenceReply(t *testing.T, r *researchArchivedReply, mutate func(map[string]any)) {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal(r.Body, &e); err != nil {
		t.Fatal(err)
	}
	mutate(e)
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	r.Body = b
}

func TestResearchQueryEvidenceTerminalAndArchive(t *testing.T) {
	route, terminal, start, end, replies := queryEvidenceFixture(t)
	check := func(term ledger.CommandStatus, rows []researchArchivedReply) researchQueryProof {
		return reviewResearchQuery("orders", "order.list.query", "order_page", "production", "2026-10-12", route, term, start, end, rows)
	}
	p := check(terminal, replies)
	if p.State != "terminal_archive_consistent" || p.Finals != 1 || p.Items != 0 || len(p.ArchiveSHA) != 64 {
		t.Fatal(p)
	}
	// A report may omit all detailed replies and keep only the actual count.
	terminal.Replies = nil
	if p := check(terminal, replies); p.State != "terminal_archive_consistent" {
		t.Fatal("report truncation treated as loss", p)
	}
	for name, mutate := range map[string]func(*ledger.CommandStatus, []researchArchivedReply){
		"missing origin":     func(s *ledger.CommandStatus, r []researchArchivedReply) { s.OriginMessageID = "" },
		"reported count":     func(s *ledger.CommandStatus, r []researchArchivedReply) { s.ReplyCount++ },
		"failed terminal":    func(s *ledger.CommandStatus, r []researchArchivedReply) { s.Success = false },
		"multiple terminals": func(s *ledger.CommandStatus, r []researchArchivedReply) { s.TerminalCount = 2 },
		"parse error":        func(s *ledger.CommandStatus, r []researchArchivedReply) { r[0].ParseError = "bad body" },
		"wrong stream":       func(s *ledger.CommandStatus, r []researchArchivedReply) { r[0].Stream = "relay:test:v1:huaxin:g:reply" },
		"late reply":         func(s *ledger.CommandStatus, r []researchArchivedReply) { r[1].ReceivedAt = end.Add(time.Second) },
		"wrong origin": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[0], func(e map[string]any) { e["origin_message_id"] = "other" })
		},
		"wrong account": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[0], func(e map[string]any) { e["routing"].(map[string]any)["account_id"] = "other" })
		},
		"wrong date": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[0], func(e map[string]any) { e["produced_at"] = start.AddDate(0, 0, -1) })
		},
		"missing items": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[0], func(e map[string]any) { e["payload"] = map[string]any{} })
		},
		"duplicate id": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[1], func(e map[string]any) { e["message_id"] = "p1" })
		},
		"last flag": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[1], func(e map[string]any) { e["chunk"] = map[string]any{"is_last": false} })
		},
		"two finals": func(s *ledger.CommandStatus, r []researchArchivedReply) {
			mutateEvidenceReply(t, &r[0], func(e map[string]any) { e["status"] = "completed"; e["chunk"] = map[string]any{"is_last": true} })
		},
	} {
		t.Run(name, func(t *testing.T) {
			term := terminal
			rows := append([]researchArchivedReply(nil), replies...)
			mutate(&term, rows)
			if p := check(term, rows); p.State != "not_proven" || len(p.Flags) == 0 {
				t.Fatal(p)
			}
		})
	}
	if p := check(terminal, replies[:1]); p.State != "not_proven" {
		t.Fatal("truncated archive", p)
	}
}

func TestResearchQueryEvidenceMissingAndUnscheduled(t *testing.T) {
	var a researchQueryAccount
	if err := json.Unmarshal([]byte(`{"refresh":[{"step":"orders"},{"step":"fills"},{"step":"asset"},{"step":"positions"}]}`), &a); err != nil {
		t.Fatal(err)
	}
	if !preOpenFeeNotScheduled("pre_open_init", a, "fees") || preOpenFeeNotScheduled("post_close_capture", a, "fees") || preOpenFeeNotScheduled("pre_open_init", a, "positions") {
		t.Fatal("scope mismatch")
	}
	a.Freshness.Terminals = map[string]ledger.CommandStatus{"fees": {}}
	if preOpenFeeNotScheduled("pre_open_init", a, "fees") {
		t.Fatal("failed scheduled fee ignored")
	}
	if preOpenFeeNotScheduled("pre_open_init", researchQueryAccount{}, "fees") {
		t.Fatal("missing report treated as plan")
	}
}

func TestResearchSnapshotTimingEvidence(t *testing.T) {
	asset := map[string]any{"account_id": "a", "updated_at": "2026-10-28T15:01:01+08:00", "cash_total": 100, "cash_available": 90, "net_asset": 100, "commission": 1}
	broker := map[string]any{"snapshot_type": "broker_close", "captured_at": "2026-10-28T15:01:23+08:00", "asset": asset}
	close := map[string]any{"snapshot_type": "close", "captured_at": "2026-10-28T15:01:01+08:00", "broker_asset": asset, "input_snapshot_type": "broker_close", "reverse_repo": map[string]any{"cash_observed_at": "2026-10-28T15:01:01+08:00"}}
	check := func() map[string]any {
		b, _ := json.Marshal(broker)
		c, _ := json.Marshal(close)
		return reviewResearchSnapshotTimes([]json.RawMessage{b, c})
	}
	if p := check(); p["state"] != "cash_observation_time_not_batch_time" || p["baseline_override"] != false {
		t.Fatal(p)
	}
	close["captured_at"] = "2026-10-28T15:01:02+08:00"
	if p := check(); p["state"] != "unexplained_time_difference" {
		t.Fatal("time discrepancy hidden", p)
	}
	close["captured_at"] = "2026-10-28T15:01:23+08:00"
	if p := check(); p["state"] != "same_batch_time" {
		t.Fatal(p)
	}
	if p := reviewResearchSnapshotTimes(nil); p["state"] != "snapshot_missing" {
		t.Fatal(p)
	}
}
