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
	follow := f.Bool("follow", false, "keep capture running across idle periods; raw-only unless project-local-events is explicit")
	project := f.Bool("project-local-events", false, "both/follow only: explicitly apply confirmed local events to the TEST ledger; no external/query projection or trading")
	maxRecords := f.Int("max-records", 50000, "follow-only record budget 1..50000; existing durable capacity also applies")
	receive := f.Bool("receive-reviewed", false, "capture and apply independently reviewed query replies")
	persist := f.Bool("persist-test-replies", false, "explicit permission to write dedicated TEST audit")
	role := f.String("stream-role", "reply", "reply, event, or both; both requires follow or status and defaults to raw-only")
	checkpointsFile := f.String("checkpoints-file", "", "both-only explicit reply/event previous checkpoints JSON, max 4 KiB")
	persistEvents := f.Bool("persist-test-events", false, "explicit permission to persist TEST event originals")
	status := f.Bool("status", false, "read checkpoint only; no takeover or Redis connection")
	owner := f.String("previous-owner", "", "explicit previous stopped receiver owner")
	confirmStopped := f.Bool("confirm-stopped", false, "confirm previous receiver was stopped before takeover")
	cursor := f.String("cursor", "0-0", "explicit previous durable cursor")
	duration := f.Duration("duration", 30*time.Second, "receive window: at most 5m, or 24h with --follow")
	pageSize := f.Int("page-size", 32, "raw/dispatch page size 1..256")
	maxPages := f.Int("max-pages", 256, "page budget 1..256")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid reply receiver arguments")
	}
	seen := map[string]bool{}
	f.Visit(func(v *flag.Flag) { seen[v.Name] = true })
	paired := *role == "both"
	if *project && (!paired || !*follow || *status || *receive) {
		return errors.New("local event projection requires paired follow capture, never status or query review")
	}
	var checkpoints futures.ResidentCheckpoints
	if paired {
		if (!*follow && !*status) || *receive || *reviewFile != "" || *evidenceFile != "" || seen["previous-owner"] || seen["cursor"] ||
			(*status && (*checkpointsFile != "" || *confirmStopped)) || (!*status && *checkpointsFile == "") || *persist != *persistEvents {
			return errors.New("paired capture requires explicit checkpoints, both write permissions, and no query review or single-stream checkpoint flags")
		}
		if !*status {
			raw, err := boundedFile(*checkpointsFile, 4096)
			if err != nil {
				return err
			}
			checkpoints, err = futures.DecodeResidentCheckpoints(raw)
			if err != nil || ((checkpoints.Reply.Owner != "" || checkpoints.Event.Owner != "") != *confirmStopped) {
				return errors.New("invalid paired checkpoints or missing confirmation that previous receivers stopped")
			}
		}
	} else if *checkpointsFile != "" {
		return errors.New("checkpoints-file is only valid for paired capture")
	}
	maxDuration := 5 * time.Minute
	if *follow {
		maxDuration = 24 * time.Hour
	}
	if (*follow && (*receive || *status || *reviewFile != "" || *evidenceFile != "" || seen["max-pages"])) ||
		(!*follow && seen["max-records"]) || *maxRecords < 1 || *maxRecords > 50000 {
		return errors.New("follow uses max-records instead of max-pages and forbids status/query-review modes")
	}
	writeAllowed := *persist
	if *role == "event" {
		writeAllowed = *persistEvents
	}
	if (*role != "reply" && *role != "event" && !paired) || (*role == "event" && (*persist || *receive || *reviewFile != "" || *evidenceFile != "")) || (*role == "reply" && *persistEvents) {
		return errors.New("event capture requires its own permission and forbids query reviews")
	}
	if f.NArg() != 0 || scope.Route.Env != "test" || !(scope.Route.BrokerID == "ctp" || strings.HasPrefix(scope.Route.BrokerID, "ctp_")) ||
		*targetFile == "" || *deployment == "" || !strings.HasSuffix(*database, "_test") || len(*database) > 63 || strings.ContainsAny(*database, " \r\n\t") ||
		(*capture && *receive) || (writeAllowed != (*capture || *receive)) || (*status && (*capture || *receive)) ||
		((*reviewFile == "") != (*evidenceFile == "")) || (*receive && *reviewFile == "") || (*capture && *reviewFile != "") ||
		(!paired && ((*owner != "") != *confirmStopped)) || (*owner == "" && *cursor != "0-0") ||
		*duration <= 0 || *duration > maxDuration || *pageSize < 1 || *pageSize > 256 || *maxPages < 1 || *maxPages > 256 {
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
		return json.NewEncoder(out).Encode(map[string]any{"mode": "check_only_no_connections", "route": scope.Route, "database": *database, "stream_role": *role, "follow": *follow, "project_local_events": *project, "reviewed_request": len(review) > 0, "business_authorized": false})
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
		if paired {
			checkpoints.Reply, err = store.ReplyCheckpoint(bounded, source.StreamConfig("reply"))
			if err == nil {
				checkpoints.Event, err = store.EventCheckpoint(bounded, source.StreamConfig("event"))
			}
			if err != nil {
				return errors.New("cannot inspect TEST reply/event checkpoints")
			}
			return json.NewEncoder(out).Encode(map[string]any{"mode": "checkpoint_only", "checkpoints": checkpoints, "source_id": source.String(), "business_authorized": false})
		}
		var cp futures.HeartbeatCheckpoint
		if *role == "event" {
			cp, err = store.EventCheckpoint(bounded, source.StreamConfig(*role))
		} else {
			cp, err = store.ReplyCheckpoint(bounded, source.StreamConfig(*role))
		}
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
	if *follow {
		opts := futures.DefaultResidentCaptureOptions()
		opts.PageSize, opts.MaxRecords = *pageSize, *maxRecords
		emit := func(p futures.ResidentCaptureProgress) error { return json.NewEncoder(out).Encode(p) }
		if paired {
			if *project {
				err = futures.FollowTestLocalEventPair(bounded, source, store, publisher, opts, checkpoints, emit)
			} else {
				err = futures.FollowTestRawPair(bounded, source, store, publisher, opts, checkpoints, emit)
			}
		} else {
			opts.Role = *role
			opts.Checkpoint = futures.HeartbeatCheckpoint{Owner: *owner, Cursor: *cursor}
			err = futures.FollowTestRawStream(bounded, source, store, publisher, opts, emit)
		}
		// A clean bounded stop is not a business success. Joined checkpoint or
		// output errors must still return nonzero even alongside cancellation.
		if err == context.Canceled || err == context.DeadlineExceeded {
			return nil
		}
	} else {
		err = futures.ReceiveTestQueryReplies(bounded, source, store, publisher, futures.TestQueryReceiveOptions{Role: *role, Checkpoint: futures.HeartbeatCheckpoint{Owner: *owner, Cursor: *cursor}, PageSize: *pageSize, MaxPages: *maxPages, Review: review, Evidence: evidence}, func(p futures.QueryReceiveProgress) error { return json.NewEncoder(out).Encode(p) })
	}
	if err != nil {
		for _, known := range []error{futures.ErrStreamOwner, futures.ErrStreamCursor, futures.ErrStreamIdentity, futures.ErrStreamHistory, futures.ErrResidentCaptureBudget, futures.ErrLocalProjectionConflict, futures.ErrTradeCheckpoint, futures.ErrBatchIdentity, futures.ErrQueryPublishGate, context.Canceled, context.DeadlineExceeded} {
			if errors.Is(err, known) {
				return fmt.Errorf("TEST reply receive stopped: %w; no command sent", known)
			}
		}
		return errors.New("TEST reply receive stopped; retain raw evidence and inspect checkpoint/query state before further action; no command sent")
	}
	return nil
}
