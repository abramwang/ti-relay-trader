package performance

import (
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/trading"
)

type componentSaleLink struct {
	redemptionGatewayOrderID string
	basketRoot               string
	quantity                 int64
}

type componentTransferGroup struct {
	redemptionGatewayOrderID string
	basketRoot               string
	matchedAt                time.Time
	expectedBySecurity       map[string]int64
	linkedBySecurity         map[string]int64
	linkedFills              []trading.Fill
}

type componentSaleLinks struct {
	byFill map[string][]componentSaleLink
	groups map[string]*componentTransferGroup
	flags  []string
}

type componentSaleBucketLink struct {
	linkedQuantity           int64
	allFillsLinked           bool
	redemptionGatewayOrderID string
	basketRoot               string
	groupComplete            bool
}

func buildComponentSaleLinks(orders []trading.Order, fills []trading.Fill, transfers []trading.ComponentTransfer) componentSaleLinks {
	result := componentSaleLinks{
		byFill: make(map[string][]componentSaleLink),
		groups: make(map[string]*componentTransferGroup),
	}
	ordersByID := make(map[string]trading.Order, len(orders))
	for _, order := range orders {
		ordersByID[order.GatewayOrderID] = order
	}

	seenTransfers := make(map[string]bool)
	for _, transfer := range transfers {
		order, ok := ordersByID[transfer.GatewayOrderID]
		if !ok || !isETFBusinessRedemption(order.TradeSide, order.BusinessType) {
			continue
		}
		transferKey := strings.Join([]string{
			transfer.AccountID,
			transfer.GatewayOrderID,
			transfer.FillID,
			transfer.OrderStreamID,
			contributionSecurityID(transfer.Symbol, transfer.Exchange),
		}, "\x00")
		if seenTransfers[transferKey] {
			continue
		}
		seenTransfers[transferKey] = true

		group := result.groups[transfer.GatewayOrderID]
		if group == nil {
			basketRoot := firstNonBlank(componentBasketRoot(transfer.BasketID), componentBasketRoot(order.BasketID))
			group = &componentTransferGroup{
				redemptionGatewayOrderID: transfer.GatewayOrderID,
				basketRoot:               firstNonBlank(basketRoot, strings.TrimSpace(order.Symbol)),
				expectedBySecurity:       make(map[string]int64),
				linkedBySecurity:         make(map[string]int64),
			}
			result.groups[transfer.GatewayOrderID] = group
		}
		if group.matchedAt.IsZero() || (!transfer.MatchedAt.IsZero() && transfer.MatchedAt.Before(group.matchedAt)) {
			group.matchedAt = transfer.MatchedAt
		}

		componentSymbol := firstNonBlank(transfer.ComponentSymbol, transfer.Symbol)
		componentExchange := transfer.ComponentExchange
		if componentExchange == "" {
			componentExchange = transfer.Exchange
		}
		componentSecurityID := contributionSecurityID(componentSymbol, componentExchange)
		parentSecurityID := contributionSecurityID(order.Symbol, order.Exchange)
		quantity := transfer.ComponentQty
		if quantity <= 0 {
			quantity = transfer.Qty
		}
		if componentSecurityID == parentSecurityID || transfer.CashSubstitution || quantity <= 0 {
			continue
		}
		group.expectedBySecurity[componentSecurityID] += quantity
	}

	sortedFills := append([]trading.Fill(nil), fills...)
	sort.SliceStable(sortedFills, func(i, j int) bool {
		return contributionFillTime(sortedFills[i]).Before(contributionFillTime(sortedFills[j]))
	})
	for _, fill := range sortedFills {
		if fill.TradeSide != trading.TradeSideSell || fill.Qty <= 0 {
			continue
		}
		basketRoot := componentBasketRoot(firstNonBlank(fill.BasketID, contributionString(fill.AdapterContext["basket_id"])))
		securityID := contributionSecurityID(fill.Symbol, fill.Exchange)
		fillTime := contributionFillTime(fill)
		eligible := make([]*componentTransferGroup, 0)
		for _, group := range result.groups {
			basketMatches := basketRoot != "" && group.basketRoot == basketRoot
			historicalMatch := basketRoot == ""
			if (!basketMatches && !historicalMatch) || group.expectedBySecurity[securityID]-group.linkedBySecurity[securityID] <= 0 {
				continue
			}
			if !fillTime.IsZero() && !group.matchedAt.IsZero() && fillTime.Before(group.matchedAt) {
				continue
			}
			eligible = append(eligible, group)
		}
		sort.SliceStable(eligible, func(i, j int) bool {
			if eligible[i].matchedAt.Equal(eligible[j].matchedAt) {
				return eligible[i].redemptionGatewayOrderID < eligible[j].redemptionGatewayOrderID
			}
			return eligible[i].matchedAt.After(eligible[j].matchedAt)
		})
		remaining := fill.Qty
		allocations := make([]componentSaleLink, 0, len(eligible))
		for _, group := range eligible {
			available := group.expectedBySecurity[securityID] - group.linkedBySecurity[securityID]
			quantity := available
			if quantity > remaining {
				quantity = remaining
			}
			if quantity <= 0 {
				continue
			}
			allocations = append(allocations, componentSaleLink{
				redemptionGatewayOrderID: group.redemptionGatewayOrderID,
				basketRoot:               group.basketRoot,
				quantity:                 quantity,
			})
			remaining -= quantity
			if remaining == 0 {
				break
			}
		}
		if remaining != 0 {
			continue
		}
		if basketRoot == "" {
			result.flags = appendUnique(result.flags, "historical_component_sale_link_inferred")
		}
		for _, allocation := range allocations {
			group := result.groups[allocation.redemptionGatewayOrderID]
			group.linkedBySecurity[securityID] += allocation.quantity
			allocatedFill := fill
			allocatedFill.Qty = allocation.quantity
			group.linkedFills = append(group.linkedFills, allocatedFill)
		}
		result.byFill[contributionFillKey(fill)] = allocations
	}

	for _, group := range result.groups {
		if componentTransferGroupComplete(group) {
			continue
		}
		result.flags = appendUnique(result.flags, "component_transfer_sell_quantity_mismatch")
	}
	return result
}

