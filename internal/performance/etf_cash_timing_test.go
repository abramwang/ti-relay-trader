package performance

import (
	"reflect"
	"testing"
)

func cashTimingLeg(id, source, posted string, amount int64) ObservedETFCashLeg {
	return ObservedETFCashLeg{AccountID: "a", FlowID: id, SourceDate: source, PostedDate: posted,
		AmountCents: amount, SourceEvidence: "explicit_source", EvidenceRef: "raw:" + id}
}

func TestETFCashTimingSignedBalances(t *testing.T) {
	legs := []ObservedETFCashLeg{
		cashTimingLeg("in", "2026-10-12", "2026-10-13", 10000),
		cashTimingLeg("out", "2026-10-12", "2026-10-14", -10000),
		cashTimingLeg("same", "2026-10-12", "2026-10-12", 20000),
	}
	before := append([]ObservedETFCashLeg(nil), legs...)
	dates := []string{"2026-10-12", "2026-10-13", "2026-10-14"}
	r, err := ReviewETFCashTiming("a", "2026-10-09", dates, legs)
	if err != nil {
		t.Fatal(err)
	}
	d := r.Days[0]
	if d.CloseReceivableCents != 10000 || d.ClosePayableCents != 10000 || d.PendingLegs != 2 || d.NetCarryAdjustmentCents != 0 || d.SameDayIncludedCents != 20000 {
		t.Fatalf("net zero hid unsettled gross balances: %+v", d)
	}
	if r.Days[1].ClosePayableCents != 10000 || r.Days[1].CloseReceivableCents != 0 || r.Days[2].PostedCashCents != -10000 || r.Days[2].NetCarryAdjustmentCents != 10000 {
		t.Fatalf("signed payment release failed: %+v", r.Days)
	}
	if r.CompleteSettlement || r.EconomicNAVPublishable || !reflect.DeepEqual(before, legs) {
		t.Fatal("scope promoted or inputs changed")
	}
	again, _ := ReviewETFCashTiming("a", "2026-10-09", dates, legs)
	if !reflect.DeepEqual(r, again) {
		t.Fatal("repeat invocation accumulated cash")
	}
}

func TestETFCashTimingOpeningAndSameDayDebit(t *testing.T) {
	legs := []ObservedETFCashLeg{cashTimingLeg("prior", "2026-10-08", "2026-10-12", -500),
		cashTimingLeg("already", "2026-10-08", "2026-10-09", 900),
		cashTimingLeg("fee", "2026-10-12", "2026-10-12", -100)}
	r, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12"}, legs)
	if err != nil {
		t.Fatal(err)
	}
	d := r.Days[0]
	if d.OpenPayableCents != 500 || d.PostedCashCents != -600 || d.SourceCashCents != -100 || d.SameDayIncludedCents != -100 || d.ClosePayableCents != 0 || d.PendingLegs != 0 {
		t.Fatalf("wrong carry/duplicate prior payment: %+v", d)
	}
}

func TestETFCashTimingRejectsAmbiguousEvidence(t *testing.T) {
	mutations := map[string]func(*ObservedETFCashLeg){
		"account":              func(l *ObservedETFCashLeg) { l.AccountID = "b" },
		"candidate":            func(l *ObservedETFCashLeg) { l.SourceEvidence = "pcf_candidate" },
		"empty proof":          func(l *ObservedETFCashLeg) { l.EvidenceRef = " " },
		"empty id":             func(l *ObservedETFCashLeg) { l.FlowID = "" },
		"padded id":            func(l *ObservedETFCashLeg) { l.FlowID = " f " },
		"zero":                 func(l *ObservedETFCashLeg) { l.AmountCents = 0 },
		"overflow":             func(l *ObservedETFCashLeg) { l.AmountCents = -9223372036854775808 },
		"future receipt":       func(l *ObservedETFCashLeg) { l.PostedDate = "2026-10-14" },
		"source after receipt": func(l *ObservedETFCashLeg) { l.SourceDate = "2026-10-14" },
		"invalid date":         func(l *ObservedETFCashLeg) { l.SourceDate = "2026-02-30" },
		"missing day":          func(l *ObservedETFCashLeg) { l.SourceDate = "2026-10-11" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			leg := cashTimingLeg("f", "2026-10-12", "2026-10-13", 100)
			mutate(&leg)
			if _, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, []ObservedETFCashLeg{leg}); err == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
}

func TestETFCashTimingDuplicateAndDateCoverage(t *testing.T) {
	l := cashTimingLeg("f", "2026-10-12", "2026-10-13", 100)
	if _, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, []ObservedETFCashLeg{l, l}); err == nil {
		t.Fatal("duplicate flow accepted")
	}
	for _, dates := range [][]string{nil, {"2026-10-13", "2026-10-12"}, {"2026-10-12", "2026-10-12"}, {"2026-10-09"}, {"2026-10-12"}} {
		if _, err := ReviewETFCashTiming("a", "2026-10-09", dates, []ObservedETFCashLeg{l}); err == nil {
			t.Fatal("invalid day scope accepted")
		}
	}
}

func TestETFCashTimingEmptyDoesNotProveCompletion(t *testing.T) {
	r, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12"}, nil)
	if err != nil || r.CompleteSettlement || r.EconomicNAVPublishable || len(r.Days) != 1 {
		t.Fatalf("empty evidence upgraded: %+v %v", r, err)
	}
}

func TestETFCashTimingBounds(t *testing.T) {
	if _, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12"}, make([]ObservedETFCashLeg, 100001)); err == nil {
		t.Fatal("unbounded input accepted")
	}
	for _, proof := range []string{"explicit_source_and_order", "unique_order_reference_in_reviewed_coverage"} {
		l := cashTimingLeg("f", "2026-10-12", "2026-10-12", 1000000000000)
		l.SourceEvidence = proof
		if _, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12"}, []ObservedETFCashLeg{l}); err != nil {
			t.Fatal(err)
		}
	}
}
