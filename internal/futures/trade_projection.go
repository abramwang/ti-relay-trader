package futures

import (
	"fmt"
	"sort"
)

type OrderRecord struct {
	Order   OrderRow
	Blocked bool
}

// FeeEvidence is a source observation, never an aggregate/final settlement fee.
type FeeEvidence struct {
	ObservationID string
	Fee           *Money
	Complete      bool
}

type ExecutionRecord struct {
	// First valid execution economics, with source time enriched if initially null.
	// Fee is the first observation only; consumers must inspect FeeEvidence.
	Fill          FillRow
	GatewayHints  []string
	FeeEvidence   []FeeEvidence
	State         string
	LinkedGateway string
	FactConflict  bool
}

type TradeReport struct {
	Scope        TradeScope
	Orders       map[string]*OrderRecord
	Executions   map[string]*ExecutionRecord
	Conflicts    []string
	PendingCount int
	Blocked      bool
}

// tradeProjection is rebuilt from durable observations. It is not a live event
// consumer, does not infer external ownership and never fabricates missing orders.
type tradeProjection struct {
	report    TradeReport
	aliases   map[string]string
	ambiguous map[string]bool
}

func newTradeProjection(scope TradeScope) *tradeProjection {
	return &tradeProjection{report: TradeReport{Scope: scope, Orders: map[string]*OrderRecord{}, Executions: map[string]*ExecutionRecord{}, Conflicts: []string{}}, aliases: map[string]string{}, ambiguous: map[string]bool{}}
}

func (p *tradeProjection) conflict(reason string) {
	if !oneOf(reason, p.report.Conflicts...) {
		p.report.Conflicts = append(p.report.Conflicts, reason)
	}
	p.report.Blocked = true
}

func (p *tradeProjection) observe(id, kind string, raw []byte) string {
	if kind == "order" {
		row, err := DecodeOrder(raw, p.report.Scope)
		if err != nil {
			p.conflict(fmt.Sprintf("observation %s: %v", id, err))
			return "rejected"
		}
		p.order(row)
	} else if kind == "fill" {
		row, err := DecodeFill(raw, p.report.Scope)
		if err != nil {
			p.conflict(fmt.Sprintf("observation %s: %v", id, err))
			return "rejected"
		}
		p.fill(id, row)
	} else {
		p.conflict("unsupported observation kind")
		return "rejected"
	}
	p.reconcile()
	return "observed"
}

func economicsEqual(a, b TradeRow) bool {
	return a.Symbol == b.Symbol && a.Exchange == b.Exchange && a.BusinessType == b.BusinessType && a.TradeSide == b.TradeSide && a.OffsetType == b.OffsetType && a.HedgeType == b.HedgeType
}

func (p *tradeProjection) alias(row TradeRow) string {
	if row.NativeIDs.OrderSysID == "" {
		return ""
	}
	return nativeIdentity("order", p.report.Scope, row.Exchange, row.NativeIDs.OrderSysID)
}

func terminal(status string) bool { return oneOf(status, "filled", "cancelled", "rejected") }

func (p *tradeProjection) order(row OrderRow) {
	key := row.GatewayOrderID
	old := p.report.Orders[key]
	if old == nil {
		old = &OrderRecord{Order: row}
		p.report.Orders[key] = old
	} else {
		a := old.Order
		if !economicsEqual(a.TradeRow, row.TradeRow) || a.OrderQty != row.OrderQty || !equalDecimal(string(a.LimitPrice), string(row.LimitPrice)) ||
			(a.NativeIDs.OrderSysID != "" && row.NativeIDs.OrderSysID != "" && a.NativeIDs.OrderSysID != row.NativeIDs.OrderSysID) {
			old.Blocked = true
			p.conflict("order identity/economics conflict: " + key)
			return
		}
		if a.SubmittedAt != nil && row.SubmittedAt != nil && !equalInstant(*a.SubmittedAt, *row.SubmittedAt) {
			old.Blocked = true
			p.conflict("order source time conflict: " + key)
			return
		}
		if old.Order.SubmittedAt == nil {
			old.Order.SubmittedAt = row.SubmittedAt
		}
		if old.Order.NativeIDs.OrderSysID == "" {
			old.Order.NativeIDs.OrderSysID = row.NativeIDs.OrderSysID
		}
		if !old.Blocked {
			p.mergeOrder(old, row)
		}
	}
	alias := p.alias(old.Order.TradeRow)
	if alias != "" {
		if owner, ok := p.aliases[alias]; ok && owner != key {
			p.ambiguous[alias] = true
			p.report.Orders[owner].Blocked = true
			old.Blocked = true
			p.conflict("native order has multiple gateway owners: " + alias)
		} else {
			p.aliases[alias] = key
		}
	}
}

