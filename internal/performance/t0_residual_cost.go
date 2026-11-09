package performance

import (
	"math"
	"sort"
	"strings"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

const t0ResidualPrefix = "ETF_T0_RESIDUAL:"

func isT0ResidualBucket(bucket string) bool {
	return strings.HasPrefix(bucket, t0ResidualPrefix)
}

func residualCostKey(securityID, bucket string) string { return securityID + "\x00" + bucket }

func hasOpenT0Residual(states []ledger.PositionCostState) bool {
	for _, state := range states {
		if isT0ResidualBucket(state.CostBucket) && state.CloseQuantity != 0 {
			return true
		}
	}
	return false
}

func residualsForSecurity(states map[string]*costWorkingState, securityID string) []*costWorkingState {
	var result []*costWorkingState
	for _, state := range states {
		if state.item.Symbol+"."+state.item.Exchange == securityID {
			result = append(result, state)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].item.CostBucket < result[j].item.CostBucket })
	return result
}

func openResidualQuantity(states map[string]*costWorkingState, securityID string) int64 {
	var quantity int64
	for _, state := range residualsForSecurity(states, securityID) {
		quantity += state.quantity
	}
	return quantity
}

func blockCostState(state *costWorkingState, flag string) {
	state.item.Status = "blocked"
	state.flags = appendUnique(state.flags, flag)
}

// Only explicit, security-scoped ownership can create or consume residual lots.
// Redemption from a carried lot needs separate allocation evidence and is not inferred here.
func prepareT0Residuals(accountID, tradeDate string, orders []trading.Order, fills []trading.Fill,
	instruments map[string]contributionInstrument, groups []t0RedemptionGroup,
	consumed map[string]bool, states map[string]*costWorkingState,
) map[string]string {
	byOrder := make(map[string]trading.Order, len(orders))
	for _, order := range orders {
		byOrder[order.GatewayOrderID] = order
	}
	redemptions := make(map[string]bool)
	for _, group := range groups {
		if !group.explicit {
			continue
		}
		key := residualCostKey(group.securityID, t0ResidualPrefix+group.groupID)
		redemptions[key] = true
		if state := states[key]; state != nil && state.quantity != 0 {
			blockCostState(state, "t0_residual_redemption_allocation_required")
		}
	}
	routes := make(map[string]string)
	for _, fill := range fills {
		orderGroup := strings.TrimSpace(byOrder[fill.GatewayOrderID].T0OrderGroupID)
		fillGroup := strings.TrimSpace(fill.T0OrderGroupID)
		group := firstNonBlank(fillGroup, orderGroup)
		if group == "" {
			continue
		}
		conflict := orderGroup != "" && fillGroup != "" && orderGroup != fillGroup
		if consumed[contributionFillKey(fill)] && !conflict {
			continue
		}
		securityID := contributionSecurityID(fill.Symbol, fill.Exchange)
		bucket := t0ResidualPrefix + group
		key := residualCostKey(securityID, bucket)
		state := states[key]
		if state == nil {
			state = newCostWorkingState(accountID, tradeDate, fill.Symbol, string(fill.Exchange), "explicit_t0_residual_group")
			state.item.CostBucket = bucket
			state.flags = appendUnique(state.flags, "etf_t0_residual_cost_separated")
			states[key] = state
		}
		if conflict {
			blockCostState(state, "t0_residual_group_conflict")
		}
		if !strings.EqualFold(instruments[securityID].InstrumentType, "etf") {
			blockCostState(state, "t0_residual_instrument_type_unconfirmed")
		}
		if redemptions[key] {
			blockCostState(state, "t0_residual_redemption_allocation_required")
		}
		routes[contributionFillKey(fill)] = key
	}
	return routes
}

func applyBucketedOpening(core *costWorkingState, residuals []*costWorkingState, brokerQuantity int64,
	factor corporateActionFactor, hasFactor, factorsAvailable bool,
) {
	quantity := core.quantity
	for _, state := range residuals {
		quantity += state.quantity
	}
	states := append([]*costWorkingState{core}, residuals...)
	for _, state := range states {
		applyCorporateActionOpening(state, state.quantity, factor, hasFactor, factorsAvailable)
		state.flags = appendUnique(state.flags, "aggregate_cost_bucket_open_reconciliation")
		if quantity != brokerQuantity {
			blockCostState(state, "t0_residual_open_allocation_required")
		}
	}
}

func appendT0ResidualResults(result *CostLedgerResult, states map[string]*costWorkingState, instruments map[string]contributionInstrument) {
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state := states[key]
		instrument := instruments[state.item.Symbol+"."+state.item.Exchange]
		state.item.CloseQuantity = state.quantity
		state.item.CloseTotalCost = roundMoney(math.Max(0, state.cost))
		if state.quantity > 0 {
			state.item.AverageCost = roundMoney(state.item.CloseTotalCost / float64(state.quantity))
		}
		if instrument.HasClose {
			state.item.ClosePrice = instrument.Close
			state.item.CloseMarketValue = roundMoney(instrument.Close * float64(state.quantity))
			state.item.UnrealizedPnL = roundMoney(state.item.CloseMarketValue - state.item.CloseTotalCost)
		} else if state.quantity > 0 {
			blockCostState(state, "missing_meridian_close")
		}
		if containsStringValue(state.flags, "missing_fee_rule") && state.item.Status == "calculated" {
			state.item.Status = "estimated"
		}
		state.item.BuyAmount = roundMoney(state.item.BuyAmount)
		state.item.BuyFee = roundMoney(state.item.BuyFee)
		state.item.SellAmount = roundMoney(state.item.SellAmount)
		state.item.SellFee = roundMoney(state.item.SellFee)
		state.item.RealizedPnL = roundMoney(state.item.RealizedPnL)
		state.flags = appendUnique(state.flags, "aggregate_quantity_reconciled_on_core")
		state.item.QualityFlags = state.flags
		state.item.CalculatedAt = result.CalculatedAt
		result.Positions = append(result.Positions, state.item)
	}
}
