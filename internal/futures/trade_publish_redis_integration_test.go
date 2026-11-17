package futures

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
)

func TestAssessmentRedisGateAndExactOnce(t *testing.T) {
	for _, scenario := range []string{"publish", "epoch_changed", "heartbeat_changed", "expired", "wrong_generation", "closed", "readonly", "assessment_publish", "native_stale"} {
		t.Run(scenario, func(t *testing.T) {
			db, _, h, client, _, ctx := realStreamRole(t, "hb")
			migrateFuturesTest(t, db, ctx, "000046_futures_test_trade_publication", "up")
			cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
			if err != nil {
				t.Fatal(err)
			}
			prefix := strings.TrimSuffix(h.stream, ":hb")
			cmd, epoch := prefix+":cmd.trade", prefix+":cmd.trade:transport-epoch"
			if n, err := client.Exists(ctx, cmd, epoch).Result(); err != nil || n != 0 {
				t.Fatal("development keys occupied", err)
			}
			t.Cleanup(func() {
				c, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				if err := client.Del(c, cmd, epoch).Err(); err != nil {
					t.Error(err)
				}
			})
			v, _, target, a := assessmentFixture(t)
			scope := h.config.Scope
			v.Scope = map[string]string{"env": scope.Route.Env, "broker_id": scope.Route.BrokerID, "account_id": scope.Route.AccountID, "gateway_id": scope.Route.GatewayID, "native_broker_id": scope.NativeBrokerID, "investor_id": scope.InvestorID, "user_id": scope.UserID}
			v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = scope.Route.AccountID }))
			a.CommandSHA256 = commandDigest([]byte(v.Raw))
			e := registrationEvidence(t, v)
			info, err := client.Info(ctx, "server").Result()
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(info, "\r\n") {
				if strings.HasPrefix(line, "run_id:") {
					target.ServerRunID = strings.TrimPrefix(line, "run_id:")
				}
			}
			c := TestSourceConfig{Scope: scope, DeploymentID: "authorized-development", RedisURL: cfg.Redis.URL}
			source, err := NewTestStreamSource(c)
			if err != nil {
				t.Fatal(err)
			}
			target.SourceID = source.String()
			source.Close()
			if err := client.XGroupCreateMkStream(ctx, cmd, target.Group, "0-0").Err(); err != nil {
				t.Fatal(err)
			}
			if ok, err := client.SetNX(ctx, epoch, target.Epoch, time.Hour).Result(); err != nil || !ok {
				t.Fatal(err)
			}
			body := assessmentHeartbeat(t, scope.Route, target, time.Now()).Raw
			if oneOf(scenario, "assessment_publish", "native_stale") {
				row, _, _, _ := freshOCTradeHeartbeat(t, time.Now(), false)
				body = editBody(t, row.Raw, func(m map[string]any) {
					m["routing"], m["counter_session_id"] = scope.Route, target.CounterSessionID
					p := m["payload"].(map[string]any)
					p["counter_session_id"], p["trade_date"] = target.CounterSessionID, target.TradeDate
					e := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
					e["assessment_id"] = a.AssessmentID
					if scenario == "native_stale" {
						e["native_limits"].(map[string]any)["started_at"] = assessmentTime(time.Now().Add(-6 * time.Second))
					}
				})
			}
			if scenario == "closed" {
				body = editBody(t, body, func(m map[string]any) { m["payload"].(map[string]any)["accepting_trade_commands"] = false })
			}
			if scenario == "readonly" {
				body = editBody(t, body, func(m map[string]any) {
					m["adapter_context"].(map[string]any)["runtime_mode"] = "test_broker_read_only"
				})
			}
			appendRealStream(t, ctx, client, h, "2-0", "body", body)
			if scenario == "wrong_generation" {
				target.ConnectionGeneration = 2
			}
			p, err := NewTestTradePublisher(c, target, a)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if oneOf(scenario, "closed", "readonly", "wrong_generation", "native_stale") {
				if _, err := p.inspect(ctx); err == nil {
					t.Fatal("invalid gate accepted")
				}
			} else if oneOf(scenario, "publish", "assessment_publish") {
				s, _ := NewCommandStore(db)
				r := registerVector(t, s, ctx, v, e, "registered")
				result, err := s.PublishTestTrade(ctx, r.CommandID, r.Original.MessageID, p)
				if err != nil || result.Published == nil || !*result.Published || result.BrokerAccepted {
					t.Fatal(result, err)
				}
				again, err := s.PublishTestTrade(ctx, r.CommandID, r.Original.MessageID, p)
				if err != nil || !again.Replayed || again.StreamID != result.StreamID {
					t.Fatal(again, err)
				}
				rows, err := client.XRange(ctx, cmd, "-", "+").Result()
				if err != nil || len(rows) != 1 || rows[0].Values["body"] != v.Raw {
					t.Fatal("wire bytes or count changed", err)
				}
				if n, err := client.Exists(ctx, prefix+":cmd.query").Result(); err != nil || n != 0 {
					t.Fatal("wrong command stream")
				}
				// Raw event capture uses the existing transport binding and keeps
				// audit responsibility pending; it does not invent a business event.
				eventKey := prefix + ":event"
				if n, err := client.Exists(ctx, eventKey).Result(); err != nil || n != 0 {
					t.Fatal("event namespace occupied", err)
				}
				if err := client.XAdd(ctx, &redis.XAddArgs{Stream: eventKey, ID: "1-0", Values: []any{"body", `{"synthetic_audit_only":true}`}}).Err(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					clean, done := context.WithTimeout(context.Background(), 5*time.Second)
					defer done()
					if err := client.Del(clean, eventKey).Err(); err != nil {
						t.Error(err)
					}
				})
				source, err := NewTestStreamSource(c)
				if err != nil {
					t.Fatal(err)
				}
				defer source.Close()
				store, _ := NewStreamStore(db)
				options := TestQueryReceiveOptions{Role: "event", Checkpoint: HeartbeatCheckpoint{Cursor: "0-0"}, PageSize: 32, MaxPages: 2}
				var progress QueryReceiveProgress
				emit := func(p QueryReceiveProgress) error { progress = p; return nil }
				if err := ReceiveTestQueryReplies(ctx, source, store, p.base, options, emit); err != nil {
					t.Fatal(err)
				}
				if progress.Captured != 1 || progress.Pending != 1 || progress.Delivered != 0 || progress.BusinessAuthorized {
					t.Fatal(progress)
				}
				cp, err := store.EventCheckpoint(ctx, source.StreamConfig("event"))
				if err != nil {
					t.Fatal(err)
				}
				options.Checkpoint = cp
				if err := ReceiveTestQueryReplies(ctx, source, store, p.base, options, emit); err != nil {
					t.Fatal(err)
				}
				if progress.Captured != 0 || progress.Cursor != "1-0" {
					t.Fatal("resume duplicated event", progress)
				}
				options.Review = []byte("not permitted")
				if err := ReceiveTestQueryReplies(ctx, source, store, p.base, options, emit); err == nil {
					t.Fatal("event accepted query projection")
				}
				return
			} else {
				proof, err := p.inspect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "epoch_changed":
					if err := client.Set(ctx, epoch, "other", time.Hour).Err(); err != nil {
						t.Fatal(err)
					}
				case "heartbeat_changed":
					appendRealStream(t, ctx, client, h, "3-0", "body", body)
				case "expired":
					proof.ExpiresAt = time.Now().Add(-time.Second)
				}
				id, rejected, err := p.appendQuery(ctx, proof, []byte(v.Raw))
				if err != nil || !rejected || id != "" {
					t.Fatal("atomic guard failed", err)
				}
			}
			if n, err := client.XLen(ctx, cmd).Result(); err != nil || n != 0 {
				t.Fatal("closed gate appended", err)
			}
		})
	}
}
