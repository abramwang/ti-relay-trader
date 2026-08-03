package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

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

func TestUpsertAccountAliasBuildsAliasWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	repo.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	err := repo.UpsertAccountAlias(context.Background(), " acct-1 ", " huaxin ", " 生产一号 ")
	if err != nil {
		t.Fatalf("UpsertAccountAlias() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO accounts")
	requireQueryContains(t, exec.query, "account_name")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id) DO UPDATE SET")
	requireQueryContains(t, exec.query, "account_name = EXCLUDED.account_name")
	requireArgLen(t, exec.args, 4)
	if exec.args[0] != "acct-1" || exec.args[1] != "huaxin" || exec.args[2] != "生产一号" {
		t.Fatalf("alias args = %#v", exec.args)
	}
}

func TestUpsertOrderBuildsLedgerUpsert(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpsertOrder(context.Background(), trading.Order{
		AccountID:       "acct-1",
		ClientOrderID:   "client-1",
		GatewayOrderID:  "gateway-1",
		TradeDate:       "2026-06-13",
		Symbol:          "600000",
		Exchange:        trading.ExchangeSH,
		TradeSide:       trading.TradeSideBuy,
		BusinessType:    trading.BusinessTypeStock,
		LimitPrice:      10.25,
		OrderQty:        100,
		Status:          trading.OrderStatusAccepted,
		GatewayStatus:   trading.GatewayStatusAccepted,
		OriginMessageID: "msg-1",
		StrategyType:    "stock_cross_section",
		StrategyID:      "strategy-a",
		BasketID:        "basket-1",
		ParentOrderID:   "parent-1",
		T0OrderGroupID:  "t0-1",
		AdapterContext:  map[string]any{"front_status": "accepted"},
	})
	if err != nil {
		t.Fatalf("UpsertOrder() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO orders")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id, trade_date, gateway_order_id)")
	requireQueryContains(t, exec.query, "created_at = COALESCE(EXCLUDED.created_at, orders.created_at)")
	requireQueryContains(t, exec.query, "WHEN EXCLUDED.adapter_context ? 'relay_reply_status' OR EXCLUDED.is_terminal = TRUE")
	requireQueryContains(t, exec.query, "WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.status")
	requireQueryContains(t, exec.query, "WHEN EXCLUDED.status = 'rejected' OR EXCLUDED.gateway_status = 'rejected'")
	requireQueryContains(t, exec.query, "SELECT MIN(event.produced_at)")
	requireQueryContains(t, exec.query, "event.trade_date = EXCLUDED.trade_date")
	requireQueryContains(t, exec.query, "trade_date = COALESCE(EXCLUDED.trade_date, orders.trade_date)")
	requireQueryContains(t, exec.query, "orders.adapter_context ? 'fee_record_id'")
	requireQueryContains(t, exec.query, "reported_fee_source")
	requireArgLen(t, exec.args, 44)
	if exec.args[0] != "acct-1" || exec.args[2] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.args[0], exec.args[2])
	}
	if exec.args[21] != int64(100) {
		t.Fatalf("leaves_qty arg = %#v, want 100", exec.args[21])
	}
	if tradeDate, ok := exec.args[5].(sql.NullString); !ok || !tradeDate.Valid || tradeDate.String != "2026-06-13" {
		t.Fatalf("trade_date arg = %#v", exec.args[5])
	}
	if strategyType, ok := exec.args[6].(sql.NullString); !ok || !strategyType.Valid || strategyType.String != "stock_cross_section" {
		t.Fatalf("strategy_type arg = %#v", exec.args[6])
	}
	assertJSONContains(t, exec.args[42], `"gateway_order_id":"gateway-1"`)
	assertJSONContains(t, exec.args[42], `"strategy_type":"stock_cross_section"`)
	assertJSONContains(t, exec.args[43], `"front_status":"accepted"`)
}

