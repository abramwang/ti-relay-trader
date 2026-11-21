package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// Test-only construction of the previous writer's exact checkpoint. This is
// not a production downgrade/recovery entry point and never deletes evidence.
func seedLegacyTradeCheckpoint(t *testing.T, db *sql.DB, ctx context.Context, version int) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count, size int
	if err := tx.QueryRowContext(ctx, `SELECT audit_count,audit_bytes FROM futures_trade_scopes FOR UPDATE`).Scan(&count, &size); err != nil {
		t.Fatal(err)
	}
	p, err := replayTradeEvidence(ctx, tx, tradeScope(), count, size)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyTradeFactRows(ctx, tx, tradeScope(), p); err != nil {
		t.Fatal(err)
	}
	report, _ := json.Marshal(p.report)
	rh := sha256.Sum256(report)
	var state, digest []byte
	if version == 1 {
		state, _ = p.checkpoint(count, size, report)
		sh := sha256.Sum256(state)
		digest = sh[:]
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM futures_trade_aliases`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_order_states SET native_alias='',record_sha256=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_execution_facts SET native_alias='',record_sha256=NULL,eligible_gateway=''`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_trade_scopes SET projection_version=$1,projection_state=$2,projection_sha256=$3,report=$4,report_sha256=$5,summary=NULL,summary_sha256=NULL,projection_dirty=false`, version, state, digest, report, rh[:]); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestTradeEntitiesLegacyActivation(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db, s, ctx := tradeTestDB(t)
			if _, err := s.Observe(ctx, tradeScope(), "fill", "fill", tradeFixture(t, "fill")); err != nil {
				t.Fatal(err)
			}
			seedLegacyTradeCheckpoint(t, db, ctx, version)
			if _, err := s.Verify(ctx, tradeScope()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Observe(ctx, tradeScope(), "order", "order", tradeFixture(t, "order")); err != nil {
				t.Fatal(err)
			}
			if r, err := s.Verify(ctx, tradeScope()); err != nil || r.PendingCount != 0 {
				t.Fatal(r, err)
			}
			var v int
			if err := db.QueryRowContext(ctx, `SELECT projection_version FROM futures_trade_scopes`).Scan(&v); err != nil || v != 2 {
				t.Fatal(v, err)
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
				t.Fatal("downgrade discarded active entity projection")
			}
		})
	}
}

func TestTradeEntitiesAffectedGroupsAndCompactHeader(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	orders, fills := capacityRows(t, "order", 1000), capacityRows(t, "fill", 1000)
	items := make([]tradeObservation, 0, 2000)
	for i := range orders {
		items = append(items, tradeObservation{fmt.Sprint("o", i), "order", orders[i]}, tradeObservation{fmt.Sprint("f", i), "fill", fills[i]})
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observeTradesTx(ctx, tx, tradeScope(), items); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var headerRaw, state []byte
	if err := db.QueryRowContext(ctx, `SELECT summary,projection_state FROM futures_trade_scopes`).Scan(&headerRaw, &state); err != nil {
		t.Fatal(err)
	}
	var header TradeReport
	if json.Unmarshal(headerRaw, &header) != nil || len(header.Orders)+len(header.Executions) != 0 || len(headerRaw) > 1024 || len(state) > 512 {
		t.Fatal("whole-day facts remain in header", len(headerRaw), len(state))
	}
	var legacyReport, legacyHash []byte
	if err := db.QueryRowContext(ctx, `SELECT report,report_sha256 FROM futures_trade_scopes`).Scan(&legacyReport, &legacyHash); err != nil {
		t.Fatal(err)
	}
	if _, err := localReportConflict(legacyReport, legacyHash); err == nil {
		t.Fatal("old reader accepted a partial ledger")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	a, o, f, err := affectedTradeAliases(ctx, tx, tradeScope(), []tradeObservation{{"update", "order", orders[4]}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := loadTradeEntities(ctx, tx, header, a, o, f, false)
	if err != nil || len(p.report.Orders) != 1 || len(p.report.Executions) != 1 || len(entityAliases(p)) != 1 {
		t.Fatal("loaded unrelated daily facts", p, err)
	}
	tx.Rollback()
	if _, err := s.Observe(ctx, tradeScope(), "update", "order", orders[4]); err != nil {
		t.Fatal(err)
	}
	r, err := s.Verify(ctx, tradeScope())
	if err != nil || len(r.Orders) != 1000 || len(r.Executions) != 1000 {
		t.Fatal("full read lost unrelated facts", err)
	}
}

func TestTradeEntitiesRandomPartitionEquivalence(t *testing.T) {
	_, s, ctx := tradeTestDB(t)
	orders, fills := capacityRows(t, "order", 8), capacityRows(t, "fill", 8)
	items := []tradeObservation{}
	for i := range orders {
		orders[i] = editBody(t, orders[i], func(m map[string]any) { m["gateway_order_id"] = fmt.Sprint("local-", i) })
		fills[i] = editBody(t, fills[i], func(m map[string]any) { m["gateway_order_id"] = fmt.Sprint("local-", i) })
		items = append(items, tradeObservation{fmt.Sprint("o", i), "order", orders[i]}, tradeObservation{fmt.Sprint("f", i), "fill", fills[i]},
			tradeObservation{fmt.Sprint("fee", i), "fill", editBody(t, fills[i], func(m map[string]any) { m["fee"] = "0.12345678901234" })})
	}
	items = append(items,
		tradeObservation{"collision", "order", editBody(t, orders[0], func(m map[string]any) { m["gateway_order_id"] = "different-owner" })},
		tradeObservation{"sparse", "order", editBody(t, orders[1], func(m map[string]any) { delete(m["native_ids"].(map[string]any), "order_sys_id") })},
		tradeObservation{"sys-conflict", "order", editBody(t, orders[2], func(m map[string]any) { m["native_ids"].(map[string]any)["order_sys_id"] = "99999" })},
		tradeObservation{"fill-conflict", "fill", editBody(t, fills[3], func(m map[string]any) { m["native_ids"].(map[string]any)["order_sys_id"] = "99998" })},
		tradeObservation{"overfill", "fill", editBody(t, fills[4], func(m map[string]any) {
			m["native_ids"].(map[string]any)["trade_id"] = "00009001"
			m["fill_id"] = nativeIdentity("fill", tradeScope(), "SHFE", "00009001")
		})})
	for _, item := range items {
		if item.kind == "order" {
			if _, err := DecodeOrder(item.raw, tradeScope()); err != nil {
				t.Fatal(item.sourceID, err)
			}
		} else if _, err := DecodeFill(item.raw, tradeScope()); err != nil {
			t.Fatal(item.sourceID, err)
		}
	}
	rng := rand.New(rand.NewSource(300))
	for _, n := range rng.Perm(len(items)) {
		i := items[n]
		if _, err := s.Observe(ctx, tradeScope(), i.sourceID, i.kind, i.raw); err != nil {
			t.Fatal(i.sourceID, err)
		}
		if _, err := s.Verify(ctx, tradeScope()); err != nil {
			t.Fatal(i.sourceID, err)
		}
	}
	before, err := s.Read(ctx, tradeScope())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range items {
		if out, err := s.Observe(ctx, tradeScope(), i.sourceID, i.kind, i.raw); err != nil || !out.Replayed {
			t.Fatal(i.sourceID, out, err)
		}
	}
	a, _ := json.Marshal(before)
	after, err := s.Verify(ctx, tradeScope())
	b, _ := json.Marshal(after)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("duplicate changed report", err)
	}
}

func TestTradeEntitiesDamageBlocksUnrelatedUpdates(t *testing.T) {
	for _, q := range []string{
		`DELETE FROM futures_trade_aliases`,
		`TRUNCATE futures_trade_aliases`,
		`UPDATE futures_trade_aliases SET record=record||'{"Total":999}'::jsonb`,
		`UPDATE futures_order_states SET native_alias='different'`,
		`UPDATE futures_execution_facts SET eligible_gateway='different'`,
	} {
		t.Run(q, func(t *testing.T) {
			db, s, ctx := tradeTestDB(t)
			for _, kind := range []string{"order", "fill"} {
				if _, err := s.Observe(ctx, tradeScope(), kind, kind, tradeFixture(t, kind)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Observe(ctx, tradeScope(), "unrelated", "order", capacityRows(t, "order", 2)[1]); !errors.Is(err, ErrTradeCheckpoint) {
				t.Fatal(err)
			}
			if _, err := s.Read(ctx, tradeScope()); !errors.Is(err, ErrTradeCheckpoint) {
				t.Fatal(err)
			}
			if _, err := s.Verify(ctx, tradeScope()); !errors.Is(err, ErrTradeCheckpoint) {
				t.Fatal(err)
			}
		})
	}
}

func TestTradeEntitiesConcurrentReportSnapshot(t *testing.T) {
	db, s, ctx := tradeTestDB(t)
	orders, fills := capacityRows(t, "order", 24), capacityRows(t, "fill", 24)
	appendPair := func(i int) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = observeTradesTx(ctx, tx, tradeScope(), []tradeObservation{{fmt.Sprint("o", i), "order", orders[i]}, {fmt.Sprint("f", i), "fill", fills[i]}})
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := appendPair(0); err != nil {
		t.Fatal(err)
	}
	readResult := make(chan error, 1)
	go func() {
		for i := 0; i < 40; i++ {
			r, err := s.Read(ctx, tradeScope())
			if err != nil {
				readResult <- err
				return
			}
			if r.Blocked || r.PendingCount != 0 || len(r.Orders) != len(r.Executions) {
				readResult <- fmt.Errorf("mixed report snapshots: %d orders, %d fills", len(r.Orders), len(r.Executions))
				return
			}
		}
		readResult <- nil
	}()
	var writeErr error
	for i := 1; i < len(orders); i++ {
		if writeErr = appendPair(i); writeErr != nil {
			break
		}
	}
	readErr := <-readResult
	if writeErr != nil || readErr != nil {
		t.Fatal(writeErr, readErr)
	}
	if _, err := s.Verify(ctx, tradeScope()); err != nil {
		t.Fatal(err)
	}
}
