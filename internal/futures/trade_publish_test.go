package futures

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"
)

func assessmentTime(t time.Time) string {
	return t.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format(time.RFC3339Nano)
}

func assessmentFixture(t *testing.T) (intentVector, RegistrationEvidence, TestQueryTarget, TestTradeApproval) {
	t.Helper()
	v := intentVectors(t)["submit"]
	target := publicationTarget()
	v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
		p := m["payload"].(map[string]any)
		p["trade_date"], p["symbol"], p["order_qty"], p["limit_price"] = target.TradeDate, "rb2701", 1, "2964"
		p["trade_side"], p["offset_type"], p["hedge_type"] = "B", "O", "speculation"
	}))
	e := registrationEvidence(t, v)
	c, err := DecodeCommand([]byte(v.Raw), v.scope(), v.Stream)
	if err != nil {
		t.Fatal(err)
	}
	b := e.Mappings[c.Orders[0].GatewayOrderID]
	a := TestTradeApproval{AssessmentID: "synthetic-assessment", CommandSHA256: commandDigest([]byte(v.Raw)), ExpiresAt: assessmentTime(time.Now().Add(30 * time.Second)), Market: &TestTradeMarket{SecurityID: b.SecurityID, SourceInstrumentID: b.SourceInstrumentID, ExchangeID: b.ExchangeID, TradeDate: target.TradeDate, ObservedAt: assessmentTime(time.Now()), PublicationSnapshotID: b.PublicationSnapshotID, LogicalTableChecksum: b.LogicalTableChecksum, PriceTick: "1", BestBid: "2974", LowerLimit: "2870", UpperLimit: "3173"}}
	return v, e, target, a
}

func assessmentHeartbeat(t *testing.T, route QueryRoute, target TestQueryTarget, now time.Time) StreamInput {
	t.Helper()
	b, err := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "heartbeat", "message_id": "synthetic-trade-heartbeat", "produced_at": assessmentTime(now), "routing": route, "payload": map[string]any{"product_type": "futures", "schema_version": "ctp.f0.draft.2", "counter_session_id": target.CounterSessionID, "trade_date": target.TradeDate, "redis_ready": true, "broker_ready": true, "settlement_confirmed": true, "order_snapshot_ready": true, "accepting_trade_commands": true, "accepting_cancel_commands": true}, "adapter_context": map[string]any{"connection_generation": target.ConnectionGeneration, "transport_recovery_clear": true}})
	if err != nil {
		t.Fatal(err)
	}
	return StreamInput{"1-0", b}
}

func TestAssessmentStrictPreparation(t *testing.T) {
	v, e, target, a := assessmentFixture(t)
	if _, err := ValidateTestTradePreparation([]byte(v.Raw), v.scope(), target, e, a); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"order_qty", "trade_side", "offset_type", "limit_price", "exchange"} {
		raw := editBody(t, []byte(v.Raw), func(m map[string]any) {
			p := m["payload"].(map[string]any)
			switch key {
			case "order_qty":
				p[key] = 2
			case "trade_side":
				p[key] = "S"
			case "offset_type":
				p[key] = "C"
			case "limit_price":
				p[key] = "2974"
			case "exchange":
				p[key] = "CFFEX"
			}
		})
		changed := a
		changed.CommandSHA256 = commandDigest(raw)
		if _, err := ValidateTestTradePreparation(raw, v.scope(), target, e, changed); err == nil {
			t.Fatal("unsafe assessment accepted", key)
		}
	}
	for _, change := range []func(*TestTradeApproval){
		func(a *TestTradeApproval) { a.Market.Stale = true },
		func(a *TestTradeApproval) { a.Market.PriceTick = "5" },
		func(a *TestTradeApproval) { a.Market.LowerLimit = "3000" },
		func(a *TestTradeApproval) { a.Market.PublicationSnapshotID = "other" },
		func(a *TestTradeApproval) { a.Market.SourceInstrumentID = "RB2701.SHFE" },
		func(a *TestTradeApproval) { a.CommandSHA256 = "bad" },
		func(a *TestTradeApproval) { a.CancelReceiptID = 1 },
	} {
		_, _, _, a := assessmentFixture(t)
		change(&a)
		if _, err := ValidateTestTradePreparation([]byte(v.Raw), v.scope(), target, e, a); err == nil {
			t.Fatal("invalid evidence accepted")
		}
	}
	scope := v.scope()
	scope.Route.Env = "prod"
	if _, err := ValidateTestTradePreparation([]byte(v.Raw), scope, target, e, a); err == nil {
		t.Fatal("production accepted")
	}
	raw, _ := json.Marshal(a)
	if _, err := DecodeTestTradeApproval(editBody(t, raw, func(m map[string]any) { delete(m["market"].(map[string]any), "stale") })); err == nil {
		t.Fatal("missing stale accepted")
	}
}

