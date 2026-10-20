package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTradeQualityBulkReadDoesNotUseOffsetPages(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop")}
	repo := NewRepository(exec)
	if _, err := repo.ReadTradeQualityLedger(context.Background(), "acct", "20261001", "20261019", 200000); err == nil {
		t.Fatal("expected query error")
	}
	if strings.Contains(exec.query, "OFFSET") {
		t.Fatalf("bulk read still uses OFFSET: %s", exec.query)
	}
	if exec.args[len(exec.args)-1] != 200001 {
		t.Fatalf("missing overflow sentinel: %v", exec.args)
	}
}

func TestPerformanceSeriesSeparatesLegacyFillDates(t *testing.T) {
	if !strings.Contains(dailyPerformanceSeriesSQL, "UNION ALL") || !strings.Contains(dailyPerformanceSeriesSQL, "positions AS MATERIALIZED") || !strings.Contains(dailyPerformanceSeriesSQL, "trade_date IS NULL") {
		t.Fatal("series must preserve legacy dates without an OR scan")
	}
}
