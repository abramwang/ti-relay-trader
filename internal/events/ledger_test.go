package events

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

func TestFromLedgerChangePreservesCallbackSemantics(t *testing.T) {
	retrySafe := true
	events := FromLedgerChange(redisstream.LedgerChange{
		Stream:         "relay:prod:v1:huaxin:acct-1:event",
		Role:           redisstream.SuffixEvent,
		AccountIDs:     []string{"acct-1"},
		OrderEvents:    1,
		CancelFailures: 1,
		CancelFailureItems: []ledger.OrderCancelAttempt{{
			AttemptID:      "cancel-1",
			AccountID:      "acct-1",
			GatewayOrderID: "order-1",
			Code:           "BROKER_NOT_READY",
			RetrySafe:      &retrySafe,
			RawPayload:     strings.Repeat("x", 9000),
		}},
		Fills:        1,
		Assets:       1,
		Positions:    1,
		LastStreamID: "1-0",
	})

	wantTypes := []string{TypeOrderChanged, TypeOrderCancelRejected, TypeFillChanged, TypeAssetChanged, TypePositionsChanged}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d", len(events), len(wantTypes))
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d].type = %q, want %q", i, events[i].Type, want)
		}
	}
	cancelData := events[1].Data.(map[string]any)["cancel_attempt"].(map[string]any)
	if cancelData["gateway_order_id"] != "order-1" || cancelData["code"] != "BROKER_NOT_READY" {
		t.Fatalf("cancel event data = %#v", cancelData)
	}
	if _, ok := cancelData["raw_payload"]; ok {
		t.Fatal("cross-process cancel event must not include raw payload")
	}
}

func TestPostgresNotifierEncodesVersionTolerantEvent(t *testing.T) {
	exec := &captureExecutor{}
	notifier := NewPostgresNotifier(exec)
	event := Event{Type: TypeOrderChanged, AccountIDs: []string{"acct-1"}, Time: time.Now()}
	if err := notifier.Notify(context.Background(), event); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if exec.query != "SELECT pg_notify($1, $2)" || len(exec.args) != 2 || exec.args[0] != PostgresChannel {
		t.Fatalf("notification query = %q args=%#v", exec.query, exec.args)
	}
	payload := exec.args[1].(string)
	decoded, err := decodePostgresEvent(strings.TrimSuffix(payload, "}") + `,"future_field":true}`)
	if err != nil {
		t.Fatalf("decodePostgresEvent rejected a future field: %v", err)
	}
	if decoded.Type != TypeOrderChanged || decoded.AccountIDs[0] != "acct-1" {
		t.Fatalf("decoded event = %#v", decoded)
	}
}

func TestPostgresEventDecoderAcceptsLegacyPayload(t *testing.T) {
	decoded, err := decodePostgresEvent(`{"type":"fill.changed","account_ids":["acct-1"]}`)
	if err != nil {
		t.Fatalf("decode legacy event: %v", err)
	}
	if decoded.Type != TypeFillChanged || decoded.AccountIDs[0] != "acct-1" {
		t.Fatalf("decoded legacy event = %#v", decoded)
	}
}

func TestPostgresEventDecoderRejectsUnknownSchema(t *testing.T) {
	_, err := decodePostgresEvent(`{"schema":"relay.ledger_event.v2","event":{"type":"fill.changed"}}`)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPostgresNotifierRejectsOversizedPayload(t *testing.T) {
	notifier := NewPostgresNotifier(&captureExecutor{})
	err := notifier.Notify(context.Background(), Event{
		Type: TypeOrderChanged,
		Data: map[string]any{"oversized": strings.Repeat("x", postgresNotifyMaxPayload)},
	})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type captureExecutor struct {
	query string
	args  []any
}

func (exec *captureExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	exec.query = query
	exec.args = args
	return captureResult{}, nil
}

type captureResult struct{}

func (captureResult) LastInsertId() (int64, error) { return 0, nil }
func (captureResult) RowsAffected() (int64, error) { return 1, nil }
