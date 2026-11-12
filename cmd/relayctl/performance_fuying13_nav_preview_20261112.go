package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

const fuyingCashMatrixPath = "outputs/recovery/fuying13_cash_matrix_20261112/matrix_verified.json"
const fuyingCashMatrixHash = "7550662d6ef5f8addff9a07fc796c01c4e560fb13f1ea4d30f96be524d8d17eb"

type fuyingMatrixCash struct {
	AccountID  string            `json:"account_id"`
	FlowID     string            `json:"broker_flow_id"`
	Amount     string            `json:"actual_amount"`
	SourceDate string            `json:"resolved_source_trade_date"`
	Raw        map[string]string `json:"raw_fields"`
}

type fuyingCashMatrix struct {
	Schema      string             `json:"schema"`
	AccountID   string             `json:"account_id"`
	From        string             `json:"date_from"`
	To          string             `json:"date_to"`
	Hashes      map[string]string  `json:"source_sha256"`
	Writes      int                `json:"database_writes"`
	Commands    int                `json:"broker_commands"`
	Publishable bool               `json:"economic_nav_publishable"`
	Overlap     bool               `json:"asset_overlap_verified"`
	AllClosed   bool               `json:"all_rights_closed"`
	Resolved    []fuyingMatrixCash `json:"resolved_cash"`
	Unresolved  []fuyingMatrixCash `json:"unresolved_cash"`
	PCF         struct {
		Candidates []struct {
			FlowIDs  []string `json:"flow_ids"`
			Verified bool     `json:"private_source_verified"`
			Included bool     `json:"included_in_known_carry"`
			Matches  []struct {
				SourceDate string `json:"source_trade_date"`
			} `json:"pcf_order_multiset_candidates"`
		} `json:"unresolved_candidates"`
	} `json:"cash_pcf_review"`
}

type fuyingCashTimingLeg struct {
	ID, Source, Posted string
	Cents              int64
}
type fuyingNAVInputs struct {
	Bases             []brokerBasis20261110Day
	Known, Candidates []fuyingCashTimingLeg
}

type fuyingNativeNAVCheck struct {
	Date   string                        `json:"trade_date"`
	Result performance.EconomicNAVResult `json:"engine_result"`
	Error  string                        `json:"error,omitempty"`
}

type fuyingNAVScenarioDay struct {
	Date                string  `json:"trade_date"`
	ReportedOpen        float64 `json:"reported_open_asset"`
	ReportedClose       float64 `json:"reported_close_asset"`
	KnownOpen           float64 `json:"known_open_carry"`
	KnownClose          float64 `json:"known_close_carry"`
	CandidateOpen       float64 `json:"candidate_open_carry"`
	CandidateClose      float64 `json:"candidate_close_carry"`
	ConditionalOpen     float64 `json:"conditional_open_asset"`
	ConditionalClose    float64 `json:"conditional_close_asset"`
	PnL                 float64 `json:"conditional_day_pnl"`
	Return              float64 `json:"conditional_daily_return"`
	Index               float64 `json:"normalized_reference_index"`
	ModelContribution   float64 `json:"model_net_contribution"`
	Residual            float64 `json:"conditional_pnl_minus_model_contribution"`
	Unclassified        float64 `json:"broker_unclassified_asset_preserved"`
	RepoAlreadyIncluded float64 `json:"repo_principal_already_in_reported_asset"`
}

type fuyingNAVScenario struct {
	Name        string                 `json:"name"`
	Assumptions []string               `json:"assumptions"`
	Days        []fuyingNAVScenarioDay `json:"days"`
	Publishable bool                   `json:"economic_nav_publishable"`
	SumPnL      float64                `json:"conditional_period_pnl"`
	EndingIndex float64                `json:"ending_reference_index"`
}

var fuyingStrictMoney = regexp.MustCompile(`^-?[0-9]{1,12}\.[0-9]{2}$`)

func fuyingTimingLeg(row fuyingMatrixCash, source string) (fuyingCashTimingLeg, error) {
	var leg fuyingCashTimingLeg
	if row.AccountID != fuying13Account || row.FlowID == "" || !fuyingStrictMoney.MatchString(row.Amount) {
		return leg, errors.New("cash matrix identity/money invalid")
	}
	for _, d := range []string{source, row.Raw["交易日期"]} {
		if parsed, err := time.Parse("20060102", d); err != nil || parsed.Format("20060102") != d {
			return leg, errors.New("invalid cash matrix date")
		}
	}
	if source < "20261012" || source > row.Raw["交易日期"] || row.Raw["交易日期"] > "20261109" {
		return leg, errors.New("cash timing outside reviewed window")
	}
	n, err := parseBrokerMoneyCents(row.Amount)
	if err != nil {
		return leg, err
	}
	return fuyingCashTimingLeg{row.FlowID, compactToISODate(source), compactToISODate(row.Raw["交易日期"]), n}, nil
}

