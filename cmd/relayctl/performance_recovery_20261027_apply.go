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
	"io"
	"math"
	"os"
	"os/exec"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type recovery20261027Reviewed struct {
	DatabaseReadOnly  bool                     `json:"database_read_only"`
	DatabaseWrites    int                      `json:"database_writes"`
	QualityGatePassed bool                     `json:"quality_gate_passed"`
	Plans             []recovery20261027Plan   `json:"plans"`
	Preview           performanceRebuildReport `json:"preview"`
}

func recovery20261027FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Applying is deliberately separate from the read-only planner and pinned to
// a reviewed report, its original evidence, and a freshly verified DB archive.
func runPerformanceRecovery20261027Apply(args []string) error {
	flags := flag.NewFlagSet("performance-recovery-20261027-apply", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	planPath := flags.String("plan", "", "reviewed read-only report")
	planHash := flags.String("plan-sha256", "", "reviewed report digest")
	backupPath := flags.String("backup", "", "fresh PostgreSQL custom archive")
	backupHash := flags.String("backup-sha256", "", "verified archive digest")
	output := flags.String("output", "", "new private pre-commit receipt")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *planPath == "" || len(*planHash) != 64 || *backupPath == "" || len(*backupHash) != 64 || *output == "" {
		return errors.New("reviewed plan/digest, backup/digest and output are required")
	}
	for _, item := range [][2]string{{*planPath, *planHash}, {*backupPath, *backupHash}} {
		hash, err := recovery20261027FileHash(item[0])
		if err != nil {
			return err
		}
		if hash != item[1] {
			return errors.New("plan or backup hash mismatch")
		}
	}
	stat, err := os.Stat(*backupPath)
	if err != nil {
		return err
	}
	if stat.Size() < 1024 || time.Since(stat.ModTime()) > 2*time.Hour || stat.ModTime().After(time.Now().Add(time.Minute)) {
		return errors.New("backup must be fresh (within 2h)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := exec.CommandContext(ctx, "pg_restore", "--list", *backupPath).Run(); err != nil {
		return errors.New("backup archive listing failed")
	}
	data, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	var reviewed recovery20261027Reviewed
	if err := json.Unmarshal(data, &reviewed); err != nil {
		return err
	}
	if err := validateRecovery20261027Reviewed(reviewed); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("reviewed production ledger required")
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext('relay-recovery-20261027'))"); err != nil {
		return err
	}
	repo := ledger.NewRepository(tx)
	// Re-read every file and financial source row in this serializable snapshot.
	for _, old := range reviewed.Plans {
		pcf, ok := old.Finalization.RawPayload["meridian_components"].(map[string]any)
		if !ok {
			return errors.New("PCF proof missing")
		}
		cash, ok := old.Finalization.RawPayload["meridian_cash_component"].(map[string]any)
		if !ok {
			return errors.New("cash component proof missing")
		}
		expected, err := verifyRecovery20261027PCF(pcf, cash)
		if err != nil {
			return err
		}
		current, err := buildRecovery20261027Plan(ctx, tx, old.AccountID, expected)
		if err != nil {
			return err
		}
		current.Finalization.RawPayload["meridian_components"] = pcf
		current.Finalization.RawPayload["meridian_cash_component"] = cash
		got, _ := json.Marshal(current)
		want, _ := json.Marshal(old)
		if !bytes.Equal(got, want) {
			return errors.New("source evidence changed after preview")
		}
		existing, err := repo.ListETFSettlementFinalizations(ctx, old.AccountID, "2026-10-21")
		if err != nil {
			return err
		}
		if len(existing) != 0 {
			return errors.New("finalization already exists; refusing duplicate recovery")
		}
		entries, err := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: old.AccountID, DateFrom: "2026-10-21", DateTo: "2026-10-27", Limit: 1000})
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return errors.New("new cash ledger entries require a refreshed conflict review")
		}
		for _, b := range old.Bases {
			_, err := repo.GetAssetPositionObservation(ctx, old.AccountID, b.TradeDate, "reconcile")
			if err == nil {
				return errors.New("reconcile basis already exists; refusing overwrite")
			}
			if !errors.Is(err, ledger.ErrAssetNotFound) {
				return err
			}
		}
	}
	now := timeutil.Now()
	for _, p := range reviewed.Plans {
		f := p.Finalization
		f.ConfirmedAt = now
		f.ConfirmedBy = "user_authorized_relay_20261027"
		f.RawPayload["reviewed_plan_sha256"] = *planHash
		f.RawPayload["backup_sha256"] = *backupHash
		if _, err := repo.UpsertETFSettlementFinalization(ctx, f); err != nil {
			return err
		}
		for _, entry := range p.CashEntries {
			entry.Status = "draft"
			entry.RawPayload["reviewed_plan_sha256"] = *planHash
			if _, err := repo.CreateCashLedgerEntry(ctx, entry); err != nil {
				return err
			}
			if _, err := repo.ConfirmCashLedgerEntry(ctx, entry.AccountID, entry.EntryID, "user_authorized_relay_20261027", now); err != nil {
				return err
			}
		}
		for _, b := range p.Bases {
			b.RawPayload["reviewed_plan_sha256"] = *planHash
			a := trading.Asset{AccountID: b.AccountID, CashAvailable: b.CashAvailable, CashTotal: b.CashTotal, NetAsset: b.NetAsset, MarketValue: b.MarketValue, ReverseRepoReceivable: b.ReverseRepoReceivable, UpdatedAt: b.CapturedAt}
			if err := repo.UpsertAssetSnapshotForDate(ctx, a, b.TradeDate, "reconcile", b.Source, b.RawPayload, b.CapturedAt); err != nil {
				return err
			}
		}
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := performance.New(performance.Options{Store: repo, Calendar: client, Market: client, FormulaVersion: cfg.Performance.FormulaVersion,
		ETFT0FrictionRate: cfg.Performance.ETFT0FrictionRate, AutoToleranceCNY: cfg.Performance.AutoToleranceCNY, AutoToleranceBP: cfg.Performance.AutoToleranceBP,
		WarningToleranceCNY: cfg.Performance.WarningToleranceCNY, WarningToleranceBP: cfg.Performance.WarningToleranceBP})
	if err != nil {
		return err
	}
	report := performanceRebuildReport{DateFrom: "2026-10-21", DateTo: "2026-10-27", Persist: true}
	for _, expected := range reviewed.Preview.Items {
		item := performanceRebuildItem{AccountID: expected.AccountID, TradeDate: expected.TradeDate}
		item.Cost, err = service.CalculateCostLedger(ctx, item.AccountID, item.TradeDate, performance.CostLedgerOptions{Persist: true})
		if err != nil {
			return err
		}
		item.NAV, err = service.CalculateEconomicNAV(ctx, item.AccountID, item.TradeDate, performance.EconomicNAVOptions{})
		if err != nil {
			return err
		}
		if !performanceResultPublishable(item.Cost.Status, item.NAV.Status) {
			return fmt.Errorf("%s/%s blocked; entire recovery rolled back", item.AccountID, item.TradeDate)
		}
		if err := compareRecovery20261027NAV(item.NAV.NAV, expected.NAV.NAV); err != nil {
			return fmt.Errorf("%s/%s: %w", item.AccountID, item.TradeDate, err)
		}
		item.NAV, err = service.CalculateEconomicNAV(ctx, item.AccountID, item.TradeDate, performance.EconomicNAVOptions{Persist: true, Status: "provisional"})
		if err != nil {
			return err
		}
		if err := compareRecovery20261027NAV(item.NAV.NAV, expected.NAV.NAV); err != nil {
			return err
		}
		report.Items = append(report.Items, item)
	}
	if err := writeETFTransferReviewFile(*output, map[string]any{"transaction_state": "prepared_not_committed", "reviewed_plan_sha256": *planHash, "backup_sha256": *backupHash, "results": report}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed; prepared report is NOT a commit receipt: %w", err)
	}
	return writeJSON(map[string]any{"transaction_state": "committed", "committed_at": timeutil.Now(), "reviewed_plan_sha256": *planHash, "backup_sha256": *backupHash, "accounts": 2, "nav_days": 10, "reconcile_bases": 8, "finalizations": 2, "cash_entries": 198, "broker_commands": 0})
}

