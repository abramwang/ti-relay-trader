// relay-ctp-trade is an explicit TEST-only single-order assessment publisher.
// Default execution is offline. It does not log in or confirm settlement.
package main

import (
	"bytes"
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

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open preparation file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	ce := f.Close()
	if err != nil || ce != nil || int64(len(b)) > limit {
		return nil, errors.New("cannot read bounded preparation file")
	}
	return b, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, out, diagnostics io.Writer) error {
	f := flag.NewFlagSet("relay-ctp-trade", flag.ContinueOnError)
	f.SetOutput(diagnostics)
	var scope futures.CommandScope
	f.StringVar(&scope.Route.Env, "env", "", "explicit test only")
	f.StringVar(&scope.Route.BrokerID, "broker", "", "CTP routing broker")
	f.StringVar(&scope.Route.GatewayID, "gateway", "", "existing reviewed gateway")
	f.StringVar(&scope.Route.AccountID, "account", "", "logical account")
	f.StringVar(&scope.NativeBrokerID, "native-broker", "", "native BrokerID")
	f.StringVar(&scope.UserID, "user", "", "native UserID")
	f.StringVar(&scope.InvestorID, "investor", "", "native InvestorID")
	commandFile := f.String("command-file", "", "exact original command JSON")
	targetFile := f.String("target-file", "", "reviewed source/run/epoch/group/session/day/generation JSON")
	evidenceFile := f.String("evidence-file", "", "RegistrationEvidence JSON with verified mapping or cancel target")
	approvalFile := f.String("approval-file", "", "short-lived exact-body TEST approval; relay_test_execution.v1 requires explicit submit/cancel permissions")
	database := f.String("database", "", "dedicated database ending in _test")
	deployment := f.String("deployment-id", "", "TEST Redis deployment label")
	register := f.Bool("register", false, "persist TEST intention only")
	persist := f.Bool("persist-test-intent", false, "explicit TEST database write permission")
	publish := f.Bool("publish-trade", false, "one gated cmd.trade append; never a retry loop")
	allow := f.Bool("allow-test-trade-write", false, "explicit TEST assessment publication permission")
	preparePrice := f.Bool("prepare-native-price", false, "read durable TEST facts and suggest a limit; no Redis, registration or permission")
	nativeQuery := f.Int64("native-query-id", 0, "registered and published instrument.constraints.query command ID")
	nativeReceipt := f.Int64("native-receipt-id", 0, "durable raw reply receipt ID")
	nativeMapping := f.String("native-mapping-file", "", "verified Meridian CatalogBinding JSON; identities only, not a quote")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid trade preparation arguments")
	}
	if f.NArg() != 0 || scope.Route.Env != "test" || !(scope.Route.BrokerID == "ctp" || strings.HasPrefix(scope.Route.BrokerID, "ctp_")) || !strings.HasSuffix(*database, "_test") || len(*database) > 63 || strings.ContainsAny(*database, " \t\r\n") || *register != *persist || *publish != *allow || (*publish && (!*register || *deployment == "")) || len(*deployment) > 128 || strings.ContainsAny(*deployment, " \t\r\n") {
		return errors.New("explicit TEST CTP scope, dedicated database and paired write flags required")
	}
	if *preparePrice {
		if *register || *persist || *publish || *allow || *commandFile != "" || *approvalFile != "" || *evidenceFile != "" || *nativeQuery <= 0 || *nativeReceipt <= 0 || *nativeMapping == "" {
			return errors.New("native price preparation is read-only and requires query/receipt IDs and mapping only")
		}
	} else if *nativeQuery != 0 || *nativeReceipt != 0 || *nativeMapping != "" {
		return errors.New("native price flags require --prepare-native-price")
	}
	b, err := readBounded(*targetFile, 4096)
	if err != nil {
		return err
	}
	target, err := futures.DecodeTestQueryTarget(b)
	if err != nil {
		return errors.New("invalid reviewed TEST target")
	}
	if *preparePrice {
		return prepareNativePrice(ctx, scope, target, *database, *nativeQuery, *nativeReceipt, *nativeMapping, getenv, out)
	}
	raw, err := readBounded(*commandFile, 65536)
	if err != nil {
		return err
	}
	b, err = readBounded(*approvalFile, 8192)
	if err != nil {
		return err
	}
	approval, err := futures.DecodeTestTradeApproval(b)
	if err != nil {
		return errors.New("invalid TEST trade approval")
	}
	b, err = readBounded(*evidenceFile, 65536)
	if err != nil {
		return err
	}
	var evidence futures.RegistrationEvidence
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&evidence); err != nil {
		return errors.New("invalid registration evidence")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing registration evidence")
	}
	command, err := futures.ValidateTestTradePreparation(raw, scope, target, evidence, approval)
	if err != nil {
		return errors.New("invalid single TEST assessment command, mapping, quote or approval")
	}
	report := map[string]any{"mode": "check_only_no_connections", "action": command.Action, "message_id": command.MessageID, "body_sha256": approval.CommandSHA256, "assessment_id": approval.AssessmentID, "command_published": false, "broker_accepted": false, "business_authorized": false, "database": *database, "route": scope.Route}
	report["permission_profile"] = approval.PermissionProfile
	report["execution_permission"] = approval.ExecutionPermission
	if !*register {
		return json.NewEncoder(out).Encode(report)
	}
	pg, err := pgx.ParseConfig(getenv("RELAY_CTP_TRADE_DATABASE_URL"))
	if getenv("RELAY_CTP_TRADE_DATABASE_URL") == "" || err != nil || pg.Database != *database {
		return errors.New("explicit matching RELAY_CTP_TRADE_DATABASE_URL required")
	}
	pg.ConnectTimeout = 3 * time.Second
	db := stdlib.OpenDB(*pg)
	db.SetMaxOpenConns(2)
	defer db.Close()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var actual string
	if err := db.QueryRowContext(bounded, "SELECT current_database()").Scan(&actual); err != nil || actual != *database {
		return errors.New("cannot verify dedicated TEST database")
	}
	store, err := futures.NewCommandStore(db)
	if err != nil {
		return errors.New("cannot open command registry")
	}
	r, err := store.Register(bounded, scope, "cmd.trade", raw, evidence)
	if err != nil {
		return errors.New("TEST registration failed; no command sent")
	}
	report["mode"], report["outcome"], report["reason"], report["command_id"] = "test_trade_intent_not_sent", r.Outcome, r.Reason, r.CommandID
	if r.Outcome != "registered" && r.Outcome != "replayed" {
		json.NewEncoder(out).Encode(report)
		return errors.New("trade registration rejected or conflicted")
	}
	if !*publish {
		return json.NewEncoder(out).Encode(report)
	}
	url := getenv("RELAY_CTP_TRADE_REDIS_URL")
	if url == "" {
		return errors.New("explicit RELAY_CTP_TRADE_REDIS_URL required; no command sent")
	}
	p, err := futures.NewTestTradePublisher(futures.TestSourceConfig{Scope: scope, DeploymentID: *deployment, RedisURL: url}, target, approval)
	if err != nil {
		return errors.New("TEST source binding rejected; no command sent")
	}
	defer p.Close()
	publication, publishErr := store.PublishTestTrade(bounded, r.CommandID, command.MessageID, p)
	report["mode"], report["publication"] = "test_trade_publication", publication
	// nil includes preflight/storage errors: this CLI never infers safe retry.
	report["command_published"] = publication.Published
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return err
	}
	if publishErr != nil {
		return errors.New("TEST publication not confirmed; inspect durable outcome, do not resend")
	}
	return nil
}

