package performance

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type qualityBulkStore struct {
	*fakePerformanceStore
	data  ledger.TradeQualityLedger
	err   error
	limit int
}

func TestContributionWaiterSurvivesSupersededQueryCancellation(t *testing.T) {
	store := &blockingPerformanceStore{fakePerformanceStore: &fakePerformanceStore{dailyErr: sql.ErrNoRows}, started: make(chan struct{}), release: make(chan struct{})}
	service, err := New(Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := make(chan error, 1)
	waiter := make(chan error, 1)
	go func() {
		_, err := service.CalculateContributions(ctx, "acct", "20261019")
		owner <- err
	}()
	<-store.started
	go func() {
		_, err := service.CalculateContributions(context.Background(), "acct", "20261019")
		waiter <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled owner returned %v", err)
	}
	select {
	case err := <-waiter:
		if err != nil {
			t.Fatalf("live waiter inherited cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("live waiter did not complete")
	}
	if store.calls.Load() != 2 {
		t.Fatalf("retry must be bounded: calls=%d", store.calls.Load())
	}
}

func (store *qualityBulkStore) ReadTradeQualityLedger(_ context.Context, account, from, to string, limit int) (ledger.TradeQualityLedger, error) {
	store.limit = limit
	return store.data, store.err
}

func TestTradeQualityBulkReaderMatchesLegacyRules(t *testing.T) {
	at := time.Date(2026, 10, 19, 14, 0, 0, 0, timeutil.Location())
	store := &fakePerformanceStore{
		orders:    []trading.Order{{AccountID: "acct", GatewayOrderID: "o", TradeDate: "2026-10-19", Symbol: "600000", Exchange: "SH", OrderQty: 100, CumFilledQty: 100, Status: trading.OrderStatusFilled, GatewayStatus: trading.GatewayStatusFilled, IsTerminal: true, CreatedAt: at, TerminalAt: at}},
		fills:     []trading.Fill{{AccountID: "acct", FillID: "f", GatewayOrderID: "o", TradeDate: "2026-10-19", Symbol: "600000", Exchange: "SH", Qty: 100, Price: 10, MatchedAt: at}},
		orderFees: []ledger.OrderFeeRecord{{GatewayOrderID: "o", TradeDate: "2026-10-19", TotalFee: 5, FeeComplete: true, AssociationComplete: true}},
	}
	legacy, _ := New(Options{Store: store, Now: func() time.Time { return at }})
	want, err := legacy.CalculateTradeQuality(context.Background(), "acct", "20261019", "20261019")
	if err != nil {
		t.Fatal(err)
	}
	bulk := &qualityBulkStore{fakePerformanceStore: store, data: ledger.TradeQualityLedger{Orders: store.orders, Fills: store.fills, Fees: store.orderFees}}
	service, _ := New(Options{Store: bulk, Now: func() time.Time { return at }})
	got, err := service.CalculateTradeQuality(context.Background(), "acct", "20261019", "20261019")
	if err != nil || !reflect.DeepEqual(got, want) || bulk.limit != 200000 {
		t.Fatalf("bulk rules changed: got=%+v want=%+v err=%v", got, want, err)
	}
	bulk.err = errors.New("row limit reached")
	if _, err := service.CalculateTradeQuality(context.Background(), "acct", "20261019", "20261019"); !errors.Is(err, bulk.err) {
		t.Fatal("bulk errors must not fall back to a partial result")
	}
}
