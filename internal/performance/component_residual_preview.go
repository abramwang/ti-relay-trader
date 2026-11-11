package performance

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

const componentResidualPrefix = "ETF_T0_COMPONENT:"
const componentResidualPreviewFlag = "t0_component_residual_preview_only"

func isComponentResidualBucket(bucket string) bool {
	return strings.HasPrefix(bucket, componentResidualPrefix)
}
func hasOpenComponentResidual(items []ledger.PositionCostState) bool {
	for _, item := range items {
		if isComponentResidualBucket(item.CostBucket) && item.CloseQuantity != 0 {
			return true
		}
	}
	return false
}

func componentResidualParent(bucket string) (date, parent string) {
	parts := strings.SplitN(strings.TrimPrefix(bucket, componentResidualPrefix), ":", 2)
	if len(parts) == 2 && isComponentResidualBucket(bucket) {
		return parts[0], parts[1]
	}
	return "", ""
}

func componentInventoryContested(fills []trading.Fill, open []trading.Position) map[string]bool {
	result := map[string]bool{}
	for _, p := range open {
		if p.Quantity != 0 {
			result[contributionSecurityID(p.Symbol, p.Exchange)] = true
		}
	}
	for _, f := range fills {
		if f.Qty > 0 && f.TradeSide != trading.TradeSideSell {
			result[contributionSecurityID(f.Symbol, f.Exchange)] = true
		}
	}
	return result
}

// Opaque basket labels are not parent identities. Only a unique available lot,
// no opening inventory/buys and strict event ordering permit this inference.
func completeUniqueComponentSales(links *componentSaleLinks, fills []trading.Fill, open []trading.Position) {
	contested := componentInventoryContested(fills, open)
	ordered := append([]trading.Fill(nil), fills...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := contributionFillTime(ordered[i]), contributionFillTime(ordered[j])
		if a.Equal(b) {
			return contributionFillKey(ordered[i]) < contributionFillKey(ordered[j])
		}
		return a.Before(b)
	})
	for _, f := range ordered {
		key := contributionSecurityID(f.Symbol, f.Exchange)
		at := contributionFillTime(f)
		if f.TradeSide != trading.TradeSideSell || f.Qty <= 0 || contested[key] || links.excludes(f) || at.IsZero() {
			continue
		}
		var eligible []*componentTransferGroup
		root := componentBasketRoot(firstNonBlank(f.BasketID, contributionString(f.AdapterContext["basket_id"])))
		for _, g := range links.groups {
			if g.matchedAt.IsZero() || !at.After(g.matchedAt) || g.expectedBySecurity[key] <= g.linkedBySecurity[key] {
				continue
			}
			if len(root) == 6 && strings.Trim(root, "0123456789") == "" && root != g.basketRoot {
				continue
			}
			eligible = append(eligible, g)
		}
		if len(eligible) != 1 {
			continue
		}
		g := eligible[0]
		if f.Qty > g.expectedBySecurity[key]-g.linkedBySecurity[key] {
			continue
		}
		g.linkedBySecurity[key] += f.Qty
		g.linkedFills = append(g.linkedFills, f)
		links.byFill[contributionFillKey(f)] = []componentSaleLink{{redemptionGatewayOrderID: g.redemptionGatewayOrderID, basketRoot: g.basketRoot, quantity: f.Qty}}
		links.flags = appendUnique(links.flags, "component_sale_unique_inventory_inferred", componentResidualPreviewFlag)
	}
}

func routeCarriedComponentSales(core, residuals map[string]*costWorkingState, fills []trading.Fill, links componentSaleLinks) map[string]string {
	routes := map[string]string{}
	remaining := map[string]int64{}
	for key, s := range residuals {
		remaining[key] = s.quantity
	}
	contested := componentInventoryContested(fills, nil)
	for _, g := range links.groups {
		for key, q := range g.expectedBySecurity {
			if q > 0 {
				contested[key] = true
			}
		}
	}
	for _, f := range fills {
		if f.TradeSide != trading.TradeSideSell || f.Qty <= 0 {
			continue
		}
		key := contributionSecurityID(f.Symbol, f.Exchange)
		root := componentBasketRoot(firstNonBlank(f.BasketID, contributionString(f.AdapterContext["basket_id"])))
		explicitBasket := len(root) == 6 && strings.Trim(root, "0123456789") == ""
		var eligible []string
		other := false
		for k, s := range residuals {
			if s.item.Symbol+"."+s.item.Exchange != key || remaining[k] <= 0 {
				continue
			}
			if isComponentResidualBucket(s.item.CostBucket) {
				eligible = append(eligible, k)
			} else {
				other = true
			}
		}
		if len(eligible) == 0 {
			continue
		}
		if core[key] == nil || core[key].quantity != 0 || contested[key] || other || explicitBasket || len(eligible) != 1 || f.Qty > remaining[eligible[0]] {
			for _, k := range eligible {
				blockCostState(residuals[k], "component_residual_sale_allocation_required")
			}
			continue
		}
		routes[contributionFillKey(f)] = eligible[0]
		remaining[eligible[0]] -= f.Qty
	}
	return routes
}

