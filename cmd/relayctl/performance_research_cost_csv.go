package main

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func researchCSVText(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed != "" && strings.ContainsAny(trimmed[:1], "=+-@") {
		return "'" + s
	}
	return s
}

func writeResearchCostCSV(path string, r performance.ResearchCostComparison) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	writeErr := w.Write([]string{"account_id", "version_id", "trade_date", "security_id", "cost_bucket", "basis_kind", "outcome", "anchor_and_allocations_match", "stored_day_missing", "research_open_quantity", "stored_open_quantity", "research_open_cost", "stored_open_cost", "research_close_quantity", "stored_close_quantity", "research_close_cost", "stored_close_cost", "research_realized_pnl", "stored_realized_pnl", "delta_open_cost_micros", "delta_close_cost_micros", "delta_realized_pnl_micros", "delta_unavailable_reason", "changed_fields", "metadata_fields"})
	values := func(p *ledger.PositionCostState) []string {
		if p == nil {
			return []string{"", "", "", "", ""}
		}
		return []string{strconv.FormatInt(p.OpenQuantity, 10), strconv.FormatFloat(p.OpenTotalCost, 'f', -1, 64), strconv.FormatInt(p.CloseQuantity, 10), strconv.FormatFloat(p.CloseTotalCost, 'f', -1, 64), strconv.FormatFloat(p.RealizedPnL, 'f', -1, 64)}
	}
	for _, day := range r.Days {
		for _, row := range day.Rows {
			if writeErr != nil {
				break
			}
			a, b := values(row.Research), values(row.Stored)
			delta := []string{"", "", ""}
			if row.Delta != nil {
				delta = []string{strconv.FormatInt(row.Delta.OpenCostMicros, 10), strconv.FormatInt(row.Delta.CloseCostMicros, 10), strconv.FormatInt(row.Delta.RealizedPnLMicros, 10)}
			}
			writeErr = w.Write([]string{researchCSVText(r.AccountID), researchCSVText(r.VersionID), day.TradeDate, researchCSVText(row.SecurityID), researchCSVText(row.CostBucket), row.BasisKind, row.Outcome, strconv.FormatBool(r.AnchorAndAllocationsMatch), strconv.FormatBool(day.StoredDayMissing), a[0], b[0], a[1], b[1], a[2], b[2], a[3], b[3], a[4], b[4], delta[0], delta[1], delta[2], row.DeltaUnavailable, strings.Join(row.ChangedFields, "|"), strings.Join(row.MetadataFields, "|")})
		}
	}
	w.Flush()
	writeErr = errors.Join(writeErr, w.Error())
	if writeErr == nil {
		writeErr = f.Sync()
	}
	err = errors.Join(writeErr, f.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
