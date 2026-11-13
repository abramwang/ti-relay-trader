package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const fuyingOvernightGroup = "user-confirmed-768-20261103-overnight-159915"
const fuyingOvernightBuy = fuying23Prefix + "12002A620003293"
const fuyingOvernightRedemption = fuying23Prefix + "12002A620008286"
const fuyingOvernightCoreBuy = fuying23Prefix + "12002A620000014"

func loadFuyingOvernightConfirmation() (ledger.T0Allocation, string, error) {
	raw, err := os.ReadFile("testdata/fixtures/fuying13_overnight_review_20261111.json")
	if err != nil {
		return ledger.T0Allocation{}, "", err
	}
	var evidence map[string]any
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "b7e39c26a0014ca5465e4128cfc174e6a5399c886ab8f1e93554793567109851" {
		return ledger.T0Allocation{}, "", fmt.Errorf("overnight confirmation fingerprint changed")
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return ledger.T0Allocation{}, "", err
	}
	want := map[string]any{"account_id": fuying13Account, "security_id": "159915.SZ", "source_date": "2026-11-03", "redemption_date": "2026-11-04",
		"source_buy_order": fuyingOvernightBuy, "redemption_order": fuyingOvernightRedemption, "core_buy_order": fuyingOvernightCoreBuy,
		"overnight_quantity": float64(1000000), "core_quantity": float64(123200), "group_id": fuyingOvernightGroup,
		"evidence_type": "user_confirmation", "confirmation_date": "2026-11-11", "scope": "ownership_only_not_settlement_confirmation"}
	for k, v := range want {
		if evidence[k] != v {
			return ledger.T0Allocation{}, "", fmt.Errorf("overnight confirmation changed at %s", k)
		}
	}
	link := ledger.T0Allocation{LinkID: "fu13-20261103-overnight-buy-v1", AccountID: fuying13Account, TradeDate: "2026-11-03", GatewayOrderID: fuyingOvernightBuy,
		SecurityID: "159915.SZ", TradeSide: "B", Quantity: 1000000, GroupID: fuyingOvernightGroup, Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation",
		EvidenceRef: "testdata/fixtures/fuying13_overnight_review_20261111.json", ConfirmedBy: "relay:recorded-user-confirmation", ConfirmedDate: "2026-11-11"}
	_, err = ledger.ResolveT0Allocations([]ledger.T0Allocation{link})
	return link, fmt.Sprintf("%x", sha256.Sum256(raw)), err
}

type fuyingOvernightEvidence struct {
	Source                 ledger.PositionCostState          `json:"source_cost_memory_only"`
	Orders                 []trading.Order                   `json:"orders"`
	Fills                  []trading.Fill                    `json:"fills"`
	Transfers              []trading.ComponentTransfer       `json:"parent_transfers"`
	Open                   []trading.Position                `json:"open_positions"`
	Close                  []trading.Position                `json:"close_positions"`
	Observations           []ledger.AssetPositionObservation `json:"observations"`
	Fees                   []ledger.OrderFeeRecord           `json:"core_buy_fee_records"`
	Preview                performance.OvernightT0Preview    `json:"daily_estimate"`
	CoreBuyAmount          float64                           `json:"core_buy_amount"`
	CoreBuyFee             float64                           `json:"core_buy_fee"`
	CoreCloseCost          float64                           `json:"core_close_cost"`
	CoreCloseMarketValue   float64                           `json:"core_close_market_value"`
	CoreDayPnL             float64                           `json:"core_day_pnl"`
	OvernightCloseQuantity int64                             `json:"overnight_close_quantity_diagnostic"`
	CoreCloseQuantity      int64                             `json:"core_close_quantity_diagnostic"`
	QuantityResidual       int64                             `json:"quantity_residual_diagnostic"`
}

