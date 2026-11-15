package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func cashCyclePublicationFixture(t *testing.T) (provisionalRecoveryPlan, performance.ETFCashCycleReview) {
	t.Helper()
	p := provisionalRecoveryPlan{Account: "a", Dates: []string{"2026-10-12", "2026-10-13", "2026-10-14"},
		Anchor:        ledger.PerformanceNAV{AccountID: "a", TradeDate: "2026-10-09", CloseEconomicNAV: 1000, CumulativeNAV: 1, Status: "provisional"},
		Authorization: map[string]any{"approved": true, "scope": "provisional_only"}}
	for _, date := range p.Dates {
		p.Assets = append(p.Assets, performance.ReconstructionAssetDay{Date: date, ReportedClose: 1000, ObservedOpen: 1000, Source: "fixture", InputSHA: strings.Repeat("a", 64)})
	}
	p.Assets[0].ReceivableCents = 100
	var err error
	p.NAVs, err = performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	r, err := performance.ReviewETFCashCycles("a", "2026-10-09", p.Dates, []performance.ETFCycleCashLeg{{SecurityID: "510310.SH", ObservedETFCashLeg: performance.ObservedETFCashLeg{AccountID: "a", FlowID: "f", SourceDate: "2026-10-12", PostedDate: "2026-10-13", AmountCents: 100, SourceEvidence: "explicit_source", EvidenceRef: "fixture"}}})
	if err != nil {
		t.Fatal(err)
	}
	return p, r
}

func TestCashCyclePublicationDayAttribution(t *testing.T) {
	p, r := cashCyclePublicationFixture(t)
	d, err := compareCashCyclePublication(p, r)
	if err != nil {
		t.Fatal(err)
	}
	if d[0].PublishedPnLCents != 100 || d[1].PublishedPnLCents != -100 || d[0].BrokerPnLCents != 0 || d[1].NoObservedTimingOrOther || !d[2].NoObservedTimingOrOther || len(d[2].OriginalFlags) == 0 {
		t.Fatal("wrong attribution/flag scope", d)
	}
}

func TestCashCyclePublicationRejectsChangedLedger(t *testing.T) {
	for name, mutate := range map[string]func(*provisionalRecoveryPlan, *performance.ETFCashCycleReview){
		"external flow": func(p *provisionalRecoveryPlan, r *performance.ETFCashCycleReview) {
			p.NAVs[0].ExternalNetFlow = 5000000
		},
		"nav amount": func(p *provisionalRecoveryPlan, r *performance.ETFCashCycleReview) { p.NAVs[0].AccountDayPnL++ },
		"asset":      func(p *provisionalRecoveryPlan, r *performance.ETFCashCycleReview) { p.Assets[0].ReportedClose++ },
		"timing": func(p *provisionalRecoveryPlan, r *performance.ETFCashCycleReview) {
			r.Days[0].Known.CloseReceivableCents++
		},
		"account": func(p *provisionalRecoveryPlan, r *performance.ETFCashCycleReview) { r.AccountID = "b" },
		"dates":   func(p *provisionalRecoveryPlan, r *performance.ETFCashCycleReview) { p.Dates = nil },
	} {
		t.Run(name, func(t *testing.T) {
			p, r := cashCyclePublicationFixture(t)
			mutate(&p, &r)
			if _, err := compareCashCyclePublication(p, r); err == nil {
				t.Fatal("accepted changed evidence")
			}
		})
	}
}

func TestCashCycleReviewCLIRequiresPlanHash(t *testing.T) {
	for _, args := range [][]string{
		{"--cycle-plan", "missing"},
		{"--cycle-plan-sha256", strings.Repeat("a", 64)},
		{"--cycle-plan", "missing", "--cycle-plan-sha256", strings.Repeat("a", 64), "--published-boundary", "missing", "--published-boundary-sha256", strings.Repeat("a", 64)},
	} {
		if err := runFuyingCashTiming(append(args, "--output", filepath.Join(t.TempDir(), "report.json"))); err == nil {
			t.Fatal("invalid CLI accepted")
		}
	}
}

func TestCashCycleReviewPublishedEvidence(t *testing.T) {
	path := filepath.Join("../..", "outputs/recovery/fuying13_broker_refresh_20261115/plan.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("private broker recovery evidence is not in the source repository")
	}
	var p provisionalRecoveryPlan
	if err := readProvisionalPlan(path, "dc38a0f24aea64c40da7735e76eb32cdf6790a2a9fdb3a501b8fe49c7ecfe8f7", &p); err != nil {
		t.Fatal(err)
	}
	var m fuyingCashMatrix
	if err := readPinnedJSON(filepath.Join("../..", fuyingCashMatrixPath), fuyingCashMatrixHash, &m); err != nil {
		t.Fatal(err)
	}
	legs, err := fuyingCycleLegs(m)
	if err != nil {
		t.Fatal(err)
	}
	r, err := performance.ReviewETFCashCycles(p.Account, p.Anchor.TradeDate, p.Dates, legs)
	if err != nil {
		t.Fatal(err)
	}
	days, err := compareCashCyclePublication(p, r)
	if err != nil {
		t.Fatal(err)
	}
	unaffected := 0
	var broker, published int64
	for _, d := range days {
		if d.NoObservedTimingOrOther {
			unaffected++
		}
		broker += d.BrokerPnLCents
		published += d.PublishedPnLCents
	}
	if len(legs) != 3616 || len(r.Cycles) != 14 || len(days) != 25 || unaffected != 9 || r.PeriodAdjustmentCents != 0 || broker != -182359022 || published != broker || r.EconomicNAVPublishable {
		t.Fatalf("private evidence regression: cycles=%d days=%d unaffected=%d broker=%d published=%d", len(r.Cycles), len(days), unaffected, broker, published)
	}
}
