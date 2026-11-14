package main

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

type fuyingAccountBridgeDay struct {
	Date                  string                       `json:"trade_date"`
	ReportedOpen          int64                        `json:"reported_open_cents"`
	ReportedClose         int64                        `json:"reported_close_cents"`
	OtherComponent        int64                        `json:"unclassified_broker_component_cents"`
	CashTiming            performance.ETFCashTimingDay `json:"observed_cash_timing"`
	ConditionalOpen       int64                        `json:"conditional_open_cents"`
	ConditionalClose      int64                        `json:"conditional_close_cents"`
	ConditionalPnL        int64                        `json:"conditional_pnl_cents"`
	UnresolvedPostedFlows []string                     `json:"unresolved_source_flows_posted"`
	LocalReviewFlags      []string                     `json:"local_review_flags"`
}

type fuyingBoundaryRelease struct {
	PostedDate      string   `json:"posted_date"`
	FlowIDs         []string `json:"flow_ids"`
	InflowCents     int64    `json:"inflow_cents"`
	OutflowCents    int64    `json:"outflow_cents"`
	NetReleaseCents int64    `json:"net_release_cents"`
}

type fuyingAccountBridgeReview struct {
	Schema                 string                   `json:"schema"`
	AccountID              string                   `json:"account_id"`
	Through                string                   `json:"reviewed_through"`
	PublishedPK            int64                    `json:"published_nav_pk"`
	PublishedDate          string                   `json:"published_boundary_date"`
	PublishedClose         int64                    `json:"published_close_cents"`
	ReportedBoundary       int64                    `json:"reported_boundary_cents"`
	ConditionalBoundary    int64                    `json:"conditional_boundary_cents"`
	BoundaryDifference     int64                    `json:"conditional_minus_published_cents"`
	NextOpening            int64                    `json:"next_conditional_open_cents"`
	EarliestTailSource     string                   `json:"earliest_observed_tail_source"`
	BoundaryTail           []fuyingMatrixCash       `json:"observed_boundary_tail"`
	BoundaryReleases       []fuyingBoundaryRelease  `json:"observed_boundary_releases"`
	UnresolvedCash         []fuyingMatrixCash       `json:"unresolved_source_cash_excluded"`
	Days                   []fuyingAccountBridgeDay `json:"days"`
	QualityFlags           []string                 `json:"quality_flags"`
	SourceHashes           map[string]string        `json:"source_sha256"`
	EconomicNAVPublishable bool                     `json:"economic_nav_publishable"`
}

