package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withAssessmentLegacy(t *testing.T, raw []byte, s CommandScope) []byte {
	t.Helper()
	return editBody(t, raw, func(m map[string]any) {
		a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		a["callback_policy"], a["deferred_callback_count"], a["blocking_callback_count"], a["callback_reconciliation_complete"] = "local_assessment_only.v1", 0, 0, true
		a["legacy_source"] = AssessmentLegacySource{strings.Repeat("a", 64), ReadOnlySourceScope{s.Route.Env, s.Route.BrokerID, s.Route.AccountID, "old-test-scope", s.NativeBrokerID, s.InvestorID, s.UserID}, AssessmentLegacyDiagnostics{Retained: 50, Pending: 32, AuditOnly: 18}}
	})
}

func TestAssessmentLegacyStrict(t *testing.T) {
	row, scope, target, approval := freshOCTradeHeartbeat(t, time.Now(), false)
	row.Raw = withAssessmentLegacy(t, row.Raw, scope)
	h, err := DecodeHeartbeat(row.Raw, scope.Route)
	if err != nil || h.Assessment.LegacySource.Diagnostics.Pending != 32 {
		t.Fatal(err)
	}
	if _, err := testTradeHeartbeatProof(row, scope.Route, target, approval, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"null":       func(m map[string]any) { m["diagnostics"] = nil },
		"hash":       func(m map[string]any) { m["manifest_sha256"] = "bad" },
		"count":      func(m map[string]any) { m["diagnostics"].(map[string]any)["source_callback_count"] = 51 },
		"bool":       func(m map[string]any) { m["diagnostics"].(map[string]any)["original_pending_count"] = true },
		"string":     func(m map[string]any) { m["diagnostics"].(map[string]any)["linked_callback_count"] = "0" },
		"overflow":   func(m map[string]any) { m["diagnostics"].(map[string]any)["query_observation_count"] = 10001 },
		"linked":     func(m map[string]any) { m["diagnostics"].(map[string]any)["linked_callback_count"] = 33 },
		"conflict":   func(m map[string]any) { m["diagnostics"].(map[string]any)["blocking_conflict"] = true },
		"processed":  func(m map[string]any) { m["diagnostics"].(map[string]any)["business_processed_count"] = 1 },
		"mutated":    func(m map[string]any) { m["diagnostics"].(map[string]any)["old_outcomes_mutated"] = true },
		"authorized": func(m map[string]any) { m["diagnostics"].(map[string]any)["business_publication_authorized"] = true },
		"scope":      func(m map[string]any) { m["source_scope"].(map[string]any)["account_id"] = "990000000019" },
		"extra":      func(m map[string]any) { m["allow_trading"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			bad := editBody(t, row.Raw, func(m map[string]any) {
				mutate(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["legacy_source"].(map[string]any))
			})
			if _, err := DecodeHeartbeat(bad, scope.Route); err == nil {
				t.Fatal("invalid legacy accepted")
			}
		})
	}
	for _, change := range []string{"hash", "counts", "scope", "removed", "native"} {
		t.Run("pin_"+change, func(t *testing.T) {
			cfg := sourceFixture()
			cfg.Scope = scope
			s, err := NewTestStreamSource(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			g, err := NewTestSessionGuard(s, DeliveryQualityContext{TradeScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, h.TradeDate}, h.CounterSessionID, 1}, DefaultTestHeartbeatPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if err = g.observeAt(guardSource(g, "1-0"), row.Raw, h.ProducedAt); err != nil {
				t.Fatal(err)
			}
			next := editBody(t, row.Raw, func(m map[string]any) {
				m["message_id"], m["produced_at"] = "next", assessmentTime(h.ProducedAt.Add(time.Second))
				a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
				l := a["legacy_source"].(map[string]any)
				switch change {
				case "hash":
					l["manifest_sha256"] = strings.Repeat("b", 64)
				case "counts":
					l["diagnostics"].(map[string]any)["original_audit_only_count"] = 19
					l["diagnostics"].(map[string]any)["source_callback_count"] = 51
				case "scope":
					l["source_scope"].(map[string]any)["gateway_id"] = "another-old"
				case "removed":
					delete(a, "legacy_source")
				case "native":
					l["source_scope"].(map[string]any)["investor_id"] = "other"
				}
			})
			if err = g.observeAt(guardSource(g, "2-0"), next, h.ProducedAt.Add(time.Second)); err == nil {
				t.Fatal("source drift accepted")
			}
		})
	}
}

func TestAssessmentUnlinkedExternalReplayDoesNotBlockLocalFlow(t *testing.T) {
	now := time.Now()
	for _, cancel := range []bool{false, true} {
		row, scope, target, approval := freshOCTradeHeartbeat(t, now, cancel)
		row.Raw = deferredAssessmentBody(t, withAssessmentLegacy(t, row.Raw, scope))
		h, err := DecodeHeartbeat(row.Raw, scope.Route)
		if err != nil {
			t.Fatal(err)
		}
		d := h.Assessment.LegacySource.Diagnostics
		if d.Linked != 0 || d.QueryObservations != 0 || d.BusinessProcessed != 0 || d.Pending != 32 || h.Assessment.CallbackReconciliationComplete {
			t.Fatal("external replay must remain unresolved")
		}
		if _, ready := h.queryCollection(); !ready {
			t.Fatal("unlinked external replay blocked initialization")
		}
		if _, err := testTradeHeartbeatProof(row, scope.Route, target, approval, now); err != nil {
			t.Fatal("unlinked external replay blocked local submit/cancel", err)
		}
		// Isolation cannot turn a local execution conflict into a deferred item.
		h.Assessment.BlockingCallbacks = 1
		h.Assessment.DeferredCallbacks--
		if h.Assessment.callbacksAllowAssessment() {
			t.Fatal("local blocking callback ignored")
		}
	}
}

type ocNextdayCase struct {
	Scenario   string            `json:"scenario"`
	Audit      string            `json:"audit_prefix"`
	Heartbeats []json.RawMessage `json:"heartbeats"`
	Outputs    []json.RawMessage `json:"outputs"`
	Inputs     []struct {
		Body string `json:"body"`
	} `json:"inputs"`
	Rejected []struct {
		Body json.RawMessage `json:"body"`
	} `json:"rejected_heartbeats"`
}

func nextdayCases(t *testing.T) []ocNextdayCase {
	t.Helper()
	dir := os.Getenv("RELAY_OC_CTP_NEXTDAY_REVIEW_DIR")
	if fixed := os.Getenv("RELAY_OC_CTP_AUDIT_GROUP_FIX_DIR"); fixed != "" {
		dir = fixed
	}
	if dir == "" {
		t.Skip("explicit private OC nextday review required")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "nextday-recorded.json"))
	pin := commandDigest(raw)
	fixed := pin == "cd5a16bbcaa831c4a75aa1ef6522ba145a6c6f41940ee4da4e62f0cdbd62c68e"
	if err != nil || (!fixed && pin != "f7c6e2dd56b7d13fec08a0e071f8169329290bd48f22bda4a2287138fc1b9ed6") {
		t.Fatal("original evidence", err)
	}
	var doc struct {
		RealSDK   bool            `json:"real_sdk"`
		RealRedis bool            `json:"real_redis"`
		Day       string          `json:"simulation_trade_date"`
		Cases     []ocNextdayCase `json:"cases"`
	}
	want := 7
	if fixed {
		want = 8
	}
	if err = json.Unmarshal(raw, &doc); err != nil || doc.RealSDK || doc.RealRedis || doc.Day != "20261118" || len(doc.Cases) != want {
		t.Fatal("evidence boundary", err)
	}
	return doc.Cases
}

