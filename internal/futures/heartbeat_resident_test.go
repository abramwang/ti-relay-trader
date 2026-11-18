package futures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const residentProgressDir = "../../testdata/oc_resident_progress_20261118"

func TestResidentProgressOriginalDigests(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(residentProgressDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 8 {
		t.Fatal("unexpected inventory")
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 2 || filepath.Base(f[1]) != f[1] {
			t.Fatal("invalid manifest")
		}
		raw, err := os.ReadFile(filepath.Join(residentProgressDir, f[1]))
		if err != nil || commandDigest(raw) != f[0] {
			t.Fatal("changed material", f[1], err)
		}
	}
}

// Synthetic diagnostics based on the delivered field specification. The
// progress bundle contains summary logs, not complete encoded OC heartbeats.
func syntheticResidentUsage(t *testing.T, used int64) map[string]any {
	t.Helper()
	u := ExecutionResourceUsage{QueryCommandsReceived: 2001, Journal: JournalResourceUsage{Revision: 1, Metrics: map[string]JournalResourceMetric{}}}
	for _, key := range []string{"file_bytes", "commands", "messages", "deliveries", "conflicts", "callbacks", "events"} {
		limit := int64(10000)
		if key == "file_bytes" {
			limit = 64 * 1024 * 1024
		}
		if key == "commands" {
			limit = 2000
		}
		m := JournalResourceMetric{Limit: limit, Remaining: limit}
		if key == "callbacks" {
			m.Used = used
			m.Remaining = max(int64(0), limit-used)
			m.Warning, m.Exhausted = used >= 8000, used >= limit
			u.Journal.Warning, u.Journal.Exhausted = m.Warning, m.Exhausted
		}
		u.Journal.Metrics[key] = m
	}
	b, _ := json.Marshal(u)
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		t.Fatal("synthetic usage")
	}
	return m
}

func syntheticResidentHeartbeat(t *testing.T, used int64, blocked bool) []byte {
	t.Helper()
	return editBody(t, executionAuthorityHeartbeats(t)[0], func(m map[string]any) {
		p := m["payload"].(map[string]any)
		adapter := m["adapter_context"].(map[string]any)
		a := adapter["admission_evidence"].(map[string]any)
		a["process_lifecycle"].(map[string]any)["process_duration_seconds"] = 0
		a["resource_usage"] = syntheticResidentUsage(t, used)
		if blocked || used >= 10000 {
			reason := "journal_capacity_exhausted"
			if blocked {
				reason = "recovery_blocked"
				m["state_text"], p["state_text"] = reason, reason
				m["broker_ready"], p["broker_ready"] = false, false
				adapter["transport_recovery_clear"] = false
				a["runtime_blocked_reason"] = "synthetic_storage_failure"
			}
			m["accepting_query_commands"], p["accepting_query_commands"] = false, false
			adapter["operation_reasons"] = map[string]any{"query": reason, "submit": reason, "cancel": reason}
			a["query_execution"] = map[string]any{"ready": false, "reason": reason}
		}
	})
}

func TestResidentProgressZeroDurationAndResourceGates(t *testing.T) {
	for _, used := range []int64{0, 7999, 8000, 9999, 10000, 10001} {
		raw := syntheticResidentHeartbeat(t, used, false)
		h, err := DecodeHeartbeat(raw, responsibilityRoute())
		if err != nil {
			t.Fatal(used, err)
		}
		a := h.Assessment
		if a.ResidentDurationSeconds != 0 || a.Lifecycle != nil || !a.ExpiresAt.IsZero() || a.ResourceUsage.QueryCommandsReceived != 2001 || !bytes.Equal(raw, h.Raw) {
			t.Fatal("invented lifetime or modified raw")
		}
		if a.ResourceUsage.Journal.Warning != (used >= 8000) || a.ResourceUsage.Journal.Exhausted != (used >= 10000) {
			t.Fatal("wrong capacity")
		}
		_, ready := h.queryCollection()
		if ready != (used < 10000) || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
			t.Fatal("capacity became authorization", used)
		}
		target := publicationTarget()
		target.CounterSessionID, target.TradeDate = h.CounterSessionID, h.TradeDate
		scope := CommandScope{Route: responsibilityRoute(), NativeBrokerID: "4900", UserID: "300052141", InvestorID: "300052141"}
		_, err = testQueryHeartbeatProof(StreamInput{"1-0", raw}, scope, target, h.ProducedAt)
		if (err == nil) != (used < 10000) {
			t.Fatal("query gate", used, err)
		}
		if _, err := testQueryHeartbeatProof(StreamInput{"1-0", raw}, scope, target, h.ProducedAt.Add(5*time.Second)); err == nil {
			t.Fatal("resident removed freshness")
		}
	}
	old, err := DecodeHeartbeat(executionAuthorityHeartbeats(t)[0], responsibilityRoute())
	if err != nil || old.Assessment.ResourceUsage != nil {
		t.Fatal("missing resource report fabricated", err)
	}
}

