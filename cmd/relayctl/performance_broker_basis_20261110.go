package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const brokerBasis20261110Source = "broker_historical_funds_statement_one_time_audit_20261110"

type brokerBasis20261110Spec struct {
	Account, From, FundsHash, FlowsHash string
	Days                                int
}

var brokerBasis20261110Specs = []brokerBasis20261110Spec{
	{"314000045768", "20261012", "a8e5130576756acd0d8ce765b2f3a42901684ca09b78ac50722b3cd37c6c4379", "dae4863b8e337b9c0b28ad37e84503f84a920e3ea066fc8f956771cc2748a224", 21},
	{"307000051387", "20261104", "cda20342476090817e50ddfc42d8fbc5fbba065482973712e660c76fc2992bac", "163f3bc2f1032a82fc26e9c9eb5a2e5dbaf8955dfa9150e6950c213c9d60c3a4", 4},
	{"307000051388", "20261104", "c758da91e436f89be648fcf7c5bab71b4915de6a42f84f690945847a932811bf", "0d7414af3054475a965ceda12047bc5b5c1f5e103c31bbb03a00332d0d38ecca", 4},
}

type brokerBasis20261110Day struct {
	Account string                    `json:"account_id"`
	Date    string                    `json:"trade_date"`
	Asset   trading.Asset             `json:"reported_asset"`
	Raw     map[string]any            `json:"evidence"`
	Gold    ledger.PerformanceNAVGold `json:"gold"`
}

var brokerBasis20261110Repo = regexp.MustCompile(`融券成交GC001\(204001\)([0-9]+)手，利率[0-9]+\.[0-9]+$`)

