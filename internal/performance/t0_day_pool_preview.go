package performance

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const closedDayPoolFlag = "closed_day_t0_cost_pool_preview_only"

// A closed strategy-day has an identifiable total cost without assigning buys
// to individual redemptions. This is not FIFO or a per-redemption profit claim.
func closedDayPool(account, date, security string, groups []t0RedemptionGroup, orders []trading.Order, fills []trading.Fill, open, close []trading.Position) (t0RedemptionGroup, bool) {
	var pool t0RedemptionGroup
	needsReview := false
	for _, g := range groups {
		if g.securityID != security {
			continue
		}
		if g.explicit || !isETFInstrument(g.instrument.InstrumentType) || g.redemptionUnit <= 0 {
			return pool, false
		}
		pool.instrument, pool.redemptionUnit = g.instrument, g.redemptionUnit
		needsReview = needsReview || hasAnyQualityFlag(g.flags, "ambiguous_t0_order_group", "incomplete_t0_order_group")
	}
	if !needsReview {
		return pool, false
	}
	for _, positions := range [][]trading.Position{open, close} {
		for _, p := range positions {
			if contributionSecurityID(p.Symbol, p.Exchange) == security && p.Quantity != 0 {
				return pool, false
			}
		}
	}
	byOrder := map[string]trading.Order{}
	quantities := map[string]int64{}
	var events []trading.Fill
	var strategyID string
	strategySeen := false
	tagged := func(label string) bool {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(label)), "etfarb#") && componentBasketRoot(label) == pool.instrument.Symbol
	}
	for _, o := range orders {
		if contributionSecurityID(o.Symbol, o.Exchange) != security {
			continue
		}
		_, duplicate := byOrder[o.GatewayOrderID]
		if strategySeen && strategyID != o.StrategyID {
			return pool, false
		}
		strategyID, strategySeen = o.StrategyID, true
		if duplicate || o.GatewayOrderID == "" || o.AccountID != account || o.TradeDate != date || !o.IsTerminal || o.T0OrderGroupID != "" ||
			!tagged(o.BasketID) || (o.StrategyType != "" && o.StrategyType != StrategyETFRedemptionT0) ||
			!(o.TradeSide == trading.TradeSideBuy && o.BusinessType == trading.BusinessTypeStock || isETFBusinessRedemption(o.TradeSide, o.BusinessType)) {
			return pool, false
		}
		byOrder[o.GatewayOrderID] = o
	}
	seen := map[string]bool{}
	for _, f := range fills {
		if contributionSecurityID(f.Symbol, f.Exchange) != security {
			continue
		}
		o, found := byOrder[f.GatewayOrderID]
		key, at := contributionFillKey(f), contributionFillTime(f)
		if !found || seen[key] || f.AccountID != account || f.TradeDate != date || f.Qty <= 0 || f.T0OrderGroupID != "" ||
			f.TradeSide != o.TradeSide || f.BusinessType != o.BusinessType || at.IsZero() || at.In(timeutil.Location()).Format("2006-01-02") != date ||
			(f.BasketID != "" && !tagged(f.BasketID)) || (f.StrategyType != "" && f.StrategyType != StrategyETFRedemptionT0) || (f.StrategyID != "" && f.StrategyID != o.StrategyID) {
			return pool, false
		}
		seen[key] = true
		if f.TradeSide == trading.TradeSideBuy {
			amount := contributionFillAmount(f)
			if f.Price <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
				return pool, false
			}
			pool.buyFills = append(pool.buyFills, f)
		} else {
			pool.redemptions = append(pool.redemptions, f)
		}
		if quantities[f.GatewayOrderID] > math.MaxInt64-f.Qty {
			return pool, false
		}
		quantities[f.GatewayOrderID] += f.Qty
		events = append(events, f)
	}
	for id, o := range byOrder {
		q := quantities[id]
		if q != o.CumFilledQty || q > o.OrderQty || q < 0 || (isETFBusinessRedemption(o.TradeSide, o.BusinessType) && q%pool.redemptionUnit != 0) {
			return pool, false
		}
		if q > 0 && o.TradeSide == trading.TradeSideBuy {
			pool.orders = append(pool.orders, o)
		}
	}
	// At equal timestamps process redemptions first: coarse timestamps cannot
	// manufacture proof that a simultaneous buy funded a redemption.
	sort.SliceStable(events, func(i, j int) bool {
		a, b := contributionFillTime(events[i]), contributionFillTime(events[j])
		if a.Equal(b) {
			return isRedemptionFill(events[i]) && !isRedemptionFill(events[j])
		}
		return a.Before(b)
	})
	var quantity int64
	for _, f := range events {
		if f.TradeSide == trading.TradeSideBuy {
			if quantity > math.MaxInt64-f.Qty {
				return pool, false
			}
			quantity += f.Qty
		} else {
			quantity -= f.Qty
			if quantity < 0 {
				return pool, false
			}
		}
	}
	if quantity != 0 || len(pool.buyFills) == 0 || len(pool.redemptions) < 2 {
		return pool, false
	}
	sort.Slice(pool.orders, func(i, j int) bool { return pool.orders[i].GatewayOrderID < pool.orders[j].GatewayOrderID })
	pool.securityID, pool.groupID = security, "day-pool:"+date+":"+security
	pool.flags = []string{closedDayPoolFlag, "per_redemption_buy_allocation_unasserted", "closed_t0_inventory_verified"}
	return pool, true
}

