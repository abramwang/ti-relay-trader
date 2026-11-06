package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func capacityRows(t *testing.T, kind string, count int) [][]byte {
	t.Helper()
	o, err := DecodeOrder(tradeFixture(t, "order"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	f, err := DecodeFill(tradeFixture(t, "fill"), tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		sys := fmt.Sprintf("%010d", i+1)
		gateway := nativeIdentity("order", tradeScope(), "SHFE", sys)
		var raw []byte
		if kind == "order" {
			row := cloneOrder(o)
			row.GatewayOrderID = gateway
			row.NativeIDs.OrderSysID = sys
			row.NativeIDs.FrontID = nil
			row.NativeIDs.SessionID = nil
			row.NativeIDs.SDKRequestID = nil
			raw, err = json.Marshal(row)
		} else {
			row := cloneFill(f)
			row.GatewayOrderID = gateway
			row.NativeIDs.OrderSysID = sys
			row.NativeIDs.TradeID = sys
			row.FillID = nativeIdentity("fill", tradeScope(), "SHFE", sys)
			raw, err = json.Marshal(row)
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	return out
}

func capacityQuery(t *testing.T, db *sql.DB, q *QueryStore, ctx context.Context, id, kind string, items [][]byte) []QueryInput {
	t.Helper()
	c := tradeQueryContext(kind)
	c.OriginMessageID = id
	r, c := registeredQueryCommand(t, db, ctx, c)
	bindQuery(t, q, ctx, id, r, c)
	inputs := []QueryInput{}
	total := 0
	for start := 0; start < len(items); start += 200 {
		end := min(start+200, len(items))
		index := len(inputs)
		raw := boundReply(t, tradePage(t, kind, fmt.Sprintf("%s-%d", id, index), index, end == len(items), items[start:end]...), r)
		if len(raw) > 262144 {
			t.Fatal("fixture exceeds OC page body budget")
		}
		total += len(raw)
		inputs = append(inputs, QueryInput{QuerySource{queryReplyStream(c.Route), fmt.Sprintf("%d-0", index+1)}, raw, time.Duration(index) * time.Millisecond})
	}
	if total > maxSnapshotBytes {
		t.Fatal("fixture exceeds OC complete output budget", total)
	}
	return inputs
}

func TestTradeCapacityTenThousandOrdersAndFills(t *testing.T) {
	db, q, _ := queryTestDB(t)
	// A large synthetic race test is allowed more wall time than small unit tests;
	// the query elapsed clock remains explicit synthetic evidence, not live timing.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	for _, kind := range []string{"fill", "order"} {
		inputs := capacityQuery(t, db, q, ctx, kind, kind, capacityRows(t, kind, 10000))
		start := time.Now()
		out, err := q.IngestBatch(ctx, kind, inputs)
		if err != nil || len(out) != 50 || out[49].State != BatchReady || !out[49].Published {
			t.Fatal("assemble", len(out), err)
		}
		for _, r := range out[:49] {
			if r.Published {
				t.Fatal("partial publication")
			}
		}
		assembly := time.Since(start)
		start = time.Now()
		applied, err := q.ApplyTradeQuery(ctx, kind)
		if err != nil || !applied.Applied || applied.RowCount != 10000 {
			t.Fatal("apply", applied, err)
		}
		t.Logf("%s: rows=10000 pages=50 assemble=%s apply=%s", kind, assembly, time.Since(start))
	}
	s, _ := NewTradeStore(db)
	report, err := s.Read(ctx, tradeScope())
	if err != nil || report.Blocked || report.PendingCount != 0 || len(report.Orders) != 10000 || len(report.Executions) != 10000 {
		t.Fatal("ledger", len(report.Orders), len(report.Executions), report.PendingCount, report.Blocked, err)
	}
	for _, r := range report.Executions {
		if r.State != "linked" || len(r.FeeEvidence) != 1 {
			t.Fatal("lost or duplicated fill evidence")
		}
	}
	if storedCount(t, db, "futures_trade_observations") != 20000 || storedCount(t, db, "futures_query_observations") != 100 {
		t.Fatal("row/page count mismatch")
	}
	restarted, _ := NewQueryStore(db)
	if _, err := restarted.Recover(ctx, "order"); err != nil {
		t.Fatal(err)
	}
	if out, err := restarted.ApplyTradeQuery(ctx, "order"); err != nil || !out.Replayed || out.RowCount != 10000 {
		t.Fatal("lost-ack replay", out, err)
	}
	if storedCount(t, db, "futures_trade_observations") != 20000 {
		t.Fatal("recovery duplicated facts")
	}
	// A downgrade must reject the enlarged journal, never truncate its evidence.
	down, err := os.ReadFile("../../migrations/postgres/000039_futures_trade_capacity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, string(down))
	_ = tx.Rollback()
	if err == nil || storedCount(t, db, "futures_trade_observations") != 20000 {
		t.Fatal("unsafe capacity downgrade")
	}
}

func TestTradeBulkAuditLateFailureRollsBackAllChunks(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	inputs := capacityQuery(t, db, q, ctx, "orders", "order", capacityRows(t, "order", 513))
	if _, err := q.IngestBatch(ctx, "orders", inputs); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_large_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic final marker failure'; END $$; CREATE TRIGGER fail_apply BEFORE INSERT ON futures_query_trade_applications FOR EACH ROW EXECUTE FUNCTION fail_large_apply()`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ApplyTradeQuery(ctx, "orders"); err == nil {
		t.Fatal("final marker failure hidden")
	}
	for _, table := range []string{"futures_trade_scopes", "futures_trade_observations", "futures_order_states", "futures_query_trade_applications"} {
		if storedCount(t, db, table) != 0 {
			t.Fatal("partial batch", table)
		}
	}
	if storedCount(t, db, "futures_query_observations") != 3 {
		t.Fatal("durable raw query lost")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_apply ON futures_query_trade_applications`); err != nil {
		t.Fatal(err)
	}
	if out, err := q.ApplyTradeQuery(ctx, "orders"); err != nil || out.RowCount != 513 {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_trade_observations") != 513 {
		t.Fatal("recovery audit count")
	}
}

func TestQueryInputBatchSourceFailureAtomicAndReplay(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	inputs := capacityQuery(t, db, q, ctx, "orders", "order", capacityRows(t, "order", 201))
	bad := append([]QueryInput(nil), inputs...)
	bad[1].Source.StreamKey = "wrong:reply"
	if _, err := q.IngestBatch(ctx, "orders", bad); err == nil {
		t.Fatal("bad source accepted")
	}
	if storedCount(t, db, "futures_query_observations") != 0 || storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("partial input chunk committed")
	}
	if _, err := q.IngestBatch(ctx, "orders", inputs); err != nil {
		t.Fatal(err)
	}
	for i := range inputs {
		inputs[i].Elapsed = time.Second
	}
	out, err := q.IngestBatch(ctx, "orders", inputs)
	if err != nil || len(out) != 2 || !out[0].Replayed || !out[1].Replayed || out[1].State != BatchReady {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_query_observations") != 2 {
		t.Fatal("duplicate raw pages")
	}
}

func TestQueryInputBatchLateConflictNeverPublishes(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	inputs := capacityQuery(t, db, q, ctx, "orders", "order", capacityRows(t, "order", 1))
	bad := inputs[0]
	bad.Raw = append(append([]byte{}, bad.Raw...), byte(' '))
	bad.Source.EntryID = "2-0"
	out, err := q.IngestBatch(ctx, "orders", append(inputs, bad))
	if err != nil || out[1].State != BatchInvalid || out[0].Published || out[1].Published {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_query_snapshots") != 0 || storedCount(t, db, "futures_query_observations") != 2 {
		t.Fatal("conflicting complete result published")
	}
	if _, err := q.Snapshot(ctx, "orders"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal(err)
	}
}