func createComponentResidualMarks(account, date string, core, residuals map[string]*costWorkingState, links componentSaleLinks, instruments map[string]contributionInstrument, fills []trading.Fill, closePositions map[string]trading.Position) []string {
	var flags []string
	contested := componentInventoryContested(fills, nil)
	counts := map[string]int{}
	for _, g := range links.groups {
		for key, q := range g.expectedBySecurity {
			if q > g.linkedBySecurity[key] {
				counts[key]++
			}
		}
	}
	for _, s := range residuals {
		if s.quantity > 0 {
			contested[s.item.Symbol+"."+s.item.Exchange] = true
		}
	}
	for _, g := range links.groups {
		for key, q := range g.expectedBySecurity {
			q -= g.linkedBySecurity[key]
			if q <= 0 {
				continue
			}
			// A transfer row alone does not prove physical inventory. Unknown
			// cash-like records remain unresolved, not fabricated stock lots.
			if closePositions[key].Quantity <= 0 {
				flags = appendUnique(flags, "component_transfer_without_closing_inventory_unclassified", componentResidualPreviewFlag)
				continue
			}
			symbol, exchange := splitContributionSecurityID(key)
			s := newCostWorkingState(account, date, symbol, exchange, "meridian_source_close_component_mark")
			s.item.CostBucket = componentResidualPrefix + date + ":" + g.redemptionGatewayOrderID
			s.quantity = q
			s.item.Status = "estimated"
			s.flags = appendUnique(s.flags, componentResidualPreviewFlag, "component_marked_basis_not_acquisition_cost")
			instrument := instruments[key]
			if instrument.InstrumentType != "stock" {
				blockCostState(s, "component_residual_instrument_type_unconfirmed")
			}
			if !instrument.HasClose || instrument.Close <= 0 || math.IsNaN(instrument.Close) || math.IsInf(instrument.Close, 0) {
				blockCostState(s, "missing_meridian_component_mark")
			} else {
				s.cost = roundMoney(float64(q) * instrument.Close)
			}
			if core[key] == nil || core[key].quantity != 0 || contested[key] || counts[key] != 1 {
				blockCostState(s, "component_residual_ownership_requires_review")
			}
			residuals[residualCostKey(key, s.item.CostBucket)] = s
		}
	}
	return flags
}

