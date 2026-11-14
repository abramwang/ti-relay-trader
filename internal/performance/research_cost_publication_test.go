package performance

import (
	"reflect"
	"strings"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func publicationFixture(t *testing.T, b ResearchCostBundle, stored []ledger.PositionCostState) (ledger.ResearchCostVersion, ResearchCostComparison, ResearchSourcePreflight) {
	t.Helper()
	v, err := PrepareResearchCostVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := CompareResearchCostVersion(v, b.Opening, b.Audits, stored)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := sourceFixture(t)
	o.PayloadSHA256 = v.PayloadSHA256
	s, err := ReviewResearchSources(o, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	return v, c, s
}

func publicationGate(r ResearchCostPublicationReview, code string) string {
	for _, gate := range r.CostGates {
		if gate.Code == code {
			return gate.State
		}
	}
	return "missing"
}

func TestResearchCostPublicationInventory(t *testing.T) {
	v, c, s := publicationFixture(t, researchCostFixture(), nil)
	r, err := ReviewResearchCostPublication(v, c, s)
	if err != nil || r.Structure.Rows != 11 || len(r.Days) != 2 || r.DatabaseWrites != 0 || r.BrokerCommands != 0 || r.CostPublishable || r.NAVPublishable {
		t.Fatal(r, err)
	}
	if publicationGate(r, "formal_targets_absent") != "passed" || publicationGate(r, "structural_continuity") != "passed" || publicationGate(r, "source_observation") != "blocked" || publicationGate(r, "atomic_publication_and_next_day_reader") != "not_implemented" {
		t.Fatal(r.CostGates)
	}
	rows := 0
	for _, d := range r.Days {
		rows += d.Rows
		n := 0
		for _, count := range d.Buckets {
			n += count
		}
		if n != d.Rows {
			t.Fatal("bucket counts differ", d)
		}
	}
	if rows != r.Structure.Rows {
		t.Fatal("incomplete inventory")
	}
	again, err := ReviewResearchCostPublication(v, c, s)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("non deterministic review", err)
	}
	if len(r.Semantics) == 0 || publicationGate(r, "cost_semantics") != "blocked" {
		t.Fatal("preview costs accepted without semantic review")
	}
}

func TestResearchCostPublicationUnknownFlagAndPartialTargets(t *testing.T) {
	b := researchCostFixture()
	b.Costs[0].Positions[0].QualityFlags = append(b.Costs[0].Positions[0].QualityFlags, "new_unreviewed_rule")
	v, c, s := publicationFixture(t, b, []ledger.PositionCostState{b.Costs[0].Positions[0]})
	r, err := ReviewResearchCostPublication(v, c, s)
	if err != nil || publicationGate(r, "formal_targets_absent") != "blocked" || r.CostPublishable {
		t.Fatal(r, err)
	}
	found := false
	for _, item := range r.Semantics {
		if item.Code == "unreviewed_flag:new_unreviewed_rule" {
			found = len(item.Occurrences) == 1 && item.Occurrences[0].TradeDate == b.Dates[0]
		}
	}
	if !found {
		t.Fatal("unknown flag not traced to source row")
	}
	b.Costs[0].Positions[0].CostBucket = "UNKNOWN"
	if _, err := PrepareResearchCostVersion(b); err == nil {
		t.Fatal("unknown bucket accepted by structural gate")
	}
}

func TestResearchCostPublicationSourceAndAnchorChanges(t *testing.T) {
	v, c, first := publicationFixture(t, researchCostFixture(), nil)
	o := first.Observation
	o.Days = append([]ResearchSourceDay(nil), o.Days...)
	o.ObservedAt = o.ObservedAt.Add(1)
	s, err := ReviewResearchSources(o, &first, c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ReviewResearchCostPublication(v, c, s)
	if err != nil || publicationGate(r, "source_observation") != "passed" || r.CostPublishable || r.NAVPublishable {
		t.Fatal(r, err)
	}
	o.Days[1].Rows = 1
	o.Days[1].SHA256 = strings.Repeat("a", 64)
	s, err = ReviewResearchSources(o, &first, c)
	if err != nil {
		t.Fatal(err)
	}
	r, err = ReviewResearchCostPublication(v, c, s)
	if err != nil || publicationGate(r, "source_observation") != "blocked" || r.Source.RecheckFrom != o.Days[1].Date {
		t.Fatal(r, err)
	}
	b := researchCostFixture()
	b.Opening[0].CloseTotalCost++
	c, err = CompareResearchCostVersion(v, b.Opening, b.Audits, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err = ReviewResearchSources(o, &first, c)
	if err != nil {
		t.Fatal(err)
	}
	r, err = ReviewResearchCostPublication(v, c, s)
	if err != nil || publicationGate(r, "anchor_and_attribution") != "blocked" || r.Source.RecheckFrom != b.AnchorDate {
		t.Fatal(r, err)
	}
}

func TestResearchCostPublicationRejectsMismatchedInputs(t *testing.T) {
	for _, name := range []string{"hash", "account", "payload", "source-hash", "date", "days", "rows", "count", "authorization", "write"} {
		t.Run(name, func(t *testing.T) {
			v, c, s := publicationFixture(t, researchCostFixture(), nil)
			switch name {
			case "hash":
				v.PayloadSHA256 = strings.Repeat("a", 64)
			case "account":
				c.AccountID = "other"
			case "payload":
				s.Observation.PayloadSHA256 = strings.Repeat("a", 64)
				s.ObservationSHA256, _ = SourceObservationHash(s.Observation)
			case "source-hash":
				s.ObservationSHA256 = strings.Repeat("a", 64)
			case "date":
				c.Days[0].TradeDate = "2026-01-01"
			case "days":
				c.Days = c.Days[:1]
			case "rows":
				c.Days[0].Rows = c.Days[0].Rows[1:]
			case "count":
				c.Counts.ResearchOnly--
			case "authorization":
				s.CostPublishable = true
			case "write":
				s.DatabaseWrites = 1
			}
			if _, err := ReviewResearchCostPublication(v, c, s); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
