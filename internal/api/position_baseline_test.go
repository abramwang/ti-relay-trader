package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/trading"
)

type fakeBaselineProvider struct {
	*fakeSettlementStore
	query  ledger.PositionBaselineQuery
	err    error
	called bool
}

type pagedBaselinePositions struct {
	*fakeSettlementStore
	count     int
	calls     int
	failAt    int
	duplicate bool
}

func (store *pagedBaselinePositions) ListPositionSnapshots(_ context.Context, q trading.PositionQuery) ([]trading.Position, error) {
	store.calls++
	if store.failAt == store.calls {
		return nil, fmt.Errorf("source page unavailable")
	}
	offset, _ := strconv.Atoi(q.Cursor)
	rows := []trading.Position{}
	for i := offset; i < store.count && len(rows) < q.Limit; i++ {
		symbol := fmt.Sprintf("%06d", i)
		if store.duplicate && offset > 0 && i == offset {
			symbol = "000000"
		}
		rows = append(rows, trading.Position{AccountID: "a", Symbol: symbol, Exchange: "SH", Quantity: 100})
	}
	return rows, nil
}

type pagedBaselineCurrentPositions struct {
	*fakeOrderSubmitter
	pages *pagedBaselinePositions
}

func (store *pagedBaselineCurrentPositions) ListPositions(ctx context.Context, q trading.PositionQuery) (orderflow.ListPositionsResult, error) {
	rows, err := store.pages.ListPositionSnapshots(ctx, q)
	result := orderflow.ListPositionsResult{Positions: rows}
	if len(rows) == q.Limit {
		offset, _ := strconv.Atoi(q.Cursor)
		result.NextCursor = strconv.Itoa(offset + q.Limit)
	}
	return result, err
}

func TestSettlementPositionCollectionDoesNotTruncate(t *testing.T) {
	for _, history := range []bool{false, true} {
		for _, test := range []struct {
			count, failAt        int
			duplicate, wantError bool
		}{
			{0, 0, false, false}, {500, 0, false, false}, {2001, 0, false, false},
			{10001, 0, false, false}, {100001, 0, false, true},
			{10001, 2, false, true}, {10001, 0, true, true},
		} {
			t.Run(fmt.Sprintf("history=%v/count=%d/fail=%d/duplicate=%v", history, test.count, test.failAt, test.duplicate), func(t *testing.T) {
				store := &pagedBaselinePositions{fakeSettlementStore: &fakeSettlementStore{}, count: test.count, failAt: test.failAt, duplicate: test.duplicate}
				s := &Server{settles: store, orders: &pagedBaselineCurrentPositions{fakeOrderSubmitter: &fakeOrderSubmitter{}, pages: store}}
				result, err := s.listAllSettlementPositions(context.Background(), trading.PositionQuery{AccountID: "a", TradeDate: "20261009", History: history, SnapshotType: "broker_close"})
				if (err != nil) != test.wantError || (!test.wantError && len(result.Positions) != test.count) {
					t.Fatalf("collection count=%d want=%d err=%v", len(result.Positions), test.count, err)
				}
			})
		}
	}
}

func (store *fakeBaselineProvider) GetPositionBaseline(_ context.Context, q ledger.PositionBaselineQuery) (ledger.PositionBaselinePage, error) {
	store.query = q
	store.called = true
	return ledger.PositionBaselinePage{Positions: []ledger.PositionBaselineRow{}}, store.err
}

func TestPositionBaselineHTTPReadOnlyContract(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.AccountRouteConfig{{AccountID: "a", BrokerID: "huaxin", GatewayID: "g", Enabled: false}}
	store := &fakeBaselineProvider{fakeSettlementStore: &fakeSettlementStore{}}
	handler := NewWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{Settlements: store})
	for _, suffix := range []string{"", "?trade_date=20261009&limit=501", "?trade_date=20261009&snapshot_type=open", "?trade_date=20261009&snapshot_type=current", "?trade_date=20261009&symbol=600000", "?trade_date=20261009&limit=1&limit=2"} {
		store.called = false
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/accounts/a/positions/baseline"+suffix, nil))
		if rec.Code != http.StatusBadRequest || store.called {
			t.Fatalf("invalid query %s: %d called=%v", suffix, rec.Code, store.called)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/accounts/a/positions/baseline", strings.NewReader("{}")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatal("baseline accepts writes")
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/accounts/a/positions/baseline?trade_date=20261009&limit=2", nil))
	if rec.Code != 200 || store.query.SnapshotType != "close" || store.query.Limit != 2 || store.query.BrokerID != "huaxin" || store.query.GatewayID != "g" {
		t.Fatalf("history-only disabled account read failed: %d %+v", rec.Code, store.query)
	}
	for _, test := range []struct {
		err  error
		code int
	}{{ledger.ErrPositionBaselineChanged, 409}, {ledger.ErrPositionBaselineTooLarge, 422}} {
		store.err = test.err
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/accounts/a/positions/baseline?trade_date=20261009", nil))
		if rec.Code != test.code {
			t.Fatalf("error mapping: %d want %d", rec.Code, test.code)
		}
	}
}
