package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func replyArgs(t *testing.T) []string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "target.json")
	raw := `{"source_id":"synthetic-source","server_run_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","epoch":"synthetic-epoch","group":"synthetic-group","counter_session_id":"synthetic-session","trade_date":"20261117","connection_generation":1}`
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"--env", "test", "--broker", "ctp", "--gateway", "relay-dev-replies", "--account", "990000000117", "--native-broker", "4900", "--user", "fake-user", "--investor", "investor01", "--database", "relay_replies_test", "--deployment-id", "synthetic", "--target-file", file}
}

func TestRepliesDefaultOffline(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), replyArgs(t), func(string) string { t.Fatal("offline read credentials"); return "" }, &out, &out)
	if err != nil || !strings.Contains(out.String(), `"mode":"check_only_no_connections"`) || !strings.Contains(out.String(), `"business_authorized":false`) {
		t.Fatal(out.String(), err)
	}
}

func TestRepliesUnsafeFlagsOffline(t *testing.T) {
	for _, extra := range [][]string{{"--env", "prod"}, {"--broker", "huaxin"}, {"--user", ""}, {"--database", "relay_trader"}, {"--capture"}, {"--persist-test-replies"}, {"--receive-reviewed", "--persist-test-replies"}, {"--capture", "--receive-reviewed", "--persist-test-replies"}, {"--status", "--capture", "--persist-test-replies"}, {"--duration", "6m"}, {"--max-pages", "257"}, {"--page-size", "0"}, {"--previous-owner", "old"}, {"--confirm-stopped"}, {"--cursor", "1-0"}, {"--review-file", "missing"}, {"--publish"}, {"unexpected"}} {
		var out bytes.Buffer
		if err := run(context.Background(), append(replyArgs(t), extra...), func(string) string { t.Fatal("unsafe invocation read credentials"); return "" }, &out, &out); err == nil {
			t.Fatal("accepted", extra)
		}
	}
}

func TestRepliesDatabaseErrorsRedacted(t *testing.T) {
	for _, dsn := range []string{"", "secret-invalid", "postgres://u:private-password@localhost/production"} {
		var out bytes.Buffer
		err := run(context.Background(), append(replyArgs(t), "--capture", "--persist-test-replies"), func(string) string { return dsn }, &out, &out)
		if err == nil || strings.Contains(out.String()+err.Error(), "private-password") || strings.Contains(out.String()+err.Error(), "secret-invalid") {
			t.Fatal("unsafe diagnostic", err)
		}
	}
}