func buildBrokerBasis20261110(spec brokerBasis20261110Spec, fundsCSV, flowsCSV *brokerCSV) ([]brokerBasis20261110Day, error) {
	for _, r := range fundsCSV.rows {
		owner, err := fundsCSV.value(r, "客户号")
		if err != nil || owner != spec.Account {
			return nil, errors.New("funds account mismatch")
		}
	}
	// This dated repair accepts only the exact, independently reviewed export layout.
	for _, r := range flowsCSV.rows {
		if len(flowsCSV.header) != 14 || len(r) != 13 || flowsCSV.header[11] != "资金余额" || flowsCSV.header[12] != "操作摘要" || r[2] != spec.Account+"01" || r[7] != spec.Account {
			return nil, errors.New("flow identity or shape mismatch")
		}
	}
	funds, _, err := parseBrokerFunds(fundsCSV)
	if err != nil || len(funds) < 2 {
		return nil, errors.New("invalid funds series")
	}
	flows, unique, _, externalFailures, err := parseAndAuditCashFlows(flowsCSV, funds)
	if err != nil || unique != len(flows) || externalFailures != 0 {
		return nil, errors.New("cash flow audit failed")
	}
	net, repo := map[string]int64{}, map[string]int64{}
	repoIDs := map[string][]string{}
	for _, f := range flows {
		if f.FlowID == "" || f.InCents < 0 || f.OutCents < 0 || (f.InCents != 0 && f.OutCents != 0) {
			return nil, errors.New("invalid cash flow")
		}
		net[f.TradeDate] += f.InCents - f.OutCents
		if f.TradeDate < spec.From || f.TradeDate > "20261109" || f.Subject != "回购融券成交划出资金" {
			continue
		}
		match := brokerBasis20261110Repo.FindStringSubmatch(f.Summary)
		if f.Code != "204001" || match == nil || f.InCents != 0 {
			return nil, errors.New("unverified reverse repo contract")
		}
		lots, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || lots <= 0 || lots > 100000000 {
			return nil, errors.New("invalid repo lots")
		}
		principal := lots * 100000 // Broker export lots, not OC's quantity unit.
		if f.OutCents < principal || f.OutCents-principal > principal/100 {
			return nil, errors.New("invalid repo debit")
		}
		repo[f.TradeDate] += principal
		repoIDs[f.TradeDate] = append(repoIDs[f.TradeDate], f.FlowID)
	}
	result := make([]brokerBasis20261110Day, 0, spec.Days)
	for i := 1; i < len(funds); i++ {
		f, p := funds[i], funds[i-1]
		if f.CashCents-p.CashCents != net[f.TradeDate] || f.TotalCents-p.TotalCents != f.DepositCents-f.WithdrawalCents+f.DailyPnLCents {
			return nil, fmt.Errorf("funds bridge mismatch %s", f.TradeDate)
		}
		if f.TradeDate < spec.From || f.TradeDate > "20261109" {
			continue
		}
		if f.DepositCents != 0 || f.WithdrawalCents != 0 {
			return nil, errors.New("external flow outside reviewed repair")
		}
		if len(repoIDs[f.TradeDate]) == 0 {
			return nil, errors.New("missing reviewed repo evidence")
		}
		date := compactToISODate(f.TradeDate)
		at, _ := time.ParseInLocation("20060102", f.TradeDate, timeutil.Location())
		path := "reference/" + spec.Account + "_资金.csv"
		raw := map[string]any{
			"broker_asset_base_authoritative": true, "economic_nav_base_confirmed": false,
			"settlement_overlay_status": "pending_review", "recurring_import": false,
			"asset_scope": "broker_reported_total_asset", "observation_precision": "trade_date_only",
			"statement_path": path, "statement_sha256": spec.FundsHash, "flows_sha256": spec.FlowsHash, "statement_row_number": f.RowNumber,
			"reported_open_total_asset": centsFloat(p.TotalCents), "reported_close_total_asset": centsFloat(f.TotalCents),
			"reported_daily_pnl": centsFloat(f.DailyPnLCents), "reported_deposit": 0, "reported_withdrawal": 0,
			"broker_customer_funds": centsFloat(f.CashCents), "broker_security_market_value": centsFloat(f.MarketCents),
			"verified_reverse_repo_principal": centsFloat(repo[f.TradeDate]), "reverse_repo_flow_ids": repoIDs[f.TradeDate],
			"broker_unclassified_asset_component": centsFloat(f.TotalCents - f.CashCents - f.MarketCents - repo[f.TradeDate]),
			"cash_available_unverified":           true, "cause_of_oc_difference": "unverified",
			"confirmation":           "user_20261110_use_broker_export_as_asset_authority",
			"economic_nav_published": false,
		}
		asset := trading.Asset{AccountID: spec.Account, CashTotal: centsFloat(f.CashCents), NetAsset: centsFloat(f.TotalCents), MarketValue: centsFloat(f.MarketCents), ReverseRepoReceivable: centsFloat(repo[f.TradeDate]), DayProfit: centsFloat(f.DailyPnLCents), UpdatedAt: at}
		gold := ledger.PerformanceNAVGold{AccountID: spec.Account, TradeDate: date, Status: "confirmed", CarriedOpenAsset: centsFloat(p.TotalCents), CloseAsset: centsFloat(f.TotalCents), DailyPnL: centsFloat(f.DailyPnLCents), AssetScope: "broker_reported_total_asset", Source: brokerStatementGoldSource, SourceRef: path + "#sha256=" + spec.FundsHash, RawPayload: raw}
		result = append(result, brokerBasis20261110Day{spec.Account, date, asset, raw, gold})
	}
	if len(result) != spec.Days {
		return nil, errors.New("reviewed date coverage changed")
	}
	return result, nil
}

func loadBrokerBasis20261110() ([]brokerBasis20261110Day, error) {
	var result []brokerBasis20261110Day
	for _, s := range brokerBasis20261110Specs {
		f, h, err := readBrokerCSV("reference/" + s.Account + "_资金.csv")
		if err != nil || h != s.FundsHash {
			return nil, errors.New("reviewed funds file changed")
		}
		c, h, err := readBrokerCSV("reference/" + s.Account + "_资金流水.csv")
		if err != nil || h != s.FlowsHash {
			return nil, errors.New("reviewed cash file changed")
		}
		days, err := buildBrokerBasis20261110(s, f, c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Account, err)
		}
		result = append(result, days...)
	}
	return result, nil
}

