package futures

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func executionTradeFixture(t *testing.T, now time.Time) (intentVector, RegistrationEvidence, TestQueryTarget, TestTradeApproval, StreamInput) {
	t.Helper()
	v, _, target, a := assessmentFixture(t)
	route := responsibilityRoute()
	v.Scope = map[string]string{"env": route.Env, "broker_id": route.BrokerID, "gateway_id": route.GatewayID, "account_id": route.AccountID, "native_broker_id": "4900", "user_id": "300052141", "investor_id": "300052141"}
	v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
		m["sent_at"] = assessmentTime(now)
		m["payload"].(map[string]any)["account_id"] = route.AccountID
	}))
	e := registrationEvidence(t, v)
	a.PermissionProfile = relayTestExecutionProfile
	a.ExecutionPermission = &TestExecutionPermission{Submit: true, Cancel: false}
	a.CommandSHA256, a.ExpiresAt = commandDigest([]byte(v.Raw)), assessmentTime(now.Add(30*time.Second))
	a.Market.ObservedAt = assessmentTime(now)
	raw := editBody(t, executionAuthorityHeartbeats(t)[1], func(m map[string]any) {
		m["produced_at"], m["counter_session_id"] = assessmentTime(now), target.CounterSessionID
		p := m["payload"].(map[string]any)
		p["counter_session_id"], p["trade_date"] = target.CounterSessionID, target.TradeDate
	})
	return v, e, target, a, StreamInput{"1-0", raw}
}

func TestRelayExecutionApprovalAndClock(t *testing.T) {
	for _, clock := range []string{"09:00:00", "10:14:59", "10:30:00", "11:29:59", "13:30:00", "14:59:59", "08:59:59", "10:15:00", "11:30:00", "13:29:59", "15:00:00", "21:00:00"} {
		t.Run(clock, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, "2026-11-17T"+clock+"+08:00")
			v, e, target, a, row := executionTradeFixture(t, now)
			if _, err := ValidateTestTradePreparation([]byte(v.Raw), v.scope(), target, e, a); err != nil {
				t.Fatal(err)
			}
			proof, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now)
			if assessmentDaySegment(now) == 0 {
				if !errors.Is(err, ErrQueryPublishGate) {
					t.Fatal("off-hours capability authorized trading", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if now.Second() == 59 && !proof.ExpiresAt.Equal(now.Add(time.Second)) {
				t.Fatal("publication crossed segment boundary", proof.ExpiresAt)
			}
			if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now.UTC()); err != nil {
				t.Fatal("business clock depends on host timezone", err)
			}
		})
	}
}

func TestRelayExecutionPermissionsAndFreshness(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, _, target, a, row := executionTradeFixture(t, now)
	for name, change := range map[string]func(*TestTradeApproval){
		"missing_permission": func(a *TestTradeApproval) { a.ExecutionPermission = nil },
		"submit_disabled":    func(a *TestTradeApproval) { a.ExecutionPermission = &TestExecutionPermission{Cancel: true} },
		"legacy_grant":       func(a *TestTradeApproval) { a.PermissionProfile = "test_day_relay.v1" },
		"expired":            func(a *TestTradeApproval) { a.ExpiresAt = assessmentTime(now) },
		"long_lived":         func(a *TestTradeApproval) { a.ExpiresAt = assessmentTime(now.Add(61 * time.Second)) },
		"stale_quote": func(a *TestTradeApproval) {
			m := *a.Market
			m.ObservedAt = assessmentTime(now.Add(-5 * time.Second))
			a.Market = &m
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := a
			change(&changed)
			if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, changed, now); err == nil {
				t.Fatal("unsafe grant")
			}
		})
	}
	for _, mutate := range []func(*TestQueryTarget){
		func(t *TestQueryTarget) { t.CounterSessionID = "old-session" },
		func(t *TestQueryTarget) { t.ConnectionGeneration++ },
		func(t *TestQueryTarget) { t.TradeDate = "20261118" },
	} {
		changed := target
		mutate(&changed)
		if _, err := testTradeHeartbeatProof(row, v.scope().Route, changed, a, now); err == nil {
			t.Fatal("wrong target accepted")
		}
	}
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now.Add(5*time.Second)); err == nil {
		t.Fatal("stale heartbeat")
	}
	old := assessmentHeartbeat(t, v.scope().Route, target, now)
	if _, err := testTradeHeartbeatProof(old, v.scope().Route, target, a, now); err == nil {
		t.Fatal("new grant accepted old capability")
	}
	// Independently authorize cancel, even with both Relay and OC submit closed.
	a.CancelReceiptID, a.Market = 1, nil
	a.ExecutionPermission = &TestExecutionPermission{Cancel: true}
	row.Raw = editBody(t, row.Raw, func(m map[string]any) {
		m["accepting_trade_commands"] = false
		m["payload"].(map[string]any)["accepting_trade_commands"] = false
		m["adapter_context"].(map[string]any)["operation_reasons"].(map[string]any)["submit"] = "execution_busy"
	})
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now); err != nil {
		t.Fatal("cancel tied to submit", err)
	}
	a.ExecutionPermission.Cancel = false
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now); err == nil {
		t.Fatal("cancel without permission")
	}
}

