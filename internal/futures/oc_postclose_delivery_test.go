package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Private recorded callbacks stay outside Git. This test reads the delivered
// encoder output only; it does not publish fixtures or validate a live session.
func TestOCPostcloseDeliveredHeartbeats(t *testing.T) {
	dir := os.Getenv("RELAY_OC_CTP_POSTCLOSE_REVIEW_DIR")
	if dir == "" {
		t.Skip("set RELAY_OC_CTP_POSTCLOSE_REVIEW_DIR for private OC evidence")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "recorded-flow-20261117-v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	if commandDigest(raw) != "26a57a0b701798a6ba3503b5b472888815aa4993939a017d219cbcb5932be665" {
		t.Fatal("original OC evidence changed")
	}
	var doc struct {
		RealBroker bool               `json:"real_broker"`
		RealRedis  bool               `json:"real_redis"`
		Simulated  bool               `json:"new_commands_sdk_and_query_completion_are_simulated"`
		Cases      []ocAssessmentCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.RealBroker || doc.RealRedis || !doc.Simulated || len(doc.Cases) != 2 {
		t.Fatal("incorrect evidence boundary", err)
	}
	route := ocAssessmentRoute()
	route.GatewayID = "ctp-dongwu-assessment-pm-20261117"
	count, events, queries := 0, 0, 0
	for _, c := range doc.Cases {
		for i, raw := range c.Heartbeats {
			h, err := DecodeHeartbeat(raw, route)
			if err != nil {
				t.Fatalf("%s heartbeat %d: %v", c.Scenario, i, err)
			}
			if !bytes.Equal(raw, h.Raw) || h.Assessment == nil || h.Assessment.AssetScopeVerified {
				t.Fatal("audit/profile changed")
			}
			_, queryReady := h.queryCollection()
			if h.StateText == "order_snapshot_not_ready" && !queryReady {
				t.Fatal("deferred callbacks deadlock initialization")
			}
			if c.Scenario == "deferred_replay_ack_uncertain" && (queryReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands || h.Assessment.TransportRecoveryClear) {
				t.Fatal("unknown ACK opened admission")
			}
			count++
		}
		for _, raw := range c.Outputs {
			var obj rawObject
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			if oneOf(textField(obj, "action"), "order.list.query", "account.positions.query") {
				// Fixed offline scenario expectations, not request evidence learned
				// from a live reply. This does not register or publish a command.
				ctx := QueryContext{Route: route, Action: "order.list.query", OriginMessageID: "offline-cmd.query-104", Ticket: "1", CounterSessionID: "ctpproc-9f0eb2ced2c57026ed45de0c0e826f35", TradeDate: "20261117", NativeBrokerID: "4900", NativeInvestorID: "300052141", NativeUserID: "300052141"}
				generation, sdk := int64(1), int64(5)
				if textField(obj, "action") == "account.positions.query" {
					ctx.Action, ctx.OriginMessageID, ctx.Ticket, sdk = "account.positions.query", "offline-cmd.query-108", "2", 6
				}
				ctx.ConnectionGeneration, ctx.SDKRequestID = &generation, &sdk
				batch, err := NewQueryBatch(ctx, 0, time.Second, DefaultBatchLimits())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := batch.Accept(raw, time.Millisecond); err != nil {
					t.Fatal(err)
				}
				candidate, err := batch.Candidate()
				if err != nil {
					t.Fatal(err)
				}
				if ctx.Action == "order.list.query" && len(candidate.Orders) != 12 {
					t.Fatal("order coverage changed")
				}
				queries++
			}
			if textField(obj, "message_type") != "event" {
				continue
			}
			origin := "offline-cmd.trade-113"
			e, err := DecodeTradeEvent(raw, EventContext{Scope: TradeScope{Route: route, NativeBrokerID: "4900", InvestorID: "300052141", TradeDate: "20261117"}, ExpectedOrigin: &origin})
			if err != nil || e.Order == nil || e.Fill != nil || e.Order.OrderQty != 1 || e.Order.Symbol != "rb2701" || !bytes.Equal(raw, e.Raw) {
				t.Fatal("unexpected local event", err)
			}
			events++
		}
	}
	if count != 17 || events != 2 || queries != 2 {
		t.Fatal("coverage changed", count, events, queries)
	}
}