func TestCreateOrderUsesInsertOnlyReservation(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.CreateOrder(context.Background(), trading.Order{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		TradeDate:      "2026-06-13",
		Symbol:         "600000",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideBuy,
		BusinessType:   trading.BusinessTypeStock,
		LimitPrice:     10.25,
		OrderQty:       100,
		Status:         trading.OrderStatusCreated,
		GatewayStatus:  trading.GatewayStatusAccepted,
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO orders")
	if strings.Contains(exec.query, "ON CONFLICT") {
		t.Fatalf("CreateOrder() must reserve with an insert-only statement: %s", exec.query)
	}
	requireArgLen(t, exec.args, 44)
}

func TestCreateOrderClassifiesUniqueViolation(t *testing.T) {
	exec := &recordingExecutor{err: &pgconn.PgError{Code: "23505", ConstraintName: "orders_idempotency_unique"}}
	repo := NewRepository(exec)

	err := repo.CreateOrder(context.Background(), trading.Order{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		TradeDate:      "2026-06-13",
		Symbol:         "600000",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideBuy,
		BusinessType:   trading.BusinessTypeStock,
		LimitPrice:     10.25,
		OrderQty:       100,
		Status:         trading.OrderStatusCreated,
		GatewayStatus:  trading.GatewayStatusAccepted,
		IdempotencyKey: "idem-1",
	})
	if !errors.Is(err, ErrOrderConflict) {
		t.Fatalf("CreateOrder() error = %v, want ErrOrderConflict", err)
	}
}

func TestUpsertOrderCancelAttemptKeepsCancelOutcomeSeparate(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	retrySafe := false
	stateChanged := false

	err := repo.UpsertOrderCancelAttempt(context.Background(), OrderCancelAttempt{
		AttemptID:         "msg-cancel-1",
		AccountID:         "acct-1",
		TradeDate:         "2026-07-30",
		GatewayOrderID:    "gw-1",
		OrderID:           123,
		OrderStreamID:     "stream-order-1",
		OriginMessageID:   "msg-cancel-1",
		RequestID:         "req-cancel-1",
		Status:            "rejected",
		Code:              "BROKER_CANCEL_REJECTED",
		Message:           "current order state cannot be cancelled",
		RetrySafe:         &retrySafe,
		OrderStateChanged: &stateChanged,
		OccurredAt:        time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		StreamKey:         "relay:prod:v1:huaxin:acct-1:event",
		StreamID:          "1-0",
		RawPayload:        map[string]any{"cancel_status": "rejected"},
		AdapterContext:    map[string]any{"broker_error_id": 1},
	})
	if err != nil {
		t.Fatalf("UpsertOrderCancelAttempt() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO order_cancel_attempts")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id, attempt_id) DO UPDATE")
	requireQueryContains(t, exec.query, "reconciliation_required = order_cancel_attempts.reconciliation_required")
	requireArgLen(t, exec.args, 20)
	if exec.args[0] != "msg-cancel-1" || exec.args[1] != "acct-1" || exec.args[3] != "gw-1" {
		t.Fatalf("cancel attempt identity args = %#v", exec.args[:4])
	}
	assertJSONContains(t, exec.args[18], `"cancel_status":"rejected"`)
	assertJSONContains(t, exec.args[19], `"broker_error_id":1`)
}

func TestUpsertOrderFeeRecordBuildsAuthoritativeOrderFeeWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	feeAsOf := time.Date(2026, 8, 1, 15, 1, 59, 0, time.FixedZone("CST", 8*60*60))

	err := repo.UpsertOrderFeeRecord(context.Background(), OrderFeeRecord{
		AccountID:           "acct-1",
		FeeRecordID:         "broker-order-fee:stream-1",
		TradeDate:           "20260801",
		RecordScope:         "order",
		GatewayOrderID:      "gateway-1",
		OrderID:             123,
		OrderStreamID:       "stream-1",
		Symbol:              "600000",
		Exchange:            "SH",
		TradeSide:           "S",
		BusinessType:        "S",
		Turnover:            1000,
		Commission:          5,
		StampTax:            1,
		TotalFee:            6,
		Currency:            "CNY",
		FeeComplete:         true,
		FeeSource:           "broker_order_fund_detail",
		FeeAsOf:             feeAsOf,
		AssociationComplete: true,
		AdapterContext:      map[string]any{"broker_account_id": "acct-101"},
		RawPayload:          map[string]any{"fee_record_id": "broker-order-fee:stream-1"},
	})
	if err != nil {
		t.Fatalf("UpsertOrderFeeRecord() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO order_fee_records")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id, fee_record_id)")
	requireQueryContains(t, exec.query, "effective AS")
	requireQueryContains(t, exec.query, "NOT EXISTS (SELECT 1 FROM upserted)")
	requireQueryContains(t, exec.query, "order_fee_records.fee_source <> 'unavailable'")
	requireQueryContains(t, exec.query, "UPDATE orders")
	requireQueryContains(t, exec.query, "effective.association_complete")
	requireArgLen(t, exec.args, 36)
	if exec.args[0] != "acct-1" || exec.args[1] != "broker-order-fee:stream-1" || exec.args[2] != "2026-08-01" {
		t.Fatalf("fee identity args = %#v", exec.args[:3])
	}
	if exec.args[21] != float64(6) || exec.args[23] != true || exec.args[27] != true {
		t.Fatalf("fee amount/status args = %#v", exec.args[21:28])
	}
	assertJSONContains(t, exec.args[28], `"broker_account_id":"acct-101"`)
}

func TestUpsertOrderFeeRecordRejectsAssociatedRecordWithoutGatewayOrder(t *testing.T) {
	repo := NewRepository(&recordingExecutor{})
	err := repo.UpsertOrderFeeRecord(context.Background(), OrderFeeRecord{
		AccountID:           "acct-1",
		FeeRecordID:         "fee-1",
		TradeDate:           "2026-08-01",
		FeeAsOf:             time.Now(),
		AssociationComplete: true,
	})
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("UpsertOrderFeeRecord() error = %v, want ErrInvalidLedgerInput", err)
	}
}

func TestUpsertOrderInfersFilledFromExecutionQuantities(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpsertOrder(context.Background(), trading.Order{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-filled",
		TradeDate:      "2026-06-13",
		Symbol:         "600000",
		Exchange:       trading.ExchangeSH,
		TradeSide:      trading.TradeSideBuy,
		BusinessType:   trading.BusinessTypeStock,
		LimitPrice:     10.25,
		OrderQty:       100,
		CumFilledQty:   100,
		LeavesQty:      0,
		Status:         trading.OrderStatusAccepted,
		GatewayStatus:  trading.GatewayStatusAccepted,
	})
	if err != nil {
		t.Fatalf("UpsertOrder() error = %v", err)
	}

	requireArgLen(t, exec.args, 44)
	if exec.args[26] != trading.OrderStatusFilled || exec.args[27] != trading.GatewayStatusFilled || exec.args[30] != true {
		t.Fatalf("state args = %#v/%#v terminal=%#v, want filled/filled true", exec.args[26], exec.args[27], exec.args[30])
	}
}

