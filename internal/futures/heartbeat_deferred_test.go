package futures

import (
	"testing"
	"time"
)

func deferredAssessmentBody(t *testing.T, raw []byte) []byte {
	t.Helper()
	return editBody(t, raw, func(m map[string]any) {
		a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		a["callback_policy"] = "local_assessment_only.v1"
		a["pending_callback_count"] = 64
		a["deferred_callback_count"] = 64
		a["blocking_callback_count"] = 0
		a["callback_reconciliation_complete"] = false
	})
}

func TestAssessmentDeferredCallbacksRemainUnreconciled(t *testing.T) {
	now := time.Now()
	for _, cancel := range []bool{false, true} {
		row, scope, target, approval := freshOCTradeHeartbeat(t, now, cancel)
		row.Raw = deferredAssessmentBody(t, row.Raw)
		h, err := DecodeHeartbeat(row.Raw, scope.Route)
		if err != nil {
			t.Fatal(err)
		}
		if h.Assessment.PendingCallbacks != 64 || h.Assessment.DeferredCallbacks != 64 || h.Assessment.CallbackReconciliationComplete || h.Assessment.AssetScopeVerified {
			t.Fatal("deferred callbacks marked reconciled")
		}
		if _, ready := h.queryCollection(); !ready {
			t.Fatal("query blocked")
		}
		if _, err := testTradeHeartbeatProof(row, scope.Route, target, approval, now); err != nil {
			t.Fatal(err)
		}
		if _, err := testTradeHeartbeatProof(row, scope.Route, target, approval, now.Add(6*time.Second)); err == nil {
			t.Fatal("deferral bypassed freshness")
		}
		changed := row
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) { m["adapter_context"].(map[string]any)["transport_recovery_clear"] = false })
		if _, err := testTradeHeartbeatProof(changed, scope.Route, target, approval, now); err == nil {
			t.Fatal("deferral bypassed recovery")
		}
		prod := scope.Route
		prod.Env = "prod"
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" })
		if _, err := DecodeHeartbeat(changed.Raw, prod); err == nil {
			t.Fatal("TEST policy enabled production")
		}
	}
}

func TestAssessmentDeferredPolicyFailsClosed(t *testing.T) {
	raw := deferredAssessmentBody(t, ocAssessmentCases(t)[0].Heartbeats[6])
	for _, key := range []string{"callback_policy", "deferred_callback_count", "blocking_callback_count", "callback_reconciliation_complete"} {
		t.Run("missing_"+key, func(t *testing.T) {
			changed := editBody(t, raw, func(m map[string]any) {
				delete(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any), key)
			})
			if _, err := DecodeHeartbeat(changed, ocAssessmentRoute()); err == nil {
				t.Fatal("partial policy accepted")
			}
		})
	}
	for _, test := range []struct {
		key   string
		value any
	}{
		{"callback_policy", "unknown"}, {"callback_policy", nil},
		{"deferred_callback_count", -1}, {"deferred_callback_count", 65}, {"deferred_callback_count", 63},
		{"deferred_callback_count", "64"}, {"deferred_callback_count", 1.5}, {"deferred_callback_count", nil},
		{"blocking_callback_count", -1}, {"blocking_callback_count", 1},
		{"callback_reconciliation_complete", true}, {"callback_reconciliation_complete", nil}, {"callback_reconciliation_complete", "false"},
	} {
		changed := editBody(t, raw, func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)[test.key] = test.value
		})
		if _, err := DecodeHeartbeat(changed, ocAssessmentRoute()); err == nil {
			t.Fatalf("invalid policy accepted: %s=%v", test.key, test.value)
		}
	}
	// A valid count partition with one blocking callback still forbids readiness.
	changed := editBody(t, raw, func(m map[string]any) {
		a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		a["deferred_callback_count"], a["blocking_callback_count"] = 63, 1
	})
	if _, err := DecodeHeartbeat(changed, ocAssessmentRoute()); err == nil {
		t.Fatal("blocking callback accepted as ready")
	}
}

func TestAssessmentDeferredInitializationAndACKUnknown(t *testing.T) {
	raw := deferredAssessmentBody(t, ocAssessmentCases(t)[0].Heartbeats[2])
	h, err := DecodeHeartbeat(raw, ocAssessmentRoute())
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := h.queryCollection(); !ready {
		t.Fatal("initialization deadlock")
	}
	raw = editBody(t, raw, func(m map[string]any) {
		m["adapter_context"].(map[string]any)["transport_recovery_clear"] = false
		m["state_text"] = "recovery_blocked"
		m["payload"].(map[string]any)["state_text"] = "recovery_blocked"
	})
	h, err = DecodeHeartbeat(raw, ocAssessmentRoute())
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := h.queryCollection(); ready {
		t.Fatal("unknown ACK allowed query")
	}
}

func TestAssessmentInspectionShowsUnresolvedCallbacks(t *testing.T) {
	raw := deferredAssessmentBody(t, ocAssessmentCases(t)[0].Heartbeats[6])
	f, req := inspectFixture(t, ocAssessmentRoute(), raw)
	out, err := InspectTestHeartbeats(inspectContext(t), f, req)
	if err != nil || out.BusinessAuthorized || len(out.Records) != 1 {
		t.Fatal(out, err)
	}
	a := out.Records[0].Advertised
	if a == nil || a.CallbackPolicy != "local_assessment_only.v1" || a.PendingCallbacks == nil || *a.PendingCallbacks != 64 || a.DeferredCallbacks == nil || *a.DeferredCallbacks != 64 || a.BlockingCallbacks == nil || *a.BlockingCallbacks != 0 || a.CallbackReconciliationComplete == nil || *a.CallbackReconciliationComplete {
		t.Fatal("inspection hides unresolved callback responsibility", a)
	}
}
