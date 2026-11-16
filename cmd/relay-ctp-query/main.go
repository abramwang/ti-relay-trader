// relay-ctp-query defaults to offline validation. Explicit TEST publication
// requires durable registration and a separately reviewed live target.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func main() {
	redis.SetLogger(quietRedisLogger{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type quietRedisLogger struct{}

func (quietRedisLogger) Printf(context.Context, string, ...interface{}) {}

func run(ctx context.Context, args []string, getenv func(string) string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("relay-ctp-query", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	var scope futures.CommandScope
	flags.StringVar(&scope.Route.Env, "env", "", "must explicitly be test")
	flags.StringVar(&scope.Route.BrokerID, "broker", "", "CTP routing broker label")
	flags.StringVar(&scope.Route.GatewayID, "gateway", "", "explicit gateway")
	flags.StringVar(&scope.Route.AccountID, "account", "", "logical account")
	flags.StringVar(&scope.NativeBrokerID, "native-broker", "", "independently configured native BrokerID")
	flags.StringVar(&scope.UserID, "user", "", "independently configured UserID, never inferred")
	flags.StringVar(&scope.InvestorID, "investor", "", "independently configured InvestorID, never inferred")
	file := flags.String("command-file", "", "exact immutable query command JSON; max 64 KiB")
	database := flags.String("database", "", "dedicated database name ending in _test")
	register := flags.Bool("register", false, "register intention in TEST PostgreSQL; does not send it")
	persist := flags.Bool("persist-test-intent", false, "explicit permission to persist TEST command audit")
	publish := flags.Bool("publish-query", false, "explicitly attempt one registered TEST query publication")
	allowWrite := flags.Bool("allow-test-query-write", false, "authorize only a gated TEST cmd.query append")
	targetFile := flags.String("target-file", "", "reviewed source/run_id/epoch/group/session/day/generation JSON")
	deployment := flags.String("deployment-id", "", "reviewed TEST Redis deployment identity")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid query preparation arguments")
	}
	if flags.NArg() != 0 || scope.Route.Env != "test" ||
		!(scope.Route.BrokerID == "ctp" || strings.HasPrefix(scope.Route.BrokerID, "ctp_")) ||
		*file == "" || !strings.HasSuffix(*database, "_test") || len(*database) > 63 || strings.ContainsAny(*database, " \t\r\n") || *register != *persist || *publish != *allowWrite || (*publish && (!*register || *targetFile == "" || *deployment == "")) || ((*targetFile == "") != (*deployment == "")) {
		return errors.New("explicit TEST CTP scope, dedicated database and paired registration flags required")
	}
	f, err := os.Open(*file)
	if err != nil {
		return errors.New("cannot open query command file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(raw) > 65536 {
		return errors.New("cannot read bounded query command")
	}
	command, err := futures.DecodeCommand(raw, scope, "cmd.query")
	if err != nil {
		return errors.New("invalid four-query command or explicit native scope; trading actions are forbidden")
	}
	var target futures.TestQueryTarget
	if *targetFile != "" {
		if len(*deployment) > 128 || strings.ContainsAny(*deployment, " \t\r\n") {
			return errors.New("invalid deployment identity")
		}
		tf, err := os.Open(*targetFile)
		if err != nil {
			return errors.New("cannot open reviewed query target")
		}
		b, err := io.ReadAll(io.LimitReader(tf, 4097))
		ce := tf.Close()
		if err != nil || ce != nil {
			return errors.New("cannot read reviewed query target")
		}
		target, err = futures.DecodeTestQueryTarget(b)
		if err != nil {
			return errors.New("invalid reviewed query target")
		}
	}
	hash := sha256.Sum256(raw)
	report := map[string]any{
		"mode": "check_only_no_connections", "route": scope.Route,
		"stream":   fmt.Sprintf("relay:test:v1:%s:%s:cmd.query", scope.Route.BrokerID, scope.Route.GatewayID),
		"database": *database, "action": command.Action, "message_id": command.MessageID,
		"body_sha256": hex.EncodeToString(hash[:]), "command_published": false,
		"broker_accepted": false, "business_authorized": false,
	}
	if *targetFile != "" {
		report["reviewed_target"] = target
	}
	if !*register {
		return json.NewEncoder(output).Encode(report)
	}
	dsn := getenv("RELAY_CTP_QUERY_DATABASE_URL")
	if dsn == "" {
		return errors.New("RELAY_CTP_QUERY_DATABASE_URL required; active service config is not used")
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil || pg.Database != *database {
		return errors.New("query database DSN must match explicit TEST database")
	}
	pg.ConnectTimeout = 3 * time.Second
	db := stdlib.OpenDB(*pg)
	db.SetMaxOpenConns(1)
	defer db.Close()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var actual string
	if err := db.QueryRowContext(bounded, "SELECT current_database()").Scan(&actual); err != nil || actual != *database {
		return errors.New("cannot verify dedicated query database")
	}
	store, err := futures.NewCommandStore(db)
	if err != nil {
		return errors.New("cannot open command registry")
	}
	registration, err := store.Register(bounded, scope, "cmd.query", raw, futures.RegistrationEvidence{})
	if err != nil {
		return errors.New("query registration failed; inspect TEST schema/audit before retry; no command was sent")
	}
	report["mode"], report["outcome"], report["reason"] = "test_query_intent_not_sent", registration.Outcome, registration.Reason
	report["command_id"] = registration.CommandID
	if registration.CommandID != 0 {
		report["original_message_id"] = registration.Original.MessageID
		digest := sha256.Sum256(registration.Original.Raw)
		report["original_body_sha256"] = hex.EncodeToString(digest[:])
	}
	if *publish && (registration.Outcome == "registered" || registration.Outcome == "replayed") {
		url := getenv("RELAY_CTP_QUERY_REDIS_URL")
		publisher, err := futures.NewTestQueryPublisher(futures.TestSourceConfig{Scope: scope, DeploymentID: *deployment, RedisURL: url}, target)
		if err != nil {
			return errors.New("invalid explicit TEST publication source; intention may be registered but was not sent")
		}
		defer publisher.Close()
		publication, publishErr := store.PublishTestQuery(bounded, registration.CommandID, command.MessageID, publisher)
		report["mode"] = "test_query_publication"
		report["publication"] = publication
		// An unreadable prior attempt must not be reported as definitely unsent.
		report["command_published"] = publication.Published
		if err := json.NewEncoder(output).Encode(report); err != nil {
			return err
		}
		if publishErr != nil {
			if errors.Is(publishErr, futures.ErrQueryPublishUnknown) {
				return futures.ErrQueryPublishUnknown
			}
			if errors.Is(publishErr, futures.ErrQueryPublishGate) {
				return futures.ErrQueryPublishGate
			}
			return errors.New("TEST query publication failed; review durable attempt before any further action")
		}
		return nil
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return err
	}
	if registration.Outcome != "registered" && registration.Outcome != "replayed" {
		return errors.New("query intention rejected or conflicting; no command was sent")
	}
	return nil
}
