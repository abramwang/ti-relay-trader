package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const responsibilityFixtureDir = "../../testdata/oc_responsibility_review_20261118"

func responsibilityFixture(t *testing.T, name string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(responsibilityFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatal(err)
	}
}

func TestResponsibilityReviewOriginalDigests(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(responsibilityFixtureDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 7 {
		t.Fatal("unexpected evidence inventory")
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || filepath.Base(fields[1]) != fields[1] {
			t.Fatal("invalid digest manifest")
		}
		body, err := os.ReadFile(filepath.Join(responsibilityFixtureDir, fields[1]))
		if err != nil || commandDigest(body) != fields[0] {
			t.Fatal("original evidence changed", fields[1], err)
		}
	}
}

func responsibilityHeartbeats(t *testing.T) []json.RawMessage {
	t.Helper()
	var doc struct {
		Synthetic  bool              `json:"synthetic_only"`
		Deployed   bool              `json:"deployed"`
		Heartbeats []json.RawMessage `json:"heartbeats"`
	}
	responsibilityFixture(t, "heartbeat_examples.json", &doc)
	if !doc.Synthetic || doc.Deployed || len(doc.Heartbeats) != 3 {
		t.Fatal("wrong heartbeat evidence scope")
	}
	return doc.Heartbeats
}

func responsibilityRoute() QueryRoute {
	return QueryRoute{"test", "ctp_4900", "ctp-dongwu-assessment-pm-20261117", "300052141"}
}

func TestResponsibilityHeartbeatQueryIndependentOfOrderProjection(t *testing.T) {
	for i, raw := range responsibilityHeartbeats(t) {
		h, err := DecodeHeartbeat(raw, responsibilityRoute())
		if err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
		_, ready := h.queryCollection()
		if h.Operations == nil || ready != (i == 1) || h.Operations.AcceptingQueryCommands != ready ||
			h.OrderSnapshotReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands ||
			h.Assessment.OrderSnapshotScope != "relay_standard_projection" || !bytes.Equal(h.Raw, raw) {
			t.Fatal("query capability promoted to trading or audit lost", i)
		}
	}
}

func TestResponsibilityHeartbeatExplicitBusyAndInvalidMirrors(t *testing.T) {
	raw := responsibilityHeartbeats(t)[1]
	busy := editBody(t, raw, func(m map[string]any) {
		m["accepting_query_commands"] = false
		m["payload"].(map[string]any)["accepting_query_commands"] = false
		a := m["adapter_context"].(map[string]any)
		a["operation_reasons"].(map[string]any)["query"] = "query_scheduler_busy"
		a["admission_evidence"].(map[string]any)["query_execution"] = map[string]any{"ready": false, "reason": "query_scheduler_busy"}
	})
	h, err := DecodeHeartbeat(busy, responsibilityRoute())
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := h.queryCollection(); ready {
		t.Fatal("explicit false overridden by inferred readiness")
	}
	for name, edit := range map[string]func(map[string]any){
		"top_only": func(m map[string]any) { delete(m["payload"].(map[string]any), "accepting_query_commands") },
		"mirror":   func(m map[string]any) { m["accepting_query_commands"] = false },
		"reason": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["operation_reasons"].(map[string]any)["query"] = "stopping"
		},
		"execution": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["query_execution"].(map[string]any)["ready"] = false
		},
		"recovery": func(m map[string]any) { m["adapter_context"].(map[string]any)["transport_recovery_clear"] = false },
		"scope": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["order_snapshot_scope"] = "all_accounts"
		},
		"projection": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["order_projection_ready"] = true
		},
		"production": func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeHeartbeat(editBody(t, raw, edit), responsibilityRoute()); err == nil {
				t.Fatal("contradictory capability accepted")
			}
		})
	}
}

