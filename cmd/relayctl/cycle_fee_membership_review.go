package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/trading"
)

const cycleMembershipInputSHA = "18915395b9340868c63f6259d838c025ceeb7ca58bb7d0cf0d914e0920d053f1"

// Only for a second read of a fully verified capture. Unlike sequential replay,
// this requires every occurrence of a request to have identical evidence.
type researchEvidenceLookup struct {
	records []researchInputRecord
	failure error
}

func sameFrozenEvidence(method string, a, b json.RawMessage) bool {
	if !strings.HasPrefix(method, "market.") {
		return sameResearchJSON(a, b)
	}
	normalize := func(raw json.RawMessage) json.RawMessage {
		var root, payload, meta map[string]json.RawMessage
		if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["Payload"], &payload) != nil || json.Unmarshal(payload["meta"], &meta) != nil {
			return raw
		}
		// Per-request tracing is not an economic fact. Preserve data, cursors,
		// timestamps, scope, status, URL and every other metadata field.
		delete(meta, "request_id")
		payload["meta"], _ = json.Marshal(meta)
		root["Payload"], _ = json.Marshal(payload)
		b, _ := json.Marshal(root)
		return b
	}
	return sameResearchJSON(normalize(a), normalize(b))
}

func (l *researchEvidenceLookup) read(method string, request, target any) error {
	if l.failure != nil {
		return l.failure
	}
	want, err := json.Marshal(request)
	if err != nil {
		l.failure = err
		return err
	}
	var found *researchInputRecord
	for i := range l.records {
		r := &l.records[i]
		if r.Method != method || !sameResearchRequest(method, r.Request, want) {
			continue
		}
		if r.Error != "" || (found != nil && !sameFrozenEvidence(method, found.Response, r.Response)) {
			l.failure = fmt.Errorf("failed/conflicting frozen evidence %s %s", method, want)
			return l.failure
		}
		found = r
	}
	if found == nil {
		l.failure = fmt.Errorf("missing frozen evidence %s %s", method, want)
		return l.failure
	}
	l.failure = json.Unmarshal(found.Response, target)
	return l.failure
}

type cycleFeeMember struct {
	Cycle    string `json:"cycle_id"`
	Date     string `json:"trade_date"`
	Order    string `json:"gateway_order_id"`
	Security string `json:"security_id"`
	Side     string `json:"side"`
	FillID   string `json:"fill_id"`
	Quantity int64  `json:"quantity"`
	Gross    int64  `json:"gross_micros"`
}

func collectCycleFeeMembers(account, date string, groups []performance.T0FeeMembership) ([]cycleFeeMember, error) {
	var members []cycleFeeMember
	seen := map[string]bool{}
	for _, g := range groups {
		if g.GroupID == "" || seen[g.GroupID] {
			return nil, errors.New("duplicate fee membership group")
		}
		seen[g.GroupID] = true
		for kind, fills := range [][]trading.Fill{g.BuyFills, g.ComponentFills} {
			for _, f := range fills {
				price, err := fuyingRepoScaled(f.Price, 1000000)
				if err != nil || price <= 0 || price > 100000000000 || f.Qty <= 0 || f.Qty > 1000000000 || price > 100000000000000000/f.Qty || f.AccountID != account || f.GatewayOrderID == "" || f.TradeDate > date || f.TradeDate == "" || (kind == 0 && f.TradeSide != trading.TradeSideBuy) || (kind == 1 && (f.TradeSide != trading.TradeSideSell || f.TradeDate != date)) || f.BusinessType != trading.BusinessTypeStock {
					return nil, errors.New("invalid selected ordinary execution")
				}
				members = append(members, cycleFeeMember{Cycle: date + "/" + g.SecurityID, Date: f.TradeDate, Order: f.GatewayOrderID, Security: f.Symbol + "." + string(f.Exchange), Side: string(f.TradeSide), FillID: f.FillID, Quantity: f.Qty, Gross: price * f.Qty})
			}
		}
	}
	return members, nil
}

type cycleMembershipReview struct {
	Members    []cycleFeeMember
	Components []performance.CycleComponentReview
}

