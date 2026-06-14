package redisstream

import (
	"testing"
	"time"
)

func TestNewCommandEnvelope(t *testing.T) {
	sentAt := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	envelope := NewCommandEnvelope(ActionOrderSubmit, "msg-1", "req-1", "corr-1", "idem-1", map[string]any{
		"account_id": "acct-1",
	}, sentAt)

	if envelope.Protocol != Protocol {
		t.Fatalf("protocol = %q", envelope.Protocol)
	}
	if envelope.MessageType != "command" || envelope.Action != ActionOrderSubmit {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope.SentAt != "2026-06-13T18:00:00+08:00" {
		t.Fatalf("sent_at = %q", envelope.SentAt)
	}
}

func TestCommandStreamForAction(t *testing.T) {
	streams := NewStreams("relay:prod:v1:huaxin:00030484")

	stream, err := CommandStreamForAction(streams, ActionOrderSubmit)
	if err != nil {
		t.Fatalf("CommandStreamForAction() error = %v", err)
	}
	if stream != "relay:prod:v1:huaxin:00030484:cmd.trade" {
		t.Fatalf("stream = %q", stream)
	}

	stream, err = CommandStreamForAction(streams, "account.asset.query")
	if err != nil {
		t.Fatalf("CommandStreamForAction() query error = %v", err)
	}
	if stream != "relay:prod:v1:huaxin:00030484:cmd.query" {
		t.Fatalf("query stream = %q", stream)
	}
}

func TestCommandStreamForActionRejectsUnknown(t *testing.T) {
	_, err := CommandStreamForAction(NewStreams("relay:test"), "unknown.action")
	if err == nil {
		t.Fatal("expected error")
	}
}
