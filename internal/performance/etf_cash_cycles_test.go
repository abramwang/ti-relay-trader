package performance

import (
	"reflect"
	"testing"
)

func cycleLeg(id, security, source, posted string, cents int64, inferred bool) ETFCycleCashLeg {
	l := cashTimingLeg(id, source, posted, cents)
	if inferred {
		l.SourceEvidence = InferredETFCashSource
	}
	return ETFCycleCashLeg{ObservedETFCashLeg: l, SecurityID: security}
}

func TestETFCashCyclesPreserveGrossAndReleaseDayExposure(t *testing.T) {
	legs := []ETFCycleCashLeg{
		cycleLeg("refund", "510310.SH", "2026-10-12", "2026-10-13", 10000, false),
		cycleLeg("debit", "510310.SH", "2026-10-12", "2026-10-13", -10000, false),
		cycleLeg("candidate", "510310.SH", "2026-10-12", "2026-10-14", -50, true),
		cycleLeg("same", "159915.SZ", "2026-10-12", "2026-10-12", 300, false),
	}
	dates := []string{"2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15"}
	r, err := ReviewETFCashCycles("a", "2026-10-09", dates, legs)
	if err != nil {
		t.Fatal(err)
	}
	if r.KnownSourceLegs != 3 || r.InferredSourceLegs != 1 || len(r.Cycles) != 2 || r.PeriodAdjustmentCents != 0 || r.EconomicNAVPublishable {
		t.Fatalf("wrong cycle scope %+v", r)
	}
	for n := 0; n < 3; n++ {
		if r.Days[n].TimingState != "observed_timing_exposure" || len(r.Days[n].ActiveCycleIDs) != 1 || r.Days[n].AccountNAVVerified {
			t.Fatalf("release/net-zero hidden %+v", r.Days[n])
		}
	}
	if r.Days[3].TimingState != "no_observed_timing_exposure" || r.Days[0].Known.CloseReceivableCents != 10000 || r.Days[0].Known.ClosePayableCents != 10000 || r.Days[0].Inferred.ClosePayableCents != 50 {
		t.Fatal("lost gross/candidate/after-release separation")
	}
	if r.Cycles[1].NetCashCents != -50 || r.Cycles[1].ReceivedCents != 10000 || r.Cycles[1].PaidCents != 10050 || r.Cycles[1].CompleteSettlement || r.Cycles[0].SameDayCashCents != 300 {
		t.Fatal("cash double counted or upgraded to cycle profit/finality")
	}
	for a, b := 0, len(legs)-1; a < b; a, b = a+1, b-1 {
		legs[a], legs[b] = legs[b], legs[a]
	}
	again, err := ReviewETFCashCycles("a", "2026-10-09", dates, legs)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("input order changed result", err)
	}
}

func TestETFCashCyclesRejectCrossCycleReuseAndUnverifiedEvidence(t *testing.T) {
	base := cycleLeg("f", "510310.SH", "2026-10-12", "2026-10-13", 100, false)
	for name, mutate := range map[string]func(*ETFCycleCashLeg){
		"symbol":               func(l *ETFCycleCashLeg) { l.SecurityID = "510310" },
		"account":              func(l *ETFCycleCashLeg) { l.AccountID = "b" },
		"unknown evidence":     func(l *ETFCycleCashLeg) { l.SourceEvidence = "nearest_redemption" },
		"future":               func(l *ETFCycleCashLeg) { l.PostedDate = "2026-10-14" },
		"source after posting": func(l *ETFCycleCashLeg) { l.SourceDate = "2026-10-14" },
		"missing audit":        func(l *ETFCycleCashLeg) { l.EvidenceRef = "" },
		"overflow":             func(l *ETFCycleCashLeg) { l.AmountCents = 1 << 62 },
	} {
		t.Run(name, func(t *testing.T) {
			l := base
			mutate(&l)
			if _, err := ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, []ETFCycleCashLeg{l}); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	other := base
	other.SecurityID = "159915.SZ"
	other.SourceEvidence = InferredETFCashSource
	if _, err := ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, []ETFCycleCashLeg{base, other}); err == nil {
		t.Fatal("same flow consumed by two cycles")
	}
	if _, err := ReviewETFCashTiming("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, []ObservedETFCashLeg{other.ObservedETFCashLeg}); err == nil {
		t.Fatal("legacy observed path accepted inferred source")
	}
}

func TestETFCashCyclesOpeningExposureAndNoEvidence(t *testing.T) {
	r, err := ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12"}, []ETFCycleCashLeg{cycleLeg("prior", "510310.SH", "2026-10-08", "2026-10-12", 100, false)})
	if err != nil || r.PeriodAdjustmentCents != -100 || r.Days[0].TimingState != "observed_timing_exposure" {
		t.Fatal("opening receivable omitted", r, err)
	}
	r, err = ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12"}, nil)
	if err != nil || r.EconomicNAVPublishable || r.Days[0].AccountNAVVerified || len(r.QualityFlags) == 0 {
		t.Fatal("empty input promoted", err)
	}
	for _, dates := range [][]string{nil, {"2026-10-12", "2026-10-12"}, {"2026-10-09"}} {
		if _, err := ReviewETFCashCycles("a", "2026-10-09", dates, nil); err == nil {
			t.Fatal("bad calendar accepted")
		}
	}
}
