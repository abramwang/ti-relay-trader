package integration

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/events"
)

func TestLivePostgresEventBridge(t *testing.T) {
	configPath := strings.TrimSpace(os.Getenv("RELAY_EVENT_BRIDGE_TEST_CONFIG"))
	if configPath == "" {
		t.Skip("set RELAY_EVENT_BRIDGE_TEST_CONFIG to run the live cross-process event bridge smoke")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("RELAY_EVENT_BRIDGE_TEST_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9092"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.EmbeddedLedgerSyncEnabled() {
		t.Fatal("live event bridge smoke requires worker.embedded_ledger_sync=false")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	accountID := "__relay_event_bridge_smoke__"
	streamURL := baseURL + "/v1/events/stream?account_id=" + url.QueryEscape(accountID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		t.Fatalf("create SSE request: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", response.StatusCode)
	}

	scanner := bufio.NewScanner(response.Body)
	connected := readSSEEvent(t, scanner)
	if connected.Type != events.TypeConnected {
		t.Fatalf("first SSE event = %q, want %q", connected.Type, events.TypeConnected)
	}

	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	nonce := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	if err := events.NewPostgresNotifier(db).Notify(ctx, events.Event{
		Type:       "relay.runtime.test",
		AccountIDs: []string{accountID},
		Source:     "event-bridge-smoke",
		Data:       map[string]any{"nonce": nonce},
	}); err != nil {
		t.Fatalf("publish postgres event: %v", err)
	}

	received := readSSEEvent(t, scanner)
	if received.Type != "relay.runtime.test" || len(received.AccountIDs) != 1 || received.AccountIDs[0] != accountID {
		t.Fatalf("bridged SSE event = %#v", received)
	}
	data, ok := received.Data.(map[string]any)
	if !ok || data["nonce"] != nonce {
		t.Fatalf("bridged SSE data = %#v", received.Data)
	}
}

func readSSEEvent(t *testing.T, scanner *bufio.Scanner) events.Event {
	t.Helper()
	eventType := ""
	data := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data += strings.TrimPrefix(line, "data: ")
		case line == "" && eventType != "":
			var event events.Event
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatalf("decode SSE event %q: %v", eventType, err)
			}
			return event
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE stream: %v", err)
	}
	t.Fatal("SSE stream ended before an event was received")
	return events.Event{}
}
