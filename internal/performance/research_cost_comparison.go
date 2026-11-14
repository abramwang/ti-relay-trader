package performance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

type ResearchEvidenceChange struct {
	Scope    string   `json:"scope"`
	Identity string   `json:"identity"`
	Kind     string   `json:"kind"`
	Fields   []string `json:"changed_fields,omitempty"`
}

type ResearchCostDelta struct {
	OpenQuantity      int64 `json:"open_quantity"`
	CloseQuantity     int64 `json:"close_quantity"`
	OpenCostMicros    int64 `json:"open_cost_micros"`
	CloseCostMicros   int64 `json:"close_cost_micros"`
	RealizedPnLMicros int64 `json:"realized_pnl_micros"`
}

type ResearchCostComparisonRow struct {
	SecurityID       string                    `json:"security_id"`
	CostBucket       string                    `json:"cost_bucket"`
	BasisKind        string                    `json:"basis_kind"`
	Outcome          string                    `json:"outcome"`
	ChangedFields    []string                  `json:"changed_fields,omitempty"`
	MetadataFields   []string                  `json:"metadata_fields,omitempty"`
	Research         *ledger.PositionCostState `json:"research"`
	Stored           *ledger.PositionCostState `json:"stored"`
	Delta            *ResearchCostDelta        `json:"research_minus_stored"`
	DeltaUnavailable string                    `json:"delta_unavailable_reason,omitempty"`
}

type ResearchCostComparisonCounts struct {
	Equal        int `json:"equal"`
	Changed      int `json:"changed"`
	MetadataOnly int `json:"metadata_only"`
	ResearchOnly int `json:"research_only"`
	StoredOnly   int `json:"stored_only"`
}

type ResearchCostComparisonDay struct {
	TradeDate           string                       `json:"trade_date"`
	StoredDayMissing    bool                         `json:"stored_day_missing"`
	Counts              ResearchCostComparisonCounts `json:"counts"`
	Rows                []ResearchCostComparisonRow  `json:"rows"`
	BasisMovements      []CostBasisMovement          `json:"basis_movements"`
	RoundingDifferences []CostBasisMovement          `json:"rounding_differences"`
}

type ResearchCostComparison struct {
	AccountID                 string                       `json:"account_id"`
	VersionID                 string                       `json:"version_id"`
	PayloadSHA256             string                       `json:"payload_sha256"`
	ResearchOnly              bool                         `json:"research_only"`
	NAVPublishable            bool                         `json:"economic_nav_publishable"`
	EvidenceCheckScope        string                       `json:"evidence_check_scope"`
	AnchorAndAllocationsMatch bool                         `json:"anchor_and_allocations_match"`
	EvidenceChanges           []ResearchEvidenceChange     `json:"evidence_changes"`
	Counts                    ResearchCostComparisonCounts `json:"counts"`
	Days                      []ResearchCostComparisonDay  `json:"days"`
}

