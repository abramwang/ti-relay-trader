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
		time.Date(2026, 7, 30, 15, 29, 0, 0, location),
	)
	if !active || reason != "trading_session" {
		t.Fatalf("post-close monitoring window = %v %q", active, reason)
	}

	active, reason, _ = service.monitoringWindow(
		context.Background(),
		time.Date(2026, 7, 30, 15, 30, 0, 0, location),
	)
	if active || reason != "off_hours" {
		t.Fatalf("off-hours monitoring window = %v %q", active, reason)
	}
}

func TestMonitoringWindowUsesConfiguredTestCounterSchedule(t *testing.T) {
	location := timeutil.Location()
	schedule := &config.OrderEntryScheduleConfig{
		CounterMode: "huaxin_7x24_test",
		Timezone:    "Asia/Shanghai",
		Windows: []config.OrderEntryWindowConfig{
			{Start: "13:15", End: "13:25"},
			{Start: "13:30", End: "15:30"},
		},
	}
	service := &RuntimeObservability{
		cfg: config.Config{
			Service: config.ServiceConfig{Environment: config.EnvironmentTest},
			Accounts: []config.AccountRouteConfig{{
				AccountID: "a1", Enabled: true, OrderEntrySchedule: schedule,
			}},
		},
		calendar: fakeTradingCalendar{status: market.TradingDayStatus{
			IsTradingDay:      false,
			IsTradingDayKnown: true,
		}},
	}

	active, reason, _ := service.monitoringWindow(
		context.Background(),
		time.Date(2026, 9, 20, 13, 14, 44, 0, location),
	)
	if active || reason != "test_counter_off_hours" {
		t.Fatalf("test counter before window = %v %q", active, reason)
	}
	active, reason, _ = service.monitoringWindow(
		context.Background(),
		time.Date(2026, 9, 20, 13, 15, 0, 0, location),
	)
	if !active || reason != "test_counter_session" {
		t.Fatalf("test counter window = %v %q", active, reason)
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
		"counter_session_id":        "session-20260730-a",
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
			AccountID:      "a1",
			Alias:          "生产账户",
			BrokerID:       "huaxin",
			GatewayID:      "a1",
			Enabled:        true,
			TradingEnabled: true,
		},
		stream: "relay:prod:v1:huaxin:a1:hb",
		latest: latest,
	}

	status := service.gatewayStatus(now, true, command, ledger.GatewayIssue{})
	if status.Status != "online" || status.PendingTrades != 1 || status.HeartbeatAgeSecs != 5 ||
		status.BrokerReady == nil || !*status.BrokerReady ||
		status.AcceptingCancelCommands == nil || !*status.AcceptingCancelCommands ||
		!status.OrderEntryReady || status.BrokerSessionState != "ready" ||
		status.CounterSessionID != "session-20260730-a" || status.CounterMode != "" {
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

func TestGatewayStatusUsesOCLatchedOrderEntryState(t *testing.T) {
	location := timeutil.Location()
	now := time.Date(2026, 9, 17, 15, 42, 0, 0, location)
	payload, err := json.Marshal(map[string]any{
		"component_id":              "oc.huaxin.a1",
		"component_role":            "broker_trader_gateway",
		"counter_session_id":        "hxproc-current",
		"state":                     "DEGRADED",
		"state_text":                "counter_order_entry_not_ready",
		"redis_ready":               true,
		"broker_ready":              true,
		"order_snapshot_ready":      true,
		"accepting_trade_commands":  false,
		"accepting_cancel_commands": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"protocol":     Protocol,
		"message_type": "heartbeat",
		"message_id":   "hb-test-counter-state-reject",
		"produced_at":  now.Add(-5 * time.Second).Format(time.RFC3339Nano),
		"payload":      json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	latest := redis.NewXMessageSliceCmd(context.Background())
	latest.SetVal([]redis.XMessage{{
		ID:     "1789630915000-0",
		Values: map[string]any{"body": string(body)},
	}})
	schedule := &config.OrderEntryScheduleConfig{
		CounterMode: "huaxin_7x24_test",
		Timezone:    "Asia/Shanghai",
		Windows: []config.OrderEntryWindowConfig{
			{Start: "15:40", End: "17:40"},
		},
	}
	service := &RuntimeObservability{cfg: config.Config{
		Service:    config.ServiceConfig{Environment: config.EnvironmentTest},
		Operations: config.OperationsConfig{HeartbeatStaleSeconds: 600},
	}}
	command := heartbeatProbeCommand{
		account: config.AccountRouteConfig{
			AccountID: "a1", BrokerID: "huaxin", GatewayID: "a1", Enabled: true, TradingEnabled: true,
			OrderEntrySchedule: schedule,
		},
		stream: "relay:prod:v1:huaxin:a1:hb",
		latest: latest,
	}
	status := service.gatewayStatus(now, true, command, ledger.GatewayIssue{})
	if status.OrderEntryReady || status.Status != "degraded" ||
		status.BrokerSessionState != "blocked" ||
		status.OrderEntryBlockReason != "OC_TRADE_COMMANDS_PAUSED" ||
		status.OrderEntrySource != "oc_heartbeat+configured_test_schedule" {
		t.Fatalf("OC latched order-entry status = %+v", status)
	}
}

func TestGatewayStatusBlocksCredentialFailuresAndIdentityMismatch(t *testing.T) {
	location := timeutil.Location()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, location)
	buildStatus := func(t *testing.T, payload map[string]any) GatewayRuntimeStatus {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"protocol": Protocol, "message_type": "heartbeat", "message_id": "hb-credential",
			"produced_at": now.Add(-time.Second).Format(time.RFC3339Nano), "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		latest := redis.NewXMessageSliceCmd(context.Background())
		latest.SetVal([]redis.XMessage{{ID: "1789700000000-0", Values: map[string]any{"body": string(body)}}})
		service := &RuntimeObservability{cfg: config.Config{Operations: config.OperationsConfig{HeartbeatStaleSeconds: 30}}}
		return service.gatewayStatus(now, true, heartbeatProbeCommand{
			account: config.AccountRouteConfig{AccountID: "501000114077", BrokerID: "huaxin", GatewayID: "501000114077", TradingEnabled: true},
			stream:  "relay:test:v1:huaxin:501000114077:hb", latest: latest,
		}, ledger.GatewayIssue{})
	}

	base := map[string]any{
		"state": "DEGRADED", "state_text": "credential_not_ready", "redis_ready": true,
		"broker_ready": false, "order_snapshot_ready": false, "accepting_trade_commands": false,
		"credential_status": "decrypt_failed", "credential_version": 2,
		"credential_key_id": "hx-test-202609", "credential_source": "relay_redis_encrypted",
		"managed_account_id": "501000114077",
	}
	status := buildStatus(t, base)
	if status.Status != "credential_not_ready" || status.OrderEntryReady ||
		status.OrderEntryBlockReason != "OC_CREDENTIAL_NOT_READY" || status.CredentialVersion != 2 {
		t.Fatalf("credential failure status = %+v", status)
	}

	base["state"] = "UP"
	base["state_text"] = "running"
	base["broker_ready"] = true
	base["order_snapshot_ready"] = true
	base["accepting_trade_commands"] = true
	base["accepting_cancel_commands"] = true
	base["credential_status"] = "loaded"
	base["managed_account_id"] = "307000051387"
	status = buildStatus(t, base)
	if status.Status != "credential_identity_mismatch" || status.OrderEntryReady || status.OrderEntryBlockReason != "OC_CREDENTIAL_IDENTITY_MISMATCH" {
		t.Fatalf("credential identity mismatch status = %+v", status)
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

func TestGatewayStatusCombinesHeartbeatWithTestCounterSchedule(t *testing.T) {
	location := timeutil.Location()
	now := time.Date(2026, 9, 15, 13, 14, 44, 0, location)
	payload, err := json.Marshal(map[string]any{
		"component_id":              "oc.huaxin.a1",
		"component_role":            "broker_trader_gateway",
		"counter_session_id":        "session-20260915-a",
		"state":                     "UP",
		"state_text":                "running",
		"redis_ready":               true,
		"broker_ready":              true,
		"order_snapshot_ready":      true,
		"accepting_trade_commands":  true,
		"accepting_cancel_commands": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"protocol":     Protocol,
		"message_type": "heartbeat",
		"message_id":   "hb-test-counter",
		"produced_at":  now.Add(-time.Second).Format(time.RFC3339Nano),
		"payload":      json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	latest := redis.NewXMessageSliceCmd(context.Background())
	latest.SetVal([]redis.XMessage{{
		ID:     "1789449283000-0",
		Values: map[string]any{"body": string(body)},
	}})
	schedule := &config.OrderEntryScheduleConfig{
		CounterMode: "huaxin_7x24_test",
		Timezone:    "Asia/Shanghai",
		Windows: []config.OrderEntryWindowConfig{
			{Start: "13:15", End: "13:25"},
			{Start: "13:30", End: "15:30"},
		},
	}
	service := &RuntimeObservability{cfg: config.Config{
		Operations: config.OperationsConfig{HeartbeatStaleSeconds: 30},
	}}
	command := heartbeatProbeCommand{
		account: config.AccountRouteConfig{
			AccountID:          "a1",
			BrokerID:           "huaxin",
			GatewayID:          "a1",
			Enabled:            true,
			TradingEnabled:     true,
			OrderEntrySchedule: schedule,
		},
		stream: "relay:prod:v1:huaxin:a1:hb",
		latest: latest,
	}

	status := service.gatewayStatus(now, false, command, ledger.GatewayIssue{})
	if status.OrderEntryReady || status.BrokerSessionState != "blocked" ||
		status.OrderEntryBlockReason != "OUTSIDE_TEST_COUNTER_WINDOW" ||
		status.OrderEntryNextChangeAt == nil ||
		status.OrderEntryNextChangeAt.Format(time.RFC3339) != "2026-09-15T13:15:00+08:00" {
		t.Fatalf("before test window status = %+v", status)
	}

	now = time.Date(2026, 9, 15, 13, 15, 0, 0, location)
	status = service.gatewayStatus(now, true, command, ledger.GatewayIssue{})
	if !status.OrderEntryReady || status.BrokerSessionState != "ready" ||
		status.OrderEntryBlockReason != "" || status.CounterMode != "huaxin_7x24_test" ||
		status.CounterSessionID != "session-20260915-a" {
		t.Fatalf("inside test window status = %+v", status)
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