func TestUpdateOrderStatusAllowsTerminalCumFilledCorrection(t *testing.T) {
	exec := &recordingExecutor{result: rowsAffectedResult(1)}
	repo := NewRepository(exec)

	err := repo.UpdateOrderStatus(context.Background(), trading.OrderEvent{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		Status:         trading.OrderStatusFilled,
		GatewayStatus:  trading.GatewayStatusFilled,
		IsTerminal:     true,
		Order: trading.Order{
			AccountID:      "acct-1",
			GatewayOrderID: "gateway-1",
			CumFilledQty:   60,
			LeavesQty:      0,
		},
		ProducedAt: time.Date(2026, 6, 15, 15, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpdateOrderStatus() error = %v", err)
	}

	requireQueryContains(t, exec.query, "cum_filled_qty = CASE WHEN $20 = TRUE THEN $12 ELSE GREATEST(cum_filled_qty, $12) END")
	requireArgLen(t, exec.args, 25)
	if exec.args[11] != int64(60) || exec.args[19] != true {
		t.Fatalf("cum/terminal args = %#v/%#v", exec.args[11], exec.args[19])
	}
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
	requireArgLen(t, exec.args, 21)
	if exec.args[6] != true {
		t.Fatalf("is_terminal arg = %#v, want true", exec.args[6])
	}
	if streamKey := exec.args[13].(sql.NullString); streamKey.String != "relay:prod:v1:huaxin:g1:event" || !streamKey.Valid {
		t.Fatalf("stream key arg = %#v", streamKey)
	}
	if correlationID := exec.args[17].(sql.NullString); correlationID.String != "corr-1" || !correlationID.Valid {
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
	requireQueryContains(t, exec.query, "AND gateway_order_id = $2")
	requireQueryContains(t, exec.query, "AND trade_date = $5::date")
	requireQueryContains(t, exec.query, "status = CASE WHEN is_terminal = TRUE AND $20 = FALSE THEN status ELSE $18 END")
	requireArgLen(t, exec.args, 25)
	if exec.args[0] != "acct-1" || exec.args[1] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.args[0], exec.args[1])
	}
	assertJSONContains(t, exec.args[24], `"order_status_name":"queued"`)
}

func TestUpdateOrderStatusInfersFilledFromExecutionQuantities(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpdateOrderStatus(context.Background(), trading.OrderEvent{
		EventID:        "event-filled",
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-filled",
		Status:         trading.OrderStatusAccepted,
		GatewayStatus:  trading.GatewayStatusAccepted,
		Order: trading.Order{
			AccountID:      "acct-1",
			GatewayOrderID: "gateway-filled",
			TradeDate:      "2026-06-13",
			OrderQty:       100,
			CumFilledQty:   100,
			LeavesQty:      0,
		},
	})
	if err != nil {
		t.Fatalf("UpdateOrderStatus() error = %v", err)
	}

	requireArgLen(t, exec.args, 25)
	if exec.args[17] != trading.OrderStatusFilled || exec.args[18] != trading.GatewayStatusFilled || exec.args[19] != true {
		t.Fatalf("state args = %#v/%#v terminal=%#v, want filled/filled true", exec.args[17], exec.args[18], exec.args[19])
	}
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
		BusinessType:   trading.BusinessTypeStock,
		Price:          10.25,
		Qty:            100,
		Fee:            1.23,
		TradeDate:      "2026-06-13",
		MatchTimestamp: 1700000002000,
		StrategyType:   "stock_cross_section",
		StrategyID:     "strategy-a",
		BasketID:       "basket-1",
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

	if len(exec.queries) != 2 {
		t.Fatalf("query count = %d, want insert + summary cleanup", len(exec.queries))
	}
	requireQueryContains(t, exec.queries[0], "INSERT INTO fills")
	requireQueryContains(t, exec.queries[0], "ON CONFLICT DO NOTHING")
	requireArgLen(t, exec.argsList[0], 28)
	if exec.argsList[0][0] != "acct-1" || exec.argsList[0][2] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.argsList[0][0], exec.argsList[0][2])
	}
	if businessType, ok := exec.argsList[0][5].(sql.NullString); !ok || !businessType.Valid || businessType.String != "S" {
		t.Fatalf("business_type arg = %#v", exec.argsList[0][5])
	}
	if strategyType, ok := exec.argsList[0][17].(sql.NullString); !ok || !strategyType.Valid || strategyType.String != "stock_cross_section" {
		t.Fatalf("strategy_type arg = %#v", exec.argsList[0][17])
	}
	assertJSONContains(t, exec.argsList[0][26], `"fill_id":"fill-1"`)
	assertJSONContains(t, exec.argsList[0][26], `"strategy_type":"stock_cross_section"`)
	assertJSONContains(t, exec.argsList[0][27], `"match_type":"counter"`)
	requireQueryContains(t, exec.queries[1], "DELETE FROM fills")
	requireArgLen(t, exec.argsList[1], 4)
	if exec.argsList[1][0] != "acct-1" || exec.argsList[1][1] != "2026-06-13" || exec.argsList[1][2] != "gateway-1" {
		t.Fatalf("summary cleanup args = %#v", exec.argsList[1])
	}
	if orderStream, ok := exec.argsList[1][3].(sql.NullString); !ok || !orderStream.Valid || orderStream.String != "order-stream-1" {
		t.Fatalf("summary cleanup order stream arg = %#v", exec.argsList[1][3])
	}
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

func TestListOrdersBuildsFilteredRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListOrders(context.Background(), trading.OrderQuery{
		AccountID: "acct-1",
		Symbol:    "600000",
		Exchange:  trading.ExchangeSH,
		Status:    trading.OrderStatusWorking,
		Limit:     25,
	})
	if err == nil {
		t.Fatal("ListOrders() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM orders")
	requireQueryContains(t, exec.query, "account_id = $1")
	requireQueryContains(t, exec.query, "symbol = $2")
	requireQueryContains(t, exec.query, "exchange = $3")
	requireQueryContains(t, exec.query, "status = $4")
	requireQueryContains(t, exec.query, "LIMIT $5")
	requireArgLen(t, exec.args, 5)
	if exec.args[4] != 25 {
		t.Fatalf("limit arg = %#v", exec.args[4])
	}
}

func TestListOrdersBuildsDateFilteredRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListOrders(context.Background(), trading.OrderQuery{
		AccountID: "acct-1",
		TradeDate: "20260614",
		Limit:     10,
	})
	if err == nil {
		t.Fatal("ListOrders() expected query error")
	}

	requireQueryContains(t, exec.query, "trade_date >= $2::date")
	requireQueryContains(t, exec.query, "COALESCE(inserted_at, accepted_at, created_at, last_updated_at, terminal_at) >= $3")
	requireQueryContains(t, exec.query, "trade_date < $4::date")
	requireQueryContains(t, exec.query, "COALESCE(inserted_at, accepted_at, created_at, last_updated_at, terminal_at) < $5")
	requireQueryContains(t, exec.query, "LIMIT $6")
	requireArgLen(t, exec.args, 6)
	if exec.args[0] != "acct-1" || exec.args[5] != 10 {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestListOrdersBuildsCursorOffset(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListOrders(context.Background(), trading.OrderQuery{
		AccountID: "acct-1",
		Cursor:    "25",
		Limit:     10,
	})
	if err == nil {
		t.Fatal("ListOrders() expected query error")
	}

	requireQueryContains(t, exec.query, "LIMIT $2 OFFSET $3")
	requireArgLen(t, exec.args, 3)
	if exec.args[1] != 10 || exec.args[2] != 25 {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestListFillsBuildsFilteredRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListFills(context.Background(), trading.FillQuery{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
		Limit:          5,
	})
	if err == nil {
		t.Fatal("ListFills() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM fills")
	requireQueryContains(t, exec.query, "account_id = $1")
	requireQueryContains(t, exec.query, "gateway_order_id = $2")
	requireQueryContains(t, exec.query, "LIMIT $3")
	requireArgLen(t, exec.args, 3)
	if exec.args[2] != 5 {
		t.Fatalf("limit arg = %#v", exec.args[2])
	}
}

func TestListFillsBuildsCursorOffset(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListFills(context.Background(), trading.FillQuery{
		AccountID: "acct-1",
		Cursor:    "50",
		Limit:     20,
	})
	if err == nil {
		t.Fatal("ListFills() expected query error")
	}

	requireQueryContains(t, exec.query, "LIMIT $2 OFFSET $3")
	requireArgLen(t, exec.args, 3)
	if exec.args[1] != 20 || exec.args[2] != 50 {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestListFillsBuildsDateFilteredRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListFills(context.Background(), trading.FillQuery{
		AccountID: "acct-1",
		DateFrom:  "2026-06-12",
		DateTo:    "2026-06-14",
		Limit:     10,
	})
	if err == nil {
		t.Fatal("ListFills() expected query error")
	}

	requireQueryContains(t, exec.query, "trade_date >= $2::date")
	requireQueryContains(t, exec.query, "COALESCE(matched_at, created_at) >= $3")
	requireQueryContains(t, exec.query, "trade_date < $4::date")
	requireQueryContains(t, exec.query, "COALESCE(matched_at, created_at) < $5")
	requireQueryContains(t, exec.query, "LIMIT $6")
	requireArgLen(t, exec.args, 6)
}

func TestGetLatestAssetBuildsRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.GetLatestAsset(context.Background(), "acct-1")
	if err == nil {
		t.Fatal("GetLatestAsset() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM asset_snapshots")
	requireQueryContains(t, exec.query, "WHERE account_id = $1")
	requireQueryContains(t, exec.query, "ORDER BY trade_date DESC")
	requireArgLen(t, exec.args, 1)
	if exec.args[0] != "acct-1" {
		t.Fatalf("account arg = %#v", exec.args[0])
	}
}

func TestGetDailyPerformanceBuildsSnapshotRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.GetDailyPerformance(context.Background(), "acct-1", "20260612")
	if err == nil {
		t.Fatal("GetDailyPerformance() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM asset_snapshots")
	requireQueryContains(t, exec.query, "snapshot_type = 'close'")
	requireQueryContains(t, exec.query, "snapshot_type = 'open'")
	requireQueryContains(t, exec.query, "FROM position_snapshots")
	requireQueryContains(t, exec.query, "FROM fills")
	requireQueryContains(t, exec.query, "previous_asset")
	requireArgLen(t, exec.args, 4)
	if exec.args[0] != "acct-1" || exec.args[1] != "2026-06-12" {
		t.Fatalf("identity args = %#v/%#v", exec.args[0], exec.args[1])
	}
}

func TestGetAssetPositionObservationBuildsSnapshotRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.GetAssetPositionObservation(context.Background(), "acct-1", "20260612", "open")
	if err == nil {
		t.Fatal("GetAssetPositionObservation() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM asset_snapshots")
	requireQueryContains(t, exec.query, "FROM position_snapshots")
	requireQueryContains(t, exec.query, "snapshot_type = $3")
	requireQueryContains(t, exec.query, "sum(market_value)")
	requireArgLen(t, exec.args, 3)
	if exec.args[0] != "acct-1" || exec.args[1] != "2026-06-12" || exec.args[2] != "open" {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestListDailyPerformanceBuildsSeriesRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListDailyPerformance(context.Background(), "acct-1", "20260612", "20260614")
	if err == nil {
		t.Fatal("ListDailyPerformance() expected query error")
	}

	requireQueryContains(t, exec.query, "row_number() OVER")
	requireQueryContains(t, exec.query, "lag(net_asset) OVER")
	requireQueryContains(t, exec.query, "FROM asset_snapshots")
	requireQueryContains(t, exec.query, "open_asset_ranked")
	requireQueryContains(t, exec.query, "FROM position_snapshots")
	requireQueryContains(t, exec.query, "FROM fills")
	requireQueryContains(t, exec.query, "ORDER BY asset.trade_date ASC")
	requireArgLen(t, exec.args, 5)
	if exec.args[0] != "acct-1" || exec.args[1] != "2026-06-12" || exec.args[2] != "2026-06-14" {
		t.Fatalf("identity args = %#v/%#v/%#v", exec.args[0], exec.args[1], exec.args[2])
	}
}

func TestUpsertPerformanceNAVBuildsVersionedWrite(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.UpsertPerformanceNAV(context.Background(), PerformanceNAV{
		AccountID:            "acct-1",
		TradeDate:            "20260724",
		Status:               "provisional",
		FormulaVersion:       "performance_economic_nav.test",
		OpenEconomicNAV:      1000000,
		ExternalNetFlow:      10000,
		AccountDayPnL:        1200,
		SettlementAdjustment: 50,
		CloseEconomicNAV:     1011250,
		ReturnDenominator:    1005000,
		DailyReturn:          0.001194,
		CumulativeNAV:        1.001194,
		PnLComponents:        map[string]any{"unattributed": map[string]any{"pnl": 1200}},
		QualityFlags:         []string{"strategy_attribution_pending"},
		Source:               "unit",
	})
	if err == nil {
		t.Fatal("UpsertPerformanceNAV() expected query error")
	}

	requireQueryContains(t, exec.query, "UPDATE performance_nav_versions")
	requireQueryContains(t, exec.query, "INSERT INTO performance_nav_versions")
	requireQueryContains(t, exec.query, "RETURNING")
	requireArgLen(t, exec.args, 17)
	if exec.args[0] != "acct-1" || exec.args[1] != "2026-07-24" {
		t.Fatalf("identity args = %#v/%#v", exec.args[0], exec.args[1])
	}
	if exec.args[3] != "provisional" || exec.args[4] != "performance_economic_nav.test" {
		t.Fatalf("status/formula args = %#v/%#v", exec.args[3], exec.args[4])
	}
	assertJSONContains(t, exec.args[13], `"unattributed"`)
	assertJSONContains(t, exec.args[14], `"strategy_attribution_pending"`)
}

func TestUpsertPerformanceNAVGoldBuildsVersionedAuditedWrite(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)
	confirmedAt := time.Date(2026, 8, 1, 16, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))

	_, err := repo.UpsertPerformanceNAVGold(context.Background(), PerformanceNAVGold{
		AccountID:        " acct-1 ",
		TradeDate:        "20260729",
		Status:           "confirmed",
		CarriedOpenAsset: 5320996.04,
		CloseAsset:       5329753.88,
		DailyPnL:         8745.97,
		Source:           "manual_user_confirmed",
		SourceRef:        "testdata/performance/manual.csv",
		ConfirmedBy:      "user",
		ConfirmedAt:      confirmedAt,
		RawPayload:       map[string]any{"row_number": 16},
	})
	if err == nil {
		t.Fatal("UpsertPerformanceNAVGold() expected query error")
	}

	requireQueryContains(t, exec.query, "WITH existing_current AS")
	requireQueryContains(t, exec.query, "UPDATE performance_nav_gold_versions")
	requireQueryContains(t, exec.query, "INSERT INTO performance_nav_gold_versions")
	requireQueryContains(t, exec.query, "NOT EXISTS (SELECT 1 FROM existing_current)")
	requireArgLen(t, exec.args, 13)
	if exec.args[0] != "acct-1" || exec.args[1] != "2026-07-29" {
		t.Fatalf("identity args = %#v/%#v", exec.args[0], exec.args[1])
	}
	if exec.args[6] != "excluding_fund_occupancy" || exec.args[7] != "manual_user_confirmed" {
		t.Fatalf("scope/source args = %#v/%#v", exec.args[6], exec.args[7])
	}
	hash, ok := exec.args[9].(string)
	if !ok || len(hash) != 64 {
		t.Fatalf("content hash = %#v", exec.args[9])
	}
	assertJSONContains(t, exec.args[12], `"row_number":16`)
}

func TestUpsertPerformanceNAVGoldRequiresConfirmationAudit(t *testing.T) {
	repo := NewRepository(&recordingQueryExecutor{})

	_, err := repo.UpsertPerformanceNAVGold(context.Background(), PerformanceNAVGold{
		AccountID:        "acct-1",
		TradeDate:        "20260729",
		Status:           "confirmed",
		CarriedOpenAsset: 100,
		CloseAsset:       101,
		DailyPnL:         1,
		SourceRef:        "manual.csv",
	})
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("UpsertPerformanceNAVGold() error = %v, want ErrInvalidLedgerInput", err)
	}
}

func TestListPerformanceNAVGoldBuildsCurrentSeriesRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListPerformanceNAVGold(context.Background(), PerformanceNAVGoldQuery{
		AccountID: "acct-1",
		DateFrom:  "20260701",
		DateTo:    "20260731",
		Status:    "confirmed",
		Source:    "manual_user_confirmed",
	})
	if err == nil {
		t.Fatal("ListPerformanceNAVGold() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM performance_nav_gold_versions")
	requireQueryContains(t, exec.query, "AND ($6 OR is_current)")
	requireQueryContains(t, exec.query, "ORDER BY trade_date, source, version DESC")
	requireArgLen(t, exec.args, 6)
	if exec.args[0] != "acct-1" || exec.args[5] != false {
		t.Fatalf("query args = %#v", exec.args)
	}
}

func TestUpdatePerformanceNAVStatusBuildsCurrentVersionUpdate(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.UpdatePerformanceNAVStatus(context.Background(), PerformanceNAV{
		PerformanceNAVPK:  42,
		AccountID:         "acct-1",
		TradeDate:         "20260724",
		Version:           1,
		Status:            "finalized",
		FormulaVersion:    "performance_economic_nav.test",
		OpenEconomicNAV:   1000000,
		CloseEconomicNAV:  1010000,
		ReturnDenominator: 1000000,
		CumulativeNAV:     1.01,
		PnLComponents:     map[string]any{"review": map[string]any{"operator": "tester"}},
		QualityFlags:      []string{"nav_reconciliation_confirmed"},
		Source:            "unit",
		FinalizedAt:       time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("UpdatePerformanceNAVStatus() expected query error")
	}

	requireQueryContains(t, exec.query, "UPDATE performance_nav_versions")
	requireQueryContains(t, exec.query, "performance_nav_pk = $1")
	requireQueryContains(t, exec.query, "account_id = $2")
	requireQueryContains(t, exec.query, "trade_date = $3::date")
	requireQueryContains(t, exec.query, "AND is_current")
	requireQueryContains(t, exec.query, "RETURNING")
	requireArgLen(t, exec.args, 8)
	if exec.args[0] != int64(42) || exec.args[1] != "acct-1" || exec.args[2] != "2026-07-24" || exec.args[3] != "finalized" {
		t.Fatalf("identity/status args = %#v", exec.args[:4])
	}
	assertJSONContains(t, exec.args[6], `"nav_reconciliation_confirmed"`)
	assertJSONContains(t, exec.args[7], `"review"`)
}

func TestUpsertNAVReconciliationBuildsIdempotentWrite(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.UpsertNAVReconciliation(context.Background(), NAVReconciliation{
		ReconciliationID:            "nav-recon-1",
		PerformanceNAVPK:            42,
		AccountID:                   "acct-1",
		TradeDate:                   "20260724",
		ObservedTradeDate:           "20260725",
		Status:                      "review_required",
		ObservedVisibleCash:         1000,
		ObservedPositionValue:       2000,
		InvisibleCounterCash:        300,
		OutstandingSettlementAssets: 400,
		ObservedOpenAssets:          5000,
		ProvisionalCloseNAV:         3700,
		OvernightExternalNetFlow:    100,
		KnownOvernightIncomeExpense: 20,
		Residual:                    8,
		AutoThreshold:               5,
		WarningThreshold:            50,
		Details:                     map[string]any{"source": "unit"},
	})
	if err == nil {
		t.Fatal("UpsertNAVReconciliation() expected query error")
	}

	requireQueryContains(t, exec.query, "INSERT INTO performance_nav_reconciliations")
	requireQueryContains(t, exec.query, "ON CONFLICT (performance_nav_pk) DO UPDATE")
	requireArgLen(t, exec.args, 20)
	if exec.args[0] != "nav-recon-1" || exec.args[1] != int64(42) || exec.args[3] != "2026-07-24" {
		t.Fatalf("identity args = %#v", exec.args[:4])
	}
	assertJSONContains(t, exec.args[17], `"source":"unit"`)
}

func TestUpsertAssetSnapshotBuildsSnapshotWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	capturedAt := time.Date(2026, 6, 13, 10, 30, 0, 0, time.UTC)

	err := repo.UpsertAssetSnapshot(context.Background(), trading.Asset{
		AccountID:     "acct-1",
		CashAvailable: 900000,
		CashTotal:     1000000,
		NetAsset:      1200000,
		MarketValue:   200000,
	}, "intraday", "query", map[string]any{"source": "front"}, capturedAt)
	if err != nil {
		t.Fatalf("UpsertAssetSnapshot() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO asset_snapshots")
	requireQueryContains(t, exec.query, "ON CONFLICT (trade_date, account_id, snapshot_type)")
	requireArgLen(t, exec.args, 17)
	if exec.args[0] != "2026-06-13" || exec.args[1] != "acct-1" || exec.args[2] != "intraday" {
		t.Fatalf("identity args = %#v %#v %#v", exec.args[0], exec.args[1], exec.args[2])
	}
	assertJSONContains(t, exec.args[15], `"source":"front"`)
}

func TestUpsertAssetSnapshotForDateBuildsBackfillSnapshotWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	capturedAt := time.Date(2026, 6, 14, 16, 30, 0, 0, time.UTC)

	err := repo.UpsertAssetSnapshotForDate(context.Background(), trading.Asset{
		AccountID:     "acct-1",
		CashAvailable: 900000,
		CashTotal:     1000000,
		NetAsset:      1200000,
		MarketValue:   200000,
	}, "20260612", "close", "post_close_settlement", map[string]any{"run_id": "settlement-1"}, capturedAt)
	if err != nil {
		t.Fatalf("UpsertAssetSnapshotForDate() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO asset_snapshots")
	requireArgLen(t, exec.args, 17)
	if exec.args[0] != "2026-06-12" || exec.args[1] != "acct-1" || exec.args[2] != "close" {
		t.Fatalf("identity args = %#v %#v %#v", exec.args[0], exec.args[1], exec.args[2])
	}
	if exec.args[14] != "post_close_settlement" {
		t.Fatalf("source arg = %#v", exec.args[14])
	}
	assertJSONContains(t, exec.args[15], `"run_id":"settlement-1"`)
}

func TestListPositionsBuildsFilteredRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListPositions(context.Background(), trading.PositionQuery{
		AccountID: "acct-1",
		Symbol:    "600000",
		Exchange:  trading.ExchangeSH,
		Limit:     20,
	})
	if err == nil {
		t.Fatal("ListPositions() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM positions")
	requireQueryContains(t, exec.query, "account_id = $1")
	requireQueryContains(t, exec.query, "symbol = $2")
	requireQueryContains(t, exec.query, "exchange = $3")
	requireQueryContains(t, exec.query, "quantity > 0")
	requireQueryContains(t, exec.query, "LIMIT $4")
	requireArgLen(t, exec.args, 4)
	if exec.args[3] != 20 {
		t.Fatalf("limit arg = %#v", exec.args[3])
	}
}

func TestListPositionsBuildsCursorOffset(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListPositions(context.Background(), trading.PositionQuery{
		AccountID: "acct-1",
		Cursor:    "100",
		Limit:     50,
	})
	if err == nil {
		t.Fatal("ListPositions() expected query error")
	}

	requireQueryContains(t, exec.query, "LIMIT $2 OFFSET $3")
	requireArgLen(t, exec.args, 3)
	if exec.args[1] != 50 || exec.args[2] != 100 {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestListPositionSnapshotsBuildsHistoricalRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListPositionSnapshots(context.Background(), trading.PositionQuery{
		AccountID: "acct-1",
		TradeDate: "20260612",
		Limit:     20,
	})
	if err == nil {
		t.Fatal("ListPositionSnapshots() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM position_snapshots")
	requireQueryContains(t, exec.query, "account_id = $1")
	requireQueryContains(t, exec.query, "snapshot_type = $2")
	requireQueryContains(t, exec.query, "trade_date >= $3::date")
	requireQueryContains(t, exec.query, "trade_date < $4::date")
	requireQueryContains(t, exec.query, "LIMIT $5")
	requireArgLen(t, exec.args, 5)
	if exec.args[1] != "close" || exec.args[2] != "2026-06-12" {
		t.Fatalf("snapshot args = %#v", exec.args)
	}
}

func TestListPositionSnapshotsBuildsCursorOffset(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListPositionSnapshots(context.Background(), trading.PositionQuery{
		AccountID: "acct-1",
		TradeDate: "20260612",
		Cursor:    "50",
		Limit:     20,
	})
	if err == nil {
		t.Fatal("ListPositionSnapshots() expected query error")
	}

	requireQueryContains(t, exec.query, "LIMIT $5 OFFSET $6")
	requireArgLen(t, exec.args, 6)
	if exec.args[4] != 20 || exec.args[5] != 50 {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestUpsertPositionBuildsCurrentPositionWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	updatedAt := time.Date(2026, 6, 13, 10, 31, 0, 0, time.UTC)

	err := repo.UpsertPosition(context.Background(), trading.Position{
		AccountID:     "acct-1",
		Symbol:        "600000",
		Exchange:      trading.ExchangeSH,
		Quantity:      100,
		SellableQty:   80,
		AvgCost:       9.54,
		TotalCost:     954,
		AvgCostSource: "broker_total_position_cost",
		CostComplete:  true,
		ShareholderID: "A0001",
	}, "query", map[string]any{"source": "front"}, updatedAt)
	if err != nil {
		t.Fatalf("UpsertPosition() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO positions")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id, symbol, exchange)")
	requireArgLen(t, exec.args, 21)
	if exec.args[0] != "acct-1" || exec.args[1] != "600000" || exec.args[3] != trading.ExchangeSH {
		t.Fatalf("identity args = %#v %#v %#v", exec.args[0], exec.args[1], exec.args[3])
	}
	if exec.args[9] != float64(954) || exec.args[10] != "broker_total_position_cost" || exec.args[11] != true {
		t.Fatalf("cost quality args = %#v", exec.args[9:12])
	}
	assertJSONContains(t, exec.args[19], `"source":"front"`)
}

func TestUpsertPositionPreservesZeroSellableQty(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpsertPosition(context.Background(), trading.Position{
		AccountID:   "acct-1",
		Symbol:      "600000",
		Exchange:    trading.ExchangeSH,
		Quantity:    100,
		SellableQty: 0,
		AvgCost:     9.54,
		MarketValue: 954,
	}, "query", nil, time.Date(2026, 6, 13, 10, 31, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("UpsertPosition() error = %v", err)
	}

	requireArgLen(t, exec.args, 21)
	if exec.args[5] != int64(0) {
		t.Fatalf("sellable_qty arg = %#v, want 0", exec.args[5])
	}
}

func TestDeleteStalePositionsBuildsCleanup(t *testing.T) {
	exec := &recordingExecutor{result: rowsAffectedResult(3)}
	repo := NewRepository(exec)
	cutoff := time.Date(2026, 6, 22, 9, 2, 46, 0, time.UTC)

	deleted, err := repo.DeleteStalePositions(context.Background(), " acct-1 ", cutoff)
	if err != nil {
		t.Fatalf("DeleteStalePositions() error = %v", err)
	}

	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	requireQueryContains(t, exec.query, "DELETE FROM positions")
	requireQueryContains(t, exec.query, "account_id = $1")
	requireQueryContains(t, exec.query, "updated_at < $2")
	requireArgLen(t, exec.args, 2)
	if exec.args[0] != "acct-1" || exec.args[1] != cutoff {
		t.Fatalf("args = %#v", exec.args)
	}
}

func TestUpsertPositionSnapshotBuildsHistoricalWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	capturedAt := time.Date(2026, 6, 14, 16, 5, 0, 0, time.UTC)

	err := repo.UpsertPositionSnapshotWithType(context.Background(), trading.Position{
		AccountID:    "acct-1",
		TradeDate:    "20260612",
		Symbol:       "600000",
		Exchange:     trading.ExchangeSH,
		Quantity:     100,
		SellableQty:  100,
		AvgCost:      9.54,
		TotalCost:    954,
		CostComplete: true,
	}, "close", "post_close_settlement", map[string]any{"source": "settlement"}, capturedAt)
	if err != nil {
		t.Fatalf("UpsertPositionSnapshot() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO position_snapshots")
	requireQueryContains(t, exec.query, "ON CONFLICT (trade_date, account_id, snapshot_type, symbol, exchange)")
	requireArgLen(t, exec.args, 23)
	if exec.args[0] != "2026-06-12" || exec.args[1] != "acct-1" || exec.args[2] != "close" {
		t.Fatalf("snapshot identity args = %#v", exec.args[:3])
	}
	if exec.args[20] != "post_close_settlement" {
		t.Fatalf("source arg = %#v", exec.args[20])
	}
	assertJSONContains(t, exec.args[21], `"source":"settlement"`)
}

func TestSummarizeQueryCommandStatusRequiresSingleCompletedFinalReply(t *testing.T) {
	completed := summarizeQueryCommandStatus(QueryCommandStatus{
		OriginMessageID: "msg-asset-1",
		Action:          "account.asset.query",
		Replies: []QueryReplyStatus{{
			Status:     "completed",
			ResultType: "asset_page",
			IsLast:     true,
		}},
	})
	if !completed.Success || !completed.Terminal || completed.State != "completed" || completed.TerminalCount != 1 {
		t.Fatalf("completed status = %#v", completed)
	}

	contradictory := summarizeQueryCommandStatus(QueryCommandStatus{
		OriginMessageID: "msg-asset-2",
		Action:          "account.asset.query",
		Replies: []QueryReplyStatus{
			{Status: "partial", ResultType: "asset_page"},
			{Status: "failed", ResultType: "error_result", Code: "QUERY_EMPTY_RESULT"},
			{Status: "completed", ResultType: "asset_page", IsLast: true},
		},
	})
	if contradictory.Success || !contradictory.Contradictory || contradictory.State != "invalid" || contradictory.TerminalCount != 2 {
		t.Fatalf("contradictory status = %#v", contradictory)
	}

	invalid := summarizeQueryCommandStatus(QueryCommandStatus{
		OriginMessageID: "msg-position-1",
		Action:          "account.positions.query",
		Replies:         []QueryReplyStatus{{Status: "completed", ResultType: "position_page", IsLast: false}},
	})
	if invalid.Success || invalid.State != "invalid" {
		t.Fatalf("invalid final status = %#v", invalid)
	}

	failedAfterData := summarizeQueryCommandStatus(QueryCommandStatus{
		OriginMessageID: "msg-asset-3",
		Action:          "account.asset.query",
		Replies: []QueryReplyStatus{
			{Status: "partial", ResultType: "asset_page"},
			{Status: "failed", ResultType: "error_result", Code: "QUERY_EMPTY_RESULT"},
		},
	})
	if failedAfterData.Success || failedAfterData.State != "failed" || !failedAfterData.Contradictory {
		t.Fatalf("failed-after-data status = %#v", failedAfterData)
	}
}

func TestUpsertStreamCheckpointBuildsCursorWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	processedAt := time.Date(2026, 6, 14, 3, 20, 0, 0, time.UTC)

	err := repo.UpsertStreamCheckpoint(context.Background(), StreamCheckpoint{
		StreamKey:       "relay:prod:v1:huaxin:g1:event",
		Role:            "event",
		LastStreamID:    "1718340000000-0",
		LastSeenAt:      processedAt,
		LastProcessedAt: processedAt,
		ProcessedCount:  25,
		ErrorCount:      1,
		LastError:       "skipped sample",
		Metadata:        map[string]any{"last_batch_orders": 3},
	})
	if err != nil {
		t.Fatalf("UpsertStreamCheckpoint() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO stream_checkpoints")
	requireQueryContains(t, exec.query, "ON CONFLICT (stream_key)")
	requireArgLen(t, exec.args, 9)
	if exec.args[0] != "relay:prod:v1:huaxin:g1:event" || exec.args[1] != "event" || exec.args[2] != "1718340000000-0" {
		t.Fatalf("checkpoint identity args = %#v %#v %#v", exec.args[0], exec.args[1], exec.args[2])
	}
	if exec.args[6] != int64(25) || exec.args[7] != int64(1) {
		t.Fatalf("checkpoint counts = %#v %#v", exec.args[6], exec.args[7])
	}
	assertJSONContains(t, exec.args[8], `"last_batch_orders":3`)
}

func TestListDeadLettersBuildsFilteredPageQuery(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListDeadLetters(context.Background(), DeadLetterQuery{
		AccountID: "acct-1",
		Status:    "pending",
		Page:      3,
		PageSize:  25,
	})
	if err == nil {
		t.Fatal("ListDeadLetters() expected query error")
	}
	requireQueryContains(t, exec.query, "FROM raw_stream_messages raw")
	requireQueryContains(t, exec.query, "LEFT JOIN LATERAL")
	requireQueryContains(t, exec.query, "LIMIT $3 OFFSET $4")
	requireArgLen(t, exec.args, 4)
	if exec.args[0] != "acct-1" || exec.args[1] != "pending" || exec.args[2] != 25 || exec.args[3] != 50 {
		t.Fatalf("dead letter query args = %#v", exec.args)
	}
}

func TestAddDeadLetterReviewBuildsAuditedInsert(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.AddDeadLetterReview(context.Background(), DeadLetterReview{
		StreamKey: "relay:prod:v1:huaxin:acct-1:dlq",
		StreamID:  "1-0",
		Status:    "acknowledged",
		Operator:  "relay-admin",
		Note:      "validated",
	})
	if err == nil {
		t.Fatal("AddDeadLetterReview() expected query error")
	}
	requireQueryContains(t, exec.query, "INSERT INTO stream_dlq_reviews")
	requireQueryContains(t, exec.query, "WHERE EXISTS")
	requireQueryContains(t, exec.query, "stream_role = 'dlq'")
	requireArgLen(t, exec.args, 5)
	if exec.args[2] != "acknowledged" || exec.args[3] != "relay-admin" {
		t.Fatalf("dead letter review args = %#v", exec.args)
	}
}

func TestUpsertJobRunBuildsStatusWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	repo.now = func() time.Time { return time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC) }

	run, err := repo.UpsertJobRun(context.Background(), JobRun{
		JobName:         "pre_open_init",
		TargetTradeDate: "20260614",
		Status:          "succeeded",
		Report:          map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatalf("UpsertJobRun() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO job_runs")
	requireQueryContains(t, exec.query, "ON CONFLICT (run_id)")
	requireArgLen(t, exec.args, 12)
	if run.RunID == "" || exec.args[2] != "2026-06-14" || exec.args[4] != "succeeded" {
		t.Fatalf("job run = %#v args=%#v", run, exec.args)
	}
	assertJSONContains(t, exec.args[10], `"ok":true`)
}

func TestUpsertJobRunMapsCompletedStatus(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	repo.now = func() time.Time { return time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC) }

	run, err := repo.UpsertJobRun(context.Background(), JobRun{
		JobName:         "post_close_settlement",
		TargetTradeDate: "20260614",
		Status:          "completed",
	})
	if err != nil {
		t.Fatalf("UpsertJobRun() error = %v", err)
	}
	if run.Status != "succeeded" || exec.args[4] != "succeeded" {
		t.Fatalf("job status = %s args=%#v, want succeeded", run.Status, exec.args)
	}
}

func TestLatestJobRunsBuildsDistinctRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.LatestJobRuns(context.Background(), []string{"pre_open_init", "post_close_settlement"})
	if err == nil {
		t.Fatal("LatestJobRuns() expected query error")
	}

	requireQueryContains(t, exec.query, "SELECT DISTINCT ON (job_name)")
	requireQueryContains(t, exec.query, "FROM job_runs")
	requireQueryContains(t, exec.query, "job_name IN ($1, $2)")
	requireQueryContains(t, exec.query, "ORDER BY job_name")
	requireArgLen(t, exec.args, 2)
}

func TestUpsertReconciliationRunBuildsStatusWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	repo.now = func() time.Time { return time.Date(2026, 6, 14, 16, 5, 0, 0, time.UTC) }

	run, err := repo.UpsertReconciliationRun(context.Background(), ReconciliationRun{
		RunID:     "settlement-20260612",
		TradeDate: "20260612",
		Status:    "completed",
		Source:    "post_close_settlement",
		Summary:   map[string]any{"accounts": 1},
	})
	if err != nil {
		t.Fatalf("UpsertReconciliationRun() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO reconciliation_runs")
	requireQueryContains(t, exec.query, "ON CONFLICT (run_id)")
	requireArgLen(t, exec.args, 8)
	if run.TradeDate != "2026-06-12" || exec.args[0] != "settlement-20260612" || exec.args[1] != "2026-06-12" {
		t.Fatalf("run = %#v args=%#v", run, exec.args)
	}
	assertJSONContains(t, exec.args[6], `"accounts":1`)
}

func TestUpsertReconciliationInputBuildsIdempotentWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	repo.now = func() time.Time { return time.Date(2026, 6, 14, 16, 10, 0, 0, time.UTC) }

	err := repo.UpsertReconciliationInput(context.Background(), ReconciliationInput{
		RunID:     "settlement-20260612",
		Source:    "post_close_settlement",
		InputType: "acct-1:relay_ledger_summary",
		Payload:   map[string]any{"orders": 2},
	})
	if err != nil {
		t.Fatalf("UpsertReconciliationInput() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO reconciliation_inputs")
	requireQueryContains(t, exec.query, "ON CONFLICT (run_id, source, input_type)")
	requireArgLen(t, exec.args, 5)
	assertJSONContains(t, exec.args[3], `"orders":2`)
}

func TestUpsertReconciliationBreakBuildsIdempotentWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpsertReconciliationBreak(context.Background(), ReconciliationBreak{
		RunID:      "settlement-20260612",
		AccountID:  "acct-1",
		BreakType:  "non_terminal_order",
		ObjectType: "order",
		ObjectID:   "gw-working",
		InternalPayload: map[string]any{
			"status": "working",
		},
	})
	if err != nil {
		t.Fatalf("UpsertReconciliationBreak() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO reconciliation_breaks")
	requireQueryContains(t, exec.query, "COALESCE(account_id, '')")
	requireArgLen(t, exec.args, 12)
	if exec.args[3] != "warning" || exec.args[4] != "open" {
		t.Fatalf("default state args = %#v/%#v", exec.args[3], exec.args[4])
	}
	assertJSONContains(t, exec.args[7], `"status":"working"`)
}

func TestListReconciliationBreaksBuildsFilteredRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListReconciliationBreaks(context.Background(), ReconciliationBreakQuery{
		RunID:     "settlement-20260612",
		AccountID: "acct-1",
		Status:    "open",
		Limit:     50,
	})
	if err == nil {
		t.Fatal("ListReconciliationBreaks() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM reconciliation_breaks")
	requireQueryContains(t, exec.query, "run_id = $1")
	requireQueryContains(t, exec.query, "account_id = $2")
	requireQueryContains(t, exec.query, "status = $3")
	requireArgLen(t, exec.args, 4)
}

func TestListJobRunsBuildsTradeDateRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.ListJobRuns(context.Background(), JobRunQuery{
		JobNames:  []string{"pre_open_init", "post_close_settlement"},
		TradeDate: "20260731",
		Limit:     20,
	})
	if err == nil {
		t.Fatal("ListJobRuns() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM job_runs")
	requireQueryContains(t, exec.query, "job_name IN ($1, $2)")
	requireQueryContains(t, exec.query, "trade_date = $3::date")
	requireArgLen(t, exec.args, 4)
	if exec.args[2] != "2026-07-31" || exec.args[3] != 20 {
		t.Fatalf("job run args = %#v", exec.args)
	}
}

func TestRawStreamSummaryBuildsWindowRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.RawStreamSummary(context.Background(), "acct-1", time.Unix(1700000000, 0), time.Unix(1700000060, 0))
	if err == nil {
		t.Fatal("RawStreamSummary() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM raw_stream_messages")
	requireQueryContains(t, exec.query, "GROUP BY stream_role")
	requireArgLen(t, exec.args, 3)
}

func TestJobRunJSONOmitZeroTimesAndFormatBusinessTime(t *testing.T) {
	body, err := json.Marshal(JobRun{
		RunID:           "run-1",
		JobName:         "pre_open_init",
		TargetTradeDate: "2026-06-14",
		Status:          "succeeded",
		StartedAt:       time.Date(2026, 6, 14, 0, 20, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("marshal job run: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "0001-01-01") || strings.Contains(text, "finished_at") {
		t.Fatalf("job run json leaked zero time: %s", text)
	}
	if !strings.Contains(text, `"started_at":"2026-06-14T08:20:00+08:00"`) {
		t.Fatalf("job run json missing business time: %s", text)
	}
}

func TestGetStreamCheckpointBuildsRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop after query")}
	repo := NewRepository(exec)

	_, err := repo.GetStreamCheckpoint(context.Background(), "relay:prod:v1:huaxin:g1:reply")
	if err == nil {
		t.Fatal("GetStreamCheckpoint() expected query error")
	}

	requireQueryContains(t, exec.query, "FROM stream_checkpoints")
	requireQueryContains(t, exec.query, "WHERE stream_key = $1")
	requireArgLen(t, exec.args, 1)
	if exec.args[0] != "relay:prod:v1:huaxin:g1:reply" {
		t.Fatalf("stream key arg = %#v", exec.args[0])
	}
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

	_, err = repo.ListOrders(context.Background(), trading.OrderQuery{})
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("ListOrders() error = %v, want ErrInvalidLedgerInput", err)
	}

	_, err = repo.GetLatestAsset(context.Background(), "")
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("GetLatestAsset() error = %v, want ErrInvalidLedgerInput", err)
	}

	err = repo.UpsertStreamCheckpoint(context.Background(), StreamCheckpoint{})
	if !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("UpsertStreamCheckpoint() error = %v, want ErrInvalidLedgerInput", err)
	}
}

type recordingExecutor struct {
	query    string
	args     []any
	queries  []string
	argsList [][]any
	err      error
	result   sql.Result
}

func (exec *recordingExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	exec.query = query
	exec.args = append([]any(nil), args...)
	exec.queries = append(exec.queries, query)
	exec.argsList = append(exec.argsList, append([]any(nil), args...))
	return exec.result, exec.err
}

type rowsAffectedResult int64

func (result rowsAffectedResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (result rowsAffectedResult) RowsAffected() (int64, error) {
	return int64(result), nil
}

type recordingQueryExecutor struct {
	recordingExecutor
	err error
}

func (exec *recordingQueryExecutor) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
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
