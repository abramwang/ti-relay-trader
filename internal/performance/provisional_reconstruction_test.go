package performance

import (
	"context"
	"math"
	"strings"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func TestProvisionalReconstructionCashReleaseAndContinuity(t *testing.T) {
	a := ledger.PerformanceNAV{AccountID: "a", TradeDate: "2026-10-09", Status: "provisional", CloseEconomicNAV: 1000, CumulativeNAV: 2}
	days := []ReconstructionAssetDay{
		{Date: "2026-10-12", ReportedClose: 900, ObservedOpen: 1000, ReceivableCents: 11000, PayableCents: 1000, OtherCents: 700, Source: "broker", InputSHA: strings.Repeat("a", 64)},
		{Date: "2026-10-13", ReportedClose: 1005, ObservedOpen: 900, Source: "broker", InputSHA: strings.Repeat("b", 64), UnresolvedFlows: []string{"unresolved"}},
	}
	auth := map[string]any{"approved": true, "scope": "provisional_only"}
	r, err := BuildProvisionalReconstruction(a, []string{"2026-10-12", "2026-10-13"}, days, auth)
	if err != nil {
		t.Fatal(err)
	}
	if r[0].CloseEconomicNAV != 1000 || r[0].AccountDayPnL != 0 || r[1].AccountDayPnL != 5 || r[1].OpenEconomicNAV != 1000 || r[1].CumulativeNAV != 2.01 {
		t.Fatalf("broken cash release/continuity: %+v", r)
	}
	for _, n := range r {
		if n.Status != "provisional" || n.PnLComponents["account_ledger_verified"] != false || !containsStringValue(n.QualityFlags, "etf_asset_inclusion_unverified") {
			t.Fatal("unverified facts promoted")
		}
	}
	for name, mutate := range map[string]func(){
		"authorization":      func() { auth["approved"] = false },
		"finalized approval": func() { auth["scope"] = "finalized" },
		"duplicate day":      func() { days[1].Date = days[0].Date },
		"negative payable":   func() { days[0].PayableCents = -1 },
		"nonfinite":          func() { days[0].ReportedClose = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) {
			old := append([]ReconstructionAssetDay(nil), days...)
			mutate()
			if _, e := BuildProvisionalReconstruction(a, []string{"2026-10-12", "2026-10-13"}, days, auth); e == nil {
				t.Fatal("invalid plan accepted")
			}
			days = old
			auth["approved"] = true
			auth["scope"] = "provisional_only"
		})
	}
}

func TestProvisionalReconstructionSupportsContinuity(t *testing.T) {
	if !performanceNAVSupportsContinuity(ProvisionalReconstructionFormula, "performance_economic_nav.v2.7") {
		t.Fatal("provisional chain cannot continue")
	}
	store := &fakePerformanceStore{navs: []ledger.PerformanceNAV{{PerformanceNAVPK: 1, AccountID: "a", TradeDate: "2026-11-12", Status: "provisional", FormulaVersion: ProvisionalReconstructionFormula, CumulativeNAV: 1, CloseEconomicNAV: 1000}}}
	s, err := New(Options{Store: store, Calendar: weekdayCalendar{}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, flags, err := s.previousNAVContext(context.Background(), "a", "2026-11-13")
	if err != nil || !containsStringValue(flags, "provisional_reconstruction_anchor") {
		t.Fatalf("lost provisional provenance: %v %v", flags, err)
	}
	if _, err := s.CalculateEconomicNAV(context.Background(), "a", "2026-11-12", EconomicNAVOptions{Persist: true}); err == nil {
		t.Fatal("routine rebuild overwrites reviewed NAV")
	}
	if _, err := s.ReviewNAVReconciliation(context.Background(), "a", "2026-11-12", NAVReconciliationReviewOptions{Action: "confirm", Force: true}); err == nil {
		t.Fatal("forced finalization accepted")
	}
	if len(store.navUpserts) != 0 {
		t.Fatal("unexpected financial write")
	}
}

func TestProvisionalInferredSourceRelease(t *testing.T) {
	a := ledger.PerformanceNAV{AccountID: "a", TradeDate: "2026-10-09", Status: "provisional", CloseEconomicNAV: 1000, CumulativeNAV: 1}
	days := []ReconstructionAssetDay{
		{Date: "2026-10-12", ReportedClose: 1000, ObservedOpen: 1000, InferredReceivableCents: 200, InferredPayableCents: 1200, Source: "broker", InputSHA: strings.Repeat("a", 64)},
		{Date: "2026-10-13", ReportedClose: 990, ObservedOpen: 1000, UnresolvedFlows: []string{"credit", "debit"}, Source: "broker", InputSHA: strings.Repeat("a", 64)},
	}
	auth := map[string]any{"approved": true, "scope": "provisional_only"}
	if _, err := BuildProvisionalReconstruction(a, []string{"2026-10-12", "2026-10-13"}, days, auth); err == nil {
		t.Fatal("unapproved inference accepted")
	}
	auth["cash_source_policy"] = "same_etf_prior_redemption_and_unique_pcf_match"
	r, err := BuildProvisionalReconstruction(a, []string{"2026-10-12", "2026-10-13"}, days, auth)
	if err != nil {
		t.Fatal(err)
	}
	if r[0].AccountDayPnL != -10 || r[1].AccountDayPnL != 0 || r[1].CloseEconomicNAV != 990 || !containsStringValue(r[0].QualityFlags, "cash_source_date_inferred") {
		t.Fatalf("double counting inferred payment: %+v", r)
	}
	if r[0].PnLComponents["reconstruction"].(map[string]any)["known_payable"] != float64(0) {
		t.Fatal("inferred payment promoted to known")
	}
}
