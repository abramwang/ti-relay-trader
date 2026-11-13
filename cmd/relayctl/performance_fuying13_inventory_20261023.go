package main

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const fuying23Date = "2026-10-23"
const fuying23Prefix = "external-huaxin-31400004576801-"
const fuying23CashSHA = "dae4863b8e337b9c0b28ad37e84503f84a920e3ea066fc8f956771cc2748a224"

func fuying23BasketMatches(value string) bool {
	return value == "etfarb#159869" || strings.HasPrefix(value, "etfarb#159869.SZSE#")
}

type fuying23InventoryEvidence struct {
	Orders       []trading.Order                   `json:"orders"`
	Fills        []trading.Fill                    `json:"fills"`
	Transfers    []trading.ComponentTransfer       `json:"parent_transfers"`
	Open         []trading.Position                `json:"open_positions"`
	Close        []trading.Position                `json:"close_positions"`
	Observations []ledger.AssetPositionObservation `json:"observations"`
	Cash         []brokerCashFlowRow               `json:"broker_cash_rows"`
	CashSHA      string                            `json:"cash_sha256"`
	Links        []ledger.T0Allocation             `json:"inventory_review_links_memory_only"`
}

func loadFuying23InventoryReview(ctx context.Context, repo *ledger.Repository) (fuying23InventoryEvidence, error) {
	var e fuying23InventoryEvidence
	data, hash, err := readBrokerCSV("reference/314000045768_资金流水.csv")
	if err != nil || hash != fuying23CashSHA {
		return e, fmt.Errorf("October 23 reviewed cash source changed: %v", err)
	}
	for _, row := range data.rows {
		account, aerr := data.value(row, "投资者")
		fund, ferr := data.value(row, "资金账户")
		if aerr != nil || ferr != nil || account != fuying13Account || fund != fuying13Account+"01" {
			return e, fmt.Errorf("cash account identity mismatch")
		}
	}
	flows, unique, _, _, err := parseAndAuditCashFlows(data, nil)
	if err != nil || unique != len(flows) {
		return e, fmt.Errorf("cash identity/parse failure: %v", err)
	}
	for _, f := range flows {
		if f.TradeDate == "20261023" && f.Code == "159869" {
			e.Cash = append(e.Cash, f)
		}
	}
	e.CashSHA = hash
	e.Orders, err = repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, Limit: 500})
	if err != nil {
		return e, err
	}
	e.Fills, err = repo.ListFills(ctx, trading.FillQuery{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, Limit: 500})
	if err != nil {
		return e, err
	}
	e.Transfers, err = repo.ListComponentTransfers(ctx, trading.ComponentTransferQuery{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, Limit: 500})
	if err != nil {
		return e, err
	}
	for _, phase := range []string{"open", "close"} {
		o, err := repo.GetAssetPositionObservation(ctx, fuying13Account, fuying23Date, phase)
		if err != nil {
			return e, err
		}
		e.Observations = append(e.Observations, o)
		positions, err := repo.ListPositionSnapshots(ctx, trading.PositionQuery{AccountID: fuying13Account, TradeDate: fuying23Date, Symbol: "159869", Exchange: trading.ExchangeSZ, SnapshotType: phase, Limit: 500})
		if err != nil {
			return e, err
		}
		if phase == "open" {
			e.Open = positions
		} else {
			e.Close = positions
		}
	}
	if err := validateFuying23Inventory(e); err != nil {
		return e, err
	}
	for _, pair := range []struct {
		id, side string
		qty      int64
	}{{"12002A620003017", "B", 536400}, {"12002A620003020", "B", 463600}, {"12002A620003347", "R", 1000000}} {
		e.Links = append(e.Links, ledger.T0Allocation{LinkID: "fu13-20261023-inventory-" + pair.id + "-v1", AccountID: fuying13Account, TradeDate: fuying23Date, GatewayOrderID: fuying23Prefix + pair.id,
			SecurityID: "159869.SZ", TradeSide: pair.side, Quantity: pair.qty, GroupID: "inventory-768-20261023-round2", Role: "redemption", Revision: 1, Status: "active", EvidenceType: "inventory_evidence",
			EvidenceRef: "docs/PERFORMANCE_BLOCK_REVIEW_20261109.md#11-10-23日底仓清空与补单归属预演", ConfirmedBy: "relay:read-only-inventory-review"})
	}
	// Reuse the actual recorded review timestamps from the immutable evidence,
	// rather than giving the same link a different payload on every invocation.
	var frozen struct {
		Inventory fuying23InventoryEvidence `json:"oct23_inventory_evidence"`
	}
	if err := readPinnedJSON(fuyingCostChainPath, fuyingCostChainHash, &frozen); err != nil {
		return e, err
	}
	e.Links, err = stableFuyingInventoryLinks(e.Links, frozen.Inventory.Links)
	if err != nil {
		return e, err
	}
	_, err = ledger.ResolveT0Allocations(e.Links)
	return e, err
}

