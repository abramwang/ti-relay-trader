package redisstream

import (
	"testing"

	"ti-relay-trader/internal/config"
)

func TestNewStreams(t *testing.T) {
	streams := NewStreams("relay:prod:v1:huaxin:00030484:")
	if streams.Prefix != "relay:prod:v1:huaxin:00030484" {
		t.Fatalf("prefix = %q", streams.Prefix)
	}
	if streams.Event != "relay:prod:v1:huaxin:00030484:event" {
		t.Fatalf("event stream = %q", streams.Event)
	}
	if len(streams.All()) != 6 {
		t.Fatalf("stream count = %d", len(streams.All()))
	}
}

func TestPrefixesFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.Env = "prod"
	cfg.Redis.BrokerID = "huaxin"
	cfg.Redis.GatewayID = "00030484"
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID:    "00030484",
			BrokerID:     "huaxin",
			GatewayID:    "00030484",
			StreamPrefix: "relay:prod:v1:huaxin:00030484",
		},
	}

	prefixes := PrefixesFromConfig(cfg)
	if len(prefixes) != 1 {
		t.Fatalf("prefix count = %d", len(prefixes))
	}
	if prefixes[0] != "relay:prod:v1:huaxin:00030484" {
		t.Fatalf("prefix = %q", prefixes[0])
	}
}