func runBrokerBasis20261110(args []string) error {
	flags := flag.NewFlagSet("performance-broker-basis-20261110", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	output := flags.String("output", "", "new private report")
	persist := flags.Bool("persist", false, "store reported asset bases only, never publish NAV")
	backup := flags.String("backup", "", "fresh custom PostgreSQL archive")
	backupHash := flags.String("backup-sha256", "", "verified archive digest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	days, err := loadBrokerBasis20261110()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if *persist {
		stat, err := os.Stat(*backup)
		if err != nil || stat.Size() < 1024 || time.Since(stat.ModTime()) > 2*time.Hour || stat.ModTime().After(time.Now().Add(time.Minute)) {
			return errors.New("fresh backup required")
		}
		h, err := recovery20261027FileHash(*backup)
		if err != nil || h != *backupHash || len(h) != 64 {
			return errors.New("backup digest mismatch")
		}
		if err := exec.CommandContext(ctx, "pg_restore", "--list", *backup).Run(); err != nil {
			return errors.New("invalid backup archive")
		}
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("reviewed production database required")
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: !*persist})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	if *persist {
		if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext('relay-broker-basis-20261110'))"); err != nil {
			return err
		}
	}
	repo := ledger.NewRepository(tx)
	saved, skipped := 0, 0
	for _, d := range days {
		gold, err := repo.ListPerformanceNAVGold(ctx, ledger.PerformanceNAVGoldQuery{AccountID: d.Account, DateFrom: d.Date, DateTo: d.Date, Source: brokerStatementGoldSource})
		if err != nil {
			return err
		}
		old, err := repo.GetAssetPositionObservation(ctx, d.Account, d.Date, "reconcile")
		if err == nil {
			a, _ := json.Marshal(old.RawPayload)
			b, _ := json.Marshal(d.Raw)
			if old.Source != brokerBasis20261110Source || string(a) != string(b) || old.NetAsset != d.Asset.NetAsset || old.CashTotal != d.Asset.CashTotal || old.MarketValue != d.Asset.MarketValue || old.ReverseRepoReceivable != d.Asset.ReverseRepoReceivable {
				return errors.New("existing reconciliation conflicts with reviewed basis")
			}
			if len(gold) != 1 || gold[0].Status != "confirmed" || gold[0].AssetScope != d.Gold.AssetScope || gold[0].CarriedOpenAsset != d.Gold.CarriedOpenAsset || gold[0].CloseAsset != d.Gold.CloseAsset || gold[0].DailyPnL != d.Gold.DailyPnL || gold[0].SourceRef != d.Gold.SourceRef {
				return errors.New("existing gold is missing or differs from repaired basis")
			}
			skipped++
			continue
		} else if !errors.Is(err, ledger.ErrAssetNotFound) {
			return err
		}
		if len(gold) != 0 {
			return errors.New("existing gold requires separate review")
		}
		if !*persist {
			continue
		}
		d.Gold.ConfirmedBy = "user_authorized_relay_20261110"
		d.Gold.ConfirmedAt = timeutil.Now()
		if _, err := repo.UpsertPerformanceNAVGold(ctx, d.Gold); err != nil {
			return err
		}
		if err := repo.UpsertAssetSnapshotForDate(ctx, d.Asset, d.Date, "reconcile", brokerBasis20261110Source, d.Raw, d.Asset.UpdatedAt); err != nil {
			return err
		}
		saved++
	}
	// Validate files again before committing; a new user export requires a fresh review.
	current, err := loadBrokerBasis20261110()
	if err != nil {
		return err
	}
	a, _ := json.Marshal(days)
	b, _ := json.Marshal(current)
	if string(a) != string(b) {
		return errors.New("source changed during repair")
	}
	if err := writeETFTransferReviewFile(*output, map[string]any{"phase": "prepared_not_committed", "persist_requested": *persist, "planned_rows": len(days), "rows_prepared": saved, "identical_rows_skipped": skipped, "nav_writes": 0, "original_oc_writes": 0, "plans": days, "backup_sha256": *backupHash}); err != nil {
		return err
	}
	if *persist {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return writeJSON(map[string]any{"committed": *persist, "asset_bases_saved": saved, "gold_saved": saved, "identical_rows_skipped": skipped, "nav_writes": 0, "remaining": "ETF settlement overlays and T0 cost continuity require a separate sequential NAV rebuild"})
}
