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
	accounts []trading.Account
	orders   []trading.Order
	raw      []ledger.RawStreamMessage
}

func (writer *fakeLedger) UpsertAccount(_ context.Context, account trading.Account) error {
	writer.accounts = append(writer.accounts, account)
	return nil
}

func (writer *fakeLedger) UpsertOrder(_ context.Context, order trading.Order) error {
	writer.orders = append(writer.orders, order)
	return nil
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
