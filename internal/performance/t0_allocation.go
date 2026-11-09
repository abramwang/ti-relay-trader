package performance

import (
	"context"
	"fmt"
	"strings"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

type t0AllocationReader interface {
	ListT0AllocationHistory(context.Context, string, string) ([]ledger.T0Allocation, error)
}

func (service *Service) loadT0Allocations(ctx context.Context, accountID, date string) ([]ledger.T0Allocation, error) {
	reader, ok := service.store.(t0AllocationReader)
	if !ok {
		return nil, nil
	}
	history, err := reader.ListT0AllocationHistory(ctx, accountID, date)
	if err != nil {
		return nil, fmt.Errorf("read T0 attribution audit: %w", err)
	}
	for _, item := range history {
		if item.AccountID != accountID || item.TradeDate != date {
			return nil, fmt.Errorf("T0 allocation audit scope mismatch")
		}
	}
	return ledger.ResolveT0Allocations(history)
}

// Apply only to copies. A revision cannot silently replace an OC-provided group.
func applyT0Allocations(accountID, date string, orders []trading.Order, fills []trading.Fill, links []ledger.T0Allocation) ([]trading.Order, []trading.Fill, []string, error) {
	orders = append([]trading.Order(nil), orders...)
	fills = append([]trading.Fill(nil), fills...)
	var ids []string
	roles := make(map[string]string)
	for _, link := range links {
		if link.AccountID != accountID || link.TradeDate != date || link.Status != "active" {
			return nil, nil, nil, fmt.Errorf("T0 allocation outside calculation scope")
		}
		key := residualCostKey(link.SecurityID, link.GroupID)
		if role := roles[key]; role != "" && role != link.Role {
			return nil, nil, nil, fmt.Errorf("T0 allocation group has conflicting roles")
		}
		roles[key] = link.Role
		found := false
		for i := range orders {
			order := &orders[i]
			if order.GatewayOrderID != link.GatewayOrderID {
				continue
			}
			if found || order.AccountID != accountID || order.TradeDate != date || contributionSecurityID(order.Symbol, order.Exchange) != link.SecurityID ||
				string(order.TradeSide) != link.TradeSide || order.CumFilledQty != link.Quantity || !order.IsTerminal ||
				(order.T0OrderGroupID != "" && order.T0OrderGroupID != link.GroupID) {
				return nil, nil, nil, fmt.Errorf("T0 allocation %s no longer matches terminal order evidence", link.LinkID)
			}
			order.T0OrderGroupID = link.GroupID
			found = true
		}
		if !found {
			return nil, nil, nil, fmt.Errorf("T0 allocation %s order is missing", link.LinkID)
		}
		var quantity int64
		for i := range fills {
			fill := &fills[i]
			if fill.GatewayOrderID != link.GatewayOrderID {
				continue
			}
			if fill.AccountID != accountID || fill.TradeDate != date || contributionSecurityID(fill.Symbol, fill.Exchange) != link.SecurityID ||
				string(fill.TradeSide) != link.TradeSide || fill.Qty <= 0 || (fill.T0OrderGroupID != "" && fill.T0OrderGroupID != link.GroupID) {
				return nil, nil, nil, fmt.Errorf("T0 allocation %s conflicts with fill evidence", link.LinkID)
			}
			quantity += fill.Qty
			fill.T0OrderGroupID = link.GroupID
		}
		// Redemptions may arrive through the transfer ledger, validated by buildT0Groups.
		if quantity != link.Quantity && !(link.TradeSide == "R" && quantity == 0) {
			return nil, nil, nil, fmt.Errorf("T0 allocation %s executed quantity mismatch", link.LinkID)
		}
		ids = append(ids, link.LinkID)
	}
	for key, role := range roles {
		var bought, redeemed int64
		for _, order := range orders {
			if residualCostKey(contributionSecurityID(order.Symbol, order.Exchange), order.T0OrderGroupID) != key {
				continue
			}
			if order.TradeSide == trading.TradeSideBuy {
				bought += order.CumFilledQty
			}
			if order.TradeSide == trading.TradeSideRedemption {
				redeemed += order.CumFilledQty
			}
		}
		if (role == "residual" && redeemed != 0) || (role == "redemption" && (redeemed == 0 || bought != redeemed)) {
			return nil, nil, nil, fmt.Errorf("T0 allocation group role/quantity is not reconciled")
		}
	}
	return orders, fills, ids, nil
}

func splitResidualContributionBucket(bucket contributionBucket, costs []ledger.PositionCostState) (contributionBucket, []contributionBucket, error) {
	core := bucket
	core.orders = nil
	core.fills = nil
	var residuals []contributionBucket
	byGroup := make(map[string]int)
	for _, cost := range costs {
		if !isT0ResidualBucket(cost.CostBucket) || cost.Symbol+"."+cost.Exchange != bucket.instrument.SecurityID {
			continue
		}
		group := strings.TrimPrefix(cost.CostBucket, t0ResidualPrefix)
		part := bucket
		part.residualGroup = group
		part.orders = nil
		part.fills = nil
		part.open.Quantity = cost.OpenQuantity
		part.close.Quantity = cost.CloseQuantity
		part.open.MarketValue = 0
		part.close.MarketValue = 0
		part.hasOpen = bucket.hasOpen || cost.OpenQuantity == 0
		part.hasClose = bucket.hasClose || cost.CloseQuantity == 0
		core.open.Quantity -= cost.OpenQuantity
		core.close.Quantity -= cost.CloseQuantity
		byGroup[group] = len(residuals)
		residuals = append(residuals, part)
	}
	if len(residuals) == 0 {
		return bucket, nil, nil
	}
	if core.open.Quantity < 0 || core.close.Quantity < 0 {
		return core, nil, fmt.Errorf("residual quantity exceeds account position")
	}
	core.open.MarketValue = 0
	core.close.MarketValue = 0
	orderGroups := make(map[string]string, len(bucket.orders))
	for _, order := range bucket.orders {
		orderGroups[order.GatewayOrderID] = order.T0OrderGroupID
		if i, ok := byGroup[order.T0OrderGroupID]; ok {
			residuals[i].orders = append(residuals[i].orders, order)
		} else {
			core.orders = append(core.orders, order)
		}
	}
	for _, fill := range bucket.fills {
		group := fill.T0OrderGroupID
		if group == "" {
			group = orderGroups[fill.GatewayOrderID]
		}
		if i, ok := byGroup[group]; ok {
			residuals[i].fills = append(residuals[i].fills, fill)
		} else {
			core.fills = append(core.fills, fill)
		}
	}
	return core, residuals, nil
}
