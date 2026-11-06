package futures

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
)

// Opt-in writes are restricted to random relay-dev-* CTP keys, never the
// configured gateway. PostgreSQL still comes from the disposable test harness.
func realStream(t *testing.T) (*sql.DB, *StreamStore, StreamHandle, *redis.Client, *RedisStreamReader, context.Context) {
	t.Helper()
	return realStreamRole(t, "event")
}

func realStreamRole(t *testing.T, role string) (*sql.DB, *StreamStore, StreamHandle, *redis.Client, *RedisStreamReader, context.Context) {
	t.Helper()
	path := os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG")
	if path == "" {
		t.Skip("set RELAY_FUTURES_TEST_REDIS_CONFIG for authorized TEST Redis development-key tests")
	}
	if os.Getenv("RELAY_FUTURES_TEST_REDIS_WRITES") != "development-keys-only" {
		t.Fatal("TEST Redis development-key writes require explicit opt-in")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal("cannot load TEST Redis configuration")
	}
	if cfg.Service.Environment != config.EnvironmentTest || cfg.Redis.Env != "test" {
		t.Fatal("only an explicitly TEST environment is permitted")
	}
	options, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		t.Fatal("invalid TEST Redis configuration")
	}
	options.Protocol = 2
	options.MaxRetries = -1
	options.DialTimeout = 2 * time.Second
	options.ReadTimeout = 2 * time.Second
	options.WriteTimeout = 2 * time.Second
	options.ClientName = "relay-ctp-development-tests"
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	db, _, ctx := queryTestDB(t)
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "redis_version:") {
			t.Log(line)
		}
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	c := streamTestConfig(role)
	c.Scope.Route.Env = "test"
	c.Scope.Route.BrokerID = "ctp"
	c.Scope.Route.GatewayID = "relay-dev-" + hex.EncodeToString(nonce[:])
	c.SourceID = "authorized-test-development-fixture"
	s, h := openStream(t, db, ctx, c)
	if !strings.HasPrefix(h.stream, "relay:test:v1:ctp:relay-dev-") {
		t.Fatal("development key escaped reserved namespace")
	}
	if n, err := client.Exists(ctx, h.stream).Result(); err != nil || n != 0 {
		t.Fatal("development key already exists or cannot be checked")
	}
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: h.stream, ID: "1-0", Values: []any{"body", "synthetic seed"}}).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Del(cleanup, h.stream).Err(); err != nil {
			t.Error("development key cleanup failed", h.stream, err)
		}
	})
	if err := client.Expire(ctx, h.stream, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewRedisStreamReader(client)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("isolated development stream:", h.stream)
	return db, s, h, client, reader, ctx
}

func appendRealStream(t *testing.T, ctx context.Context, client *redis.Client, h StreamHandle, id string, fields ...any) {
	t.Helper()
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: h.stream, ID: id, Values: fields}).Err(); err != nil {
		t.Fatal(err)
	}
}

func assertRealCursor(t *testing.T, ctx context.Context, s *StreamStore, h StreamHandle, cursor string, count int) {
	t.Helper()
	status, err := s.Status(ctx, h)
	if err != nil || status.Cursor != cursor || status.Pending+status.Quarantined+status.Delivered != count {
		t.Fatal(status, err)
	}
}

