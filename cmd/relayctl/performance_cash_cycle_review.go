package main

import (
	"errors"
	"fmt"
	"strings"

	"ti-relay-trader/internal/performance"
)

type cashCycleAccountDay struct {
	TradeDate               string   `json:"trade_date"`
	BrokerPnLCents          int64    `json:"broker_basis_pnl_cents"`
	PublishedPnLCents       int64    `json:"published_pnl_cents"`
	TimingAdjustmentCents   int64    `json:"conditional_timing_adjustment_cents"`
	OtherOpeningCents       int64    `json:"unclassified_open_component_cents"`
	OtherClosingCents       int64    `json:"unclassified_close_component_cents"`
	NoObservedTimingOrOther bool     `json:"no_observed_timing_or_component_exposure"`
	OriginalFlags           []string `json:"published_quality_flags_preserved"`
}

func fuyingCycleLegs(m fuyingCashMatrix) ([]performance.ETFCycleCashLeg, error) {
	if m.AccountID != fuying13Account || m.Publishable || m.Overlap || m.AllClosed || m.Writes != 0 || m.Commands != 0 {
		return nil, errors.New("unexpected cash cycle evidence scope")
	}
	known, inferred, err := fuyingMatrixLegs(m)
	if err != nil {
		return nil, err
	}
	var result []performance.ETFCycleCashLeg
	for i, l := range known {
		r := m.Resolved[i]
		result = append(result, performance.ETFCycleCashLeg{SecurityID: r.SecurityID,
			ObservedETFCashLeg: performance.ObservedETFCashLeg{AccountID: r.AccountID, FlowID: l.ID,
				SourceDate: l.Source, PostedDate: l.Posted, AmountCents: l.Cents, SourceEvidence: r.Reason,
				EvidenceRef: "sha256:" + fuyingCashMatrixHash + "#" + l.ID}})
	}
	security := map[string]string{}
	for _, g := range m.PCF.Candidates {
		for _, id := range g.FlowIDs {
			security[id] = g.Matches[0].SecurityID
		}
	}
	for _, l := range inferred {
		result = append(result, performance.ETFCycleCashLeg{SecurityID: security[l.ID],
			ObservedETFCashLeg: performance.ObservedETFCashLeg{AccountID: m.AccountID, FlowID: l.ID,
				SourceDate: l.Source, PostedDate: l.Posted, AmountCents: l.Cents, SourceEvidence: performance.InferredETFCashSource,
				EvidenceRef: "sha256:" + fuyingCashMatrixHash + "#candidate/" + l.ID}})
	}
	return result, nil
}

func compareCashCyclePublication(p provisionalRecoveryPlan, cycles performance.ETFCashCycleReview) ([]cashCycleAccountDay, error) {
	if p.Account != cycles.AccountID || p.Anchor.TradeDate != cycles.OpeningDate || len(p.Assets) == 0 || len(p.Assets) != len(cycles.Days) || len(p.NAVs) != len(p.Assets) || len(p.Dates) != len(p.Assets) {
		return nil, errors.New("cash cycle publication coverage mismatch")
	}
	rebuilt, err := performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	if err != nil {
		return nil, err
	}
	previous, err := fuyingRepoScaled(p.Anchor.CloseEconomicNAV, 100)
	if err != nil {
		return nil, err
	}
	var other int64
	var result []cashCycleAccountDay
	for i, a := range p.Assets {
		d, n := cycles.Days[i], p.NAVs[i]
		if a.Date != d.TradeDate || !sameProvisionalNAV(rebuilt[i], n) || n.ExternalNetFlow != 0 ||
			a.ReceivableCents != d.Known.CloseReceivableCents || a.PayableCents != d.Known.ClosePayableCents ||
			a.InferredReceivableCents != d.Inferred.CloseReceivableCents || a.InferredPayableCents != d.Inferred.ClosePayableCents {
			return nil, fmt.Errorf("cycle timing differs from reviewed publication on %s", a.Date)
		}
		close, e1 := fuyingRepoScaled(a.ReportedClose, 100)
		pnl, e2 := fuyingRepoScaled(n.AccountDayPnL, 100)
		if e1 != nil || e2 != nil || pnl != close-previous+d.AdjustmentCents {
			return nil, fmt.Errorf("broker/published PnL timing identity failed on %s", a.Date)
		}
		result = append(result, cashCycleAccountDay{TradeDate: a.Date, BrokerPnLCents: close - previous,
			PublishedPnLCents: pnl, TimingAdjustmentCents: d.AdjustmentCents, OtherOpeningCents: other,
			OtherClosingCents: a.OtherCents, NoObservedTimingOrOther: len(d.ActiveCycleIDs) == 0 && other == 0 && a.OtherCents == 0,
			OriginalFlags: append([]string(nil), n.QualityFlags...)})
		previous, other = close, a.OtherCents
	}
	return result, nil
}