func TestAssessmentHeartbeatAndExpiryGates(t *testing.T) {
	v, _, target, a := assessmentFixture(t)
	now := time.Now()
	row := assessmentHeartbeat(t, v.scope().Route, target, now)
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"redis_ready", "broker_ready", "settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands"} {
		changed := row
		changed.Raw = editBody(t, row.Raw, func(m map[string]any) { m["payload"].(map[string]any)[key] = false })
		if _, err := testTradeHeartbeatProof(changed, v.scope().Route, target, a, now); err == nil {
			t.Fatal("closed gate", key)
		}
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = 2 },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["transport_recovery_clear"] = false },
		func(m map[string]any) {
			m["adapter_context"].(map[string]any)["runtime_mode"] = "test_broker_read_only"
		},
		func(m map[string]any) { m["payload"].(map[string]any)["counter_session_id"] = "other" },
		func(m map[string]any) { m["produced_at"] = assessmentTime(now.Add(-6 * time.Second)) },
		func(m map[string]any) { m["produced_at"] = assessmentTime(now.Add(3 * time.Second)) },
	} {
		changed := row
		changed.Raw = editBody(t, row.Raw, change)
		if _, err := testTradeHeartbeatProof(changed, v.scope().Route, target, a, now); err == nil {
			t.Fatal("invalid heartbeat accepted")
		}
	}
	for _, delta := range []time.Duration{-time.Second, 2 * time.Minute} {
		changed := a
		changed.ExpiresAt = assessmentTime(now.Add(delta))
		if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, changed, now); err == nil {
			t.Fatal("invalid approval time")
		}
	}
	a.Market.ObservedAt = assessmentTime(now.Add(-6 * time.Second))
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now); err == nil {
		t.Fatal("stale market accepted")
	}
	a.CancelReceiptID, a.Market = 1, nil
	row.Raw = editBody(t, row.Raw, func(m map[string]any) {
		p := m["payload"].(map[string]any)
		p["settlement_confirmed"], p["accepting_trade_commands"] = false, false
	})
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now); err != nil {
		t.Fatal("cancel unnecessarily coupled to submit", err)
	}
	row.Raw = editBody(t, row.Raw, func(m map[string]any) { m["payload"].(map[string]any)["accepting_cancel_commands"] = false })
	if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, a, now); err == nil {
		t.Fatal("closed cancel gate")
	}
}

func TestAssessmentHandoffWireExamples(t *testing.T) {
	raw, err := os.ReadFile("../../docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllSubmatch(raw, -1)
	if len(blocks) != 2 {
		t.Fatal("expected exactly two reviewed wire examples")
	}
	scope := CommandScope{Route: QueryRoute{Env: "test", BrokerID: "ctp_4900", GatewayID: "ctp-dongwu-asset-retest-20261117", AccountID: "300052141"}, NativeBrokerID: "4900", UserID: "300052141", InvestorID: "300052141"}
	for _, block := range blocks {
		if _, err := DecodeCommand(block[1], scope, "cmd.trade"); err != nil {
			t.Fatal(err)
		}
	}
}