func nextdayRoute() QueryRoute {
	return QueryRoute{"test", "ctp_4900", "ctp-dongwu-readonly-recovery-20261117", "300052141"}
}

func TestOCNextdayDelivery(t *testing.T) {
	n, bad, events, pages := 0, 0, 0, 0
	cases := nextdayCases(t)
	for _, c := range cases {
		origin := ""
		for _, in := range c.Inputs {
			var o rawObject
			if json.Unmarshal([]byte(in.Body), &o) != nil {
				t.Fatal("command")
			}
			if textField(o, "action") == "order.submit" {
				origin = textField(o, "message_id")
			}
		}
		for _, raw := range c.Heartbeats {
			h, err := DecodeHeartbeat(raw, nextdayRoute())
			if err != nil || h.Assessment == nil || h.Assessment.LegacySource == nil || !bytes.Equal(raw, h.Raw) {
				t.Fatal(c.Scenario, err)
			}
			l := h.Assessment.LegacySource.Diagnostics
			if l.Pending != 32 || l.AuditOnly != 18 || l.Linked != 0 {
				t.Fatal("old source cleared")
			}
			_, ready := h.queryCollection()
			if oneOf(c.Scenario, "ack_unknown", "callback_conflict") && (ready || h.AcceptingTradeCommands || h.AcceptingCancelCommands) {
				t.Fatal("blocked session ready")
			}
			n++
		}
		for _, r := range c.Rejected {
			if _, err := DecodeHeartbeat(r.Body, nextdayRoute()); err == nil {
				t.Fatal("negative accepted", c.Scenario)
			}
			bad++
		}
		for _, raw := range c.Outputs {
			var o rawObject
			if json.Unmarshal(raw, &o) != nil {
				t.Fatal("output")
			}
			if textField(o, "message_type") == "event" {
				e, err := DecodeTradeEvent(raw, EventContext{Scope: TradeScope{nextdayRoute(), "4900", "300052141", "20261118"}, ExpectedOrigin: &origin})
				if err != nil || !bytes.Equal(raw, e.Raw) {
					t.Fatal(c.Scenario, err)
				}
				events++
			} else if strings.HasSuffix(textField(o, "action"), ".query") {
				var p struct {
					Adapter struct {
						E struct {
							Ticket  string `json:"ticket"`
							Session string `json:"counter_session_id"`
							Gen     *int64 `json:"connection_generation"`
							SDK     *int64 `json:"sdk_request_id"`
						} `json:"query_evidence"`
					} `json:"adapter_context"`
				}
				if json.Unmarshal(raw, &p) != nil {
					t.Fatal("page")
				}
				// Offline shape replay only; real request identity must come from independent audit.
				q := QueryContext{Route: nextdayRoute(), Action: textField(o, "action"), OriginMessageID: textField(o, "origin_message_id"), Ticket: p.Adapter.E.Ticket, CounterSessionID: p.Adapter.E.Session, ConnectionGeneration: p.Adapter.E.Gen, SDKRequestID: p.Adapter.E.SDK, TradeDate: "20261118", NativeBrokerID: "4900", NativeInvestorID: "300052141", NativeUserID: "300052141"}
				b, err := NewQueryBatch(q, 0, time.Minute, DefaultBatchLimits())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = b.Accept(raw, 0); err != nil {
					t.Fatal(c.Scenario, err)
				}
				want := BatchReady
				if textField(o, "status") == "failed" {
					want = BatchFailed
				}
				assertState(t, b, want)
				pages++
			}
		}
	}
	wantHeartbeats := 63
	if len(cases) == 8 {
		wantHeartbeats = 68
	}
	if n != wantHeartbeats || bad != 8 || events != 7 || pages != 9 {
		t.Fatal("coverage", n, bad, events, pages)
	}
}