// Compare structured values without float64 decoding or subtracting unrelated
// bases. Timestamp representations of the same instant compare equally.
func researchChangedFields(a, b any) ([]string, error) {
	encode := func(v any) (map[string]json.RawMessage, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var m map[string]json.RawMessage
		err = json.Unmarshal(raw, &m)
		return m, err
	}
	x, err := encode(a)
	if err != nil {
		return nil, err
	}
	y, err := encode(b)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for k := range x {
		keys[k] = true
	}
	for k := range y {
		keys[k] = true
	}
	var fields []string
	for k := range keys {
		if !bytes.Equal(x[k], y[k]) {
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	return fields, nil
}

func canonicalResearchCost(p ledger.PositionCostState) ledger.PositionCostState {
	p.CalculatedAt = p.CalculatedAt.UTC()
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p
}

func displayResearchCost(p ledger.PositionCostState) ledger.PositionCostState {
	if !p.CalculatedAt.IsZero() {
		p.CalculatedAt = p.CalculatedAt.In(timeutil.Location())
	}
	if !p.CreatedAt.IsZero() {
		p.CreatedAt = p.CreatedAt.In(timeutil.Location())
	}
	if !p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.UpdatedAt.In(timeutil.Location())
	}
	return p
}

func researchCostKey(p ledger.PositionCostState) string {
	return p.Symbol + "." + p.Exchange + "|" + p.CostBucket
}

func researchEvidenceChanges(b ResearchCostBundle, opening []ledger.PositionCostState, audits []ledger.T0Allocation) ([]ResearchEvidenceChange, error) {
	var changes []ResearchEvidenceChange
	before, after := map[string]any{}, map[string]any{}
	addCosts := func(rows []ledger.PositionCostState, dest map[string]any) error {
		if len(rows) > 10000 {
			return fmt.Errorf("opening evidence exceeds limit")
		}
		for _, p := range rows {
			k := researchCostKey(p)
			date, _, dateErr := parseTradeDate(p.TradeDate)
			if dateErr != nil || date != p.TradeDate || p.AccountID != b.AccountID || p.TradeDate >= b.Dates[0] || p.Symbol == "" || p.CostBucket == "" {
				return fmt.Errorf("invalid opening evidence scope")
			}
			if _, ok := dest[k]; ok {
				return fmt.Errorf("duplicate opening evidence")
			}
			dest[k] = canonicalResearchCost(p)
		}
		return nil
	}
	if err := addCosts(b.Opening, before); err != nil {
		return nil, err
	}
	if err := addCosts(opening, after); err != nil {
		return nil, err
	}
	compare := func(scope string) error {
		keys := map[string]bool{}
		for k := range before {
			keys[k] = true
		}
		for k := range after {
			keys[k] = true
		}
		for k := range keys {
			a, hasA := before[k]
			c, hasC := after[k]
			change := ResearchEvidenceChange{Scope: scope, Identity: k}
			switch {
			case !hasA:
				change.Kind = "current_only"
			case !hasC:
				change.Kind = "missing_current"
			default:
				fields, err := researchChangedFields(a, c)
				if err != nil {
					return err
				}
				if len(fields) == 0 {
					continue
				}
				change.Kind = "changed"
				change.Fields = fields
			}
			changes = append(changes, change)
		}
		return nil
	}
	if err := compare("opening_anchor"); err != nil {
		return nil, err
	}
	before, after = map[string]any{}, map[string]any{}
	dates := map[string]bool{}
	for _, d := range b.Dates {
		dates[d] = true
	}
	addAudits := func(items []ledger.T0Allocation, dest map[string]any) error {
		if len(items) > 10000 {
			return fmt.Errorf("allocation evidence exceeds limit")
		}
		ids := map[string]bool{}
		for _, a := range items {
			n, err := ledger.NormalizeT0Allocation(a)
			if err != nil {
				return err
			}
			k := n.TradeDate + "|" + n.GatewayOrderID
			if n.AccountID != b.AccountID || !dates[n.TradeDate] || n.Status != "active" || ids[n.LinkID] {
				return fmt.Errorf("invalid current allocation scope")
			}
			if _, ok := dest[k]; ok {
				return fmt.Errorf("multiple active revisions for order")
			}
			ids[n.LinkID] = true
			dest[k] = n
		}
		return nil
	}
	if err := addAudits(b.Audits, before); err != nil {
		return nil, err
	}
	if err := addAudits(audits, after); err != nil {
		return nil, err
	}
	if err := compare("active_allocation"); err != nil {
		return nil, err
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Scope+"|"+changes[i].Identity < changes[j].Scope+"|"+changes[j].Identity
	})
	return changes, nil
}

func researchRowDelta(a, b ledger.PositionCostState) (*ResearchCostDelta, string) {
	for _, q := range []int64{a.OpenQuantity, a.CloseQuantity, b.OpenQuantity, b.CloseQuantity} {
		if q < 0 || q > 1e12 {
			return nil, "quantity_out_of_review_range"
		}
	}
	values := []float64{a.OpenTotalCost, b.OpenTotalCost, a.CloseTotalCost, b.CloseTotalCost, a.RealizedPnL, b.RealizedPnL}
	m := make([]int64, len(values))
	for i, value := range values {
		n, err := costReviewMicros(value)
		if err != nil {
			return nil, "amount_outside_exact_six_decimal_review"
		}
		m[i] = n
	}
	return &ResearchCostDelta{OpenQuantity: a.OpenQuantity - b.OpenQuantity, CloseQuantity: a.CloseQuantity - b.CloseQuantity, OpenCostMicros: m[0] - m[1], CloseCostMicros: m[2] - m[3], RealizedPnLMicros: m[4] - m[5]}, ""
}

func countResearchOutcome(c *ResearchCostComparisonCounts, outcome string) {
	switch outcome {
	case "equal":
		c.Equal++
	case "changed":
		c.Changed++
	case "metadata_only":
		c.MetadataOnly++
	case "research_only":
		c.ResearchOnly++
	case "stored_only":
		c.StoredOnly++
	}
}

