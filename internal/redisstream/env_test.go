package redisstream

import (
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
)

func TestApplyProbeEnvFromHXVariables(t *testing.T) {
	t.Setenv("HX_REDIS_HOST", "127.0.0.1")
	t.Setenv("HX_REDIS_PORT", "6379")
	t.Setenv("HX_REDIS_PASSWORD", "secret")
	t.Setenv("HX_REDIS_DB", "2")
	t.Setenv("HX_RELAY_ENV", "prod")
	t.Setenv("HX_RELAY_BROKER_ID", "huaxin")
	t.Setenv("HX_RELAY_GATEWAY_ID", "00030484")
	t.Setenv("HX_ACCOUNT_ID", "00030484")

	cfg := ApplyProbeEnv(config.Default())

	if !strings.HasPrefix(cfg.Redis.URL, "redis://") {
		t.Fatalf("redis url = %q", cfg.Redis.URL)
	}
	if cfg.Redis.Env != "prod" || cfg.Redis.BrokerID != "huaxin" || cfg.Redis.GatewayID != "00030484" {
		t.Fatalf("redis routing = %#v", cfg.Redis)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("account count = %d", len(cfg.Accounts))
	}
	if cfg.Accounts[0].StreamPrefix != "relay:prod:v1:huaxin:00030484" {
		t.Fatalf("stream prefix = %q", cfg.Accounts[0].StreamPrefix)
	}
}

func TestApplyProbeEnvPrefersRedisURL(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("HX_REDIS_HOST", "10.0.0.1")

	cfg := ApplyProbeEnv(config.Default())
	if cfg.Redis.URL != "redis://localhost:6379/0" {
		t.Fatalf("redis url = %q", cfg.Redis.URL)
	}
}
