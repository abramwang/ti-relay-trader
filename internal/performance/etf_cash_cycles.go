package performance

import (
	"errors"
	"regexp"
	"sort"
)

const InferredETFCashSource = "same_etf_prior_redemption_and_unique_pcf_match"

// A cycle is an account/source-day/security cohort, not an invented allocation
// of pooled refunds to individual redemption orders.
type ETFCycleCashLeg struct {
	ObservedETFCashLeg
	SecurityID string `json:"security_id"`
}

type ETFCashCycle struct {
	CycleID             string   `json:"cycle_id"`
	SecurityID          string   `json:"security_id"`
	SourceDate          string   `json:"source_date"`
	LastObservedPosting string   `json:"last_observed_posting"`
	KnownSourceLegs     int      `json:"known_source_legs"`
	InferredSourceLegs  int      `json:"inferred_source_legs"`
	ReceivedCents       int64    `json:"observed_received_cents"`
	PaidCents           int64    `json:"observed_paid_cents"`
	NetCashCents        int64    `json:"observed_net_cash_cents"`
	SameDayCashCents    int64    `json:"same_day_already_in_cash_cents"`
	FlowIDs             []string `json:"flow_ids"`
	CompleteSettlement  bool     `json:"complete_settlement"`
}

type ETFCashCycleDay struct {
	TradeDate          string           `json:"trade_date"`
	Known              ETFCashTimingDay `json:"known_source"`
	Inferred           ETFCashTimingDay `json:"inferred_source"`
	ActiveCycleIDs     []string         `json:"timing_exposed_cycle_ids"`
	TimingState        string           `json:"timing_state"`
	AdjustmentCents    int64            `json:"conditional_timing_adjustment_cents"`
	AccountNAVVerified bool             `json:"account_nav_verified"`
}

type ETFCashCycleReview struct {
	AccountID              string            `json:"account_id"`
	OpeningDate            string            `json:"opening_date"`
	ObservedThrough        string            `json:"observed_through"`
	KnownSourceLegs        int               `json:"known_source_legs"`
	InferredSourceLegs     int               `json:"inferred_source_legs"`
	Cycles                 []ETFCashCycle    `json:"cycles"`
	Days                   []ETFCashCycleDay `json:"days"`
	PeriodAdjustmentCents  int64             `json:"conditional_period_adjustment_cents"`
	EconomicNAVPublishable bool              `json:"economic_nav_publishable"`
	QualityFlags           []string          `json:"quality_flags"`
}

var cashCycleSecurity = regexp.MustCompile(`^[0-9]{6}\.(SH|SZ)$`)

// ReviewETFCashCycles separates cash observations from inferred timing and
// limits exposure to the supplied legs. It never treats a zero balance as proof
// that all private settlement items have been observed.
func ReviewETFCashCycles(account, opening string, dates []string, legs []ETFCycleCashLeg) (ETFCashCycleReview, error) {
	fail := func() (ETFCashCycleReview, error) {
		return ETFCashCycleReview{}, errors.New("invalid ETF cash cycle evidence")
	}
	if len(legs) > 100000 {
		return fail()
	}
	known, inferred := []ObservedETFCashLeg{}, []ObservedETFCashLeg{}
	seen := map[string]bool{}
	for _, leg := range legs {
		if !cashCycleSecurity.MatchString(leg.SecurityID) || seen[leg.FlowID] {
			return fail()
		}
		seen[leg.FlowID] = true
		if leg.SourceEvidence == InferredETFCashSource {
			inferred = append(inferred, leg.ObservedETFCashLeg)
		} else {
			known = append(known, leg.ObservedETFCashLeg)
		}
	}
	k, err := ReviewETFCashTiming(account, opening, dates, known)
	if err != nil {
		return fail()
	}
	i, err := reviewETFCashTiming(account, opening, dates, inferred, true)
	if err != nil {
		return fail()
	}
	cycles := map[string]*ETFCashCycle{}
	active := make([]map[string]bool, len(dates))
	for n := range active {
		active[n] = map[string]bool{}
	}
	for _, leg := range legs {
		id := leg.SourceDate + "/" + leg.SecurityID
		c := cycles[id]
		if c == nil {
			if len(cycles) >= 1000 {
				return fail()
			}
			c = &ETFCashCycle{CycleID: id, SourceDate: leg.SourceDate, SecurityID: leg.SecurityID}
			cycles[id] = c
		}
		if leg.PostedDate > c.LastObservedPosting {
			c.LastObservedPosting = leg.PostedDate
		}
		c.FlowIDs = append(c.FlowIDs, leg.FlowID)
		if leg.SourceEvidence == InferredETFCashSource {
			c.InferredSourceLegs++
		} else {
			c.KnownSourceLegs++
		}
		if leg.AmountCents > 0 {
			c.ReceivedCents += leg.AmountCents
		} else {
			c.PaidCents -= leg.AmountCents
		}
		c.NetCashCents += leg.AmountCents
		if leg.SourceDate == leg.PostedDate {
			c.SameDayCashCents += leg.AmountCents
		}
	}
	result := ETFCashCycleReview{AccountID: account, OpeningDate: opening, ObservedThrough: k.ObservedThrough,
		KnownSourceLegs: len(known), InferredSourceLegs: len(inferred), Cycles: []ETFCashCycle{},
		QualityFlags: []string{"observed_cash_not_cycle_profit", "settlement_coverage_unverified", "asset_inclusion_requires_separate_evidence", "strategy_attribution_separate"}}
	for _, c := range cycles {
		previous := opening
		for n, date := range dates {
			// All legs share a source day. The latest observed posting bounds
			// their union; include opening exposure on the release day.
			if (c.SourceDate <= previous && previous < c.LastObservedPosting) || (c.SourceDate <= date && date < c.LastObservedPosting) {
				active[n][c.CycleID] = true
			}
			previous = date
		}
		sort.Strings(c.FlowIDs)
		result.Cycles = append(result.Cycles, *c)
	}
	sort.Slice(result.Cycles, func(a, b int) bool { return result.Cycles[a].CycleID < result.Cycles[b].CycleID })
	for n, date := range dates {
		d := ETFCashCycleDay{TradeDate: date, Known: k.Days[n], Inferred: i.Days[n], ActiveCycleIDs: []string{}, TimingState: "no_observed_timing_exposure"}
		for id := range active[n] {
			d.ActiveCycleIDs = append(d.ActiveCycleIDs, id)
		}
		sort.Strings(d.ActiveCycleIDs)
		if len(d.ActiveCycleIDs) > 0 {
			d.TimingState = "observed_timing_exposure"
		}
		d.AdjustmentCents = d.Known.NetCarryAdjustmentCents + d.Inferred.NetCarryAdjustmentCents
		result.PeriodAdjustmentCents += d.AdjustmentCents
		result.Days = append(result.Days, d)
	}
	return result, nil
}