func reviewFuyingCycleFeeMembers(trades []performance.ETFCycleTradingEvidence) (*cycleMembershipReview, error) {
	r, err := loadResearchInputReplay("outputs/recovery/fuying13_engine_capture_20261114/run-1", cycleMembershipInputSHA)
	if err != nil {
		return nil, err
	}
	lookup := &researchEvidenceLookup{records: r.records}
	// This audits ownership against a sealed result, not a replay of today's
	// entire NAV/cost engine. Verify the reused grouping implementations exactly.
	var hashes map[string]string
	if err := lookup.read("code_fingerprints", nil, &hashes); err != nil {
		return nil, err
	}
	for _, name := range []string{"contributions.go", "etf_transfer_links.go", "component_residual_preview.go", "t0_allocation.go", "t0_day_pool_preview.go", "overnight_t0_chain.go", "overnight_t0_preview.go"} {
		path := "internal/performance/" + name
		data, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != hashes[path] {
			return nil, fmt.Errorf("ownership source differs: %s", path)
		}
	}
	var scope struct {
		Config config.PerformanceConfig `json:"performance_config"`
		Now    time.Time                `json:"fixed_calculation_time"`
	}
	if err := lookup.read("scope", nil, &scope); err != nil {
		return nil, err
	}
	capture := &researchInputCapture{lookup: lookup}
	store, market := &capturedResearchStore{capture: capture}, &capturedResearchMarket{capture: capture}
	cfg := scope.Config
	s, err := performance.New(performance.Options{Store: store, Calendar: market, Market: market, Now: func() time.Time { return scope.Now }, FormulaVersion: cfg.FormulaVersion,
		ComponentResidualPreview: true, ClosedDayT0PoolPreviewDate: "2026-10-16",
		OvernightT0Allocations: []performance.OvernightT0Allocation{{AccountID: fuying13Account, SourceDate: "2026-11-03", RedemptionDate: "2026-11-04", SecurityID: "159915.SZ", GroupID: fuyingOvernightGroup, SourceBuyOrderID: fuyingOvernightBuy, RedemptionOrderID: fuyingOvernightRedemption, Quantity: 1000000, EvidenceRef: "testdata/fixtures/fuying13_overnight_review_20261111.json"}}})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var members []cycleFeeMember
	components := map[string]*performance.CycleComponentReview{}
	for _, t := range trades {
		day := strings.SplitN(t.CycleID, "/", 2)[0]
		if seen[day] {
			continue
		}
		seen[day] = true
		groups, err := s.ReviewT0FeeMembership(context.Background(), fuying13Account, day)
		if err != nil {
			return nil, err
		}
		if lookup.failure != nil {
			return nil, lookup.failure
		}
		m, err := collectCycleFeeMembers(fuying13Account, day, groups)
		if err != nil {
			return nil, err
		}
		members = append(members, m...)
		for _, g := range groups {
			key := day + "/" + g.SecurityID
			if components[key] == nil {
				components[key] = &performance.CycleComponentReview{CycleID: key, InputSHA256: cycleMembershipInputSHA, CostSHA256: cycleCostEvidenceSHA, Gaps: []performance.CycleComponentGap{}}
			}
			components[key].Groups++
			components[key].Gaps = append(components[key].Gaps, g.ComponentGaps...)
		}
	}
	if err := verifyFrozenFeeFills(lookup, members); err != nil {
		return nil, err
	}
	result := &cycleMembershipReview{Members: members}
	for _, c := range components {
		sort.Slice(c.Gaps, func(i, j int) bool {
			return c.Gaps[i].ParentID+"/"+c.Gaps[i].SecurityID < c.Gaps[j].ParentID+"/"+c.Gaps[j].SecurityID
		})
		result.Components = append(result.Components, *c)
	}
	sort.Slice(result.Components, func(i, j int) bool { return result.Components[i].CycleID < result.Components[j].CycleID })
	return result, nil
}

func verifyFrozenFeeFills(lookup *researchEvidenceLookup, members []cycleFeeMember) error {
	full := map[string]trading.Fill{}
	dates := map[string]bool{}
	for _, m := range members {
		dates[m.Date] = true
	}
	for day := range dates {
		complete := false
		for offset := 0; offset < 100000; offset += 500 {
			var batch []trading.Fill
			q := trading.FillQuery{AccountID: fuying13Account, TradeDate: day, History: true, Limit: 500, Cursor: strconv.Itoa(offset)}
			if err := lookup.read("store.ListFills", q, &batch); err != nil {
				return err
			}
			for _, f := range batch {
				key := f.TradeDate + "/" + string(f.Exchange) + "/" + f.FillID
				if _, exists := full[key]; exists || f.AccountID != fuying13Account || f.TradeDate != day || f.FillID == "" || f.Qty < 0 || f.Qty > 1000000000000 {
					return errors.New("duplicate/cross-scope raw fee fill")
				}
				full[key] = f
			}
			if len(batch) < 500 {
				complete = true
				break
			}
		}
		if !complete {
			return errors.New("incomplete fee fill pagination")
		}
	}
	used := map[string]int64{}
	for _, m := range members {
		parts := strings.Split(m.Security, ".")
		if len(parts) != 2 {
			return errors.New("invalid fee security")
		}
		key := m.Date + "/" + parts[1] + "/" + m.FillID
		f, ok := full[key]
		price, err := fuyingRepoScaled(f.Price, 1000000)
		if !ok || err != nil || price <= 0 || f.GatewayOrderID != m.Order || f.Symbol != parts[0] || string(f.TradeSide) != m.Side || f.BusinessType != trading.BusinessTypeStock || m.Quantity <= 0 || m.Gross/m.Quantity != price || m.Gross%m.Quantity != 0 || used[key] > f.Qty-m.Quantity {
			return errors.New("selected fill exceeds or disagrees with frozen original")
		}
		used[key] += m.Quantity
	}
	return nil
}