func componentTransferGroupComplete(group *componentTransferGroup) bool {
	if group == nil || len(group.expectedBySecurity) == 0 {
		return false
	}
	for securityID, expected := range group.expectedBySecurity {
		if expected <= 0 || group.linkedBySecurity[securityID] != expected {
			return false
		}
	}
	return true
}

func (links componentSaleLinks) bucketLink(fills []trading.Fill) componentSaleBucketLink {
	result := componentSaleBucketLink{allFillsLinked: len(fills) > 0, groupComplete: len(fills) > 0}
	seenGroups := make(map[string]bool)
	for _, fill := range fills {
		fillLinks := links.byFill[contributionFillKey(fill)]
		if len(fillLinks) == 0 {
			result.allFillsLinked = false
			result.groupComplete = false
			continue
		}
		result.linkedQuantity += fill.Qty
		for _, link := range fillLinks {
			seenGroups[link.redemptionGatewayOrderID] = true
			if result.redemptionGatewayOrderID == "" {
				result.redemptionGatewayOrderID = link.redemptionGatewayOrderID
				result.basketRoot = link.basketRoot
			}
		}
	}
	if len(seenGroups) > 1 {
		result.redemptionGatewayOrderID = ""
	}
	for groupID := range seenGroups {
		if !componentTransferGroupComplete(links.groups[groupID]) {
			result.groupComplete = false
		}
	}
	return result
}

func (links componentSaleLinks) linkedFills(redemptionGatewayOrderID string) ([]trading.Fill, bool) {
	group := links.groups[strings.TrimSpace(redemptionGatewayOrderID)]
	if group == nil {
		return nil, false
	}
	return append([]trading.Fill(nil), group.linkedFills...), componentTransferGroupComplete(group)
}

func (links componentSaleLinks) excludes(fill trading.Fill) bool {
	return len(links.byFill[contributionFillKey(fill)]) > 0
}

func componentBasketRoot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "#")
	if len(parts) >= 2 {
		value = parts[1]
	}
	if index := strings.Index(value, "."); index >= 0 {
		value = value[:index]
	}
	return strings.ToUpper(strings.TrimSpace(value))
}
