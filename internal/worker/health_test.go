package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthStateTransitionsToReady(t *testing.T) {
	state := NewHealthState("production")
	server := httptest.NewServer(state.Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET starting readyz: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("starting status = %d", response.StatusCode)
	}

	state.MarkReady()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := HealthCheckURL(server.URL+"/readyz", "production")(ctx); err != nil {
		t.Fatalf("ready health check failed: %v", err)
	}
}

func TestHealthCheckRejectsWrongEnvironment(t *testing.T) {
	state := NewHealthState("test")
	state.MarkReady()
	server := httptest.NewServer(state.Handler())
	defer server.Close()

	err := HealthCheckURL(server.URL+"/readyz", "production")(context.Background())
	if err == nil {
		t.Fatal("expected environment mismatch")
	}
}
