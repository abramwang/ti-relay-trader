package main

import (
	"math"
	"slices"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func fuyingAccountBridgeFixture() ([]brokerBasis20261110Day, fuyingCashMatrix, ledger.PerformanceNAV) {
	var bases []brokerBasis20261110Day
	for i, day := range []string{"2026-10-12", "2026-10-13", "2026-10-14"} {
		bases = append(bases, brokerBasis20261110Day{Account: fuying13Account, Date: day, Raw: map[string]any{
			"reported_open_total_asset": float64(1000 + i*20), "reported_close_total_asset": float64(1020 + i*20),
			"reported_daily_pnl": float64(20), "reported_deposit": 0, "reported_withdrawal": 0,
			"broker_unclassified_asset_component": float64(0),
		}})
	}
	bases[1].Raw["broker_unclassified_asset_component"] = float64(3)
	m := fuyingCashMatrix{AccountID: fuying13Account}
	for _, row := range []struct{ id, source, posted, amount string }{
		{"refund", "20261012", "20261014", "30.00"},
		{"payable", "20261013", "20261014", "-5.00"},
		{"same-day", "20261013", "20261013", "50.00"},
	} {
		m.Resolved = append(m.Resolved, fuyingMatrixCash{AccountID: fuying13Account, FlowID: row.id,
			Amount: row.amount, SourceDate: row.source, Reason: "explicit_source", Raw: map[string]string{"交易日期": row.posted}})
	}
	m.Unresolved = []fuyingMatrixCash{{AccountID: fuying13Account, FlowID: "unknown", Amount: "8.00", Raw: map[string]string{"交易日期": "20261014"}}}
	nav := ledger.PerformanceNAV{AccountID: fuying13Account, TradeDate: "2026-10-13", PerformanceNAVPK: 1,
		Version: 1, IsCurrent: true, Status: "provisional", FormulaVersion: fuyingContinuousFormula, CloseEconomicNAV: 1040}
	return bases, m, nav
}

func TestFuyingAccountBridgeBoundaryAndRelease(t *testing.T) {
	b, m, n := fuyingAccountBridgeFixture()
	r, err := reviewFuyingAccountBridge(b, m, n)
	if err != nil {
		t.Fatal(err)
	}
	if r.EconomicNAVPublishable || len(r.BoundaryTail) != 2 || r.EarliestTailSource != "2026-10-12" ||
		r.ReportedBoundary != 104000 || r.PublishedClose != 104000 || r.ConditionalBoundary != 106500 ||
		r.BoundaryDifference != 2500 || r.NextOpening != 106500 {
		t.Fatalf("wrong boundary: %+v", r)
	}
	if len(r.BoundaryReleases) != 1 || r.BoundaryReleases[0].InflowCents != 3000 ||
		r.BoundaryReleases[0].OutflowCents != 500 || r.BoundaryReleases[0].NetReleaseCents != 2500 ||
		r.BoundaryReleases[0].PostedDate != "2026-10-14" || !slices.Equal(r.BoundaryReleases[0].FlowIDs, []string{"payable", "refund"}) {
		t.Fatalf("incorrect actual tail release: %+v", r.BoundaryReleases)
	}
	last := r.Days[2]
	if last.ConditionalClose != 106000 || last.ConditionalPnL != -500 ||
		last.CashTiming.OpenReceivableCents != 3000 || last.CashTiming.OpenPayableCents != 500 ||
		last.CashTiming.CloseReceivableCents != 0 || last.CashTiming.ClosePayableCents != 0 {
		t.Fatalf("cash receipt counted as new income or payable clamped: %+v", last)
	}
	if r.Days[1].CashTiming.SameDayIncludedCents != 5000 || len(last.UnresolvedPostedFlows) != 1 ||
		!slices.Contains(last.LocalReviewFlags, "adjacent_broker_component_unclassified") ||
		!slices.Contains(last.LocalReviewFlags, "unresolved_source_cash_posted") ||
		len(r.UnresolvedCash) != 1 || r.Days[1].OtherComponent != 300 {
		t.Fatalf("scope evidence lost: %+v", r)
	}
	if b[1].Raw["reported_close_total_asset"] != float64(1040) || n.CloseEconomicNAV != 1040 || m.Unresolved[0].SourceDate != "" {
		t.Fatal("original evidence changed")
	}
}

func TestFuyingAccountBridgeZeroGapDoesNotApprovePublication(t *testing.T) {
	b, m, n := fuyingAccountBridgeFixture()
	n.CloseEconomicNAV = 1065
	r, err := reviewFuyingAccountBridge(b, m, n)
	if err != nil || r.BoundaryDifference != 0 || r.EconomicNAVPublishable ||
		!slices.Contains(r.QualityFlags, "asset_inclusion_unverified") ||
		!slices.Contains(r.QualityFlags, "private_settlement_coverage_unverified") {
		t.Fatalf("arithmetic promoted to publication: %+v %v", r, err)
	}
}

func TestFuyingAccountBridgeRejectsBadEvidence(t *testing.T) {
	for name, change := range map[string]func([]brokerBasis20261110Day, *fuyingCashMatrix, *ledger.PerformanceNAV){
		"account":     func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) { n.AccountID = "other" },
		"old version": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) { n.IsCurrent = false },
		"blocked":     func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) { n.Status = "blocked" },
		"formula": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			n.FormulaVersion = "other"
		},
		"missing id": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			n.PerformanceNAVPK = 0
		},
		"nan": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			n.CloseEconomicNAV = math.NaN()
		},
		"precision": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			n.CloseEconomicNAV = 1040.001
		},
		"missing boundary": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			n.TradeDate = "2026-10-09"
		},
		"missing next day": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			n.TradeDate = "2026-10-14"
		},
		"wrong basis account": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			b[0].Account = "other"
		},
		"missing basis money": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			delete(b[0].Raw, "reported_open_total_asset")
		},
		"changed bridge": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			b[1].Raw["reported_open_total_asset"] = float64(1030)
		},
		"flow": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			b[1].Raw["reported_deposit"] = 1
		},
		"bad date": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			b[1].Date = "2026-10-32"
		},
		"duplicate day": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) { b[1].Date = b[0].Date },
		"cash repeated": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			m.Resolved = append(m.Resolved, m.Resolved[0])
		},
		"candidate promoted": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			m.Unresolved[0].SourceDate = "20261012"
		},
		"unknown time": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			m.Unresolved[0].Raw["交易日期"] = ""
		},
		"unknown amount": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			m.Unresolved[0].Amount = "NaN"
		},
		"candidate source": func(b []brokerBasis20261110Day, m *fuyingCashMatrix, n *ledger.PerformanceNAV) {
			m.Resolved[0].Reason = "pcf_candidate"
		},
	} {
		t.Run(name, func(t *testing.T) {
			b, m, n := fuyingAccountBridgeFixture()
			change(b, &m, &n)
			if _, err := reviewFuyingAccountBridge(b, m, n); err == nil {
				t.Fatal("invalid account bridge accepted")
			}
		})
	}
}

func TestFuyingAccountBridgeCLIRejectsUnpinnedBoundary(t *testing.T) {
	for _, extra := range [][]string{{"--published-boundary", "unused.json"}, {"--published-boundary-sha256", "bad"}} {
		args := append([]string{"--output", t.TempDir() + "/new.json"}, extra...)
		if err := runFuyingCashTiming(args); err == nil {
			t.Fatal("unpinned boundary accepted")
		}
	}
}
