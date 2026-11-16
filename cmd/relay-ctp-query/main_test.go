package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func queryArgs(t *testing.T, action string) []string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"protocol": "relay.stream.v1", "message_type": "command", "message_id": "synthetic-message",
		"request_id": "synthetic-request", "correlation_id": "synthetic-correlation", "idempotency_key": "synthetic-idempotency",
		"sent_at": "2026-11-17T09:05:00+08:00", "action": action,
		"payload": map[string]any{"account_id": "990000000117", "product_type": "futures", "schema_version": "ctp.f0.draft.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "command.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"--env", "test", "--broker", "ctp", "--gateway", "relay-dev-query", "--account", "990000000117", "--native-broker", "4900", "--user", "fake-user", "--investor", "investor01", "--database", "relay_query_test", "--command-file", file}
}

func TestFourQueriesDefaultOffline(t *testing.T) {
	for _, action := range []string{"account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"} {
		var out bytes.Buffer
		if err := run(context.Background(), queryArgs(t, action), func(string) string { t.Fatal("offline read credentials"); return "" }, &out, &out); err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err := json.Unmarshal(out.Bytes(), &report); err != nil || report["mode"] != "check_only_no_connections" || report["command_published"] != false || report["broker_accepted"] != false || report["business_authorized"] != false {
			t.Fatal("preparation was promoted", out.String(), err)
		}
	}
}

func TestUnsafeQueryFlagsNeverReadCredentials(t *testing.T) {
	for _, extra := range [][]string{{"--env", "prod"}, {"--broker", "huaxin"}, {"--user", ""}, {"--investor", ""}, {"--database", "relay_trader"}, {"--register"}, {"--persist-test-intent"}, {"--run"}, {"--publish"}, {"unexpected"}} {
		var out bytes.Buffer
		if err := run(context.Background(), append(queryArgs(t, "order.list.query"), extra...), func(string) string { t.Fatal("unsafe flags read credentials"); return "" }, &out, &out); err == nil {
			t.Fatal("unsafe flags accepted", extra)
		}
	}
}

func TestTradingActionsRejected(t *testing.T) {
	for _, action := range []string{"order.submit", "order.batch.submit", "order.cancel", "unknown"} {
		var out bytes.Buffer
		if err := run(context.Background(), queryArgs(t, action), func(string) string { t.Fatal("trade read credentials"); return "" }, &out, &out); err == nil {
			t.Fatal("trade accepted", action)
		}
	}
}

func TestQueryDatabaseFailureDoesNotLeakSecrets(t *testing.T) {
	for _, dsn := range []string{"", "invalid-secret-DSN", "postgres://user:secret@localhost/relay_trader"} {
		var out bytes.Buffer
		err := run(context.Background(), append(queryArgs(t, "order.list.query"), "--register", "--persist-test-intent"), func(string) string { return dsn }, &out, &out)
		if err == nil || strings.Contains(out.String()+err.Error(), "secret") {
			t.Fatal("database safety", err)
		}
	}
}
