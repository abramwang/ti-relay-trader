package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestQueryRegistrationDatabaseReplayAndConflict(t *testing.T) {
	dsn := os.Getenv("RELAY_CTP_QUERY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RELAY_CTP_QUERY_TEST_DATABASE_URL to a disposable dedicated _test database")
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(pg.Database, "_test") {
		t.Fatal("explicit disposable TEST database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin := stdlib.OpenDB(*pg)
	defer admin.Close()
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "query_cli_" + hex.EncodeToString(nonce[:])
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("cannot create disposable schema")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("schema cleanup failed")
		}
	}()
	pg.RuntimeParams["search_path"] = schema
	scopedDSN := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("invalid test URL")
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		scopedDSN = u.String()
	}
	db := stdlib.OpenDB(*pg)
	defer db.Close()
	migration, err := os.ReadFile("../../migrations/postgres/000037_futures_command_registry.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal("test migration failed")
	}
	getenv := func(key string) string {
		if key != "RELAY_CTP_QUERY_DATABASE_URL" {
			t.Fatal("unexpected credential access", key)
		}
		return scopedDSN
	}
	args := append(queryArgs(t, "order.list.query"), "--database", pg.Database, "--register", "--persist-test-intent")
	var originalID any
	for i, want := range []string{"registered", "replayed", "replayed"} {
		var out bytes.Buffer
		if err := run(ctx, args, getenv, &out, &out); err != nil {
			t.Fatal(err)
		}
		var receipt map[string]any
		if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt["outcome"] != want || receipt["command_published"] != false || receipt["broker_accepted"] != false {
			t.Fatal("registration incorrectly promoted", out.String(), err)
		}
		if i == 0 {
			originalID = receipt["command_id"]
		} else if receipt["command_id"] != originalID {
			t.Fatal("duplicate durable intention")
		}
	}
	var conflict bytes.Buffer
	args = append(queryArgs(t, "fill.list.query"), "--database", pg.Database, "--register", "--persist-test-intent")
	if err := run(ctx, args, getenv, &conflict, &conflict); err == nil || !strings.Contains(conflict.String(), `"outcome":"conflict"`) {
		t.Fatal("changed intent did not conflict", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM futures_commands").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate commands", count, err)
	}
	var saved []byte
	if err := db.QueryRowContext(ctx, "SELECT body FROM futures_commands").Scan(&saved); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(saved, &body); err != nil || body["action"] != "order.list.query" {
		t.Fatal("original body overwritten", err)
	}
}
