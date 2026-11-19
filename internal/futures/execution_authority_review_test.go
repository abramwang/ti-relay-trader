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

const executionAuthorityFixtureDir = "../../testdata/oc_execution_authority_review_20261118"

func executionAuthorityHeartbeats(t *testing.T) []json.RawMessage {
	t.Helper()
	var doc struct {
		Synthetic  bool              `json:"synthetic_only"`
		RealSDK    bool              `json:"real_sdk_calls"`
		Deployed   bool              `json:"deployed"`
		Heartbeats []json.RawMessage `json:"heartbeats"`
	}
	raw, err := os.ReadFile(filepath.Join(executionAuthorityFixtureDir, "execution_authority_examples.json"))
	if err != nil || json.Unmarshal(raw, &doc) != nil || !doc.Synthetic || doc.RealSDK || doc.Deployed || len(doc.Heartbeats) != 2 {
		t.Fatal("invalid execution fixture", err)
	}
	return doc.Heartbeats
}

func TestExecutionAuthorityOriginalDigests(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(executionAuthorityFixtureDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 9 {
		t.Fatal("unexpected inventory", len(lines))
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 2 || filepath.Base(f[1]) != f[1] {
			t.Fatal("invalid manifest")
		}
		body, err := os.ReadFile(filepath.Join(executionAuthorityFixtureDir, f[1]))
		if err != nil || commandDigest(body) != f[0] {
			t.Fatal("evidence changed", f[1], err)
		}
	}
}

func TestExecutionAuthorityOriginalHeartbeats(t *testing.T) {
	for i, raw := range executionAuthorityHeartbeats(t) {
		h, err := DecodeHeartbeat(raw, responsibilityRoute())
		if err != nil {
			t.Fatal(i, err)
		}
		a := h.Assessment
		if a == nil || !a.relayExecution() || a.ResidentDurationSeconds <= 0 || a.AssessmentID != "" || a.PermissionProfile != "" || !a.ExpiresAt.IsZero() || a.Lifecycle != nil || !bytes.Equal(h.Raw, raw) {
			t.Fatal("fabricated authorization or changed raw", a)
		}
		_, ready := h.queryCollection()
		if ready != (i == 0) || h.AcceptingTradeCommands != (i == 1) || h.AcceptingCancelCommands != (i == 1) || a.PositionSnapshotReady || a.Static != nil || a.Limits != nil {
			t.Fatal("incorrect capabilities", i)
		}
		scope := CommandScope{Route: responsibilityRoute(), NativeBrokerID: "4900", UserID: "300052141", InvestorID: "300052141"}
		target := publicationTarget()
		target.CounterSessionID, target.TradeDate = h.CounterSessionID, h.TradeDate
		row := StreamInput{"1-0", raw}
		proof, err := testQueryHeartbeatProof(row, scope, target, h.ProducedAt)
		if i == 0 {
			if err != nil || !proof.ExpiresAt.Equal(h.ProducedAt.Add(5*time.Second)) {
				t.Fatal("unconfirmed settlement blocked query", proof, err)
			}
			if _, err := testQueryHeartbeatProof(row, scope, target, h.ProducedAt.Add(5*time.Second)); err == nil {
				t.Fatal("stale query accepted")
			}
		} else if err == nil {
			t.Fatal("explicit false query inferred true")
		}
		// The new contract is not silently promoted into the old publisher.
		for _, cancelID := range []int64{0, 1} {
			if _, err := testTradeHeartbeatProof(row, scope.Route, target, TestTradeApproval{CancelReceiptID: cancelID}, h.ProducedAt); err == nil {
				t.Fatal("capability became authorization")
			}
		}
	}
}

func TestExecutionAuthorityStrictContract(t *testing.T) {
	base := executionAuthorityHeartbeats(t)[1]
	for name, change := range map[string]func(map[string]any){
		"owner_missing":     func(a map[string]any) { delete(a, "authorization_owner") },
		"owner_unknown":     func(a map[string]any) { a["authorization_owner"] = "oc" },
		"old_profile":       func(a map[string]any) { a["permission_profile"] = "test_day_relay.v1" },
		"old_expiry_null":   func(a map[string]any) { a["expires_at"] = nil },
		"old_id":            func(a map[string]any) { a["assessment_id"] = "legacy" },
		"old_symbol":        func(a map[string]any) { a["symbol"] = "rb2701" },
		"old_exchange":      func(a map[string]any) { a["exchange"] = "SHFE" },
		"old_phase":         func(a map[string]any) { a["process_lifecycle"].(map[string]any)["phase"] = "active" },
		"bad_lifetime":      func(a map[string]any) { a["process_lifecycle"].(map[string]any)["process_duration_seconds"] = 86401 },
		"negative_lifetime": func(a map[string]any) { a["process_lifecycle"].(map[string]any)["process_duration_seconds"] = -1 },
		"old_lifecycle":     func(a map[string]any) { a["process_lifecycle"].(map[string]any)["mode"] = "scheduled_assessment.v1" },
		"missing_scope":     func(a map[string]any) { delete(a, "order_snapshot_scope") },
		"bad_native_check":  func(a map[string]any) { a["per_command_native_validation"] = "false" },
		"null_symbol":       func(a map[string]any) { a["constraint_symbol"] = nil },
		"busy":              func(a map[string]any) { a["trade_preparation_pending"] = true },
		"missing_ref":       func(a map[string]any) { a["order_ref_ready"] = false },
		"conflict": func(a map[string]any) {
			a["pending_callback_count"] = 1
			a["blocking_callback_count"] = 1
			a["callback_reconciliation_complete"] = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := editBody(t, base, func(m map[string]any) {
				change(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any))
			})
			if _, err := DecodeHeartbeat(raw, responsibilityRoute()); err == nil {
				t.Fatal("unsafe execution evidence accepted")
			}
		})
	}
	for _, key := range []string{"submit_attempts", "cancel_attempts"} {
		raw := editBody(t, base, func(m map[string]any) {
			m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)[key] = 200
		})
		if _, err := DecodeHeartbeat(raw, responsibilityRoute()); err != nil {
			t.Fatal("audit count became authorization budget", key, err)
		}
	}
	// Reject an owner marker grafted onto the old profile, not a fallback.
	raw := editBody(t, queryRuntimeHeartbeats(t)[1], func(m map[string]any) {
		m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["authorization_owner"] = "relay"
	})
	if _, err := DecodeHeartbeat(raw, responsibilityRoute()); err == nil {
		t.Fatal("mixed old and new contract")
	}
}