// This is a forensic comparison with stored costs, not a calculation or approval.
// A stale anchor/allocation snapshot remains inspectable but is explicitly marked.
// Market data, raw trades and cash settlement finality are NOT refreshed here.
func CompareResearchCostVersion(v ledger.ResearchCostVersion, opening []ledger.PositionCostState, audits []ledger.T0Allocation, stored []ledger.PositionCostState) (ResearchCostComparison, error) {
	b, err := DecodeResearchCostVersion(v)
	if err != nil {
		return ResearchCostComparison{}, err
	}
	r := ResearchCostComparison{AccountID: b.AccountID, VersionID: b.VersionID, PayloadSHA256: v.PayloadSHA256, ResearchOnly: true, EvidenceCheckScope: "opening_anchor_and_active_allocations_only"}
	r.EvidenceChanges, err = researchEvidenceChanges(b, opening, audits)
	if err != nil {
		return r, err
	}
	r.AnchorAndAllocationsMatch = len(r.EvidenceChanges) == 0
	if len(stored) > 100000 {
		return r, fmt.Errorf("stored cost comparison exceeds limit")
	}
	byDate := map[string]map[string]ledger.PositionCostState{}
	for _, d := range b.Dates {
		byDate[d] = map[string]ledger.PositionCostState{}
	}
	for _, p := range stored {
		rows, ok := byDate[p.TradeDate]
		if !ok || p.AccountID != b.AccountID || p.Symbol == "" || p.Exchange == "" || p.CostBucket == "" {
			return r, fmt.Errorf("stored cost scope mismatch")
		}
		k := researchCostKey(p)
		if _, ok := rows[k]; ok {
			return r, fmt.Errorf("duplicate stored cost bucket")
		}
		rows[k] = p
	}
	for _, day := range b.Costs {
		current := byDate[day.TradeDate]
		research := map[string]ledger.PositionCostState{}
		keys := map[string]bool{}
		for _, p := range day.Positions {
			k := researchCostKey(p)
			research[k] = p
			keys[k] = true
		}
		for k := range current {
			keys[k] = true
		}
		ordered := make([]string, 0, len(keys))
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		d := ResearchCostComparisonDay{TradeDate: day.TradeDate, StoredDayMissing: len(current) == 0}
		for _, k := range ordered {
			a, hasA := research[k]
			c, hasC := current[k]
			p := a
			if !hasA {
				p = c
			}
			row := ResearchCostComparisonRow{SecurityID: p.Symbol + "." + p.Exchange, CostBucket: p.CostBucket, BasisKind: "unknown"}
			switch {
			case p.CostBucket == "CORE":
				row.BasisKind = "core_cost"
			case strings.HasPrefix(p.CostBucket, "ETF_T0:"):
				row.BasisKind = "redemption_cost_release_not_loss"
			case isT0ResidualBucket(p.CostBucket):
				row.BasisKind = "t0_residual_cost"
			case isComponentResidualBucket(p.CostBucket):
				row.BasisKind = "component_marked_basis_not_acquisition_cost"
			}
			if hasA {
				copy := displayResearchCost(a)
				row.Research = &copy
			}
			if hasC {
				copy := displayResearchCost(c)
				row.Stored = &copy
			}
			switch {
			case !hasC:
				row.Outcome = "research_only"
				row.DeltaUnavailable = "stored_row_missing"
			case !hasA:
				row.Outcome = "stored_only"
				row.DeltaUnavailable = "research_row_missing"
			default:
				fields, err := researchChangedFields(canonicalResearchCost(a), canonicalResearchCost(c))
				if err != nil {
					return r, err
				}
				for _, field := range fields {
					switch field {
					case "created_at", "updated_at", "calculated_at":
						row.MetadataFields = append(row.MetadataFields, field)
					default:
						row.ChangedFields = append(row.ChangedFields, field)
					}
				}
				row.Outcome = "equal"
				if len(row.MetadataFields) > 0 {
					row.Outcome = "metadata_only"
				}
				if len(row.ChangedFields) > 0 {
					row.Outcome = "changed"
				}
				row.Delta, row.DeltaUnavailable = researchRowDelta(a, c)
			}
			countResearchOutcome(&d.Counts, row.Outcome)
			countResearchOutcome(&r.Counts, row.Outcome)
			d.Rows = append(d.Rows, row)
		}
		for _, m := range b.Review.Movements {
			if m.TradeDate == day.TradeDate {
				d.BasisMovements = append(d.BasisMovements, m)
			}
		}
		for _, m := range b.Review.RoundingDifferences {
			if m.TradeDate == day.TradeDate {
				d.RoundingDifferences = append(d.RoundingDifferences, m)
			}
		}
		r.Days = append(r.Days, d)
	}
	return r, nil
}
