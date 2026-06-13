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
	if cfg.Service.PublicURL == "" {
		t.Fatal("public URL default was not applied")
	}
	if cfg.Database.Driver != "postgres" {
		t.Fatalf("database driver = %q, want postgres", cfg.Database.Driver)
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