func TestStreamRedisRawBytesPagingAndRestart(t *testing.T) {
	_, s, h, client, reader, ctx := realStream(t)
	raw := []byte{0xff, 0, '{', '\r', '\n'}
	appendRealStream(t, ctx, client, h, "2-0", "body", raw)
	appendRealStream(t, ctx, client, h, "3-0", "body", raw)
	if result, err := s.ReadOnce(ctx, h, reader, 1); err != nil || result.Cursor != "1-0" || len(result.ReceiptIDs) != 1 {
		t.Fatal(result, err)
	}
	// Explicit new receiver, fresh connection, same persistent checkpoint.
	newHandle, err := s.Open(ctx, h.config)
	if err != nil {
		t.Fatal(err)
	}
	secondClient := redis.NewClient(client.Options())
	defer secondClient.Close()
	secondReader, err := NewRedisStreamReader(secondClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ReadOnce(ctx, newHandle, secondReader, 2)
	if err != nil || result.Cursor != "3-0" || len(result.ReceiptIDs) != 2 {
		t.Fatal(result, err)
	}
	for _, id := range result.ReceiptIDs {
		record, err := s.Record(ctx, newHandle, id)
		if err != nil || !bytes.Equal(record.Raw, raw) {
			t.Fatal("binary body changed", err)
		}
	}
	if result, err := s.ReadOnce(ctx, newHandle, secondReader, 2); err != nil || result.Cursor != "3-0" || len(result.ReceiptIDs) != 0 {
		t.Fatal(result, err)
	}
	if _, err := s.ReadOnce(ctx, h, reader, 2); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("retired receiver was not fenced", err)
	}
	if groups, err := client.XInfoGroups(ctx, h.stream).Result(); err != nil || len(groups) != 0 {
		t.Fatal("reader created a consumer group", err)
	}
	assertRealCursor(t, ctx, s, newHandle, "3-0", 3)
}

func TestStreamRedisRejectsDuplicateBodyBeforeCheckpoint(t *testing.T) {
	for _, second := range []string{"first", "changed"} {
		t.Run(second, func(t *testing.T) {
			_, s, h, client, reader, ctx := realStream(t)
			appendRealStream(t, ctx, client, h, "2-0", "body", "first", "body", second)
			// Reproduce the loss in the normal go-redis Stream map decoder.
			typed, err := client.XRead(ctx, &redis.XReadArgs{Streams: []string{h.stream, "1-0"}, Count: 1, Block: -1}).Result()
			if err != nil || len(typed) != 1 || len(typed[0].Messages) != 1 || len(typed[0].Messages[0].Values) != 1 || typed[0].Messages[0].Values["body"] != second {
				t.Fatal("unexpected typed decoder behavior", err)
			}
			if _, err = s.ReadOnce(ctx, h, reader, 10); err == nil {
				t.Fatal("duplicate body silently overwritten")
			}
			assertRealCursor(t, ctx, s, h, "0-0", 0)
			if n, err := client.XLen(ctx, h.stream).Result(); err != nil || n != 2 {
				t.Fatal("bad source entry removed", err)
			}
		})
	}
}

