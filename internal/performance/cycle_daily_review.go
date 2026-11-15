package performance

import (
	"errors"
	"reflect"
	"sort"
	"time"
)

// These are observations from the reviewed publication, not a new NAV formula.
type CycleAccountDay struct {
	TradeDate               string   `json:"trade_date"`
	BrokerPnLCents          int64    `json:"broker_basis_pnl_cents"`
	PublishedPnLCents       int64    `json:"published_pnl_cents"`
	TimingAdjustmentCents   int64    `json:"conditional_timing_adjustment_cents"`
	OtherOpeningCents       int64    `json:"unclassified_open_component_cents"`
	OtherClosingCents       int64    `json:"unclassified_close_component_cents"`
	NoObservedTimingOrOther bool     `json:"no_observed_timing_or_component_exposure"`
	OriginalFlags           []string `json:"published_quality_flags_preserved"`
}

type CycleDailyReviewDay struct {
	Account         CycleAccountDay `json:"account_basis"`
	Timing          ETFCashCycleDay `json:"cash_timing"`
	InferredFlowIDs []string        `json:"inferred_exposed_flow_ids"`
}

type CycleDailyReview struct {
	OpeningDate  string                `json:"opening_date"`
	Days         []CycleDailyReviewDay `json:"days"`
	InferredLegs []ETFCycleCashLeg     `json:"inferred_cash_legs"`
}

func buildCycleDailyReview(s CycleReviewSource) (*CycleDailyReview, error) {
	if s.CashLegs == nil {
		return nil, nil
	} // Older immutable versions had no daily evidence.
	dates := []string{}
	for _, d := range s.Cycles.Days {
		dates = append(dates, d.TradeDate)
	}
	replayed, err := ReviewETFCashCycles(s.AccountID, s.Cycles.OpeningDate, dates, s.CashLegs)
	if err != nil || !reflect.DeepEqual(replayed, s.Cycles) || len(s.AccountDays) != len(dates) {
		return nil, errors.New("daily cycle evidence differs from cash replay")
	}
	r := &CycleDailyReview{OpeningDate: s.Cycles.OpeningDate, Days: []CycleDailyReviewDay{}, InferredLegs: []ETFCycleCashLeg{}}
	for _, l := range s.CashLegs {
		if l.SourceEvidence == InferredETFCashSource {
			r.InferredLegs = append(r.InferredLegs, l)
		}
	}
	sort.Slice(r.InferredLegs, func(i, j int) bool { return r.InferredLegs[i].FlowID < r.InferredLegs[j].FlowID })
	previous := r.OpeningDate
	for i, d := range replayed.Days {
		r.Days = append(r.Days, CycleDailyReviewDay{Account: s.AccountDays[i], Timing: d, InferredFlowIDs: exposedInferredFlows(r.InferredLegs, previous, d.TradeDate)})
		previous = d.TradeDate
	}
	return r, nil
}

func exposedInferredFlows(legs []ETFCycleCashLeg, previous, date string) []string {
	ids := []string{}
	for _, l := range legs {
		if l.SourceDate <= date && l.PostedDate > previous {
			ids = append(ids, l.FlowID)
		}
	}
	sort.Strings(ids)
	return ids
}

func timingExposed(d ETFCashTimingDay) bool {
	return d.OpenReceivableCents != 0 || d.OpenPayableCents != 0 || d.CloseReceivableCents != 0 || d.ClosePayableCents != 0
}

