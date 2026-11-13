package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type fuyingOvernightSourceEvidence struct {
	Order        trading.Order                     `json:"order"`
	Fills        []trading.Fill                    `json:"fills"`
	CoreSales    []trading.Order                   `json:"source_core_sale_orders"`
	CoreFills    []trading.Fill                    `json:"source_core_sale_fills"`
	Open         []trading.Position                `json:"open_positions"`
	Close        []trading.Position                `json:"close_positions"`
	Observations []ledger.AssetPositionObservation `json:"observations"`
}

func validateFuyingOvernightSource(e fuyingOvernightSourceEvidence) error {
	fail := func() error { return fmt.Errorf("overnight source ownership evidence changed") }
	o := e.Order
	if o.AccountID != fuying13Account || o.GatewayOrderID != fuyingOvernightBuy || o.TradeDate != "2026-11-03" || o.Symbol != "159915" || o.Exchange != "SZ" || o.BusinessType != "S" || o.TradeSide != "B" || !o.IsTerminal || o.OrderQty != 1000000 || o.CumFilledQty != 1000000 || o.BasketID != "etfarb#159915.SZSE#105630#b" || o.T0OrderGroupID != "" || o.StrategyID != "" || o.StrategyType != "" || len(e.Fills) == 0 || len(e.Fills) >= 500 || len(e.Open) > 1 || len(e.Close) != 1 || len(e.Observations) != 2 {
		return fmt.Errorf("overnight source header changed: account=%s order=%s date=%s basket=%q group=%q strategy=%q/%q qty=%d/%d terminal=%t fills=%d positions=%d/%d observations=%d", o.AccountID, o.GatewayOrderID, o.TradeDate, o.BasketID, o.T0OrderGroupID, o.StrategyID, o.StrategyType, o.CumFilledQty, o.OrderQty, o.IsTerminal, len(e.Fills), len(e.Open), len(e.Close), len(e.Observations))
	}
	var qty int64
	var amount float64
	var firstBuy time.Time
	seen := map[string]bool{}
	for _, f := range e.Fills {
		if f.FillID == "" || seen[f.FillID] || f.AccountID != o.AccountID || f.GatewayOrderID != o.GatewayOrderID || f.TradeDate != o.TradeDate || f.Symbol != o.Symbol || f.Exchange != o.Exchange || f.TradeSide != "B" || f.BusinessType != "S" || f.Qty <= 0 || f.Qty > 1000000-qty || f.Price <= 0 || math.IsNaN(f.Price) || math.IsInf(f.Price, 0) || f.MatchedAt.IsZero() || f.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != o.TradeDate || f.T0OrderGroupID != "" || f.StrategyID != "" || f.StrategyType != "" {
			return fmt.Errorf("overnight source fill changed: id=%s date=%s matched=%s qty=%d running=%d side=%s business=%s group=%q strategy=%q/%q", f.FillID, f.TradeDate, f.MatchedAt, f.Qty, qty, f.TradeSide, f.BusinessType, f.T0OrderGroupID, f.StrategyID, f.StrategyType)
		}
		seen[f.FillID] = true
		qty += f.Qty
		amount += f.Price * float64(f.Qty)
		if firstBuy.IsZero() || f.MatchedAt.Before(firstBuy) {
			firstBuy = f.MatchedAt
		}
	}
	if qty != 1000000 || math.Abs(amount-2452000) > .0001 {
		return fmt.Errorf("overnight source executed totals changed: qty=%d amount=%.6f", qty, amount)
	}
	for _, p := range append(append([]trading.Position(nil), e.Open...), e.Close...) {
		if p.AccountID != o.AccountID || p.TradeDate != o.TradeDate || p.Symbol != o.Symbol || p.Exchange != o.Exchange {
			return fail()
		}
	}
	if len(e.Open) != 1 || e.Open[0].Quantity != 7300 || e.Close[0].Quantity != 1000000 {
		return fmt.Errorf("overnight source position boundary changed")
	}
	if len(e.CoreSales) == 0 || len(e.CoreSales) >= 499 || len(e.CoreFills) == 0 || len(e.CoreFills)+len(e.Fills) >= 500 {
		return fail()
	}
	sales := map[string]trading.Order{}
	for _, sale := range e.CoreSales {
		if _, duplicate := sales[sale.GatewayOrderID]; duplicate || sale.GatewayOrderID == "" || sale.AccountID != o.AccountID || sale.TradeDate != o.TradeDate || sale.Symbol != o.Symbol || sale.Exchange != o.Exchange || sale.TradeSide != "S" || sale.BusinessType != "S" || !sale.IsTerminal || sale.CumFilledQty < 0 || sale.CumFilledQty > 7300 || sale.OrderQty < sale.CumFilledQty || sale.T0OrderGroupID != "" {
			return fail()
		}
		sales[sale.GatewayOrderID] = sale
	}
	byOrder := map[string]int64{}
	var sold int64
	for _, f := range e.CoreFills {
		_, known := sales[f.GatewayOrderID]
		if !known || f.FillID == "" || seen[f.FillID] || f.AccountID != o.AccountID || f.TradeDate != o.TradeDate || f.Symbol != o.Symbol || f.Exchange != o.Exchange || f.TradeSide != "S" || f.BusinessType != "S" || f.Qty <= 0 || f.Qty > 7300-sold || f.Price <= 0 || math.IsNaN(f.Price) || math.IsInf(f.Price, 0) || f.MatchedAt.IsZero() || f.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != o.TradeDate || !f.MatchedAt.Before(firstBuy) || f.T0OrderGroupID != "" {
			return fail()
		}
		seen[f.FillID] = true
		sold += f.Qty
		byOrder[f.GatewayOrderID] += f.Qty
	}
	if sold != 7300 {
		return fail()
	}
	for id, sale := range sales {
		if byOrder[id] != sale.CumFilledQty {
			return fail()
		}
	}
	for i, obs := range e.Observations {
		if obs.AccountID != o.AccountID || obs.TradeDate != o.TradeDate || obs.SnapshotType != []string{"open", "close"}[i] || obs.PositionCapturedAt == nil || obs.PositionCapturedAt.IsZero() {
			return fail()
		}
	}
	return nil
}

