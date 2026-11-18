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

func TestEventCaptureDefaultAndPermissions(t *testing.T) {
	var out bytes.Buffer
	args := append(replyArgs(t), "--stream-role", "event")
	if err := run(context.Background(), args, func(string) string { t.Fatal("offline read credentials"); return "" }, &out, &out); err != nil || !strings.Contains(out.String(), `"stream_role":"event"`) {
		t.Fatal(out.String(), err)
	}
	for _, extra := range [][]string{{"--stream-role", "unknown"}, {"--stream-role", "event", "--capture", "--persist-test-replies"}, {"--stream-role", "event", "--receive-reviewed", "--persist-test-events"}, {"--persist-test-events"}, {"--stream-role", "event", "--capture"}} {
		out.Reset()
		if err := run(context.Background(), append(replyArgs(t), extra...), func(string) string { t.Fatal("invalid mode read credentials"); return "" }, &out, &out); err == nil {
			t.Fatal("unsafe event invocation", extra)
		}
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

func TestResidentFollowOfflineAndFlags(t *testing.T) {
	for _, role := range []string{"reply", "event"} {
		var out bytes.Buffer
		args := append(replyArgs(t), "--follow", "--duration", "9h", "--stream-role", role)
		if err := run(context.Background(), args, func(string) string { t.Fatal("offline read credentials"); return "" }, &out, &out); err != nil || !strings.Contains(out.String(), `"follow":true`) {
			t.Fatal(out.String(), err)
		}
	}
	for _, extra := range [][]string{
		{"--follow", "--duration", "25h"}, {"--follow", "--status"},
		{"--follow", "--receive-reviewed", "--persist-test-replies"},
		{"--follow", "--max-pages", "1"}, {"--max-records", "1"},
		{"--follow", "--max-records", "0"}, {"--follow", "--max-records", "50001"},
		{"--follow", "--capture"}, {"--follow", "--persist-test-replies"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), append(replyArgs(t), extra...), func(string) string { t.Fatal("unsafe follow read credentials"); return "" }, &out, &out); err == nil {
			t.Fatal("unsafe follow accepted", extra)
		}
	}
}

func TestPairedResidentOfflineAndPermissions(t *testing.T) {
	file := filepath.Join(t.TempDir(), "positions.json")
	if err := os.WriteFile(file, []byte(`{"reply":{"cursor":"0-0"},"event":{"cursor":"0-0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	args := append(replyArgs(t), "--stream-role", "both", "--follow", "--duration", "9h", "--checkpoints-file", file)
	var out bytes.Buffer
	noEnv := func(string) string { t.Fatal("offline or invalid mode read credentials"); return "" }
	if err := run(context.Background(), args, noEnv, &out, &out); err != nil || !strings.Contains(out.String(), `"stream_role":"both"`) || !strings.Contains(out.String(), `"business_authorized":false`) {
		t.Fatal(out.String(), err)
	}
	for _, extra := range [][]string{
		{"--capture"}, {"--capture", "--persist-test-replies"}, {"--capture", "--persist-test-events"},
		{"--persist-test-replies", "--persist-test-events"}, {"--previous-owner", ""}, {"--cursor", "0-0"},
		{"--confirm-stopped"}, {"--receive-reviewed"}, {"--status"}, {"--review-file", "missing"},
		{"--max-pages", "1"}, {"--duration", "25h"}, {"--checkpoints-file", ""},
	} {
		out.Reset()
		if err := run(context.Background(), append(append([]string{}, args...), extra...), noEnv, &out, &out); err == nil {
			t.Fatal("unsafe paired invocation", extra)
		}
	}
	// Only fully paired write permission may proceed to credential validation.
	out.Reset()
	err := run(context.Background(), append(args, "--capture", "--persist-test-replies", "--persist-test-events"), func(string) string { return "" }, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "matching TEST database") {
		t.Fatal(out.String(), err)
	}
	// Explicit old owners require stopped confirmation even for offline preflight.
	old := `{"reply":{"owner":"` + strings.Repeat("a", 64) + `","cursor":"1-0"},"event":{"cursor":"0-0"}}`
	if err := os.WriteFile(file, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args, noEnv, &out, &out); err == nil {
		t.Fatal("unconfirmed takeover accepted")
	}
	if err := run(context.Background(), append(args, "--confirm-stopped"), noEnv, &out, &out); err != nil {
		t.Fatal(err)
	}
}
