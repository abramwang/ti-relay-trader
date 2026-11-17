package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type ocAssessmentCase struct {
	Scenario        string            `json:"scenario"`
	Synthetic       bool              `json:"synthetic_only"`
	ExitCode        int               `json:"exit_code"`
	SubmitCount     int               `json:"sdk_submit_count"`
	CancelCount     int               `json:"sdk_cancel_count"`
	SettlementCount int               `json:"sdk_settlement_count"`
	Heartbeats      []json.RawMessage `json:"heartbeats"`
	Outputs         []json.RawMessage `json:"outputs"`
}

func ocAssessmentCases(t *testing.T) []ocAssessmentCase {
	t.Helper()
	dir := "../../testdata/oc-assessment-c83731d-20261117/assessment_review_20261117"
	raw, err := os.ReadFile(filepath.Join(dir, "runtime.examples.json"))
	if err != nil {
		t.Fatal(err)
	}
	if commandDigest(raw) != "7c05889470381c0195532bb63c57b6260603c409e1c1df992e89cbf94caef8ed" {
		t.Fatal("OC original fixture changed")
	}
	log, err := os.ReadFile(filepath.Join(dir, "runtime-test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log, bytes.TrimSpace(raw)) {
		t.Fatal("JSON not found verbatim in same runtime output")
	}
	var doc struct {
		Synthetic  bool               `json:"synthetic_only"`
		RealBroker bool               `json:"real_broker"`
		RealRedis  bool               `json:"real_redis"`
		Cases      []ocAssessmentCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || !doc.Synthetic || doc.RealBroker || doc.RealRedis || len(doc.Cases) != 7 {
		t.Fatal("wrong evidence scope", err)
	}
	return doc.Cases
}

func ocAssessmentRoute() QueryRoute {
	return QueryRoute{Env: "test", BrokerID: "ctp_4900", GatewayID: "ctp-dongwu-asset-retest-20261117", AccountID: "300052141"}
}

func TestOCOfflineAssessmentHeartbeatsAndEvents(t *testing.T) {
	count, events := 0, 0
	for _, c := range ocAssessmentCases(t) {
		t.Run(c.Scenario, func(t *testing.T) {
			if !c.Synthetic {
				t.Fatal("missing synthetic label")
			}
			for _, raw := range c.Heartbeats {
				h, err := DecodeHeartbeat(raw, ocAssessmentRoute())
				if err != nil {
					t.Fatal(err)
				}
				if h.Assessment == nil || h.ReadOnly != nil || h.Recovery != nil || h.Assessment.AssetScopeVerified || !bytes.Equal(h.Raw, raw) {
					t.Fatal("profile or audit changed")
				}
				g, ready := h.queryCollection()
				if h.StateText == "order_snapshot_not_ready" && (!ready || g == nil) {
					t.Fatal("initialization deadlock")
				}
				if oneOf(h.StateText, "broker_not_ready", "recovery_blocked", "stopping") && ready {
					t.Fatal("unsafe query readiness")
				}
				count++
			}
			for _, raw := range c.Outputs {
				var m map[string]json.RawMessage
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatal(err)
				}
				if textField(m, "message_type") != "event" {
					continue
				}
				origin := textField(m, "origin_message_id")
				e, err := DecodeTradeEvent(raw, EventContext{Scope: TradeScope{Route: ocAssessmentRoute(), NativeBrokerID: "4900", InvestorID: "300052141", TradeDate: "20261117"}, ExpectedOrigin: &origin})
				if err != nil {
					t.Fatal(err)
				}
				if e.Order == nil || e.Order.OrderQty != 1 || e.Order.Symbol != "rb2701" || e.Order.NativeIDs.OrderSysID != "0000000088" || !bytes.Equal(e.Raw, raw) {
					t.Fatal("order identity changed")
				}
				events++
			}
			if oneOf(c.Scenario, "happy", "duplicate_submit") && (c.ExitCode != 0 || c.SubmitCount != 1 || c.CancelCount != 1 || c.SettlementCount != 1) {
				t.Fatal("OC offline budget result changed")
			}
		})
	}
	if count != 48 || events != 4 {
		t.Fatal("unexpected coverage", count, events)
	}
}

func TestAssessmentModeMalformedAndProductionRejected(t *testing.T) {
	raw := ocAssessmentCases(t)[0].Heartbeats[6]
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" },
		func(m map[string]any) { m["accepting_trade_commands"] = false },
		func(m map[string]any) { delete(m["adapter_context"].(map[string]any), "connection_generation") },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = 0 },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["runtime_mode"] = "test_broker_unknown" },
		func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = nil },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["transport_recovery_clear"] = false },
	} {
		if _, err := DecodeHeartbeat(editBody(t, raw, mutate), ocAssessmentRoute()); err == nil {
			t.Fatal("unsafe heartbeat accepted")
		}
	}
	prod := ocAssessmentRoute()
	prod.Env = "prod"
	if _, err := DecodeHeartbeat(editBody(t, raw, func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" }), prod); err == nil {
		t.Fatal("assessment allowed in production")
	}
	for _, key := range []string{"asset_scope_verified", "position_snapshot_ready", "native_limits_fresh", "order_ref_ready", "instrument_usable", "submit_attempts", "pending_callback_count", "native_depth_request_id", "static_constraints", "native_limits", "assessment_id", "expires_at"} {
		changed := editBody(t, raw, func(m map[string]any) {
			delete(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any), key)
		})
		if _, err := DecodeHeartbeat(changed, ocAssessmentRoute()); err == nil {
			t.Fatal("missing readiness fact", key)
		}
	}
	for key, value := range map[string]any{"asset_scope_verified": true, "submit_attempts": 1, "pending_callback_count": 1, "native_limits_fresh": false, "position_snapshot_ready": false} {
		changed := editBody(t, raw, func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)[key] = value
		})
		if _, err := DecodeHeartbeat(changed, ocAssessmentRoute()); err == nil {
			t.Fatal("contradictory readiness", key)
		}
	}
}

