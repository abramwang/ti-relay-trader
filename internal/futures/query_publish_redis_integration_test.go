package futures

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
)

func TestQueryPublisherRedisAtomicGate(t *testing.T) {
	for _, scenario := range []string{"publish", "epoch_changed", "heartbeat_changed", "expired", "wrong_run", "wrong_generation", "closed", "duplicate_body"} {
		t.Run(scenario, func(t *testing.T) {
			db, _, h, client, _, ctx := realStreamRole(t, "hb")
			cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
			if err != nil {
				t.Fatal("TEST config unavailable")
			}
			prefix := strings.TrimSuffix(h.stream, ":hb")
			commandKey, epochKey := prefix+":cmd.query", prefix+":cmd.trade:transport-epoch"
			if n, err := client.Exists(ctx, commandKey, epochKey).Result(); err != nil || n != 0 {
				t.Fatal("development keys occupied", err)
			}
			t.Cleanup(func() {
				c, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				if err := client.Del(c, commandKey, epochKey).Err(); err != nil {
					t.Error(err)
				}
			})
			target := publicationTarget()
			info, err := client.Info(ctx, "server").Result()
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(info, "\r\n") {
				if strings.HasPrefix(line, "run_id:") {
					target.ServerRunID = strings.TrimPrefix(line, "run_id:")
				}
			}
			sourceCfg := TestSourceConfig{Scope: h.config.Scope, DeploymentID: "authorized-development", RedisURL: cfg.Redis.URL}
			source, err := NewTestStreamSource(sourceCfg)
			if err != nil {
				t.Fatal(err)
			}
			target.SourceID = source.String()
			source.Close()
			if err := client.XGroupCreateMkStream(ctx, commandKey, target.Group, "0-0").Err(); err != nil {
				t.Fatal(err)
			}
			if err := client.Expire(ctx, commandKey, time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			if ok, err := client.SetNX(ctx, epochKey, target.Epoch, time.Hour).Result(); err != nil || !ok {
				t.Fatal("epoch setup", err)
			}
			body := editBody(t, readOnlyScenarios(t)[0].Heartbeats[1], func(m map[string]any) {
				m["routing"] = h.config.Scope.Route
				m["counter_session_id"] = target.CounterSessionID
				m["produced_at"] = time.Now().Format(time.RFC3339Nano)
				p := m["payload"].(map[string]any)
				p["counter_session_id"], p["trade_date"] = target.CounterSessionID, target.TradeDate
				if scenario == "closed" {
					m["adapter_context"].(map[string]any)["query_collection_ready"] = false
				}
			})
			if scenario == "duplicate_body" {
				appendRealStream(t, ctx, client, h, "2-0", "body", body, "body", body)
			} else {
				appendRealStream(t, ctx, client, h, "2-0", "body", body)
			}
			if scenario == "wrong_run" {
				target.ServerRunID = strings.Repeat("0", 40)
			}
			if scenario == "wrong_generation" {
				target.ConnectionGeneration = 2
			}
			publisher, err := NewTestQueryPublisher(sourceCfg, target)
			if err != nil {
				t.Fatal(err)
			}
			defer publisher.Close()
			if oneOf(scenario, "closed", "wrong_run", "wrong_generation", "duplicate_body") {
				if _, err := publisher.inspect(ctx); err == nil {
					t.Fatal("closed gate accepted")
				}
			} else if scenario == "publish" {
				raw := editBody(t, []byte(intentVectors(t)["account.asset.query"].Raw), func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = h.config.Scope.Route.AccountID })
				store, _ := NewCommandStore(db)
				registration, err := store.Register(ctx, h.config.Scope, "cmd.query", raw, RegistrationEvidence{})
				if err != nil {
					t.Fatal(err)
				}
				result, err := store.PublishTestQuery(ctx, registration.CommandID, registration.Original.MessageID, publisher)
				if err != nil || result.Published == nil || !*result.Published || result.BrokerAccepted {
					t.Fatal(result, err)
				}
				again, err := store.PublishTestQuery(ctx, registration.CommandID, registration.Original.MessageID, publisher)
				if err != nil || !again.Replayed || again.StreamID != result.StreamID {
					t.Fatal("publication replay", again, err)
				}
				rows, err := client.XRange(ctx, commandKey, "-", "+").Result()
				if err != nil || len(rows) != 1 || rows[0].ID != result.StreamID || rows[0].Values["body"] != string(raw) {
					t.Fatal("wire bytes changed/duplicated", err)
				}
				return
			} else {
				proof, err := publisher.inspect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "epoch_changed":
					if err := client.Set(ctx, epochKey, "changed", time.Hour).Err(); err != nil {
						t.Fatal(err)
					}
				case "heartbeat_changed":
					appendRealStream(t, ctx, client, h, "3-0", "body", body)
				case "expired":
					proof.ExpiresAt = time.Now().Add(-time.Second)
				}
				id, rejected, err := publisher.appendQuery(ctx, proof, []byte("must not be published"))
				if err != nil || !rejected || id != "" {
					t.Fatal("atomic gate failed", id, rejected, err)
				}
			}
			if count, err := client.XLen(ctx, commandKey).Result(); err != nil || count != 0 {
				t.Fatal("gate appended command", count, err)
			}
		})
	}
}

// A network result that cannot prove non-publication remains unknown.
func TestQueryPublisherCancelledAppendIsUnknown(t *testing.T) {
	c := sourceFixture()
	source, err := NewTestStreamSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := publicationTarget()
	target.SourceID = source.String()
	p, err := NewTestQueryPublisher(c, target)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, rejected, err := p.appendQuery(ctx, queryPublishProof{}, nil)
	if rejected || !errors.Is(err, ErrQueryPublishUnknown) {
		t.Fatal("uncertain append reported unsent", err)
	}
}