func (p *tradeProjection) mergeOrder(record *OrderRecord, row OrderRow) {
	old := record.Order
	conflict := false
	selected := false
	if terminal(old.GatewayStatus) {
		if terminal(row.GatewayStatus) {
			conflict = old.GatewayStatus != row.GatewayStatus || old.CumFilledQty != row.CumFilledQty || old.LeavesQty != row.LeavesQty
		} else {
			conflict = row.CumFilledQty > old.CumFilledQty
		}
	} else if terminal(row.GatewayStatus) {
		conflict = row.CumFilledQty < old.CumFilledQty
		selected = !conflict
	} else {
		rank := func(s string) int {
			if s == "working" {
				return 1
			}
			return 0
		}
		delta := rank(row.GatewayStatus) - rank(old.GatewayStatus)
		qty := int64(row.CumFilledQty) - int64(old.CumFilledQty)
		conflict = int64(delta)*qty < 0
		selected = !conflict && delta >= 0 && qty >= 0 && (delta > 0 || qty > 0)
	}
	if conflict {
		record.Blocked = true
		p.conflict("order lifecycle conflict: " + row.GatewayOrderID)
		return
	}
	if selected {
		// Preserve established native identity/source time even in sparse old rows.
		row.NativeIDs.OrderSysID = record.Order.NativeIDs.OrderSysID
		row.SubmittedAt = record.Order.SubmittedAt
		record.Order = row
	}
}

func (p *tradeProjection) fill(id string, row FillRow) {
	r := p.report.Executions[row.FillID]
	if r == nil {
		r = &ExecutionRecord{Fill: row, GatewayHints: []string{}, FeeEvidence: []FeeEvidence{}, State: "pending"}
		p.report.Executions[row.FillID] = r
	} else {
		a := r.Fill
		if !economicsEqual(a.TradeRow, row.TradeRow) || p.alias(a.TradeRow) != p.alias(row.TradeRow) || a.Qty != row.Qty || !equalDecimal(string(a.Price), string(row.Price)) ||
			(a.MatchedAt != nil && row.MatchedAt != nil && !equalInstant(*a.MatchedAt, *row.MatchedAt)) {
			r.FactConflict = true
			p.conflict("execution facts/source time conflict: " + row.FillID)
			return
		}
		if r.Fill.MatchedAt == nil {
			r.Fill.MatchedAt = row.MatchedAt
		}
	}
	if !oneOf(row.GatewayOrderID, r.GatewayHints...) {
		r.GatewayHints = append(r.GatewayHints, row.GatewayOrderID)
	}
	r.FeeEvidence = append(r.FeeEvidence, FeeEvidence{ObservationID: id, Fee: row.Fee, Complete: row.FeeComplete})
}

func (p *tradeProjection) reconcile() {
	totals := map[string]int64{}
	for _, r := range p.report.Executions {
		r.LinkedGateway = ""
		alias := p.alias(r.Fill.TradeRow)
		if r.FactConflict || p.ambiguous[alias] {
			r.State = "conflict"
			continue
		}
		key, ok := p.aliases[alias]
		if !ok {
			r.State = "pending"
			continue
		}
		order := p.report.Orders[key]
		valid := !order.Blocked && economicsEqual(order.Order.TradeRow, r.Fill.TradeRow)
		for _, hint := range r.GatewayHints {
			valid = valid && (hint == key || hint == alias)
		}
		if !valid {
			r.State = "conflict"
			p.conflict("execution/order association conflict: " + r.Fill.FillID)
			continue
		}
		r.State = "linked"
		r.LinkedGateway = key
		totals[key] += int64(r.Fill.Qty)
	}
	for key, total := range totals {
		if total <= int64(p.report.Orders[key].Order.OrderQty) {
			continue
		}
		p.conflict("unique executions exceed order quantity: " + key)
		for _, r := range p.report.Executions {
			if r.LinkedGateway == key {
				r.State = "conflict"
				r.LinkedGateway = ""
			}
		}
	}
	p.report.PendingCount = 0
	for _, r := range p.report.Executions {
		if r.State == "pending" {
			p.report.PendingCount++
		}
	}
	p.report.Blocked = p.report.PendingCount > 0 || len(p.report.Conflicts) > 0
	sort.Strings(p.report.Conflicts)
}
