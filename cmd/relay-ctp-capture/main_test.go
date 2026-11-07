package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func captureArgs() []string {
	return []string{"--env", "test", "--broker", "ctp", "--gateway", "relay-dev-example", "--account", "990000000001", "--deployment-id", "synthetic-deployment", "--database", "relay_capture_test"}
}

func TestCaptureCheckOnlyAndHelp(t *testing.T) {
	for _, args := range [][]string{captureArgs(), {"--help"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, func(string) string { t.Fatal("offline mode read credentials"); return "" }, &out, &out); err != nil {
			t.Fatal(err)
		}
		if len(args) > 1 && !strings.Contains(out.String(), "check_only_no_connections") {
			t.Fatal(out.String())
		}
	}
}

func TestCaptureUnsafeFlagsNeverReadCredentials(t *testing.T) {
	for _, extra := range [][]string{
		{"--env", "prod"}, {"--broker", "huaxin"}, {"--database", "relay_trader"}, {"--run"}, {"--persist-test-audit"},
		{"--status", "--run", "--persist-test-audit"}, {"--resume-cursor", "1-0"}, {"--resume-owner", strings.Repeat("a", 64)},
		{"--confirm-stopped"}, {"--timeout", "6m"}, {"--pages", "257"}, {"--limit", "0"}, {"--deployment-id", ""}, {"unexpected"},
		{"--audit"}, {"--include-raw"}, {"--audit-owner", strings.Repeat("a", 64)},
		{"--audit", "--status"}, {"--audit", "--run", "--persist-test-audit"},
		{"--audit", "--audit-owner", strings.Repeat("a", 64), "--audit-cursor", "$"},
		{"--audit", "--audit-owner", strings.Repeat("a", 64), "--audit-cursor", "0-0", "--after-receipt", "-1"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), append(captureArgs(), extra...), func(string) string { t.Fatal("unsafe flags accessed secrets"); return "" }, &out, &out); err == nil {
			t.Fatal("accepted unsafe flags", extra)
		}
	}
}

func TestCaptureMissingOrWrongDestination(t *testing.T) {
	for _, values := range [][2]string{{"", ""}, {"redis://:secret@localhost:0", "invalid secret dsn"}, {"redis://:secret@localhost:0", "postgres://localhost/relay_trader"}} {
		var out bytes.Buffer
		err := run(context.Background(), append(captureArgs(), "--run", "--persist-test-audit"), func(key string) string {
			if key == "RELAY_CTP_CAPTURE_REDIS_URL" {
				return values[0]
			}
			return values[1]
		}, &out, &out)
		if err == nil || strings.Contains(out.String()+err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}