type cycleOrdinaryFeeReview struct {
	Cycle      string   `json:"cycle_id"`
	Orders     int      `json:"orders"`
	FeeCents   int64    `json:"matched_fee_cents"`
	Applied    bool     `json:"applied"`
	Unresolved []string `json:"unresolved_orders"`
}

func applyCycleOrdinaryFees(trades []performance.ETFCycleTradingEvidence, members []cycleFeeMember, evidence cycleExecutionFeeEvidence) ([]performance.ETFCycleTradingEvidence, []cycleOrdinaryFeeReview, error) {
	if evidence.Schema != "relay.broker_execution_fee_association.v1" || evidence.Account != fuying13Account || evidence.Writes != 0 || evidence.Commands != 0 || len(trades) == 0 || len(trades) > 1000 || len(members) > 100000 || len(evidence.OC.Orders) > 100000 {
		return nil, nil, errors.New("invalid fee evidence scope")
	}
	fees := map[string]cycleExecutionFeeOrder{}
	for _, o := range evidence.OC.Orders {
		key := o.Date + "/" + o.Order
		if _, ok := fees[key]; ok {
			return nil, nil, errors.New("duplicate fee order")
		}
		fees[key] = o
	}
	type total struct{ qty, gross int64 }
	alloc := map[string]map[string]total{}
	buy, sell := map[string]total{}, map[string]total{}
	for _, m := range members {
		if m.Quantity <= 0 || m.Quantity > 1000000000 || m.Gross <= 0 || m.Gross > 100000000000000000 || (m.Side != "B" && m.Side != "S") {
			return nil, nil, errors.New("invalid member money")
		}
		key := m.Date + "/" + m.Order
		o, ok := fees[key]
		if !ok || o.Side != m.Side || o.Security != m.Security {
			return nil, nil, fmt.Errorf("selected order absent from fee evidence: %s", key)
		}
		if alloc[key] == nil {
			alloc[key] = map[string]total{}
		}
		a := alloc[key][m.Cycle]
		if a.gross > 100000000000000000-m.Gross || a.qty > 1000000000000-m.Quantity {
			return nil, nil, errors.New("fee allocation bound")
		}
		a.qty += m.Quantity
		a.gross += m.Gross
		alloc[key][m.Cycle] = a
		bucket := buy
		if m.Side == "S" {
			bucket = sell
		}
		v := bucket[m.Cycle]
		if v.gross > 100000000000000000-m.Gross {
			return nil, nil, errors.New("cycle money bound")
		}
		v.qty += m.Quantity
		v.gross += m.Gross
		bucket[m.Cycle] = v
	}
	reviews := map[string]*cycleOrdinaryFeeReview{}
	result := append([]performance.ETFCycleTradingEvidence(nil), trades...)
	for _, t := range trades {
		b, s := buy[t.CycleID], sell[t.CycleID]
		if t.AccountID != evidence.Account || reviews[t.CycleID] != nil || b.qty != t.AcquisitionQuantity || t.AcquisitionGrossCents > 10000000000000 || t.ComponentSaleCents > 10000000000000 || b.gross != t.AcquisitionGrossCents*10000 || s.gross != t.ComponentSaleCents*10000 {
			return nil, nil, fmt.Errorf("cycle selected execution totals differ: %s buy=%+v sell=%+v", t.CycleID, b, s)
		}
		reviews[t.CycleID] = &cycleOrdinaryFeeReview{Cycle: t.CycleID, Unresolved: []string{}}
	}
	for key, cohorts := range alloc {
		o := fees[key]
		var qty, gross int64
		for _, a := range cohorts {
			if gross > 100000000000000000-a.gross {
				return nil, nil, errors.New("shared order money bound")
			}
			qty += a.qty
			gross += a.gross
		}
		if qty > o.Quantity || gross > o.Gross {
			return nil, nil, fmt.Errorf("fee execution overallocated: %s", key)
		}
		resolved := len(cohorts) == 1 && qty == o.Quantity && gross == o.Gross && o.Status == "matched" && o.Fee != nil && *o.Fee >= 0 && *o.Fee <= 100000000000
		for cycle := range cohorts {
			r := reviews[cycle]
			if r == nil {
				return nil, nil, errors.New("unknown fee cohort")
			}
			r.Orders++
			if resolved {
				r.FeeCents += *o.Fee
			} else {
				r.Unresolved = append(r.Unresolved, key)
			}
		}
	}
	var output []cycleOrdinaryFeeReview
	for i := range result {
		g := &result[i]
		r := reviews[g.CycleID]
		sort.Strings(r.Unresolved)
		if len(r.Unresolved) == 0 && r.Orders > 0 {
			g.OrdinaryFeeCents = r.FeeCents
			g.FeeBasis = "broker_cash_reconciled"
			r.Applied = true
			g.EvidenceRefs = append(append([]string(nil), g.EvidenceRefs...), "sha256:"+cycleExecutionFeeSHA+"#oc_comparison/orders", "sha256:"+cycleMembershipInputSHA)
		} else {
			g.QualityFlags = append(append([]string(nil), g.QualityFlags...), "ordinary_fee_shared_or_partial_order_unverified")
		}
		output = append(output, *r)
	}
	return result, output, nil
}
