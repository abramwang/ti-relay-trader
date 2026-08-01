package performance

import (
	"fmt"
	"strings"
)

type t0CostBucketResult struct {
	states        []*costWorkingState
	consumedFills map[string]bool
}

func buildT0CostBuckets(accountID, tradeDate string, groups []t0RedemptionGroup) t0CostBucketResult {
	result := t0CostBucketResult{consumedFills: make(map[string]bool)}
	for index, group := range groups {
		groupID := strings.TrimSpace(group.groupID)
		if groupID == "" {
			groupID = fmt.Sprintf("unidentified-%s-%d", strings.ReplaceAll(group.securityID, ".", "-"), index+1)
		}
		symbol, exchange := splitContributionSecurityID(group.securityID)
		openingSource := "historical_t0_order_group_inferred"
		if group.explicit {
			openingSource = "explicit_t0_order_group"
		}
		state := newCostWorkingState(accountID, tradeDate, symbol, exchange, openingSource)
		state.item.CostBucket = "ETF_T0:" + groupID
		state.item.FeeSource = "included_in_etf_t0_friction"
		state.flags = appendUnique(state.flags, group.flags...)

		for _, fill := range group.buyFills {
			state.item.BuyQuantity += fill.Qty
			state.item.BuyAmount += contributionFillAmount(fill)
			result.consumedFills[contributionFillKey(fill)] = true
		}
		for _, fill := range group.redemptions {
			state.item.SellQuantity += fill.Qty
			result.consumedFills[contributionFillKey(fill)] = true
		}
		state.item.BuyAmount = roundMoney(state.item.BuyAmount)
		state.item.QualityFlags = state.flags

		complete := state.item.BuyQuantity > 0 &&
			state.item.BuyQuantity == state.item.SellQuantity &&
			group.redemptionUnit > 0 &&
			state.item.SellQuantity%group.redemptionUnit == 0 &&
			!hasAnyQualityFlag(state.flags,
				"incomplete_t0_order_group",
				"redemption_quantity_not_pcf_unit_multiple",
				"missing_meridian_etf_redemption_unit",
				"redemption_instrument_type_unconfirmed",
			)
		if complete {
			state.item.CloseQuantity = 0
			state.item.CloseTotalCost = 0
			state.item.AverageCost = 0
			state.flags = appendUnique(state.flags, "etf_t0_cost_separated", "etf_t0_redemption_cost_released")
			if group.explicit {
				state.item.Status = "calculated"
			} else {
				state.item.Status = "estimated"
			}
			if containsStringValue(state.flags, "ambiguous_t0_order_group") {
				state.item.Status = "blocked"
			}
		} else {
			state.quantity = max(0, state.item.BuyQuantity-state.item.SellQuantity)
			state.cost = state.item.BuyAmount
			state.item.CloseQuantity = state.quantity
			state.item.CloseTotalCost = state.cost
			if state.quantity > 0 {
				state.item.AverageCost = roundMoney(state.cost / float64(state.quantity))
			}
			state.item.Status = "blocked"
			state.flags = appendUnique(state.flags, "etf_t0_cost_bucket_incomplete")
		}
		state.item.QualityFlags = state.flags
		result.states = append(result.states, state)
	}
	return result
}

func hasAnyQualityFlag(values []string, targets ...string) bool {
	for _, target := range targets {
		if containsStringValue(values, target) {
			return true
		}
	}
	return false
}

func isT0CostBucket(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "ETF_T0:")
}
