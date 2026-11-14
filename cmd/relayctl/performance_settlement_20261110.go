package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

var settlement20261110Accounts = []string{"307000051387", "307000051388"}
var settlement20261110Dates = []string{"2026-11-04", "2026-11-05", "2026-11-06", "2026-11-09", "2026-11-10"}

func settlement20261110Spec() actualSettlementSpec {
	s := actualSettlementSpec{SourceDate: "20261104", FeeDate: "20261105", CashDate: "20261106", BasisThrough: "20261109",
		Source: "broker_historical_funds_statement_one_time_audit_20261110_settlement", Tag: "20261110-settlement",
		CashComponentCents: 337695, DeferredFeeCents: 1304, Hashes: map[string][2]string{},
		Orders: map[string][2]string{"307000051387": {"12002A620001941", "12002A620008181"}, "307000051388": {"12002A620001949", "12002A620008082"}}}
	for _, b := range brokerBasis20261110Specs {
		if _, ok := s.Orders[b.Account]; ok {
			s.Hashes[b.Account] = [2]string{b.FundsHash, b.FlowsHash}
		}
	}
	return s
}

func validateSettlement20261110Reviewed(r recovery20261027Reviewed) error {
	if !r.DatabaseReadOnly || r.DatabaseWrites != 0 || !r.QualityGatePassed || len(r.Plans) != 2 || len(r.Preview.Items) != 10 {
		return errors.New("incomplete settlement preview")
	}
	for i, a := range settlement20261110Accounts {
		if r.Plans[i].AccountID != a || r.Plans[i].Finalization.SourceTradeDate != "2026-11-04" || len(r.Plans[i].CashEntries) != 99 || len(r.Plans[i].Bases) != 4 {
			return errors.New("unexpected settlement plan scope")
		}
		for j, d := range settlement20261110Dates {
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

// Build against fresh broker files and current PG rows, never just an old audit total.
func buildSettlement20261110(ctx context.Context, tx *sql.Tx, account string, pcf, cash map[string]any) (recovery20261027Plan, error) {
	expected, err := verifyActualSettlementPCF(pcf, cash, 20261104, 20261105, 3376.95)
	if err != nil {
		return recovery20261027Plan{}, err
	}
	p, err := buildActualSettlementPlan(ctx, tx, account, expected, settlement20261110Spec())
	if err != nil {
		return p, err
	}
	repo := ledger.NewRepository(tx)
	f, err := repo.ListETFSettlementFinalizations(ctx, account, "2026-11-04")
	if err != nil {
		return p, err
	}
	if len(f) != 0 {
		return p, errors.New("settlement already exists; do not replay")
	}
	entries, err := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: account, DateFrom: "2026-11-04", DateTo: "2026-11-10", Limit: 1000})
	if err != nil {
		return p, err
	}
	if len(entries) != 0 {
		return p, errors.New("cash ledger changed; refresh conflict review")
	}
	for i := range p.Bases {
		b := &p.Bases[i]
		old, err := repo.GetAssetPositionObservation(ctx, account, b.TradeDate, "reconcile")
		if err != nil {
			return p, err
		}
		if old.Source != brokerBasis20261110Source || old.RawPayload["broker_asset_base_authoritative"] != true || old.RawPayload["economic_nav_base_confirmed"] != false || old.RawPayload["statement_sha256"] != b.RawPayload["statement_sha256"] || old.NetAsset != b.NetAsset || old.CashTotal != b.CashTotal || old.MarketValue != b.MarketValue || old.ReverseRepoReceivable != b.ReverseRepoReceivable {
			return p, errors.New("prior broker asset basis changed")
		}
		b.CashAvailable = old.CashAvailable
		b.RawPayload["cash_available_unverified"] = true
		b.RawPayload["broker_asset_base_authoritative"] = true
		b.RawPayload["settlement_overlay_status"] = "reviewed_actual_settlement"
		b.RawPayload["prior_reported_asset_basis"] = old.RawPayload
	}
	p.Finalization.RawPayload["meridian_components"] = pcf
	p.Finalization.RawPayload["meridian_cash_component"] = cash
	return p, nil
}

func settlement20261110Service(cfg *config.Config, store performance.Store, client researchMarket) (*performance.Service, error) {
	return performance.New(performance.Options{Store: store, Calendar: client, Market: client, FormulaVersion: cfg.Performance.FormulaVersion,
		ETFT0FrictionRate: cfg.Performance.ETFT0FrictionRate, AutoToleranceCNY: cfg.Performance.AutoToleranceCNY, AutoToleranceBP: cfg.Performance.AutoToleranceBP,
		WarningToleranceCNY: cfg.Performance.WarningToleranceCNY, WarningToleranceBP: cfg.Performance.WarningToleranceBP})
}

func runSettlement20261110(args []string) error {
	flags := flag.NewFlagSet("performance-settlement-20261110", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	output := flags.String("output", "", "new private evidence report")
	apply := flags.Bool("apply", false, "apply a reviewed preview atomically")
	planPath := flags.String("plan", "", "reviewed preview JSON")
	planHash := flags.String("plan-sha256", "", "reviewed preview digest")
	backup := flags.String("backup", "", "fresh PostgreSQL archive")
	backupHash := flags.String("backup-sha256", "", "archive digest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var reviewed recovery20261027Reviewed
	if *apply {
		if len(*planHash) != 64 || len(*backupHash) != 64 {
			return errors.New("plan and backup digests required")
		}
		for _, f := range [][2]string{{*planPath, *planHash}, {*backup, *backupHash}} {
			h, err := recovery20261027FileHash(f[0])
			if err != nil || h != f[1] {
				return errors.New("plan or backup digest mismatch")
			}
		}
		stat, err := os.Stat(*backup)
		if err != nil || stat.Size() < 1024 || time.Since(stat.ModTime()) > 2*time.Hour || stat.ModTime().After(time.Now().Add(time.Minute)) {
			return errors.New("fresh backup required")
		}
		if err := exec.CommandContext(ctx, "pg_restore", "--list", *backup).Run(); err != nil {
			return errors.New("invalid backup archive")
		}
		body, err := os.ReadFile(*planPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &reviewed); err != nil {
			return err
		}
		if err := validateSettlement20261110Reviewed(reviewed); err != nil {
			return err
		}
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
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*apply, Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var dbname string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&dbname); err != nil || dbname != "relay_trader" {
		return errors.New("unexpected database")
	}
	if *apply {
		if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext('relay-settlement-20261110'))"); err != nil {
			return err
		}
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	var pcf, cash map[string]any
	if !*apply {
		r, err := client.MarketETFComponents(ctx, url.Values{"security_id": {"159915.SZ"}, "trade_date": {"20261104"}, "limit": {"1000"}})
		if err != nil || r.StatusCode != 200 {
			return errors.New("PCF unavailable")
		}
		pcf = r.Payload
		r, err = client.MarketETFCashComponents(ctx, url.Values{"security_id": {"159915.SZ"}, "trade_date": {"20261105"}, "limit": {"5"}})
		if err != nil || r.StatusCode != 200 {
			return errors.New("cash component unavailable")
		}
		cash = r.Payload
	}
	repo := ledger.NewRepository(tx)
	preview := &recovery20261027Preview{performancePreviewStore: &performancePreviewStore{Repository: repo, costs: map[string]map[string][]ledger.PositionCostState{}, navs: map[string]map[string]ledger.PerformanceNAV{}}, plans: map[string]recovery20261027Plan{}}
	plans := make([]recovery20261027Plan, 0, 2)
	for i, a := range settlement20261110Accounts {
		if *apply {
			pcf, _ = reviewed.Plans[i].Finalization.RawPayload["meridian_components"].(map[string]any)
			cash, _ = reviewed.Plans[i].Finalization.RawPayload["meridian_cash_component"].(map[string]any)
		}
		p, err := buildSettlement20261110(ctx, tx, a, pcf, cash)
		if err != nil {
			return fmt.Errorf("%s: %w", a, err)
		}
		if *apply && !equalRecovery20261027Plan(p, reviewed.Plans[i]) {
			return errors.New("source changed after preview")
		}
		plans = append(plans, p)
		preview.plans[a] = p
	}
	var store performance.Store = preview
	if *apply {
		if err := persistSettlement20261110(ctx, repo, plans, *planHash, *backupHash); err != nil {
			return err
		}
		store = repo
	}
	service, err := settlement20261110Service(cfg, store, client)
	if err != nil {
		return err
	}
	report := performanceRebuildReport{DateFrom: "2026-11-04", DateTo: "2026-11-10", Persist: *apply, PreviewChain: !*apply}
	passed := true
	for _, p := range plans {
		for _, d := range settlement20261110Dates {
			item := performanceRebuildItem{AccountID: p.AccountID, TradeDate: d}
			item.Cost, err = service.CalculateCostLedger(ctx, p.AccountID, d, performance.CostLedgerOptions{Persist: *apply})
			if err == nil {
				item.NAV, err = service.CalculateEconomicNAV(ctx, p.AccountID, d, performance.EconomicNAVOptions{})
			}
			if err != nil {
				item.Error = err.Error()
			}
			valid := err == nil && performanceResultPublishable(item.Cost.Status, item.NAV.Status)
			if valid {
				err = verifySettlement20261110NAV(p, item)
				valid = err == nil
				if err != nil {
					item.Error = err.Error()
				}
			}
			if valid && *apply {
				if !sameSettlementCost(item.Cost, reviewed.Preview.Items[len(report.Items)].Cost) {
					return errors.New("cost summary changed after reviewed preview")
				}
				want := reviewed.Preview.Items[len(report.Items)].NAV.NAV
				if err := compareRecovery20261027NAV(item.NAV.NAV, want); err != nil {
					return err
				}
				item.NAV, err = service.CalculateEconomicNAV(ctx, p.AccountID, d, performance.EconomicNAVOptions{Persist: true, Status: "provisional"})
				if err != nil {
					return err
				}
				if err := compareRecovery20261027NAV(item.NAV.NAV, want); err != nil {
					return err
				}
			}
			report.Items = append(report.Items, item)
			if !valid {
				passed = false
				break
			}
			preview.remember(p.AccountID, d, item.Cost.Positions, item.NAV.NAV)
		}
	}
	writesPrepared := 0
	if *apply {
		writesPrepared = 208 // Two settlements, 198 cash legs and eight reconcile bases.
		for _, item := range report.Items {
			writesPrepared += len(item.Cost.Positions) + 1
		}
	}
	if err := writeETFTransferReviewFile(*output, map[string]any{"database_read_only": !*apply, "database_writes": writesPrepared, "transaction_state": "prepared_not_committed", "quality_gate_passed": passed, "plans": plans, "preview": report, "reviewed_plan_sha256": *planHash, "backup_sha256": *backupHash}); err != nil {
		return err
	}
	if !passed {
		return errors.New("recovery blocked; no transaction committed")
	}
	if *apply {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit failed; report is NOT a receipt: %w", err)
		}
	}
	return writeJSON(map[string]any{"committed": *apply, "quality_gate_passed": passed, "nav_days": len(report.Items), "finalizations": 2, "cash_entries": 198, "broker_commands": 0})
}

func verifySettlement20261110NAV(p recovery20261027Plan, item performanceRebuildItem) error {
	for _, b := range p.Bases {
		if b.TradeDate != item.TradeDate {
			continue
		}
		want := b.RawPayload["reported_daily_pnl"].(float64) + b.RawPayload["close_outstanding_etf_settlement_asset"].(float64) - b.RawPayload["open_outstanding_etf_settlement_asset"].(float64)
		if !item.NAV.AssetBasis.Applied || math.Abs(item.NAV.NAV.AccountDayPnL-want) > .01 {
			return errors.New("NAV differs from broker asset and actual settlement bridge")
		}
	}
	if math.Abs(item.NAV.ETFSettlement.SettlementVariance) > .000001 || math.Abs(item.NAV.ETFSettlement.PostCloseReceiptAmount) > .000001 {
		return errors.New("actual settlement was recognized as new income or cash was added twice")
	}
	return nil
}

func persistSettlement20261110(ctx context.Context, repo *ledger.Repository, plans []recovery20261027Plan, planHash, backupHash string) error {
	now := timeutil.Now()
	for _, p := range plans {
		f := p.Finalization
		f.ConfirmedAt = now
		f.ConfirmedBy = "user_authorized_relay_20261110"
		f.RawPayload["reviewed_plan_sha256"] = planHash
		f.RawPayload["backup_sha256"] = backupHash
		if _, err := repo.UpsertETFSettlementFinalization(ctx, f); err != nil {
			return err
		}
		for _, e := range p.CashEntries {
			e.Status = "draft"
			e.RawPayload["reviewed_plan_sha256"] = planHash
			if _, err := repo.CreateCashLedgerEntry(ctx, e); err != nil {
				return err
			}
			if _, err := repo.ConfirmCashLedgerEntry(ctx, e.AccountID, e.EntryID, "user_authorized_relay_20261110", now); err != nil {
				return err
			}
		}
		for _, b := range p.Bases {
			b.RawPayload["reviewed_plan_sha256"] = planHash
			a := trading.Asset{AccountID: b.AccountID, CashAvailable: b.CashAvailable, CashTotal: b.CashTotal, NetAsset: b.NetAsset, MarketValue: b.MarketValue, ReverseRepoReceivable: b.ReverseRepoReceivable, UpdatedAt: b.CapturedAt}
			if err := repo.UpsertAssetSnapshotForDate(ctx, a, b.TradeDate, "reconcile", b.Source, b.RawPayload, b.CapturedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// Avoid accepting a shape-preserving change to the reviewed cost result on apply.
func sameSettlementCost(a, b performance.CostLedgerResult) bool {
	if !reflect.DeepEqual(a.Summary, b.Summary) || a.Status != b.Status || a.AccountID != b.AccountID || a.TradeDate != b.TradeDate || len(a.Positions) != len(b.Positions) {
		return false
	}
	normalize := func(items []ledger.PositionCostState) string {
		rows := append([]ledger.PositionCostState(nil), items...)
		for i := range rows {
			rows[i].CalculatedAt = time.Time{}
			rows[i].CreatedAt = time.Time{}
			rows[i].UpdatedAt = time.Time{}
		}
		data, err := json.Marshal(rows)
		if err != nil {
			return "invalid"
		}
		return string(data)
	}
	left, right := normalize(a.Positions), normalize(b.Positions)
	return left != "invalid" && left == right
}