func TestStreamRedisRejectsUnreadHistoryLoss(t *testing.T) {
	for _, operation := range []string{"trim", "delete", "missing", "rollback", "wrong_type"} {
		t.Run(operation, func(t *testing.T) {
			_, s, h, client, reader, ctx := realStream(t)
			appendRealStream(t, ctx, client, h, "2-0", "body", "unread")
			appendRealStream(t, ctx, client, h, "3-0", "body", "latest")
			if _, err := s.ReadOnce(ctx, h, reader, 1); err != nil {
				t.Fatal(err)
			}
			var err error
			switch operation {
			case "trim":
				err = client.XTrimMaxLen(ctx, h.stream, 1).Err()
			case "delete":
				err = client.XDel(ctx, h.stream, "2-0").Err()
			case "missing", "rollback", "wrong_type":
				err = client.Del(ctx, h.stream).Err()
				if err == nil && operation == "rollback" {
					appendRealStream(t, ctx, client, h, "0-1", "body", "rolled back")
					err = client.Expire(ctx, h.stream, time.Hour).Err()
				}
				if err == nil && operation == "wrong_type" {
					err = client.Set(ctx, h.stream, "not a stream", time.Hour).Err()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReadOnce(ctx, h, reader, 10); err == nil {
				t.Fatal("lost history accepted", operation)
			}
			assertRealCursor(t, ctx, s, h, "1-0", 1)
		})
	}
}

func TestStreamRedisAllowsTrimmingAlreadyCapturedHistory(t *testing.T) {
	_, s, h, client, reader, ctx := realStream(t)
	appendRealStream(t, ctx, client, h, "2-0", "body", "captured")
	appendRealStream(t, ctx, client, h, "3-0", "body", "next")
	if _, err := s.ReadOnce(ctx, h, reader, 2); err != nil {
		t.Fatal(err)
	}
	if err := client.XTrimMaxLen(ctx, h.stream, 2).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadOnce(ctx, h, reader, 10); err != nil {
		t.Fatal("captured history trim blocked", err)
	}
	assertRealCursor(t, ctx, s, h, "3-0", 3)
}

type interruptedRealReader struct {
	*RedisStreamReader
	afterRead func()
}

func (r interruptedRealReader) ReadStream(ctx context.Context, key, cursor string, limit int) (any, error) {
	result, err := r.RedisStreamReader.ReadStream(ctx, key, cursor, limit)
	if err == nil {
		r.afterRead()
	}
	return result, err
}

func TestStreamRedisLossAndTakeoverDuringRead(t *testing.T) {
	for _, action := range []string{"delete", "takeover"} {
		t.Run(action, func(t *testing.T) {
			_, s, h, client, reader, ctx := realStream(t)
			next := h
			interrupted := interruptedRealReader{reader, func() {
				var err error
				if action == "delete" {
					err = client.XDel(ctx, h.stream, "1-0").Err()
				} else {
					next, err = s.Open(ctx, h.config)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			if _, err := s.ReadOnce(ctx, h, interrupted, 10); err == nil {
				t.Fatal("in-flight change did not block commit")
			}
			assertRealCursor(t, ctx, s, next, "0-0", 0)
		})
	}
}

func TestStreamRedisPGFailureRereadsWithoutDuplicate(t *testing.T) {
	db, s, h, _, reader, ctx := realStream(t)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_cursor() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit failure'; END $$; CREATE TRIGGER fail_cursor BEFORE UPDATE OF cursor_id ON futures_stream_receivers FOR EACH ROW EXECUTE FUNCTION fail_cursor()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadOnce(ctx, h, reader, 10); err == nil {
		t.Fatal("commit failure ignored")
	}
	assertRealCursor(t, ctx, s, h, "0-0", 0)
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_cursor ON futures_stream_receivers`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.ReadOnce(ctx, h, reader, 10); err != nil {
			t.Fatal(err)
		}
	}
	assertRealCursor(t, ctx, s, h, "1-0", 1)
}

func TestStreamRedisClientDisconnectAndReconnect(t *testing.T) {
	_, s, h, client, _, ctx := realStream(t)
	disconnected := redis.NewClient(client.Options())
	reader, err := NewRedisStreamReader(disconnected)
	if err != nil {
		t.Fatal(err)
	}
	if err = disconnected.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	_ = disconnected.Close()
	if _, err = s.ReadOnce(ctx, h, reader, 10); err == nil {
		t.Fatal("closed client reported success")
	}
	assertRealCursor(t, ctx, s, h, "0-0", 0)
	reconnected := redis.NewClient(client.Options())
	defer reconnected.Close()
	reader, err = NewRedisStreamReader(reconnected)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := interruptedRealReader{reader, func() { _ = reconnected.Close() }}
	if _, err = s.ReadOnce(ctx, h, interrupted, 10); err == nil {
		t.Fatal("connection closed after XREAD still advanced the checkpoint")
	}
	assertRealCursor(t, ctx, s, h, "0-0", 0)
	reader, err = NewRedisStreamReader(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadOnce(ctx, h, reader, 10); err != nil {
		t.Fatal(err)
	}
	assertRealCursor(t, ctx, s, h, "1-0", 1)
}

func TestStreamRedisQueryPagesHandoffAndRecovery(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "complete"
		if interrupted {
			name = "interrupted"
		}
		t.Run(name, func(t *testing.T) {
			db, s, h, client, reader, ctx := realStreamRole(t, "reply")
			q, err := NewQueryStore(db)
			if err != nil {
				t.Fatal(err)
			}
			c := batchContext("positions")
			c.Route = h.config.Scope.Route
			r, c := registeredQueryCommand(t, db, ctx, c)
			bindQuery(t, q, ctx, "real-pages", r, c)
			first := boundReply(t, page(t, "positions", "first", 0, false, "FIRST"), r)
			last := boundReply(t, page(t, "positions", "last", 1, true, "LAST"), r)
			// Only synthetic fixture routing changes; OC-delivered files stay untouched.
			route := func(raw []byte) []byte {
				return editBody(t, raw, func(m map[string]any) {
					m["routing"].(map[string]any)["broker_id"] = c.Route.BrokerID
					m["routing"].(map[string]any)["gateway_id"] = c.Route.GatewayID
				})
			}
			first, last = route(first), route(last)
			appendRealStream(t, ctx, client, h, "2-0", "body", first)
			appendRealStream(t, ctx, client, h, "3-0", "body", last)
			in, err := s.ReadOnce(ctx, h, reader, 3)
			if err != nil || len(in.ReceiptIDs) != 3 {
				t.Fatal(in, err)
			}
			if err := s.Quarantine(ctx, h, in.ReceiptIDs[0], "synthetic seed, not a business reply"); err != nil {
				t.Fatal(err)
			}
			if out, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[1], q, "real-pages", 0); err != nil || out.State != BatchCollecting {
				t.Fatal(out, err)
			}
			h, err = s.Open(ctx, h.config)
			if err != nil {
				t.Fatal(err)
			}
			if interrupted {
				next, _ := NewQueryStore(db)
				if state, err := next.Recover(ctx, "real-pages"); err != nil || state != BatchInterrupted {
					t.Fatal(state, err)
				}
				if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[2], next, "real-pages", time.Second); !errors.Is(err, ErrBatchOwner) {
					t.Fatal("old partial query revived", err)
				}
				if storedCount(t, db, "futures_query_snapshots") != 0 {
					t.Fatal("incomplete query published")
				}
			} else {
				if out, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[2], q, "real-pages", time.Second); err != nil || !out.Published || out.State != BatchReady {
					t.Fatal(out, err)
				}
				if out, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[2], q, "real-pages", time.Second); err != nil || !out.Replayed {
					t.Fatal(out, err)
				}
				if storedCount(t, db, "futures_query_observations") != 2 || storedCount(t, db, "futures_query_snapshots") != 1 {
					t.Fatal("query pages lost or repeated")
				}
			}
			record, err := s.Record(ctx, h, in.ReceiptIDs[2])
			if err != nil || !bytes.Equal(record.Raw, last) {
				t.Fatal("raw final page changed", err)
			}
			assertRealCursor(t, ctx, s, h, "3-0", 3)
		})
	}
}

func TestStreamRedisDispatchRegisteredQueryOnly(t *testing.T) {
	db, s, h, client, reader, ctx := realStreamRole(t, "reply")
	q, _ := NewQueryStore(db)
	c := batchContext("positions")
	c.Route = h.config.Scope.Route
	r, c := registeredQueryCommand(t, db, ctx, c)
	bindQuery(t, q, ctx, "dispatched", r, c)
	raw := editBody(t, boundReply(t, fixtureBody(t, "messages.json", "positions"), r), func(m map[string]any) {
		m["routing"].(map[string]any)["broker_id"] = c.Route.BrokerID
		m["routing"].(map[string]any)["gateway_id"] = c.Route.GatewayID
	})
	foreign := editBody(t, raw, func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" })
	appendRealStream(t, ctx, client, h, "2-0", "body", foreign)
	appendRealStream(t, ctx, client, h, "3-0", "body", raw)
	if _, err := s.ReadOnce(ctx, h, reader, 10); err != nil {
		t.Fatal(err)
	}
	d, err := NewStreamDispatcher(ctx, s, h, q, nil, []QueryDispatchBinding{{"dispatched", time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Dispatch(ctx, 0, 10)
	if err != nil || page.Scanned != 3 || page.Delivered != 1 || len(page.Deferred) != 2 || !page.CycleComplete {
		t.Fatal(page, err)
	}
	if storedCount(t, db, "futures_query_observations") != 1 || storedCount(t, db, "futures_query_snapshots") != 1 {
		t.Fatal("foreign source entered query evidence")
	}
	status, err := s.Status(ctx, h)
	if err != nil || status.Pending != 2 || status.Delivered != 1 {
		t.Fatal(status, err)
	}
}
