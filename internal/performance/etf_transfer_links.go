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
	byFill map[string]componentSaleLink
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
		byFill: make(map[string]componentSaleLink),
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
			group = &componentTransferGroup{
				redemptionGatewayOrderID: transfer.GatewayOrderID,
				basketRoot:               firstNonBlank(componentBasketRoot(transfer.BasketID), componentBasketRoot(order.BasketID), strings.TrimSpace(order.Symbol)),
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
		if basketRoot == "" {
			continue
		}
		securityID := contributionSecurityID(fill.Symbol, fill.Exchange)
		fillTime := contributionFillTime(fill)
		var selected *componentTransferGroup
		for _, group := range result.groups {
			if group.basketRoot != basketRoot || group.expectedBySecurity[securityID]-group.linkedBySecurity[securityID] < fill.Qty {
				continue
			}
			if !fillTime.IsZero() && !group.matchedAt.IsZero() && fillTime.Before(group.matchedAt) {
				continue
			}
			if selected == nil || group.matchedAt.After(selected.matchedAt) {
				selected = group
			}
		}
		if selected == nil {
			continue
		}
		selected.linkedBySecurity[securityID] += fill.Qty
		selected.linkedFills = append(selected.linkedFills, fill)
		result.byFill[contributionFillKey(fill)] = componentSaleLink{
			redemptionGatewayOrderID: selected.redemptionGatewayOrderID,
			basketRoot:               selected.basketRoot,
		}
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
	result := componentSaleBucketLink{allFillsLinked: len(fills) > 0}
	for _, fill := range fills {
		link, ok := links.byFill[contributionFillKey(fill)]
		if !ok {
			result.allFillsLinked = false
			continue
		}
		result.linkedQuantity += fill.Qty
		if result.redemptionGatewayOrderID == "" {
			result.redemptionGatewayOrderID = link.redemptionGatewayOrderID
			result.basketRoot = link.basketRoot
		} else if result.redemptionGatewayOrderID != link.redemptionGatewayOrderID {
			result.allFillsLinked = false
		}
	}
	result.groupComplete = componentTransferGroupComplete(links.groups[result.redemptionGatewayOrderID])
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
	_, ok := links.byFill[contributionFillKey(fill)]
	return ok
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
