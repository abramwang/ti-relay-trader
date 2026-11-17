package futures

import (
	"bytes"
	"testing"
)

func TestFillV2StoreMixedVersionsSurviveRestart(t *testing.T) {
	for _, firstVersion := range []string{"v1", "v2"} {
		t.Run(firstVersion, func(t *testing.T) {
			db, s, ctx := tradeTestDB(t)
			old := tradeFixture(t, "fill")
			new := asV2Fill(t, old)
			first, second := old, new
			if firstVersion == "v2" {
				first, second = new, old
			}
			if _, err := s.Observe(ctx, tradeScope(), "order", "order", tradeFixture(t, "order")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Observe(ctx, tradeScope(), "first", "fill", first); err != nil {
				t.Fatal(err)
			}
			s, _ = NewTradeStore(db)
			out, err := s.Observe(ctx, tradeScope(), "second", "fill", second)
			if err != nil || !out.Blocked {
				t.Fatal("mixed version not blocked", out, err)
			}
			if storedCount(t, db, "futures_trade_observations") != 3 || storedCount(t, db, "futures_execution_facts") != 1 {
				t.Fatal("duplicate execution or missing audit")
			}
			var raw []byte
			if err := db.QueryRowContext(ctx, `SELECT body FROM futures_trade_observations WHERE source_id='second'`).Scan(&raw); err != nil || !bytes.Equal(raw, second) {
				t.Fatal("mixed original lost", err)
			}
			s, _ = NewTradeStore(db)
			if out, err := s.Observe(ctx, tradeScope(), "second", "fill", second); err != nil || !out.Replayed || !out.Blocked {
				t.Fatal("restart unblocked mixed scope", out, err)
			}
			r, err := s.Read(ctx, tradeScope())
			if err != nil || !r.Blocked || r.FillIdentityVersion != firstVersion || len(r.Executions) != 1 {
				t.Fatal(r, err)
			}
		})
	}
}

func TestFillV2StoreQueryRealtimeAndRestart(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	orders, fills := v2Pair(t)
	q, _ := NewQueryStore(db)
	readyTradeQuery(t, q, ctx, "v2-fills", "fill", fills...)
	if _, err := q.ApplyTradeQuery(ctx, "v2-fills"); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"a", "b"} {
		if _, err := s.Observe(ctx, tradeScope(), "order-"+key, "order", orders[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Observe(ctx, tradeScope(), "realtime-"+key, "fill", fills[i]); err != nil {
			t.Fatal(err)
		}
	}
	s, _ = NewTradeStore(db)
	r, err := s.Read(ctx, tradeScope())
	if err != nil || r.Blocked || r.FillIdentityVersion != "v2" || r.PendingCount != 0 || len(r.Executions) != 2 {
		t.Fatal(r, err)
	}
	if storedCount(t, db, "futures_execution_facts") != 2 {
		t.Fatal("query/realtime duplicate")
	}
	for _, r := range r.Executions {
		if len(r.FeeEvidence) != 2 || r.Fill.Qty != 1 || r.State != "linked" {
			t.Fatal("replay count", r)
		}
	}
	if out, err := q.ApplyTradeQuery(ctx, "v2-fills"); err != nil || !out.Replayed {
		t.Fatal(out, err)
	}
}
