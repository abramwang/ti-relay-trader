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
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func safeError(err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, futures.ErrStreamIdentity, futures.ErrStreamOwner, futures.ErrStreamCursor, futures.ErrStreamHistory, futures.ErrAuditCapacity} {
		if errors.Is(err, known) {
			return known
		}
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "42P01", "42703":
			return errors.New("capture schema unavailable; no automatic migrations")
		case "42501":
			return errors.New("capture database permission denied")
		}
	}
	return errors.New("capture failed; check TEST endpoints, schema and saved checkpoint before explicit retry")
}

func run(ctx context.Context, args []string, getenv func(string) string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("relay-ctp-capture", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	var route futures.QueryRoute
	var expected futures.HeartbeatCheckpoint
	var options futures.HeartbeatCaptureOptions
	flags.StringVar(&route.Env, "env", "", "must explicitly be test")
	flags.StringVar(&route.BrokerID, "broker", "", "CTP routing broker label")
	flags.StringVar(&route.GatewayID, "gateway", "", "explicit gateway")
	flags.StringVar(&route.AccountID, "account", "", "logical account; no native identity is inferred")
	deployment := flags.String("deployment-id", "", "operator-reviewed persistent TEST Redis deployment identity")
	database := flags.String("database", "", "explicit target database name ending in _test")
	flags.StringVar(&expected.Owner, "resume-owner", "", "exact previous receiver owner; never discovered for takeover")
	flags.StringVar(&expected.Cursor, "resume-cursor", "0-0", "exact previously persisted cursor, not a skip position")
	stopped := flags.Bool("confirm-stopped", false, "operator confirms previous receiver is stopped")
	flags.IntVar(&options.PageSize, "limit", 32, "entries per page, 1..256")
	flags.IntVar(&options.MaxPages, "pages", 16, "maximum read pages, 1..256; stop at first idle read")
	duration := flags.Duration("timeout", 30*time.Second, "total bounded run, at most 5m")
	runCapture := flags.Bool("run", false, "persist TEST heartbeat audit and cursor, not business facts")
	persist := flags.Bool("persist-test-audit", false, "explicitly authorize writes to the selected TEST audit database")
	status := flags.Bool("status", false, "read saved checkpoint only; no Redis reads or owner changes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid capture arguments")
	}
	req := futures.HeartbeatInspectionRequest{Route: route, After: expected.Cursor, Limit: options.PageSize}
	stream, err := req.Stream()
	if err != nil || flags.NArg() != 0 || options.MaxPages < 1 || options.MaxPages > 256 || *duration <= 0 || *duration > 5*time.Minute ||
		*deployment == "" || len(*deployment) > 128 || strings.ContainsAny(*deployment, " \t\r\n") ||
		!strings.HasSuffix(*database, "_test") || len(*database) > 63 || strings.ContainsAny(*database, " \t\r\n") ||
		(*status && *runCapture) || (*runCapture != *persist) ||
		(expected.Owner == "" && (expected.Cursor != "0-0" || *stopped)) || (expected.Owner != "" && (len(expected.Owner) != 64 || !*stopped)) {
		return errors.New("explicit TEST capture scope, database, bounds and takeover confirmation required")
	}
	if !*runCapture && !*status {
		return json.NewEncoder(output).Encode(map[string]any{"mode": "check_only_no_connections", "stream": stream, "database": *database, "business_authorized": false})
	}
	url, dsn := getenv("RELAY_CTP_CAPTURE_REDIS_URL"), getenv("RELAY_CTP_CAPTURE_DATABASE_URL")
	if url == "" || dsn == "" {
		return errors.New("dedicated RELAY_CTP_CAPTURE_REDIS_URL and RELAY_CTP_CAPTURE_DATABASE_URL required")
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil || pg.Database != *database {
		return errors.New("capture database DSN must match the explicitly selected TEST database")
	}
	pg.ConnectTimeout = 3 * time.Second
	source, err := futures.NewTestHeartbeatSource(futures.TestSourceConfig{Scope: futures.CommandScope{Route: route}, DeploymentID: *deployment, RedisURL: url})
	if err != nil {
		return errors.New("invalid TEST heartbeat source configuration")
	}
	defer source.Close()
	db := stdlib.OpenDB(*pg)
	db.SetMaxOpenConns(1)
	defer db.Close()
	bounded, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()
	var actual string
	if err := db.QueryRowContext(bounded, "SELECT current_database()").Scan(&actual); err != nil {
		return safeError(err)
	}
	if actual != *database {
		return errors.New("connected capture database differs from explicit target")
	}
	store, err := futures.NewStreamStore(db)
	if err != nil {
		return safeError(err)
	}
	encoder := json.NewEncoder(output)
	if *status {
		checkpoint, err := store.HeartbeatCheckpoint(bounded, source.StreamConfig("hb"))
		if err != nil {
			return safeError(err)
		}
		return encoder.Encode(map[string]any{"mode": "checkpoint_only", "stream": stream, "source_id": source.String(), "checkpoint": checkpoint, "business_authorized": false, "at": time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600))})
	}
	err = futures.CaptureTestHeartbeats(bounded, source, store, expected, options, func(p futures.HeartbeatCaptureProgress) error { return encoder.Encode(p) })
	if err != nil {
		return safeError(err)
	}
	return nil
}
