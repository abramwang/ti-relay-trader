package performance

import (
	"errors"
	"strings"
	"time"
)

// ObservedETFCashLeg represents actual observed cash, not a predicted payment.
// Its source association must be verified by the caller against raw evidence.
type ObservedETFCashLeg struct {
	AccountID      string `json:"account_id"`
	FlowID         string `json:"flow_id"`
	SourceDate     string `json:"source_date"`
	PostedDate     string `json:"posted_date"`
	AmountCents    int64  `json:"amount_cents"`
	SourceEvidence string `json:"source_evidence"`
	EvidenceRef    string `json:"evidence_ref"`
}

type ETFCashTimingDay struct {
	TradeDate               string `json:"trade_date"`
	OpenReceivableCents     int64  `json:"open_receivable_cents"`
	OpenPayableCents        int64  `json:"open_payable_cents"`
	SourceCashCents         int64  `json:"source_cash_cents"`
	PostedCashCents         int64  `json:"posted_cash_cents"`
	SameDayIncludedCents    int64  `json:"same_day_included_cents"`
	CloseReceivableCents    int64  `json:"close_receivable_cents"`
	ClosePayableCents       int64  `json:"close_payable_cents"`
	PendingLegs             int    `json:"pending_legs"`
	NetCarryAdjustmentCents int64  `json:"net_carry_adjustment_cents"`
}

type ETFCashTimingReview struct {
	AccountID              string             `json:"account_id"`
	OpeningDate            string             `json:"opening_date"`
	ObservedThrough        string             `json:"observed_through"`
	LegCount               int                `json:"leg_count"`
	Days                   []ETFCashTimingDay `json:"days"`
	CompleteSettlement     bool               `json:"complete_settlement"`
	EconomicNAVPublishable bool               `json:"economic_nav_publishable"`
	QualityFlags           []string           `json:"quality_flags"`
}

// ReviewETFCashTiming reconstructs timing for explicitly associated, observed
// legs only. Zero final carry proves those legs were paid, not that every fund
// settlement item has been observed. No dates or payments are inferred.
func ReviewETFCashTiming(account, opening string, dates []string, legs []ObservedETFCashLeg) (ETFCashTimingReview, error) {
	return reviewETFCashTiming(account, opening, dates, legs, false)
}

func reviewETFCashTiming(account, opening string, dates []string, legs []ObservedETFCashLeg, allowInferred bool) (ETFCashTimingReview, error) {
	fail := func() (ETFCashTimingReview, error) {
		return ETFCashTimingReview{}, errors.New("invalid ETF observed cash timing evidence")
	}
	validDate := func(value string) bool {
		d, err := time.Parse("2006-01-02", value)
		return err == nil && d.Format("2006-01-02") == value
	}
	if strings.TrimSpace(account) == "" || account != strings.TrimSpace(account) || !validDate(opening) || len(dates) == 0 || len(dates) > 1000 || len(legs) > 100000 {
		return fail()
	}
	daySet := map[string]bool{}
	previous := opening
	for _, date := range dates {
		if !validDate(date) || date <= previous {
			return fail()
		}
		daySet[date], previous = true, date
	}
	end := dates[len(dates)-1]
	type movement struct {
		receivable, payable, source, posted, sameDay int64
		count                                        int
	}
	events := map[string]movement{}
	seen := map[string]bool{}
	var receivable, payable int64
	pending := 0
	for _, leg := range legs {
		// Per-leg bounds and input cardinality bound every aggregate below int64.
		if leg.AccountID != account || strings.TrimSpace(leg.FlowID) == "" || leg.FlowID != strings.TrimSpace(leg.FlowID) || seen[leg.FlowID] || strings.TrimSpace(leg.EvidenceRef) == "" ||
			!validDate(leg.SourceDate) || !validDate(leg.PostedDate) || leg.SourceDate > leg.PostedDate || leg.PostedDate > end ||
			(leg.SourceDate > opening && !daySet[leg.SourceDate]) || (leg.PostedDate > opening && !daySet[leg.PostedDate]) ||
			leg.AmountCents == 0 || leg.AmountCents < -1000000000000 || leg.AmountCents > 1000000000000 {
			return fail()
		}
		switch leg.SourceEvidence {
		case "explicit_source", "explicit_source_and_order", "unique_order_reference_in_reviewed_coverage":
		case "same_etf_prior_redemption_and_unique_pcf_match":
			if !allowInferred {
				return fail()
			}
		default:
			return fail()
		}
		seen[leg.FlowID] = true
		var r, p int64
		if leg.AmountCents > 0 {
			r = leg.AmountCents
		} else {
			p = -leg.AmountCents
		}
		if leg.SourceDate <= opening && leg.PostedDate > opening {
			receivable += r
			payable += p
			pending++
		}
		if leg.SourceDate > opening {
			e := events[leg.SourceDate]
			e.receivable += r
			e.payable += p
			e.source += leg.AmountCents
			e.count++
			if leg.SourceDate == leg.PostedDate {
				e.sameDay += leg.AmountCents
			}
			events[leg.SourceDate] = e
		}
		if leg.PostedDate > opening {
			e := events[leg.PostedDate]
			e.receivable -= r
			e.payable -= p
			e.posted += leg.AmountCents
			e.count--
			events[leg.PostedDate] = e
		}
	}
	result := ETFCashTimingReview{AccountID: account, OpeningDate: opening, ObservedThrough: end, LegCount: len(legs),
		QualityFlags: []string{"observed_etf_cash_only", "settlement_coverage_unverified", "asset_inclusion_requires_separate_evidence"}}
	if allowInferred {
		result.QualityFlags = append(result.QualityFlags, "cash_source_date_inferred")
	}
	for _, date := range dates {
		e := events[date]
		d := ETFCashTimingDay{TradeDate: date, OpenReceivableCents: receivable, OpenPayableCents: payable,
			SourceCashCents: e.source, PostedCashCents: e.posted, SameDayIncludedCents: e.sameDay}
		receivable += e.receivable
		payable += e.payable
		pending += e.count
		d.CloseReceivableCents, d.ClosePayableCents, d.PendingLegs = receivable, payable, pending
		d.NetCarryAdjustmentCents = receivable - payable - d.OpenReceivableCents + d.OpenPayableCents
		if receivable < 0 || payable < 0 || pending < 0 || d.PostedCashCents+d.NetCarryAdjustmentCents != d.SourceCashCents {
			return fail()
		}
		result.Days = append(result.Days, d)
	}
	return result, nil
}
