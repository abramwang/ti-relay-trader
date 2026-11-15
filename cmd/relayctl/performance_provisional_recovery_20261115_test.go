package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/trading"
)

func TestProvisionalPlanRoundTripCanonicalFingerprint(t *testing.T) {
	type evidence struct {
		Z uint64 `json:"z"`
		A string `json:"a"`
	}
	p := provisionalRecoveryPlan{Assets: []performance.ReconstructionAssetDay{{Evidence: map[string]any{"typed": evidence{Z: 18446744073709551615, A: "raw"}}}}}
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := writeETFTransferReviewFile(path, p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var read provisionalRecoveryPlan
	if err := readProvisionalPlan(path, fmt.Sprintf("%x", sha256.Sum256(b)), &read); err != nil {
		t.Fatal(err)
	}
	if reconstructionSHA(p) != reconstructionSHA(read) {
		t.Fatal("typed/map ordering or numeric precision changed during review")
	}
	a := ledger.PerformanceNAV{PnLComponents: p.Assets[0].Evidence}
	bnav := ledger.PerformanceNAV{PnLComponents: read.Assets[0].Evidence}
	if !sameProvisionalNAV(a, bnav) {
		t.Fatal("typed/map readback differs")
	}
}

type provisionalOrders []trading.Order

func (o provisionalOrders) ListOrders(context.Context, trading.OrderQuery) ([]trading.Order, error) {
	return o, nil
}

func TestProvisionalCashSourceRequiresSameETFAndPCF(t *testing.T) {
	var m fuyingCashMatrix
	if err := json.Unmarshal([]byte(`{"unresolved_cash":[{"broker_flow_id":"f","raw_fields":{"股票代码":"510310","交易日期":"20261014"}}],"cash_pcf_review":{"unresolved_candidates":[{"flow_ids":["f"],"pcf_order_multiset_candidates":[{"source_trade_date":"20261012","security_id":"510310.SH","gateway_order_ids":["o"]}]}]}}`), &m); err != nil {
		t.Fatal(err)
	}
	o := provisionalOrders{{TradeSide: "R", CumFilledQty: 900000, TradeDate: "2026-10-12", GatewayOrderID: "o"}}
	if err := validateProvisionalCashSources(context.Background(), o, m); err != nil {
		t.Fatal(err)
	}
	if err := validateProvisionalCashSources(context.Background(), append(o, trading.Order{TradeSide: "R", CumFilledQty: 900000, TradeDate: "2026-10-13", GatewayOrderID: "new"}), m); err != nil {
		t.Fatal("newer unrelated redemption must not replace unique PCF match", err)
	}
	if err := validateProvisionalCashSources(context.Background(), provisionalOrders{{TradeSide: "R", CumFilledQty: 900000, TradeDate: "2026-10-13", GatewayOrderID: "new"}}, m); err == nil {
		t.Fatal("missing matched source accepted")
	}
	m.Unresolved[0].Raw["股票代码"] = "516630"
	if err := validateProvisionalCashSources(context.Background(), o, m); err == nil {
		t.Fatal("cross-ETF association accepted")
	}
}

func TestProvisionalRecoveryRequiresBackupAndPlan(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--apply", "--config", "config/relay.prod.yaml", "--output", "x"}} {
		if runProvisionalRecovery(args) == nil {
			t.Fatal("unsafe invocation accepted")
		}
	}
}

func TestFuyingResearchProvisionalRecoveryPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_RESEARCH_COST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	all, _, err := loadResearchMigration()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbmigrations.NewRunner(db).Up(ctx, all[:32]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts(account_id,broker_id) VALUES($1,'huaxin')`, fuying13Account); err != nil {
		t.Fatal(err)
	}
	repo := ledger.NewRepository(db)
	p := provisionalRecoveryPlan{Account: fuying13Account, Authorization: map[string]any{"approved": true, "scope": "provisional_only"}}
	for _, date := range []string{"2026-10-09", "2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15", "2026-10-16", "2026-10-19"} {
		_, err := repo.UpsertPerformanceNAV(ctx, ledger.PerformanceNAV{AccountID: fuying13Account, TradeDate: date, Status: "provisional", FormulaVersion: fuyingContinuousFormula, OpenEconomicNAV: 1000, CloseEconomicNAV: 1000, CumulativeNAV: 1, ReturnDenominator: 1000, Source: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
	}
	p.Before, err = repo.ListPerformanceNAVs(ctx, p.Account, "2026-10-09", "2026-11-12")
	if err != nil {
		t.Fatal(err)
	}
	p.Anchor = p.Before[0]
	start, _ := time.Parse("2006-01-02", "2026-10-12")
	for d := start; d.Format("2006-01-02") <= "2026-11-12"; d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		date := d.Format("2006-01-02")
		p.Dates = append(p.Dates, date)
		p.Assets = append(p.Assets, performance.ReconstructionAssetDay{Date: date, ReportedClose: 1001 + float64(len(p.Dates)), ObservedOpen: 1000, Source: "fixture", InputSHA: strings.Repeat("a", 64)})
	}
	p.NAVs, err = performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	begin := func(ro bool) *sql.Tx {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: ro})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx
	}
	tx := begin(true)
	if _, err := insertProvisionalRecovery(ctx, tx, p, strings.Repeat("a", 64)); err == nil {
		t.Fatal("read only write")
	}
	tx.Rollback()
	tx = begin(false)
	if count, err := insertProvisionalRecovery(ctx, tx, p, strings.Repeat("a", 64)); err != nil || count != 24 {
		t.Fatalf("write: %d %v", count, err)
	}
	tx.Rollback()
	current, err := repo.ListPerformanceNAVs(ctx, p.Account, "2026-10-09", "2026-11-12")
	if err != nil || reconstructionSHA(current) != reconstructionSHA(p.Before) {
		t.Fatal("rollback changed old NAV")
	}
	tx = begin(false)
	if _, err := insertProvisionalRecovery(ctx, tx, p, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	current, err = repo.ListPerformanceNAVs(ctx, p.Account, "2026-10-09", "2026-11-12")
	if err != nil || len(current) != 25 {
		t.Fatal("incomplete current chain", len(current), err)
	}
	var total, retired int
	if err := db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE NOT is_current) FROM performance_nav_versions`).Scan(&total, &retired); err != nil || total != 31 || retired != 6 {
		t.Fatal("history not retained", total, retired, err)
	}
	tx = begin(false)
	if _, err := insertProvisionalRecovery(ctx, tx, p, strings.Repeat("a", 64)); err == nil {
		t.Fatal("duplicate replacement accepted")
	}
	tx.Rollback()
	for _, n := range current[1:] {
		if n.Status != "provisional" || n.PnLComponents["account_ledger_verified"] != false {
			t.Fatal("false finality")
		}
	}
	// A new broker revision retains every preceding version and appends Nov 13.
	p.Before = current
	p.Dates = append(p.Dates, "2026-11-13")
	d, err := refreshedBrokerAsset("2026-11-13", performance.ReconstructionAssetDay{}, refreshFixture(), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	p.Assets = append(p.Assets, d)
	p.NAVs, err = performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	tx = begin(false)
	if count, err := insertProvisionalRecovery(ctx, tx, p, strings.Repeat("b", 64)); err != nil || count != 25 {
		t.Fatal(count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListPerformanceNAVs(ctx, p.Account, "2026-10-12", "2026-11-13")
	if err != nil || len(rows) != 25 {
		t.Fatal(len(rows), err)
	}
	for i, n := range rows {
		expected := p.NAVs[i]
		expected.Source = "relay.provisional_reconstruction:" + strings.Repeat("b", 64)
		if !sameProvisionalNAV(n, expected) {
			t.Fatal("revision readback differs", i)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE NOT is_current) FROM performance_nav_versions`).Scan(&total, &retired); err != nil || total != 56 || retired != 30 {
		t.Fatal("revision history lost", total, retired, err)
	}
}
