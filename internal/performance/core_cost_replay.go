package performance

import (
	"fmt"
	"math"
	"sort"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

// ReplayCoreCostDay checks acquisition-cost facts only. The caller must prove
// the initial zero holding and complete daily inputs. No broker avg_cost, price
// estimates, corporate actions or research opening costs are accepted here.
func ReplayCoreCostDay(account, date, symbol, exchange string, openQuantity int64, openCost float64, fills []trading.Fill, fees []ledger.OrderFeeRecord) (ledger.PositionCostState, error) {
	bad := func(reason string) (ledger.PositionCostState, error) {
		return ledger.PositionCostState{}, fmt.Errorf("core replay %s %s: %s", date, symbol, reason)
	}
	if _, _, err := parseTradeDate(date); err != nil || account == "" || symbol == "" || (exchange != "SH" && exchange != "SZ") || openQuantity < 0 || !finiteCoreMoney(openCost) || (openQuantity == 0 && openCost != 0) {
		return bad("invalid scope or opening")
	}
	state := newCostWorkingState(account, date, symbol, exchange, "verified_zero_origin_replay")
	state.quantity, state.cost = openQuantity, openCost
	state.item.OpenQuantity, state.item.OpenTotalCost = openQuantity, openCost
	authoritative := authoritativeOrderFees(fees)
	consumed := map[string]contributionOrderFeeState{}
	fills = append([]trading.Fill(nil), fills...)
	sort.SliceStable(fills, func(i, j int) bool { return fills[i].MatchedAt.Before(fills[j].MatchedAt) })
	seen := map[string]bool{}
	for i, fill := range fills {
		key := contributionFillKey(fill)
		if fill.AccountID != account || fill.TradeDate != date || fill.Symbol != symbol || string(fill.Exchange) != exchange || fill.FillID == "" || fill.GatewayOrderID == "" || seen[key] || fill.MatchedAt.IsZero() || fill.Qty <= 0 || fill.Qty > math.MaxInt64-state.quantity || !finiteCoreMoney(fill.Price) || fill.Price <= 0 || string(fill.BusinessType) != "S" {
			return bad("invalid, duplicate or non-ordinary fill")
		}
		if fill.TradeSide != trading.TradeSideBuy && fill.TradeSide != trading.TradeSideSell {
			return bad("subscription/redemption needs a separate review")
		}
		if i > 0 && fill.MatchedAt.Equal(fills[i-1].MatchedAt) && fill.TradeSide != fills[i-1].TradeSide {
			return bad("ambiguous buy/sell ordering")
		}
		seen[key] = true
		record, ok := authoritative[fill.GatewayOrderID]
		if !ok || record.AccountID != account || record.TradeDate != date || record.Symbol != symbol || record.Exchange != exchange || record.TradeSide != string(fill.TradeSide) || record.Currency != "CNY" || !finiteCoreMoney(record.TotalFee) {
			return bad("complete associated order fee unavailable or conflicting")
		}
		amount := contributionFillAmount(fill)
		if !finiteCoreMoney(amount) || amount <= 0 {
			return bad("invalid fill amount")
		}
		fee := contributionFeeForFill(fill, contributionInstrument{}, nil, authoritative, consumed)
		state.item.FeeSource = mergeContributionFeeSource(state.item.FeeSource, fee.source)
		applyCostFill(state, fill, amount, fee.effective)
		if state.item.Status == "blocked" || !finiteCoreMoney(state.cost) {
			return bad("sale exceeds verified holdings or cost overflow")
		}
	}
	state.item.BuyAmount, state.item.BuyFee = roundMoney(state.item.BuyAmount), roundMoney(state.item.BuyFee)
	state.item.SellAmount, state.item.SellFee = roundMoney(state.item.SellAmount), roundMoney(state.item.SellFee)
	state.item.RealizedPnL = roundMoney(state.item.RealizedPnL)
	state.item.CloseQuantity, state.item.CloseTotalCost = state.quantity, roundMoney(state.cost)
	if state.quantity > 0 {
		state.item.AverageCost = roundMoney(state.item.CloseTotalCost / float64(state.quantity))
	}
	return state.item, nil
}

func finiteCoreMoney(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n < 1e12
}
