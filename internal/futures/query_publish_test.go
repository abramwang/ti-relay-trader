package futures

import (
	"encoding/json"
	"strings"
	"testing"
)

func publicationTarget() TestQueryTarget {
	return TestQueryTarget{SourceID: "synthetic-source", ServerRunID: strings.Repeat("a", 40), Epoch: "synthetic-epoch", Group: "synthetic-group", CounterSessionID: "synthetic-session", TradeDate: "20261117", ConnectionGeneration: 1}
}

func TestQueryPublishTargetStrict(t *testing.T) {
	target := publicationTarget()
	raw, _ := json.Marshal(target)
	if _, err := DecodeTestQueryTarget(raw); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { delete(m, "epoch") },
		func(m map[string]any) { m["connection_generation"] = nil },
		func(m map[string]any) { m["connection_generation"] = 0 },
		func(m map[string]any) { m["server_run_id"] = "unknown" },
		func(m map[string]any) { m["trade_date"] = "20260230" },
		func(m map[string]any) { m["extra"] = true },
	} {
		if _, err := DecodeTestQueryTarget(editBody(t, raw, change)); err == nil {
			t.Fatal("unsafe target accepted")
		}
	}
	dup := append([]byte(`{"epoch":"other",`), raw[1:]...)
	if _, err := DecodeTestQueryTarget(dup); err == nil {
		t.Fatal("duplicate target accepted")
	}
}

func TestQueryPublisherOfflineSourceAndNoRetry(t *testing.T) {
	c := sourceFixture()
	s, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	target := publicationTarget()
	target.SourceID = s.String()
	p, err := NewTestQueryPublisher(c, target)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	opts := p.client.Options()
	if opts.MaxRetries != 0 || opts.DialerRetries != 1 || opts.Protocol != 2 || opts.MaxActiveConns != 1 {
		t.Fatal("unsafe retry/pool settings")
	}
	target.SourceID = "other"
	if _, err := NewTestQueryPublisher(c, target); err == nil {
		t.Fatal("wrong source accepted")
	}
	c.Scope.Route.Env = "prod"
	if _, err := NewTestQueryPublisher(c, target); err == nil {
		t.Fatal("prod publisher accepted")
	}
}
