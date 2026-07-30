package redisstream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/timeutil"
)

func TestMonitoringWindowUsesTradingCalendarAndSession(t *testing.T) {
	location := timeutil.Location()
	service := &RuntimeObservability{
		calendar: fakeTradingCalendar{status: market.TradingDayStatus{
			IsTradingDay:      true,
			IsTradingDayKnown: true,
		}},
	}

	active, reason, tradingDay := service.monitoringWindow(
		context.Background(),
		time.Date(2026, 7, 30, 10, 0, 0, 0, location),
	)
	if !active || reason != "trading_session" || !tradingDay.IsTradingDayKnown || !tradingDay.IsTradingDay {
		t.Fatalf("monitoring window = %v %q %+v", active, reason, tradingDay)
	}

	active, reason, _ = service.monitoringWindow(
		context.Background(),
		time.Date(2026, 7, 30, 16, 0, 0, 0, location),
	)
	if active || reason != "off_hours" {
		t.Fatalf("off-hours monitoring window = %v %q", active, reason)
	}
}

func TestStreamStatusUsesRealLagThresholdsAndSuppressesOffHours(t *testing.T) {
	ctx := context.Background()
	service := &RuntimeObservability{cfg: config.Config{
		Operations: config.OperationsConfig{
			LagWarningEntries:  10,
			LagCriticalEntries: 100,
		},
	}}
	info := redis.NewXInfoStreamCmd(ctx, "relay:prod:v1:huaxin:a1:event")
	info.SetVal(&redis.XInfoStream{Length: 200, LastGeneratedID: "1785308335135-0"})
	lag := redis.NewCmd(ctx)
	lag.SetVal(int64(25))
	command := streamProbeCommands{
		account: config.AccountRouteConfig{
			AccountID: "a1",
			BrokerID:  "huaxin",
			GatewayID: "a1",
		},
		role:      SuffixEvent,
		streamKey: "relay:prod:v1:huaxin:a1:event",
		checkpoint: ledger.StreamCheckpoint{
			LastStreamID: "1785308000000-0",
		},
		info: info,
		lag:  lag,
	}

	status := service.streamStatus(true, command)
	if status.Status != "warning" || status.Lag != 25 {
		t.Fatalf("stream status = %+v", status)
	}
	status = service.streamStatus(false, command)
	if status.Status != "off_hours" || status.Lag != 25 {
		t.Fatalf("off-hours stream status = %+v", status)
	}
}

