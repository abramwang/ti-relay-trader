package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
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
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM performance_etf_settlement_versions WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(cleanupCtx, "DELETE FROM performance_position_cost_states WHERE account_id = $1", accountID)
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

	costState, err := repo.UpsertPositionCostState(ctx, PositionCostState{
		AccountID:                    accountID,
		TradeDate:                    "2026-06-13",
		Symbol:                       "588200",
		Exchange:                     "SH",
		CostBucket:                   "CORE",
		Status:                       "calculated",
		FormulaVersion:               "performance_position_cost.v2",
		PreviousCloseQuantity:        100,
		BrokerOpenQuantity:           300,
		OpenQuantity:                 300,
		OpenTotalCost:                1000,
		CloseQuantity:                300,
		CloseTotalCost:               1000,
		AverageCost:                  3.333333,
		BrokerCloseQuantity:          300,
		ClosePrice:                   0.41,
		CloseMarketValue:             123,
		UnrealizedPnL:                -877,
		CorporateActionType:          "quantity_adjustment",
		CorporateActionFactor:        3,
		CorporateActionQuantityDelta: 200,
		CorporateActionSource:        "mysql_ti_db",
		CorporateActionContext: map[string]any{
			"security_id": "588200.SH",
			"ex_date":     20260613,
			"ex_factor":   3,
		},
		QualityFlags: []string{"corporate_action_quantity_adjusted"},
	})
	if err != nil {
		t.Fatalf("UpsertPositionCostState() error = %v", err)
	}
	if costState.CorporateActionType != "quantity_adjustment" || costState.CorporateActionFactor != 3 || costState.BrokerOpenQuantity != 300 {
		t.Fatalf("saved position cost corporate action = %#v", costState)
	}
	costStates, err := repo.ListPositionCostStates(ctx, PositionCostStateQuery{AccountID: accountID, TradeDate: "20260613"})
	if err != nil || len(costStates) != 1 || costStates[0].CorporateActionQuantityDelta != 200 {
		t.Fatalf("ListPositionCostStates() states/error = %#v/%v", costStates, err)
	}
	if _, err := repo.UpsertPositionCostState(ctx, PositionCostState{
		AccountID:      accountID,
		TradeDate:      "2026-06-13",
		Symbol:         "588200",
		Exchange:       "SH",
		CostBucket:     "ETF_T0:basket-1",
		Status:         "calculated",
		FormulaVersion: "performance_position_cost.v3",
		BuyQuantity:    1000,
		BuyAmount:      1200,
		SellQuantity:   1000,
		FeeSource:      "included_in_etf_t0_friction",
		OpeningSource:  "explicit_t0_order_group",
		QualityFlags:   []string{"etf_t0_cost_separated", "etf_t0_redemption_cost_released"},
	}); err != nil {
		t.Fatalf("UpsertPositionCostState() T0 bucket error = %v", err)
	}
	costStates, err = repo.ListPositionCostStates(ctx, PositionCostStateQuery{AccountID: accountID, TradeDate: "20260613"})
	if err != nil || len(costStates) != 2 || costStates[0].CostBucket != "CORE" || costStates[1].CostBucket != "ETF_T0:basket-1" {
		t.Fatalf("ListPositionCostStates() cost buckets/error = %#v/%v", costStates, err)
	}

	pendingSettlement, err := repo.UpsertETFSettlementFinalization(ctx, ETFSettlementFinalization{
		AccountID:                  accountID,
		SourceTradeDate:            "2026-06-13",
		SecurityID:                 "588200.SH",
		Status:                     "pending",
		RedemptionQuantity:         1000,
		RedemptionUnit:             500,
		BuyGrossAmount:             1000,
		ComponentSaleGrossAmount:   900,
		ActualCashComponent:        10,
		ActualTotalFee:             5,
		SourceCloseSettlementCarry: -50,
		GrossContribution:          -90,
		NetContribution:            -95,
		Source:                     "integration-test",
		RawPayload:                 map[string]any{"stage": "pending"},
	})
	if err != nil {
		t.Fatalf("UpsertETFSettlementFinalization() pending error = %v", err)
	}
	if pendingSettlement.Version != 1 || !pendingSettlement.IsCurrent {
		t.Fatalf("pending ETF settlement = %#v", pendingSettlement)
	}
	confirmedAt := time.Now().UTC()
	confirmedSettlement, err := repo.UpsertETFSettlementFinalization(ctx, ETFSettlementFinalization{
		AccountID:                  accountID,
		SourceTradeDate:            "2026-06-13",
		SecurityID:                 "588200.SH",
		Status:                     "confirmed",
		SettlementComplete:         true,
		RedemptionQuantity:         1000,
		RedemptionUnit:             500,
		BuyGrossAmount:             1000,
		ComponentSaleGrossAmount:   900,
		ActualCashComponent:        20,
		ActualTotalFee:             5,
		SourceCloseSettlementCarry: -50,
		GrossContribution:          -80,
		NetContribution:            -85,
		PCFTradeDate:               "2026-06-16",
		PCFSchemaVersion:           "etf_cash_component.v1",
		Source:                     "integration-test",
		ConfirmedBy:                "ledger-integration-test",
		ConfirmedAt:                confirmedAt,
		RawPayload:                 map[string]any{"stage": "confirmed"},
	})
	if err != nil {
		t.Fatalf("UpsertETFSettlementFinalization() confirmed error = %v", err)
	}
	if confirmedSettlement.Version != 2 || !confirmedSettlement.IsCurrent || confirmedSettlement.Status != "confirmed" {
		t.Fatalf("confirmed ETF settlement = %#v", confirmedSettlement)
	}
	settlements, err := repo.ListETFSettlementFinalizations(ctx, accountID, "20260613")
	if err != nil || len(settlements) != 1 || settlements[0].Version != 2 || settlements[0].ActualCashComponent != 20 {
		t.Fatalf("ListETFSettlementFinalizations() settlements/error = %#v/%v", settlements, err)
	}
	var settlementVersions, currentSettlements int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE is_current)
		FROM performance_etf_settlement_versions
		WHERE account_id = $1 AND source_trade_date = $2::date AND security_id = $3
	`, accountID, "2026-06-13", "588200.SH").Scan(&settlementVersions, &currentSettlements); err != nil {
		t.Fatalf("query ETF settlement versions: %v", err)
	}
	if settlementVersions != 2 || currentSettlements != 1 {
		t.Fatalf("ETF settlement versions/current = %d/%d, want 2/1", settlementVersions, currentSettlements)
	}
	concurrentSettlements := []ETFSettlementFinalization{
		{
			AccountID: accountID, SourceTradeDate: "2026-06-13", SecurityID: "588200.SH",
			Status: "confirmed", SettlementComplete: true, RedemptionQuantity: 1000, RedemptionUnit: 500,
			BuyGrossAmount: 1000, ComponentSaleGrossAmount: 900, ActualCashComponent: 30,
			ActualTotalFee: 5, SourceCloseSettlementCarry: -50, GrossContribution: -70, NetContribution: -75,
			PCFTradeDate: "2026-06-16", PCFSchemaVersion: "etf_cash_component.v1",
			Source: "integration-test", ConfirmedBy: "concurrent-a", ConfirmedAt: confirmedAt,
		},
		{
			AccountID: accountID, SourceTradeDate: "2026-06-13", SecurityID: "588200.SH",
			Status: "confirmed", SettlementComplete: true, RedemptionQuantity: 1000, RedemptionUnit: 500,
			BuyGrossAmount: 1000, ComponentSaleGrossAmount: 900, ActualCashComponent: 40,
			ActualTotalFee: 5, SourceCloseSettlementCarry: -50, GrossContribution: -60, NetContribution: -65,
			PCFTradeDate: "2026-06-16", PCFSchemaVersion: "etf_cash_component.v1",
			Source: "integration-test", ConfirmedBy: "concurrent-b", ConfirmedAt: confirmedAt,
		},
	}
	var settlementWG sync.WaitGroup
	settlementErrors := make(chan error, len(concurrentSettlements))
	for _, update := range concurrentSettlements {
		settlementWG.Add(1)
		go func(item ETFSettlementFinalization) {
			defer settlementWG.Done()
			_, err := repo.UpsertETFSettlementFinalization(ctx, item)
			settlementErrors <- err
		}(update)
	}
	settlementWG.Wait()
	close(settlementErrors)
	for err := range settlementErrors {
		if err != nil {
			t.Fatalf("concurrent UpsertETFSettlementFinalization() error = %v", err)
		}
	}
	var maxSettlementVersion int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE is_current), max(version)
		FROM performance_etf_settlement_versions
		WHERE account_id = $1 AND source_trade_date = $2::date AND security_id = $3
	`, accountID, "2026-06-13", "588200.SH").Scan(&settlementVersions, &currentSettlements, &maxSettlementVersion); err != nil {
		t.Fatalf("query concurrent ETF settlement versions: %v", err)
	}
	if settlementVersions != 4 || currentSettlements != 1 || maxSettlementVersion != 4 {
		t.Fatalf("concurrent ETF settlement versions/current/max = %d/%d/%d, want 4/1/4", settlementVersions, currentSettlements, maxSettlementVersion)
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
