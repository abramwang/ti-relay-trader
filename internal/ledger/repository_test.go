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
	query string
	args  []any
	err   error
}

func (exec *recordingExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	exec.query = query
	exec.args = append([]any(nil), args...)
	return nil, exec.err
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