func validateCycleDailyReview(d CycleReviewDocument) error {
	r := d.Daily
	if r == nil {
		return nil
	}
	bad := func() error { return errors.New("invalid daily cycle review") }
	if len(r.Days) == 0 || len(r.Days) > 1000 || len(r.InferredLegs) > 100000 {
		return bad()
	}
	opening, err := time.Parse("2006-01-02", r.OpeningDate)
	if err != nil || opening.Format("2006-01-02") != r.OpeningDate {
		return bad()
	}
	dates := []string{}
	previous := r.OpeningDate
	for _, day := range r.Days {
		date := day.Timing.TradeDate
		v, err := time.Parse("2006-01-02", date)
		if err != nil || v.Format("2006-01-02") != date || date <= previous || date > d.ObservedThrough || day.Account.TradeDate != date {
			return bad()
		}
		dates = append(dates, date)
		previous = date
	}
	if previous != d.ObservedThrough {
		return bad()
	}
	legs := []ObservedETFCashLeg{}
	previous = ""
	cycles := map[string]CycleReviewRow{}
	counts := map[string]int{}
	for _, c := range d.Rows {
		cycles[c.Profit.CycleID] = c
	}
	for _, l := range r.InferredLegs {
		c, ok := cycles[l.SourceDate+"/"+l.SecurityID]
		if !ok || l.SourceEvidence != InferredETFCashSource || l.FlowID <= previous || l.PostedDate > c.LastPosting {
			return bad()
		}
		legs = append(legs, l.ObservedETFCashLeg)
		counts[c.Profit.CycleID]++
		previous = l.FlowID
	}
	for _, c := range d.Rows {
		if counts[c.Profit.CycleID] != c.InferredLegs {
			return bad()
		}
	}
	inferred, err := reviewETFCashTiming(d.AccountID, r.OpeningDate, dates, legs, true)
	if err != nil {
		return bad()
	}
	previous = r.OpeningDate
	for i, day := range r.Days {
		a, t := day.Account, day.Timing
		if t.AccountNAVVerified || !reflect.DeepEqual(t.Inferred, inferred.Days[i]) || !reflect.DeepEqual(day.InferredFlowIDs, exposedInferredFlows(r.InferredLegs, previous, t.TradeDate)) {
			return bad()
		}
		for _, b := range []ETFCashTimingDay{t.Known, t.Inferred} {
			if b.TradeDate != t.TradeDate || b.PendingLegs < 0 || b.PendingLegs > 100000 {
				return bad()
			}
			for _, n := range []int64{b.OpenReceivableCents, b.OpenPayableCents, b.CloseReceivableCents, b.ClosePayableCents} {
				if n < 0 || n > 100000000000000000 {
					return bad()
				}
			}
			for _, n := range []int64{b.SourceCashCents, b.PostedCashCents, b.SameDayIncludedCents, b.NetCarryAdjustmentCents, a.BrokerPnLCents, a.PublishedPnLCents, a.TimingAdjustmentCents, a.OtherOpeningCents, a.OtherClosingCents} {
				if n < -100000000000000000 || n > 100000000000000000 {
					return bad()
				}
			}
			if b.NetCarryAdjustmentCents != b.CloseReceivableCents-b.ClosePayableCents-b.OpenReceivableCents+b.OpenPayableCents || b.PostedCashCents+b.NetCarryAdjustmentCents != b.SourceCashCents || (b.PendingLegs == 0) != (b.CloseReceivableCents == 0 && b.ClosePayableCents == 0) {
				return bad()
			}
		}
		if i > 0 {
			p := r.Days[i-1]
			if t.Known.OpenReceivableCents != p.Timing.Known.CloseReceivableCents || t.Known.OpenPayableCents != p.Timing.Known.ClosePayableCents || a.OtherOpeningCents != p.Account.OtherClosingCents {
				return bad()
			}
		}
		active := []string{}
		for _, c := range d.Rows {
			if (c.SourceDate <= previous && previous < c.LastPosting) || (c.SourceDate <= t.TradeDate && t.TradeDate < c.LastPosting) {
				active = append(active, c.Profit.CycleID)
			}
		}
		sort.Strings(active)
		state := "no_observed_timing_exposure"
		if len(active) > 0 {
			state = "observed_timing_exposure"
		}
		if !reflect.DeepEqual(active, t.ActiveCycleIDs) || state != t.TimingState || (len(active) > 0) != (timingExposed(t.Known) || timingExposed(t.Inferred)) || t.AdjustmentCents != t.Known.NetCarryAdjustmentCents+t.Inferred.NetCarryAdjustmentCents || a.TimingAdjustmentCents != t.AdjustmentCents || a.PublishedPnLCents != a.BrokerPnLCents+t.AdjustmentCents || a.NoObservedTimingOrOther != (len(active) == 0 && a.OtherOpeningCents == 0 && a.OtherClosingCents == 0) {
			return bad()
		}
		previous = t.TradeDate
	}
	last := r.Days[len(r.Days)-1].Timing
	if last.Known.PendingLegs != 0 || last.Inferred.PendingLegs != 0 {
		return bad()
	}
	return nil
}