func loadFuyingRemainingPlan(ctx context.Context, repo *ledger.Repository) (fuyingAllocationPlan, error) {
	var empty fuyingAllocationPlan
	inventory, err := loadFuying23InventoryReview(ctx, repo)
	if err != nil {
		return empty, err
	}
	link, hash, err := loadFuyingOvernightConfirmation()
	if err != nil {
		return empty, err
	}
	var frozen struct {
		Costs []performance.CostLedgerResult `json:"costs"`
	}
	if err := readPinnedJSON(fuyingCostChainPath, fuyingCostChainHash, &frozen); err != nil {
		return empty, err
	}
	var previous []ledger.PositionCostState
	for _, cost := range frozen.Costs {
		if cost.TradeDate == "2026-11-03" {
			previous = cost.Positions
		}
	}
	target, err := loadFuyingOvernightEvidence(ctx, repo, previous)
	if err != nil {
		return empty, err
	}
	var source fuyingOvernightSourceEvidence
	source.Order, err = repo.GetOrder(ctx, fuying13Account, fuyingOvernightBuy)
	if err != nil {
		return empty, err
	}
	source.Fills, err = repo.ListFills(ctx, trading.FillQuery{AccountID: fuying13Account, GatewayOrderID: fuyingOvernightBuy, TradeDate: "2026-11-03", Limit: 500})
	if err != nil {
		return empty, err
	}
	allOrders, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: "2026-11-03", Symbol: "159915", Exchange: "SZ", Limit: 500})
	if err != nil {
		return empty, err
	}
	if len(allOrders) >= 500 {
		return empty, fmt.Errorf("source order evidence truncated")
	}
	for _, o := range allOrders {
		if o.GatewayOrderID != fuyingOvernightBuy {
			source.CoreSales = append(source.CoreSales, o)
		}
	}
	allFills, err := repo.ListFills(ctx, trading.FillQuery{AccountID: fuying13Account, TradeDate: "2026-11-03", Symbol: "159915", Exchange: "SZ", Limit: 500})
	if err != nil {
		return empty, err
	}
	if len(allFills) >= 500 {
		return empty, fmt.Errorf("source fill evidence truncated")
	}
	for _, f := range allFills {
		if f.GatewayOrderID != fuyingOvernightBuy {
			source.CoreFills = append(source.CoreFills, f)
		}
	}
	for _, phase := range []string{"open", "close"} {
		obs, err := repo.GetAssetPositionObservation(ctx, fuying13Account, "2026-11-03", phase)
		if err != nil {
			return empty, err
		}
		source.Observations = append(source.Observations, obs)
		p, err := repo.ListPositionSnapshots(ctx, trading.PositionQuery{AccountID: fuying13Account, TradeDate: "2026-11-03", Symbol: "159915", Exchange: "SZ", SnapshotType: phase, Limit: 500})
		if err != nil {
			return empty, err
		}
		if phase == "open" {
			source.Open = p
		} else {
			source.Close = p
		}
	}
	if err := validateFuyingOvernightSource(source); err != nil {
		return empty, err
	}
	links := append(append([]ledger.T0Allocation(nil), inventory.Links...), link)
	return fuyingAllocationPlan{links, map[string]any{
		"inventory_evidence": inventory, "overnight_source_evidence": source, "overnight_target_evidence": target,
		"frozen_review_sha256": fuyingCostChainHash, "confirmation_sha256": hash,
		"date_only_source": "relay.t0_allocation.v2", "date_only_runtime_reader_deployed": false,
		"scope": "ownership audit only; overnight redemption relation remains research-only; no cost or NAV publication",
	}}, nil
}

func runFuyingRemainingAudit(args []string) error {
	return runFuyingAllocationAudit(args, "performance-fuying13-remaining-audit-20261113", loadFuyingRemainingPlan)
}