func fuyingMatrixLegs(m fuyingCashMatrix) ([]fuyingCashTimingLeg, []fuyingCashTimingLeg, error) {
	var known, candidate []fuyingCashTimingLeg
	seen := map[string]bool{}
	for _, r := range m.Resolved {
		leg, err := fuyingTimingLeg(r, r.SourceDate)
		if err != nil {
			return nil, nil, err
		}
		if seen[leg.ID] {
			return nil, nil, errors.New("duplicate matrix flow")
		}
		seen[leg.ID] = true
		known = append(known, leg)
	}
	unresolved := map[string]fuyingMatrixCash{}
	for _, r := range m.Unresolved {
		if r.SourceDate != "" || r.FlowID == "" || seen[r.FlowID] || unresolved[r.FlowID].FlowID != "" {
			return nil, nil, errors.New("unresolved flow reused or silently confirmed")
		}
		unresolved[r.FlowID] = r
	}
	for _, g := range m.PCF.Candidates {
		if g.Verified || g.Included || len(g.Matches) != 1 {
			return nil, nil, errors.New("candidate scenario requires exactly one unconfirmed candidate")
		}
		for _, id := range g.FlowIDs {
			r, ok := unresolved[id]
			if !ok || seen[id] {
				return nil, nil, errors.New("candidate flow missing/reused")
			}
			leg, err := fuyingTimingLeg(r, g.Matches[0].SourceDate)
			if err != nil {
				return nil, nil, err
			}
			candidate = append(candidate, leg)
			seen[id] = true
		}
	}
	if len(candidate) != len(unresolved) {
		return nil, nil, errors.New("candidate coverage incomplete")
	}
	return known, candidate, nil
}

