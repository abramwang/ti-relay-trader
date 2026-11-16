package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
)

func seedPublishedReview(t *testing.T, db *sql.DB, ctx context.Context, r CommandRegistration, target TestQueryTarget) {
	t.Helper()
	raw, _ := json.Marshal(target)
	_, err := db.ExecContext(ctx, `INSERT INTO futures_test_query_publications(command_id,target_json,proof_json,state,entry_id,finished_at) VALUES($1,$2,'{}','published','2-0',clock_timestamp())`, r.CommandID, raw)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewedQueryAtomicBindingAndRestart(t *testing.T) {
	for _, scenario := range []string{"ok", "unknown", "target", "expired", "evidence_failure"} {
		t.Run(scenario, func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			r, c := registeredQueryCommand(t, db, ctx, batchContext("asset"))
			target := publicationTarget()
			target.CounterSessionID = c.CounterSessionID
			target.TradeDate = c.TradeDate
			seedPublishedReview(t, db, ctx, r, target)
			switch scenario {
			case "unknown":
				_, _ = db.ExecContext(ctx, `UPDATE futures_test_query_publications SET state='attempted',entry_id=NULL,finished_at=NULL`)
			case "target":
				target.Epoch = "other"
			case "expired":
				_, _ = db.ExecContext(ctx, `UPDATE futures_test_query_publications SET attempted_at=clock_timestamp()-interval '2 minutes'`)
			case "evidence_failure":
				_, err := db.ExecContext(ctx, `CREATE FUNCTION reject_review() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER fail_review BEFORE INSERT ON futures_test_query_reviews FOR EACH ROW EXECUTE FUNCTION reject_review()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, evidence := reviewFixture(t, c, target, r.CommandID)
			_, _, err := q.RegisterReviewedQuery(ctx, raw, evidence)
			if scenario != "ok" {
				if err == nil || storedCount(t, db, "futures_query_batches") != 0 || storedCount(t, db, "futures_test_query_reviews") != 0 {
					t.Fatal("failed review partly committed", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = q.RegisterReviewedQuery(ctx, raw, evidence); err != nil {
				t.Fatal(err)
			}
			var saved []byte
			if err = db.QueryRowContext(ctx, `SELECT evidence FROM futures_test_query_reviews`).Scan(&saved); err != nil || !bytes.Equal(saved, evidence) {
				t.Fatal("raw evidence changed", err)
			}
			changed := editBody(t, raw, func(m map[string]any) { m["review_id"] = "other-review" })
			if _, _, err = q.RegisterReviewedQuery(ctx, changed, evidence); err == nil {
				t.Fatal("review overwritten")
			}
			fresh, _ := NewQueryStore(db)
			if _, _, err = fresh.RegisterReviewedQuery(ctx, raw, evidence); err == nil {
				t.Fatal("new process resumed partial query")
			}
			if state, err := fresh.Recover(ctx, "reviewed-query"); err != nil || state != BatchInterrupted {
				t.Fatal(state, err)
			}
			if _, _, err = q.RegisterReviewedQuery(ctx, raw, evidence); err == nil {
				t.Fatal("retired batch reused")
			}
		})
	}
}

func TestReplyCheckpointCAS(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, _ := NewStreamStore(db)
	c := streamTestConfig("reply")
	target := publicationTarget()
	target.SourceID = c.SourceID
	cp, err := s.ReplyCheckpoint(ctx, c)
	if err != nil || cp.Registered {
		t.Fatal(cp, err)
	}
	h, err := s.OpenReplyCapture(ctx, c, cp, target)
	if err != nil {
		t.Fatal(err)
	}
	captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", []byte("audit original")})
	if _, err = s.OpenReplyCapture(ctx, c, cp, target); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("implicit takeover", err)
	}
	cp, err = s.ReplyCheckpoint(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	wrong := target
	wrong.Epoch = "replacement"
	if _, err = s.OpenReplyCapture(ctx, c, cp, wrong); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("epoch replaced under saved cursor", err)
	}
	if _, err = s.Status(ctx, h); err != nil {
		t.Fatal("rejected takeover changed owner", err)
	}
	if _, err = s.OpenReplyCapture(ctx, c, cp, target); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Status(ctx, h); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("old owner accepted", err)
	}
	c.Scope.Route.Env = "prod"
	if _, err = s.ReplyCheckpoint(ctx, c); err == nil {
		t.Fatal("prod accepted")
	}
}

// Real shared TEST Redis, synthetic SDK derivatives in random development keys.
func TestReviewedQueryRedisReceive(t *testing.T) {
	for _, scenario := range []string{"asset", "positions", "order", "fill", "failed", "wrong_request", "partial", "raw_closed", "epoch_changed", "contradiction", "not_created"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, h, client, _, ctx := realStreamRole(t, "reply")
			cfg, err := config.Load(os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG"))
			if err != nil {
				t.Fatal("TEST config unavailable")
			}
			sourceCfg := TestSourceConfig{Scope: h.config.Scope, DeploymentID: "authorized-development", RedisURL: cfg.Redis.URL}
			source, err := NewTestStreamSource(sourceCfg)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			// The fixture's empty receiver has no receipts; use the real source binding.
			if _, err = db.ExecContext(ctx, `DELETE FROM futures_stream_receivers WHERE stream_key=$1`, h.stream); err != nil {
				t.Fatal(err)
			}
			c := batchContext("positions")
			var body []byte
			switch scenario {
			case "asset":
				c = batchContext("asset")
				body = fixtureBody(t, "messages.json", "asset")
			case "order", "fill":
				c = tradeQueryContext(scenario)
				body = tradePage(t, scenario, "query-page", 0, true)
			default:
				body = fixtureBody(t, "messages.json", "positions")
			}
			c.Route = h.config.Scope.Route
			r, c := registeredQueryCommand(t, db, ctx, c)
			target := publicationTarget()
			target.SourceID = source.String()
			target.CounterSessionID = c.CounterSessionID
			target.TradeDate = c.TradeDate
			info, err := client.Info(ctx, "server").Result()
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(info, "\r\n") {
				if strings.HasPrefix(line, "run_id:") {
					target.ServerRunID = strings.TrimPrefix(line, "run_id:")
				}
			}
			prefix := strings.TrimSuffix(h.stream, ":reply")
			cmdKey, epochKey, hbKey := prefix+":cmd.query", prefix+":cmd.trade:transport-epoch", prefix+":hb"
			t.Cleanup(func() {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := client.Del(c, cmdKey, epochKey, hbKey).Err(); err != nil {
					t.Error(err)
				}
			})
			if n, err := client.Exists(ctx, cmdKey, epochKey, hbKey).Result(); err != nil || n != 0 {
				t.Fatal("occupied development namespace", err)
			}
			if err = client.XGroupCreateMkStream(ctx, cmdKey, target.Group, "0-0").Err(); err != nil {
				t.Fatal(err)
			}
			if err = client.Expire(ctx, cmdKey, time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			if ok, err := client.SetNX(ctx, epochKey, target.Epoch, time.Hour).Result(); err != nil || !ok {
				t.Fatal(err)
			}
			hb := editBody(t, readOnlyScenarios(t)[0].Heartbeats[1], func(m map[string]any) {
				m["routing"] = c.Route
				m["counter_session_id"] = c.CounterSessionID
				m["produced_at"] = time.Now().Format(time.RFC3339Nano)
				p := m["payload"].(map[string]any)
				p["counter_session_id"] = c.CounterSessionID
				p["trade_date"] = c.TradeDate
				if scenario == "raw_closed" {
					m["adapter_context"].(map[string]any)["query_collection_ready"] = false
				}
			})
			if err = client.XAdd(ctx, &redis.XAddArgs{Stream: hbKey, Values: []any{"body", hb}}).Err(); err != nil {
				t.Fatal(err)
			}
			if err = client.Expire(ctx, hbKey, time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			publisher, err := NewTestQueryPublisher(sourceCfg, target)
			if err != nil {
				t.Fatal(err)
			}
			defer publisher.Close()
			wantCommands := int64(0)
			if scenario == "asset" {
				commands, _ := NewCommandStore(db)
				published, err := commands.PublishTestQuery(ctx, r.CommandID, r.Original.MessageID, publisher)
				if err != nil || published.Published == nil || !*published.Published {
					t.Fatal("actual development query publication", published, err)
				}
				wantCommands = 1
			} else {
				seedPublishedReview(t, db, ctx, r, target)
			}
			review, evidence := reviewFixture(t, c, target, r.CommandID)
			body = boundReply(t, body, r)
			body = editBody(t, body, func(m map[string]any) {
				m["routing"] = c.Route
				if scenario == "wrong_request" {
					m["adapter_context"].(map[string]any)["query_evidence"].(map[string]any)["sdk_request_id"] = 999
				}
				if scenario == "partial" {
					m["chunk"].(map[string]any)["is_last"] = false
					m["status"] = "partial"
				}
				if scenario == "failed" {
					m["status"] = "failed"
					m["code"] = "QUERY_FAILED"
					delete(m, "chunk")
					delete(m["payload"].(map[string]any), "items")
					e := m["adapter_context"].(map[string]any)["query_evidence"].(map[string]any)
					e["snapshot_applied"] = false
					e["callback_error_id"] = 42
				}
			})
			appendRealStream(t, ctx, client, h, "2-0", "body", body)
			if scenario == "not_created" {
				if err = client.Del(ctx, h.stream).Err(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "contradiction" {
				bad := editBody(t, body, func(m map[string]any) { m["code"] = "NOT_OK" })
				appendRealStream(t, ctx, client, h, "3-0", "body", bad)
			}
			options := TestQueryReceiveOptions{Checkpoint: HeartbeatCheckpoint{Cursor: "0-0"}, PageSize: 2, MaxPages: 8, Review: review, Evidence: evidence}
			if scenario == "partial" {
				options.MaxPages = 2
			}
			if scenario == "raw_closed" {
				options.Review = nil
				options.Evidence = nil
			}
			var last QueryReceiveProgress
			err = ReceiveTestQueryReplies(ctx, source, store, publisher, options, func(p QueryReceiveProgress) error {
				last = p
				if scenario == "epoch_changed" && p.Phase == "opened" {
					return client.Set(ctx, epochKey, "changed", time.Hour).Err()
				}
				return nil
			})
			success := oneOf(scenario, "asset", "positions", "order", "fill")
			if success && (err != nil || last.State != BatchReady || last.Snapshot == nil || last.BusinessAuthorized) {
				t.Fatal(last, err)
			}
			if !success && scenario != "raw_closed" && err == nil {
				t.Fatal("failure promoted", last)
			}
			if scenario == "raw_closed" && (err != nil || last.Captured != 2 || last.Snapshot != nil || storedCount(t, db, "futures_query_batches") != 0) {
				t.Fatal("raw mode depended on readiness", last, err)
			}
			if n, err := client.XLen(ctx, cmdKey).Result(); err != nil || n != wantCommands {
				t.Fatal("receiver sent command", n, err)
			}
			if scenario == "not_created" && (storedCount(t, db, "futures_query_batches") != 0 || storedCount(t, db, "futures_stream_receivers") != 0) {
				t.Fatal("unavailable first stream consumed query attempt")
			}
			if scenario != "epoch_changed" && scenario != "not_created" {
				cp, err := store.ReplyCheckpoint(ctx, source.StreamConfig("reply"))
				if err != nil {
					t.Fatal(err)
				}
				options = TestQueryReceiveOptions{Checkpoint: cp, PageSize: 8, MaxPages: 2}
				if err = ReceiveTestQueryReplies(ctx, source, store, publisher, options, func(p QueryReceiveProgress) error { last = p; return nil }); err != nil || last.Captured != 0 {
					t.Fatal("raw continuation duplicated", last, err)
				}
			}
			if success || scenario == "partial" || scenario == "failed" || scenario == "wrong_request" || scenario == "contradiction" {
				var retired bool
				var state string
				if err = db.QueryRowContext(ctx, `SELECT retired,state FROM futures_query_batches WHERE batch_id='reviewed-query'`).Scan(&retired, &state); err != nil || !retired {
					t.Fatal("receiver not retired", err)
				}
				if scenario == "partial" && state != "interrupted" {
					t.Fatal(state)
				}
				if scenario == "failed" && state != "failed" {
					t.Fatal(state)
				}
				if oneOf(scenario, "wrong_request", "contradiction") && state != "invalid" {
					t.Fatal(state)
				}
			}
		})
	}
}
