package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestFuyingResearchSourceArgumentGuards(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--config", "missing", "--account", "a", "--version", " v", "--output", "x"}, {"--config", "missing", "--account", "a", "--version", "v", "--output", "x", "extra"}} {
		if err := runResearchSourceCheck(args); err == nil {
			t.Fatal("unsafe args accepted")
		}
	}
}

// Called only by the frozen import test in its disposable PG instance.
func checkResearchSourcePostgres(t *testing.T, db *sql.DB, b performance.ResearchCostBundle, v ledger.ResearchCostVersion) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	scope := performance.ResearchSourceObservation{Environment: "test", Database: database, AccountID: b.AccountID, VersionID: b.VersionID, PayloadSHA256: v.PayloadSHA256, DateFrom: b.AnchorDate, DateTo: b.Dates[len(b.Dates)-1]}
	begin := func(ro bool, isolation sql.IsolationLevel) *sql.Tx {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: ro, Isolation: isolation})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx
	}
	for _, options := range []struct {
		ro        bool
		isolation sql.IsolationLevel
	}{{false, sql.LevelRepeatableRead}, {true, sql.LevelReadCommitted}} {
		tx := begin(options.ro, options.isolation)
		if _, err := performance.ReadResearchSourceObservation(ctx, tx, scope); err == nil {
			t.Fatal("unsafe transaction accepted")
		}
		tx.Rollback()
	}
	tx := begin(true, sql.LevelRepeatableRead)
	c, err := performance.ReadResearchCostComparison(ctx, tx, b.AccountID, b.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := performance.ReadResearchSourceObservation(ctx, tx, scope)
	if err != nil {
		t.Fatal(err)
	}
	before, err := fuyingResearchFingerprints(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	// The historical source rows are initially empty; empty is not proof of
	// a complete broker response or evidence that the product was flat.
	for _, d := range first.Days {
		if d.Rows != 0 {
			t.Fatal("unexpected fixture source", d)
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO position_snapshots(account_id,trade_date,symbol,exchange,quantity) VALUES($1,$2,'510310','SH',100)`, b.AccountID, b.Dates[0])
	if err != nil {
		t.Fatal(err)
	}
	again, err := performance.ReadResearchSourceObservation(ctx, tx, scope)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("snapshot isolation lost", err)
	}
	after, err := fuyingResearchFingerprints(ctx, tx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("reader changed financial state", err)
	}
	tx.Rollback()
	baseline, err := performance.ReviewResearchSources(first, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	read := func() performance.ResearchSourceObservation {
		tx := begin(true, sql.LevelRepeatableRead)
		defer tx.Rollback()
		o, e := performance.ReadResearchSourceObservation(ctx, tx, scope)
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	next := read()
	report, err := performance.ReviewResearchSources(next, &baseline, c)
	if err != nil || len(report.Changes) != 1 || report.Changes[0].Table != "position_snapshots" || report.RecheckFrom != b.Dates[0] || report.CostPublishable {
		t.Fatal(report.Summary(), err)
	}
	path := filepath.Join(t.TempDir(), "observation.json")
	if err := writeETFTransferReviewFile(path, report); err != nil {
		t.Fatal(err)
	}
	loaded, err := readResearchSourceBaseline(path)
	if err != nil || loaded.ObservationSHA256 != report.ObservationSHA256 {
		t.Fatal("baseline read", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private evidence", err)
	}
	if err := writeETFTransferReviewFile(path, report); err == nil {
		t.Fatal("overwrote observation")
	}
	_, err = db.ExecContext(ctx, `UPDATE position_snapshots SET avg_cost=0.000001 WHERE account_id=$1`, b.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := performance.ReviewResearchSources(read(), loaded, c)
	if err != nil || len(revised.Changes) != 1 || revised.Changes[0].BeforeRows != revised.Changes[0].AfterRows {
		t.Fatal("same-count mutation missed", err)
	}
	_, err = db.ExecContext(ctx, `DELETE FROM position_snapshots WHERE account_id=$1`, b.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := performance.ReviewResearchSources(read(), &revised, c)
	if err != nil || len(deleted.Changes) != 1 || deleted.Changes[0].AfterRows != 0 {
		t.Fatal("deletion missed", err)
	}
	badScope := scope
	badScope.Database = "wrong-database"
	tx = begin(true, sql.LevelRepeatableRead)
	if _, err := performance.ReadResearchSourceObservation(ctx, tx, badScope); err == nil {
		t.Fatal("wrong database accepted")
	}
	tx.Rollback()
}
