package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

func reviewFuyingObservedCash(m fuyingCashMatrix, dates []string) (performance.ETFCashTimingReview, error) {
	if m.AccountID != fuying13Account || m.Publishable || m.Overlap || m.AllClosed || m.Writes != 0 || m.Commands != 0 {
		return performance.ETFCashTimingReview{}, errors.New("unexpected cash matrix evidence scope")
	}
	seen := map[string]bool{}
	legs := make([]performance.ObservedETFCashLeg, 0, len(m.Resolved))
	for _, r := range m.Resolved {
		l, err := fuyingTimingLeg(r, r.SourceDate)
		if err != nil || seen[r.FlowID] {
			return performance.ETFCashTimingReview{}, errors.New("resolved cash identity/date/amount conflict")
		}
		seen[r.FlowID] = true
		legs = append(legs, performance.ObservedETFCashLeg{AccountID: r.AccountID, FlowID: r.FlowID,
			SourceDate: l.Source, PostedDate: l.Posted, AmountCents: l.Cents, SourceEvidence: r.Reason,
			EvidenceRef: "sha256:" + fuyingCashMatrixHash + "#" + r.FlowID})
	}
	for _, r := range m.Unresolved {
		if r.AccountID != fuying13Account || r.FlowID == "" || seen[r.FlowID] || r.SourceDate != "" {
			return performance.ETFCashTimingReview{}, errors.New("unresolved cash promoted or reused")
		}
		seen[r.FlowID] = true
	}
	return performance.ReviewETFCashTiming(fuying13Account, "2026-10-09", dates, legs)
}

func runFuyingCashTiming(args []string) error {
	flags := flag.NewFlagSet("performance-fuying13-cash-timing-20261113", flag.ContinueOnError)
	output := flags.String("output", "", "new private observed cash timing report; offline only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	var matrix fuyingCashMatrix
	if err := readPinnedJSON(fuyingCashMatrixPath, fuyingCashMatrixHash, &matrix); err != nil {
		return err
	}
	var nav struct {
		Account   string              `json:"account_id"`
		Scenarios []fuyingNAVScenario `json:"cash_nav_scenarios"`
	}
	if err := readPinnedJSON("outputs/recovery/fuying13_cash_matrix_20261112/nav_scenarios_v1.json", fuyingCashNAVHash, &nav); err != nil {
		return err
	}
	if matrix.Schema != "fuying13_settlement_matrix.v1" || len(matrix.Resolved) != 3606 || len(matrix.Unresolved) != 10 ||
		nav.Account != fuying13Account || len(nav.Scenarios) != 2 || nav.Scenarios[0].Name != "explicit_source_cash_only" || len(nav.Scenarios[0].Days) != 21 {
		return errors.New("reviewed evidence coverage changed")
	}
	var dates []string
	for _, d := range nav.Scenarios[0].Days {
		dates = append(dates, d.Date)
	}
	bridge, err := reviewFuyingObservedCash(matrix, dates)
	if err != nil {
		return err
	}
	for i, d := range bridge.Days {
		old := nav.Scenarios[0].Days[i]
		op, e1 := fuyingRepoScaled(old.KnownOpen, 100)
		cl, e2 := fuyingRepoScaled(old.KnownClose, 100)
		if e1 != nil || e2 != nil || d.OpenReceivableCents-d.OpenPayableCents != op || d.CloseReceivableCents-d.ClosePayableCents != cl {
			return fmt.Errorf("independent cash timing comparison failed %s", d.TradeDate)
		}
	}
	report := map[string]any{"account_id": fuying13Account, "generated_at": timeutil.Now(), "bridge": bridge,
		"resolved_count": len(matrix.Resolved), "unresolved_count": len(matrix.Unresolved), "unresolved_cash": matrix.Unresolved,
		"source_hashes": map[string]string{"matrix": fuyingCashMatrixHash, "nav": fuyingCashNAVHash},
		"observed_timing_matches_frozen_python_review": true, "complete_settlement": false,
		"economic_nav_publishable": false, "database_writes": 0, "broker_commands": 0,
		"scope": "explicit-source observed cash only; positive and negative carries separate; candidates excluded; not a final settlement"}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	return writeJSON(map[string]any{"days": len(bridge.Days), "resolved_count": len(matrix.Resolved),
		"unresolved_count": len(matrix.Unresolved), "database_writes": 0, "economic_nav_publishable": false})
}
