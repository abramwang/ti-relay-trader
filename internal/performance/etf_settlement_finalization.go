package performance

import (
	"math"
	"sort"
	"strings"

	"ti-relay-trader/internal/ledger"
)

func applyETFSettlementFinalizations(
	items []SecurityContribution,
	groups []t0RedemptionGroup,
	componentLinks componentSaleLinks,
	finalizations []ledger.ETFSettlementFinalization,
	openNAV float64,
) ([]SecurityContribution, map[string]bool, []string) {
	coveredOrders := make(map[string]bool)
	flags := make([]string, 0)
	if len(finalizations) == 0 {
		return items, coveredOrders, flags
	}

	itemsBySecurity := make(map[string][]SecurityContribution)
	nonT0 := make([]SecurityContribution, 0, len(items))
	for _, item := range items {
		if item.StrategyType != StrategyETFRedemptionT0 {
			nonT0 = append(nonT0, item)
			continue
		}
		itemsBySecurity[item.SecurityID] = append(itemsBySecurity[item.SecurityID], item)
	}
	groupsBySecurity := make(map[string][]t0RedemptionGroup)
	for _, group := range groups {
		groupsBySecurity[group.securityID] = append(groupsBySecurity[group.securityID], group)
	}

	applied := make(map[string]bool)
	for _, finalization := range finalizations {
		securityID := strings.ToUpper(strings.TrimSpace(finalization.SecurityID))
		groupItems := itemsBySecurity[securityID]
		groupSet := groupsBySecurity[securityID]
		if finalization.Status != "confirmed" || !finalization.SettlementComplete || len(groupItems) == 0 || len(groupSet) == 0 {
			flags = appendUnique(flags, "etf_settlement_finalization_incomplete")
			continue
		}

		var redemptionQuantity int64
		var buyQuantity int64
		buyGross := 0.0
		componentGross := 0.0
		orders := 0
		fills := 0
		qualityFlags := make([]string, 0)
		for _, item := range groupItems {
			redemptionQuantity += item.RedemptionQuantity
			buyQuantity += item.BuyQuantity
			buyGross += item.BuyAmount
			componentGross += item.LinkedComponentSales
			orders += item.Orders
			fills += item.Fills
			for _, flag := range item.QualityFlags {
				if flag == "historical_t0_order_group_inferred" {
					qualityFlags = appendUnique(qualityFlags, flag)
				}
			}
		}
		valid := redemptionQuantity == finalization.RedemptionQuantity && buyQuantity == finalization.RedemptionQuantity &&
			finalization.RedemptionUnit > 0 && finalization.RedemptionQuantity%finalization.RedemptionUnit == 0 &&
			math.Abs(buyGross-finalization.BuyGrossAmount) <= 0.01 &&
			math.Abs(componentGross-finalization.ComponentSaleGrossAmount) <= 0.01
		if !valid {
			flags = appendUnique(flags, "etf_settlement_finalization_trade_mismatch")
			continue
		}

		base := groupItems[0]
		base.StrategyID = "final:" + finalization.SourceTradeDate + ":" + securityID
		base.BuyQuantity = finalization.RedemptionQuantity
		base.RedemptionQuantity = finalization.RedemptionQuantity
		base.SellQuantity = finalization.RedemptionQuantity
		base.RedemptionUnit = finalization.RedemptionUnit
		base.BuyAmount = roundMoney(finalization.BuyGrossAmount)
		base.LinkedComponentSales = roundMoney(finalization.ComponentSaleGrossAmount)
		base.ActualCashComponent = roundMoney(finalization.ActualCashComponent)
		base.ActualCashSubstitution = roundMoney(finalization.ActualCashSubstitution)
		base.SellAmount = roundMoney(finalization.ComponentSaleGrossAmount + finalization.ActualCashComponent + finalization.ActualCashSubstitution)
		base.Turnover = roundMoney(base.BuyAmount + math.Abs(base.SellAmount))
		base.ActualFee = roundMoney(finalization.ActualTotalFee)
		base.EstimatedFee = 0
		base.EffectiveFee = base.ActualFee
		base.FeeSource = "actual_final_settlement:" + finalization.Source

		gross := roundMoney(finalization.GrossContribution)
		net := roundMoney(finalization.NetContribution)
		carry := roundMoney(finalization.SourceCloseSettlementCarry)
		base.GrossContribution = floatPointer(gross)
		base.NetContribution = floatPointer(net)
		base.ContributionBPS = contributionBPSPointer(net, openNAV)
		base.EstimatedExitValue = nil
		base.ETFSettlementEstimate = floatPointer(carry)
		base.ReferenceIOPV = nil
		base.ReferenceTime = nil
		base.PnLStatus = "calculated"
		base.EstimationMethod = "actual_component_sales_plus_final_cash_settlement_minus_actual_fees"
		base.PriceSource = "broker_settlement_and_meridian_pcf"
		base.ETFSettlementStatus = "confirmed"
		base.SettlementPCFTradeDate = finalization.PCFTradeDate
		base.Orders = orders
		base.Fills = fills
		base.QualityFlags = appendUnique(qualityFlags,
			"etf_t0_final_settlement_confirmed",
			"etf_cash_component_reconciled_to_meridian_pcf",
			"broker_settlement_evidence_confirmed",
		)
		nonT0 = append(nonT0, base)
		applied[securityID] = true
		flags = appendUnique(flags, base.QualityFlags...)

		for _, group := range groupSet {
			for _, order := range group.orders {
				coveredOrders[strings.TrimSpace(order.GatewayOrderID)] = true
			}
			for _, redemption := range group.redemptions {
				coveredOrders[strings.TrimSpace(redemption.GatewayOrderID)] = true
			}
			linkedFills, _ := componentLinks.linkedFills(group.redemptions[0].GatewayOrderID)
			for _, fill := range linkedFills {
				coveredOrders[strings.TrimSpace(fill.GatewayOrderID)] = true
			}
		}
	}

	for securityID, groupItems := range itemsBySecurity {
		if !applied[securityID] {
			nonT0 = append(nonT0, groupItems...)
		}
	}
	sort.SliceStable(nonT0, func(i, j int) bool {
		if nonT0[i].SecurityID == nonT0[j].SecurityID {
			return nonT0[i].StrategyID < nonT0[j].StrategyID
		}
		return nonT0[i].SecurityID < nonT0[j].SecurityID
	})
	return nonT0, coveredOrders, flags
}
