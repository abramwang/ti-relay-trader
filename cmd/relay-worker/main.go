package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	relayconfig "ti-relay-trader/internal/config"
	"ti-relay-trader/internal/logging"
	"ti-relay-trader/internal/worker"
)

func main() {
	configPath := flag.String("config", os.Getenv(relayconfig.EnvPath), "relay config file path")
	healthAddr := flag.String("health-addr", "", "worker health listen address")
	flag.Parse()

	if strings.TrimSpace(*configPath) == "" {
		exitError("config path is required")
	}
	cfg, err := relayconfig.Load(*configPath)
	if err != nil {
		exitError("load config: %v", err)
	}
	cfg.Service.Mode = relayconfig.ModeWorker
	if strings.TrimSpace(*healthAddr) == "" {
		*healthAddr = cfg.Worker.HealthAddr
	}

	logger, err := logging.New(os.Stdout, cfg.Service.LogLevel, cfg.Service.LogFormat)
	if err != nil {
		exitError("create logger: %v", err)
	}
	if err := run(*cfg, *healthAddr, logger); err != nil {
		logger.Error("relay_worker_process_stopped", "error", err)
		os.Exit(1)
	}
}

func run(cfg relayconfig.Config, healthAddr string, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	health := worker.NewHealthState(string(cfg.Service.Environment))
	listener, healthServer, err := worker.ListenHealth(healthAddr, health.Handler())
	if err != nil {
		return fmt.Errorf("listen worker health: %w", err)
	}
	healthDone := make(chan error, 1)
	go func() {
		err := healthServer.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			healthDone <- err
			return
		}
		healthDone <- nil
	}()
	logger.Info("relay_worker_health_listening", "addr", healthAddr)

	workerDone := make(chan error, 1)
	go func() {
		workerDone <- worker.RunWithOptions(ctx, cfg, logger, worker.RunOptions{
			OnReady:       health.MarkReady,
			PublishEvents: true,
		})
	}()

	select {
	case err := <-workerDone:
		stop()
		shutdownHealth(healthServer)
		<-healthDone
		return err
	case err := <-healthDone:
		stop()
		workerErr := <-workerDone
		if err != nil {
			return fmt.Errorf("worker health server: %w", err)
		}
		return workerErr
	case <-ctx.Done():
		shutdownHealth(healthServer)
		workerErr := <-workerDone
		<-healthDone
		return workerErr
	}
}

func shutdownHealth(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func exitError(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "relay-worker: "+format+"\n", args...)
	os.Exit(1)
}