func runFuyingCashCycleReview(path, digest, output string) error {
	var p provisionalRecoveryPlan
	if err := readProvisionalPlan(path, digest, &p); err != nil {
		return err
	}
	if digest != "dc38a0f24aea64c40da7735e76eb32cdf6790a2a9fdb3a501b8fe49c7ecfe8f7" || p.Account != fuying13Account || len(p.Dates) != 25 || p.Dates[24] != "2026-11-13" || p.InputSHA["cash_matrix"] != fuyingCashMatrixHash {
		return errors.New("cycle review requires the published November 13 evidence version")
	}
	var m fuyingCashMatrix
	if err := readPinnedJSON(fuyingCashMatrixPath, fuyingCashMatrixHash, &m); err != nil {
		return err
	}
	// The older matrix ends on November 9. Extend only using the reviewed cash
	// refresh that explicitly found no new ETF legs, not by assuming silence.
	var audit brokerRefreshAudit
	if err := readPinnedJSON("outputs/recovery/fuying13_broker_refresh_20261115/review-v2/audit.json", p.InputSHA["broker_refresh_audit"], &audit); err != nil {
		return err
	}
	if audit.Account != p.Account || audit.Final || audit.CashOverlap.Rows != 12535 || audit.CashOverlap.Conflicts != 0 || len(audit.NewCash) != 345 || len(m.Resolved) != 3606 || len(m.Unresolved) != 10 {
		return errors.New("cash cycle extension coverage changed")
	}
	for _, r := range audit.NewCash {
		if r.ETF || r.Raw["投资者"] != p.Account || r.Raw["交易日期"] < "20261110" || r.Raw["交易日期"] > "20261113" {
			return errors.New("unreviewed cash in cycle extension")
		}
	}
	for _, a := range p.Assets {
		funds := audit.Days[strings.ReplaceAll(a.Date, "-", "")].Funds
		close, e1 := parseBrokerMoneyCents(funds["总资产"])
		in, e2 := parseBrokerMoneyCents(funds["入金"])
		out, e3 := parseBrokerMoneyCents(funds["出金"])
		want, e4 := fuyingRepoScaled(a.ReportedClose, 100)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || close != want || in != 0 || out != 0 {
			return errors.New("cash cycle review requires matching assets and reviewed zero external flows")
		}
	}
	anchor, err := parseBrokerMoneyCents(audit.Days["20261009"].Funds["总资产"])
	if err != nil || float64(anchor)/100 != p.Anchor.CloseEconomicNAV {
		return errors.New("opening broker asset differs from published anchor")
	}
	legs, err := fuyingCycleLegs(m)
	if err != nil {
		return err
	}
	cycles, err := performance.ReviewETFCashCycles(p.Account, p.Anchor.TradeDate, p.Dates, legs)
	if err != nil {
		return err
	}
	days, err := compareCashCyclePublication(p, cycles)
	if err != nil {
		return err
	}
	unaffected := []string{}
	var brokerPnL, publishedPnL int64
	for _, d := range days {
		brokerPnL += d.BrokerPnLCents
		publishedPnL += d.PublishedPnLCents
		if d.NoObservedTimingOrOther {
			unaffected = append(unaffected, d.TradeDate)
		}
	}
	report := map[string]any{"schema": "relay.etf_cash_cycle_comparison.v1", "account_id": p.Account,
		"cycles": cycles, "account_days": days, "no_observed_timing_or_component_exposure_dates": unaffected,
		"broker_period_pnl_cents": brokerPnL, "published_period_pnl_cents": publishedPnL,
		"publication_plan_sha256": digest, "source_sha256": p.InputSHA,
		"scope":                    "observed cash timing cohorts, not full T0 profit or proof of complete private settlement; no fee or cash counted twice",
		"economic_nav_publishable": false, "database_writes": 0, "broker_commands": 0}
	if err := writeETFTransferReviewFile(output, report); err != nil {
		return err
	}
	return writeJSON(map[string]any{"days": len(days), "cycles": len(cycles.Cycles), "unaffected_dates": unaffected,
		"period_timing_adjustment_cents": cycles.PeriodAdjustmentCents, "database_writes": 0})
}
