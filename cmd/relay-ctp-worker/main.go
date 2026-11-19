package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/futures"
)

type quietRedisLogger struct{}

func (quietRedisLogger) Printf(context.Context, string, ...interface{}) {}

func main() {
	redis.SetLogger(quietRedisLogger{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("relay-ctp-worker", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	path := flags.String("config", "", "explicit TEST capture service JSON; never uses active-config")
	execute := flags.Bool("run", false, "capture retained OC output to TEST PostgreSQL until SIGTERM; no commands")
	check := flags.Bool("check-config", false, "offline validation only (also the default)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid worker arguments")
	}
	if *path == "" || flags.NArg() != 0 || (*execute && *check) {
		return errors.New("explicit capture config required; --run and --check-config are exclusive")
	}
	f, err := os.Open(*path)
	if err != nil {
		return errors.New("capture config unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	f.Close()
	if err != nil {
		return errors.New("capture config unreadable")
	}
	c, err := futures.DecodeCaptureServiceConfig(raw)
	if err != nil {
		return errors.New("invalid TEST capture config")
	}
	encoder := json.NewEncoder(output)
	if !*execute {
		return encoder.Encode(map[string]any{"mode": "check_only_no_connections", "service_id": c.ServiceID,
			"route": c.Scope.Route, "database": c.Database, "business_authorized": false})
	}
	url, dsn := getenv("RELAY_CTP_CAPTURE_REDIS_URL"), getenv("RELAY_CTP_CAPTURE_DATABASE_URL")
	if url == "" || dsn == "" {
		return errors.New("dedicated RELAY_CTP_CAPTURE_REDIS_URL and RELAY_CTP_CAPTURE_DATABASE_URL required")
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil || pg.Database != c.Database {
		return errors.New("worker DSN must match explicitly configured TEST database")
	}
	pg.ConnectTimeout = 3 * time.Second
	sc := futures.TestSourceConfig{Scope: c.Scope, DeploymentID: c.DeploymentID, RedisURL: url}
	source, err := futures.NewTestStreamSource(sc)
	if err != nil {
		return errors.New("invalid TEST output source")
	}
	defer source.Close()
	verifier, err := futures.NewTestQueryPublisher(sc, c.Target)
	if err != nil {
		return errors.New("configured source fingerprint does not match TEST endpoint")
	}
	defer verifier.Close()
	db := stdlib.OpenDB(*pg)
	db.SetMaxOpenConns(8)
	defer db.Close()
	store, err := futures.NewStreamStore(db)
	if err != nil {
		return safeError(err)
	}
	err = futures.RunTestCaptureService(ctx, source, store, verifier, c, func(p futures.ResidentCaptureProgress) error { return encoder.Encode(p) })
	if ctx.Err() == context.Canceled && cancellationOnly(err) {
		return nil
	}
	return safeError(err)
}

// A joined checkpoint/storage failure must not disappear behind SIGTERM.
func cancellationOnly(err error) bool {
	if err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			if !cancellationOnly(e) {
				return false
			}
		}
		return len(joined.Unwrap()) > 0
	}
	return false
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{futures.ErrStreamOwner, futures.ErrStreamCursor, futures.ErrStreamIdentity,
		futures.ErrStreamHistory, futures.ErrAuditCapacity, futures.ErrLocalProjectionConflict, futures.ErrCaptureUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return errors.New("CTP capture stopped; check TEST source, schema and durable ownership; no automatic restart or commands")
}
