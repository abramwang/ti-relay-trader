package orderflow

import (
	"context"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/redisstream"
)

type inertSinglePublisher struct{ fakePublisher }

type unusedCommandJournal struct{ SecuritiesCommandJournal }

func (p *inertSinglePublisher) PublishCommandBodyOnce(context.Context, string, []byte) (redisstream.CommandPublishResult, error) {
	panic("must not publish")
}

func TestRegisteredCommandConfigurationGuards(t *testing.T) {
	for _, kind := range []string{"production", "wrong-redis", "unnamed-database", "legacy-publisher", "missing-store", "disabled-with-store", "valid"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testConfig(true, true)
			cfg.Service.Environment = config.EnvironmentTest
			cfg.Service.SecuritiesCommandRegistration = true
			cfg.Redis.Env = "test"
			cfg.Database.ExpectedName = "isolated_test"
			var pub CommandPublisher = &inertSinglePublisher{}
			var journal SecuritiesCommandJournal = unusedCommandJournal{}
			switch kind {
			case "production":
				cfg.Service.Environment = config.EnvironmentProduction
			case "wrong-redis":
				cfg.Redis.Env = "prod"
			case "unnamed-database":
				cfg.Database.ExpectedName = "live"
			case "legacy-publisher":
				pub = &fakePublisher{}
			case "missing-store":
				journal = nil
			case "disabled-with-store":
				cfg.Service.SecuritiesCommandRegistration = false
			}
			_, err := New(Options{Config: cfg, Ledger: &fakeLedger{}, Publisher: pub, CommandJournal: journal})
			if (kind == "valid") != (err == nil) {
				t.Fatal("unexpected construction result", err)
			}
		})
	}
}
