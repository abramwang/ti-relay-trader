package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/config"
	dbmigrations "ti-relay-trader/internal/db/migrations"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

const fuyingResearchVersion = "fuying13-cost-research-20261114-v1"
const fuyingResearchPayloadHash = "d565651ae60c6172b4838003a65ca5f5534c5207b80130eea90dbedd9f1e4169"
const fuyingResearchReviewPath = "outputs/recovery/fuying13_remaining_20261113/cost_continuity.json"
const fuyingResearchReviewHash = "56a8d0826101100302a6c3b74463e15ce39cd9d2902071094c886f378bda8f9d"
const researchMigrationHash = "3865626724c81ae629c923d7fa77798a6751c12d130dbe08e20458b444ff35b2"
const researchMigrationName = "research_cost_versions"

func loadFuyingResearchBundle() (performance.ResearchCostBundle, ledger.ResearchCostVersion, error) {
	var frozen struct {
		Costs []performance.CostLedgerResult `json:"costs"`
	}
	var review struct {
		Opening []ledger.PositionCostState       `json:"stored_opening_anchor"`
		Audits  []ledger.T0Allocation            `json:"active_attribution_audits"`
		Review  performance.CostContinuityReview `json:"review"`
		Missing []string                         `json:"unpersisted_attribution_link_ids"`
	}
	if err := readPinnedJSON(fuyingCostChainPath, fuyingCostChainHash, &frozen); err != nil {
		return performance.ResearchCostBundle{}, ledger.ResearchCostVersion{}, err
	}
	if err := readPinnedJSON(fuyingResearchReviewPath, fuyingResearchReviewHash, &review); err != nil {
		return performance.ResearchCostBundle{}, ledger.ResearchCostVersion{}, err
	}
	b := performance.ResearchCostBundle{AccountID: fuying13Account, VersionID: fuyingResearchVersion, AnchorDate: "2026-10-09", Opening: review.Opening, Costs: frozen.Costs, Audits: review.Audits, EvidenceHashes: map[string]string{"cost_chain": fuyingCostChainHash, "continuity_review": fuyingResearchReviewHash}}
	for _, day := range b.Costs {
		b.Dates = append(b.Dates, day.TradeDate)
	}
	v, err := performance.PrepareResearchCostVersion(b)
	if err != nil {
		return b, v, err
	}
	decoded, err := performance.DecodeResearchCostVersion(v)
	if err != nil || len(review.Missing) != 0 || !reflect.DeepEqual(decoded.Review, review.Review) || v.PayloadSHA256 != fuyingResearchPayloadHash {
		return b, v, errors.New("frozen research cost plan differs from verified version")
	}
	return decoded, v, nil
}

// Compare the full active revision payloads, not just IDs. Canonical time zones
// allow PG and JSON representations of the same instant to compare equally.
func compareFuyingResearchEvidence(b performance.ResearchCostBundle, opening []ledger.PositionCostState, audits []ledger.T0Allocation) error {
	normalizeOpening := func(items []ledger.PositionCostState) []ledger.PositionCostState {
		items = append([]ledger.PositionCostState(nil), items...)
		for i := range items {
			items[i].CalculatedAt = items[i].CalculatedAt.UTC()
			items[i].CreatedAt = items[i].CreatedAt.UTC()
			items[i].UpdatedAt = items[i].UpdatedAt.UTC()
		}
		sort.Slice(items, func(i, j int) bool {
			a, c := items[i], items[j]
			return a.Symbol+"|"+a.Exchange+"|"+a.CostBucket < c.Symbol+"|"+c.Exchange+"|"+c.CostBucket
		})
		return items
	}
	normalizeAudits := func(items []ledger.T0Allocation) ([]ledger.T0Allocation, error) {
		items = append([]ledger.T0Allocation(nil), items...)
		for i := range items {
			n, err := ledger.NormalizeT0Allocation(items[i])
			if err != nil {
				return nil, err
			}
			items[i] = n
		}
		sort.Slice(items, func(i, j int) bool { return items[i].LinkID < items[j].LinkID })
		return items, nil
	}
	equalJSON := func(a, b any) bool {
		x, e1 := json.Marshal(a)
		y, e2 := json.Marshal(b)
		return e1 == nil && e2 == nil && bytes.Equal(x, y)
	}
	a, err := normalizeAudits(b.Audits)
	if err != nil {
		return err
	}
	c, err := normalizeAudits(audits)
	if err != nil {
		return err
	}
	if !equalJSON(a, c) {
		return errors.New("active attribution revisions changed since frozen review")
	}
	if !equalJSON(normalizeOpening(b.Opening), normalizeOpening(opening)) {
		return errors.New("opening cost anchor changed since frozen review")
	}
	return nil
}

