package performance

import (
	"encoding/json"
	"strings"
	"testing"
)

func dailyCycleFixture(t *testing.T) CycleReviewSource {
	t.Helper()
	legs := []ETFCycleCashLeg{
		cycleLeg("known", "510310.SH", "2026-10-12", "2026-10-13", 300, false),
		cycleLeg("pay", "510310.SH", "2026-10-12", "2026-10-14", -100, true),
		cycleLeg("receive", "510310.SH", "2026-10-12", "2026-10-14", 100, true),
	}
	c, err := ReviewETFCashCycles("a", "2026-10-09", []string{"2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15", "2026-10-16"}, legs)
	if err != nil {
		t.Fatal(err)
	}
	tr := ETFCycleTradingEvidence{AccountID: "a", CycleID: "2026-10-12/510310.SH", AcquisitionDate: "2026-10-12", AcquisitionQuantity: 100, RedemptionQuantity: 100, AcquisitionGrossCents: 1000, ComponentSaleCents: 1000, FeeScope: "ordinary_buys_and_component_sales_only", FeeBasis: "broker_cash_reconciled", EvidenceRefs: []string{"synthetic"}}
	s := CycleReviewSource{AccountID: "a", Cycles: c, CashLegs: legs}
	s.Profit.Profits, err = CalculateETFCycleProfits(c, []ETFCycleTradingEvidence{tr})
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range c.Days {
		s.AccountDays = append(s.AccountDays, CycleAccountDay{TradeDate: day.TradeDate, BrokerPnLCents: 10000, PublishedPnLCents: 10000 + day.AdjustmentCents, TimingAdjustmentCents: day.AdjustmentCents, NoObservedTimingOrOther: len(day.ActiveCycleIDs) == 0})
	}
	s.AccountDays[2].OtherClosingCents = 55
	s.AccountDays[3].OtherOpeningCents = 55
	s.AccountDays[3].NoObservedTimingOrOther = false
	return s
}

func TestCycleDailyReviewPublication(t *testing.T) {
	s := dailyCycleFixture(t)
	v, err := BuildCycleReviewVersion(s, "daily-v1", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var d CycleReviewDocument
	if err = json.Unmarshal(v.Payload, &d); err != nil {
		t.Fatal(err)
	}
	if d.Daily == nil || len(d.Daily.Days) != 5 || len(d.Daily.InferredLegs) != 2 {
		t.Fatal("missing daily evidence")
	}
	first := d.Daily.Days[0]
	if first.Timing.Inferred.NetCarryAdjustmentCents != 0 || len(first.InferredFlowIDs) != 2 || first.Account.NoObservedTimingOrOther {
		t.Fatal("offsetting gross obligations hidden")
	}
	if len(d.Daily.Days[2].InferredFlowIDs) != 2 || len(d.Daily.Days[3].InferredFlowIDs) != 0 || d.Daily.Days[3].Account.NoObservedTimingOrOther || !d.Daily.Days[4].Account.NoObservedTimingOrOther {
		t.Fatal("release day or asset scope lost")
	}
	for _, kind := range []string{"replay", "published-pnl", "scope-flag", "inferred-amount", "source", "duplicate-flow", "source-proof", "opening", "continuity", "known-cash", "active-cycle", "finality", "missing-day", "overflow", "original-other"} {
		t.Run(kind, func(t *testing.T) {
			var x CycleReviewDocument
			_ = json.Unmarshal(v.Payload, &x)
			r := x.Daily
			switch kind {
			case "replay":
				r.Days[0].Timing.Inferred.PendingLegs++
			case "published-pnl":
				r.Days[0].Account.PublishedPnLCents++
			case "scope-flag":
				r.Days[0].Account.NoObservedTimingOrOther = true
			case "inferred-amount":
				r.InferredLegs[0].AmountCents--
			case "source":
				r.InferredLegs[0].SourceDate = "2026-10-13"
			case "duplicate-flow":
				r.InferredLegs = append(r.InferredLegs, r.InferredLegs[0])
			case "source-proof":
				r.InferredLegs[0].SourceEvidence = "explicit_source"
			case "opening":
				r.OpeningDate = "invalid"
			case "continuity":
				r.Days[1].Timing.Known.OpenReceivableCents++
			case "known-cash":
				r.Days[0].Timing.Known.PostedCashCents++
			case "active-cycle":
				r.Days[0].Timing.ActiveCycleIDs = nil
			case "finality":
				r.Days[0].Timing.AccountNAVVerified = true
			case "missing-day":
				r.Days = r.Days[:len(r.Days)-1]
			case "overflow":
				r.Days[0].Account.BrokerPnLCents = 1 << 62
			case "original-other":
				r.Days[3].Account.OtherOpeningCents = 0
			}
			if ValidateCycleReview(x) == nil {
				t.Fatal("invalid daily review accepted")
			}
		})
	}
	s.Cycles.Days[0].Inferred.SourceCashCents++
	if _, err = BuildCycleReviewVersion(s, "bad", strings.Repeat("b", 64)); err == nil {
		t.Fatal("tampered source aggregates accepted")
	}
}