func TestRelayExecutionStrictGrantAndCommandDeadline(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, _, _, a, _ := executionTradeFixture(t, now)
	raw, _ := json.Marshal(a)
	if _, err := DecodeTestTradeApproval(raw); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { delete(m, "execution_permission") },
		func(m map[string]any) { m["execution_permission"] = nil },
		func(m map[string]any) { delete(m["execution_permission"].(map[string]any), "cancel") },
		func(m map[string]any) { m["execution_permission"].(map[string]any)["submit"] = nil },
		func(m map[string]any) { m["execution_permission"].(map[string]any)["cancel"] = "false" },
		func(m map[string]any) { m["execution_permission"].(map[string]any)["batch"] = true },
		func(m map[string]any) { m["permission_profile"] = "test_day_relay.v1" },
	} {
		if _, err := DecodeTestTradeApproval(editBody(t, raw, mutate)); err == nil {
			t.Fatal("ambiguous grant accepted")
		}
	}
	for _, delta := range []time.Duration{-time.Nanosecond, 0, 4 * time.Second, 5 * time.Second} {
		deadline, err := relayExecutionCommandDeadline([]byte(v.Raw), a, now.Add(delta))
		valid := delta >= 0 && delta < 5*time.Second
		if valid != (err == nil) || valid && !deadline.Equal(now.Add(5*time.Second)) {
			t.Fatal(delta, deadline, err)
		}
	}
	if _, err := relayExecutionCommandDeadline(append([]byte(v.Raw), ' '), a, now); err == nil {
		t.Fatal("body changed")
	}
}

func TestRelayExecutionDurablePermissions(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "unknown"}[unknown], func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			v, e, target, a, _ := executionTradeFixture(t, time.Now())
			r := registerVector(t, store, ctx, v, e, "registered")
			transport := &fakeQueryPublisher{appendError: unknown}
			a.ExecutionPermission.Submit = false
			if _, err := store.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, transport); !errors.Is(err, ErrQueryPublishGate) || transport.appends.Load() != 0 || storedCount(t, db, "futures_test_trade_publications") != 0 {
				t.Fatal("closed permission published", err)
			}
			a.ExecutionPermission.Submit = true
			result, err := store.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, transport)
			if unknown {
				if !errors.Is(err, ErrQueryPublishUnknown) || result.Published != nil {
					t.Fatal(result, err)
				}
			} else if err != nil || result.Published == nil || !*result.Published {
				t.Fatal(result, err)
			}
			fresh, _ := NewCommandStore(db)
			again, err := fresh.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, transport)
			if !again.Replayed || transport.appends.Load() != 1 || unknown && !errors.Is(err, ErrQueryPublishUnknown) {
				t.Fatal("resent", again, err)
			}
			if unknown {
				return
			}
			other := v
			other.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
				m["message_id"], m["idempotency_key"] = "execution-second", "execution-second"
				p := m["payload"].(map[string]any)
				p["gateway_order_id"], p["client_order_id"] = "execution-second-order", "execution-second-client"
			}))
			second := registerVector(t, store, ctx, other, registrationEvidence(t, other), "registered")
			secondApproval := a
			secondApproval.CommandSHA256 = commandDigest([]byte(other.Raw))
			if _, err := store.publishTestTrade(ctx, other.scope(), second.CommandID, second.Original.MessageID, target, secondApproval, transport); err != nil || transport.appends.Load() != 2 {
				t.Fatal("inherited one-order assessment quota", err)
			}
			a.CancelReceiptID = assessmentOrderReceipt(t, db, ctx, r, target, nil)
			o := r.Original.Orders[0]
			cancelV := v
			cancelV.Raw = string(editBody(t, []byte(intentVectors(t)["cancel_target_day"].Raw), func(m map[string]any) {
				m["message_id"], m["idempotency_key"] = "relay-execution-cancel", "relay-execution-cancel"
				p := m["payload"].(map[string]any)
				p["account_id"], p["gateway_order_id"], p["trade_date"], p["cancel_id"] = o.AccountID, o.GatewayOrderID, o.TradeDate, "cancel-one"
			}))
			cancelR := registerVector(t, store, ctx, cancelV, registrationEvidence(t, cancelV), "registered")
			a.Market, a.CommandSHA256 = nil, commandDigest([]byte(cancelV.Raw))
			a.ExecutionPermission = &TestExecutionPermission{Cancel: true}
			result, err = store.publishTestTrade(ctx, v.scope(), cancelR.CommandID, cancelR.Original.MessageID, target, a, transport)
			if err != nil || transport.appends.Load() != 3 || result.BrokerAccepted {
				t.Fatal("independent cancel", result, err)
			}
		})
	}
}
