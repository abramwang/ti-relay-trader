package main

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"ti-relay-trader/internal/performance"
)

func fuyingNAVFixture() (fuyingNAVInputs, []performance.ContributionResult) {
	input := fuyingNAVInputs{Known: []fuyingCashTimingLeg{
		{"same-day", "2026-10-12", "2026-10-12", 20000},
		{"refund-1", "2026-10-12", "2026-10-13", 10000},
		{"fee", "2026-10-12", "2026-10-13", -500},
		{"refund-2", "2026-10-12", "2026-10-14", 5000},
	}, Candidates: []fuyingCashTimingLeg{{"candidate", "2026-10-12", "2026-10-14", -2000}}}
	var contributions []performance.ContributionResult
	for i, date := range []string{"2026-10-12", "2026-10-13", "2026-10-14"} {
		open := []float64{10000, 10000, 10095}[i]
		close := []float64{10000, 10095, 10125}[i]
		input.Bases = append(input.Bases, brokerBasis20261110Day{Account: fuying13Account, Date: date, Raw: map[string]any{
			"reported_open_total_asset": open, "reported_close_total_asset": close, "reported_daily_pnl": close - open,
			"reported_deposit": 0, "reported_withdrawal": 0, "broker_unclassified_asset_component": float64(10), "verified_reverse_repo_principal": float64(2000), "economic_nav_base_confirmed": false,
		}})
		contributions = append(contributions, performance.ContributionResult{AccountID: fuying13Account, TradeDate: date, Summary: performance.ContributionSummary{NetContribution: []float64{125, 0, 0}[i]}})
	}
	return input, contributions
}

func TestFuyingCashNAVReleaseAndNoDuplicateAssets(t *testing.T) {
	in, c := fuyingNAVFixture()
	before, _ := json.Marshal(in)
	scenarios, err := previewFuyingCashNAV(in, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 {
		t.Fatal("missing independent scenario")
	}
	for n, s := range scenarios {
		if s.Publishable || len(s.Days) != 3 || s.SumPnL != 125 || math.Abs(s.EndingIndex-1.0125) > 1e-12 {
			t.Fatalf("scenario totals %#v", s)
		}
		want := [][]float64{{145, 0, -20}, {125, 0, 0}}[n]
		for i, d := range s.Days {
			if d.PnL != want[i] || d.Unclassified != 10 || d.RepoAlreadyIncluded != 2000 {
				t.Fatalf("double-counted cash/repo or dropped unclassified assets: %#v", d)
			}
			if i > 0 && s.Days[i-1].ConditionalClose != d.ConditionalOpen {
				t.Fatal("discontinuous reference capital")
			}
		}
	}
	if scenarios[0].Days[0].KnownClose != 145 || scenarios[0].Days[0].CandidateClose != 0 || scenarios[1].Days[0].CandidateClose != -20 {
		t.Fatal("candidate promoted into known carry")
	}
	if scenarios[0].Days[0].Residual != 20 || scenarios[1].Days[0].Residual != 0 {
		t.Fatal("lost attribution comparison")
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("mutated source evidence")
	}
	var decoded fuyingNAVInputs
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	replayed, err := previewFuyingCashNAV(decoded, c)
	if err != nil || !reflect.DeepEqual(scenarios, replayed) {
		t.Fatalf("in-memory integer and JSON numeric bases diverged: %v", err)
	}
}

func TestFuyingCashNAVRejectsBrokenInputs(t *testing.T) {
	for name, mutate := range map[string]func(*fuyingNAVInputs, *[]performance.ContributionResult){
		"duplicate flow":       func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Candidates[0].ID = i.Known[0].ID },
		"future source":        func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Known[0].Source = "2026-10-13" },
		"invalid date":         func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Known[0].Source = "2026-10-99" },
		"money overflow":       func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Known[0].Cents = math.MaxInt64 },
		"account":              func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Bases[0].Account = "307000051387" },
		"invalid basis date":   func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Bases[0].Date = "2026-10-99" },
		"basis outside window": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) { i.Bases[2].Date = "2026-11-10" },
		"money string": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) {
			i.Bases[0].Raw["reported_deposit"] = "0"
		},
		"wrong daily identity": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) {
			i.Bases[0].Raw["reported_daily_pnl"] = float64(1)
		},
		"gap bridge": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) {
			i.Bases[1].Raw["reported_open_total_asset"] = float64(10001)
			i.Bases[1].Raw["reported_daily_pnl"] = float64(94)
		},
		"unknown external flow": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) {
			i.Bases[0].Raw["reported_deposit"] = float64(1)
		},
		"NaN": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) {
			i.Bases[0].Raw["reported_close_total_asset"] = math.NaN()
		},
		"subcent": func(i *fuyingNAVInputs, _ *[]performance.ContributionResult) {
			i.Bases[0].Raw["broker_unclassified_asset_component"] = 10.001
		},
		"missing model":    func(_ *fuyingNAVInputs, c *[]performance.ContributionResult) { *c = (*c)[1:] },
		"duplicate model":  func(_ *fuyingNAVInputs, c *[]performance.ContributionResult) { *c = append(*c, (*c)[0]) },
		"model incomplete": func(_ *fuyingNAVInputs, c *[]performance.ContributionResult) { (*c)[0].Summary.MissingItems = 1 },
		"model account":    func(_ *fuyingNAVInputs, c *[]performance.ContributionResult) { (*c)[0].AccountID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			i, c := fuyingNAVFixture()
			mutate(&i, &c)
			if _, err := previewFuyingCashNAV(i, c); err == nil {
				t.Fatal("accepted invalid scenario")
			}
		})
	}
}