func splitComponentContributionBucket(bucket contributionBucket, costs []ledger.PositionCostState, links componentSaleLinks, openNAV float64) (contributionBucket, []SecurityContribution, error) {
	core := bucket
	var result []SecurityContribution
	var sold int64
	for _, c := range costs {
		if !isComponentResidualBucket(c.CostBucket) || c.Symbol+"."+c.Exchange != bucket.instrument.SecurityID {
			continue
		}
		if c.Status == "blocked" {
			return core, nil, fmt.Errorf("component residual cost is blocked")
		}
		core.open.Quantity -= c.OpenQuantity
		core.close.Quantity -= c.CloseQuantity
		sold += c.SellQuantity
		item := SecurityContribution{SecurityID: bucket.instrument.SecurityID, Symbol: c.Symbol, Exchange: c.Exchange, Name: bucket.instrument.Name, InstrumentType: bucket.instrument.InstrumentType,
			StrategyType: StrategyETFRedemptionT0, StrategyID: c.CostBucket, OpenQuantity: c.OpenQuantity, CloseQuantity: c.CloseQuantity, SellQuantity: c.SellQuantity, SellAmount: c.SellAmount,
			EffectiveFee: c.SellFee, FeeSource: c.FeeSource, PnLStatus: "estimated", PriceSource: "meridian_daily_bar", EstimationMethod: "component_residual_daily_mark_from_source_close",
			QualityFlags: []string{componentResidualPreviewFlag, "component_marked_basis_not_acquisition_cost"}}
		item.QualityFlags = appendUnique(item.QualityFlags, c.QualityFlags...)
		if c.SellQuantity > 0 {
			item.QualityFlags = appendUnique(item.QualityFlags, "component_sale_fee_aggregate_only")
		}
		item.Turnover = roundMoney(c.SellAmount)
		instrument := bucket.instrument
		validPrice := func(p float64) bool { return p > 0 && !math.IsNaN(p) && !math.IsInf(p, 0) }
		if (c.OpenQuantity > 0 && (!instrument.HasPreClose || !validPrice(instrument.PreClose))) || (c.CloseQuantity > 0 && (!instrument.HasClose || !validPrice(instrument.Close))) {
			return core, nil, fmt.Errorf("missing component residual daily price")
		}
		if c.OpenQuantity > 0 {
			item.OpenPrice = floatPointer(instrument.PreClose)
			item.OpenValue = roundMoney(float64(c.OpenQuantity) * instrument.PreClose)
		}
		if c.CloseQuantity > 0 {
			item.ClosePrice = floatPointer(instrument.Close)
			item.CloseValue = roundMoney(float64(c.CloseQuantity) * instrument.Close)
		}
		item.MarketValue = item.CloseValue
		gross := roundMoney(item.CloseValue + item.SellAmount - item.OpenValue)
		if c.OpenQuantity == 0 {
			if c.SellQuantity != 0 {
				return core, nil, fmt.Errorf("same-day component mark must not fabricate a sale")
			}
			gross = 0 // Source-day exposure is already in the parent's IOPV contribution.
		}
		net := roundMoney(gross - item.EffectiveFee)
		item.GrossContribution = floatPointer(gross)
		item.NetContribution = floatPointer(net)
		item.ContributionBPS = contributionBPSPointer(net, openNAV)
		result = append(result, item)
	}
	if len(result) == 0 {
		return bucket, nil, nil
	}
	if core.open.Quantity < 0 || core.close.Quantity < 0 {
		return core, nil, fmt.Errorf("component lot exceeds broker inventory")
	}
	core.open.MarketValue = 0
	core.close.MarketValue = 0
	if sold > 0 {
		var actual int64
		core.fills = nil
		core.orders = nil
		removed := map[string]bool{}
		for _, f := range bucket.fills {
			if f.TradeSide == trading.TradeSideSell && !links.excludes(f) {
				actual += f.Qty
				removed[f.GatewayOrderID] = true
			} else {
				core.fills = append(core.fills, f)
			}
		}
		if actual != sold {
			return core, nil, fmt.Errorf("component sale attribution changed after cost preview")
		}
		for _, o := range bucket.orders {
			if !removed[o.GatewayOrderID] {
				core.orders = append(core.orders, o)
			}
		}
	}
	return core, result, nil
}

// Separate inventory value from the cash still owed by the fund. This does not
// change the parent's IOPV contribution or assert private settlement finality.
func applyComponentResidualValuation(items []SecurityContribution, groups []t0RedemptionGroup, costs []ledger.PositionCostState, date string, links componentSaleLinks) error {
	values := map[string]float64{}
	quantities := map[string]map[string]int64{}
	for _, c := range costs {
		source, parent := componentResidualParent(c.CostBucket)
		if source != date {
			continue
		}
		if c.Status == "blocked" {
			return fmt.Errorf("component marked inventory is blocked")
		}
		values[parent] += c.CloseMarketValue
		if quantities[parent] == nil {
			quantities[parent] = map[string]int64{}
		}
		quantities[parent][c.Symbol+"."+c.Exchange] += c.CloseQuantity
	}
	for _, g := range groups {
		var value float64
		parents := map[string]bool{}
		for _, r := range g.redemptions {
			if parents[r.GatewayOrderID] {
				continue
			}
			parents[r.GatewayOrderID] = true
			value += values[r.GatewayOrderID]
		}
		if value == 0 {
			continue
		}
		for _, r := range g.redemptions {
			linked := links.groups[r.GatewayOrderID]
			if linked == nil {
				return fmt.Errorf("component parent transfer evidence is missing")
			}
			for key, qty := range linked.expectedBySecurity {
				if qty != linked.linkedBySecurity[key]+quantities[r.GatewayOrderID][key] {
					return fmt.Errorf("component parent has unresolved transfer inventory: %s", key)
				}
			}
		}
		found := false
		for i := range items {
			item := &items[i]
			if item.StrategyID != g.groupID || item.SecurityID != g.securityID {
				continue
			}
			if item.EstimatedExitValue == nil || item.PnLStatus != "estimated" {
				return fmt.Errorf("component inventory requires parent's provisional IOPV valuation")
			}
			v := roundMoney(*item.EstimatedExitValue - item.LinkedComponentSales - item.EstimatedFee + item.ActualFee + item.PriorDayFeeCredit - value)
			item.ETFSettlementEstimate = &v
			item.QualityFlags = appendUnique(item.QualityFlags, componentResidualPreviewFlag, "component_inventory_excluded_from_settlement_estimate", "component_settlement_fee_finality_unverified")
			found = true
		}
		if !found {
			return fmt.Errorf("component marked inventory has no parent contribution")
		}
	}
	return nil
}