// This joins broker totals and observed cash timing, never PCF-only candidates.
// The resulting amounts are conditional: source identity does not prove that
// a receivable is outside the broker total, or that all private cash is known.
func reviewFuyingAccountBridge(bases []brokerBasis20261110Day, matrix fuyingCashMatrix, nav ledger.PerformanceNAV) (fuyingAccountBridgeReview, error) {
	fail := func(reason string) (fuyingAccountBridgeReview, error) {
		return fuyingAccountBridgeReview{}, fmt.Errorf("account NAV bridge: %s", reason)
	}
	if len(bases) < 2 || len(bases) > 1000 || nav.AccountID != fuying13Account || nav.PerformanceNAVPK <= 0 ||
		!nav.IsCurrent || nav.Status != "provisional" || nav.Version <= 0 || nav.FormulaVersion != fuyingContinuousFormula {
		return fail("invalid basis or published boundary identity")
	}
	published, err := fuyingRepoScaled(nav.CloseEconomicNAV, 100)
	if err != nil || published <= 0 {
		return fail("invalid published asset")
	}
	dates := make([]string, len(bases))
	for i, b := range bases {
		dates[i] = b.Date
		if b.Account != fuying13Account {
			return fail("wrong basis account")
		}
	}
	timing, err := reviewFuyingObservedCash(matrix, dates)
	if err != nil {
		return fail(err.Error())
	}
	result := fuyingAccountBridgeReview{Schema: "fuying13_account_nav_bridge_review.v1", AccountID: fuying13Account,
		Through: dates[len(dates)-1], PublishedPK: nav.PerformanceNAVPK, PublishedDate: nav.TradeDate, PublishedClose: published,
		BoundaryTail: []fuyingMatrixCash{}, UnresolvedCash: matrix.Unresolved,
		QualityFlags: []string{"conditional_not_official_nav", "broker_total_preserved", "asset_inclusion_unverified",
			"private_settlement_coverage_unverified", "repo_interest_inclusion_unverified", "published_boundary_requires_review"}}
	if len(matrix.Unresolved) != 0 {
		result.QualityFlags = append(result.QualityFlags, "unresolved_private_cash_source_excluded")
	}
	posted := map[string][]string{}
	for _, cash := range matrix.Unresolved {
		day := cash.Raw["交易日期"]
		parsed, e1 := time.Parse("20060102", day)
		amount, e2 := parseBrokerMoneyCents(cash.Amount)
		if e1 != nil || parsed.Format("20060102") != day || day < "20261012" || day > "20261109" ||
			!fuyingStrictMoney.MatchString(cash.Amount) || e2 != nil || amount == 0 {
			return fail("unresolved cash posting date or amount invalid")
		}
		posted[parsed.Format("2006-01-02")] = append(posted[parsed.Format("2006-01-02")], cash.FlowID)
	}
	var previousReported, previousConditional, previousOther int64
	boundaryIndex := -1
	for i, b := range bases {
		values := map[string]int64{}
		for _, key := range []string{"reported_open_total_asset", "reported_close_total_asset", "reported_daily_pnl",
			"reported_deposit", "reported_withdrawal", "broker_unclassified_asset_component"} {
			n, err := fuyingBasisCents(b, key)
			if err != nil {
				return fail(err.Error())
			}
			values[key] = n
		}
		op, cl := values["reported_open_total_asset"], values["reported_close_total_asset"]
		other := values["broker_unclassified_asset_component"]
		if op <= 0 || cl <= 0 || values["reported_deposit"] != 0 || values["reported_withdrawal"] != 0 ||
			cl-op != values["reported_daily_pnl"] || (i > 0 && op != previousReported) {
			return fail("broker continuity or external flows outside reviewed scope")
		}
		t := timing.Days[i]
		d := fuyingAccountBridgeDay{Date: b.Date, ReportedOpen: op, ReportedClose: cl, OtherComponent: other, CashTiming: t,
			ConditionalOpen:       op + t.OpenReceivableCents - t.OpenPayableCents,
			ConditionalClose:      cl + t.CloseReceivableCents - t.ClosePayableCents,
			UnresolvedPostedFlows: append([]string{}, posted[b.Date]...), LocalReviewFlags: []string{}}
		sort.Strings(d.UnresolvedPostedFlows)
		d.ConditionalPnL = d.ConditionalClose - d.ConditionalOpen
		if d.ConditionalOpen <= 0 || d.ConditionalClose <= 0 || (i > 0 && d.ConditionalOpen != previousConditional) ||
			d.ConditionalPnL != cl-op+t.NetCarryAdjustmentCents {
			return fail("conditional continuity failed")
		}
		if other != 0 || (i > 0 && previousOther != 0) {
			d.LocalReviewFlags = append(d.LocalReviewFlags, "adjacent_broker_component_unclassified")
		}
		if len(d.UnresolvedPostedFlows) != 0 {
			d.LocalReviewFlags = append(d.LocalReviewFlags, "unresolved_source_cash_posted")
		}
		if b.Date == nav.TradeDate {
			boundaryIndex = i
			result.ReportedBoundary, result.ConditionalBoundary = cl, d.ConditionalClose
			result.BoundaryDifference = d.ConditionalClose - published
		}
		result.Days = append(result.Days, d)
		previousReported, previousConditional, previousOther = cl, d.ConditionalClose, other
	}
	if boundaryIndex < 0 || boundaryIndex+1 == len(bases) {
		return fail("published boundary and its next day must be covered")
	}
	result.NextOpening = result.Days[boundaryIndex+1].ConditionalOpen
	var tailNet, tailReceivable, tailPayable int64
	releases := map[string]*fuyingBoundaryRelease{}
	for _, row := range matrix.Resolved {
		leg, err := fuyingTimingLeg(row, row.SourceDate)
		if err != nil {
			return fail(err.Error())
		}
		if leg.Source <= nav.TradeDate && nav.TradeDate < leg.Posted {
			result.BoundaryTail = append(result.BoundaryTail, row)
			tailNet += leg.Cents
			if releases[leg.Posted] == nil {
				releases[leg.Posted] = &fuyingBoundaryRelease{PostedDate: leg.Posted}
			}
			release := releases[leg.Posted]
			release.FlowIDs = append(release.FlowIDs, row.FlowID)
			release.NetReleaseCents += leg.Cents
			if leg.Cents > 0 {
				tailReceivable += leg.Cents
				release.InflowCents += leg.Cents
			} else {
				tailPayable -= leg.Cents
				release.OutflowCents -= leg.Cents
			}
			if result.EarliestTailSource == "" || leg.Source < result.EarliestTailSource {
				result.EarliestTailSource = leg.Source
			}
		}
	}
	result.BoundaryReleases = []fuyingBoundaryRelease{}
	for _, release := range releases {
		sort.Strings(release.FlowIDs)
		result.BoundaryReleases = append(result.BoundaryReleases, *release)
	}
	sort.Slice(result.BoundaryReleases, func(i, j int) bool {
		return result.BoundaryReleases[i].PostedDate < result.BoundaryReleases[j].PostedDate
	})
	t := timing.Days[boundaryIndex]
	if tailReceivable != t.CloseReceivableCents || tailPayable != t.ClosePayableCents ||
		result.ConditionalBoundary != result.ReportedBoundary+tailNet || result.NextOpening != result.ConditionalBoundary {
		return fail("boundary tail does not match next opening")
	}
	sort.Slice(result.BoundaryTail, func(i, j int) bool { return result.BoundaryTail[i].FlowID < result.BoundaryTail[j].FlowID })
	return result, nil
}