func loadFuyingNAVInputs(ctx context.Context, tx *sql.Tx) (fuyingNAVInputs, error) {
	var result fuyingNAVInputs
	raw, err := os.ReadFile(fuyingCashMatrixPath)
	if err != nil {
		return result, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != fuyingCashMatrixHash {
		return result, errors.New("verified matrix changed")
	}
	var m fuyingCashMatrix
	if err := json.Unmarshal(raw, &m); err != nil {
		return result, err
	}
	spec := brokerBasis20261110Specs[0]
	if m.Schema != "fuying13_settlement_matrix.v1" || m.AccountID != fuying13Account || m.From != "20261012" || m.To != "20261109" || m.Publishable || m.Overlap || m.AllClosed || m.Writes != 0 || m.Commands != 0 || m.Hashes["funds"] != spec.FundsHash || m.Hashes["cash_flows"] != spec.FlowsHash {
		return result, errors.New("matrix evidence scope changed")
	}
	result.Known, result.Candidates, err = fuyingMatrixLegs(m)
	if err != nil {
		return result, err
	}
	if len(result.Known) != 3606 || len(result.Candidates) != 10 {
		return result, errors.New("matrix flow coverage changed")
	}
	f, fh, err := readBrokerCSV("reference/" + fuying13Account + "_资金.csv")
	if err != nil {
		return result, err
	}
	c, ch, err := readBrokerCSV("reference/" + fuying13Account + "_资金流水.csv")
	if err != nil {
		return result, err
	}
	if fh != spec.FundsHash || ch != spec.FlowsHash {
		return result, errors.New("broker exports changed")
	}
	result.Bases, err = buildBrokerBasis20261110(spec, f, c)
	if err != nil {
		return result, err
	}
	repo := ledger.NewRepository(tx)
	for _, b := range result.Bases {
		o, err := repo.GetAssetPositionObservation(ctx, fuying13Account, b.Date, "reconcile")
		if err != nil {
			return result, err
		}
		if o.Source != brokerBasis20261110Source || o.NetAsset != b.Asset.NetAsset || o.CashTotal != b.Asset.CashTotal || o.MarketValue != b.Asset.MarketValue || o.ReverseRepoReceivable != b.Asset.ReverseRepoReceivable || o.RawPayload["statement_sha256"] != fh || o.RawPayload["economic_nav_base_confirmed"] != false {
			return result, fmt.Errorf("broker basis changed %s", b.Date)
		}
	}
	return result, nil
}

func fuyingBasisCents(b brokerBasis20261110Day, key string) (int64, error) {
	var n float64
	switch value := b.Raw[key].(type) {
	case float64:
		n = value
	case int:
		n = float64(value)
	default:
		return 0, fmt.Errorf("invalid basis amount %s", key)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) >= 1e12 || math.Abs(n*100-math.Round(n*100)) > 0.001 {
		return 0, fmt.Errorf("invalid basis amount %s", key)
	}
	return int64(math.Round(n * 100)), nil
}

func fuyingCarry(legs []fuyingCashTimingLeg, day string) int64 {
	var n int64
	for _, l := range legs {
		if l.Source <= day && day < l.Posted {
			n += l.Cents
		}
	}
	return n
}

// These are normalized conditional curves, deliberately not PerformanceNAVs.
// No confirmed-basis flag is changed and blocked engine NAVs never feed them.
func previewFuyingCashNAV(input fuyingNAVInputs, contributions []performance.ContributionResult) ([]fuyingNAVScenario, error) {
	if len(input.Bases) == 0 {
		return nil, errors.New("no broker basis")
	}
	seen := map[string]bool{}
	if len(input.Known)+len(input.Candidates) > 5000 {
		return nil, errors.New("cash preview bound exceeded")
	}
	for _, legs := range [][]fuyingCashTimingLeg{input.Known, input.Candidates} {
		for _, l := range legs {
			for _, d := range []string{l.Source, l.Posted} {
				if _, err := time.Parse("2006-01-02", d); err != nil {
					return nil, errors.New("invalid leg date")
				}
			}
			if l.ID == "" || seen[l.ID] || l.Source < "2026-10-12" || l.Source > l.Posted || l.Posted > "2026-11-09" || l.Cents <= -100000000000000 || l.Cents >= 100000000000000 {
				return nil, errors.New("invalid/reused cash leg")
			}
			seen[l.ID] = true
		}
	}
	models := map[string]performance.ContributionSummary{}
	for _, c := range contributions {
		_, exists := models[c.TradeDate]
		if c.AccountID != fuying13Account || exists {
			return nil, errors.New("contribution identity mismatch")
		}
		if c.Summary.MissingItems != 0 || math.IsNaN(c.Summary.NetContribution) || math.IsInf(c.Summary.NetContribution, 0) {
			return nil, errors.New("incomplete contribution preview")
		}
		models[c.TradeDate] = c.Summary
	}
	var scenarios []fuyingNAVScenario
	for _, include := range []bool{false, true} {
		s := fuyingNAVScenario{Name: "explicit_source_cash_only", Assumptions: []string{"known cash timing rights assumed separate from broker reported total; overlap unverified", "observed cash legs only; missing or future settlements are not asserted zero", "broker reported total preserved, including unclassified components", "no repo principal, market value, fees or model settlement added twice", "normalized at 2026-10-09 close=1, not product inception NAV"}}
		if include {
			s.Name = "explicit_plus_pcf_candidates"
			s.Assumptions = append(s.Assumptions, "10 private-source-unverified PCF candidates allocated solely for sensitivity comparison")
		}
		var previousReported, previousClose, total int64
		previousDay := "2026-10-09"
		index := 1.0
		for i, b := range input.Bases {
			_, dateErr := time.Parse("2006-01-02", b.Date)
			if b.Account != fuying13Account || dateErr != nil || b.Date <= previousDay || b.Date > "2026-11-09" {
				return nil, errors.New("basis dates/account invalid")
			}
			values := map[string]int64{}
			for _, key := range []string{"reported_open_total_asset", "reported_close_total_asset", "reported_daily_pnl", "reported_deposit", "reported_withdrawal", "broker_unclassified_asset_component", "verified_reverse_repo_principal"} {
				v, err := fuyingBasisCents(b, key)
				if err != nil {
					return nil, err
				}
				values[key] = v
			}
			op, cl := values["reported_open_total_asset"], values["reported_close_total_asset"]
			if values["reported_deposit"] != 0 || values["reported_withdrawal"] != 0 || cl-op != values["reported_daily_pnl"] || (i > 0 && op != previousReported) {
				return nil, errors.New("broker asset bridge or external flow requires review")
			}
			ko, kc := fuyingCarry(input.Known, previousDay), fuyingCarry(input.Known, b.Date)
			co, cc := int64(0), int64(0)
			if include {
				co, cc = fuyingCarry(input.Candidates, previousDay), fuyingCarry(input.Candidates, b.Date)
			}
			a, z := op+ko+co, cl+kc+cc
			if a <= 0 || z <= 0 || (i > 0 && a != previousClose) {
				return nil, errors.New("conditional cash NAV continuity failed")
			}
			model, ok := models[b.Date]
			if !ok {
				return nil, fmt.Errorf("missing contribution %s", b.Date)
			}
			pnl := z - a
			total += pnl
			daily := float64(pnl) / float64(a)
			index *= 1 + daily
			s.Days = append(s.Days, fuyingNAVScenarioDay{Date: b.Date, ReportedOpen: centsFloat(op), ReportedClose: centsFloat(cl), KnownOpen: centsFloat(ko), KnownClose: centsFloat(kc), CandidateOpen: centsFloat(co), CandidateClose: centsFloat(cc), ConditionalOpen: centsFloat(a), ConditionalClose: centsFloat(z), PnL: centsFloat(pnl), Return: daily, Index: index, ModelContribution: model.NetContribution, Residual: centsFloat(pnl) - model.NetContribution, Unclassified: centsFloat(values["broker_unclassified_asset_component"]), RepoAlreadyIncluded: centsFloat(values["verified_reverse_repo_principal"])})
			previousReported, previousClose, previousDay = cl, z, b.Date
		}
		s.SumPnL = centsFloat(total)
		s.EndingIndex = index
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
}

func fuyingEngineFlags(checks []fuyingNativeNAVCheck) []string {
	seen := map[string]bool{}
	for _, c := range checks {
		for _, f := range c.Result.QualityFlags {
			seen[f] = true
		}
		if c.Error != "" {
			seen["engine_error:"+strings.TrimSpace(c.Error)] = true
		}
	}
	var flags []string
	for f := range seen {
		flags = append(flags, f)
	}
	sort.Strings(flags)
	return flags
}