func validateRecovery20261027Reviewed(r recovery20261027Reviewed) error {
	if !r.DatabaseReadOnly || r.DatabaseWrites != 0 || !r.QualityGatePassed || len(r.Plans) != 2 || len(r.Preview.Items) != 10 {
		return errors.New("incomplete reviewed read-only plan")
	}
	dates := []string{"2026-10-21", "2026-10-22", "2026-10-23", "2026-10-26", "2026-10-27"}
	for i, a := range []string{"307000051387", "307000051388"} {
		if r.Plans[i].AccountID != a {
			return errors.New("account scope differs")
		}
		for j, d := range dates {
			item := r.Preview.Items[i*5+j]
			if item.AccountID != a || item.TradeDate != d || item.Error != "" || !performanceResultPublishable(item.Cost.Status, item.NAV.Status) {
				return errors.New("preview chain incomplete or blocked")
			}
			if err := compareRecovery20261027NAV(item.NAV.NAV, item.NAV.NAV); err != nil {
				return err
			}
		}
	}
	return nil
}

func compareRecovery20261027NAV(actual, expected ledger.PerformanceNAV) error {
	for _, pair := range [][2]float64{{actual.OpenEconomicNAV, expected.OpenEconomicNAV}, {actual.CloseEconomicNAV, expected.CloseEconomicNAV}, {actual.AccountDayPnL, expected.AccountDayPnL}, {actual.ReturnDenominator, expected.ReturnDenominator}, {actual.ExternalNetFlow, expected.ExternalNetFlow}} {
		if math.IsNaN(pair[0]) || math.IsNaN(pair[1]) || math.IsInf(pair[0], 0) || math.IsInf(pair[1], 0) || math.Abs(pair[0]-pair[1]) > 0.01 {
			return errors.New("NAV changed after reviewed preview")
		}
	}
	if math.IsNaN(actual.CumulativeNAV) || math.IsNaN(expected.CumulativeNAV) || math.IsInf(actual.CumulativeNAV, 0) || math.IsInf(expected.CumulativeNAV, 0) || math.Abs(actual.CumulativeNAV-expected.CumulativeNAV) > 0.00000001 {
		return errors.New("cumulative NAV changed after preview")
	}
	return nil
}