func fuyingMatrixFixture() fuyingCashMatrix {
	var m fuyingCashMatrix
	if err := json.Unmarshal([]byte(`{"resolved_cash":[{"account_id":"314000045768","broker_flow_id":"known","actual_amount":"5.00","resolved_source_trade_date":"20261012","raw_fields":{"交易日期":"20261013"}}],"unresolved_cash":[{"account_id":"314000045768","broker_flow_id":"candidate","actual_amount":"-1.00","raw_fields":{"交易日期":"20261014"}}],"cash_pcf_review":{"unresolved_candidates":[{"flow_ids":["candidate"],"private_source_verified":false,"included_in_known_carry":false,"pcf_order_multiset_candidates":[{"source_trade_date":"20261012"}]}]}}`), &m); err != nil {
		panic(err)
	}
	return m
}

func TestFuyingCashMatrixCandidateIsolation(t *testing.T) {
	m := fuyingMatrixFixture()
	k, c, err := fuyingMatrixLegs(m)
	if err != nil || len(k) != 1 || len(c) != 1 || k[0].Cents != 500 || c[0].Cents != -100 {
		t.Fatalf("legs %v %v %v", k, c, err)
	}
	for name, mutate := range map[string]func(*fuyingCashMatrix){
		"confirmed": func(m *fuyingCashMatrix) { m.PCF.Candidates[0].Verified = true },
		"included":  func(m *fuyingCashMatrix) { m.PCF.Candidates[0].Included = true },
		"nonunique": func(m *fuyingCashMatrix) {
			m.PCF.Candidates[0].Matches = append(m.PCF.Candidates[0].Matches, m.PCF.Candidates[0].Matches[0])
		},
		"duplicate candidate": func(m *fuyingCashMatrix) {
			m.PCF.Candidates[0].FlowIDs = append(m.PCF.Candidates[0].FlowIDs, "candidate")
		},
		"missing candidate": func(m *fuyingCashMatrix) { m.PCF.Candidates = nil },
		"borrowed known id": func(m *fuyingCashMatrix) { m.PCF.Candidates[0].FlowIDs = []string{"known"} },
		"silent source":     func(m *fuyingCashMatrix) { m.Unresolved[0].SourceDate = "20261012" },
		"wrong account":     func(m *fuyingCashMatrix) { m.Resolved[0].AccountID = "other" },
		"empty money":       func(m *fuyingCashMatrix) { m.Resolved[0].Amount = "--" },
		"bad money":         func(m *fuyingCashMatrix) { m.Resolved[0].Amount = "NaN" },
		"bad date":          func(m *fuyingCashMatrix) { m.Resolved[0].SourceDate = "20260230" },
	} {
		t.Run(name, func(t *testing.T) {
			m := fuyingMatrixFixture()
			mutate(&m)
			if _, _, err := fuyingMatrixLegs(m); err == nil {
				t.Fatal("promoted invalid or ambiguous cash")
			}
		})
	}
}

func TestFuyingNAVFlagAndNativeDiagnostics(t *testing.T) {
	if err := runFuying13Review20261110([]string{"-cash-nav-preview", "-output", "unused"}); err == nil {
		t.Fatal("skipped evidence gates")
	}
	if err := runFuying13Review20261110([]string{"-cash-nav-preview", "-persist", "-output", "unused"}); err == nil {
		t.Fatal("accepted persistence")
	}
	checks := []fuyingNativeNAVCheck{{Result: performance.EconomicNAVResult{Status: "blocked", QualityFlags: []string{"preview_only", "unverified"}}}, {Error: "missing daily performance"}, {Result: performance.EconomicNAVResult{QualityFlags: []string{"preview_only"}}}}
	want := []string{"engine_error:missing daily performance", "preview_only", "unverified"}
	if !reflect.DeepEqual(fuyingEngineFlags(checks), want) {
		t.Fatal("lost blocked/error evidence")
	}
}
