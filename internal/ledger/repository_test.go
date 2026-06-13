package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/trading"
)

func TestUpsertAccountBuildsAccountWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	repo.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	err := repo.UpsertAccount(context.Background(), trading.Account{
		AccountID:      "acct-1",
		BrokerID:       "huaxin",
		Status:         trading.AccountStatusEnabled,
		Enabled:        true,
		TradingEnabled: false,
		Simulated:      false,
		Tags:           map[string]string{"desk": "live"},
	})
	if err != nil {
		t.Fatalf("UpsertAccount() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO accounts")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id)")
	requireArgLen(t, exec.args, 8)
	if exec.args[0] != "acct-1" {
		t.Fatalf("account_id arg = %#v", exec.args[0])
	}
	assertJSONContains(t, exec.args[6], `"desk":"live"`)
}

func TestUpsertOrderBuildsLedgerUpsert(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpsertOrder(context.Background(), trading.Order{
		AccountID:       "acct-1",
		ClientOrderID:   "client-1",
		GatewayOrderID:  "gateway-1",
		Symbol:          "600000",
		Exchange:        trading.ExchangeSH,
		TradeSide:       trading.TradeSideBuy,
		BusinessType:    trading.BusinessTypeStock,
		LimitPrice:      10.25,
		OrderQty:        100,
		Status:          trading.OrderStatusAccepted,
		GatewayStatus:   trading.GatewayStatusAccepted,
		OriginMessageID: "msg-1",
		AdapterContext:  map[string]any{"front_status": "accepted"},
	})
	if err != nil {
		t.Fatalf("UpsertOrder() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO orders")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id, gateway_order_id)")
	requireArgLen(t, exec.args, 38)
	if exec.args[0] != "acct-1" || exec.args[2] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.args[0], exec.args[2])
	}
	if exec.args[15] != int64(100) {
		t.Fatalf("leaves_qty arg = %#v, want 100", exec.args[15])
	}
	assertJSONContains(t, exec.args[36], `"gateway_order_id":"gateway-1"`)
	assertJSONContains(t, exec.args[37], `"front_status":"accepted"`)
}

func TestAppendOrderEventIsIdempotentByEventOrStream(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.AppendOrderEvent(context.Background(), trading.OrderEvent{
		EventID:        "event-1",
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		Status:         trading.OrderStatusFilled,
		GatewayStatus:  trading.GatewayStatusFilled,
		Order: trading.Order{
			AccountID:       "acct-1",
			GatewayOrderID:  "gateway-1",
			OriginMessageID: "msg-1",
			RequestID:       "req-1",
		},
		ProducedAt: time.Unix(1700000001, 0).UTC(),
	}, StreamRef{
		Key: "relay:prod:v1:huaxin:g1:event",
		ID:  "1700000001000-0",
	}, SourceRef{
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("AppendOrderEvent() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO order_events")
	requireQueryContains(t, exec.query, "ON CONFLICT DO NOTHING")
	requireArgLen(t, exec.args, 15)
	if exec.args[6] != true {
		t.Fatalf("is_terminal arg = %#v, want true", exec.args[6])
	}
	if streamKey := exec.args[7].(sql.NullString); streamKey.String != "relay:prod:v1:huaxin:g1:event" || !streamKey.Valid {
		t.Fatalf("stream key arg = %#v", streamKey)
	}
	if correlationID := exec.args[11].(sql.NullString); correlationID.String != "corr-1" || !correlationID.Valid {
		t.Fatalf("correlation arg = %#v", correlationID)
	}
}

func TestUpdateOrderStatusBuildsPartialStatusUpdate(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpdateOrderStatus(context.Background(), trading.OrderEvent{
		EventID:        "event-1",
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		Status:         trading.OrderStatusWorking,
		GatewayStatus:  trading.GatewayStatusWorking,
		Order: trading.Order{
			AccountID:      "acct-1",
			GatewayOrderID: "gateway-1",
			OrderID:        1680001,
			OrderStreamID:  "order-stream-1",
			CumFilledQty:   50,
			LeavesQty:      50,
		},
		ProducedAt:     time.Unix(1700000001, 0).UTC(),
		AdapterContext: map[string]any{"order_status_name": "queued"},
	})
	if err != nil {
		t.Fatalf("UpdateOrderStatus() error = %v", err)
	}

	requireQueryContains(t, exec.query, "UPDATE orders SET")
	requireQueryContains(t, exec.query, "WHERE account_id = $1 AND gateway_order_id = $2")
	requireArgLen(t, exec.args, 19)
	if exec.args[0] != "acct-1" || exec.args[1] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.args[0], exec.args[1])
	}
	assertJSONContains(t, exec.args[18], `"order_status_name":"queued"`)
}

func TestInsertFillBuildsIdempotentFillWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.InsertFill(context.Background(), trading.Fill{
		FillID:         "fill-1",
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		OrderStreamID:  "order-stream-1",
		Symbol:         "600000",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideBuy,
		Price:          10.25,
		Qty:            100,
		Fee:            1.23,
		TradeDate:      "2026-06-13",
		MatchTimestamp: 1700000002000,
		AdapterContext: map[string]any{"match_type": "counter"},
	}, StreamRef{
		Key: "relay:prod:v1:huaxin:g1:event",
		ID:  "1700000002000-0",
	}, SourceRef{
		OriginMessageID: "msg-1",
		RequestID:       "req-1",
	})
	if err != nil {
		t.Fatalf("InsertFill() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO fills")
	requireQueryContains(t, exec.query, "ON CONFLICT DO NOTHING")
	requireArgLen(t, exec.args, 22)
	if exec.args[0] != "acct-1" || exec.args[2] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.args[0], exec.args[2])
	}
	assertJSONContains(t, exec.args[20], `"fill_id":"fill-1"`)
	assertJSONContains(t, exec.args[21], `"match_type":"counter"`)
}

func TestArchiveRawStreamMessageBuildsAuditWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.ArchiveRawStreamMessage(context.Background(), RawStreamMessage{
		StreamRef: StreamRef{
			Key: "relay:prod:v1:huaxin:g1:reply",
			ID:  "1700000003000-0",
		},
		SourceRef: SourceRef{
			OriginMessageID: "msg-1",
			RequestID:       "req-1",
			CorrelationID:   "corr-1",
			IdempotencyKey:  "idem-1",
		},
		Direction:      "out",
		Role:           "reply",
		MessageType:    "reply",
		Action:         "order.submit",
		Status:         "accepted",
		Code:           "OK",
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		Body: map[string]any{
			"gateway_order_id": "gateway-1",
			"status":           "accepted",
		},
		BodyText:   `{"status":"accepted"}`,
		ReceivedAt: time.Unix(1700000003, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("ArchiveRawStreamMessage() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO raw_stream_messages")
	requireQueryContains(t, exec.query, "ON CONFLICT (stream_key, stream_id)")
	requireArgLen(t, exec.args, 19)
	if exec.args[0] != "relay:prod:v1:huaxin:g1:reply" || exec.args[3] != "reply" {
		t.Fatalf("stream args = %#v %#v", exec.args[0], exec.args[3])
	}
	assertJSONContains(t, exec.args[15], `"gateway_order_id":"gateway-1"`)
}

func TestRepositoryValidation(t *testing.T) {
	repo := NewRepository(&recordingExecutor{})

	err := repo.UpsertOrder(context.Background(), trading.Order{})
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("UpsertOrder() error = %v, want ErrInvalidLedgerInput", err)
	}

	err = repo.ArchiveRawStreamMessage(context.Background(), RawStreamMessage{})
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("ArchiveRawStreamMessage() error = %v, want ErrInvalidLedgerInput", err)
	}
}

type recordingExecutor struct {
	query string
	args  []any
	err   error
}

func (exec *recordingExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	exec.query = query
	exec.args = append([]any(nil), args...)
	return nil, exec.err
}

func requireQueryContains(t *testing.T, query, needle string) {
	t.Helper()
	if !strings.Contains(query, needle) {
		t.Fatalf("query does not contain %q:\n%s", needle, query)
	}
}

func requireArgLen(t *testing.T, args []any, want int) {
	t.Helper()
	if len(args) != want {
		t.Fatalf("arg len = %d, want %d", len(args), want)
	}
}

func assertJSONContains(t *testing.T, value any, needle string) {
	t.Helper()
	var bytes []byte
	switch typed := value.(type) {
	case []byte:
		bytes = typed
	default:
		marshaled, err := json.Marshal(typed)
		if err != nil {
			t.Fatalf("marshal arg json: %v", err)
		}
		bytes = marshaled
	}
	if !strings.Contains(string(bytes), needle) {
		t.Fatalf("json %s does not contain %s", bytes, needle)
	}
}