func TestResidentProgressBlockedHeartbeatIsNotRecovery(t *testing.T) {
	raw := syntheticResidentHeartbeat(t, 0, true)
	h, err := DecodeHeartbeat(raw, responsibilityRoute())
	if err != nil || h.Assessment.RuntimeBlockedReason != "synthetic_storage_failure" {
		t.Fatal(err)
	}
	if _, ready := h.queryCollection(); ready || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
		t.Fatal("liveness became ready")
	}
	for _, key := range []string{"accepting_query_commands", "accepting_trade_commands", "accepting_cancel_commands"} {
		changed := editBody(t, raw, func(m map[string]any) {
			m[key], m["payload"].(map[string]any)[key] = true, true
			a := m["adapter_context"].(map[string]any)
			reason := map[string]string{"accepting_query_commands": "query", "accepting_trade_commands": "submit", "accepting_cancel_commands": "cancel"}[key]
			a["operation_reasons"].(map[string]any)[reason] = "ready"
			if reason == "query" {
				a["admission_evidence"].(map[string]any)["query_execution"] = map[string]any{"ready": true, "reason": "ready"}
			}
		})
		if _, err := DecodeHeartbeat(changed, responsibilityRoute()); err == nil {
			t.Fatal("blocked capability", key)
		}
	}
}

func TestResidentProgressMalformedDiagnostics(t *testing.T) {
	base := syntheticResidentHeartbeat(t, 8000, false)
	for name, mutate := range map[string]func(map[string]any){
		"negative":   func(a map[string]any) { a["resource_usage"].(map[string]any)["query_commands_received"] = -1 },
		"null_count": func(a map[string]any) { a["resource_usage"].(map[string]any)["query_commands_received"] = nil },
		"old_query_quota": func(a map[string]any) {
			u := a["resource_usage"].(map[string]any)
			delete(u, "query_commands_received")
			u["query_commands"] = map[string]any{"used": 0, "limit": 1}
		},
		"null_revision": func(a map[string]any) {
			a["resource_usage"].(map[string]any)["journal"].(map[string]any)["revision"] = nil
		},
		"summary": func(a map[string]any) {
			a["resource_usage"].(map[string]any)["journal"].(map[string]any)["warning"] = false
		},
		"null_lifetime":           func(a map[string]any) { a["process_lifecycle"].(map[string]any)["process_duration_seconds"] = nil },
		"null_reason":             func(a map[string]any) { a["runtime_blocked_reason"] = nil },
		"reason_with_query_ready": func(a map[string]any) { a["runtime_blocked_reason"] = "synthetic_fault" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := editBody(t, base, func(m map[string]any) {
				mutate(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any))
			})
			if _, err := DecodeHeartbeat(bad, responsibilityRoute()); err == nil {
				t.Fatal("invalid diagnostic")
			}
		})
	}
	for _, field := range []string{"used", "limit", "remaining", "warning", "exhausted"} {
		bad := editBody(t, base, func(m map[string]any) {
			u := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["resource_usage"].(map[string]any)
			metric := u["journal"].(map[string]any)["metrics"].(map[string]any)["callbacks"].(map[string]any)
			metric[field] = nil
		})
		if _, err := DecodeHeartbeat(bad, responsibilityRoute()); err == nil {
			t.Fatal("null metric", field)
		}
	}
	for _, key := range []string{"file_bytes", "commands", "messages", "deliveries", "conflicts", "callbacks", "events"} {
		bad := editBody(t, base, func(m map[string]any) {
			u := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["resource_usage"].(map[string]any)
			metric := u["journal"].(map[string]any)["metrics"].(map[string]any)[key].(map[string]any)
			metric["limit"] = 999
		})
		if _, err := DecodeHeartbeat(bad, responsibilityRoute()); err == nil {
			t.Fatal("wrong documented limit", key)
		}
	}
	for field, value := range map[string]any{"used": 7999, "remaining": 2001, "warning": false, "exhausted": true} {
		bad := editBody(t, base, func(m map[string]any) {
			u := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["resource_usage"].(map[string]any)
			u["journal"].(map[string]any)["metrics"].(map[string]any)["callbacks"].(map[string]any)[field] = value
		})
		if _, err := DecodeHeartbeat(bad, responsibilityRoute()); err == nil {
			t.Fatal("inconsistent metric", field)
		}
	}
	// Exhaustion must close capabilities even when the envelope otherwise
	// describes an executable session. Warning alone must not close it.
	for _, used := range []int64{8000, 10000} {
		raw := editBody(t, executionAuthorityHeartbeats(t)[1], func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["resource_usage"] = syntheticResidentUsage(t, used)
		})
		_, err := DecodeHeartbeat(raw, responsibilityRoute())
		if (err == nil) != (used == 8000) {
			t.Fatal("capacity/capability mismatch", used, err)
		}
	}
	bad := editBody(t, syntheticResidentHeartbeat(t, 10000, false), func(m map[string]any) {
		delete(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any), "resource_usage")
	})
	if _, err := DecodeHeartbeat(bad, responsibilityRoute()); err == nil {
		t.Fatal("capacity reason without observation")
	}
}

