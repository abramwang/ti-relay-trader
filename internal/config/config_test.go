package config

import (
	"strings"
	"testing"
)

func TestDecodeAppliesDefaults(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`service: {}`))
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if cfg.Service.Mode != ModeDocs {
		t.Fatalf("mode = %q, want %q", cfg.Service.Mode, ModeDocs)
	}
	if cfg.Service.Environment != EnvironmentTest {
		t.Fatalf("environment = %q, want %q", cfg.Service.Environment, EnvironmentTest)
	}
	if cfg.Service.PublicURL == "" {
		t.Fatal("public URL default was not applied")
	}
	if cfg.Database.Driver != "postgres" {
		t.Fatalf("database driver = %q, want postgres", cfg.Database.Driver)
	}
	if cfg.Service.LogLevel != "info" {
		t.Fatalf("log level = %q, want info", cfg.Service.LogLevel)
	}
	if cfg.Service.LogFormat != "json" {
		t.Fatalf("log format = %q, want json", cfg.Service.LogFormat)
	}
	if cfg.Service.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q, want Asia/Shanghai", cfg.Service.Timezone)
	}
	if cfg.Market.BaseURL != "http://meridian-data.quantstage.com" {
		t.Fatalf("market base url = %q", cfg.Market.BaseURL)
	}
	if cfg.Market.TimeoutSeconds != 15 {
		t.Fatalf("market timeout = %d, want 15", cfg.Market.TimeoutSeconds)
	}
	if cfg.Market.SnapshotMarketLevel != "level1" || cfg.Market.SnapshotDataScope != "realtime" {
		t.Fatalf("market snapshot defaults = %q/%q", cfg.Market.SnapshotMarketLevel, cfg.Market.SnapshotDataScope)
	}
	if !cfg.AutoRefreshEnabled() {
		t.Fatal("auto refresh should be enabled by default")
	}
	if cfg.AutoRefresh.DebounceSeconds != 2 || cfg.AutoRefresh.CooldownSeconds != 20 || cfg.AutoRefresh.TimeoutSeconds != 10 {
		t.Fatalf("auto refresh defaults = %+v", cfg.AutoRefresh)
	}
}

func TestDecodeRejectsInvalidMode(t *testing.T) {
	_, err := Decode(strings.NewReader(`service: {mode: bad}`))
	if err == nil {
		t.Fatal("expected invalid mode error")
	}
	if !strings.Contains(err.Error(), "invalid service.mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsInvalidEnvironment(t *testing.T) {
	_, err := Decode(strings.NewReader(`service: {environment: staging}`))
	if err == nil {
		t.Fatal("expected invalid environment error")
	}
	if !strings.Contains(err.Error(), "invalid service.environment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsDuplicateAccounts(t *testing.T) {
	_, err := Decode(strings.NewReader(`
accounts:
  - account_id: "acct-1"
    broker_id: "huaxin"
    gateway_id: "gw-1"
    stream_prefix: "relay:prod:v1:huaxin:gw-1"
  - account_id: "acct-1"
    broker_id: "huaxin"
    gateway_id: "gw-2"
    stream_prefix: "relay:prod:v1:huaxin:gw-2"
`))
	if err == nil {
		t.Fatal("expected duplicate account error")
	}
	if !strings.Contains(err.Error(), "duplicate account route") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsStreamPrefixMismatch(t *testing.T) {
	_, err := Decode(strings.NewReader(`
redis:
  env: "prod"
accounts:
  - account_id: "acct-1"
    broker_id: "huaxin"
    gateway_id: "gw-1"
    stream_prefix: "relay:test:v1:huaxin:gw-1"
    enabled: true
    trading_enabled: true
`))
	if err == nil {
		t.Fatal("expected stream prefix mismatch")
	}
	if !strings.Contains(err.Error(), "does not match expected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsProductionTradingForSimulatedAccount(t *testing.T) {
	_, err := Decode(strings.NewReader(`
service:
  environment: "production"
redis:
  env: "prod"
accounts:
  - account_id: "acct-1"
    broker_id: "huaxin"
    gateway_id: "gw-1"
    stream_prefix: "relay:prod:v1:huaxin:gw-1"
    enabled: true
    trading_enabled: true
    simulated: true
`))
	if err == nil {
		t.Fatal("expected production simulated trading error")
	}
	if !strings.Contains(err.Error(), "simulated must be false") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsTradingWhenAccountDisabled(t *testing.T) {
	_, err := Decode(strings.NewReader(`
accounts:
  - account_id: "acct-1"
    broker_id: "huaxin"
    gateway_id: "gw-1"
    stream_prefix: "relay:prod:v1:huaxin:gw-1"
    enabled: false
    trading_enabled: true
`))
	if err == nil {
		t.Fatal("expected disabled trading account error")
	}
	if !strings.Contains(err.Error(), "trading_enabled requires enabled=true") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsInvalidLogFormat(t *testing.T) {
	_, err := Decode(strings.NewReader(`service: {log_format: xml}`))
	if err == nil {
		t.Fatal("expected invalid log format error")
	}
	if !strings.Contains(err.Error(), "invalid service.log_format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeRejectsInvalidTimezone(t *testing.T) {
	_, err := Decode(strings.NewReader(`service: {timezone: Mars/Relay}`))
	if err == nil {
		t.Fatal("expected invalid timezone error")
	}
	if !strings.Contains(err.Error(), "invalid service.timezone") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAccountRoute(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
accounts:
  - account_id: "acct-1"
    broker_id: "huaxin"
    gateway_id: "gw-1"
    stream_prefix: "relay:prod:v1:huaxin:gw-1"
`))
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	route, ok := cfg.AccountRoute("acct-1")
	if !ok {
		t.Fatal("expected route for acct-1")
	}
	if route.StreamPrefix != "relay:prod:v1:huaxin:gw-1" {
		t.Fatalf("stream prefix = %q", route.StreamPrefix)
	}
}

func TestDecodeCanDisableAutoRefresh(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
service: {}
auto_refresh:
  enabled: false
  debounce_seconds: 1
  cooldown_seconds: 30
  timeout_seconds: 5
`))
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if cfg.AutoRefreshEnabled() {
		t.Fatal("auto refresh should be disabled")
	}
	if cfg.AutoRefresh.CooldownSeconds != 30 {
		t.Fatalf("cooldown = %d", cfg.AutoRefresh.CooldownSeconds)
	}
}
