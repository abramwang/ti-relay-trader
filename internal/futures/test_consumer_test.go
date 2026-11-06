package futures

import (
	"context"
	"strings"
	"testing"
	"time"
)

func sourceFixture() TestSourceConfig {
	c := TestSourceConfig{Scope: streamTestConfig("event").Scope, DeploymentID: "synthetic-deployment", RedisURL: "redis://sample:synthetic-secret@localhost:6379/0"}
	c.Scope.Route.BrokerID = "ctp"
	return c
}

func TestTestSourceFingerprintAndSafety(t *testing.T) {
	c := sourceFixture()
	s, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if strings.Contains(s.String(), "secret") || strings.Contains(s.String(), "localhost") {
		t.Fatal("source identity exposes connection details")
	}
	for _, change := range []struct {
		url, deployment string
		same            bool
	}{
		{"redis://rotated:other-secret@localhost:6379/0", c.DeploymentID, true},
		{"redis://localhost:6379/1", c.DeploymentID, false},
		{"redis://localhost:6380/0", c.DeploymentID, false},
		{"rediss://localhost:6379/0", c.DeploymentID, false},
		{c.RedisURL, "replacement", false},
	} {
		candidate := c
		candidate.RedisURL, candidate.DeploymentID = change.url, change.deployment
		next, err := NewTestStreamSource(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if (s.String() == next.String()) != change.same {
			t.Fatal("wrong endpoint binding")
		}
		_ = next.Close()
	}
	for _, change := range []func(*TestSourceConfig){
		func(c *TestSourceConfig) { c.Scope.Route.Env = "prod" },
		func(c *TestSourceConfig) { c.Scope.Route.BrokerID = "huaxin" },
		func(c *TestSourceConfig) { c.DeploymentID = "" },
		func(c *TestSourceConfig) { c.RedisURL = "invalid://sample:synthetic-secret@host" },
	} {
		bad := c
		change(&bad)
		if _, err := NewTestStreamSource(bad); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("unsafe source accepted or secret exposed")
		}
	}
	if _, err := s.ReadOnce(context.Background(), nil, StreamHandle{}, 1); err == nil {
		t.Fatal("unbound source read")
	}
}

func TestTestConsumerBackoffBounds(t *testing.T) {
	options := DefaultTestConsumerOptions()
	delay := options.MinDelay
	for i := 0; i < 100; i++ {
		next := consumerDelay(delay, options, false)
		if next < delay || next > options.MaxDelay {
			t.Fatal(next)
		}
		delay = next
	}
	if delay != options.MaxDelay || consumerDelay(delay, options, true) != options.MinDelay {
		t.Fatal("backoff cap/reset failed")
	}
	options.MinDelay, options.MaxDelay = 10*time.Millisecond, 10*time.Millisecond
	if consumerDelay(options.MinDelay, options, false) != options.MaxDelay {
		t.Fatal("equal delay bounds failed")
	}
}