func loadFuyingAccountBridge(matrix fuyingCashMatrix, path, digest string) (fuyingAccountBridgeReview, error) {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Count int                     `json:"count"`
			NAVs  []ledger.PerformanceNAV `json:"navs"`
		} `json:"data"`
	}
	if err := readPinnedJSON(path, digest, &envelope); err != nil {
		return fuyingAccountBridgeReview{}, err
	}
	if !envelope.OK || envelope.Data.Count != 1 || len(envelope.Data.NAVs) != 1 {
		return fuyingAccountBridgeReview{}, errors.New("published boundary response must contain exactly one NAV")
	}
	spec := brokerBasis20261110Specs[0]
	funds, fh, err := readBrokerCSV("reference/" + fuying13Account + "_资金.csv")
	if err != nil || fh != spec.FundsHash || matrix.Hashes["funds"] != fh {
		return fuyingAccountBridgeReview{}, errors.New("reviewed broker funds changed")
	}
	flows, ch, err := readBrokerCSV("reference/" + fuying13Account + "_资金流水.csv")
	if err != nil || ch != spec.FlowsHash || matrix.Hashes["cash_flows"] != ch {
		return fuyingAccountBridgeReview{}, errors.New("reviewed broker cash changed")
	}
	bases, err := buildBrokerBasis20261110(spec, funds, flows)
	if err != nil {
		return fuyingAccountBridgeReview{}, err
	}
	report, err := reviewFuyingAccountBridge(bases, matrix, envelope.Data.NAVs[0])
	if err != nil {
		return report, err
	}
	report.SourceHashes = map[string]string{"funds": fh, "cash_flows": ch, "matrix": fuyingCashMatrixHash, "published_boundary": digest}
	return report, nil
}
