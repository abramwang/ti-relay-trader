package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/futures"
)

func TestInspectionCLIRealTESTRedisReadOnly(t *testing.T) {
	path := os.Getenv("RELAY_FUTURES_TEST_REDIS_CONFIG")
	if path == "" {
		t.Skip("explicit TEST development configuration required")
	}
	if os.Getenv("RELAY_FUTURES_TEST_REDIS_WRITES") != "development-keys-only" {
		t.Fatal("development-key writes require opt-in")
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Service.Environment != config.EnvironmentTest || cfg.Redis.Env != "test" {
		t.Fatal("TEST configuration required")
	}
	options, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		t.Fatal("invalid TEST URL")
	}
	options.Protocol, options.MaxRetries = 2, -1
	options.DialTimeout, options.ReadTimeout, options.WriteTimeout = time.Second, time.Second, time.Second
	client := redis.NewClient(options)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	gateway := "relay-dev-" + hex.EncodeToString(nonce[:])
	route := futures.QueryRoute{Env: "test", BrokerID: "ctp", GatewayID: gateway, AccountID: "990000000001"}
	req := futures.HeartbeatInspectionRequest{Route: route, After: "0-0", Limit: 32}
	key, err := req.Stream()
	if err != nil || !strings.HasPrefix(key, "relay:test:v1:ctp:relay-dev-") {
		t.Fatal("unsafe test key")
	}
	if n, err := client.Exists(ctx, key).Result(); err != nil || n != 0 {
		t.Fatal("test key already exists or Redis unavailable")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := client.Del(cleanup, key).Err(); err != nil {
			t.Error("development key cleanup failed")
		}
	}()
	// Received OC body with an explicitly synthetic test route, never a business stream.
	raw, err := os.ReadFile("../../testdata/runtime_review/heartbeat_examples.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Examples []struct{ Body string } }
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Examples) == 0 {
		t.Fatal("missing fixture")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(doc.Examples[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	body["routing"] = route
	rewritten, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{string(rewritten), "malformed", string(rewritten)} {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: []any{"body", value}}).Err(); err != nil {
			t.Fatal("cannot seed development stream")
		}
	}
	if err := client.Expire(ctx, key, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	before, err := client.XInfoStream(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--env", "test", "--broker", "ctp", "--gateway", gateway, "--account", route.AccountID, "--run", "--limit", "2"}
	read := func(args []string) futures.HeartbeatInspection {
		t.Helper()
		var out, diagnostics bytes.Buffer
		if err := run(ctx, args, func(string) string { return cfg.Redis.URL }, &out, &diagnostics); err != nil {
			t.Fatal("CLI read failed", err)
		}
		if diagnostics.Len() != 0 || strings.Contains(out.String(), cfg.Redis.URL) {
			t.Fatal("unsafe diagnostics")
		}
		var report futures.HeartbeatInspection
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	first := read(args)
	if len(first.Records) != 2 || !first.MoreAtObservedTail || first.BusinessAuthorized || first.Records[0].Advertised.TradeDate != nil || first.Records[0].Classification != "transport_recovery_only" || first.Records[1].Classification != "invalid_heartbeat" {
		t.Fatal("unexpected first page", first)
	}
	repeat := read(args)
	if repeat.NextAfter != first.NextAfter || repeat.Records[0].BodySHA256 != first.Records[0].BodySHA256 {
		t.Fatal("read changed source")
	}
	last := read(append(args, "--after", first.NextAfter))
	if len(last.Records) != 1 || last.MoreAtObservedTail || last.BusinessAuthorized {
		t.Fatal("pagination changed")
	}
	empty := read(append(args, "--after", last.NextAfter))
	if len(empty.Records) != 0 || empty.MoreAtObservedTail {
		t.Fatal("tail is not empty")
	}
	after, err := client.XInfoStream(ctx, key).Result()
	if err != nil || after.Length != before.Length || after.EntriesAdded != before.EntriesAdded || after.Groups != before.Groups || after.Groups != 0 || after.LastGeneratedID != before.LastGeneratedID {
		t.Fatal("CLI mutated source")
	}
}