func TestGatewayStatusClassifiesHeartbeatAndBrokerNotReady(t *testing.T) {
	location := timeutil.Location()
	now := time.Date(2026, 7, 30, 10, 0, 20, 0, location)
	payload, err := json.Marshal(map[string]any{
		"component_id":              "oc.huaxin.a1",
		"component_role":            "broker_trader_gateway",
		"state":                     "UP",
		"state_text":                "running",
		"redis_ready":               true,
		"broker_ready":              true,
		"order_snapshot_ready":      true,
		"accepting_trade_commands":  true,
		"accepting_cancel_commands": true,
		"pending_trade_count":       1,
		"pending_query_count":       0,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"protocol":     Protocol,
		"message_type": "heartbeat",
		"message_id":   "hb-1",
		"produced_at":  now.Add(-5 * time.Second).Format(time.RFC3339Nano),
		"payload":      json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	latest := redis.NewXMessageSliceCmd(context.Background())
	latest.SetVal([]redis.XMessage{{
		ID:     "1785308335135-0",
		Values: map[string]any{"body": string(body)},
	}})
	service := &RuntimeObservability{cfg: config.Config{
		Operations: config.OperationsConfig{HeartbeatStaleSeconds: 30},
	}}
	command := heartbeatProbeCommand{
		account: config.AccountRouteConfig{
			AccountID: "a1",
			Alias:     "测试账户",
			BrokerID:  "huaxin",
			GatewayID: "a1",
		},
		stream: "relay:prod:v1:huaxin:a1:hb",
		latest: latest,
	}

	status := service.gatewayStatus(now, true, command, ledger.GatewayIssue{})
	if status.Status != "online" || status.PendingTrades != 1 || status.HeartbeatAgeSecs != 5 ||
		status.BrokerReady == nil || !*status.BrokerReady ||
		status.AcceptingCancelCommands == nil || !*status.AcceptingCancelCommands {
		t.Fatalf("gateway status = %+v", status)
	}

	status = service.gatewayStatus(now, true, command, ledger.GatewayIssue{
		AccountID:  "a1",
		Code:       "BROKER_NOT_READY",
		Message:    "login pending",
		ReceivedAt: now.Add(-time.Minute),
	})
	if status.Status != "broker_not_ready" || !status.BrokerNotReady {
		t.Fatalf("broker not ready status = %+v", status)
	}
}

func TestGatewayStatusUsesOCV12ReadinessFlags(t *testing.T) {
	location := timeutil.Location()
	now := time.Date(2026, 7, 30, 9, 1, 10, 0, location)
	payload, err := json.Marshal(map[string]any{
		"component_id":              "oc.huaxin.a1",
		"component_role":            "broker_trader_gateway",
		"state":                     "DEGRADED",
		"state_text":                "initial_order_sync_pending",
		"redis_ready":               true,
		"broker_ready":              true,
		"order_snapshot_ready":      false,
		"accepting_trade_commands":  true,
		"accepting_cancel_commands": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"protocol":     Protocol,
		"message_type": "heartbeat",
		"message_id":   "hb-v12",
		"produced_at":  now.Add(-time.Second).Format(time.RFC3339Nano),
		"payload":      json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	latest := redis.NewXMessageSliceCmd(context.Background())
	latest.SetVal([]redis.XMessage{{
		ID:     "1785308335135-0",
		Values: map[string]any{"body": string(body)},
	}})
	service := &RuntimeObservability{cfg: config.Config{
		Operations: config.OperationsConfig{HeartbeatStaleSeconds: 30},
	}}
	status := service.gatewayStatus(now, true, heartbeatProbeCommand{
		account: config.AccountRouteConfig{AccountID: "a1", BrokerID: "huaxin", GatewayID: "a1"},
		stream:  "relay:prod:v1:huaxin:a1:hb",
		latest:  latest,
	}, ledger.GatewayIssue{})

	if status.Status != "degraded" || status.OrderSnapshotReady == nil || *status.OrderSnapshotReady ||
		status.AcceptingCancelCommands == nil || *status.AcceptingCancelCommands {
		t.Fatalf("gateway v1.2 readiness status = %+v", status)
	}
}

func TestStreamLagLuaAgainstConfiguredRedis(t *testing.T) {
	configPath := os.Getenv(config.EnvPath)
	if configPath == "" {
		t.Skip("RELAY_CONFIG_PATH is not set")
	}
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join("..", "..", configPath)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load integration config: %v", err)
	}
	options, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := redis.NewClient(options)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, account := range cfg.Accounts {
		streamKey := NewStreams(account.StreamPrefix).Event
		info, infoErr := client.XInfoStream(ctx, streamKey).Result()
		if infoErr != nil || info.Length < 2 {
			continue
		}
		const limit = int64(10)
		got, evalErr := client.Eval(ctx, streamLagLua, []string{streamKey}, info.FirstEntry.ID, limit).Int64()
		if evalErr != nil {
			t.Fatalf("evaluate lag script for %s: %v", streamKey, evalErr)
		}
		want := info.Length - 1
		if want > limit {
			want = limit
		}
		if got != want {
			t.Fatalf("lag from first entry for %s = %d, want %d", streamKey, got, want)
		}
		return
	}
	t.Skip("no configured event stream with at least two entries")
}

type fakeTradingCalendar struct {
	status market.TradingDayStatus
	err    error
}

func (calendar fakeTradingCalendar) TradingDayStatus(context.Context, string) (market.TradingDayStatus, error) {
	return calendar.status, calendar.err
}
