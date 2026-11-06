package futures

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Pre-optimization full-scan oracle, intentionally independent of dirty groups,
// eligible totals and pending deltas. Compare after EVERY observation, not just
// the final batch, so transient conflicts cannot silently disappear.
func reconcileFullScan(p *tradeProjection) {
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

func TestTradeIncrementalReconcileMatchesFullScan(t *testing.T) {
	o, err := DecodeOrder(tradeFixture(t, "order"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	f, err := DecodeFill(tradeFixture(t, "fill"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	variants := []TradeEvent{{Order: &o}, {Fill: &f}}
	otherOrder := cloneOrder(o)
	otherOrder.GatewayOrderID = "competing-owner"
	variants = append(variants, TradeEvent{Order: &otherOrder})
	changed := cloneOrder(o)
	changed.LimitPrice = "3601"
	variants = append(variants, TradeEvent{Order: &changed})
	second := cloneFill(f)
	second.FillID = "second"
	variants = append(variants, TradeEvent{Fill: &second})
	bad := cloneFill(f)
	bad.Price = "3601"
	variants = append(variants, TradeEvent{Fill: &bad})
	wrong := cloneFill(second)
	wrong.GatewayOrderID = "wrong-owner"
	variants = append(variants, TradeEvent{Fill: &wrong})
	unknown := cloneFill(f)
	unknown.FillID = "unknown"
	unknown.NativeIDs.OrderSysID = "unknown"
	unknown.GatewayOrderID = "later"
	variants = append(variants, TradeEvent{Fill: &unknown})
	later := cloneOrder(o)
	later.GatewayOrderID = "later"
	later.NativeIDs.OrderSysID = "unknown"
	variants = append(variants, TradeEvent{Order: &later})
	sparse := cloneOrder(later)
	sparse.NativeIDs.OrderSysID = ""
	variants = append(variants, TradeEvent{Order: &sparse})
	rng := rand.New(rand.NewSource(39))
	for run := 0; run < 100; run++ {
		fast, slow := newTradeProjection(tradeScope()), newTradeProjection(tradeScope())
		for i := 0; i < 40; i++ {
			e := variants[rng.Intn(len(variants))]
			for _, p := range []*tradeProjection{fast, slow} {
				if e.Order != nil {
					p.order(cloneOrder(*e.Order))
				} else {
					p.fill(fmt.Sprint(i), cloneFill(*e.Fill))
				}
			}
			fast.reconcile()
			reconcileFullScan(slow)
			if !reflect.DeepEqual(fast.report, slow.report) {
				a, _ := json.Marshal(fast.report)
				b, _ := json.Marshal(slow.report)
				t.Fatalf("run=%d step=%d\nfast=%s\nslow=%s", run, i, a, b)
			}
		}
	}
}

func TestTradeSingleOrderTenThousandFillsLinear(t *testing.T) {
	o, err := DecodeOrder(tradeFixture(t, "order"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	f, err := DecodeFill(tradeFixture(t, "fill"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	o.OrderQty = 10000
	o.LeavesQty = 10000
	p := newTradeProjection(tradeScope())
	p.order(o)
	p.reconcile()
	for i := 0; i < 10000; i++ {
		row := cloneFill(f)
		row.FillID = fmt.Sprint(i)
		p.fill(fmt.Sprint(i), row)
		p.reconcile()
	}
	if p.report.Blocked || p.report.PendingCount != 0 || len(p.report.Executions) != 10000 || p.reconciledRecords > 20000 {
		t.Fatal("nonlinear/incorrect association", p.reconciledRecords, p.report.Blocked)
	}
}

func TestTradeCapacitySeparateFromQueryPageBudget(t *testing.T) {
	if maxAuditCount != 1024 || maxAuditBytes != 32*1024*1024 {
		t.Fatal("query page budget accidentally expanded")
	}
	for _, c := range [][4]int{{50000, 67108864, 0, 0}, {49999, 67108863, 1, 1}} {
		if err := tradeCapacity(c[0], c[1], c[2], c[3]); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range [][4]int{{50000, 0, 1, 0}, {0, 67108864, 0, 1}, {-1, 0, 0, 0}, {50001, 0, 0, 0}, {0, 0, 50001, 0}, {0, 0, 0, 67108865}} {
		if !errors.Is(tradeCapacity(c[0], c[1], c[2], c[3]), ErrAuditCapacity) {
			t.Fatal(c)
		}
	}
	limits := DefaultBatchLimits()
	limits.Rows = 10001
	if _, err := NewQueryBatch(batchContext("positions"), 0, time.Minute, limits); err == nil {
		t.Fatal("query exceeds OC candidate row cap")
	}
	s := &QueryStore{}
	for _, inputs := range [][]QueryInput{nil, make([]QueryInput, 257), {{Raw: make([]byte, maxSnapshotBytes+1)}}} {
		if _, err := s.IngestBatch(context.Background(), "none", inputs); !errors.Is(err, ErrAuditCapacity) {
			t.Fatal(err)
		}
	}
}
