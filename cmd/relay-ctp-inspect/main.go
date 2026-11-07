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

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/futures"
)

type quietRedisLogger struct{}

func (quietRedisLogger) Printf(context.Context, string, ...interface{}) {}

func main() {
	// This standalone process emits only classified errors, not library logs
	// that can contain endpoints or arbitrary server-supplied text.
	redis.SetLogger(quietRedisLogger{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("relay-ctp-inspect", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	var req futures.HeartbeatInspectionRequest
	flags.StringVar(&req.Route.Env, "env", "", "must explicitly be test")
	flags.StringVar(&req.Route.BrokerID, "broker", "", "CTP routing broker label")
	flags.StringVar(&req.Route.GatewayID, "gateway", "", "explicit gateway, not an account alias")
	flags.StringVar(&req.Route.AccountID, "account", "", "logical account; no native identity inferred")
	flags.StringVar(&req.After, "after", "0-0", "exclusive diagnostic cursor; never use $ to skip history")
	flags.IntVar(&req.Limit, "limit", 32, "one page, 1..256 entries")
	runNetwork := flags.Bool("run", false, "perform one read-only TEST heartbeat inspection")
	timeout := flags.Duration("timeout", 10*time.Second, "total network deadline, at most 30s")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid inspection arguments")
	}
	if flags.NArg() != 0 || *timeout <= 0 || *timeout > 30*time.Second {
		return errors.New("invalid inspection arguments")
	}
	key, err := req.Stream()
	if err != nil {
		return errors.New("explicit TEST CTP route, canonical cursor and bounded page required")
	}
	if !*runNetwork {
		return json.NewEncoder(output).Encode(struct {
			Mode               string                             `json:"mode"`
			Stream             string                             `json:"stream"`
			Request            futures.HeartbeatInspectionRequest `json:"request"`
			BusinessAuthorized bool                               `json:"business_authorized"`
		}{"check_only_no_connections", key, req, false})
	}
	url := getenv("RELAY_CTP_INSPECT_REDIS_URL")
	if url == "" {
		return errors.New("RELAY_CTP_INSPECT_REDIS_URL is required; active service config is not used")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		return errors.New("invalid inspection Redis URL")
	}
	if options.TLSConfig != nil && options.TLSConfig.InsecureSkipVerify {
		return errors.New("TLS verification must remain enabled")
	}
	options.Protocol, options.MaxRetries = 2, -1
	options.DialerRetries = 1
	options.PoolSize, options.MaxActiveConns, options.MinIdleConns, options.MaxIdleConns = 1, 1, 0, 1
	options.DialTimeout, options.ReadTimeout, options.WriteTimeout = 2*time.Second, 2*time.Second, 2*time.Second
	options.ContextTimeoutEnabled = true
	options.ClientName = "relay-ctp-test-heartbeat-inspect"
	client := redis.NewClient(options)
	defer client.Close()
	reader, err := futures.NewRedisStreamReader(client)
	if err != nil {
		return errors.New("cannot create inspection reader")
	}
	bounded, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := futures.InspectTestHeartbeats(bounded, reader, req)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(report)
}
