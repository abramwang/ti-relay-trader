package redisstream

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

func TestCheckpointErrorLocatorIndependentOfConsumptionCursor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		metadata  map[string]any
		lastError string
		want      string
	}{
		{"active", map[string]any{"active_error": map[string]any{"stream_id": "123-0"}}, "456-0: error", "123-0"},
		{"persisted", map[string]any{"last_error_stream_id": "123-0"}, "456-0: error", "123-0"},
		{"legacy", nil, "123-0: indeterminate command failure BROKER_NOT_READY", "123-0"},
		{"invalid", nil, "not-an-id: error", ""},
		{"signed", nil, "+123-0: error", ""},
		{"overflow", nil, "999999999999999999999999-0: error", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkpointErrorStreamID(tc.metadata, tc.lastError); got != tc.want {
				t.Fatalf("locator = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckpointRecoveryRequiresAllReadinessEvidence(t *testing.T) {
	yes := true
	now := time.Now()
	ready := GatewayRuntimeStatus{Status: "online", OrderEntryReady: true, CounterSessionID: "session-a", LastHeartbeatAt: &now, RedisReady: &yes, BrokerReady: &yes, OrderSnapshotReady: &yes, AcceptingTradeCommands: &yes}
	if !checkpointGatewayReady(&ready) {
		t.Fatal("complete readiness rejected")
	}
	for _, change := range []func(*GatewayRuntimeStatus){
		func(g *GatewayRuntimeStatus) { g.OrderEntryReady = false },
		func(g *GatewayRuntimeStatus) { g.Status = "stale" },
		func(g *GatewayRuntimeStatus) { g.CounterSessionID = "" },
		func(g *GatewayRuntimeStatus) { g.LastHeartbeatAt = nil },
		func(g *GatewayRuntimeStatus) { g.PendingTrades = 1 },
		func(g *GatewayRuntimeStatus) { g.PendingQueries = 1 },
		func(g *GatewayRuntimeStatus) { g.RedisReady = nil },
		func(g *GatewayRuntimeStatus) { g.BrokerReady = nil },
		func(g *GatewayRuntimeStatus) { g.OrderSnapshotReady = nil },
		func(g *GatewayRuntimeStatus) { g.AcceptingTradeCommands = nil },
	} {
		g := ready
		change(&g)
		if checkpointGatewayReady(&g) {
			t.Fatalf("incomplete readiness accepted: %+v", g)
		}
	}
}

func TestRecoveredCheckpointRetainsHistoricalErrorWithoutDegradingHealth(t *testing.T) {
	ctx := context.Background()
	service := &RuntimeObservability{cfg: config.Config{Operations: config.OperationsConfig{LagWarningEntries: 10, LagCriticalEntries: 100}}}
	info := redis.NewXInfoStreamCmd(ctx, "reply")
	info.SetVal(&redis.XInfoStream{Length: 1, LastGeneratedID: "1000-0"})
	command := streamProbeCommands{role: SuffixReply, info: info, checkpoint: ledger.StreamCheckpoint{LastStreamID: "1000-0", LastError: "historical failure", Metadata: map[string]any{"active_error": nil, "last_recovered_error": map[string]any{"code": "BROKER_NOT_READY"}}}}
	s := service.streamStatus(true, command)
	if s.Status != "ok" || s.LastError != "historical failure" || s.ActiveError != nil || s.LastRecoveredError["code"] != "BROKER_NOT_READY" {
		t.Fatalf("recovered status: %+v", s)
	}
	command.checkpoint.Metadata = map[string]any{"active_error": map[string]any{"code": "COMMAND_OUTCOME_UNKNOWN"}}
	s = service.streamStatus(true, command)
	if s.Status != "error" {
		t.Fatalf("unknown outcome lost blocking state: %+v", s)
	}
}