func checkFuyingResearchEvidence(ctx context.Context, tx *sql.Tx, b performance.ResearchCostBundle) error {
	repo := ledger.NewRepository(tx)
	opening, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: b.AccountID, BeforeDate: b.Dates[0]})
	if err != nil {
		return err
	}
	var active []ledger.T0Allocation
	for _, date := range b.Dates {
		history, err := repo.ListT0AllocationHistory(ctx, b.AccountID, date)
		if err != nil {
			return err
		}
		items, err := ledger.ResolveT0Allocations(history)
		if err != nil {
			return err
		}
		active = append(active, items...)
	}
	return compareFuyingResearchEvidence(b, opening, active)
}

func fuyingResearchFingerprints(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	result := map[string]string{}
	for _, table := range []string{"performance_nav_versions", "performance_position_cost_states", "asset_snapshots", "cash_ledger", "orders", "fills", "etf_component_transfers"} {
		var hash string
		err := tx.QueryRowContext(ctx, `SELECT md5(COALESCE(string_agg(h,'' ORDER BY h),'')) FROM
		(SELECT md5(to_jsonb(t)::text) h FROM `+table+` t WHERE account_id=$1 AND trade_date BETWEEN '2026-10-09'::date AND '2026-11-10'::date) s`, fuying13Account).Scan(&hash)
		if err != nil {
			return nil, err
		}
		result[table] = hash
	}
	return result, nil
}

// Allow the reviewed 1..32 base and independent research/cycle migrations 42/43.
// It neither advances a high-water mark nor marks the pending CTP gap applied.
func researchMigrationState(ctx context.Context, tx *sql.Tx, local []dbmigrations.Migration) ([]int64, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT version,name FROM public.relay_schema_migrations ORDER BY version`)
	if err != nil {
		return nil, false, err
	}
	var versions []int64
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			rows.Close()
			return nil, false, err
		}
		m, ok := dbmigrations.FindByVersion(local, version)
		if !ok || m.Name != name || (version > 32 && version != 42 && version != 43) {
			rows.Close()
			return nil, false, errors.New("unexpected migration set; review required")
		}
		versions = append(versions, version)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if len(versions) < 32 || len(versions) > 34 {
		return nil, false, errors.New("expected complete 1..32 base and optional research migration")
	}
	for i := 0; i < 32; i++ {
		if versions[i] != int64(i+1) {
			return nil, false, errors.New("base migration gap")
		}
	}
	present := len(versions) >= 33 && versions[32] == 42
	var table, fn bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.performance_research_cost_versions') IS NOT NULL, to_regprocedure('public.reject_research_cost_version_mutation()') IS NOT NULL`).Scan(&table, &fn); err != nil {
		return nil, false, err
	}
	if table != present || fn != present {
		return nil, false, errors.New("research schema and migration registry disagree")
	}
	if present {
		var guarded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.performance_research_cost_versions'::regclass AND tgname='research_cost_version_immutable' AND tgenabled IN ('O','A') AND tgtype=27 AND tgfoid='public.reject_research_cost_version_mutation()'::regprocedure)`).Scan(&guarded); err != nil {
			return nil, false, err
		}
		if !guarded {
			return nil, false, errors.New("research immutability trigger missing or disabled")
		}
	}
	return versions, present, nil
}