func TestResponsibilityQueryProofKeepsSessionAndFreshnessChecks(t *testing.T) {
	raw := responsibilityHeartbeats(t)[1]
	h, err := DecodeHeartbeat(raw, responsibilityRoute())
	if err != nil {
		t.Fatal(err)
	}
	scope := CommandScope{Route: responsibilityRoute(), NativeBrokerID: "4900", UserID: "300052141", InvestorID: "300052141"}
	target := TestQueryTarget{CounterSessionID: h.CounterSessionID, ConnectionGeneration: 1, TradeDate: h.TradeDate}
	row := StreamInput{EntryID: "1-0", Raw: raw}
	if _, err := testQueryHeartbeatProof(row, scope, target, h.ProducedAt); err != nil {
		t.Fatal("initialization query deadlocked on standard projection", err)
	}
	for _, now := range []time.Time{h.ProducedAt.Add(5 * time.Second), h.ProducedAt.Add(-3 * time.Second)} {
		if _, err := testQueryHeartbeatProof(row, scope, target, now); err == nil {
			t.Fatal("stale or future heartbeat accepted")
		}
	}
	target.ConnectionGeneration++
	if _, err := testQueryHeartbeatProof(row, scope, target, h.ProducedAt); err == nil {
		t.Fatal("wrong generation accepted")
	}
}

func responsibilityRejections(t *testing.T) []json.RawMessage {
	t.Helper()
	var doc struct {
		Synthetic bool `json:"synthetic_only"`
		Cases     []struct {
			Body json.RawMessage `json:"body"`
		} `json:"cases"`
	}
	responsibilityFixture(t, "rejection_events.json", &doc)
	if !doc.Synthetic || len(doc.Cases) != 2 {
		t.Fatal("wrong rejection evidence scope")
	}
	return []json.RawMessage{doc.Cases[0].Body, doc.Cases[1].Body}
}

func responsibilityCommandScope() CommandScope {
	return CommandScope{QueryRoute{"test", "ctp_fixture", "fixture-gw", "990000000001"}, "9999", "FIXTURE_INV", "FIXTURE_USER"}
}

func responsibilityCancelContext(t *testing.T) CancelRejectionContext {
	t.Helper()
	// This command is synthetic matching context, not OC-supplied request evidence.
	v := intentVectors(t)["cancel_target_day"]
	raw := editBody(t, []byte(v.Raw), func(m map[string]any) {
		m["message_id"], m["request_id"], m["correlation_id"], m["idempotency_key"] = "cancel-message", "fixture-request", "fixture-correlation", "cancel-idem"
		m["payload"] = map[string]any{"account_id": "990000000001", "product_type": "futures", "schema_version": "ctp.f0.draft.2", "gateway_order_id": "fixture-order", "cancel_id": "fixture-cancel", "trade_date": "20261105"}
	})
	return CancelRejectionContext{responsibilityCommandScope(), raw, "ctpproc-journal-fixture", 1}
}

func TestResponsibilityRejectionTypesAndCommandBinding(t *testing.T) {
	raws := responsibilityRejections(t)
	scope := responsibilityCommandScope()
	origin := "fixture-submit"
	e, err := DecodeTradeEvent(raws[0], EventContext{TradeScope{Route: scope.Route, NativeBrokerID: scope.NativeBrokerID, InvestorID: scope.InvestorID, TradeDate: "20261105"}, &origin})
	if err != nil || e.Order == nil || e.Order.GatewayStatus != "rejected" || e.Order.SubmittedAt != nil {
		t.Fatal("insert rejection lost", err)
	}
	c, err := DecodeCancelRejection(raws[1], responsibilityCancelContext(t))
	if err != nil || c.CancelID != "fixture-cancel" || c.GatewayOrderID != "fixture-order" || c.NativeErrorID != 42 ||
		c.OrderStateChanged || c.RetrySafe || !c.ReconciliationRequired || !bytes.Equal(c.Raw, raws[1]) {
		t.Fatal("cancel outcome changed order or permitted retry", err)
	}
	raws[1][0] = '!'
	if c.Raw[0] == '!' || len(c.RawPayload) == 0 {
		t.Fatal("raw audit aliased or missing")
	}
}

