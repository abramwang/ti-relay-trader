package performance

import (
	"errors"
	"math/big"
	"reflect"
	"regexp"
)

type AttributionPart struct {
	Date       string   `json:"trade_date"`
	CycleID    string   `json:"cycle_id"`
	Kind       string   `json:"kind"`
	SecurityID string   `json:"security_id,omitempty"`
	Net        string   `json:"net"`
	OldNet     string   `json:"old_net"`
	Fee        string   `json:"fee"`
	Correction string   `json:"correction"`
	Flags      []string `json:"quality_flags"`
}

type AttributionDay struct {
	Date             string            `json:"trade_date"`
	Status           string            `json:"status"`
	Published        string            `json:"published_pnl"`
	Model            string            `json:"model_net"`
	T0               string            `json:"t0_correction"`
	Repo             string            `json:"repo_timing_correction"`
	RepoFee          string            `json:"repo_fee_correction,omitempty"`
	StatementRepoFee string            `json:"statement_repo_fee,omitempty"`
	ModelRepoFee     string            `json:"model_repo_fee,omitempty"`
	Interest         string            `json:"observed_repo_interest"`
	ModelInterest    string            `json:"model_repo_interest"`
	Net              string            `json:"corrected_net"`
	Residual         string            `json:"remaining_residual"`
	Other            string            `json:"other_asset_component_change"`
	Comparison       string            `json:"residual_after_asset_component_comparison"`
	AssetVerified    bool              `json:"asset_component_classification_verified"`
	ModelStrategies  map[string]string `json:"model_strategies"`
	Strategies       map[string]string `json:"conditional_strategies"`
	Parts            []AttributionPart `json:"t0_daily_parts"`
	Flags            []string          `json:"original_quality_flags"`
	CostEvidence     string            `json:"cost_evidence,omitempty"`
}

type AttributionStatementDay struct {
	Date            string `json:"trade_date"`
	Orders          int    `json:"statement_order_count"`
	Buy             string `json:"statement_buy_amount"`
	Sell            string `json:"statement_sell_amount"`
	Fee             string `json:"statement_fee"`
	MarketChange    string `json:"statement_market_value_change"`
	PnL             string `json:"statement_based_pnl"`
	Published       string `json:"published_pnl"`
	Difference      string `json:"statement_pnl_difference"`
	ValuationEffect string `json:"valuation_precision_effect"`
	TurnoverEffect  string `json:"turnover_precision_effect"`
	FeeEffect       string `json:"fee_amount_effect"`
	Unexplained     string `json:"unexplained_model_difference"`
	IdentityMatched bool   `json:"order_identity_and_quantity_matched"`
	Final           bool   `json:"final_settlement"`
}

type AttributionReview struct {
	Schema      string            `json:"schema"`
	AccountID   string            `json:"account_id"`
	Writes      int               `json:"database_writes"`
	Commands    int               `json:"broker_commands"`
	Publishable bool              `json:"economic_nav_publishable"`
	Final       bool              `json:"final_settlement"`
	SourceSHA   string            `json:"review_sha256"`
	Sources     map[string]string `json:"source_sha256"`
	Days        []AttributionDay  `json:"days"`
	Parts       []AttributionPart `json:"daily_cycle_parts"`
	Statements  struct {
		Days []AttributionStatementDay `json:"days"`
	} `json:"statement_daily_comparison"`
}

var attributionDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,9})(\.[0-9]{1,6})?$`)

// Exact micro-yuan arithmetic; reject exponent notation and silent rounding.
func attributionMoney(values ...string) ([]int64, error) {
	r := make([]int64, len(values))
	for i, s := range values {
		if !attributionDecimal.MatchString(s) {
			return nil, errors.New("invalid attribution decimal")
		}
		n, ok := new(big.Rat).SetString(s)
		if !ok {
			return nil, errors.New("invalid attribution money")
		}
		n.Mul(n, big.NewRat(1000000, 1))
		if !n.IsInt() || !n.Num().IsInt64() {
			return nil, errors.New("attribution money precision")
		}
		r[i] = n.Num().Int64()
		if r[i] < -1000000000000000 || r[i] > 1000000000000000 {
			return nil, errors.New("attribution money bound")
		}
	}
	return r, nil
}

func attributionStrategySum(rows map[string]string) (int64, error) {
	if len(rows) == 0 || len(rows) > 4 {
		return 0, errors.New("invalid attribution strategies")
	}
	var n int64
	for k, s := range rows {
		if k != "stock_cross_section" && k != "etf_cross_section" && k != "etf_redemption_t0" && k != "cash_management" {
			return 0, errors.New("unknown attribution strategy")
		}
		v, e := attributionMoney(s)
		if e != nil {
			return 0, e
		}
		n += v[0]
	}
	return n, nil
}

func validateAttributionReview(d CycleReviewDocument) error {
	a := d.Attribution
	if a == nil {
		return nil
	}
	bad := func() error { return errors.New("invalid daily attribution review") }
	if a.Schema != "relay.daily_attribution_bridge.v1" || a.AccountID != d.AccountID || a.Writes != 0 || a.Commands != 0 || a.Publishable || a.Final || !cycleEvidenceHash.MatchString(a.SourceSHA) || len(a.Sources) == 0 || len(a.Sources) > 20 || d.Daily == nil || len(a.Days) != len(d.Daily.Days) || len(a.Parts) > 10000 {
		return bad()
	}
	for _, h := range a.Sources {
		if !cycleEvidenceHash.MatchString(h) {
			return bad()
		}
	}
	cycleNet, cycleFees := map[string]int64{}, map[string]int64{}
	partIDs := map[string]bool{}
	partDays := map[string][]AttributionPart{}
	for _, p := range a.Parts {
		id := p.Date + "/" + p.CycleID + "/" + p.Kind + "/" + p.SecurityID
		if partIDs[id] {
			return bad()
		}
		partIDs[id] = true
		v, e := attributionMoney(p.Net, p.OldNet, p.Fee, p.Correction)
		if e != nil || v[2] < 0 || v[0]-v[1] != v[3] || (p.Kind != "redemption_source" && p.Kind != "overnight_etf" && p.Kind != "carried_component") {
			return bad()
		}
		cycleNet[p.CycleID] += v[0]
		cycleFees[p.CycleID] += v[2]
		if cycleNet[p.CycleID] < -1000000000000000 || cycleNet[p.CycleID] > 1000000000000000 || cycleFees[p.CycleID] > 1000000000000000 {
			return bad()
		}
		partDays[p.Date] = append(partDays[p.Date], p)
	}
	if len(cycleNet) != len(d.Rows) {
		return bad()
	}
	for _, c := range d.Rows {
		if c.Profit.NetCyclePnLCents < -100000000000 || c.Profit.NetCyclePnLCents > 100000000000 || c.Profit.Trading.OrdinaryFeeCents > 100000000000 {
			return bad()
		}
		if c.Profit.NetCyclePnLCents*10000 != cycleNet[c.Profit.CycleID] || c.Profit.Trading.OrdinaryFeeCents*10000 != cycleFees[c.Profit.CycleID] {
			return bad()
		}
	}
	byDate := map[string]AttributionDay{}
	for i, r := range a.Days {
		basis := d.Daily.Days[i].Account
		for _, n := range []int64{basis.PublishedPnLCents, basis.OtherClosingCents, basis.OtherOpeningCents} {
			if n < -100000000000 || n > 100000000000 {
				return bad()
			}
		}
		fee := r.RepoFee
		if fee == "" {
			fee = "0"
		}
		v, e := attributionMoney(r.Published, r.Model, r.T0, r.Repo, fee, r.Net, r.Residual, r.Other, r.Comparison, r.Interest, r.ModelInterest)
		if e != nil || r.Date != basis.TradeDate || r.Status != "conditional_bridge_not_final" || r.AssetVerified || v[0] != basis.PublishedPnLCents*10000 || v[7] != (basis.OtherClosingCents-basis.OtherOpeningCents)*10000 || v[1]+v[2]+v[3]+v[4] != v[5] || v[0]-v[5] != v[6] || v[6]-v[7] != v[8] || v[9]-v[10] != v[3] {
			return bad()
		}
		if r.RepoFee != "" {
			f, e := attributionMoney(r.ModelRepoFee, r.StatementRepoFee)
			if e != nil || f[0] < 0 || f[1] < 0 || f[0]-f[1] != v[4] {
				return bad()
			}
		}
		m, e := attributionStrategySum(r.ModelStrategies)
		if e != nil || m != v[1] {
			return bad()
		}
		n, e := attributionStrategySum(r.Strategies)
		if e != nil || n != v[5] {
			return bad()
		}
		for _, k := range []string{"stock_cross_section", "etf_cross_section", "etf_redemption_t0", "cash_management"} {
			old, new := r.ModelStrategies[k], r.Strategies[k]
			if old == "" {
				old = "0"
			}
			if new == "" {
				new = "0"
			}
			x, e := attributionMoney(old, new)
			if e != nil {
				return bad()
			}
			delta := int64(0)
			if k == "etf_redemption_t0" {
				delta = v[2]
			}
			if k == "cash_management" {
				delta = v[3] + v[4]
			}
			if x[1]-x[0] != delta {
				return bad()
			}
		}
		parts := partDays[r.Date]
		if len(parts) != len(r.Parts) || (len(parts) > 0 && !reflect.DeepEqual(parts, r.Parts)) {
			return bad()
		}
		var correction int64
		for _, p := range parts {
			x, _ := attributionMoney(p.Correction)
			correction += x[0]
			if correction < -1000000000000000 || correction > 1000000000000000 {
				return bad()
			}
		}
		if correction != v[2] {
			return bad()
		}
		delete(partDays, r.Date)
		byDate[r.Date] = r
	}
	if len(partDays) > 0 {
		return bad()
	}
	seen := map[string]bool{}
	for _, s := range a.Statements.Days {
		r, ok := byDate[s.Date]
		v, e := attributionMoney(s.Buy, s.Sell, s.Fee, s.MarketChange, s.PnL, s.Published, s.Difference, s.ValuationEffect, s.TurnoverEffect, s.FeeEffect, s.Unexplained)
		if !ok || seen[s.Date] || e != nil || !s.IdentityMatched || s.Final || s.Orders <= 0 || s.Orders > 100000 || v[0] < 0 || v[1] < 0 || v[2] < 0 {
			return bad()
		}
		x, e := attributionMoney(r.Published, r.Interest, r.Residual)
		if e != nil {
			return bad()
		}
		if v[3]+v[1]-v[0]+x[1]-v[2] != v[4] || x[0] != v[5] || v[5]-v[4] != v[6] || x[2]-v[7]-v[8]-v[9] != v[10] {
			return bad()
		}
		seen[s.Date] = true
	}
	return nil
}