func TestResidentProgressLocalRedisInspectionAndBlockedQueries(t *testing.T) {
	ctx, s, stream, p, _ := residentFixture(t)
	set := func(id string, blocked bool) {
		raw := editBody(t, syntheticResidentHeartbeat(t, 8000, blocked), func(m map[string]any) {
			m["routing"], m["counter_session_id"], m["produced_at"] = s.scope.Route, p.target.CounterSessionID, assessmentTime(time.Now())
			body := m["payload"].(map[string]any)
			body["counter_session_id"], body["trade_date"] = p.target.CounterSessionID, p.target.TradeDate
		})
		residentAppend(t, ctx, s, p.prefix()+":hb", id, string(raw))
	}
	set("1-0", false)
	store, _ := NewCommandStore(stream.db)
	raw := editBody(t, []byte(intentVectors(t)["account.asset.query"].Raw), func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = s.scope.Route.AccountID })
	r, err := store.Register(ctx, s.scope, "cmd.query", raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishTestQuery(ctx, r.CommandID, r.Original.MessageID, p); err != nil {
		t.Fatal("warning stopped queries", err)
	}
	set("2-0", true)
	if _, err := p.inspect(ctx); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal("blocked query", err)
	}
	if n, err := s.reader.client.XLen(ctx, p.prefix()+":cmd.query").Result(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	ic, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := InspectTestHeartbeats(ic, s.reader, HeartbeatInspectionRequest{Route: s.scope.Route, After: "0-0", Limit: 2})
	if err != nil || len(result.Records) != 2 {
		t.Fatal(result, err)
	}
	for i, row := range result.Records {
		a := row.Advertised
		if a == nil || a.ResidentDurationSeconds == nil || *a.ResidentDurationSeconds != 0 || a.ResourceUsage == nil || a.ResourceUsage.QueryCommandsReceived != 2001 {
			t.Fatal("missing resident diagnostics", a)
		}
		b, _ := json.Marshal(a)
		if !bytes.Contains(b, []byte(`"resident_duration_seconds":0`)) {
			t.Fatal("zero omitted")
		}
		if i == 1 && (a.RuntimeBlockedReason != "synthetic_storage_failure" || a.QueryCollectionReady == nil || *a.QueryCollectionReady) {
			t.Fatal("hidden blocked state", a)
		}
	}
}
