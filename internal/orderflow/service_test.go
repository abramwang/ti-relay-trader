package orderflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/trading"
)

func TestSubmitOrderWritesDraftPublishesCommandAndArchives(t *testing.T) {
	cfg := testConfig(true, true)
	ledgerWriter := &fakeLedger{}
	publisher := &fakePublisher{streamID: "1777100000000-0"}
	service, err := New(Options{
		Config:    cfg,
		Ledger:    ledgerWriter,
		Publisher: publisher,
		IDs:       sequenceIDs{"gw-1", "msg-1", "req-unused"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.SubmitOrder(context.Background(), trading.SubmitOrderRequest{
		AccountID:    "acct-1",
		Symbol:       "600000",
		Exchange:     trading.ExchangeSH,
		TradeSide:    trading.TradeSideBuy,
		BusinessType: trading.BusinessTypeStock,
		OffsetType:   trading.OffsetTypeClose,
		Price:        9.54,
		Qty:          100,
	}, SubmitOptions{RequestID: "req-http-1"})
	if err != nil {
		t.Fatalf("SubmitOrder() error = %v", err)
	}

	if result.Order.GatewayOrderID != "gw-1" {
		t.Fatalf("gateway_order_id = %q", result.Order.GatewayOrderID)
	}
	if result.MessageID != "msg-1" || result.RequestID != "req-http-1" {
		t.Fatalf("ids = %s/%s", result.MessageID, result.RequestID)
	}
	if len(ledgerWriter.accounts) != 1 {
		t.Fatalf("accounts = %#v", ledgerWriter.accounts)
	}
	if len(ledgerWriter.orders) != 1 {
		t.Fatalf("orders = %#v", ledgerWriter.orders)
	}
	order := ledgerWriter.orders[0]
	if order.Status != trading.OrderStatusCreated || order.LeavesQty != 100 {
		t.Fatalf("draft order = %#v", order)
	}
	if len(publisher.commands) != 1 {
		t.Fatalf("published commands = %#v", publisher.commands)
	}
	command := publisher.commands[0]
	if command.streamKey != "relay:prod:v1:huaxin:gw-1:cmd.trade" {
		t.Fatalf("stream = %s", command.streamKey)
	}
	if command.envelope.Action != redisstream.ActionOrderSubmit || command.envelope.IdempotencyKey == "" {
		t.Fatalf("envelope = %#v", command.envelope)
	}
	if len(ledgerWriter.raw) != 1 {
		t.Fatalf("raw archives = %#v", ledgerWriter.raw)
	}
	raw := ledgerWriter.raw[0]
	if raw.Direction != "in" || raw.Role != redisstream.SuffixCmdTrade || raw.StreamRef.ID != "1777100000000-0" {
		t.Fatalf("raw archive = %#v", raw)
	}
}

func TestSubmitOrderRejectsTradingDisabled(t *testing.T) {
	service, err := New(Options{
		Config:    testConfig(true, false),
		Ledger:    &fakeLedger{},
		Publisher: &fakePublisher{},
		IDs:       sequenceIDs{"gw-1"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.SubmitOrder(context.Background(), validSubmitRequest(), SubmitOptions{})
	if !errors.Is(err, ErrTradingDisabled) {
		t.Fatalf("SubmitOrder() error = %v, want ErrTradingDisabled", err)
	}
}

func TestSubmitOrderRejectsUnknownAccount(t *testing.T) {
	service, err := New(Options{
		Config:    testConfig(true, true),
		Ledger:    &fakeLedger{},
		Publisher: &fakePublisher{},
		IDs:       sequenceIDs{"gw-1"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := validSubmitRequest()
	req.AccountID = "missing"
	_, err = service.SubmitOrder(context.Background(), req, SubmitOptions{})
	if !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("SubmitOrder() error = %v, want ErrRouteNotFound", err)
	}
}

func TestSubmitOrderRejectsInvalidRequestBeforeWrites(t *testing.T) {
	ledgerWriter := &fakeLedger{}
	publisher := &fakePublisher{}
	service, err := New(Options{
		Config:    testConfig(true, true),
		Ledger:    ledgerWriter,
		Publisher: publisher,
		IDs:       sequenceIDs{"gw-1"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.SubmitOrder(context.Background(), trading.SubmitOrderRequest{}, SubmitOptions{})
	if !errors.Is(err, trading.ErrInvalidSchema) {
		t.Fatalf("SubmitOrder() error = %v, want schema error", err)
	}
	if len(ledgerWriter.orders) != 0 || len(publisher.commands) != 0 {
		t.Fatalf("invalid request should not write: orders=%d commands=%d", len(ledgerWriter.orders), len(publisher.commands))
	}
}

func TestQueryServiceCanStartWithoutPublisher(t *testing.T) {
	ledgerWriter := &fakeLedger{
		listedOrders: []trading.Order{{AccountID: "acct-1", GatewayOrderID: "gateway-1"}},
	}
	service, err := New(Options{
		Config: testConfig(true, true),
		Ledger: ledgerWriter,
		Clock:  fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.ListOrders(context.Background(), trading.OrderQuery{AccountID: "acct-1"})
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("count = %d", result.Count)
	}

	_, err = service.SubmitOrder(context.Background(), validSubmitRequest(), SubmitOptions{})
	if !errors.Is(err, ErrMissingPublisher) {
		t.Fatalf("SubmitOrder() error = %v, want ErrMissingPublisher", err)
	}
}

func TestBatchSubmitOrdersWritesDraftsPublishesCommandAndArchives(t *testing.T) {
	ledgerWriter := &fakeLedger{}
	publisher := &fakePublisher{streamID: "1777100000200-0"}
	service, err := New(Options{
		Config:    testConfig(true, true),
		Ledger:    ledgerWriter,
		Publisher: publisher,
		IDs:       sequenceIDs{"msg-batch-1", "batch-1", "gw-b1", "gw-b2"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.BatchSubmitOrders(context.Background(), trading.BatchSubmitOrderRequest{
		AccountID: "acct-1",
		Orders: []trading.SubmitOrderRequest{
			{
				Symbol:       "600000",
				Exchange:     trading.ExchangeSH,
				TradeSide:    trading.TradeSideBuy,
				BusinessType: trading.BusinessTypeStock,
				OffsetType:   trading.OffsetTypeClose,
				Price:        9.67,
				Qty:          100,
			},
			{
				Symbol:       "000001",
				Exchange:     trading.ExchangeSZ,
				TradeSide:    trading.TradeSideBuy,
				BusinessType: trading.BusinessTypeStock,
				OffsetType:   trading.OffsetTypeClose,
				Price:        11.24,
				Qty:          100,
			},
		},
	}, BatchSubmitOptions{RequestID: "req-http-batch"})
	if err != nil {
		t.Fatalf("BatchSubmitOrders() error = %v", err)
	}

	if result.MessageID != "msg-batch-1" || result.IdempotencyKey != "batch:acct-1:batch-1" {
		t.Fatalf("batch result = %#v", result)
	}
	if len(result.Orders) != 2 || len(ledgerWriter.orders) != 2 {
		t.Fatalf("orders result=%#v ledger=%#v", result.Orders, ledgerWriter.orders)
	}
	if ledgerWriter.orders[0].GatewayOrderID != "gw-b1" || ledgerWriter.orders[1].GatewayOrderID != "gw-b2" {
		t.Fatalf("generated gateway ids = %#v", ledgerWriter.orders)
	}
	if len(publisher.commands) != 1 {
		t.Fatalf("commands = %#v", publisher.commands)
	}
	command := publisher.commands[0]
	if command.envelope.Action != redisstream.ActionOrderBatchSubmit {
		t.Fatalf("action = %s", command.envelope.Action)
	}
	if len(ledgerWriter.raw) != 1 || ledgerWriter.raw[0].Action != redisstream.ActionOrderBatchSubmit {
		t.Fatalf("raw archive = %#v", ledgerWriter.raw)
	}
}

func TestCancelOrderPublishesCommandAndArchives(t *testing.T) {
	cfg := testConfig(true, true)
	ledgerWriter := &fakeLedger{
		order: trading.Order{
			AccountID:      "acct-1",
			GatewayOrderID: "gateway-1",
			Symbol:         "600000",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideBuy,
			BusinessType:   trading.BusinessTypeStock,
			LimitPrice:     9.54,
			OrderQty:       100,
			LeavesQty:      100,
			Status:         trading.OrderStatusWorking,
			GatewayStatus:  trading.GatewayStatusWorking,
		},
	}
	publisher := &fakePublisher{streamID: "1777100000100-0"}
	service, err := New(Options{
		Config:    cfg,
		Ledger:    ledgerWriter,
		Publisher: publisher,
		IDs:       sequenceIDs{"cancel-1", "msg-cancel-1", "req-unused"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CancelOrder(context.Background(), trading.CancelOrderRequest{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
	}, CancelOptions{RequestID: "req-http-cancel"})
	if err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}

	if result.CancelID != "cancel-1" || result.MessageID != "msg-cancel-1" {
		t.Fatalf("cancel ids = %#v", result)
	}
	if result.StreamKey != "relay:prod:v1:huaxin:gw-1:cmd.trade" {
		t.Fatalf("stream = %s", result.StreamKey)
	}
	if len(publisher.commands) != 1 {
		t.Fatalf("commands = %#v", publisher.commands)
	}
	command := publisher.commands[0]
	if command.envelope.Action != redisstream.ActionOrderCancel {
		t.Fatalf("action = %s", command.envelope.Action)
	}
	if len(ledgerWriter.raw) != 1 {
		t.Fatalf("raw archive = %#v", ledgerWriter.raw)
	}
	raw := ledgerWriter.raw[0]
	if raw.Action != redisstream.ActionOrderCancel || raw.GatewayOrderID != "gateway-1" {
		t.Fatalf("raw = %#v", raw)
	}
}

func TestCancelOrderRejectsTerminalOrder(t *testing.T) {
	ledgerWriter := &fakeLedger{
		order: trading.Order{
			AccountID:      "acct-1",
			GatewayOrderID: "gateway-1",
			OrderQty:       100,
			Status:         trading.OrderStatusFilled,
			IsTerminal:     true,
		},
	}
	publisher := &fakePublisher{}
	service, err := New(Options{
		Config:    testConfig(true, true),
		Ledger:    ledgerWriter,
		Publisher: publisher,
		IDs:       sequenceIDs{"cancel-1"},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.CancelOrder(context.Background(), trading.CancelOrderRequest{
		AccountID:      "acct-1",
		GatewayOrderID: "gateway-1",
	}, CancelOptions{})
	if !errors.Is(err, ErrOrderTerminalNotCancelable) {
		t.Fatalf("CancelOrder() error = %v, want terminal error", err)
	}
	if len(publisher.commands) != 0 {
		t.Fatalf("terminal order should not publish commands: %#v", publisher.commands)
	}
}

func TestListOrdersNormalizesQueryLimit(t *testing.T) {
	ledgerWriter := &fakeLedger{
		listedOrders: []trading.Order{{AccountID: "acct-1", GatewayOrderID: "gateway-1"}},
	}
	service, err := New(Options{
		Config:    testConfig(true, true),
		Ledger:    ledgerWriter,
		Publisher: &fakePublisher{},
		Clock:     fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.ListOrders(context.Background(), trading.OrderQuery{
		AccountID: "acct-1",
		Limit:     1000,
	})
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if result.Query.Limit != 500 || ledgerWriter.lastOrderQuery.Limit != 500 {
		t.Fatalf("limit = result %d ledger %d", result.Query.Limit, ledgerWriter.lastOrderQuery.Limit)
	}
	if result.Count != 1 {
		t.Fatalf("count = %d", result.Count)
	}
}

func TestGetAssetAndListPositionsUseConfiguredAccount(t *testing.T) {
	ledgerWriter := &fakeLedger{
		asset: trading.Asset{
			AccountID:     "acct-1",
			CashTotal:     1000000,
			CashAvailable: 900000,
		},
		listedPositions: []trading.Position{
			{AccountID: "acct-1", Symbol: "600000", Exchange: trading.ExchangeSH, Quantity: 100},
		},
	}
	service, err := New(Options{
		Config: testConfig(true, false),
		Ledger: ledgerWriter,
		Clock:  fixedClock{t: time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	assetResult, err := service.GetAsset(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("GetAsset() error = %v", err)
	}
	if assetResult.Asset.CashAvailable != 900000 {
		t.Fatalf("asset = %#v", assetResult.Asset)
	}

	positionResult, err := service.ListPositions(context.Background(), trading.PositionQuery{
		AccountID: "acct-1",
		Limit:     5000,
	})
	if err != nil {
		t.Fatalf("ListPositions() error = %v", err)
	}
	if positionResult.Query.Limit != 2000 || ledgerWriter.lastPositionQuery.Limit != 2000 {
		t.Fatalf("limit = result %d ledger %d", positionResult.Query.Limit, ledgerWriter.lastPositionQuery.Limit)
	}
	if positionResult.Count != 1 {
		t.Fatalf("count = %d", positionResult.Count)
	}
}

func validSubmitRequest() trading.SubmitOrderRequest {
	return trading.SubmitOrderRequest{
		AccountID:    "acct-1",
		Symbol:       "600000",
		Exchange:     trading.ExchangeSH,
		TradeSide:    trading.TradeSideBuy,
		BusinessType: trading.BusinessTypeStock,
		OffsetType:   trading.OffsetTypeClose,
		Price:        9.54,
		Qty:          100,
	}
}

func testConfig(enabled, tradingEnabled bool) config.Config {
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{
		{
			AccountID:      "acct-1",
			BrokerID:       "huaxin",
			GatewayID:      "gw-1",
			StreamPrefix:   "relay:prod:v1:huaxin:gw-1",
			Enabled:        enabled,
			TradingEnabled: tradingEnabled,
		},
	}
	return cfg
}

type fakeLedger struct {
	accounts          []trading.Account
	orders            []trading.Order
	order             trading.Order
	getOrderErr       error
	listedOrders      []trading.Order
	listedFills       []trading.Fill
	asset             trading.Asset
	assetErr          error
	listedPositions   []trading.Position
	lastOrderQuery    trading.OrderQuery
	lastFillQuery     trading.FillQuery
	lastPositionQuery trading.PositionQuery
	raw               []ledger.RawStreamMessage
}

func (writer *fakeLedger) UpsertAccount(_ context.Context, account trading.Account) error {
	writer.accounts = append(writer.accounts, account)
	return nil
}

func (writer *fakeLedger) UpsertOrder(_ context.Context, order trading.Order) error {
	writer.orders = append(writer.orders, order)
	return nil
}

func (writer *fakeLedger) GetOrder(_ context.Context, accountID string, gatewayOrderID string) (trading.Order, error) {
	if writer.getOrderErr != nil {
		return trading.Order{}, writer.getOrderErr
	}
	if writer.order.AccountID == accountID && writer.order.GatewayOrderID == gatewayOrderID {
		return writer.order, nil
	}
	return trading.Order{}, ledger.ErrOrderNotFound
}

func (writer *fakeLedger) ListOrders(_ context.Context, query trading.OrderQuery) ([]trading.Order, error) {
	writer.lastOrderQuery = query
	return writer.listedOrders, nil
}

func (writer *fakeLedger) ListFills(_ context.Context, query trading.FillQuery) ([]trading.Fill, error) {
	writer.lastFillQuery = query
	return writer.listedFills, nil
}

func (writer *fakeLedger) GetLatestAsset(_ context.Context, accountID string) (trading.Asset, error) {
	if writer.assetErr != nil {
		return trading.Asset{}, writer.assetErr
	}
	if writer.asset.AccountID == accountID {
		return writer.asset, nil
	}
	return trading.Asset{}, ledger.ErrAssetNotFound
}

func (writer *fakeLedger) ListPositions(_ context.Context, query trading.PositionQuery) ([]trading.Position, error) {
	writer.lastPositionQuery = query
	return writer.listedPositions, nil
}

func (writer *fakeLedger) ArchiveRawStreamMessage(_ context.Context, message ledger.RawStreamMessage) error {
	writer.raw = append(writer.raw, message)
	return nil
}

type fakePublisher struct {
	streamID string
	commands []publishedCommand
}

type publishedCommand struct {
	streamKey string
	envelope  redisstream.CommandEnvelope
}

func (publisher *fakePublisher) PublishCommand(_ context.Context, streamKey string, envelope redisstream.CommandEnvelope) (redisstream.CommandPublishResult, error) {
	publisher.commands = append(publisher.commands, publishedCommand{streamKey: streamKey, envelope: envelope})
	streamID := publisher.streamID
	if streamID == "" {
		streamID = "1-0"
	}
	return redisstream.CommandPublishResult{
		StreamKey: streamKey,
		StreamID:  streamID,
		BodyBytes: 128,
	}, nil
}

type sequenceIDs []string

func (ids sequenceIDs) NewID(prefix string) string {
	if len(ids) == 0 {
		return prefix + "-fallback"
	}
	value := ids[0]
	copy(ids, ids[1:])
	return value
}

type fixedClock struct {
	t time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.t
}
