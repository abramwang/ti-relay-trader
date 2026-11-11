package performance

import (
	"context"
	"fmt"
	"math"
	"strings"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

const overnightT0ChainFlag = "confirmed_overnight_t0_research_only"

// OvernightT0Allocation is an internal, non-publishing research instruction.
// Source ownership must also exist in the independently audited T0 allocation store.
type OvernightT0Allocation struct {
	AccountID         string `json:"account_id"`
	SourceDate        string `json:"source_date"`
	RedemptionDate    string `json:"redemption_date"`
	SecurityID        string `json:"security_id"`
	GroupID           string `json:"group_id"`
	SourceBuyOrderID  string `json:"source_buy_order_id"`
	RedemptionOrderID string `json:"redemption_order_id"`
	Quantity          int64  `json:"quantity"`
	EvidenceRef       string `json:"evidence_ref"`
}

type overnightT0Plan struct {
	link   OvernightT0Allocation
	source ledger.PositionCostState
	quote  OvernightT0Preview
}

type OvernightT0Release struct {
	Allocation            OvernightT0Allocation `json:"allocation"`
	SourceAcquisitionCost float64               `json:"source_acquisition_cost"`
	DailyBridge           OvernightT0Preview    `json:"daily_bridge"`
}

func validateOvernightT0Allocations(links []OvernightT0Allocation, research bool) error {
	seenSources, seenRedemptions := map[string]bool{}, map[string]bool{}
	for _, l := range links {
		source, _, e1 := parseTradeDate(l.SourceDate)
		target, _, e2 := parseTradeDate(l.RedemptionDate)
		if !research || e1 != nil || e2 != nil || source != l.SourceDate || target != l.RedemptionDate || source >= target || l.AccountID == "" || l.SecurityID == "" || l.GroupID == "" || l.SourceBuyOrderID == "" || l.RedemptionOrderID == "" || l.Quantity <= 0 || l.EvidenceRef == "" {
			return fmt.Errorf("invalid non-publishing overnight allocation")
		}
		sourceKey := strings.Join([]string{l.AccountID, l.SourceDate, l.SecurityID, l.GroupID}, "\x00")
		redKey := strings.Join([]string{l.AccountID, l.RedemptionDate, l.RedemptionOrderID}, "\x00")
		if seenSources[sourceKey] || seenRedemptions[redKey] {
			return fmt.Errorf("overnight source or redemption allocated more than once")
		}
		seenSources[sourceKey], seenRedemptions[redKey] = true, true
	}
	return nil
}

// Keep normal grouping untouched. A reviewed overnight lot may only replace an
// incomplete group with no selected same-day buys, and requires a full redemption.
func (service *Service) attachOvernightT0(ctx context.Context, account, date string, groups []t0RedemptionGroup, orders []trading.Order, fills []trading.Fill, transfers []trading.ComponentTransfer) ([]t0RedemptionGroup, error) {
	if len(service.overnightT0Allocations) == 0 {
		return groups, nil
	}
	result := append([]t0RedemptionGroup(nil), groups...)
	for _, link := range service.overnightT0Allocations {
		if link.AccountID != account || link.RedemptionDate != date {
			continue
		}
		if !service.componentResidualPreview {
			return nil, fmt.Errorf("overnight allocation requires research mode")
		}
		previous, err := service.store.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: account, BeforeDate: date})
		if err != nil {
			return nil, err
		}
		var source ledger.PositionCostState
		matches := 0
		for _, p := range previous {
			if p.AccountID == account && p.Symbol+"."+p.Exchange == link.SecurityID && p.CostBucket == t0ResidualPrefix+link.GroupID {
				source = p
				matches++
			}
		}
		if matches != 1 || source.TradeDate != link.SourceDate || source.CloseQuantity != link.Quantity {
			return nil, fmt.Errorf("overnight source cost missing, stale or ambiguous")
		}
		audit, err := service.loadT0Allocations(ctx, account, link.SourceDate)
		if err != nil {
			return nil, err
		}
		found := false
		for _, a := range audit {
			if a.GroupID != link.GroupID || a.SecurityID != link.SecurityID {
				continue
			}
			if found || a.GatewayOrderID != link.SourceBuyOrderID || a.Quantity != link.Quantity || a.TradeSide != "B" || a.Role != "residual" || a.EvidenceType != "user_confirmation" || a.EvidenceRef != link.EvidenceRef {
				return nil, fmt.Errorf("overnight source ownership audit conflict")
			}
			found = true
		}
		if !found {
			return nil, fmt.Errorf("overnight source ownership audit missing")
		}
		var order trading.Order
		matches = 0
		for _, o := range orders {
			if o.GatewayOrderID == link.RedemptionOrderID {
				order = o
				matches++
			}
			if o.T0OrderGroupID == link.GroupID {
				return nil, fmt.Errorf("overnight group reused by current-day order")
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("overnight redemption order missing or duplicated")
		}
		var parent trading.ComponentTransfer
		matches = 0
		for _, tr := range transfers {
			if tr.GatewayOrderID == link.RedemptionOrderID && contributionSecurityID(tr.ComponentSymbol, tr.ComponentExchange) == link.SecurityID {
				parent = tr
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("overnight full parent transfer missing or duplicated")
		}
		quote, err := service.PreviewOvernightT0(ctx, source, order, parent)
		if err != nil {
			return nil, err
		}
		matches = 0
		for i, g := range result {
			if len(g.redemptions) == 0 || g.redemptions[0].GatewayOrderID != link.RedemptionOrderID {
				continue
			}
			if len(g.redemptions) != 1 || g.redemptions[0].Qty != link.Quantity || !contributionFillTime(g.redemptions[0]).Equal(parent.MatchedAt) || len(g.buyFills) != 0 || len(g.orders) != 0 || g.explicit || g.securityID != link.SecurityID || g.redemptionUnit <= 0 || link.Quantity%g.redemptionUnit != 0 || !isETFInstrument(g.instrument.InstrumentType) {
				return nil, fmt.Errorf("overnight redemption conflicts with selected buys, PCF or parent evidence")
			}
			for _, f := range fills {
				if f.T0OrderGroupID == link.GroupID {
					return nil, fmt.Errorf("overnight group reused by current-day execution")
				}
			}
			g.groupID = link.GroupID
			g.flags = []string{overnightT0ChainFlag, "source_day_buy_retained", "prior_day_fee_credit_not_cash"}
			g.overnight = &overnightT0Plan{link: link, source: source, quote: quote}
			result[i] = g
			matches++
		}
		if matches != 1 {
			return nil, fmt.Errorf("overnight redemption group missing or duplicated")
		}
	}
	return result, nil
}

// Called after aggregate opening/corporate-action reconciliation, before today's
// fills. The output is an estimated cost release, never a synthetic broker fill.
func releaseOvernightT0Costs(groups []t0RedemptionGroup, residuals map[string]*costWorkingState) error {
	for _, g := range groups {
		p := g.overnight
		if p == nil {
			continue
		}
		state := residuals[residualCostKey(g.securityID, p.source.CostBucket)]
		if state == nil || state.item.Status == "blocked" || state.quantity != p.link.Quantity || state.item.OpenQuantity != p.link.Quantity || math.Abs(state.cost-p.source.CloseTotalCost) > 0.000001 || state.item.BuyQuantity != 0 || state.item.SellQuantity != 0 {
			return fmt.Errorf("overnight opening lot changed or was already consumed")
		}
		state.item.SellQuantity = p.link.Quantity
		state.item.SellAmount = p.quote.EstimatedExitValue
		state.item.SellFee = roundMoney(p.quote.EstimatedAllInFee - p.quote.PreviouslyExpensedFeeCredit)
		state.item.RealizedPnL = roundMoney(state.item.SellAmount - state.cost - state.item.SellFee)
		state.item.Status = "estimated"
		state.item.FeeSource = "all_in_t0_friction_less_source_day_expense"
		state.flags = appendUnique(state.flags, overnightT0ChainFlag, "etf_t0_redemption_cost_released", "iopv_release_not_cash")
		state.quantity, state.cost = 0, 0
	}
	return nil
}

func overnightResidualConsumed(groups []t0RedemptionGroup, security, group string) bool {
	for _, g := range groups {
		if g.overnight != nil && g.securityID == security && g.groupID == group {
			return true
		}
	}
	return false
}
