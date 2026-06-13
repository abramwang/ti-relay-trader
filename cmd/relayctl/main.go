package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/redisstream"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "redis-probe":
		if err := runRedisProbe(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "relayctl redis-probe: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runRedisProbe(args []string) error {
	flags := flag.NewFlagSet("redis-probe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	prefix := flags.String("stream-prefix", "", "override stream prefix, for example relay:prod:v1:huaxin:00030484")
	samples := flags.Int("samples", 3, "latest message summaries per stream")
	timeout := flags.Duration("timeout", 5*time.Second, "probe timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	*cfg = redisstream.ApplyProbeEnv(*cfg)

	var prefixes []string
	if strings.TrimSpace(*prefix) != "" {
		prefixes = []string{strings.TrimSpace(*prefix)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report, err := redisstream.Probe(ctx, *cfg, redisstream.ProbeOptions{
		SamplesPerStream: *samples,
		Prefixes:         prefixes,
	})
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func loadConfig(path string) (*config.Config, error) {
	if strings.TrimSpace(path) == "" {
		cfg := config.Default()
		return &cfg, nil
	}
	return config.Load(path)
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, `relayctl commands:
  redis-probe  Read-only Redis Stream probe using relay config

Examples:
  RELAY_CONFIG_PATH=config/relay.local.yaml go run ./cmd/relayctl redis-probe
  go run ./cmd/relayctl redis-probe -config config/relay.local.yaml -samples 2
  go run ./cmd/relayctl redis-probe -config config/relay.local.yaml -stream-prefix relay:prod:v1:huaxin:00030484`)
}
