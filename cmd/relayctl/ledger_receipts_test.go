package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

func handoffCLIConfig() (config.Config, []ledger.LedgerHandoffInput) {
	embedded := false
	c := config.Config{
		Service:  config.ServiceConfig{Environment: config.EnvironmentTest, Mode: config.ModeWorker},
		Database: config.DatabaseConfig{DSN: "postgres://localhost:1/handoff_test", ExpectedName: "handoff_test"},
		Redis:    config.RedisConfig{URL: "redis://localhost:1/0", Env: "test", BrokerID: "huaxin", GatewayID: "fixture"},
		Worker:   config.WorkerConfig{EmbeddedLedgerSync: &embedded},
		Accounts: []config.AccountRouteConfig{{AccountID: "fixture", BrokerID: "huaxin", GatewayID: "fixture", StreamPrefix: "relay:test:v1:huaxin:fixture", Enabled: true}},
	}
	var inputs []ledger.LedgerHandoffInput
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		inputs = append(inputs, ledger.LedgerHandoffInput{Binding: ledger.LedgerStreamBinding{
			Environment: "test", AccountID: "fixture", BrokerID: "huaxin", GatewayID: "fixture", Role: role,
			SourceID: "fixture", ServerRunID: strings.Repeat("a", 40),
		}, Cursor: "0-0"})
	}
	return c, inputs
}

func TestLedgerReceiptHandoffCLIConfig(t *testing.T) {
	c, inputs := handoffCLIConfig()
	if err := validateLedgerHandoffConfig(c, inputs); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*config.Config, *[]ledger.LedgerHandoffInput)
	}{
		{"production", func(c *config.Config, _ *[]ledger.LedgerHandoffInput) {
			c.Service.Environment = config.EnvironmentProduction
		}},
		{"production_database", func(c *config.Config, _ *[]ledger.LedgerHandoffInput) {
			c.Database.ExpectedName = "relay"
			c.Database.DSN = "postgres://localhost:1/relay"
		}},
		{"different_database", func(c *config.Config, _ *[]ledger.LedgerHandoffInput) { c.Database.ExpectedName = "other_test" }},
		{"incomplete_streams", func(_ *config.Config, in *[]ledger.LedgerHandoffInput) { *in = (*in)[:3] }},
		{"embedded", func(c *config.Config, _ *[]ledger.LedgerHandoffInput) { c.Worker.EmbeddedLedgerSync = nil }},
		{"source_mismatch", func(_ *config.Config, in *[]ledger.LedgerHandoffInput) { (*in)[0].Binding.SourceID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, in := handoffCLIConfig()
			tc.change(&c, &in)
			if err := validateLedgerHandoffConfig(c, in); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestLedgerReceiptHandoffCLIFlags(t *testing.T) {
	base := []string{"-config", "/nonexistent/private/config.yaml", "-input", "/nonexistent/private/request.json", "-output", filepath.Join(t.TempDir(), "manifest.json")}
	for _, args := range [][]string{
		{}, {"-mode", "apply"}, {"-mode", "apply", "-plan-sha256", strings.Repeat("a", 64)},
		{"-mode", "plan", "-legacy-writers-stopped"}, {"-mode", "plan", "-plan-sha256", strings.Repeat("a", 64)},
		{"-mode", "auto"}, {"-timeout", "3m"}, {"-timeout", "0s"}, {"unexpected"},
	} {
		actual := append(append([]string{}, base...), args...)
		if len(args) == 0 {
			actual = nil
		}
		err := runLedgerReceipts(actual)
		if err == nil || strings.Contains(err.Error(), "no such file") {
			t.Fatalf("flags should fail before config or connection: %v %v", actual, err)
		}
	}
}

func TestLedgerReceiptPrivateDocuments(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private.json")
	want := ledgerReceiptRequest{Version: "ledger.receipt-handoff-request.v1", Action: "register"}
	if err := writeLedgerReceiptDocument(path, want, 1024); err != nil {
		t.Fatal(err)
	}
	var got ledgerReceiptRequest
	if err := redisstream.ReadLedgerReceiptDocument(path, 1024, &got); err != nil || got.Version != want.Version {
		t.Fatal(got, err)
	}
	before, _ := os.ReadFile(path)
	if err := writeLedgerReceiptDocument(path, map[string]bool{"replace": true}, 1024); err == nil {
		t.Fatal("existing document overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("existing document changed")
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := redisstream.ReadLedgerReceiptDocument(link, 1024, &got); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := redisstream.ReadLedgerReceiptDocument(path, 1024, &got); err == nil {
		t.Fatal("exposed owner accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := redisstream.ReadLedgerReceiptDocument(path, 1, &got); err == nil {
		t.Fatal("limit ignored")
	}
	for _, raw := range []string{`{"action":"register","action":"takeover"}`, `{"override":true}`, `{} {}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := redisstream.ReadLedgerReceiptDocument(path, 1024, &got); err == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	public := filepath.Join(dir, "public")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeLedgerReceiptDocument(filepath.Join(public, "new.json"), want, 1024); err == nil {
		t.Fatal("public output accepted")
	}
	if err := writeLedgerReceiptDocument(filepath.Join(dir, "large.json"), want, 1); err == nil {
		t.Fatal("output limit ignored")
	}
}
