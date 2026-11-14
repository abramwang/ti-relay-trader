package performance

import (
	"strings"
	"testing"
	"time"
)

func sourceFixture(t *testing.T) (ResearchSourceObservation, ResearchCostComparison) {
	t.Helper()
	v, b, _ := comparisonFixture(t)
	c, err := CompareResearchCostVersion(v, b.Opening, b.Audits, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := ResearchSourceObservation{Schema: "relay.research_source_observation.v1", Environment: "test", Database: "isolated", AccountID: b.AccountID, VersionID: b.VersionID, PayloadSHA256: v.PayloadSHA256, DateFrom: b.AnchorDate, DateTo: b.Dates[len(b.Dates)-1], ObservedAt: time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)}
	dates, err := sourceDays(o.DateFrom, o.DateTo)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range researchSourceTables {
		for _, date := range dates {
			o.Days = append(o.Days, ResearchSourceDay{Table: table, Date: date, SHA256: sourceHash(nil)})
		}
	}
	return o, c
}

func TestResearchCostVersionSourcePreflight(t *testing.T) {
	o, c := sourceFixture(t)
	first, err := ReviewResearchSources(o, nil, c)
	if err != nil || first.SourceState != "observation_started_not_historical_verification" || first.CostPublishable || first.NAVPublishable || len(first.Unverified) != 5 {
		t.Fatal(first, err)
	}
	o.ObservedAt = o.ObservedAt.Add(time.Second)
	second, err := ReviewResearchSources(o, &first, c)
	if err != nil || second.SourceState != "unchanged_since_observation_not_historical_verification" || second.CostPublishable || second.NAVPublishable || len(second.Unverified) != 5 {
		t.Fatal(second, err)
	}
	o.Days = append([]ResearchSourceDay(nil), o.Days...)
	o.Days[1].Rows = 1
	o.Days[1].SHA256 = sourceHash([]byte("new row"))
	changed, err := ReviewResearchSources(o, &first, c)
	if err != nil || len(changed.Changes) != 1 || changed.RecheckFrom != o.Days[1].Date || changed.Changes[0].BeforeRows != 0 || changed.Changes[0].AfterRows != 1 {
		t.Fatal(changed, err)
	}
	o.ObservedAt = o.ObservedAt.Add(time.Second)
	o.Days[1].SHA256 = sourceHash([]byte("same count, new amount"))
	revised, err := ReviewResearchSources(o, &changed, c)
	if err != nil || len(revised.Changes) != 1 || revised.Changes[0].AfterRows != revised.Changes[0].BeforeRows {
		t.Fatal(revised, err)
	}
	o.ObservedAt = o.ObservedAt.Add(time.Second)
	o.Days[1].Rows = 0
	o.Days[1].SHA256 = sourceHash(nil)
	deleted, err := ReviewResearchSources(o, &revised, c)
	if err != nil || len(deleted.Changes) != 1 || deleted.Changes[0].BeforeRows != 1 || deleted.Changes[0].AfterRows != 0 {
		t.Fatal(deleted, err)
	}
	c.AnchorAndAllocationsMatch = false
	r, err := ReviewResearchSources(o, &first, c)
	if err != nil || r.RecheckFrom != o.DateFrom || r.CostPublishable || r.NAVPublishable {
		t.Fatal(r, err)
	}
}

func TestResearchCostVersionSourceRejectsInvalidBaseline(t *testing.T) {
	for _, name := range []string{"hash", "account", "version", "payload", "env", "database", "future", "dates", "missing-day", "duplicate-day", "negative-count", "empty-hash", "over-budget", "schema"} {
		t.Run(name, func(t *testing.T) {
			o, c := sourceFixture(t)
			prior, err := ReviewResearchSources(o, nil, c)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "hash":
				prior.ObservationSHA256 = strings.Repeat("0", 64)
			case "account":
				prior.Observation.AccountID = "other"
			case "version":
				prior.Observation.VersionID = "other"
			case "payload":
				prior.Observation.PayloadSHA256 = strings.Repeat("0", 64)
			case "env":
				prior.Observation.Environment = "production"
			case "database":
				prior.Observation.Database = "other"
			case "future":
				prior.Observation.ObservedAt = o.ObservedAt.Add(time.Second)
			case "dates":
				prior.Observation.DateFrom = "2026-02-30"
			case "missing-day":
				prior.Observation.Days = prior.Observation.Days[1:]
			case "duplicate-day":
				prior.Observation.Days[1] = prior.Observation.Days[0]
			case "negative-count":
				prior.Observation.Days[0].Rows = -1
			case "empty-hash":
				prior.Observation.Days[0].SHA256 = sourceHash([]byte("not empty"))
			case "over-budget":
				prior.Observation.Days[0].Rows = 100001
			case "schema":
				prior.Schema = "unknown"
			}
			if name != "hash" {
				if h, e := SourceObservationHash(prior.Observation); e == nil {
					prior.ObservationSHA256 = h
				}
			}
			if _, err := ReviewResearchSources(o, &prior, c); err == nil {
				t.Fatal("invalid baseline accepted")
			}
		})
	}
}

func TestResearchCostVersionSourceComparisonScope(t *testing.T) {
	o, c := sourceFixture(t)
	c.NAVPublishable = true
	if _, err := ReviewResearchSources(o, nil, c); err == nil {
		t.Fatal("publishable comparison accepted")
	}
	c.NAVPublishable = false
	c.AccountID = "other"
	if _, err := ReviewResearchSources(o, nil, c); err == nil {
		t.Fatal("cross-account comparison accepted")
	}
}