func TestExecutionQueryReadinessIndependentOfTradeClock(t *testing.T) {
	for _, clock := range []string{"08:55:00", "10:20:00", "12:00:00", "15:01:00"} {
		raw := editBody(t, executionAuthorityHeartbeats(t)[0], func(m map[string]any) { m["produced_at"] = "2026-11-17T" + clock + "+08:00" })
		h, err := DecodeHeartbeat(raw, responsibilityRoute())
		if err != nil {
			t.Fatal(clock, err)
		}
		_, ready := h.queryCollection()
		if !ready || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
			t.Fatal("query widened trading", clock)
		}
	}
}

func TestExecutionAuthorityLocalRedisQueryAndTradeFence(t *testing.T) {
	ctx, s, stream, p, _ := residentFixture(t)
	setHeartbeat := func(index int, id string) {
		raw := editBody(t, executionAuthorityHeartbeats(t)[index], func(m map[string]any) {
			m["routing"], m["counter_session_id"], m["produced_at"] = s.scope.Route, p.target.CounterSessionID, assessmentTime(time.Now())
			payload := m["payload"].(map[string]any)
			payload["counter_session_id"], payload["trade_date"] = p.target.CounterSessionID, p.target.TradeDate
		})
		residentAppend(t, ctx, s, p.prefix()+":hb", id, string(raw))
	}
	setHeartbeat(0, "1-0")
	store, _ := NewCommandStore(stream.db)
	raw := editBody(t, []byte(intentVectors(t)["account.asset.query"].Raw), func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = s.scope.Route.AccountID })
	registration, err := store.Register(ctx, s.scope, "cmd.query", raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.PublishTestQuery(ctx, registration.CommandID, registration.Original.MessageID, p)
	if err != nil || result.Published == nil || !*result.Published || result.BrokerAccepted {
		t.Fatal(result, err)
	}
	if _, err := store.PublishTestQuery(ctx, registration.CommandID, registration.Original.MessageID, p); err != nil {
		t.Fatal(err)
	}
	if n, err := s.reader.client.XLen(ctx, p.prefix()+":cmd.query").Result(); err != nil || n != 1 {
		t.Fatal("query replay", n, err)
	}
	setHeartbeat(1, "2-0")
	if _, err := p.inspect(ctx); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal("query false ignored", err)
	}
	// Even the old valid explicit approval cannot implicitly opt into the new
	// execution contract. No trading command or attempted publication is made.
	migrateFuturesTest(t, stream.db, ctx, "000046_futures_test_trade_publication", "up")
	v, e, _, approval := assessmentFixture(t)
	tradeRaw := editBody(t, []byte(v.Raw), func(m map[string]any) {
		m["message_id"], m["idempotency_key"] = "execution-trade", "execution-trade"
		m["payload"].(map[string]any)["account_id"] = s.scope.Route.AccountID
	})
	approval.CommandSHA256 = commandDigest(tradeRaw)
	registered, err := store.Register(ctx, s.scope, "cmd.trade", tradeRaw, e)
	if err != nil || registered.Outcome != "registered" {
		t.Fatal(registered.Outcome, registered.Reason, err)
	}
	if _, err := ValidateTestTradePreparation(tradeRaw, s.scope, p.target, e, approval); err != nil {
		t.Fatalf("invalid synthetic trade preparation: %v", err)
	}
	publisher := &TestTradePublisher{base: p, approval: approval}
	if _, err := store.PublishTestTrade(ctx, registered.CommandID, registered.Original.MessageID, publisher); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal("legacy approval accepted execution contract", err)
	}
	if n, err := s.reader.client.XLen(ctx, p.prefix()+":cmd.trade").Result(); err != nil || n != 0 {
		t.Fatal("unauthorized publication", n, err)
	}
	if n := storedCount(t, stream.db, "futures_test_trade_publications"); n != 0 {
		t.Fatal("trade budget consumed", n)
	}
	inspectionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inspection, err := InspectTestHeartbeats(inspectionCtx, s.reader, HeartbeatInspectionRequest{Route: s.scope.Route, After: "0-0", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range inspection.Records {
		if r.Advertised == nil || r.Advertised.AuthorizationOwner != "relay" || r.Advertised.ResidentDurationSeconds == nil || *r.Advertised.ResidentDurationSeconds <= 0 || r.Advertised.ProcessLifecycle != nil {
			t.Fatal("inspection invented legacy permission", r)
		}
	}
}