func freshOCTradeHeartbeat(t *testing.T, now time.Time, cancel bool) (StreamInput, CommandScope, TestQueryTarget, TestTradeApproval) {
	t.Helper()
	index := 6
	if cancel {
		index = 7
	}
	raw := ocAssessmentCases(t)[0].Heartbeats[index]
	raw = editBody(t, raw, func(m map[string]any) {
		m["produced_at"] = assessmentTime(now)
		a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		a["expires_at"] = assessmentTime(now.Add(4 * time.Minute))
		a["static_constraints"].(map[string]any)["captured_at"] = assessmentTime(now.Add(-time.Second))
		l := a["native_limits"].(map[string]any)
		l["started_at"] = assessmentTime(now.Add(-time.Second))
		l["completed_at"] = assessmentTime(now.Add(-500 * time.Millisecond))
	})
	h, err := DecodeHeartbeat(raw, ocAssessmentRoute())
	if err != nil {
		t.Fatal(err)
	}
	_, _, target, a := assessmentFixture(t)
	target.CounterSessionID = h.CounterSessionID
	a.AssessmentID = h.Assessment.AssessmentID
	a.ExpiresAt = assessmentTime(now.Add(30 * time.Second))
	a.Market.ObservedAt = assessmentTime(now)
	a.Market.SourceInstrumentID = "rb2701"
	if cancel {
		a.CancelReceiptID = 1
		a.Market = nil
	}
	scope := CommandScope{Route: ocAssessmentRoute(), NativeBrokerID: "4900", UserID: "300052141", InvestorID: "300052141"}
	return StreamInput{EntryID: "1-0", Raw: raw}, scope, target, a
}

func TestAssessmentTradeProofNativeLimitsAndIndependentCancel(t *testing.T) {
	now := time.Now()
	row, scope, target, a := freshOCTradeHeartbeat(t, now, false)
	proof, err := testTradeHeartbeatProof(row, scope.Route, target, a, now)
	if err != nil {
		t.Fatal(err)
	}
	if proof.ExpiresAt.After(now.Add(4 * time.Second)) {
		t.Fatal("native request freshness not bounded")
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["assessment_id"] = "other" },
		func(m map[string]any) { m["expires_at"] = assessmentTime(now) },
		func(m map[string]any) { m["static_constraints"].(map[string]any)["price_tick"] = "2" },
		func(m map[string]any) {
			m["static_constraints"].(map[string]any)["captured_at"] = assessmentTime(now.Add(-301 * time.Second))
		},
		func(m map[string]any) {
			l := m["native_limits"].(map[string]any)
			l["started_at"] = assessmentTime(now.Add(-6 * time.Second))
		},
		func(m map[string]any) { m["static_constraints"].(map[string]any)["min_limit_lots"] = 2 },
	} {
		changed := row
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) {
			change(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any))
		})
		if _, err := testTradeHeartbeatProof(changed, scope.Route, target, a, now); err == nil {
			t.Fatal("invalid native readiness accepted")
		}
	}
	cancelRow, scope, target, cancelApproval := freshOCTradeHeartbeat(t, now, true)
	cancelRow.Raw = editBody(t, cancelRow.Raw, func(m map[string]any) {
		e := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		e["native_limits_fresh"] = false
		e["instrument_usable"] = false
		delete(e, "native_limits")
		delete(e, "static_constraints")
	})
	if _, err := testTradeHeartbeatProof(cancelRow, scope.Route, target, cancelApproval, now); err != nil {
		t.Fatal("cancel wrongly requires quotes", err)
	}
	// Original samples deliberately use separate synthetic business and capture
	// clocks. Decoder acceptance does not make those samples live price evidence.
	original := ocAssessmentCases(t)[0].Heartbeats[6]
	h, _ := DecodeHeartbeat(original, scope.Route)
	a.ExpiresAt = assessmentTime(h.ProducedAt.Add(30 * time.Second))
	a.Market.ObservedAt = assessmentTime(h.ProducedAt)
	if _, err := testTradeHeartbeatProof(StreamInput{"1-0", original}, scope.Route, target, a, h.ProducedAt); err == nil {
		t.Fatal("offline old native limits treated as live")
	}
}

func TestAssessmentNativePriceAndEventAction(t *testing.T) {
	row, scope, _, _ := freshOCTradeHeartbeat(t, time.Now(), false)
	h, err := DecodeHeartbeat(row.Raw, scope.Route)
	if err != nil {
		t.Fatal(err)
	}
	v, _, _, _ := assessmentFixture(t)
	raw := editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = scope.Route.AccountID })
	if err := assessmentNativePrice(raw, scope, h); err != nil {
		t.Fatal(err)
	}
	for _, price := range []string{"2799", "3201", "2964.5"} {
		changed := editBody(t, raw, func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = price })
		if err := assessmentNativePrice(changed, scope, h); err == nil {
			t.Fatal("native price violation", price)
		}
	}
	event := ocAssessmentCases(t)[0].Outputs[3]
	ec := EventContext{Scope: TradeScope{Route: scope.Route, NativeBrokerID: scope.NativeBrokerID, InvestorID: scope.InvestorID, TradeDate: "20261117"}}
	for _, action := range []any{"order.cancel", "order.batch.submit", "other", nil} {
		if _, err := DecodeTradeEvent(editBody(t, event, func(m map[string]any) { m["action"] = action }), ec); err == nil {
			t.Fatal("unagreed event action accepted", action)
		}
	}
}
