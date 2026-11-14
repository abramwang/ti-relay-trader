package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/config"
	dbmigrations "ti-relay-trader/internal/db/migrations"
)

func TestResearchBookCoveragePostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_BOOK_COVERAGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable socket-only PostgreSQL")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(filepath.Clean(cfg.Host), "/tmp/relay-book-coverage-pg.") || cfg.User != "relay_book_test" {
		t.Fatal("not an isolated book coverage instance")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='public'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("requires empty disposable instance", err)
	}
	all, err := dbmigrations.LoadDir("../../migrations/postgres")
	if err != nil {
		t.Fatal(err)
	}
	var base []dbmigrations.Migration
	for _, m := range all {
		if m.Version <= 32 {
			base = append(base, m)
		}
	}
	if _, err := dbmigrations.NewRunner(db).Up(ctx, base); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO accounts(account_id,broker_id) VALUES('a','huaxin'),('other','huaxin')`,
		`INSERT INTO orders(account_id,trade_date,gateway_order_id,symbol,exchange,trade_side,business_type,limit_price,order_qty) SELECT 'a','2026-10-12','g'||i,'600000','SH','B','S',10,100 FROM generate_series(1,501) i`,
		`INSERT INTO orders(account_id,trade_date,gateway_order_id,symbol,exchange,trade_side,business_type,limit_price,order_qty) VALUES('other','2026-10-12','other','600000','SH','B','S',10,100),('a','2026-10-13','next','600000','SH','B','S',10,100)`,
		`INSERT INTO fills(account_id,trade_date,gateway_order_id,fill_id,symbol,exchange,trade_side,business_type,price,qty,matched_at) SELECT 'a','2026-10-12','g'||i,'f'||i,'600000','SH','B','S',10,100,'2026-10-12T10:00:00+08:00' FROM generate_series(1,501) i`,
		`INSERT INTO etf_component_transfers(account_id,trade_date,gateway_order_id,fill_id,symbol,exchange,trade_side,business_type,qty,component_symbol,component_exchange,component_qty) VALUES('a','2026-10-12','g1','t','600000','SH','R','E',100,'600000','SH',100)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	raw, diff, _ := researchEventFixture(t, "fills")
	for _, scope := range []struct{ account, day, streamID string }{{"a", "2026-10-12T10:00:02+08:00", "1-0"}, {"other", "2026-10-12T10:00:02+08:00", "2-0"}, {"a", "2026-10-13T10:00:02+08:00", "3-0"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,gateway_order_id,body,received_at) VALUES($1,$2,'in','event',$3,'g1',$4,$5)`, raw.Stream, scope.streamID, scope.account, string(raw.Body), scope.day); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	day := researchQueryDay{Date: "2026-10-12", Attempts: []researchQueryAttempt{{Queries: []researchQueryProof{{Step: "fills", BookCoverage: &researchBookCoverage{Rows: researchBookComparison{Differences: []researchBookDifference{diff}}}}}}}}
	if err := attachResearchEventEvidence(ctx, tx, "production", config.AccountRouteConfig{AccountID: "a", BrokerID: "huaxin", GatewayID: "g"}, &day); err != nil {
		t.Fatal(err)
	}
	proof := day.Attempts[0].Queries[0].BookCoverage.Rows.Differences[0].EventEvidence
	if proof == nil || proof.State != "matching_full_event_found" || proof.Candidates != 1 || proof.Matches[0].StreamID != "1-0" {
		t.Fatalf("event SQL scope: %+v", proof)
	}
	first, err := readResearchDailyBooks(ctx, tx, "a", "2026-10-12")
	if err != nil || len(first.Orders) != 501 || len(first.Fills) != 501 || len(first.Transfers) != 1 {
		t.Fatal("pagination/scope", err, len(first.Orders), len(first.Fills), len(first.Transfers))
	}
	if _, err := db.ExecContext(ctx, `UPDATE fills SET price=11 WHERE account_id='a' AND fill_id='f1'`); err != nil {
		t.Fatal(err)
	}
	again, err := readResearchDailyBooks(ctx, tx, "a", "2026-10-12")
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("read snapshot changed", err)
	}
	c := compareResearchBooks(nil, first.Fills)
	if err := attachResearchBookEvidence(ctx, tx, "a", "2026-10-12", "fills", &c); err != nil {
		t.Fatal(err)
	}
	for _, d := range c.Differences {
		if len(d.BookEvidence) == 0 {
			t.Fatal("missing provenance")
		}
	}
	if err := attachResearchBookEvidence(ctx, tx, "a", "2026-10-12", "orders; DROP TABLE fills", &c); err == nil {
		t.Fatal("unsafe table")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE fills SET price=1`); err == nil {
		t.Fatal("read-only guard")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var price float64
	if err := db.QueryRowContext(ctx, `SELECT price FROM fills WHERE account_id='a' AND fill_id='f1'`).Scan(&price); err != nil || price != 11 {
		t.Fatal("reader mutated ledger", price, err)
	}
}