func TestResponsibilityCancelRejectionRefusesUnboundOrUnsafeEvent(t *testing.T) {
	raw := responsibilityRejections(t)[1]
	context := responsibilityCancelContext(t)
	for _, key := range []string{"origin_message_id", "request_id", "correlation_id", "idempotency_key"} {
		t.Run(key, func(t *testing.T) {
			if _, err := DecodeCancelRejection(editBody(t, raw, func(m map[string]any) { m[key] = "unrelated" }), context); err == nil {
				t.Fatal("unrelated command accepted")
			}
		})
	}
	for key, value := range map[string]any{"account_id": "990000000002", "gateway_order_id": "other", "cancel_id": "other", "trade_date": "20261106", "counter_session_id": "other", "retry_safe": true, "order_state_changed": true, "reconciliation_required": false} {
		t.Run(key, func(t *testing.T) {
			if _, err := DecodeCancelRejection(editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)[key] = value }), context); err == nil {
				t.Fatal("unbound or unsafe cancel event accepted")
			}
		})
	}
	context.ConnectionGeneration++
	if _, err := DecodeCancelRejection(raw, context); err == nil {
		t.Fatal("wrong connection generation accepted")
	}
	context = responsibilityCancelContext(t)
	context.CommandRaw = []byte(`{}`)
	if _, err := DecodeCancelRejection(raw, context); err == nil {
		t.Fatal("missing independent command accepted")
	}
	context = responsibilityCancelContext(t)
	for name, edit := range map[string]func(map[string]any){
		"invalid_error_hex": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["native_error"].(map[string]any)["ErrorMsg_hex"] = "xyz"
		},
		"no_error": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["native_error"].(map[string]any)["ErrorID"] = 0
		},
		"no_sdk_request": func(m map[string]any) { m["adapter_context"].(map[string]any)["sdk_request_id"] = nil },
		"wrong_route":    func(m map[string]any) { m["routing"].(map[string]any)["gateway_id"] = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCancelRejection(editBody(t, raw, edit), context); err == nil {
				t.Fatal("malformed evidence accepted")
			}
		})
	}
	duplicate := bytes.Replace(raw, []byte(`"message_type":`), []byte(`"message_type":"event","message_type":`), 1)
	if _, err := DecodeCancelRejection(duplicate, context); err == nil {
		t.Fatal("duplicate JSON key accepted")
	}
	if _, err := DecodeCancelRejection(editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["message"] = "broker diagnostic text" }), context); err != nil {
		t.Fatal("broker text incorrectly restricted to synthetic sample wording", err)
	}
}

func TestResponsibilityNativeFactsRemainCandidateNotTradingPermission(t *testing.T) {
	var doc struct {
		Candidate bool `json:"candidate_only"`
		Enabled   bool `json:"runtime_command_enabled"`
		Synthetic bool `json:"synthetic_only"`
		Cases     []struct {
			Name    string          `json:"name"`
			Request json.RawMessage `json:"request"`
			Reply   map[string]any  `json:"reply"`
		} `json:"cases"`
	}
	responsibilityFixture(t, "native_facts_candidate.json", &doc)
	if !doc.Candidate || doc.Enabled || !doc.Synthetic || len(doc.Cases) != 3 {
		t.Fatal("wrong candidate scope")
	}
	for _, c := range doc.Cases {
		if command, err := DecodeCommand(c.Request, responsibilityCommandScope(), "cmd.query"); err != nil || command.Constraints == nil {
			t.Fatal("candidate syntax not registered independently of publication", err)
		}
		a := c.Reply["adapter_context"].(map[string]any)
		p := c.Reply["payload"].(map[string]any)
		depth := a["query_evidence"].(map[string]any)["depth"].(map[string]any)
		if a["atomic_snapshot"] != false {
			t.Fatal("non-atomic evidence promoted")
		}
		if c.Name == "missing_depth_terminal" {
			if c.Reply["status"] != "failed" || depth["is_last"] != false || p["items"] != nil || c.Reply["chunk"] != nil {
				t.Fatal("partial facts promoted to success")
			}
			continue
		}
		item := p["items"].([]any)[0].(map[string]any)
		if c.Reply["status"] != "completed" || depth["is_last"] != true || item["symbol"] != "rb2701" || item["exchange"] != "SHFE" {
			t.Fatal("exact target facts missing")
		}
		if c.Name == "complete_with_unavailable_facts" {
			d := item["dynamic"].(map[string]any)
			if d["upper_limit_price"] != nil || d["bid_price1"] != nil || d["ask_price1"] != nil || len(item["unavailable_numeric_fields"].([]any)) != 3 {
				t.Fatal("unavailable numeric facts replaced")
			}
		}
	}
}
