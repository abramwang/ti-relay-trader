package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ti-relay-trader/internal/performance"
)

const cycleCostEvidencePath = "outputs/recovery/fuying13_engine_capture_20261114/run-1-result.json"
const cycleCostEvidenceSHA = "44770cd720b96bb3fc8345b1548ce9fec8b7356835a1e7add7bfb03185f21eef"
const cycleParentFeePath = "outputs/recovery/fuying13_cash_matrix_20261112/redemption_fees.json"
const cycleParentFeeSHA = "4491e548459307e452b2f5e5c0132c5865bf07efda2da4fdb4f5072fd81c5d46"
const cycleExecutionFeePath = "outputs/recovery/fuying13_cycle_review_20261115/fees-invariant-v2/audit.json"
const cycleExecutionFeeSHA = "300c54615ab38cad5f8b6746fd6805d3462bb9d10ebb180adf43b231fb0ab68f"

type cycleExecutionFeeOrder struct {
	Date     string `json:"trade_date"`
	Order    string `json:"gateway_order_id"`
	Security string `json:"security_id"`
	Side     string `json:"side"`
	Quantity int64  `json:"quantity"`
	Gross    int64  `json:"oc_gross_micros"`
	Fee      *int64 `json:"fee_cents"`
	Status   string `json:"status"`
}

type cycleExecutionFeeEvidence struct {
	Schema   string `json:"schema"`
	Account  string `json:"account_id"`
	Writes   int    `json:"database_writes"`
	Commands int    `json:"broker_commands"`
	OC       struct {
		Orders []cycleExecutionFeeOrder `json:"orders"`
	} `json:"oc_comparison"`
}

func applyCycleLaterExecutionFees(trades []performance.ETFCycleTradingEvidence, cost cycleCostEvidence, parents map[string]string, evidence cycleExecutionFeeEvidence) ([]performance.ETFCycleTradingEvidence, error) {
	if evidence.Schema != "relay.broker_execution_fee_association.v1" || evidence.Account != cost.Account || evidence.Writes != 0 || evidence.Commands != 0 || len(evidence.OC.Orders) == 0 || len(evidence.OC.Orders) > 100000 {
		return nil, errors.New("invalid cycle execution fee evidence")
	}
	bySecurity := map[string][]cycleExecutionFeeOrder{}
	seen := map[string]bool{}
	for _, o := range evidence.OC.Orders {
		key := o.Date + "/" + o.Order
		if o.Order == "" || seen[key] {
			return nil, errors.New("duplicate/missing cycle execution order")
		}
		seen[key] = true
		if o.Side == "S" {
			bySecurity[o.Date+"/"+o.Security] = append(bySecurity[o.Date+"/"+o.Security], o)
		}
	}
	sales, fees, refs := map[string]int64{}, map[string]int64{}, map[string][]string{}
	used := map[string]bool{}
	for _, d := range cost.Contributions {
		if d.AccountID != cost.Account {
			return nil, errors.New("cross-account component fees")
		}
		for _, x := range d.Contributions {
			if !strings.HasPrefix(x.StrategyID, "ETF_T0_COMPONENT:") || x.SellQuantity == 0 {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(x.StrategyID, "ETF_T0_COMPONENT:"), ":", 2)
			if len(parts) != 2 || x.ActualFee != 0 || x.CloseQuantity != 0 || x.BuyQuantity != 0 || x.SellQuantity != x.OpenQuantity {
				return nil, errors.New("component fee already included or liquidation incomplete")
			}
			cycle := parents[parts[0]+"/"+parts[1]]
			key := d.TradeDate + "/" + x.SecurityID
			// Only an entire independently matched security/day sell bucket can
			// be assigned here. Shared baskets require explicit allocation instead.
			if cycle == "" || d.TradeDate <= parts[0] || used[key] || len(bySecurity[key]) == 0 {
				return nil, errors.New("missing/ambiguous later component fee scope")
			}
			used[key] = true
			var qty, gross int64
			for _, o := range bySecurity[key] {
				if o.Status != "matched" || o.Fee == nil || *o.Fee < 0 || *o.Fee > 100000000000 || o.Quantity <= 0 || o.Quantity > 1000000000 || o.Gross <= 0 || o.Gross > 100000000000000 {
					return nil, errors.New("later component fee/money not independently matched")
				}
				qty += o.Quantity
				if gross > 100000000000000000-o.Gross {
					return nil, errors.New("later component gross exceeds review bound")
				}
				gross += o.Gross
				fees[cycle] += *o.Fee
				refs[cycle] = append(refs[cycle], "sha256:"+cycleExecutionFeeSHA+"#"+o.Date+"/"+o.Order)
			}
			amount, err := cycleMoney(x.SellAmount)
			if err != nil || qty != x.SellQuantity || gross != amount*10000 {
				return nil, errors.New("later component sell quantity/gross disagrees")
			}
			sales[cycle] += amount
		}
	}
	result := append([]performance.ETFCycleTradingEvidence(nil), trades...)
	for i := range result {
		g := &result[i]
		if g.AccountID != cost.Account || g.LaterComponentSales != sales[g.CycleID] {
			return nil, errors.New("later component cycle coverage disagrees")
		}
		if g.LaterComponentSales == 0 {
			continue
		}
		flags := []string{}
		removed := false
		for _, flag := range g.QualityFlags {
			if flag == "later_component_sale_fee_allocation_unverified" {
				removed = true
			} else {
				flags = append(flags, flag)
			}
		}
		if !removed {
			return nil, errors.New("later fee previously applied or scope not declared")
		}
		g.OrdinaryFeeCents += fees[g.CycleID]
		g.QualityFlags = flags
		g.EvidenceRefs = append(append([]string(nil), g.EvidenceRefs...), refs[g.CycleID]...)
		delete(sales, g.CycleID)
	}
	if len(sales) != 0 {
		return nil, errors.New("unconsumed later fee cohort")
	}
	return result, nil
}