func stableFuyingInventoryLinks(current, frozen []ledger.T0Allocation) ([]ledger.T0Allocation, error) {
	if len(current) != 3 || len(frozen) != 3 {
		return nil, fmt.Errorf("inventory audit coverage changed")
	}
	result := make([]ledger.T0Allocation, 0, 3)
	for i, want := range frozen {
		got := current[i]
		if want.ConfirmedAt.IsZero() || want.ConfirmedDate != "" {
			return nil, fmt.Errorf("inventory audit timestamp missing")
		}
		got.ConfirmedAt = want.ConfirmedAt
		if !reflect.DeepEqual(got, want) {
			return nil, fmt.Errorf("inventory audit payload changed")
		}
		result = append(result, want)
	}
	return ledger.ResolveT0Allocations(result)
}

// This is a fixed historical evidence audit, not a production matching rule.
func validateFuying23Inventory(e fuying23InventoryEvidence) error {
	fail := func() error { return fmt.Errorf("October 23 inventory evidence no longer matches reviewed scope") }
	if len(e.Orders) != 9 || len(e.Fills) == 0 || len(e.Fills) >= 500 || len(e.Transfers) != 3 || len(e.Open) != 1 || len(e.Close) > 1 || len(e.Observations) != 2 || e.CashSHA != fuying23CashSHA {
		return fail()
	}
	for i, o := range e.Observations {
		if o.AccountID != fuying13Account || o.TradeDate != fuying23Date || o.SnapshotType != []string{"open", "close"}[i] || o.PositionCapturedAt == nil || o.PositionCapturedAt.IsZero() {
			return fail()
		}
	}
	for _, p := range append(append([]trading.Position(nil), e.Open...), e.Close...) {
		if p.AccountID != fuying13Account || p.TradeDate != fuying23Date || p.Symbol != "159869" || p.Exchange != trading.ExchangeSZ {
			return fail()
		}
	}
	if e.Open[0].Quantity != 292000 || len(e.Close) == 1 && e.Close[0].Quantity != 0 {
		return fail()
	}
	type expected struct {
		side  trading.TradeSide
		qty   int64
		cents int64
	}
	want := map[string]expected{
		"12002A620000066": {trading.TradeSideSell, 0, 0}, "12002A620001102": {trading.TradeSideSell, 292000, 27856800},
		"12002A620002593": {trading.TradeSideBuy, 1000000, 94500000}, "12002A620002684": {trading.TradeSideRedemption, 1000000, 0},
		"12002A620003017": {trading.TradeSideBuy, 536400, 50421600}, "12002A620003020": {trading.TradeSideBuy, 463600, 43710410},
		"12002A620003347": {trading.TradeSideRedemption, 1000000, 0}, "12002A620003915": {trading.TradeSideBuy, 1000000, 94400000}, "12002A620003992": {trading.TradeSideRedemption, 1000000, 0},
	}
	orders := map[string]trading.Order{}
	for _, o := range e.Orders {
		id := strings.TrimPrefix(o.GatewayOrderID, fuying23Prefix)
		w, ok := want[id]
		_, duplicate := orders[o.GatewayOrderID]
		if !ok || duplicate || o.GatewayOrderID != fuying23Prefix+id || o.AccountID != fuying13Account || o.TradeDate != fuying23Date || o.Symbol != "159869" || o.Exchange != trading.ExchangeSZ || !o.IsTerminal || o.CumFilledQty != w.qty || o.OrderQty < o.CumFilledQty || o.TradeSide != w.side || o.T0OrderGroupID != "" || o.StrategyID != "" || o.StrategyType != "" {
			return fail()
		}
		if (w.side == trading.TradeSideRedemption && o.BusinessType != trading.BusinessTypeETF) || (w.side != trading.TradeSideRedemption && o.BusinessType != trading.BusinessTypeStock) {
			return fail()
		}
		if w.side != trading.TradeSideSell {
			if id == "12002A620003020" {
				if o.BasketID != "" {
					return fail()
				}
			} else if !fuying23BasketMatches(o.BasketID) {
				return fail()
			}
		}
		orders[o.GatewayOrderID] = o
	}
	type event struct {
		id  string
		at  time.Time
		qty int64
	}
	var events []event
	qty := map[string]int64{}
	amount := map[string]float64{}
	seen := map[string]bool{}
	for _, f := range e.Fills {
		o, ok := orders[f.GatewayOrderID]
		key := f.GatewayOrderID + "/" + f.FillID
		if !ok || seen[key] || f.FillID == "" || f.AccountID != fuying13Account || f.TradeDate != fuying23Date || f.Symbol != "159869" || f.Exchange != trading.ExchangeSZ || f.Qty <= 0 || f.Qty > 3000000 || f.Price <= 0 || math.IsNaN(f.Price) || math.IsInf(f.Price, 0) || f.TradeSide != o.TradeSide || f.BusinessType != trading.BusinessTypeStock || f.T0OrderGroupID != "" || f.StrategyID != "" || f.StrategyType != "" {
			return fail()
		}
		if f.TradeSide == trading.TradeSideBuy && f.BasketID != "" && !fuying23BasketMatches(f.BasketID) {
			return fail()
		}
		seen[key] = true
		qty[f.GatewayOrderID] += f.Qty
		amount[f.GatewayOrderID] += float64(f.Qty) * f.Price
		delta := f.Qty
		if f.TradeSide == trading.TradeSideSell {
			delta = -delta
		}
		events = append(events, event{f.GatewayOrderID, f.MatchedAt, delta})
	}
	for _, tr := range e.Transfers {
		o, ok := orders[tr.GatewayOrderID]
		if !ok || o.TradeSide != trading.TradeSideRedemption || qty[tr.GatewayOrderID] != 0 || tr.AccountID != fuying13Account || tr.TradeDate != fuying23Date || tr.Symbol != "159869" || tr.Exchange != trading.ExchangeSZ || tr.ComponentSymbol != "159869" || tr.ComponentExchange != trading.ExchangeSZ || tr.TradeSide != trading.TradeSideRedemption || tr.BusinessType != trading.BusinessTypeETF || tr.ComponentQty != 1000000 || tr.Qty != 1000000 {
			return fail()
		}
		qty[tr.GatewayOrderID] = tr.Qty
		events = append(events, event{tr.GatewayOrderID, tr.MatchedAt, -tr.Qty})
	}
	for id, w := range want {
		if qty[fuying23Prefix+id] != w.qty || math.Abs(amount[fuying23Prefix+id]*100-float64(w.cents)) > 0.001 {
			return fail()
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	// Enforce a flat boundary after the core sale and after each redemption.
	segments := [][]string{{"12002A620001102"}, {"12002A620002593", "12002A620002684"}, {"12002A620003017", "12002A620003020", "12002A620003347"}, {"12002A620003915", "12002A620003992"}}
	segmentOf := map[string]int{}
	for i, ids := range segments {
		for _, id := range ids {
			segmentOf[fuying23Prefix+id] = i
		}
	}
	position := int64(292000)
	segment := 0
	var boundary time.Time
	for _, ev := range events {
		next, ok := segmentOf[ev.id]
		if !ok || ev.at.IsZero() || ev.at.In(timeutil.Location()).Format("2006-01-02") != fuying23Date || next < segment {
			return fail()
		}
		if next != segment {
			if next != segment+1 || position != 0 || !ev.at.After(boundary) {
				return fail()
			}
			segment = next
		}
		if next > 0 && ev.qty < 0 && !ev.at.After(boundary) {
			return fail()
		}
		position += ev.qty
		if position < 0 {
			return fail()
		}
		boundary = ev.at
	}
	if segment != 3 || position != 0 {
		return fail()
	}
	// Exact ledger cents, not the rounded average price printed in summaries.
	flows := map[string]int64{"2747072774": 11027610, "2747072498": -4859, "2747071957": -94404720, "2747070185": -4859, "2747070001": 11027610, "2747069812": -43712596, "2747069775": -50424121, "2747068634": -4859, "2747068292": 11027610, "2747067937": -94504725, "2747067726": 27855407}
	if len(e.Cash) != len(flows) {
		return fail()
	}
	for _, f := range e.Cash {
		v, ok := flows[f.FlowID]
		if !ok || f.TradeDate != "20261023" || f.Code != "159869" || f.InCents-f.OutCents != v {
			return fail()
		}
		delete(flows, f.FlowID)
	}
	return nil
}
