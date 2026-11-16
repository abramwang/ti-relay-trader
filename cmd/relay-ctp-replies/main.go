// relay-ctp-replies is an explicit TEST-only reply receiver, never a sender.
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
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/futures"
)

type quietLogger struct{}

func (quietLogger) Printf(context.Context, string, ...interface{}) {}

func main() {
	redis.SetLogger(quietLogger{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func boundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open input evidence file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	ce := f.Close()
	if err != nil || ce != nil || int64(len(b)) > limit {
		return nil, errors.New("input evidence exceeds limit or cannot be read")
	}
	return b, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, out, diagnostics io.Writer) error {
	f := flag.NewFlagSet("relay-ctp-replies", flag.ContinueOnError)
	f.SetOutput(diagnostics)
	var scope futures.CommandScope
	f.StringVar(&scope.Route.Env, "env", "", "must explicitly be test")
	f.StringVar(&scope.Route.BrokerID, "broker", "", "CTP routing broker label")
	f.StringVar(&scope.Route.GatewayID, "gateway", "", "explicit gateway")
	f.StringVar(&scope.Route.AccountID, "account", "", "logical account")
	f.StringVar(&scope.NativeBrokerID, "native-broker", "", "configured native BrokerID")
	f.StringVar(&scope.UserID, "user", "", "configured native UserID")
	f.StringVar(&scope.InvestorID, "investor", "", "configured native InvestorID")
	database := f.String("database", "", "dedicated database ending in _test")
	deployment := f.String("deployment-id", "", "reviewed Redis deployment label")
	targetFile := f.String("target-file", "", "reviewed target JSON, max 4 KiB")
	reviewFile := f.String("review-file", "", "independent request review, max 16 KiB")
	evidenceFile := f.String("evidence-file", "", "original OC request evidence, max 256 KiB; no credentials")
	capture := f.Bool("capture", false, "capture TEST reply originals only")
	receive := f.Bool("receive-reviewed", false, "capture and apply independently reviewed query replies")
	persist := f.Bool("persist-test-replies", false, "explicit permission to write dedicated TEST audit")
	status := f.Bool("status", false, "read checkpoint only; no takeover or Redis connection")
	owner := f.String("previous-owner", "", "explicit previous stopped receiver owner")
	confirmStopped := f.Bool("confirm-stopped", false, "confirm previous receiver was stopped before takeover")
	cursor := f.String("cursor", "0-0", "explicit previous durable cursor")
	duration := f.Duration("duration", 30*time.Second, "bounded receive window, at most 5m")
	pageSize := f.Int("page-size", 32, "raw/dispatch page size 1..256")
	maxPages := f.Int("max-pages", 256, "page budget 1..256")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid reply receiver arguments")
	}
	if f.NArg() != 0 || scope.Route.Env != "test" || !(scope.Route.BrokerID == "ctp" || strings.HasPrefix(scope.Route.BrokerID, "ctp_")) ||
		*targetFile == "" || *deployment == "" || !strings.HasSuffix(*database, "_test") || len(*database) > 63 || strings.ContainsAny(*database, " \r\n\t") ||
		(*capture && *receive) || (*persist != (*capture || *receive)) || (*status && (*capture || *receive)) ||
		((*reviewFile == "") != (*evidenceFile == "")) || (*receive && *reviewFile == "") || (*capture && *reviewFile != "") ||
		((*owner != "") != *confirmStopped) || (*owner == "" && *cursor != "0-0") ||
		*duration <= 0 || *duration > 5*time.Minute || *pageSize < 1 || *pageSize > 256 || *maxPages < 1 || *maxPages > 256 {
		return errors.New("explicit TEST scope, paired mode/permission and bounded options required")
	}
	raw, err := boundedFile(*targetFile, 4096)
	if err != nil {
		return err
	}
	target, err := futures.DecodeTestQueryTarget(raw)
	if err != nil {
		return errors.New("invalid reviewed target")
	}
	var review, evidence []byte
	if *reviewFile != "" {
		review, err = boundedFile(*reviewFile, 16384)
		if err != nil {
			return err
		}
		evidence, err = boundedFile(*evidenceFile, 262144)
		if err != nil {
			return err
		}
		r, err := futures.DecodeQueryRequestReview(review, evidence)
		if err != nil || r.Target != target || r.Query.Route != scope.Route || r.Query.NativeBrokerID != scope.NativeBrokerID || r.Query.NativeUserID != scope.UserID || r.Query.NativeInvestorID != scope.InvestorID {
			return errors.New("request evidence does not match explicit target/native scope")
		}
	}
	// Validate the full native scope offline with the existing source constructor.
	check, err := futures.NewTestStreamSource(futures.TestSourceConfig{Scope: scope, DeploymentID: *deployment, RedisURL: "redis://127.0.0.1:1/0"})
	if err != nil {
		return errors.New("invalid explicit TEST/native identity")
	}
	check.Close()
	if !*status && !*capture && !*receive {
		return json.NewEncoder(out).Encode(map[string]any{"mode": "check_only_no_connections", "route": scope.Route, "database": *database, "reviewed_request": len(review) > 0, "business_authorized": false})
	}
	dsn, url := getenv("RELAY_CTP_REPLY_DATABASE_URL"), getenv("RELAY_CTP_REPLY_REDIS_URL")
	pg, err := pgx.ParseConfig(dsn)
	if dsn == "" || err != nil || pg.Database != *database {
		return errors.New("explicit matching TEST database URL required")
	}
	source, err := futures.NewTestStreamSource(futures.TestSourceConfig{Scope: scope, DeploymentID: *deployment, RedisURL: url})
	if err != nil || source.String() != target.SourceID {
		if source != nil {
			source.Close()
		}
		return errors.New("explicit TEST Redis source fingerprint differs")
	}
	defer source.Close()
	pg.ConnectTimeout = 3 * time.Second
	db := stdlib.OpenDB(*pg)
	defer db.Close()
	db.SetMaxOpenConns(2)
	bounded, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()
	var actual string
	if err = db.QueryRowContext(bounded, "SELECT current_database()").Scan(&actual); err != nil || actual != *database {
		return errors.New("cannot verify dedicated TEST database")
	}
	store, _ := futures.NewStreamStore(db)
	if *status {
		cp, err := store.ReplyCheckpoint(bounded, source.StreamConfig("reply"))
		if err != nil {
			return errors.New("cannot inspect TEST reply checkpoint")
		}
		return json.NewEncoder(out).Encode(map[string]any{"mode": "checkpoint_only", "checkpoint": cp, "source_id": source.String(), "business_authorized": false})
	}
	publisher, err := futures.NewTestQueryPublisher(futures.TestSourceConfig{Scope: scope, DeploymentID: *deployment, RedisURL: url}, target)
	if err != nil {
		return errors.New("cannot create TEST source verifier")
	}
	defer publisher.Close()
	err = futures.ReceiveTestQueryReplies(bounded, source, store, publisher, futures.TestQueryReceiveOptions{Checkpoint: futures.HeartbeatCheckpoint{Owner: *owner, Cursor: *cursor}, PageSize: *pageSize, MaxPages: *maxPages, Review: review, Evidence: evidence}, func(p futures.QueryReceiveProgress) error { return json.NewEncoder(out).Encode(p) })
	if err != nil {
		for _, known := range []error{futures.ErrStreamOwner, futures.ErrStreamCursor, futures.ErrStreamIdentity, futures.ErrBatchIdentity, futures.ErrQueryPublishGate, context.Canceled, context.DeadlineExceeded} {
			if errors.Is(err, known) {
				return fmt.Errorf("TEST reply receive stopped: %w; no command sent", known)
			}
		}
		return errors.New("TEST reply receive stopped; retain raw evidence and inspect checkpoint/query state before further action; no command sent")
	}
	return nil
}
