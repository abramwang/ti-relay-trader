package redisstream

import (
	"strings"
	"testing"
)

func TestMaskRedisAddr(t *testing.T) {
	masked := maskRedisAddr("redis://:secret@127.0.0.1:6379/0")
	if strings.Contains(masked, "secret") {
		t.Fatalf("masked address leaked password: %s", masked)
	}
	if !strings.Contains(masked, "%2A%2A%2A") {
		t.Fatalf("masked address missing replacement: %s", masked)
	}
}