type cycleCostEvidence struct {
	Account       string                           `json:"account_id"`
	ReadOnly      bool                             `json:"database_read_only"`
	Writes        int                              `json:"database_writes"`
	Commands      int                              `json:"broker_commands"`
	Contributions []performance.ContributionResult `json:"contributions"`
	Cash          struct {
		Overnight fuyingETFCashProof `json:"overnight_etf_cash_evidence"`
	} `json:"broker_cash_evidence"`
}

func cycleMoney(value float64) (int64, error) {
	micros, err := fuyingRepoScaled(value, 1000000)
	if err != nil || micros < 0 {
		return 0, errors.New("invalid nonnegative cycle money")
	}
	return (micros + 5000) / 10000, nil
}

func buildCycleTradingEvidence(cash performance.ETFCashCycleReview, cost cycleCostEvidence, parentFees map[string]int64, parentCycle map[string]string, redemptionQuantity map[string]int64) ([]performance.ETFCycleTradingEvidence, error) {
	if cost.Account != cash.AccountID || !cost.ReadOnly || cost.Writes != 0 || cost.Commands != 0 {
		return nil, errors.New("wrong cycle cost evidence scope")
	}
	groups := map[string]*performance.ETFCycleTradingEvidence{}
	fees := map[string]int64{}
	seen := map[string]bool{}
	for _, d := range cost.Contributions {
		if d.AccountID != cash.AccountID || seen[d.TradeDate] {
			return nil, errors.New("duplicate/cross-account contribution day")
		}
		seen[d.TradeDate] = true
		groupIDs := map[string]bool{}
		for _, x := range d.Contributions {
			if x.RedemptionQuantity <= 0 {
				continue
			}
			if x.StrategyType != performance.StrategyETFRedemptionT0 || x.StrategyID == "" || groupIDs[x.StrategyID] {
				return nil, errors.New("invalid/duplicate redemption group")
			}
			groupIDs[x.StrategyID] = true
			key := d.TradeDate + "/" + x.SecurityID
			g := groups[key]
			if g == nil {
				g = &performance.ETFCycleTradingEvidence{AccountID: cash.AccountID, CycleID: key, AcquisitionDate: d.TradeDate, FeeScope: "ordinary_buys_and_component_sales_only", FeeBasis: "oc_fee_snapshot_not_final", EvidenceRefs: []string{"sha256:" + cycleCostEvidenceSHA + "#" + key, "sha256:" + cycleParentFeeSHA + "#" + key}}
				groups[key] = g
			}
			buy, e1 := cycleMoney(x.BuyAmount)
			sales, e2 := cycleMoney(x.LinkedComponentSales)
			fee, e3 := fuyingRepoScaled(x.ActualFee, 1000000)
			if e1 != nil || e2 != nil || e3 != nil || fee < 0 {
				return nil, errors.New("invalid cycle contribution money")
			}
			g.AcquisitionQuantity += x.BuyQuantity
			g.RedemptionQuantity += x.RedemptionQuantity
			g.AcquisitionGrossCents += buy
			g.ComponentSaleCents += sales
			fees[key] += fee
			// Preserve provenance and gaps, but do not carry obsolete IOPV/model
			// warnings into an actual-cash formula that no longer uses that model.
			for _, flag := range x.QualityFlags {
				if flag == "historical_t0_order_group_inferred" || flag == "component_transfer_sell_quantity_mismatch" || flag == "etf_t0_execution_fee_incomplete" || flag == "per_redemption_buy_allocation_unasserted" {
					g.QualityFlags = append(g.QualityFlags, flag)
				}
			}
		}
	}
	for key, g := range groups {
		parent, ok := parentFees[key]
		if !ok || fees[key] < parent || g.RedemptionQuantity != redemptionQuantity[key] {
			return nil, errors.New("parent fee/redemption coverage mismatch")
		}
		// Signed ETF cash already contains these parent charges. Remove the OC
		// parent fee before subtracting ordinary buy/sell execution fees.
		g.OrdinaryFeeCents = (fees[key] - parent + 5000) / 10000
	}
	for _, d := range cost.Contributions {
		for _, x := range d.Contributions {
			if !strings.HasPrefix(x.StrategyID, "ETF_T0_COMPONENT:") || x.SellQuantity == 0 {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(x.StrategyID, "ETF_T0_COMPONENT:"), ":", 2)
			if len(parts) != 2 {
				return nil, errors.New("invalid carried component parent")
			}
			key := parentCycle[parts[0]+"/"+parts[1]]
			g := groups[key]
			if g == nil || d.TradeDate <= parts[0] || x.CloseQuantity != 0 || x.BuyQuantity != 0 || x.SellQuantity != x.OpenQuantity {
				return nil, errors.New("carried component not fully liquidated in reviewed evidence")
			}
			amount, err := cycleMoney(x.SellAmount)
			if err != nil {
				return nil, err
			}
			g.LaterComponentSales += amount
			g.QualityFlags = append(g.QualityFlags, "later_component_sale_fee_allocation_unverified")
			g.EvidenceRefs = append(g.EvidenceRefs, "sha256:"+cycleCostEvidenceSHA+"#"+d.TradeDate+"/"+x.StrategyID+"/"+x.SecurityID)
		}
	}
	// This already reviewed overnight cycle has stronger ordinary-fee evidence.
	// Revalidate the raw cash rows, rather than trusting its saved PnL summary.
	proof := cost.Cash.Overnight
	if len(proof.Orders) != 2 || len(proof.Transfers) != 99 || len(proof.Executions) != 99 {
		return nil, errors.New("overnight cycle proof incomplete")
	}
	flows := append([]brokerCashFlowRow(nil), proof.Deferred...)
	flows = append(flows, proof.Commission, proof.Receipt)
	expected := map[string]int64{}
	for _, t := range proof.Transfers {
		if t.ComponentSymbol != "159915" {
			if expected[t.ComponentSymbol] != 0 {
				return nil, errors.New("duplicate overnight component")
			}
			expected[t.ComponentSymbol] = t.ComponentQty
		}
	}
	proof.Deferred = nil
	proof.Commission = brokerCashFlowRow{}
	proof.Receipt = brokerCashFlowRow{}
	if err := completeFuyingETFCash(&proof, flows, expected); err != nil {
		return nil, err
	}
	key := "2026-11-04/159915.SZ"
	g := groups[key]
	if g == nil || g.AcquisitionQuantity != 0 || g.RedemptionQuantity != 1000000 {
		return nil, errors.New("overnight cycle cost scope mismatch")
	}
	buy, err := cycleMoney(proof.Summary["buy_gross"].(float64))
	if err != nil {
		return nil, err
	}
	buyFee, err := cycleMoney(proof.Summary["buy_fee"].(float64))
	if err != nil {
		return nil, err
	}
	sellFee, err := cycleMoney(proof.Summary["component_sale_fees"].(float64))
	if err != nil {
		return nil, err
	}
	sell, err := cycleMoney(proof.Summary["component_sale_gross"].(float64))
	if err != nil || sell != g.ComponentSaleCents {
		return nil, errors.New("overnight component sales disagree")
	}
	g.AcquisitionDate = "2026-11-03"
	g.AcquisitionQuantity = 1000000
	g.AcquisitionGrossCents = buy
	g.OrdinaryFeeCents = buyFee + sellFee
	g.FeeBasis = "broker_cash_reconciled"
	if len(g.QualityFlags) != 0 {
		return nil, errors.New("overnight unexpected evidence gaps")
	}
	result := []performance.ETFCycleTradingEvidence{}
	for _, c := range cash.Cycles {
		g := groups[c.CycleID]
		if g == nil {
			return nil, errors.New("cash has no trading cohort")
		}
		result = append(result, *g)
	}
	if len(result) != len(groups) {
		return nil, errors.New("trade cohort has no cash evidence")
	}
	return result, nil
}

