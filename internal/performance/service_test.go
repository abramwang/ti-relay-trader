package performance

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/trading"
)

func TestCalculateReverseRepoAggregatesFillsAndPersists(t *testing.T) {
	store := &fakePerformanceStore{
		fills: []trading.Fill{
			{
				FillID:         "f-actual-a",
				AccountID:      "acct-1",
				GatewayOrderID: "repo-a",
				Symbol:         "204001",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          1.4,
				Qty:            1000,
				Fee:            1.2,
			},
			{
				FillID:         "relay-summary:repo-a",
				AccountID:      "acct-1",
				GatewayOrderID: "repo-a",
				Symbol:         "204001",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          1.4,
				Qty:            1000,
			},
			{
				FillID:         "f-actual-b",
				AccountID:      "acct-1",
				GatewayOrderID: "repo-b",
				Symbol:         "204001",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          1.5,
				Qty:            500,
			},
			{
				FillID:         "stock-sell",
				AccountID:      "acct-1",
				GatewayOrderID: "stock-order",
				Symbol:         "600000",
				Exchange:       trading.ExchangeSH,
				TradeSide:      trading.TradeSideSell,
				Price:          9.2,
				Qty:            100,
			},
		},
		repoRule: ledger.FeeRule{RuleID: "repo-fee", RepoFeeRate: 0.00005},
		now:      time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC),
	}
	service, err := New(Options{
		Store:    store,
		Calendar: weekdayCalendar{},
		Now: func() time.Time {
			return store.now
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateReverseRepo(context.Background(), "acct-1", "20260723", true)
	if err != nil {
		t.Fatalf("CalculateReverseRepo() error = %v", err)
	}

	if result.Orders != 2 || result.Fills != 2 {
		t.Fatalf("counts orders=%d fills=%d, want 2/2", result.Orders, result.Fills)
	}
	if result.FirstSettlement != "2026-07-24" || result.MaturitySettlement != "2026-07-27" || result.OccupationDays != 3 {
		t.Fatalf("settlement = %s/%s days=%d", result.FirstSettlement, result.MaturitySettlement, result.OccupationDays)
	}
	if len(result.Accruals) != 2 || result.Accruals[0].GatewayOrderID != "repo-a" || result.Accruals[1].GatewayOrderID != "repo-b" {
		t.Fatalf("accrual order = %#v", result.Accruals)
	}
	assertClose(t, result.Accruals[0].Principal, 100000)
	assertClose(t, result.Accruals[0].GrossInterest, 11.506849)
	assertClose(t, result.Accruals[0].EffectiveFee, 1.2)
	if result.Accruals[0].FeeSource != "actual_fill" {
		t.Fatalf("repo-a fee_source = %q", result.Accruals[0].FeeSource)
	}
	assertClose(t, result.Accruals[1].Principal, 50000)
	assertClose(t, result.Accruals[1].GrossInterest, 6.164384)
	assertClose(t, result.Accruals[1].EffectiveFee, 2.5)
	if result.Accruals[1].FeeSource != "fee_rule:repo-fee" {
		t.Fatalf("repo-b fee_source = %q", result.Accruals[1].FeeSource)
	}
	if len(store.upserts) != 2 {
		t.Fatalf("persisted accruals = %d, want 2", len(store.upserts))
	}
	if len(store.fillQueries) != 1 || store.fillQueries[0].TradeDate != "2026-07-23" || !store.fillQueries[0].History {
		t.Fatalf("fill query = %#v", store.fillQueries)
	}
}

func TestCalculateReverseRepoMarksMissingFeeRule(t *testing.T) {
	store := &fakePerformanceStore{
		fills: []trading.Fill{{
			FillID:         "f-1",
			AccountID:      "acct-1",
			GatewayOrderID: "repo-a",
			Symbol:         "204001",
			Exchange:       trading.ExchangeSH,
			TradeSide:      trading.TradeSideSell,
			Price:          1.35,
			Qty:            10,
		}},
		repoRuleErr: sql.ErrNoRows,
	}
	service, err := New(Options{Store: store, Calendar: weekdayCalendar{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := service.CalculateReverseRepo(context.Background(), "acct-1", "2026-07-23", false)
	if err != nil {
		t.Fatalf("CalculateReverseRepo() error = %v", err)
	}

	if result.Persisted {
		t.Fatal("Persisted = true, want false")
	}
	if len(result.Accruals) != 1 {
		t.Fatalf("accruals = %d, want 1", len(result.Accruals))
	}
	if result.Accruals[0].FeeSource != "missing" {
		t.Fatalf("fee_source = %q, want missing", result.Accruals[0].FeeSource)
	}
	if len(result.Accruals[0].QualityFlags) != 1 || result.Accruals[0].QualityFlags[0] != "missing_repo_fee" {
		t.Fatalf("quality flags = %#v", result.Accruals[0].QualityFlags)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("upserts = %d, want 0", len(store.upserts))
	}
}

type fakePerformanceStore struct {
	fills       []trading.Fill
	fillQueries []trading.FillQuery
	repoRule    ledger.FeeRule
	repoRuleErr error
	upserts     []ledger.ReverseRepoAccrual
	now         time.Time
}

func (store *fakePerformanceStore) ListFills(_ context.Context, query trading.FillQuery) ([]trading.Fill, error) {
	store.fillQueries = append(store.fillQueries, query)
	return store.fills, nil
}

func (store *fakePerformanceStore) CreateFeeRule(_ context.Context, rule ledger.FeeRule) (ledger.FeeRule, error) {
	return rule, nil
}

func (store *fakePerformanceStore) ListFeeRules(_ context.Context, _ ledger.FeeRuleQuery) ([]ledger.FeeRule, error) {
	return nil, nil
}

func (store *fakePerformanceStore) EffectiveRepoFeeRule(_ context.Context, _, _ string) (ledger.FeeRule, error) {
	if store.repoRuleErr != nil {
		return ledger.FeeRule{}, store.repoRuleErr
	}
	return store.repoRule, nil
}

func (store *fakePerformanceStore) CreateCashLedgerEntry(_ context.Context, entry ledger.CashLedgerEntry) (ledger.CashLedgerEntry, error) {
	return entry, nil
}

func (store *fakePerformanceStore) ListCashLedgerEntries(_ context.Context, _ ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error) {
	return nil, nil
}

func (store *fakePerformanceStore) ConfirmCashLedgerEntry(_ context.Context, _, _, _ string, _ time.Time) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, nil
}

func (store *fakePerformanceStore) VoidCashLedgerEntry(_ context.Context, _, _, _ string, _ time.Time) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, nil
}

func (store *fakePerformanceStore) CreateNavBaseline(_ context.Context, baseline ledger.NavBaseline) (ledger.NavBaseline, error) {
	return baseline, nil
}

func (store *fakePerformanceStore) ListNavBaselines(_ context.Context, _ string) ([]ledger.NavBaseline, error) {
	return nil, nil
}

func (store *fakePerformanceStore) UpsertReverseRepoAccrual(_ context.Context, accrual ledger.ReverseRepoAccrual) error {
	store.upserts = append(store.upserts, accrual)
	return nil
}

func (store *fakePerformanceStore) ListReverseRepoAccruals(_ context.Context, _, _ string) ([]ledger.ReverseRepoAccrual, error) {
	return nil, nil
}

func (store *fakePerformanceStore) ListPerformanceNAVs(_ context.Context, _, _, _ string) ([]ledger.PerformanceNAV, error) {
	return nil, nil
}

func (store *fakePerformanceStore) ListNAVReconciliations(_ context.Context, _, _, _ string) ([]ledger.NAVReconciliation, error) {
	return nil, nil
}

type weekdayCalendar struct{}

func (weekdayCalendar) TradingDayStatus(_ context.Context, date string) (market.TradingDayStatus, error) {
	parsed, err := time.Parse("20060102", date)
	if err != nil {
		return market.TradingDayStatus{}, err
	}
	weekday := parsed.Weekday()
	isTrading := weekday != time.Saturday && weekday != time.Sunday
	return market.TradingDayStatus{
		Date:              parsed.Format("20060102"),
		IsTradingDay:      isTrading,
		IsTradingDayKnown: true,
	}, nil
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("got %.6f, want %.6f", got, want)
	}
}
