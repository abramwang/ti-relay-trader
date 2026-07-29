package redisstream

import (
	"strings"
	"testing"
)

func TestNormalizeArchiveReplayOptions(t *testing.T) {
	options, err := normalizeArchiveReplayOptions(ArchiveReplayOptions{
		AccountID: " acct-1 ",
		DateFrom:  "2026-07-01",
		DateTo:    "2026-07-29",
		Stages:    []string{" Orders ", "fills", "orders"},
	})
	if err != nil {
		t.Fatalf("normalizeArchiveReplayOptions() error = %v", err)
	}
	if options.AccountID != "acct-1" || options.DateFrom != "2026-07-01" || options.DateTo != "2026-07-29" {
		t.Fatalf("normalized options = %#v", options)
	}
	if len(options.Stages) != 2 || options.Stages[0] != "orders" || options.Stages[1] != "fills" {
		t.Fatalf("normalized stages = %#v", options.Stages)
	}

	if _, err := normalizeArchiveReplayOptions(ArchiveReplayOptions{
		DateFrom: "2026-07-30",
		DateTo:   "2026-07-29",
	}); err == nil {
		t.Fatal("normalizeArchiveReplayOptions() accepted reversed dates")
	}
	if _, err := normalizeArchiveReplayOptions(ArchiveReplayOptions{
		Stages: []string{"transfers"},
	}); err != nil {
		t.Fatalf("normalizeArchiveReplayOptions() rejected transfers stage: %v", err)
	}
	if _, err := normalizeArchiveReplayOptions(ArchiveReplayOptions{
		Stages: []string{"unknown"},
	}); err == nil {
		t.Fatal("normalizeArchiveReplayOptions() accepted unknown stage")
	}
}

func TestBuildArchiveReplayQueryScopesAccountAndDates(t *testing.T) {
	query, args := buildArchiveReplayQuery(ArchiveReplayOptions{
		AccountID: "acct-1",
		DateFrom:  "2026-07-01",
		DateTo:    "2026-07-29",
	}, "(event_type = 'order.event' OR action = 'order.list.query')")

	for _, fragment := range []string{
		"event_type = 'order.event'",
		"account_id = $1",
		"received_at AT TIME ZONE 'Asia/Shanghai'",
		">= $2::date",
		"<= $3::date",
		"ORDER BY received_at, raw_message_pk",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("query missing %q:\n%s", fragment, query)
		}
	}
	if len(args) != 3 || args[0] != "acct-1" || args[1] != "2026-07-01" || args[2] != "2026-07-29" {
		t.Fatalf("args = %#v", args)
	}
}
