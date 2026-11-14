package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestFuyingResearchBackupManifest(t *testing.T) {
	now := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	fixture := func() map[string]any {
		return map[string]any{
			"format":            "relay.postgres.backup.v1",
			"archive":           map[string]any{"file": "backup.dump", "sha256": hash, "bytes": 2048},
			"database_snapshot": map[string]any{"database": "relay_trader", "captured_at": "2026-11-14T09:50:00.123456"},
		}
	}
	raw, err := json.Marshal(fixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateResearchBackupManifest(raw, "/private/backup.dump", hash, 2048, now); err != nil {
		t.Fatal("Shanghai timestamp", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"format":           func(m map[string]any) { m["format"] = "other" },
		"account database": func(m map[string]any) { m["database_snapshot"].(map[string]any)["database"] = "relay_test" },
		"old":              func(m map[string]any) { m["database_snapshot"].(map[string]any)["captured_at"] = "2026-11-14T07:59:59" },
		"future":           func(m map[string]any) { m["database_snapshot"].(map[string]any)["captured_at"] = "2026-11-14T10:02:00" },
		"missing time":     func(m map[string]any) { delete(m["database_snapshot"].(map[string]any), "captured_at") },
		"size":             func(m map[string]any) { m["archive"].(map[string]any)["bytes"] = 2049 },
		"filename":         func(m map[string]any) { m["archive"].(map[string]any)["file"] = "different.dump" },
		"hash":             func(m map[string]any) { m["archive"].(map[string]any)["sha256"] = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			m := fixture()
			mutate(m)
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateResearchBackupManifest(raw, "/private/backup.dump", hash, 2048, now); err == nil {
				t.Fatal("invalid backup manifest accepted")
			}
		})
	}
}

func TestFuyingResearchImportArgumentGuards(t *testing.T) {
	for _, args := range [][]string{nil, {"--apply"}, {"--config", "missing", "--output", "unused", "--allow-research-migration"}, {"--config", "missing", "--output", "unused", "--apply", "--expected-payload-sha256", "wrong"}} {
		if err := runFuyingResearchImport(args); err == nil {
			t.Fatal("unsafe arguments accepted", args)
		}
	}
}

func TestFuyingResearchEvidenceComparison(t *testing.T) {
	opening := []ledger.PositionCostState{{AccountID: fuying13Account, Symbol: "510310", Exchange: "SH", CostBucket: "CORE", CloseQuantity: 1, CloseTotalCost: 2, CalculatedAt: time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)}}
	audits := []ledger.T0Allocation{{LinkID: "link", AccountID: fuying13Account, TradeDate: "2026-10-12", GatewayOrderID: "order", SecurityID: "510310.SH", TradeSide: "B", Quantity: 100, GroupID: "group", Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation", EvidenceRef: "test", ConfirmedBy: "user", ConfirmedDate: "2026-11-11"}}
	b := performance.ResearchCostBundle{Opening: opening, Audits: audits}
	copyOpening := append([]ledger.PositionCostState(nil), opening...)
	copyOpening[0].CalculatedAt = copyOpening[0].CalculatedAt.In(time.FixedZone("Asia/Shanghai", 8*3600))
	if err := compareFuyingResearchEvidence(b, copyOpening, audits); err != nil {
		t.Fatal("same instant", err)
	}
	copyOpening[0].CloseTotalCost++
	if err := compareFuyingResearchEvidence(b, copyOpening, audits); err == nil {
		t.Fatal("changed anchor")
	}
	for _, kind := range []string{"quantity", "group", "date precision", "new audit", "missing audit", "revision"} {
		t.Run(kind, func(t *testing.T) {
			items := append([]ledger.T0Allocation(nil), audits...)
			switch kind {
			case "quantity":
				items[0].Quantity++
			case "group":
				items[0].GroupID = "other"
			case "date precision":
				items[0].ConfirmedDate = ""
				items[0].ConfirmedAt = time.Now()
			case "new audit":
				a := items[0]
				a.LinkID = "new"
				items = append(items, a)
			case "missing audit":
				items = nil
			case "revision":
				items[0].Revision = 2
				items[0].Supersedes = "prior"
			}
			if err := compareFuyingResearchEvidence(b, opening, items); err == nil {
				t.Fatal("changed full audit accepted")
			}
		})
	}
}

func TestFuyingResearchImportPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_RESEARCH_COST_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("RELAY_RESEARCH_COST_FROZEN_REPLAY") != "1" {
		t.Skip("requires disposable PG and private frozen evidence")
	}
	// Match the operator's repository-root invocation, with no service config.
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	b, v, err := loadFuyingResearchBundle()
	if err != nil {
		t.Fatal(err)
	}
	all, m, err := loadResearchMigration()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	// The preceding storage test only leaves its accounts fixture in this
	// disposable cluster. Recreate a real 1..32 base for the import tests.
	if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	base := append([]dbmigrations.Migration(nil), all[:32]...)
	if _, err := dbmigrations.NewRunner(db).Up(ctx, base); err != nil {
		t.Fatal(err)
	}
	reader := &performance.ResearchCostReader{DB: db}
	if _, err := reader.ListVersions(ctx, b.AccountID); !errors.Is(err, performance.ErrResearchCostUnavailable) {
		t.Fatal("missing research schema must not migrate", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts(account_id,broker_id) VALUES($1,'huaxin')`, fuying13Account); err != nil {
		t.Fatal(err)
	}
	repo := ledger.NewRepository(db)
	for _, p := range b.Opening {
		if _, err := repo.UpsertPositionCostState(ctx, p); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE performance_position_cost_states SET created_at=$1,updated_at=$2 WHERE account_id=$3 AND trade_date=$4 AND symbol=$5 AND exchange=$6 AND cost_bucket=$7`, p.CreatedAt, p.UpdatedAt, p.AccountID, p.TradeDate, p.Symbol, p.Exchange, p.CostBucket); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range b.Audits {
		if _, err := repo.AppendT0Allocation(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	begin := func(readOnly bool) *sql.Tx {
		t.Helper()
		iso := sql.LevelReadCommitted
		if readOnly {
			iso = sql.LevelRepeatableRead
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly, Isolation: iso})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx
	}
	tx := begin(true)
	pre, err := prepareFuyingResearchImport(ctx, tx, b, v, all, m, false, false)
	tx.Rollback()
	if err != nil || !pre.MigrationNeeded || pre.VersionExists || pre.PreparedNewVersions != 0 || !reflect.DeepEqual(pre.MigrationsBefore, pre.MigrationsAfter) {
		t.Fatal("read-only preflight", err)
	}
	tx = begin(false)
	if _, err := prepareFuyingResearchImport(ctx, tx, b, v, all, m, true, false); err == nil {
		t.Fatal("migration without explicit flag")
	}
	tx.Rollback()
	tx = begin(false)
	prepared, err := prepareFuyingResearchImport(ctx, tx, b, v, all, m, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.PreparedNewVersions != 1 || len(prepared.MigrationsAfter) != 33 || prepared.MigrationsAfter[32] != 42 {
		t.Fatal("wrong prepared plan", prepared)
	}
	// Both schema registry and all archived days must disappear on abort.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx = begin(true)
	versions, present, err := researchMigrationState(ctx, tx, all)
	tx.Rollback()
	if err != nil || present || len(versions) != 32 {
		t.Fatal("atomic schema+data rollback", err)
	}
	for _, failure := range []string{"stale audit", "changed anchor", "unexpected migration", "DDL modified"} {
		t.Run(failure, func(t *testing.T) {
			tx := begin(false)
			defer tx.Rollback()
			migration := m
			switch failure {
			case "stale audit":
				a := b.Audits[0]
				a.Supersedes = a.LinkID
				a.LinkID = "new-revision"
				a.Revision++
				a.GroupID = "changed"
				if _, err := ledger.NewRepository(tx).AppendT0Allocation(ctx, a); err != nil {
					t.Fatal(err)
				}
			case "changed anchor":
				if _, err := tx.ExecContext(ctx, `UPDATE performance_position_cost_states SET close_total_cost=close_total_cost+1`); err != nil {
					t.Fatal(err)
				}
			case "unexpected migration":
				if _, err := tx.ExecContext(ctx, `INSERT INTO relay_schema_migrations(version,name) VALUES(33,'futures_query_storage')`); err != nil {
					t.Fatal(err)
				}
			case "DDL modified":
				migration.UpSQL += "\n-- changed"
			}
			if _, err := prepareFuyingResearchImport(ctx, tx, b, v, all, migration, true, true); err == nil {
				t.Fatal("accepted changed precondition")
			}
		})
	}
	tx = begin(false)
	r, err := prepareFuyingResearchImport(ctx, tx, b, v, all, m, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db2, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	readTx, err := db2.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyFuyingResearchCommit(ctx, readTx, b, v, all, &r); err != nil {
		readTx.Rollback()
		t.Fatal(err)
	}
	readTx.Rollback()
	if !r.IndependentReadback || r.Phase != "committed_and_verified" || r.FinancialWrites != 0 || r.NAVPublishable {
		t.Fatal("false verification result")
	}
	tx = begin(false)
	replay, err := prepareFuyingResearchImport(ctx, tx, b, v, all, m, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.VersionExists || replay.PreparedNewVersions != 0 || replay.MigrationNeeded {
		t.Fatal("idempotent replay", replay)
	}
	tx.Rollback()
	var ctpTables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'futures_%'`).Scan(&ctpTables); err != nil || ctpTables != 0 {
		t.Fatal("CTP side effect", ctpTables, err)
	}
	// The ordinary runner still reports each gap independently after 42.
	status, err := dbmigrations.NewRunner(db).Status(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range status {
		if s.Version >= 33 && s.Version <= 41 && s.Applied {
			t.Fatal("gap marked applied", s)
		}
	}
	t.Run("source observation and change detection", func(t *testing.T) {
		checkResearchSourcePostgres(t, db, b, v)
	})
	t.Run("read-only comparison and stale evidence", func(t *testing.T) {
		versions, err := reader.ListVersions(ctx, b.AccountID)
		if err != nil || len(versions) != 1 || versions[0].VersionID != b.VersionID || versions[0].PayloadSHA256 != v.PayloadSHA256 {
			t.Fatal("version listing", versions, err)
		}
		if empty, err := reader.ListVersions(ctx, "other-account"); err != nil || len(empty) != 0 || empty == nil {
			t.Fatal("account-isolated listing", empty, err)
		}
		if _, err := reader.Compare(ctx, "other-account", b.VersionID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("cross-account comparison", err)
		}
		if _, err := reader.Compare(ctx, b.AccountID, ""); err == nil {
			t.Fatal("implicit version accepted")
		}
		webComparison, err := reader.Compare(ctx, b.AccountID, b.VersionID)
		if err != nil || webComparison.Counts.ResearchOnly != 972 || !webComparison.AnchorAndAllocationsMatch || webComparison.NAVPublishable {
			t.Fatal("read-only web comparison", webComparison.Counts, err)
		}
		tx := begin(false)
		if _, err := readResearchCostComparison(ctx, tx, b.AccountID, b.VersionID); err == nil {
			t.Fatal("write transaction accepted")
		}
		tx.Rollback()
		tx, err = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readResearchCostComparison(ctx, tx, b.AccountID, b.VersionID); err == nil {
			t.Fatal("non-snapshot transaction accepted")
		}
		tx.Rollback()
		tx = begin(true)
		before, err := fuyingResearchFingerprints(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		comparison, err := readResearchCostComparison(ctx, tx, b.AccountID, b.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		after, err := fuyingResearchFingerprints(ctx, tx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("comparison changed financial history", err)
		}
		tx.Rollback()
		if !comparison.AnchorAndAllocationsMatch || comparison.Counts.ResearchOnly != 972 || comparison.NAVPublishable || len(comparison.Days) != 22 {
			t.Fatal("missing stored baseline", comparison.Counts)
		}
		for _, day := range comparison.Days {
			if !day.StoredDayMissing {
				t.Fatal("invented stored day")
			}
			for _, row := range day.Rows {
				if row.Delta != nil || row.Stored != nil {
					t.Fatal("missing cost treated as zero")
				}
			}
		}
		p := b.Costs[0].Positions[0]
		if _, err := repo.UpsertPositionCostState(ctx, p); err != nil {
			t.Fatal(err)
		}
		a := b.Audits[0]
		a.Supersedes = a.LinkID
		a.LinkID = "comparison-revocation"
		a.Revision++
		a.Status = "revoked"
		if _, err := repo.AppendT0Allocation(ctx, a); err != nil {
			t.Fatal(err)
		}
		tx = begin(true)
		comparison, err = readResearchCostComparison(ctx, tx, b.AccountID, b.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		tx.Rollback()
		if comparison.AnchorAndAllocationsMatch || len(comparison.EvidenceChanges) != 1 || comparison.Counts.ResearchOnly != 971 || comparison.Days[0].StoredDayMissing || comparison.NAVPublishable {
			t.Fatal("stale version hidden", comparison.Counts, comparison.EvidenceChanges)
		}
		saved, err := repo.GetResearchCostVersion(ctx, b.AccountID, b.VersionID)
		if err != nil || saved.PayloadSHA256 != v.PayloadSHA256 {
			t.Fatal("comparison modified archive", err)
		}
	})
	if _, err := db.ExecContext(ctx, `ALTER TABLE performance_research_cost_versions DISABLE TRIGGER research_cost_version_immutable`); err != nil {
		t.Fatal(err)
	}
	tx = begin(true)
	if _, _, err := researchMigrationState(ctx, tx, all); err == nil {
		t.Fatal("disabled guard accepted")
	}
	tx.Rollback()
	t.Logf("verified frozen import: %d days, %d rows, migrations 1..32+42 only; rollback/readback/replay passed", b.Review.Days, b.Review.Rows)
}