func loadFuyingCycleProfit(cash performance.ETFCashCycleReview) (map[string]any, error) {
	var cost cycleCostEvidence
	if err := readPinnedJSON(cycleCostEvidencePath, cycleCostEvidenceSHA, &cost); err != nil {
		return nil, err
	}
	var matrix struct {
		Orders []struct {
			Date     string `json:"source_trade_date"`
			Security string `json:"security_id"`
			Order    string `json:"gateway_order_id"`
			Quantity int64  `json:"quantity"`
		} `json:"source_orders"`
	}
	if err := readPinnedJSON(fuyingCashMatrixPath, fuyingCashMatrixHash, &matrix); err != nil {
		return nil, err
	}
	parents, quantity := map[string]string{}, map[string]int64{}
	for _, o := range matrix.Orders {
		key := compactToISODate(o.Date) + "/" + o.Security
		id := compactToISODate(o.Date) + "/" + o.Order
		if parents[id] != "" || o.Quantity <= 0 {
			return nil, errors.New("duplicate/invalid redemption parent")
		}
		parents[id] = key
		quantity[key] += o.Quantity
	}
	var pf struct {
		Account  string `json:"account_id"`
		ReadOnly string `json:"transaction_read_only"`
		Records  []struct {
			Account  string `json:"account_id"`
			Date     string `json:"trade_date"`
			Order    string `json:"gateway_order_id"`
			Security string `json:"security_id"`
			Fee      string `json:"total_fee"`
		} `json:"records"`
	}
	if err := readPinnedJSON(cycleParentFeePath, cycleParentFeeSHA, &pf); err != nil {
		return nil, err
	}
	if pf.Account != cash.AccountID || pf.ReadOnly != "on" {
		return nil, errors.New("parent fee source scope invalid")
	}
	fees, seen := map[string]int64{}, map[string]bool{}
	for _, f := range pf.Records {
		id := f.Date + "/" + f.Order
		key := parents[id]
		value, err := strconv.ParseFloat(f.Fee, 64)
		if err != nil {
			return nil, err
		}
		micros, err := fuyingRepoScaled(value, 1000000)
		if err != nil || micros < 0 || f.Account != cash.AccountID || key != f.Date+"/"+f.Security || seen[id] {
			return nil, errors.New("invalid parent fee association")
		}
		seen[id] = true
		fees[key] += micros
	}
	if len(seen) != len(parents) {
		return nil, errors.New("parent fee coverage incomplete")
	}
	trades, err := buildCycleTradingEvidence(cash, cost, fees, parents, quantity)
	if err != nil {
		return nil, err
	}
	var executionFees cycleExecutionFeeEvidence
	if err := readPinnedJSON(cycleExecutionFeePath, cycleExecutionFeeSHA, &executionFees); err != nil {
		return nil, err
	}
	membership, err := reviewFuyingCycleFeeMembers(trades)
	if err != nil {
		return nil, err
	}
	members := membership.Members
	if err := attachCycleLaterSales(membership.Components, cost); err != nil {
		return nil, err
	}
	if err := loadCycleComponentCash(membership.Components, cash); err != nil {
		return nil, err
	}
	trades, feeReview, err := applyCycleOrdinaryFees(trades, members, executionFees)
	if err != nil {
		return nil, err
	}
	trades, err = applyCycleLaterExecutionFees(trades, cost, parents, executionFees)
	if err != nil {
		return nil, err
	}
	profits, err := performance.CalculateETFCycleProfits(cash, trades)
	if err != nil {
		return nil, err
	}
	for _, p := range profits {
		if p.CycleID == "2026-11-04/159915.SZ" && p.NetCyclePnLCents != 113473 {
			return nil, fmt.Errorf("independent overnight cash proof differs: %d", p.NetCyclePnLCents)
		}
	}
	return map[string]any{"schema": "relay.etf_cycle_profit_review.v1", "profits": profits, "component_reviews": membership.Components, "ordinary_fee_review": feeReview, "ordinary_fee_members": members, "source_sha256": map[string]string{"cost_review": cycleCostEvidenceSHA, "parent_fees": cycleParentFeeSHA, "execution_fees": cycleExecutionFeeSHA, "membership_inputs": cycleMembershipInputSHA}, "parent_fee_removed_micros": fees, "scope": "conditional cohort cash PnL; exact whole-order broker fees where membership closes, unresolved allocations retained; not account NAV", "economic_nav_publishable": false}, nil
}

