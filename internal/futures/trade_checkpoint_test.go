package futures

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
)

func checkpointRoundTrip(t *testing.T, p *tradeProjection, count, size int) *tradeProjection {
	t.Helper()
	report, _ := json.Marshal(p.report)
	state, err := p.checkpoint(count, size, report)
	if err != nil {
		t.Fatal(err)
	}
	rh, sh := sha256.Sum256(report), sha256.Sum256(state)
	restored, err := restoreTradeProjection(p.report.Scope, count, size, report, rh[:], state, sh[:])
	if err != nil {
		t.Fatal(err)
	}
	if restored.reconciledRecords != 0 || len(restored.changedOrders)+len(restored.changedFills) != 0 {
		t.Fatal("restore replayed or marked historical facts changed")
	}
	return restored
}

func TestTradeCheckpointModelEquivalence(t *testing.T) {
	order, fill := tradeFixture(t, "order"), tradeFixture(t, "fill")
	items := []tradeObservation{
		{"o", "order", order}, {"f", "fill", fill},
		{"fee", "fill", editBody(t, fill, func(m map[string]any) { m["fee"] = "1.23000000000001"; m["fee_complete"] = true })},
		{"sparse", "order", editBody(t, order, func(m map[string]any) {
			delete(m["native_ids"].(map[string]any), "order_sys_id")
			m["submitted_at"] = nil
		})},
		{"alias", "order", editBody(t, order, func(m map[string]any) { m["gateway_order_id"] = "alias-collision" })},
		{"economics", "fill", editBody(t, fill, func(m map[string]any) { m["price"] = "3601" })},
		{"overfill", "fill", editBody(t, fill, func(m map[string]any) {
			m["native_ids"].(map[string]any)["trade_id"] = "00005679"
			m["fill_id"] = nativeIdentity("fill", tradeScope(), "SHFE", "00005679")
		})},
		{"v2", "fill", asV2Fill(t, fill)}, {"bad", "order", []byte{0xff}},
		{"cancel", "order", editBody(t, order, func(m map[string]any) { m["gateway_status"] = "cancelled"; m["leaves_qty"] = 0 })},
	}
	rng := rand.New(rand.NewSource(42))
	for run := 0; run < 60; run++ {
		order := rng.Perm(len(items))
		p := newTradeProjection(tradeScope())
		size := 0
		for i, n := range order {
			item := items[n]
			p = checkpointRoundTrip(t, p, i, size)
			p.observe(fmt.Sprint(i+1), item.kind, item.raw)
			p.reconcile()
			size += len(item.raw)
			oracle := newTradeProjection(tradeScope())
			for j, index := range order[:i+1] {
				oracle.observe(fmt.Sprint(j+1), items[index].kind, items[index].raw)
			}
			oracle.reconcile()
			a, _ := json.Marshal(p.report)
			b, _ := json.Marshal(oracle.report)
			x, _ := p.checkpoint(i+1, size, a)
			y, _ := oracle.checkpoint(i+1, size, b)
			if !bytes.Equal(a, b) || !bytes.Equal(x, y) {
				t.Fatalf("checkpoint diverged on run %d step %d\n%s\n%s\n%s\n%s", run, i, a, b, x, y)
			}
		}
	}
}

func TestTradeCheckpointRejectsUnboundState(t *testing.T) {
	p := newTradeProjection(tradeScope())
	p.observe("1", "fill", tradeFixture(t, "fill"))
	report, _ := json.Marshal(p.report)
	state, _ := p.checkpoint(1, 100, report)
	rh, sh := sha256.Sum256(report), sha256.Sum256(state)
	for _, tc := range []struct {
		scope         TradeScope
		count, size   int
		report, state []byte
	}{
		{tradeScope(), 2, 100, report, state}, {tradeScope(), 1, 101, report, state},
		{tradeScope(), 1, 100, append(bytes.Clone(report), ' '), state},
		{tradeScope(), 1, 100, report, append(bytes.Clone(state), ' ')},
		{tradeScope(), 1, 100, report, nil},
	} {
		if _, err := restoreTradeProjection(tc.scope, tc.count, tc.size, tc.report, rh[:], tc.state, sh[:]); err == nil {
			t.Fatal("accepted unbound checkpoint", tc.count, tc.size)
		}
	}
	wrong := tradeScope()
	wrong.TradeDate = "20261105"
	if _, err := restoreTradeProjection(wrong, 1, 100, report, rh[:], state, sh[:]); err == nil {
		t.Fatal("accepted another day")
	}
}

func TestTradeCheckpointRejectsInconsistentIndexes(t *testing.T) {
	p := newTradeProjection(tradeScope())
	p.observe("1", "order", tradeFixture(t, "order"))
	p.observe("2", "fill", tradeFixture(t, "fill"))
	report, _ := json.Marshal(p.report)
	state, _ := p.checkpoint(2, 100, report)
	rh := sha256.Sum256(report)
	for name, mutate := range map[string]func(*tradeCheckpoint){
		"version":         func(c *tradeCheckpoint) { c.Version++ },
		"missing_aliases": func(c *tradeCheckpoint) { c.Aliases = nil },
		"unknown_owner": func(c *tradeCheckpoint) {
			for alias := range c.Aliases {
				c.Aliases[alias] = "unknown"
			}
		},
		"eligibility": func(c *tradeCheckpoint) {
			for id := range c.Eligible {
				delete(c.Eligible, id)
			}
		},
		"total": func(c *tradeCheckpoint) {
			for alias := range c.Totals {
				c.Totals[alias]++
			}
		},
		"overfill": func(c *tradeCheckpoint) {
			for alias := range c.Overfilled {
				c.Overfilled[alias] = !c.Overfilled[alias]
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var cp tradeCheckpoint
			if err := json.Unmarshal(state, &cp); err != nil {
				t.Fatal(err)
			}
			mutate(&cp)
			bad, _ := json.Marshal(cp)
			sh := sha256.Sum256(bad)
			if _, err := restoreTradeProjection(tradeScope(), 2, 100, report, rh[:], bad, sh[:]); err == nil {
				t.Fatal("accepted internally inconsistent checkpoint")
			}
		})
	}
}