func prepareNativePrice(ctx context.Context, scope futures.CommandScope, target futures.TestQueryTarget, database string, queryID, receiptID int64, mappingFile string, getenv func(string) string, out io.Writer) error {
	raw, err := readBounded(mappingFile, 4096)
	if err != nil {
		return err
	}
	var mapping futures.CatalogBinding
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&mapping) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid native price catalog mapping")
	}
	url := getenv("RELAY_CTP_TRADE_DATABASE_URL")
	pg, err := pgx.ParseConfig(url)
	if url == "" || err != nil || pg.Database != database {
		return errors.New("explicit matching RELAY_CTP_TRADE_DATABASE_URL required")
	}
	pg.ConnectTimeout = 3 * time.Second
	db := stdlib.OpenDB(*pg)
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var actual string
	if db.QueryRowContext(ctx, "SELECT current_database()").Scan(&actual) != nil || actual != database {
		return errors.New("cannot verify dedicated TEST database")
	}
	store, err := futures.NewCommandStore(db)
	if err != nil {
		return err
	}
	result, err := store.PrepareTestNativePrice(ctx, scope, target, queryID, receiptID, mapping)
	if err != nil {
		if errors.Is(err, futures.ErrQueryPublishGate) || errors.Is(err, futures.ErrStreamIdentity) {
			return fmt.Errorf("native price preparation rejected; no command sent: %w", err)
		}
		return errors.New("native price evidence unavailable, incomplete, inconsistent or expired; no command sent")
	}
	return json.NewEncoder(out).Encode(map[string]any{"mode": "read_only_native_price", "command_published": false, "business_authorized": false, "preparation": result})
}
