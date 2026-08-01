package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"ti-relay-trader/internal/trading"
)

func TestRepositoryWritesToPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RELAY_LEDGER_TEST_DATABASE_URL to run PostgreSQL ledger integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}

	repo := NewRepository(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	accountID := "ledger-test-" + suffix
	gatewayOrderID := "gateway-" + suffix
	fillID := "fill-" + suffix
	requestID := "request-" + suffix
	streamKey := "relay:test:v1:ledger:event"
	orderStreamID := "order-stream-" + suffix

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM fills WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM order_fee_records WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM order_events WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM orders WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM raw_stream_messages WHERE request_id = $1", requestID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM accounts WHERE account_id = $1", accountID)
	})

	if err := repo.UpsertAccount(ctx, trading.Account{
		AccountID:      accountID,
		BrokerID:       "test-broker",
		Status:         trading.AccountStatusEnabled,
		Enabled:        true,
		TradingEnabled: true,
		Simulated:      true,
		Tags:           map[string]string{"scope": "integration"},
	}); err != nil {
		t.Fatalf("UpsertAccount() error = %v", err)
	}

	order := trading.Order{
		AccountID:      accountID,
		ClientOrderID:  "client-" + suffix,
		GatewayOrderID: gatewayOrderID,
		OrderStreamID:  orderStreamID,
		TradeDate:      "2026-06-13",
		Symbol:         "600000",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideBuy,
		BusinessType:   trading.BusinessTypeStock,
		LimitPrice:     10.25,
		OrderQty:       100,
		Status:         trading.OrderStatusAccepted,
		GatewayStatus:  trading.GatewayStatusAccepted,
		RequestID:      requestID,
		IdempotencyKey: "idempotency-" + suffix,
		AdapterContext: map[string]any{"scope": "integration"},
	}
	if err := repo.CreateOrder(ctx, order); err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	conflictingOrder := order
	conflictingOrder.ClientOrderID = "client-conflict-" + suffix
	conflictingOrder.GatewayOrderID = "gateway-conflict-" + suffix
	if err := repo.CreateOrder(ctx, conflictingOrder); !errors.Is(err, ErrOrderConflict) {
		t.Fatalf("CreateOrder() duplicate idempotency error = %v, want ErrOrderConflict", err)
	}

	lateGatewayOrderID := gatewayOrderID + "-late"
	lateFeeRecord := OrderFeeRecord{
		AccountID:           accountID,
		FeeRecordID:         "fee-late-" + suffix,
		TradeDate:           "2026-06-13",
		RecordScope:         "order",
		GatewayOrderID:      lateGatewayOrderID,
		OrderStreamID:       orderStreamID + "-late",
		Symbol:              "600000",
		Exchange:            "SH",
		TradeSide:           "B",
		BusinessType:        "S",
		Turnover:            1025,
		Commission:          7,
		TotalFee:            7,
		FeeComplete:         true,
		FeeSource:           "broker_order_fund_detail",
		FeeAsOf:             time.Now().UTC(),
		AssociationComplete: true,
	}
	if err := repo.UpsertOrderFeeRecord(ctx, lateFeeRecord); err != nil {
		t.Fatalf("UpsertOrderFeeRecord() before order error = %v", err)
	}
	lateOrder := order
	lateOrder.ClientOrderID += "-late"
	lateOrder.GatewayOrderID = lateGatewayOrderID
	lateOrder.OrderStreamID += "-late"
	lateOrder.IdempotencyKey += "-late"
	if err := repo.CreateOrder(ctx, lateOrder); err != nil {
		t.Fatalf("CreateOrder() after fee error = %v", err)
	}
	if err := repo.UpsertOrderFeeRecord(ctx, lateFeeRecord); err != nil {
		t.Fatalf("UpsertOrderFeeRecord() stable replay error = %v", err)
	}
	var lateOrderFee float64
	if err := db.QueryRowContext(ctx, `SELECT fee FROM orders WHERE account_id = $1 AND trade_date = $2::date AND gateway_order_id = $3`, accountID, "2026-06-13", lateGatewayOrderID).Scan(&lateOrderFee); err != nil {
		t.Fatalf("query replay-associated order fee: %v", err)
	}
	if lateOrderFee != 7 {
		t.Fatalf("replay-associated order fee = %v, want 7", lateOrderFee)
	}

	if err := repo.AppendOrderEvent(ctx, trading.OrderEvent{
		EventID:        "event-" + suffix,
		AccountID:      accountID,
		GatewayOrderID: gatewayOrderID,
		Status:         trading.OrderStatusAccepted,
		GatewayStatus:  trading.GatewayStatusAccepted,
		Order:          order,
		ProducedAt:     time.Now().UTC(),
	}, StreamRef{
		Key: streamKey,
		ID:  fmt.Sprintf("%d-0", time.Now().UnixMilli()),
	}, SourceRef{
		RequestID: requestID,
	}); err != nil {
		t.Fatalf("AppendOrderEvent() error = %v", err)
	}

	if err := repo.InsertFill(ctx, trading.Fill{
		FillID:         fillID,
		AccountID:      accountID,
		GatewayOrderID: gatewayOrderID,
		OrderStreamID:  orderStreamID,
		Symbol:         "600000",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideBuy,
		Price:          10.25,
		Qty:            100,
		Fee:            1.23,
		TradeDate:      "2026-06-13",
		MatchTimestamp: time.Now().UnixMilli(),
		MatchedAt:      time.Now().UTC(),
		AdapterContext: map[string]any{"scope": "integration"},
	}, StreamRef{
		Key: streamKey,
		ID:  fmt.Sprintf("%d-1", time.Now().UnixMilli()),
	}, SourceRef{
		RequestID: requestID,
	}); err != nil {
		t.Fatalf("InsertFill() error = %v", err)
	}

	feeAsOf := time.Now().UTC()
	if err := repo.UpsertOrderFeeRecord(ctx, OrderFeeRecord{
		AccountID:           accountID,
		FeeRecordID:         "fee-" + suffix,
		TradeDate:           "2026-06-13",
		RecordScope:         "order",
		GatewayOrderID:      gatewayOrderID,
		OrderStreamID:       orderStreamID,
		Symbol:              "600000",
		Exchange:            "SH",
		TradeSide:           "B",
		BusinessType:        "S",
		Turnover:            1025,
		Commission:          5,
		TotalFee:            5,
		FeeComplete:         true,
		FeeSource:           "broker_order_fund_detail",
		FeeAsOf:             feeAsOf,
		AssociationComplete: true,
	}); err != nil {
		t.Fatalf("UpsertOrderFeeRecord() error = %v", err)
	}
	fees, err := repo.ListOrderFeeRecords(ctx, OrderFeeRecordQuery{
		AccountID:      accountID,
		TradeDate:      "20260613",
		GatewayOrderID: gatewayOrderID,
	})
	if err != nil || len(fees) != 1 || fees[0].TotalFee != 5 {
		t.Fatalf("ListOrderFeeRecords() fees/error = %#v/%v", fees, err)
	}
	var orderFee float64
	if err := db.QueryRowContext(ctx, `SELECT fee FROM orders WHERE account_id = $1 AND trade_date = $2::date AND gateway_order_id = $3`, accountID, "2026-06-13", gatewayOrderID).Scan(&orderFee); err != nil {
		t.Fatalf("query updated order fee: %v", err)
	}
	if orderFee != 5 {
		t.Fatalf("order fee = %v, want 5", orderFee)
	}
	degradedFeeRecord := OrderFeeRecord{
		AccountID:           accountID,
		FeeRecordID:         "fee-" + suffix,
		TradeDate:           "2026-06-13",
		RecordScope:         "order",
		GatewayOrderID:      gatewayOrderID,
		TotalFee:            0,
		FeeComplete:         false,
		FeeSource:           "unavailable",
		FeeAsOf:             feeAsOf.Add(time.Minute),
		AssociationComplete: false,
	}
	if err := repo.UpsertOrderFeeRecord(ctx, degradedFeeRecord); err != nil {
		t.Fatalf("UpsertOrderFeeRecord() degraded replay error = %v", err)
	}
	fees, err = repo.ListOrderFeeRecords(ctx, OrderFeeRecordQuery{
		AccountID:      accountID,
		TradeDate:      "20260613",
		GatewayOrderID: gatewayOrderID,
	})
	if err != nil || len(fees) != 1 || !fees[0].FeeComplete || !fees[0].AssociationComplete || fees[0].TotalFee != 5 {
		t.Fatalf("final fee downgraded by incomplete replay: %#v/%v", fees, err)
	}

	if err := repo.ArchiveRawStreamMessage(ctx, RawStreamMessage{
		StreamRef: StreamRef{
			Key: "relay:test:v1:ledger:raw",
			ID:  fmt.Sprintf("%d-0", time.Now().UnixMilli()),
		},
		SourceRef: SourceRef{
			RequestID: requestID,
		},
		Direction:      "out",
		Role:           "event",
		MessageType:    "event",
		EventType:      "order.event",
		Status:         "accepted",
		AccountID:      accountID,
		GatewayOrderID: gatewayOrderID,
		Body: map[string]any{
			"account_id":       accountID,
			"gateway_order_id": gatewayOrderID,
		},
	}); err != nil {
		t.Fatalf("ArchiveRawStreamMessage() error = %v", err)
	}

	assertRowCount(ctx, t, db, "orders", "account_id = $1 AND gateway_order_id = $2", accountID, gatewayOrderID)
	assertRowCount(ctx, t, db, "fills", "account_id = $1 AND fill_id = $2", accountID, fillID)
	assertRowCount(ctx, t, db, "order_fee_records", "account_id = $1 AND gateway_order_id = $2", accountID, gatewayOrderID)
	assertRowCount(ctx, t, db, "order_events", "account_id = $1 AND gateway_order_id = $2", accountID, gatewayOrderID)
	assertRowCount(ctx, t, db, "raw_stream_messages", "request_id = $1", requestID)
}

func assertRowCount(ctx context.Context, t *testing.T, db *sql.DB, table, where string, args ...any) {
	t.Helper()
	var count int
	query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, where)
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != 1 {
		t.Fatalf("count %s = %d, want 1", table, count)
	}
}