func (s *Service) previewClosedDayPools(ctx context.Context, account, date string, groups []t0RedemptionGroup, orders []trading.Order, fills []trading.Fill, open, close []trading.Position) ([]t0RedemptionGroup, map[string]bool, map[string]bool, error) {
	if !s.componentResidualPreview || s.closedDayT0PoolPreviewDate != date {
		return nil, nil, nil, fmt.Errorf("closed-day pool preview scope mismatch")
	}
	for _, phase := range []string{"open", "close"} {
		observation, err := s.store.GetAssetPositionObservation(ctx, account, date, phase)
		if err != nil || observation.AccountID != account || observation.TradeDate != date || observation.SnapshotType != phase || observation.PositionCapturedAt == nil || observation.PositionCapturedAt.IsZero() {
			return nil, nil, nil, fmt.Errorf("closed-day pool requires complete %s position evidence", phase)
		}
	}
	finalizations, err := s.store.ListETFSettlementFinalizations(ctx, account, date)
	if err != nil {
		return nil, nil, nil, err
	}
	replacements := map[string]t0RedemptionGroup{}
	for _, g := range groups {
		if _, done := replacements[g.securityID]; done {
			continue
		}
		if pool, ok := closedDayPool(account, date, g.securityID, groups, orders, fills, open, close); ok {
			for _, f := range finalizations {
				if f.SecurityID == g.securityID && f.Status == "confirmed" {
					return nil, nil, nil, fmt.Errorf("review cannot override confirmed settlement for %s", g.securityID)
				}
			}
			replacements[g.securityID] = pool
		}
	}
	var result []t0RedemptionGroup
	used := map[string]bool{}
	consumedOrders, consumedFills := map[string]bool{}, map[string]bool{}
	for _, g := range groups {
		if pool, ok := replacements[g.securityID]; ok {
			if used[g.securityID] {
				continue
			}
			used[g.securityID], g = true, pool
		}
		result = append(result, g)
		for _, o := range g.orders {
			consumedOrders[o.GatewayOrderID] = true
		}
		for _, f := range append(append([]trading.Fill(nil), g.buyFills...), g.redemptions...) {
			consumedOrders[f.GatewayOrderID], consumedFills[contributionFillKey(f)] = true, true
		}
	}
	return result, consumedOrders, consumedFills, nil
}

func pooledComponentFills(g t0RedemptionGroup, links componentSaleLinks) ([]trading.Fill, bool) {
	var result []trading.Fill
	seenParents, seenFills := map[string]bool{}, map[string]bool{}
	complete := len(g.redemptions) > 0
	for _, r := range g.redemptions {
		if seenParents[r.GatewayOrderID] {
			continue
		}
		seenParents[r.GatewayOrderID] = true
		fills, ok := links.linkedFills(r.GatewayOrderID)
		complete = complete && ok
		for _, f := range fills {
			key := contributionFillKey(f)
			if !seenFills[key] {
				seenFills[key] = true
				result = append(result, f)
			}
		}
	}
	return result, complete
}
