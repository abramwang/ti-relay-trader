package futures

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type readOnlyScenario struct {
	Scenario      string            `json:"scenario"`
	Heartbeats    []json.RawMessage `json:"heartbeats"`
	Outputs       []ocRuntimeOutput `json:"outputs"`
	ExitCode      int               `json:"exit_code"`
	Pending       int               `json:"pending"`
	SDKQueryCalls int               `json:"sdk_query_calls"`
}

func readOnlyScenarios(t *testing.T) []readOnlyScenario {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/readonly_review/readonly_runtime.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SyntheticOnly  bool               `json:"synthetic_only"`
		RealSDKCalls   bool               `json:"real_sdk_calls"`
		RedisConnected bool               `json:"redis_connected"`
		Scenarios      []readOnlyScenario `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.SyntheticOnly || doc.RealSDKCalls || doc.RedisConnected || len(doc.Scenarios) != 8 {
		t.Fatal("offline evidence boundary")
	}
	return doc.Scenarios
}

func readOnlyRoute() QueryRoute {
	return QueryRoute{"test", "ctp_4900", "offline-readonly", "990000000017"}
}

func TestOCReadOnlyArtifactDigests(t *testing.T) {
	manifest, err := os.ReadFile("../../testdata/readonly_review/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatal("invalid manifest")
		}
		raw, err := os.ReadFile("../../testdata/readonly_review/" + fields[1])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != fields[0] {
			t.Fatal("digest mismatch", fields[1])
		}
	}
}

func TestOCReadOnlyHeartbeatSamples(t *testing.T) {
	count := 0
	for _, scenario := range readOnlyScenarios(t) {
		for _, raw := range scenario.Heartbeats {
			h, err := DecodeHeartbeat(raw, readOnlyRoute())
			if err != nil {
				t.Fatal(scenario.Scenario, err)
			}
			if h.ReadOnly == nil || h.Recovery != nil || h.SettlementConfirmed || h.OrderSnapshotReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands || !bytes.Equal(h.Raw, raw) {
				t.Fatal("read-only boundary", scenario.Scenario)
			}
			count++
		}
	}
	if count != 25 {
		t.Fatal("missing heartbeats", count)
	}
}

func TestOCReadOnlyRejectsUnsafeClaims(t *testing.T) {
	raw := readOnlyScenarios(t)[0].Heartbeats[1]
	changes := []func(map[string]any){
		func(m map[string]any) { delete(m, "broker_ready") },
		func(m map[string]any) { m["broker_ready"] = false },
		func(m map[string]any) { m["routing"].(map[string]any)["account_id"] = "other" },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["runtime_mode"] = "unknown" },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = nil },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = 0 },
		func(m map[string]any) { delete(m["adapter_context"].(map[string]any), "connection_generation") },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["query_collection_ready"] = "true" },
		func(m map[string]any) { delete(m["adapter_context"].(map[string]any), "query_collection_ready") },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["transport_recovery_clear"] = false },
		func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = nil },
		func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = 20261117 },
	}
	for _, key := range []string{"settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"} {
		changes = append(changes, func(m map[string]any) { m[key] = true; m["payload"].(map[string]any)[key] = true })
	}
	for i, change := range changes {
		if _, err := DecodeHeartbeat(editBody(t, raw, change), readOnlyRoute()); err == nil {
			t.Fatal("unsafe heartbeat accepted", i)
		}
	}
	route := readOnlyRoute()
	route.Env = "prod"
	prod := editBody(t, raw, func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" })
	if _, err := DecodeHeartbeat(prod, route); err == nil {
		t.Fatal("TEST mode in prod")
	}
}

func readOnlyGuard(t *testing.T) (*TestSessionGuard, []byte, time.Time) {
	t.Helper()
	raw := readOnlyScenarios(t)[0].Heartbeats[1]
	h, err := DecodeHeartbeat(raw, readOnlyRoute())
	if err != nil {
		t.Fatal(err)
	}
	c := sourceFixture()
	c.Scope.Route = readOnlyRoute()
	s, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	scope := tradeScope()
	scope.Route, scope.TradeDate = c.Scope.Route, h.TradeDate
	g, err := NewTestSessionGuard(s, DeliveryQualityContext{scope, h.CounterSessionID, *h.ReadOnly.ConnectionGeneration}, DefaultTestHeartbeatPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return g, raw, h.ProducedAt
}

func TestOCReadOnlyCollectionGuard(t *testing.T) {
	for _, reason := range []string{"budget", "generation", "stop", "expiry", "legacy_mode"} {
		t.Run(reason, func(t *testing.T) {
			g, raw, at := readOnlyGuard(t)
			if err := g.observeAt(guardSource(g, "1-0"), raw, at); err != nil {
				t.Fatal(err)
			}
			next := editBody(t, raw, func(m map[string]any) {
				m["message_id"], m["produced_at"] = "next-heartbeat", at.Add(time.Second).Format(time.RFC3339Nano)
				a := m["adapter_context"].(map[string]any)
				p := m["payload"].(map[string]any)
				switch reason {
				case "budget":
					a["query_collection_ready"] = false
				case "generation":
					a["connection_generation"] = 2
				case "stop":
					a["query_collection_ready"] = false
					m["state"], p["state"] = "DEGRADED", "DEGRADED"
					m["state_text"], p["state_text"] = "stopping", "stopping"
				case "legacy_mode":
					delete(m, "adapter_context")
					for _, key := range []string{"counter_session_id", "state", "state_text", "redis_ready", "broker_ready", "settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"} {
						delete(m, key)
					}
					delete(p, "state")
					delete(p, "state_text")
				}
			})
			now := at.Add(time.Second)
			if reason == "expiry" {
				now = at.Add(time.Minute)
			}
			if err := g.observeAt(guardSource(g, "2-0"), next, now); !errors.Is(err, ErrTestSessionStopped) {
				t.Fatal("guard remained active", err)
			}
			if err := g.observeAt(guardSource(g, "3-0"), raw, now); !errors.Is(err, ErrTestSessionStopped) {
				t.Fatal("stopped guard revived")
			}
		})
	}
}

func TestOCReadOnlyInspectionIsNotAuthorization(t *testing.T) {
	for _, raw := range readOnlyScenarios(t)[0].Heartbeats {
		reader, req := inspectFixture(t, readOnlyRoute(), raw)
		out, err := InspectTestHeartbeats(inspectContext(t), reader, req)
		if err != nil || out.BusinessAuthorized || out.Records[0].Classification != "test_broker_read_only" {
			t.Fatal("inspection profile", err)
		}
		a := out.Records[0].Advertised
		if a.QueryCollectionReady == nil || a.RuntimeMode != "test_broker_read_only" {
			t.Fatal("inspection facts missing")
		}
	}
}

func TestOCReadOnlyQueryOutputs(t *testing.T) {
	for _, scenario := range readOnlyScenarios(t) {
		for _, output := range scenario.Outputs {
			var m struct {
				Action  string `json:"action"`
				Origin  string `json:"origin_message_id"`
				Status  string `json:"status"`
				Payload struct {
					TradeDate string `json:"trade_date"`
				} `json:"payload"`
				Adapter struct {
					Evidence struct {
						Ticket     string `json:"ticket"`
						Session    string `json:"counter_session_id"`
						Generation *int64 `json:"connection_generation"`
						Request    *int64 `json:"sdk_request_id"`
					} `json:"query_evidence"`
				} `json:"adapter_context"`
			}
			if err := json.Unmarshal([]byte(output.Body), &m); err != nil {
				t.Fatal(err)
			}
			if output.Stream != queryReplyStream(readOnlyRoute()) {
				t.Fatal("wrong output stream")
			}
			// Expected identity is derived only for offline sample replay, never live registration.
			c := QueryContext{Route: readOnlyRoute(), Action: m.Action, OriginMessageID: m.Origin, TradeDate: m.Payload.TradeDate, Ticket: m.Adapter.Evidence.Ticket, CounterSessionID: m.Adapter.Evidence.Session, ConnectionGeneration: m.Adapter.Evidence.Generation, SDKRequestID: m.Adapter.Evidence.Request, NativeBrokerID: "4900", NativeInvestorID: "990000000017", NativeUserID: "990000000017"}
			b, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
			if err != nil {
				t.Fatal(scenario.Scenario, err)
			}
			if _, err := b.Accept([]byte(output.Body), 0); err != nil {
				t.Fatal(scenario.Scenario, m.Action, err)
			}
			want := BatchReady
			if m.Status == "failed" {
				want = BatchFailed
			}
			assertState(t, b, want)
		}
	}
}
