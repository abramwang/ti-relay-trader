package main

import "testing"

func observedCashMatrixFixture() fuyingCashMatrix {
	return fuyingCashMatrix{AccountID: fuying13Account, Resolved: []fuyingMatrixCash{{AccountID: fuying13Account,
		FlowID: "f", Amount: "-10.00", SourceDate: "20261012", Reason: "explicit_source", Raw: map[string]string{"交易日期": "20261013"}}},
		Unresolved: []fuyingMatrixCash{{AccountID: fuying13Account, FlowID: "unknown", Amount: "-10.00"}}}
}

func TestFuyingObservedCashExcludesUnresolved(t *testing.T) {
	m := observedCashMatrixFixture()
	r, err := reviewFuyingObservedCash(m, []string{"2026-10-12", "2026-10-13"})
	if err != nil {
		t.Fatal(err)
	}
	if r.LegCount != 1 || r.Days[0].ClosePayableCents != 1000 || r.Days[1].ClosePayableCents != 0 || r.EconomicNAVPublishable {
		t.Fatalf("wrong source isolation: %+v", r)
	}
}

func TestFuyingObservedCashEvidenceGuards(t *testing.T) {
	for _, mutate := range []func(*fuyingCashMatrix){
		func(m *fuyingCashMatrix) { m.Unresolved[0].SourceDate = "20261012" },
		func(m *fuyingCashMatrix) { m.Unresolved[0].FlowID = "f" },
		func(m *fuyingCashMatrix) { m.Resolved[0].Reason = "pcf_candidate" },
		func(m *fuyingCashMatrix) { m.Resolved[0].SourceDate = "" },
		func(m *fuyingCashMatrix) { m.AllClosed = true },
		func(m *fuyingCashMatrix) { m.Writes = 1 },
		func(m *fuyingCashMatrix) { m.AccountID = "other" },
	} {
		m := observedCashMatrixFixture()
		mutate(&m)
		if _, err := reviewFuyingObservedCash(m, []string{"2026-10-12", "2026-10-13"}); err == nil {
			t.Fatal("invalid evidence accepted")
		}
	}
}

func TestFuyingCashTimingCLIRequiresNewOutput(t *testing.T) {
	for _, args := range [][]string{nil, {"-apply"}, {"-output", t.TempDir()}} {
		if err := runFuyingCashTiming(args); err == nil {
			t.Fatal("invalid CLI scope accepted")
		}
	}
}
