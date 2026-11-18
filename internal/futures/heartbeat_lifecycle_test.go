package futures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lifecycleMaterials(t *testing.T, filename string) []struct {
	Scenario   string            `json:"scenario"`
	Heartbeats []json.RawMessage `json:"heartbeats"`
} {
	t.Helper()
	dir := os.Getenv("RELAY_OC_CTP_DAY_DIR")
	if dir == "" {
		t.Skip("set RELAY_OC_CTP_DAY_DIR to private OC delivery")
	}
	b, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{"scheduled-process.json": "a730ce598b59ab8f5b2cb04c99a34b40257fc9027ec9b25dc1656270107f318a", "day-permissions.json": "a5d2fd5a05e1f1293d636dcfd4070b5f60701a72b5f17ed9fee2559be7ff035a"}
	if commandDigest(b) != pins[filename] {
		t.Fatal("OC material bytes changed")
	}
	var doc struct {
		RealSDK   bool `json:"real_sdk"`
		RealRedis bool `json:"real_redis"`
		Cases     []struct {
			Scenario   string            `json:"scenario"`
			Heartbeats []json.RawMessage `json:"heartbeats"`
		} `json:"cases"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.RealSDK || doc.RealRedis || len(doc.Cases) == 0 {
		t.Fatal("invalid offline delivery")
	}
	return doc.Cases
}

func TestScheduledTradePublicationBoundaries(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:30:02+08:00")
	row, scope, target, approval := freshOCTradeHeartbeat(t, now, false)
	withLifecycle := func(phase string) StreamInput {
		changed := row
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) {
			a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
			a["expires_at"] = assessmentTime(now.Add(2 * time.Second))
			a["process_lifecycle"] = map[string]any{"mode": "scheduled_assessment.v1", "starts_at": "2026-11-17T13:30:00+08:00", "expires_at": a["expires_at"], "phase": phase, "process_duration_seconds": 14400}
		})
		return changed
	}
	active := withLifecycle("active")
	proof, err := testTradeHeartbeatProof(active, scope.Route, target, approval, now)
	if err != nil || !proof.ExpiresAt.Equal(now.Add(2*time.Second)) {
		t.Fatal("window not capped", proof, err)
	}
	if _, err := testTradeHeartbeatProof(active, scope.Route, target, approval, now.Add(2*time.Second)); err == nil {
		t.Fatal("expired authorization published")
	}
	for _, phase := range []string{"waiting", "expired"} {
		if _, err := DecodeHeartbeat(withLifecycle(phase).Raw, scope.Route); err == nil {
			t.Fatal("ready outside active phase accepted")
		}
	}
	day := withLifecycle("active")
	day.Raw = editBody(t, day.Raw, func(m map[string]any) {
		a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		a["permission_profile"] = "test_day_relay.v1"
		a["constraint_symbol"] = "rb2701"
		a["per_command_native_validation"] = true
		a["trade_preparation_pending"] = false
		a["trade_response_pending"] = false
	})
	if _, err := DecodeHeartbeat(day.Raw, scope.Route); err != nil {
		t.Fatal("day observation", err)
	}
	if _, err := testTradeHeartbeatProof(day, scope.Route, target, approval, now); err == nil {
		t.Fatal("day profile reused legacy trade publisher")
	}
	for _, key := range []string{"trade_preparation_pending", "trade_response_pending"} {
		bad := editBody(t, day.Raw, func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)[key] = true
		})
		if _, err := DecodeHeartbeat(bad, scope.Route); err == nil {
			t.Fatal("pending state advertised ready", key)
		}
	}
}

func TestOCScheduledAndDayHeartbeatMaterials(t *testing.T) {
	for _, filename := range []string{"scheduled-process.json", "day-permissions.json"} {
		count := 0
		for _, c := range lifecycleMaterials(t, filename) {
			for i, raw := range c.Heartbeats {
				var e struct {
					Route QueryRoute `json:"routing"`
				}
				if err := json.Unmarshal(raw, &e); err != nil {
					t.Fatal(err)
				}
				h, err := DecodeHeartbeat(raw, e.Route)
				if err != nil {
					t.Fatalf("%s/%s/%d: %v", filename, c.Scenario, i, err)
				}
				if h.Assessment == nil || h.Assessment.Lifecycle == nil {
					t.Fatal("lifecycle missing")
				}
				_, ready := h.queryCollection()
				if h.Assessment.Lifecycle.Phase != "active" && ready {
					t.Fatal("query admitted outside window")
				}
				count++
			}
		}
		t.Logf("%s: %d original heartbeats decoded", filename, count)
	}
}

func TestAssessmentLifecycleBoundaries(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, "2026-11-18T13:30:00+08:00")
	a := HeartbeatAssessment{ExpiresAt: start.Add(5 * time.Minute)}
	route := QueryRoute{"test", "ctp_4900", "ctp-dongwu-assessment-pm-20261117", "300052141"}
	makeRaw := func(profile bool) rawObject {
		m := map[string]any{"process_lifecycle": map[string]any{"mode": "scheduled_assessment.v1", "starts_at": start.Format(time.RFC3339), "expires_at": a.ExpiresAt.Format(time.RFC3339), "phase": "active", "process_duration_seconds": 14400}}
		if profile {
			m["permission_profile"] = "test_day_relay.v1"
			m["constraint_symbol"] = "rb2701"
			m["per_command_native_validation"] = true
			m["trade_preparation_pending"] = false
			m["trade_response_pending"] = false
		}
		b, _ := json.Marshal(m)
		var r rawObject
		_ = json.Unmarshal(b, &r)
		return r
	}
	h := Heartbeat{ProducedAt: start, TradeDate: "20261118"}
	if err := a.decodeLifecycle(makeRaw(false), h, route); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []time.Duration{-time.Second, 5 * time.Minute, 6 * time.Minute} {
		if a.windowOpen(start.Add(delta)) {
			t.Fatal("outside window admitted", delta)
		}
	}
	a.Lifecycle.Phase = "waiting"
	if a.windowOpen(start) {
		t.Fatal("waiting admitted")
	}
	a.Lifecycle.Phase = "expired"
	if a.windowOpen(start) {
		t.Fatal("expired reopened after clock rollback")
	}
	for _, change := range []func(rawObject){
		func(r rawObject) { r["permission_profile"] = json.RawMessage(`"unknown"`) },
		func(r rawObject) { r["process_lifecycle"] = json.RawMessage(`null`) },
		func(r rawObject) { r["trade_preparation_pending"] = json.RawMessage(`"false"`) },
		func(r rawObject) { delete(r, "per_command_native_validation") },
	} {
		r := makeRaw(true)
		change(r)
		copy := HeartbeatAssessment{ExpiresAt: a.ExpiresAt}
		if copy.decodeLifecycle(r, h, route) == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	for _, clock := range []string{"2026-11-18T10:15:00+08:00", "2026-11-18T11:30:00+08:00", "2026-11-18T13:29:59+08:00", "2026-11-18T15:00:00+08:00"} {
		now, _ := time.Parse(time.RFC3339, clock)
		if assessmentDaySegment(now) != 0 {
			t.Fatal("break admitted", clock)
		}
	}
}
