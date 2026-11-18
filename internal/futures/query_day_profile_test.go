package futures

import (
	"testing"
	"time"
)

func TestDayProfileInitializationQueryProof(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	row, scope, target, _ := freshOCTradeHeartbeat(t, now, false)
	row.Raw = editBody(t, deferredAssessmentBody(t, row.Raw), func(m map[string]any) {
		p := m["payload"].(map[string]any)
		m["state"], p["state"] = "DEGRADED", "DEGRADED"
		m["state_text"], p["state_text"] = "order_snapshot_not_ready", "order_snapshot_not_ready"
		for _, key := range []string{"order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"} {
			m[key], p[key] = false, false
		}
		a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		a["permission_profile"] = "test_day_relay.v1"
		a["constraint_symbol"] = "rb2701"
		a["per_command_native_validation"] = true
		a["trade_preparation_pending"], a["trade_response_pending"] = false, false
		a["position_snapshot_ready"], a["instrument_usable"], a["native_limits_fresh"] = false, false, false
		delete(a, "static_constraints")
		delete(a, "native_limits")
		a["native_depth_request_id"] = nil
		a["expires_at"] = "2026-11-17T15:00:00+08:00"
		a["process_lifecycle"] = map[string]any{"mode": "scheduled_assessment.v1", "starts_at": "2026-11-17T13:30:00+08:00", "expires_at": a["expires_at"], "phase": "active", "process_duration_seconds": 5400}
	})
	proof, err := testQueryHeartbeatProof(row, scope, target, now)
	if err != nil || !proof.ExpiresAt.Equal(now.Add(5*time.Second)) {
		t.Fatal("day initialization blocked or freshness extended", proof, err)
	}
	for _, late := range []time.Time{now.Add(5 * time.Second), now.Add(80 * time.Minute)} {
		if _, err := testQueryHeartbeatProof(row, scope, target, late); err == nil {
			t.Fatal("stale/expired query allowed")
		}
	}
	wrong := target
	wrong.CounterSessionID = "other-session"
	if _, err := testQueryHeartbeatProof(row, scope, wrong, now); err == nil {
		t.Fatal("wrong session allowed")
	}
	for _, key := range []string{"transport_recovery_clear", "blocking_callback_count", "permission_profile", "state_text"} {
		changed := row
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) {
			adapter := m["adapter_context"].(map[string]any)
			a := adapter["admission_evidence"].(map[string]any)
			switch key {
			case "transport_recovery_clear":
				adapter[key] = false
			case "blocking_callback_count":
				a[key], a["deferred_callback_count"] = 1, 63
			case "permission_profile":
				a[key] = "unknown"
			case "state_text":
				m[key], m["payload"].(map[string]any)[key] = "stopping", "stopping"
			}
		})
		if _, err := testQueryHeartbeatProof(changed, scope, target, now); err == nil {
			t.Fatal("invalid query gate accepted", key)
		}
	}
	prod := scope
	prod.Route.Env = "prod"
	if _, err := testQueryHeartbeatProof(row, prod, target, now); err == nil {
		t.Fatal("TEST authorization accepted in production")
	}
}
