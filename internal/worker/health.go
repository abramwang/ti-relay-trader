package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"ti-relay-trader/internal/timeutil"
)

type HealthState struct {
	environment string
	started     time.Time
	ready       atomic.Bool
}

func NewHealthState(environment string) *HealthState {
	return &HealthState{environment: environment, started: timeutil.Now()}
}

func (state *HealthState) MarkReady() {
	if state != nil {
		state.ready.Store(true)
	}
}

func (state *HealthState) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", state.handleHealth)
	mux.HandleFunc("/readyz", state.handleReady)
	return mux
}

func (state *HealthState) handleHealth(w http.ResponseWriter, _ *http.Request) {
	state.write(w, http.StatusOK, "running")
}

func (state *HealthState) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !state.ready.Load() {
		state.write(w, http.StatusServiceUnavailable, "starting")
		return
	}
	state.write(w, http.StatusOK, "ready")
}

func (state *HealthState) write(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          status < http.StatusBadRequest,
		"service":     "relay-worker",
		"environment": state.environment,
		"status":      value,
		"started_at":  state.started,
		"time":        timeutil.Now(),
	})
}

func ListenHealth(addr string, handler http.Handler) (net.Listener, *http.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
	}
	return listener, server, nil
}

func HealthCheckURL(rawURL string, expectedEnvironment ...string) func(context.Context) error {
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport}
	return func(ctx context.Context) error {
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("worker health URL is invalid")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return errors.New("worker is not ready")
		}
		var body struct {
			Service     string `json:"service"`
			Environment string `json:"environment"`
			Status      string `json:"status"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return fmt.Errorf("decode worker health response: %w", err)
		}
		if body.Service != "relay-worker" || body.Status != "ready" {
			return errors.New("worker health response has unexpected identity or status")
		}
		if len(expectedEnvironment) > 0 && expectedEnvironment[0] != "" && body.Environment != expectedEnvironment[0] {
			return errors.New("worker environment does not match API environment")
		}
		return nil
	}
}