func loadResearchMigration() ([]dbmigrations.Migration, dbmigrations.Migration, error) {
	all, err := dbmigrations.LoadDir("migrations/postgres")
	if err != nil {
		return nil, dbmigrations.Migration{}, err
	}
	m, ok := dbmigrations.FindByVersion(all, 42)
	if !ok || m.Name != researchMigrationName {
		return nil, m, errors.New("research migration missing")
	}
	h := sha256.Sum256([]byte(m.UpSQL))
	if hex.EncodeToString(h[:]) != researchMigrationHash {
		return nil, m, errors.New("research migration fingerprint mismatch")
	}
	return all, m, nil
}

type fuyingResearchImportReport struct {
	Phase               string                           `json:"phase"`
	GeneratedAt         time.Time                        `json:"generated_at"`
	Database            string                           `json:"database"`
	MigrationSHA256     string                           `json:"migration_sha256"`
	AccountID           string                           `json:"account_id"`
	VersionID           string                           `json:"version_id"`
	PayloadSHA256       string                           `json:"payload_sha256"`
	Review              performance.CostContinuityReview `json:"review"`
	MigrationsBefore    []int64                          `json:"migrations_before"`
	MigrationsAfter     []int64                          `json:"migrations_after"`
	MigrationNeeded     bool                             `json:"migration_needed"`
	PreparedNewVersions int                              `json:"prepared_new_versions"`
	VersionExists       bool                             `json:"version_exists"`
	FinancialBefore     map[string]string                `json:"financial_before"`
	FinancialAfter      map[string]string                `json:"financial_after"`
	FinancialWrites     int                              `json:"financial_writes"`
	BrokerCommands      int                              `json:"broker_commands"`
	NAVPublishable      bool                             `json:"economic_nav_publishable"`
	BackupSHA256        string                           `json:"backup_sha256,omitempty"`
	IndependentReadback bool                             `json:"independent_readback"`
}

func prepareFuyingResearchImport(ctx context.Context, tx *sql.Tx, b performance.ResearchCostBundle, v ledger.ResearchCostVersion, local []dbmigrations.Migration, m dbmigrations.Migration, apply, allowMigration bool) (fuyingResearchImportReport, error) {
	r := fuyingResearchImportReport{Phase: "read_only_preflight", GeneratedAt: timeutil.Now(), AccountID: b.AccountID, VersionID: b.VersionID, PayloadSHA256: v.PayloadSHA256, Review: b.Review}
	r.MigrationSHA256 = researchMigrationHash
	migrationDigest := sha256.Sum256([]byte(m.UpSQL))
	if b.AccountID != fuying13Account || b.VersionID != fuyingResearchVersion || v.PayloadSHA256 != fuyingResearchPayloadHash || m.Version != 42 || m.Name != researchMigrationName || hex.EncodeToString(migrationDigest[:]) != researchMigrationHash {
		return r, errors.New("recovery scope mismatch")
	}
	for _, statement := range []string{`SET LOCAL search_path TO public`, `SET LOCAL statement_timeout='30s'`, `SET LOCAL lock_timeout='5s'`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return r, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&r.Database); err != nil {
		return r, err
	}
	if apply {
		// Freeze the registry and audited inputs for the short import transaction.
		// SHARE also excludes newly inserted revisions, not just existing rows.
		if _, err := tx.ExecContext(ctx, `LOCK TABLE relay_schema_migrations IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return r, err
		}
		if _, err := tx.ExecContext(ctx, `LOCK TABLE performance_attribution_links,performance_position_cost_states IN SHARE MODE`); err != nil {
			return r, err
		}
	}
	versions, present, err := researchMigrationState(ctx, tx, local)
	if err != nil {
		return r, err
	}
	r.MigrationsBefore = versions
	r.MigrationNeeded = !present
	if err := checkFuyingResearchEvidence(ctx, tx, b); err != nil {
		return r, err
	}
	r.FinancialBefore, err = fuyingResearchFingerprints(ctx, tx)
	if err != nil {
		return r, err
	}
	if apply && !present {
		if !allowMigration {
			return r, errors.New("research table absent; explicit --allow-research-migration required")
		}
		if _, err := tx.ExecContext(ctx, m.UpSQL); err != nil {
			return r, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO relay_schema_migrations(version,name) VALUES(42,$1)`, m.Name); err != nil {
			return r, err
		}
		present = true
	}
	if present {
		stored, err := ledger.NewRepository(tx).GetResearchCostVersion(ctx, b.AccountID, b.VersionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return r, err
		}
		r.VersionExists = err == nil
		if r.VersionExists && (stored.PayloadSHA256 != v.PayloadSHA256 || !bytes.Equal(stored.Payload, v.Payload)) {
			return r, errors.New("stored research version conflicts with frozen plan")
		}
		if apply {
			if _, err := ledger.NewRepository(tx).AppendResearchCostVersion(ctx, v); err != nil {
				return r, err
			}
			if !r.VersionExists {
				r.PreparedNewVersions = 1
			}
		}
	}
	r.MigrationsAfter, _, err = researchMigrationState(ctx, tx, local)
	if err != nil {
		return r, err
	}
	r.FinancialAfter, err = fuyingResearchFingerprints(ctx, tx)
	if err != nil {
		return r, err
	}
	if !reflect.DeepEqual(r.FinancialBefore, r.FinancialAfter) {
		return r, errors.New("financial history changed during research import; rolled back")
	}
	if apply {
		r.Phase = "prepared_not_committed"
	}
	return r, nil
}