func reviewFuyingOvernight(ctx context.Context, repo *ledger.Repository, service *performance.Service, previous []ledger.PositionCostState) (fuyingOvernightEvidence, error) {
	e, err := loadFuyingOvernightEvidence(ctx, repo, previous)
	if err != nil {
		return e, err
	}
	var redemption trading.Order
	for _, o := range e.Orders {
		if o.GatewayOrderID == fuyingOvernightRedemption {
			redemption = o
		}
	}
	e.Preview, err = service.PreviewOvernightT0(ctx, e.Source, redemption, e.Transfers[0])
	if err != nil {
		return e, err
	}
	for _, f := range e.Fills {
		e.CoreBuyAmount += f.Price * float64(f.Qty)
	}
	e.CoreBuyFee = e.Fees[0].TotalFee
	e.CoreCloseCost = e.CoreBuyAmount + e.CoreBuyFee
	e.CoreCloseQuantity = e.Close[0].Quantity
	e.CoreCloseMarketValue = float64(e.CoreCloseQuantity) * e.Preview.TargetClosePrice
	e.CoreDayPnL = e.CoreCloseMarketValue - e.CoreCloseCost
	e.QuantityResidual = e.Open[0].Quantity + 123200 - e.Transfers[0].Qty - e.Close[0].Quantity
	return e, nil
}

func loadFuyingOvernightEvidence(ctx context.Context, repo *ledger.Repository, previous []ledger.PositionCostState) (fuyingOvernightEvidence, error) {
	var e fuyingOvernightEvidence
	for _, s := range previous {
		if s.Symbol != "159915" || s.Exchange != "SZ" {
			continue
		}
		if s.CostBucket == "ETF_T0_RESIDUAL:"+fuyingOvernightGroup && e.Source.AccountID == "" {
			e.Source = s
		} else if s.CloseQuantity != 0 {
			return e, fmt.Errorf("unexpected additional overnight lot")
		}
	}
	var err error
	e.Orders, err = repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, Limit: 500})
	if err != nil {
		return e, err
	}
	e.Fills, err = repo.ListFills(ctx, trading.FillQuery{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, Limit: 500})
	if err != nil {
		return e, err
	}
	e.Transfers, err = repo.ListComponentTransfers(ctx, trading.ComponentTransferQuery{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, Limit: 500})
	if err != nil {
		return e, err
	}
	e.Fees, err = repo.ListOrderFeeRecords(ctx, ledger.OrderFeeRecordQuery{AccountID: fuying13Account, TradeDate: "2026-11-04", GatewayOrderID: fuyingOvernightCoreBuy, Limit: 500})
	if err != nil {
		return e, err
	}
	for _, phase := range []string{"open", "close"} {
		o, err := repo.GetAssetPositionObservation(ctx, fuying13Account, "2026-11-04", phase)
		if err != nil {
			return e, err
		}
		e.Observations = append(e.Observations, o)
		p, err := repo.ListPositionSnapshots(ctx, trading.PositionQuery{AccountID: fuying13Account, TradeDate: "2026-11-04", Symbol: "159915", Exchange: trading.ExchangeSZ, SnapshotType: phase, Limit: 500})
		if err != nil {
			return e, err
		}
		if phase == "open" {
			e.Open = p
		} else {
			e.Close = p
		}
	}
	if err := validateFuyingOvernightEvidence(e); err != nil {
		return e, err
	}
	return e, nil
}

