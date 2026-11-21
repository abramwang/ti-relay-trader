package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestTradeCheckpointAppendMatchesFullReplay(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	order, fill := tradeFixture(t, "order"), tradeFixture(t, "fill")
	items := []tradeObservation{
		{"fill-first", "fill", fill}, {"order", "order", order}, {"fill-first", "fill", fill},
		{"fee", "fill", editBody(t, fill, func(m map[string]any) { m["fee"] = "1.234567890123"; m["fee_complete"] = true })},
		{"source", "order", order}, {"source", "fill", fill}, {"source", "fill", fill},
		{"collision", "order", editBody(t, order, func(m map[string]any) { m["gateway_order_id"] = "other-owner" })},
		{"mixed", "fill", asV2Fill(t, fill)}, {"invalid", "fill", []byte{0xff}},
	}
	for i, item := range items {
		s, _ = NewTradeStore(db)
		if _, err := s.Observe(ctx, tradeScope(), item.sourceID, item.kind, item.raw); err != nil {
			t.Fatal(i, err)
		}
		got, err := s.Verify(ctx, tradeScope())
		if err != nil {
			t.Fatal("full replay differs", i, err)
		}
		want, err := s.Read(ctx, tradeScope())
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		if err != nil || !bytes.Equal(a, b) {
			t.Fatal(i, err)
		}
	}
}

func TestTradeCheckpointUpdatesOnlyRelatedFacts(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	orders, fills := capacityRows(t, "order", 50), capacityRows(t, "fill", 50)
	items := []tradeObservation{}
	for i := range orders {
		items = append(items, tradeObservation{fmt.Sprint("o", i), "order", orders[i]}, tradeObservation{fmt.Sprint("f", i), "fill", fills[i]})
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = observeTradesTx(ctx, tx, tradeScope(), items); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE fact_changes(kind text);
        CREATE FUNCTION watch_facts() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO fact_changes VALUES(TG_TABLE_NAME); RETURN NEW; END $$;
        CREATE TRIGGER watch_orders AFTER INSERT OR UPDATE ON futures_order_states FOR EACH ROW EXECUTE FUNCTION watch_facts();
        CREATE TRIGGER watch_fills AFTER INSERT OR UPDATE ON futures_execution_facts FOR EACH ROW EXECUTE FUNCTION watch_facts()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "same-order-new-source", "order", orders[0]); err != nil {
		t.Fatal(err)
	}
	if n := storedCount(t, db, "fact_changes"); n != 2 {
		t.Fatalf("rewrote unrelated rows: %d", n)
	}
	if _, err := s.Verify(ctx, tradeScope()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE fact_changes`); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Observe(ctx, tradeScope(), "same-order-new-source", "order", orders[0]); err != nil || !out.Replayed {
		t.Fatal(out, err)
	}
	if n := storedCount(t, db, "fact_changes"); n != 0 {
		t.Fatal("duplicate rewrote facts", n)
	}
}

func TestTradeCheckpointLegacyActivationAndRollback(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	if _, err := s.Observe(ctx, tradeScope(), "old-fill", "fill", tradeFixture(t, "fill")); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_trade_observations`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	seedLegacyTradeCheckpoint(t, db, ctx, 1)
	migrateFuturesTest(t, db, ctx, "000063_futures_trade_entities", "down")
	migrateFuturesTest(t, db, ctx, "000062_futures_trade_checkpoint", "down")
	migrateFuturesTest(t, db, ctx, "000062_futures_trade_checkpoint", "up")
	migrateFuturesTest(t, db, ctx, "000063_futures_trade_entities", "up")
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
        IF NEW.projection_version=2 THEN RAISE EXCEPTION 'synthetic checkpoint failure'; END IF; RETURN NEW; END $$;
        CREATE TRIGGER reject_checkpoint BEFORE UPDATE ON futures_trade_scopes FOR EACH ROW EXECUTE FUNCTION reject_checkpoint()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "order", "order", tradeFixture(t, "order")); err == nil {
		t.Fatal("checkpoint failure ignored")
	}
	var version int
	var dirty bool
	if err := db.QueryRowContext(ctx, `SELECT projection_version,projection_dirty FROM futures_trade_scopes`).Scan(&version, &dirty); err != nil || version != 0 || dirty {
		t.Fatal(version, dirty, err)
	}
	if storedCount(t, db, "futures_trade_observations") != 1 || storedCount(t, db, "futures_order_states") != 0 {
		t.Fatal("partial activation committed")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_checkpoint ON futures_trade_scopes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Observe(ctx, tradeScope(), "order", "order", tradeFixture(t, "order")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, tradeScope()); err != nil {
		t.Fatal(err)
	}
	var after []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_trade_observations WHERE source_id='old-fill'`).Scan(&after); err != nil || !bytes.Equal(after, original) {
		t.Fatal("legacy original changed", err)
	}
}

func TestTradeCheckpointCorruptionNeverFallsBack(t *testing.T) {
	for name, sql := range map[string]string{
		"raw":                `UPDATE futures_trade_observations SET body=body||' '::bytea`,
		"deleted":            `DELETE FROM futures_trade_observations`,
		"truncated":          `TRUNCATE futures_trade_observations`,
		"outside_append":     `INSERT INTO futures_trade_observations(scope_key,source_id,kind,body,body_sha256,source_conflict) SELECT scope_key,source_id||'-outside',kind,body,body_sha256,source_conflict FROM futures_trade_observations LIMIT 1`,
		"order":              `UPDATE futures_order_states SET blocked=true`,
		"fill":               `DELETE FROM futures_execution_facts`,
		"missing_checkpoint": `UPDATE futures_trade_scopes SET projection_state=NULL`,
		"damaged_checkpoint": `UPDATE futures_trade_scopes SET projection_state=projection_state||' '::bytea`,
		"count":              `UPDATE futures_trade_scopes SET audit_count=audit_count+1`,
		"report":             `UPDATE futures_trade_scopes SET summary=summary||' '::bytea`,
	} {
		t.Run(name, func(t *testing.T) {
			db, s, ctx := tradeTestDB(t)
			for _, kind := range []string{"order", "fill"} {
				if _, err := s.Observe(ctx, tradeScope(), kind, kind, tradeFixture(t, kind)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, sql); err != nil {
				t.Fatal(err)
			}
			before := storedCount(t, db, "futures_trade_observations")
			if _, err := s.Observe(ctx, tradeScope(), "new-order", "order", tradeFixture(t, "order")); !errors.Is(err, ErrTradeCheckpoint) {
				t.Fatal("silently recovered damaged state", err)
			}
			if storedCount(t, db, "futures_trade_observations") != before {
				t.Fatal("failed append committed")
			}
			if _, err := s.Verify(ctx, tradeScope()); err == nil {
				t.Fatal("verification accepted damage")
			}
			var dirty bool
			if err := db.QueryRowContext(ctx, `SELECT projection_dirty FROM futures_trade_scopes`).Scan(&dirty); err != nil {
				t.Fatal(err)
			}
			if dirty {
				if _, err := s.Read(ctx, tradeScope()); !errors.Is(err, ErrTradeCheckpoint) {
					t.Fatal("dirty ledger remained readable", err)
				}
			}
			raw, err := os.ReadFile("../../migrations/postgres/000063_futures_trade_entities.down.sql")
			if err != nil {
				t.Fatal(err)
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.ExecContext(ctx, string(raw))
			tx.Rollback()
			if err == nil {
				t.Fatal("downgrade hid damage")
			}
		})
	}
}
