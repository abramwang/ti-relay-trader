package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewJSONLogger(t *testing.T) {
	var out bytes.Buffer
	logger, err := New(&out, "debug", "json")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Debug("hello", "component", "test")

	got := out.String()
	if !strings.Contains(got, `"msg":"hello"`) {
		t.Fatalf("log output missing message: %s", got)
	}
	if !strings.Contains(got, `"component":"test"`) {
		t.Fatalf("log output missing attribute: %s", got)
	}
}

func TestNewRejectsInvalidLevel(t *testing.T) {
	_, err := New(&bytes.Buffer{}, "verbose", "json")
	if err == nil {
		t.Fatal("expected invalid level error")
	}
}

func TestNewRejectsInvalidFormat(t *testing.T) {
	_, err := New(&bytes.Buffer{}, "info", "xml")
	if err == nil {
		t.Fatal("expected invalid format error")
	}
}
