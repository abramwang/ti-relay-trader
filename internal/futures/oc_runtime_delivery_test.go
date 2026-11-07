package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type ocRuntimeOutput struct {
	Body, Stream string
	StreamID     string `json:"stream_id"`
}

func runtimeHeartbeats(t *testing.T) []ocRuntimeOutput {
	t.Helper()
	var out []ocRuntimeOutput
	for _, name := range []string{"heartbeat_examples.v1.json", "main_cli_20261107.json", "shared_redis_20261107.json", "sanitizer_private_20261107.json"} {
		raw, err := os.ReadFile("../../testdata/runtime_review/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Examples []ocRuntimeOutput
			Runs     []struct{ Heartbeats []ocRuntimeOutput }
			Cases    []struct{ Heartbeat ocRuntimeOutput } `json:"runtime_cases"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		out = append(out, doc.Examples...)
		for _, run := range doc.Runs {
			out = append(out, run.Heartbeats...)
		}
		for _, c := range doc.Cases {
			out = append(out, c.Heartbeat)
		}
	}
	if len(out) != 34 {
		t.Fatal("missing heartbeat evidence", len(out))
	}
	return out
}

func runtimeRoute(t *testing.T, output ocRuntimeOutput) QueryRoute {
	t.Helper()
	var envelope struct{ Routing QueryRoute }
	if err := json.Unmarshal([]byte(output.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	// Body-derived routing is only for this received-file test, never runtime trust.
	route := envelope.Routing
	if route.AccountID == route.GatewayID || output.Stream != strings.TrimSuffix(queryReplyStream(route), "reply")+"hb" {
		t.Fatal("OC physical gateway route mismatch", output.Stream)
	}
	return route
}

func TestOCRuntimeHeartbeatSamplesStayNotReady(t *testing.T) {
	for _, output := range runtimeHeartbeats(t) {
		route := runtimeRoute(t, output)
		h, err := DecodeHeartbeat([]byte(output.Body), route)
		if err != nil || h.Recovery == nil || h.TradeDate != "" || h.State != "DEGRADED" || h.BrokerReady || h.SettlementConfirmed || h.OrderSnapshotReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
			t.Fatal("stage1 decode or readiness mismatch", err)
		}
		if !bytes.Equal(h.Raw, []byte(output.Body)) {
			t.Fatal("source audit rewritten")
		}
	}
}

func TestOCRuntimeHeartbeatCannotActivateSession(t *testing.T) {
	output := runtimeHeartbeats(t)[0]
	c := sourceFixture()
	c.Scope.Route = runtimeRoute(t, output)
	s, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := DecodeHeartbeat([]byte(output.Body), c.Scope.Route)
	if err != nil || !h.Recovery.TransportRecoveryClear {
		t.Fatal(err)
	}
	scope := tradeScope()
	scope.Route, scope.TradeDate = c.Scope.Route, "20261107"
	g, err := NewTestSessionGuard(s, DeliveryQualityContext{scope, h.CounterSessionID, 1}, DefaultTestHeartbeatPolicy())
	if err != nil {
		t.Fatal(err)
	}
	err = g.observeAt(guardSource(g, output.StreamID), []byte(output.Body), h.ProducedAt)
	if !errors.Is(err, ErrTestSessionStopped) || !strings.Contains(err.Error(), "transport_recovery_only") {
		t.Fatal("transport clear treated as a live broker", err)
	}
	if err := g.observeAt(guardSource(g, "9999999999999-0"), guardBody(t, g, "later-ready", h.ProducedAt.Add(time.Second)), h.ProducedAt.Add(time.Second)); !errors.Is(err, ErrTestSessionStopped) {
		t.Fatal("stopped guard resurrected", err)
	}
}

func TestOCRuntimeHeartbeatRejectsConflicts(t *testing.T) {
	output := runtimeHeartbeats(t)[0]
	route := runtimeRoute(t, output)
	mutations := []func(map[string]any){
		func(m map[string]any) { delete(m, "broker_ready") },
		func(m map[string]any) { m["broker_ready"] = true },
		func(m map[string]any) { m["counter_session_id"] = "different" },
		func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = "20261107" },
		func(m map[string]any) { delete(m["payload"].(map[string]any), "trade_date") },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = 0 },
		func(m map[string]any) { delete(m["adapter_context"].(map[string]any), "connection_generation") },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["runtime_mode"] = "unknown" },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["transport_recovery_clear"] = "true" },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["recovery_issue_count"] = -1 },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["recovery_issue_count"] = 1 },
		func(m map[string]any) { m["adapter_context"].(map[string]any)["process_id"] = nil },
		func(m map[string]any) { m["state"] = "UP"; m["payload"].(map[string]any)["state"] = "UP" },
		func(m map[string]any) {
			m["state_text"] = "unknown"
			m["payload"].(map[string]any)["state_text"] = "unknown"
		},
	}
	for _, flag := range []string{"broker_ready", "settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"} {
		mutations = append(mutations, func(m map[string]any) { m[flag] = true; m["payload"].(map[string]any)[flag] = true })
	}
	for i, mutate := range mutations {
		if _, err := DecodeHeartbeat(editBody(t, []byte(output.Body), mutate), route); err == nil {
			t.Fatal("unsafe recovery heartbeat accepted", i)
		}
	}
	prod := route
	prod.Env = "prod"
	raw := editBody(t, []byte(output.Body), func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" })
	if _, err := DecodeHeartbeat(raw, prod); err == nil {
		t.Fatal("TEST profile accepted in production")
	}
}
