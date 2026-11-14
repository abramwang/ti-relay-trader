package performance

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
)

func comparisonFixture(t *testing.T) (ledger.ResearchCostVersion, ResearchCostBundle, []ledger.PositionCostState) {
	t.Helper()
	v, err := PrepareResearchCostVersion(researchCostFixture())
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeResearchCostVersion(v)
	if err != nil {
		t.Fatal(err)
	}
	var rows []ledger.PositionCostState
	for _, day := range b.Costs {
		rows = append(rows, day.Positions...)
	}
	return v, b, rows
}

func TestResearchCostVersionComparisonOutcomes(t *testing.T) {
	v, b, rows := comparisonFixture(t)
	r, err := CompareResearchCostVersion(v, b.Opening, b.Audits, rows)
	if err != nil || r.Counts.Equal != 11 || !r.AnchorAndAllocationsMatch || r.NAVPublishable {
		t.Fatal(r, err)
	}
	rows[0].CreatedAt = time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	rows[1].CloseTotalCost = 0.000001
	extra := rows[2]
	extra.CostBucket = "legacy"
	rows = append(rows, extra)
	rows = append(rows[:2], rows[3:]...)
	r, err = CompareResearchCostVersion(v, b.Opening, b.Audits, rows)
	if err != nil || r.Counts.Equal != 8 || r.Counts.MetadataOnly != 1 || r.Counts.Changed != 1 || r.Counts.ResearchOnly != 1 || r.Counts.StoredOnly != 1 {
		t.Fatal(r.Counts, err)
	}
	for _, d := range r.Days {
		for _, row := range d.Rows {
			switch row.Outcome {
			case "changed":
				if row.Delta == nil || row.Delta.CloseCostMicros != -1 {
					t.Fatal("exact signed micro delta", row)
				}
			case "research_only", "stored_only":
				if row.Delta != nil || row.DeltaUnavailable == "" {
					t.Fatal("missing row treated as zero")
				}
			}
		}
	}
	again, err := CompareResearchCostVersion(v, b.Opening, b.Audits, rows)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("nondeterministic comparison", err)
	}
	r, err = CompareResearchCostVersion(v, b.Opening, b.Audits, nil)
	if err != nil || r.Counts.ResearchOnly != 11 || !r.Days[0].StoredDayMissing || !r.Days[1].StoredDayMissing {
		t.Fatal("missing days", r, err)
	}
}

func TestResearchCostVersionComparisonStaleEvidence(t *testing.T) {
	b := researchCostFixture()
	b.Audits = []ledger.T0Allocation{{LinkID: "a", AccountID: "account", TradeDate: b.Dates[0], GatewayOrderID: "order", SecurityID: "510310.SH", TradeSide: "B", Quantity: 100, GroupID: "group", Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation", EvidenceRef: "test", ConfirmedBy: "user", ConfirmedDate: "2026-11-11"}}
	v, err := PrepareResearchCostVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"revoked", "new", "revised", "same-id-payload", "anchor", "anchor missing"} {
		t.Run(kind, func(t *testing.T) {
			opening := append([]ledger.PositionCostState(nil), b.Opening...)
			audits := append([]ledger.T0Allocation(nil), b.Audits...)
			switch kind {
			case "revoked":
				audits = nil
			case "new":
				a := audits[0]
				a.LinkID = "new"
				a.GatewayOrderID = "new"
				audits = append(audits, a)
			case "revised":
				audits[0].LinkID = "rev2"
				audits[0].Revision = 2
				audits[0].Supersedes = "a"
			case "same-id-payload":
				audits[0].Quantity++
			case "anchor":
				opening[0].CloseTotalCost++
			case "anchor missing":
				opening = nil
			}
			r, err := CompareResearchCostVersion(v, opening, audits, nil)
			if err != nil || r.AnchorAndAllocationsMatch || len(r.EvidenceChanges) != 1 || r.NAVPublishable {
				t.Fatal(kind, r, err)
			}
		})
	}
}

func TestResearchCostVersionComparisonRejectsInvalidScope(t *testing.T) {
	for _, kind := range []string{"account", "date", "duplicate", "digest"} {
		t.Run(kind, func(t *testing.T) {
			v, b, rows := comparisonFixture(t)
			switch kind {
			case "account":
				rows[0].AccountID = "other"
			case "date":
				rows[0].TradeDate = "2026-10-14"
			case "duplicate":
				rows = append(rows, rows[0])
			case "digest":
				v.PayloadSHA256 = "wrong"
			}
			if _, err := CompareResearchCostVersion(v, b.Opening, b.Audits, rows); err == nil {
				t.Fatal("invalid comparison accepted")
			}
		})
	}
}

func TestResearchCostVersionComparisonPrecisionAndBasis(t *testing.T) {
	v, b, rows := comparisonFixture(t)
	rows[0].CloseTotalCost = 200.0000001
	r, err := CompareResearchCostVersion(v, b.Opening, b.Audits, rows)
	if err != nil {
		t.Fatal(err)
	}
	var unsupported, component, redemption bool
	for _, d := range r.Days {
		for _, row := range d.Rows {
			if row.SecurityID == "515100.SH" && d.TradeDate == b.Dates[0] {
				unsupported = row.Delta == nil && row.DeltaUnavailable != ""
			}
			if row.BasisKind == "component_marked_basis_not_acquisition_cost" {
				component = true
			}
			if row.BasisKind == "redemption_cost_release_not_loss" {
				redemption = true
			}
		}
	}
	if !unsupported || !component || !redemption || len(r.Days[0].BasisMovements) != 2 {
		t.Fatal("precision or economic basis lost")
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) == 0 {
		t.Fatal(err)
	}
}

func TestResearchCostVersionComparisonTimeZones(t *testing.T) {
	v, b, rows := comparisonFixture(t)
	instant := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	rows[0].CalculatedAt = instant
	r, err := CompareResearchCostVersion(v, b.Opening, b.Audits, rows)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range r.Days[0].Rows {
		if row.SecurityID == rows[0].Symbol+"."+rows[0].Exchange && row.CostBucket == rows[0].CostBucket {
			_, offset := row.Stored.CalculatedAt.Zone()
			if offset != 8*3600 || !row.Stored.CalculatedAt.Equal(instant) {
				t.Fatal("not Shanghai time")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing row")
	}
	a := rows[0]
	c := a
	c.CalculatedAt = c.CalculatedAt.In(time.FixedZone("Shanghai", 8*3600))
	fields, err := researchChangedFields(canonicalResearchCost(a), canonicalResearchCost(c))
	if err != nil || len(fields) != 0 {
		t.Fatal("same instant differs", fields, err)
	}
}