func verifyFuyingResearchCommit(ctx context.Context, tx *sql.Tx, b performance.ResearchCostBundle, v ledger.ResearchCostVersion, local []dbmigrations.Migration, r *fuyingResearchImportReport) error {
	for _, statement := range []string{`SET LOCAL search_path TO public`, `SET LOCAL statement_timeout='30s'`, `SET LOCAL lock_timeout='5s'`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	got, err := ledger.NewRepository(tx).GetResearchCostVersion(ctx, b.AccountID, b.VersionID)
	if err != nil {
		return err
	}
	decoded, err := performance.DecodeResearchCostVersion(got)
	if err != nil || !bytes.Equal(got.Payload, v.Payload) || got.PayloadSHA256 != v.PayloadSHA256 || !reflect.DeepEqual(decoded.Review, b.Review) {
		return errors.New("committed research readback differs")
	}
	versions, present, err := researchMigrationState(ctx, tx, local)
	if err != nil {
		return err
	}
	if !present || !reflect.DeepEqual(versions, r.MigrationsAfter) {
		return errors.New("migration set changed after commit")
	}
	if err := checkFuyingResearchEvidence(ctx, tx, b); err != nil {
		return err
	}
	fingerprints, err := fuyingResearchFingerprints(ctx, tx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fingerprints, r.FinancialBefore) {
		return errors.New("financial history changed after commit; investigate, do not rewrite")
	}
	r.Phase = "committed_and_verified"
	r.IndependentReadback = true
	r.VersionExists = true
	return nil
}

func validateResearchBackup(ctx context.Context, path, digest string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1024 || time.Since(info.ModTime()) > 2*time.Hour || info.ModTime().After(time.Now().Add(time.Minute)) {
		return errors.New("fresh full backup required")
	}
	h, err := recovery20261027FileHash(path)
	if err != nil || len(digest) != 64 || h != digest {
		return errors.New("backup digest mismatch")
	}
	raw, err := os.ReadFile(strings.TrimSuffix(path, ".dump") + ".manifest.json")
	if err != nil {
		return errors.New("backup manifest required")
	}
	if err := validateResearchBackupManifest(raw, path, digest, info.Size(), time.Now()); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "pg_restore", "--list", path).Run(); err != nil {
		return errors.New("backup archive validation failed")
	}
	return nil
}