// Fixed scope only: extra orders, lots, fills or partial evidence require review,
// never a best-effort allocation based on a basket tag alone.
func validateFuyingOvernightEvidence(e fuyingOvernightEvidence) error {
	fail := func() error { return fmt.Errorf("November 3/4 overnight evidence outside confirmed scope") }
	s := e.Source
	for _, v := range []float64{s.BuyAmount, s.BuyFee, s.CloseTotalCost, s.ClosePrice, s.CloseMarketValue} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return fail()
		}
	}
	if s.AccountID != fuying13Account || s.TradeDate != "2026-11-03" || s.Symbol != "159915" || s.Exchange != "SZ" || s.CostBucket != "ETF_T0_RESIDUAL:"+fuyingOvernightGroup || s.Status != "calculated" || s.OpenQuantity != 0 || s.BuyQuantity != 1000000 || s.SellQuantity != 0 || s.CloseQuantity != 1000000 || math.Abs(s.BuyAmount-2452000) > 0.0001 || math.Abs(s.BuyFee-122.6) > 0.0001 || math.Abs(s.CloseTotalCost-2452122.6) > 0.0001 || len(e.Orders) != 2 || len(e.Fills) == 0 || len(e.Fills) >= 500 || len(e.Transfers) != 1 || len(e.Open) != 1 || len(e.Close) != 1 || len(e.Observations) != 2 || len(e.Fees) != 1 {
		return fail()
	}
	seen := map[string]bool{}
	for _, o := range e.Orders {
		if seen[o.GatewayOrderID] || o.AccountID != fuying13Account || o.TradeDate != "2026-11-04" || o.Symbol != "159915" || o.Exchange != trading.ExchangeSZ || !o.IsTerminal || o.T0OrderGroupID != "" || o.StrategyID != "" || o.StrategyType != "" || o.OrderQty != o.CumFilledQty {
			return fail()
		}
		seen[o.GatewayOrderID] = true
		switch o.GatewayOrderID {
		case fuyingOvernightCoreBuy:
			if o.TradeSide != trading.TradeSideBuy || o.BusinessType != trading.BusinessTypeStock || o.CumFilledQty != 123200 || o.BasketID != "BTR.84406746|J.0" {
				return fail()
			}
		case fuyingOvernightRedemption:
			if o.TradeSide != trading.TradeSideRedemption || o.BusinessType != trading.BusinessTypeETF || o.CumFilledQty != 1000000 || o.BasketID != "etfarb#159915" {
				return fail()
			}
		default:
			return fail()
		}
	}
	seen = map[string]bool{}
	var quantity int64
	var amount float64
	for _, f := range e.Fills {
		if f.FillID == "" || seen[f.FillID] || f.AccountID != fuying13Account || f.TradeDate != "2026-11-04" || f.GatewayOrderID != fuyingOvernightCoreBuy || f.Symbol != "159915" || f.Exchange != trading.ExchangeSZ || f.TradeSide != trading.TradeSideBuy || f.BusinessType != trading.BusinessTypeStock || f.Qty <= 0 || f.Qty > 123200 || f.Price <= 0 || math.IsNaN(f.Price) || math.IsInf(f.Price, 0) || f.MatchedAt.IsZero() || f.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != "2026-11-04" || f.T0OrderGroupID != "" || f.StrategyID != "" || f.StrategyType != "" {
			return fail()
		}
		seen[f.FillID] = true
		quantity += f.Qty
		amount += f.Price * float64(f.Qty)
	}
	if quantity != 123200 || math.Abs(amount-296296) > 0.0001 {
		return fail()
	}
	for i, p := range []trading.Position{e.Open[0], e.Close[0]} {
		if p.AccountID != fuying13Account || p.TradeDate != "2026-11-04" || p.Symbol != "159915" || p.Exchange != trading.ExchangeSZ || p.Quantity != []int64{1000000, 123200}[i] {
			return fail()
		}
	}
	for i, o := range e.Observations {
		if o.AccountID != fuying13Account || o.TradeDate != "2026-11-04" || o.SnapshotType != []string{"open", "close"}[i] || o.PositionCapturedAt == nil || o.PositionCapturedAt.IsZero() {
			return fail()
		}
	}
	t := e.Transfers[0]
	if t.AccountID != fuying13Account || t.TradeDate != "2026-11-04" || t.GatewayOrderID != fuyingOvernightRedemption || t.Symbol != "159915" || t.Exchange != trading.ExchangeSZ || t.ComponentSymbol != "159915" || t.ComponentExchange != trading.ExchangeSZ || t.TradeSide != trading.TradeSideRedemption || t.BusinessType != trading.BusinessTypeETF || t.Qty != 1000000 || t.ComponentQty != 1000000 || t.MatchedAt.IsZero() || t.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != "2026-11-04" {
		return fail()
	}
	f := e.Fees[0]
	if f.AccountID != fuying13Account || f.TradeDate != "2026-11-04" || f.GatewayOrderID != fuyingOvernightCoreBuy || f.RecordScope != "order" || !f.FeeComplete || !f.AssociationComplete || f.FeeSource == "" || f.FeeSource == "unavailable" || math.Abs(f.TotalFee-14.8148) > 0.0001 || math.IsNaN(f.TotalFee) {
		return fail()
	}
	return nil
}
