package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func inspectArgs() []string {
	return []string{"--env", "test", "--broker", "ctp", "--gateway", "relay-dev-cli", "--account", "990000000001"}
}

func TestCheckOnlyNeverReadsCredentials(t *testing.T) {
	var out, diagnostic bytes.Buffer
	err := run(context.Background(), inspectArgs(), func(string) string { t.Fatal("check-only accessed credentials"); return "" }, &out, &diagnostic)
	if err != nil || !strings.Contains(out.String(), `"mode":"check_only_no_connections"`) || !strings.Contains(out.String(), `"business_authorized":false`) || diagnostic.Len() != 0 {
		t.Fatal(out.String(), err)
	}
}

func TestInspectionCLIRejectsUnsafeArguments(t *testing.T) {
	for _, extra := range [][]string{{"--env", "prod"}, {"--broker", "huaxin"}, {"--after", "$"}, {"--limit", "257"}, {"--timeout", "31s"}, {"--timeout", "0s"}, {"unexpected"}} {
		var out bytes.Buffer
		err := run(context.Background(), append(inspectArgs(), extra...), func(string) string { t.Fatal("invalid arguments accessed credentials"); return "" }, &out, &out)
		if err == nil {
			t.Fatal("unsafe arguments accepted", extra)
		}
	}
}

func TestInspectionCLICredentialsStayOutOfErrors(t *testing.T) {
	for _, value := range []string{"", "secret-not-a-url", "redis://:secret@127.0.0.1:0/0"} {
		var out, diagnostic bytes.Buffer
		err := run(context.Background(), append(inspectArgs(), "--run", "--timeout", "100ms"), func(key string) string {
			if key != "RELAY_CTP_INSPECT_REDIS_URL" {
				t.Fatal(key)
			}
			return value
		}, &out, &diagnostic)
		if err == nil || strings.Contains(err.Error()+out.String()+diagnostic.String(), "secret") || out.Len() != 0 {
			t.Fatal("credential leak or success", err)
		}
	}
}

func TestInspectionCLIHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--help"}, func(string) string { t.Fatal("help accessed credentials"); return "" }, &out, &out); err != nil || !strings.Contains(out.String(), "-run") {
		t.Fatal(err)
	}
}