func TestOCNextdayEventPostgres(t *testing.T) {
	for _, c := range nextdayCases(t) {
		if !oneOf(c.Scenario, "current_empty", "duplicate", "fill_before_order", "cancel_rejected") {
			continue
		}
		t.Run(c.Scenario, func(t *testing.T) {
			db, trades, ctx := tradeTestDB(t)
			events, _ := NewEventStore(db)
			scope := TradeScope{nextdayRoute(), "4900", "300052141", "20261118"}
			key, err := events.RegisterScope(ctx, scope, "300052141", eventStream(scope))
			if err != nil {
				t.Fatal(err)
			}
			for _, in := range c.Inputs {
				var o rawObject
				json.Unmarshal([]byte(in.Body), &o)
				if textField(o, "action") == "order.submit" {
					if _, err := registerEventCommand(t, events, ctx, key, []byte(in.Body)); err != nil {
						t.Fatal(err)
					}
				}
			}
			for i, raw := range c.Outputs {
				var o rawObject
				json.Unmarshal(raw, &o)
				if textField(o, "message_type") != "event" {
					continue
				}
				source := QuerySource{eventStream(scope), fmt.Sprintf("%d-0", i+1)}
				receipt, err := events.Receive(ctx, key, source, raw)
				if err != nil {
					t.Fatal(err)
				}
				processEvent(t, events, ctx, receipt.EventID, "applied")
				events, _ = NewEventStore(db)
				again, err := events.Receive(ctx, key, source, raw)
				if err != nil || again.EventID != receipt.EventID {
					t.Fatal("repeat", err)
				}
				processEvent(t, events, ctx, again.EventID, "applied")
			}
			r, err := trades.Read(ctx, scope)
			if err != nil || r.Blocked || len(r.Orders) != 1 {
				t.Fatal(r, err)
			}
			want := "cancelled"
			fills := 0
			if c.Scenario == "fill_before_order" {
				want = "filled"
				fills = 1
			}
			if c.Scenario == "cancel_rejected" {
				want = "working"
			}
			if r.Orders["fixture-order"] == nil || r.Orders["fixture-order"].Order.GatewayStatus != want || len(r.Executions) != fills {
				t.Fatal("unexpected final projection", r)
			}
		})
	}
}
