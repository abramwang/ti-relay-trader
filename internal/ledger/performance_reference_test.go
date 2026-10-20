package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPreviousPerformanceNAVIsSingleBoundedAccountRead(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("stop")}
	repo := NewRepository(exec)
	if _, err := repo.PreviousPerformanceNAV(context.Background(), "acct", "20260702"); err == nil {
		t.Fatal("query error lost")
	}
	for _, clause := range []string{"account_id = $1", "is_current", "trade_date < $2::date", "ORDER BY trade_date DESC", "LIMIT 1"} {
		if !strings.Contains(exec.query, clause) {
			t.Fatalf("missing %s in %s", clause, exec.query)
		}
	}
	if exec.args[0] != "acct" || exec.args[1] != "2026-07-02" {
		t.Fatalf("scope = %v", exec.args)
	}
	if _, err := repo.PreviousPerformanceNAV(context.Background(), "", "20260702"); err == nil {
		t.Fatal("account required")
	}
}
