package performance

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"

	"ti-relay-trader/internal/ledger"
)

const CycleReviewSchema = "relay.etf_cycle_review.v1"

var cycleEvidenceHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

type CycleReviewSource struct {
	AccountID   string             `json:"account_id"`
	Writes      int                `json:"database_writes"`
	Commands    int                `json:"broker_commands"`
	Publishable bool               `json:"economic_nav_publishable"`
	Cycles      ETFCashCycleReview `json:"cycles"`
	Profit      struct {
		Profits     []ETFCycleProfit       `json:"profits"`
		Publishable bool                   `json:"economic_nav_publishable"`
		Components  []CycleComponentReview `json:"component_reviews,omitempty"`
	} `json:"cycle_profit_review"`
}
type CycleReviewRow struct {
	SourceDate   string                `json:"source_date"`
	SecurityID   string                `json:"security_id"`
	LastPosting  string                `json:"last_observed_posting"`
	KnownLegs    int                   `json:"known_source_legs"`
	InferredLegs int                   `json:"inferred_source_legs"`
	Profit       ETFCycleProfit        `json:"profit"`
	Components   *CycleComponentReview `json:"component_review,omitempty"`
}
type CycleReviewDocument struct {
	Schema          string           `json:"schema"`
	AccountID       string           `json:"account_id"`
	VersionID       string           `json:"version_id"`
	DateFrom        string           `json:"date_from"`
	ObservedThrough string           `json:"observed_through"`
	SourceSHA256    string           `json:"source_sha256"`
	NAVPublishable  bool             `json:"account_nav_publishable"`
	FinalSettlement bool             `json:"final_settlement"`
	Rows            []CycleReviewRow `json:"cycles"`
}

// A separate immutable publication, never an update to daily NAV or fills.
func BuildCycleReviewVersion(source CycleReviewSource, version, sourceSHA string) (ledger.CycleReviewVersion, error) {
	if source.AccountID == "" || source.Cycles.AccountID != source.AccountID || source.Writes != 0 || source.Commands != 0 || source.Publishable || source.Profit.Publishable {
		return ledger.CycleReviewVersion{}, errors.New("invalid cycle review source scope")
	}
	trades := make([]ETFCycleTradingEvidence, len(source.Profit.Profits))
	for i, p := range source.Profit.Profits {
		trades[i] = p.Trading
	}
	computed, err := CalculateETFCycleProfits(source.Cycles, trades)
	if err != nil {
		return ledger.CycleReviewVersion{}, err
	}
	if !reflect.DeepEqual(computed, source.Profit.Profits) {
		return ledger.CycleReviewVersion{}, errors.New("cycle profit report differs from verified formula")
	}
	d := CycleReviewDocument{Schema: CycleReviewSchema, AccountID: source.AccountID, VersionID: version, ObservedThrough: source.Cycles.ObservedThrough, SourceSHA256: sourceSHA, Rows: []CycleReviewRow{}}
	for _, p := range computed {
		for _, c := range source.Cycles.Cycles {
			if c.CycleID == p.CycleID {
				d.Rows = append(d.Rows, CycleReviewRow{SourceDate: c.SourceDate, SecurityID: c.SecurityID, LastPosting: c.LastObservedPosting, KnownLegs: c.KnownSourceLegs, InferredLegs: c.InferredSourceLegs, Profit: p})
				if d.DateFrom == "" || c.SourceDate < d.DateFrom {
					d.DateFrom = c.SourceDate
				}
			}
		}
	}
	if len(source.Profit.Components) != 0 {
		if len(source.Profit.Components) != len(d.Rows) {
			return ledger.CycleReviewVersion{}, errors.New("component review coverage mismatch")
		}
		for i := range d.Rows {
			c := source.Profit.Components[i]
			d.Rows[i].Components = &c
		}
	}
	if err := ValidateCycleReview(d); err != nil {
		return ledger.CycleReviewVersion{}, err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return ledger.CycleReviewVersion{}, err
	}
	return ledger.CycleReviewVersion{AccountID: d.AccountID, VersionID: d.VersionID, DateFrom: d.DateFrom, ObservedThrough: d.ObservedThrough, Payload: b, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256(b))}, nil
}

func ValidateCycleReview(d CycleReviewDocument) error {
	fail := func() error { return errors.New("invalid published cycle review") }
	if d.Schema != CycleReviewSchema || d.AccountID == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,159}$`).MatchString(d.VersionID) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(d.SourceSHA256) || d.NAVPublishable || d.FinalSettlement || len(d.Rows) == 0 || len(d.Rows) > 1000 {
		return fail()
	}
	for _, s := range []string{d.DateFrom, d.ObservedThrough} {
		v, e := time.Parse("2006-01-02", s)
		if e != nil || v.Format("2006-01-02") != s {
			return fail()
		}
	}
	if d.DateFrom > d.ObservedThrough {
		return fail()
	}
	previous := ""
	for _, r := range d.Rows {
		p, t := r.Profit, r.Profit.Trading
		for _, s := range []string{r.SourceDate, r.LastPosting, t.AcquisitionDate} {
			v, e := time.Parse("2006-01-02", s)
			if e != nil || v.Format("2006-01-02") != s {
				return fail()
			}
		}
		if r.SourceDate < d.DateFrom || r.LastPosting < r.SourceDate || r.LastPosting > d.ObservedThrough || t.AcquisitionDate > r.SourceDate || !cashCycleSecurity.MatchString(r.SecurityID) || p.CycleID != r.SourceDate+"/"+r.SecurityID || p.CycleID <= previous || t.CycleID != p.CycleID || t.AccountID != d.AccountID || p.AccountNAVPublishable || p.FinalSettlement || r.KnownLegs < 0 || r.InferredLegs < 0 || r.KnownLegs+r.InferredLegs == 0 {
			return fail()
		}
		for _, n := range []int64{t.AcquisitionGrossCents, t.ComponentSaleCents, t.LaterComponentSales, t.OrdinaryFeeCents} {
			if n < 0 || n > 100000000000000 {
				return fail()
			}
		}
		if t.AcquisitionQuantity <= 0 || t.AcquisitionQuantity != t.RedemptionQuantity || t.AcquisitionGrossCents <= 0 || t.FeeScope != "ordinary_buys_and_component_sales_only" || (t.FeeBasis != "broker_cash_reconciled" && t.FeeBasis != "oc_fee_snapshot_not_final") || p.ObservedNetCashCents < -100000000000000000 || p.ObservedNetCashCents > 100000000000000000 || p.NetCyclePnLCents != t.ComponentSaleCents+t.LaterComponentSales+p.ObservedNetCashCents-t.AcquisitionGrossCents-t.OrdinaryFeeCents {
			return fail()
		}
		expected := "estimated"
		if t.FeeBasis == "broker_cash_reconciled" && r.InferredLegs == 0 && len(t.QualityFlags) == 0 {
			expected = "observed_cash_reconciled"
		}
		if p.Status != expected || !containsStringValue(p.QualityFlags, "private_settlement_coverage_unverified") || !containsStringValue(p.QualityFlags, "observed_cycle_profit_not_account_daily_pnl") {
			return fail()
		}
		previous = p.CycleID
		if r.Components != nil {
			if err := validateCycleComponentReview(*r.Components, r, d.ObservedThrough); err != nil {
				return err
			}
		}
	}
	if d.DateFrom != d.Rows[0].SourceDate {
		return fail()
	}
	return nil
}
