package main

import (
	"testing"

	"ti-relay-trader/internal/ledger"
)

func TestPreviewChainRejectsPersistBeforeDatabase(t *testing.T) {
	if err := runPerformanceRebuild([]string{"-preview-chain", "-persist", "-config", "/missing"}); err == nil || err.Error() != "-preview-chain cannot be combined with -persist" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPreviewCostStatesSelectWholeLatestDay(t *testing.T) {
	base := []ledger.PositionCostState{{TradeDate: "2026-10-20", Symbol: "old"}}
	previews := map[string][]ledger.PositionCostState{
		"2026-10-21": {{TradeDate: "2026-10-21", Symbol: "new"}},
		"2026-10-23": {{TradeDate: "2026-10-23", Symbol: "future"}},
	}
	got := previewCostStates(base, previews, ledger.PositionCostStateQuery{BeforeDate: "2026-10-22"})
	if len(got) != 1 || got[0].Symbol != "new" {
		t.Fatalf("mixed or future states: %#v", got)
	}
	got[0].Symbol = "mutated"
	if previews["2026-10-21"][0].Symbol != "new" {
		t.Fatal("preview shares state slice")
	}
	got = previewCostStates(base, previews, ledger.PositionCostStateQuery{BeforeDate: "2026-10-21"})
	if got[0].Symbol != "old" {
		t.Fatalf("used same-day state: %#v", got)
	}
	got = previewCostStates(base, previews, ledger.PositionCostStateQuery{TradeDate: "2026-10-21"})
	if got[0].Symbol != "new" {
		t.Fatalf("exact date not replaced: %#v", got)
	}
	got = previewCostStates([]ledger.PositionCostState{{TradeDate: "2026-10-24", Symbol: "later-db"}}, previews, ledger.PositionCostStateQuery{BeforeDate: "2026-10-26"})
	if got[0].Symbol != "later-db" {
		t.Fatalf("replaced newer stored state: %#v", got)
	}
}

func TestPreviewNAVReplacementAndRange(t *testing.T) {
	base := []ledger.PerformanceNAV{{TradeDate: "2026-10-20", AccountDayPnL: 1}, {TradeDate: "2026-10-21", AccountDayPnL: 2}}
	previews := map[string]ledger.PerformanceNAV{
		"2026-10-21": {TradeDate: "2026-10-21", AccountDayPnL: 3},
		"2026-10-23": {TradeDate: "2026-10-23", AccountDayPnL: 4},
	}
	got := previewNAVs(base, previews, "", "2026-10-22")
	if len(got) != 2 || got[1].AccountDayPnL != 3 || base[1].AccountDayPnL != 2 {
		t.Fatalf("replacement: %#v", got)
	}
	if got := previewNAVs(nil, previews, "2026-10-22", "2026-10-22"); len(got) != 0 {
		t.Fatalf("range leak: %#v", got)
	}
}