func validateResearchBackupManifest(raw []byte, path, digest string, size int64, now time.Time) error {
	var manifest struct {
		Format  string `json:"format"`
		Archive struct {
			File  string `json:"file"`
			SHA   string `json:"sha256"`
			Bytes int64  `json:"bytes"`
		} `json:"archive"`
		Snapshot struct {
			Database   string `json:"database"`
			CapturedAt string `json:"captured_at"`
		} `json:"database_snapshot"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	captured, err := time.ParseInLocation("2006-01-02T15:04:05.999999", manifest.Snapshot.CapturedAt, timeutil.Location())
	if err != nil || now.Sub(captured) > 2*time.Hour || captured.After(now.Add(time.Minute)) || manifest.Format != "relay.postgres.backup.v1" || manifest.Snapshot.Database != "relay_trader" || manifest.Archive.File != filepath.Base(path) || manifest.Archive.SHA != digest || manifest.Archive.Bytes != size {
		return errors.New("backup manifest identity/freshness mismatch")
	}
	return nil
}

func writeFuyingResearchImportSummary(r fuyingResearchImportReport) error {
	return writeJSON(map[string]any{
		"phase": r.Phase, "account_id": r.AccountID, "version_id": r.VersionID, "payload_sha256": r.PayloadSHA256,
		"days": r.Review.Days, "rows": r.Review.Rows, "migration_needed": r.MigrationNeeded, "migrations_after": r.MigrationsAfter,
		"prepared_new_versions": r.PreparedNewVersions, "version_exists": r.VersionExists, "independent_readback": r.IndependentReadback,
		"financial_writes": r.FinancialWrites, "broker_commands": r.BrokerCommands, "economic_nav_publishable": r.NAVPublishable,
	})
}

func runFuyingResearchImport(args []string) error {
	f := flag.NewFlagSet("performance-fuying13-cost-import-20261114", flag.ContinueOnError)
	configPath := f.String("config", "", "production config; no environment switch")
	output := f.String("output", "", "new private report; apply also creates .prepared.json")
	apply := f.Bool("apply", false, "archive the pinned research version only")
	allowMigration := f.Bool("allow-research-migration", false, "allow only pinned migration 42, in the import transaction")
	expectedHash := f.String("expected-payload-sha256", "", "required for apply")
	backup := f.String("backup", "", "fresh full custom backup required for apply")
	backupHash := f.String("backup-sha256", "", "expected backup digest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" || (*allowMigration && !*apply) {
		return errors.New("config/output required; migration only with apply")
	}
	if *apply && (*expectedHash != fuyingResearchPayloadHash || *backup == "" || len(*backupHash) != 64) {
		return errors.New("apply requires pinned payload hash and fresh backup")
	}
	for _, path := range []string{*output, *output + ".prepared.json"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("output paths must not exist")
		}
	}
	b, v, err := loadFuyingResearchBundle()
	if err != nil {
		return err
	}
	local, m, err := loadResearchMigration()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	parsed, err := pgconn.ParseConfig(cfg.Database.DSN)
	if err != nil || cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" || parsed.Database != "relay_trader" {
		return errors.New("production configuration and exact database identity required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if *apply {
		if err := validateResearchBackup(ctx, *backup, *backupHash); err != nil {
			return err
		}
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	var name string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("connected database identity mismatch")
	}
	isolation := sql.LevelRepeatableRead
	if *apply {
		isolation = sql.LevelReadCommitted
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*apply, Isolation: isolation})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := prepareFuyingResearchImport(ctx, tx, b, v, local, m, *apply, *allowMigration)
	if err != nil {
		return err
	}
	r.BackupSHA256 = *backupHash
	if !*apply {
		if err := writeETFTransferReviewFile(*output, r); err != nil {
			return err
		}
		return writeFuyingResearchImportSummary(r)
	}
	if err := writeETFTransferReviewFile(*output+".prepared.json", r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outcome not verified; inspect pinned version before retry: %w", err)
	}
	// A new pool ensures verification cannot accidentally reuse the writing
	// transaction's connection or its uncommitted view.
	readDB, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return fmt.Errorf("committed; independent readback unavailable: %w", err)
	}
	defer readDB.Close()
	readTx, err := readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("committed; readback unavailable: %w", err)
	}
	defer readTx.Rollback()
	if err := verifyFuyingResearchCommit(ctx, readTx, b, v, local, &r); err != nil {
		return fmt.Errorf("committed but not verified; do not change financial history: %w", err)
	}
	if err := writeETFTransferReviewFile(*output, r); err != nil {
		return fmt.Errorf("committed and readback passed; report write failed: %w", err)
	}
	return writeFuyingResearchImportSummary(r)
}
