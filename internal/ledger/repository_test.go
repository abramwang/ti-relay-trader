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
	requireQueryContains(t, exec.query, "created_at = CASE WHEN EXCLUDED.raw_payload ? 'created_at' THEN EXCLUDED.created_at ELSE orders.created_at END")
	requireQueryContains(t, exec.query, "cum_filled_qty = CASE WHEN EXCLUDED.is_terminal = TRUE THEN EXCLUDED.cum_filled_qty ELSE GREATEST(orders.cum_filled_qty, EXCLUDED.cum_filled_qty) END")
	requireQueryContains(t, exec.query, "status = CASE WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.status ELSE EXCLUDED.status END")
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

func TestUpsertOrderInfersFilledFromExecutionQuantities(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)

	err := repo.UpsertOrder(context.Background(), trading.Order{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-filled",
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

	requireArgLen(t, exec.args, 38)
	if exec.args[20] != trading.OrderStatusFilled || exec.args[21] != trading.GatewayStatusFilled || exec.args[24] != true {
		t.Fatalf("state args = %#v/%#v terminal=%#v, want filled/filled true", exec.args[20], exec.args[21], exec.args[24])
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

	requireQueryContains(t, exec.query, "cum_filled_qty = CASE WHEN $14 = TRUE THEN $6 ELSE GREATEST(cum_filled_qty, $6) END")
	requireArgLen(t, exec.args, 19)
	if exec.args[5] != int64(60) || exec.args[13] != true {
		t.Fatalf("cum/terminal args = %#v/%#v", exec.args[5], exec.args[13])
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
	requireQueryContains(t, exec.query, "status = CASE WHEN is_terminal = TRUE AND $14 = FALSE THEN status ELSE $12 END")
	requireArgLen(t, exec.args, 19)
	if exec.args[0] != "acct-1" || exec.args[1] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.args[0], exec.args[1])
	}
	assertJSONContains(t, exec.args[18], `"order_status_name":"queued"`)
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
			OrderQty:       100,
			CumFilledQty:   100,
			LeavesQty:      0,
		},
	})
	if err != nil {
		t.Fatalf("UpdateOrderStatus() error = %v", err)
	}

	requireArgLen(t, exec.args, 19)
	if exec.args[11] != trading.OrderStatusFilled || exec.args[12] != trading.GatewayStatusFilled || exec.args[13] != true {
		t.Fatalf("state args = %#v/%#v terminal=%#v, want filled/filled true", exec.args[11], exec.args[12], exec.args[13])
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

	if len(exec.queries) != 2 {
		t.Fatalf("query count = %d, want insert + summary cleanup", len(exec.queries))
	}
	requireQueryContains(t, exec.queries[0], "INSERT INTO fills")
	requireQueryContains(t, exec.queries[0], "ON CONFLICT DO NOTHING")
	requireArgLen(t, exec.argsList[0], 22)
	if exec.argsList[0][0] != "acct-1" || exec.argsList[0][2] != "gateway-1" {
		t.Fatalf("identity args = %#v %#v", exec.argsList[0][0], exec.argsList[0][2])
	}
	assertJSONContains(t, exec.argsList[0][20], `"fill_id":"fill-1"`)
	assertJSONContains(t, exec.argsList[0][21], `"match_type":"counter"`)
	requireQueryContains(t, exec.queries[1], "DELETE FROM fills")
	requireArgLen(t, exec.argsList[1], 3)
	if exec.argsList[1][0] != "acct-1" || exec.argsList[1][1] != "gateway-1" {
		t.Fatalf("summary cleanup args = %#v", exec.argsList[1])
	}
	if orderStream, ok := exec.argsList[1][2].(sql.NullString); !ok || !orderStream.Valid || orderStream.String != "order-stream-1" {
		t.Fatalf("summary cleanup order stream arg = %#v", exec.argsList[1][2])
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

	requireQueryContains(t, exec.query, "created_at >= $2")
	requireQueryContains(t, exec.query, "created_at < $3")
	requireQueryContains(t, exec.query, "LIMIT $4")
	requireArgLen(t, exec.args, 4)
	if exec.args[0] != "acct-1" || exec.args[3] != 10 {
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
	requireQueryContains(t, exec.query, "trade_date >= $2::date")
	requireQueryContains(t, exec.query, "trade_date < $3::date")
	requireQueryContains(t, exec.query, "LIMIT $4")
	requireArgLen(t, exec.args, 4)
	if exec.args[1] != "2026-06-12" {
		t.Fatalf("trade date arg = %#v", exec.args[1])
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

	requireQueryContains(t, exec.query, "LIMIT $4 OFFSET $5")
	requireArgLen(t, exec.args, 5)
	if exec.args[3] != 20 || exec.args[4] != 50 {
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
		MarketValue:   954,
		ShareholderID: "A0001",
	}, "query", map[string]any{"source": "front"}, updatedAt)
	if err != nil {
		t.Fatalf("UpsertPosition() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO positions")
	requireQueryContains(t, exec.query, "ON CONFLICT (account_id, symbol, exchange)")
	requireArgLen(t, exec.args, 17)
	if exec.args[0] != "acct-1" || exec.args[1] != "600000" || exec.args[3] != trading.ExchangeSH {
		t.Fatalf("identity args = %#v %#v %#v", exec.args[0], exec.args[1], exec.args[3])
	}
	assertJSONContains(t, exec.args[15], `"source":"front"`)
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

	requireArgLen(t, exec.args, 17)
	if exec.args[5] != int64(0) {
		t.Fatalf("sellable_qty arg = %#v, want 0", exec.args[5])
	}
}

func TestUpsertPositionSnapshotBuildsHistoricalWrite(t *testing.T) {
	exec := &recordingExecutor{}
	repo := NewRepository(exec)
	capturedAt := time.Date(2026, 6, 14, 16, 5, 0, 0, time.UTC)

	err := repo.UpsertPositionSnapshot(context.Background(), trading.Position{
		AccountID:   "acct-1",
		TradeDate:   "20260612",
		Symbol:      "600000",
		Exchange:    trading.ExchangeSH,
		Quantity:    100,
		SellableQty: 100,
		AvgCost:     9.54,
		MarketValue: 954,
	}, "close", map[string]any{"source": "settlement"}, capturedAt)
	if err != nil {
		t.Fatalf("UpsertPositionSnapshot() error = %v", err)
	}

	requireQueryContains(t, exec.query, "INSERT INTO position_snapshots")
	requireQueryContains(t, exec.query, "ON CONFLICT (trade_date, account_id, symbol, exchange)")
	requireArgLen(t, exec.args, 18)
	if exec.args[0] != "2026-06-12" || exec.args[1] != "acct-1" {
		t.Fatalf("snapshot identity args = %#v %#v", exec.args[0], exec.args[1])
	}
	assertJSONContains(t, exec.args[16], `"source":"settlement"`)
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
