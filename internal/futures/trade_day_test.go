package futures

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDayNativeSnapshotAndTradeProof(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	row, scope, target, a := freshOCTradeHeartbeat(t, now, false)
	row.Raw = editBody(t, row.Raw, func(m map[string]any) {
		v := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
		v["permission_profile"] = "test_day_relay.v1"
		v["per_command_native_validation"] = true
		v["constraint_symbol"] = "rb2701"
		v["trade_preparation_pending"], v["trade_response_pending"] = false, false
		v["order_snapshot_scope"] = "full_native_current_session"
		v["native_order_snapshot_ready"] = true
		v["order_projection_ready"] = false
		v["local_orders_only"] = true
		v["native_limits_fresh"] = false
		v["native_depth_request_id"] = nil
		delete(v, "native_limits")
		v["expires_at"] = "2026-11-17T15:00:00+08:00"
		v["process_lifecycle"] = map[string]any{"mode": "scheduled_assessment.v1", "starts_at": "2026-11-17T13:30:00+08:00", "expires_at": v["expires_at"], "phase": "active", "process_duration_seconds": 5400}
	})
	a.PermissionProfile = "test_day_relay.v1"
	h, err := DecodeHeartbeat(row.Raw, scope.Route)
	if err != nil || h.Assessment.OrderProjectionReady || !h.Assessment.NativeOrderSnapshotReady {
		t.Fatal(h, err)
	}
	if _, err := testTradeHeartbeatProof(row, scope.Route, target, a, now); err != nil {
		t.Fatal(err)
	}
	legacy := a
	legacy.PermissionProfile = ""
	if _, err := testTradeHeartbeatProof(row, scope.Route, target, legacy, now); err == nil {
		t.Fatal("implicit day authorization")
	}
	for _, key := range []string{"native_order_snapshot_ready", "order_projection_ready", "local_orders_only", "order_snapshot_scope"} {
		changed := row
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) {
			delete(m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any), key)
		})
		if _, err := DecodeHeartbeat(changed.Raw, scope.Route); err == nil {
			t.Fatal("partial snapshot scope", key)
		}
	}
	changed := row
	changed.Raw = editBody(t, row.Raw, func(m map[string]any) {
		m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)["native_order_snapshot_ready"] = false
	})
	if _, err := DecodeHeartbeat(changed.Raw, scope.Route); err == nil {
		t.Fatal("contradictory native snapshot")
	}
}

func TestDayMultipleOrdersAndOwnCancelPostgres(t *testing.T) {
	db, s, ctx := assessmentDB(t)
	v, _, target, a := assessmentFixture(t)
	v.Scope = map[string]string{"env": "test", "broker_id": "ctp_4900", "gateway_id": "day-test", "account_id": "300052141", "native_broker_id": "4900", "investor_id": "300052141", "user_id": "300052141"}
	a.PermissionProfile = "test_day_relay.v1"
	f := &fakeQueryPublisher{}
	var first CommandRegistration
	var firstApproval TestTradeApproval
	for _, id := range []string{"first", "second"} {
		next := v
		next.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
			m["message_id"], m["idempotency_key"] = "msg-"+id, "idem-"+id
			p := m["payload"].(map[string]any)
			p["account_id"] = "300052141"
			p["gateway_order_id"], p["client_order_id"] = "order-"+id, "client-"+id
		}))
		e := registrationEvidence(t, next)
		r := registerVector(t, s, ctx, next, e, "registered")
		c := r.Original.Orders[0]
		b := e.Mappings[c.GatewayOrderID]
		a.Market.SecurityID = b.SecurityID
		a.Market.SourceInstrumentID = b.SourceInstrumentID
		a.Market.ExchangeID = b.ExchangeID
		a.Market.PublicationSnapshotID = b.PublicationSnapshotID
		a.Market.LogicalTableChecksum = b.LogicalTableChecksum
		a.CommandSHA256 = commandDigest([]byte(next.Raw))
		if _, err := s.publishTestTrade(ctx, next.scope(), r.CommandID, r.Original.MessageID, target, a, f); err != nil {
			t.Fatal(id, err)
		}
		if _, err := s.publishTestTrade(ctx, next.scope(), r.CommandID, r.Original.MessageID, target, a, f); err != nil {
			t.Fatal("replay", err)
		}
		if id == "first" {
			first = r
			firstApproval = a
		}
	}
	if f.appends.Load() != 2 {
		t.Fatal("duplicate publication")
	}
	a = firstApproval
	a.CancelReceiptID = assessmentOrderReceipt(t, db, ctx, first, target, nil)
	a.Market = nil
	cancelV := v
	cancelV.Raw = string(editBody(t, []byte(intentVectors(t)["cancel_target_day"].Raw), func(m map[string]any) {
		m["message_id"], m["idempotency_key"] = "cancel-first", "idem-cancel-first"
		p := m["payload"].(map[string]any)
		p["account_id"], p["gateway_order_id"], p["trade_date"], p["cancel_id"] = "300052141", first.Original.Orders[0].GatewayOrderID, target.TradeDate, "cancel-first"
	}))
	cr := registerVector(t, s, ctx, cancelV, registrationEvidence(t, cancelV), "registered")
	a.CommandSHA256 = commandDigest([]byte(cancelV.Raw))
	if _, err := s.publishTestTrade(ctx, v.scope(), cr.CommandID, cr.Original.MessageID, target, a, f); err != nil {
		t.Fatal("specific parent cancel", err)
	}
	if _, err := s.publishTestTrade(ctx, v.scope(), cr.CommandID, cr.Original.MessageID, target, a, f); err != nil {
		t.Fatal(err)
	}
	if f.appends.Load() != 3 {
		t.Fatal("cancel replayed")
	}
	legacy := firstApproval
	legacy.PermissionProfile = ""
	raw, _ := json.Marshal(legacy)
	if _, err := DecodeTestTradeApproval(raw); err != nil {
		t.Fatal(err)
	}
	// Existing commands cannot change their publication profile after restart.
	fresh, _ := NewCommandStore(db)
	if _, err := fresh.publishTestTrade(ctx, v.scope(), first.CommandID, first.Original.MessageID, target, legacy, f); err == nil {
		t.Fatal("retargeted day command")
	}
}
