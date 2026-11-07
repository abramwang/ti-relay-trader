package futures

import (
	"context"
	"testing"
)

func TestHeartbeatCaptureSourceIsTransportOnly(t *testing.T) {
	c := sourceFixture()
	c.Scope = CommandScope{Route: c.Scope.Route}
	s, err := NewTestHeartbeatSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.StreamConfig("hb").stream(); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"reply", "event"} {
		cfg := s.StreamConfig(role)
		if _, err := cfg.stream(); err == nil {
			t.Fatal("missing native identity accepted for business stream")
		}
		if s.matches(StreamHandle{config: cfg}) {
			t.Fatal("transport source matched business handle")
		}
	}
	if _, err := NewTestStreamSource(c); err == nil {
		t.Fatal("normal source accepted absent native identity")
	}
	for _, mutate := range []func(*TestSourceConfig){
		func(c *TestSourceConfig) { c.Scope.NativeBrokerID = "4900" },
		func(c *TestSourceConfig) { c.Scope.Route.Env = "prod" },
		func(c *TestSourceConfig) { c.Scope.Route.BrokerID = "huaxin" },
		func(c *TestSourceConfig) { c.DeploymentID = "" },
	} {
		bad := c
		mutate(&bad)
		if v, err := NewTestHeartbeatSource(bad); err == nil {
			v.Close()
			t.Fatal("invalid source accepted")
		}
	}
	if err := CaptureTestHeartbeats(context.Background(), s, nil, HeartbeatCheckpoint{}, HeartbeatCaptureOptions{1, 1}, nil); err == nil {
		t.Fatal("missing bounded runtime accepted")
	}
}