func attachCycleLaterSales(reviews []performance.CycleComponentReview, cost cycleCostEvidence) error {
	for _, day := range cost.Contributions {
		if day.AccountID != cost.Account {
			return errors.New("component continuation account mismatch")
		}
		for _, x := range day.Contributions {
			if !strings.HasPrefix(x.StrategyID, "ETF_T0_COMPONENT:") || x.SellQuantity == 0 {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(x.StrategyID, "ETF_T0_COMPONENT:"), ":", 2)
			if len(parts) != 2 || day.TradeDate <= parts[0] || x.CloseQuantity != 0 || x.BuyQuantity != 0 || x.SellQuantity != x.OpenQuantity {
				return errors.New("incomplete later component sale")
			}
			matches := 0
			for i := range reviews {
				if !strings.HasPrefix(reviews[i].CycleID, parts[0]+"/") {
					continue
				}
				for j := range reviews[i].Gaps {
					g := &reviews[i].Gaps[j]
					if g.ParentID != parts[1] || g.SecurityID != x.SecurityID {
						continue
					}
					if g.LaterSaleDate != "" || g.OpenQty != 0 || g.CloseQty != g.RemainingQty || g.RemainingQty != x.SellQuantity {
						return errors.New("ambiguous later component quantity")
					}
					amount, err := cycleMoney(x.SellAmount)
					if err != nil || amount <= 0 {
						return errors.New("invalid later component amount")
					}
					g.LaterSaleDate, g.LaterSaleQty, g.LaterSaleCents = day.TradeDate, x.SellQuantity, amount
					matches++
				}
			}
			if matches != 1 {
				return errors.New("later component has no unique source gap")
			}
		}
	}
	return nil
}
