package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const recovery20261016Source = "broker_historical_funds_statement_one_time_audit_20261016"

// This dated recovery is deliberately not a general import or scheduled task.
func runPerformanceRecovery20261016(args []string) error {
	flags := flag.NewFlagSet("performance-recovery-20261016", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	persist := flags.Bool("persist", false, "commit only if every rebuilt cost and NAV passes")
	backup := flags.String("backup", "", "verified pre-write PostgreSQL archive")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *persist {
		if info, err := os.Stat(*backup); err != nil || info.Size() == 0 {
			return errors.New("a nonempty pre-write -backup archive is required")
		}
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("this recovery only applies to the reviewed production ledger")
	}
	hashes := map[string]string{
		"reference/307000051387_资金.csv":   "4dfd133fb034b2d767b4f9a92a519a3e1ba95badfe799fdb3f6fe6adabf6845c",
		"reference/307000051387_资金流水.csv": "41e259cb9c2dd2d75b006ce37b0fcc44faf12f2e25c64722069317b039c15127",
		"reference/307000051388_资金.csv":   "4188f112a894aff265b944f0fc3f1ff20a6cb808ae2f0a231bea3af18de0b4a8",
		"reference/307000051388_资金流水.csv": "c073c1eb8ce7fe5e7fbb43405f17fbb9aa3c4048963837337be88fd0dff36a84",
	}
	for path, want := range hashes {
		_, got, err := readBrokerCSV(path)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("reviewed evidence changed: %s", path)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	var databaseName string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		return err
	}
	if databaseName != "relay_trader" {
		return errors.New("connected database does not match reviewed production ledger")
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	pcf, err := client.MarketETFCashComponents(ctx, url.Values{
		"security_id": {"510310.SH"}, "trade_date": {"20261012"}, "limit": {"5"},
	})
	if err != nil || pcf.StatusCode != 200 {
		return fmt.Errorf("Meridian final cash component unavailable: status=%d error=%v", pcf.StatusCode, err)
	}
	if err := verifyRecovery20261016PCF(pcf.Payload); err != nil {
		return err
	}
	from, _ := parseCLITradeDate("20261009")
	to, _ := parseCLITradeDate("20261015")
	plan, err := buildBrokerFundsAuditPlan(ctx, ledger.NewRepository(db), "307000051387", "reference/307000051387_资金.csv", "", from, to, 0)
	if err != nil {
		return err
	}
	if !plan.report.QualityGatePassed || len(plan.report.Days) != 5 || plan.report.ExternalFlowRows != 0 {
		return errors.New("387 broker asset identity or recovery scope differs from reviewed evidence")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext('relay-performance-recovery-20261016'))"); err != nil {
		return err
	}
	repo := ledger.NewRepository(tx)
	now := timeutil.Now()
	finalization := recovery20261016Finalization(now)
	finalization.RawPayload["meridian_cash_component_response"] = pcf.Payload
	finalization.RawPayload["evidence_sha256"] = hashes
	if _, err := repo.UpsertETFSettlementFinalization(ctx, finalization); err != nil {
		return err
	}
	for _, entry := range recovery20261016CashEntries() {
		if err := ensureRecovery20261016Cash(ctx, repo, entry, now); err != nil {
			return err
		}
	}
	for _, day := range plan.report.Days {
		openCarry, closeCarry, repoPrincipal := recovery20261016Carry(day.TradeDate)
		raw := map[string]any{
			"economic_nav_base_confirmed": true, "recurring_import": false,
			"asset_scope":    "broker_reported_total_asset_excluding_fund_occupancy",
			"statement_path": plan.statement, "statement_sha256": plan.digest, "statement_row_number": day.StatementRowNumber,
			"reported_open_total_asset": day.ReportedOpenAsset, "reported_close_total_asset": day.ReportedCloseAsset,
			"reported_daily_pnl": day.ReportedDailyPnL, "reported_deposit": day.ReportedDeposit, "reported_withdrawal": day.ReportedWithdrawal,
			"open_outstanding_etf_settlement_asset": openCarry, "close_outstanding_etf_settlement_asset": closeCarry,
			"broker_other_asset": day.BrokerOtherAsset, "verified_reverse_repo_principal": repoPrincipal,
			"other_asset_not_classified_as_repo": math.Round((day.BrokerOtherAsset-repoPrincipal)*100) / 100,
			"recovery_reason":                    "verified broker cash bridge with separate refund receivable and cash-component payable",
			"evidence_sha256":                    hashes, "audit_document": "docs/BROKER_CASH_EVIDENCE_REVIEW_20261016.md",
		}
		at, _ := time.ParseInLocation("2006-01-02 15:30:00", day.TradeDate+" 15:30:00", timeutil.Location())
		asset := trading.Asset{AccountID: "307000051387", CashAvailable: day.BrokerCash, CashTotal: day.BrokerCash,
			NetAsset: day.ReportedCloseAsset, MarketValue: day.BrokerMarketValue, ReverseRepoReceivable: repoPrincipal,
			DayProfit: day.ReportedDailyPnL, UpdatedAt: at}
		if err := repo.UpsertAssetSnapshotForDate(ctx, asset, day.TradeDate, "reconcile", recovery20261016Source, raw, at); err != nil {
			return err
		}
	}
	service, err := performance.New(performance.Options{
		Store: repo, Calendar: client, Market: client, FormulaVersion: cfg.Performance.FormulaVersion,
		ETFT0FrictionRate: cfg.Performance.ETFT0FrictionRate,
		AutoToleranceCNY:  cfg.Performance.AutoToleranceCNY, AutoToleranceBP: cfg.Performance.AutoToleranceBP,
		WarningToleranceCNY: cfg.Performance.WarningToleranceCNY, WarningToleranceBP: cfg.Performance.WarningToleranceBP,
	})
	if err != nil {
		return err
	}
	report := performanceRebuildReport{DateFrom: "2026-10-09", DateTo: "2026-10-16", Persist: *persist}
	allPassed := true
	for _, date := range []string{"2026-10-09", "2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15", "2026-10-16"} {
		accounts := []string{"307000051387"}
		if date == "2026-10-16" {
			accounts = append(accounts, "307000051388")
		}
		for _, account := range accounts {
			item := performanceRebuildItem{AccountID: account, TradeDate: date}
			item.Cost, err = service.CalculateCostLedger(ctx, account, date, performance.CostLedgerOptions{Persist: true})
			if err != nil {
				return err
			}
			item.NAV, err = service.CalculateEconomicNAV(ctx, account, date, performance.EconomicNAVOptions{})
			if err != nil {
				return err
			}
			if performanceResultPublishable(item.Cost.Status, item.NAV.Status) {
				item.NAV, err = service.CalculateEconomicNAV(ctx, account, date, performance.EconomicNAVOptions{Persist: true, Status: "provisional"})
				if err != nil {
					return err
				}
			} else {
				allPassed = false
			}
			report.Items = append(report.Items, item)
		}
	}
	if err := writeJSON(report); err != nil {
		return err
	}
	if !allPassed {
		return errors.New("recovery rolled back: at least one cost or NAV remains blocked")
	}
	if *persist {
		return tx.Commit()
	}
	return nil
}

func recovery20261016Finalization(at time.Time) ledger.ETFSettlementFinalization {
	return ledger.ETFSettlementFinalization{
		AccountID: "307000051387", SourceTradeDate: "2026-10-09", SecurityID: "510310.SH",
		Status: "confirmed", SettlementComplete: true, RedemptionQuantity: 900000, RedemptionUnit: 900000,
		BuyGrossAmount: 3771000, ComponentSaleGrossAmount: 2344211, ActualCashSubstitution: 1440127.62,
		ActualCashComponent: -1817.21, ActualTotalFee: 1740.18, SourceCloseSettlementCarry: 172982.71,
		GrossContribution: 11521.41, NetContribution: 9781.23,
		PCFTradeDate: "2026-10-12", PCFSchemaVersion: "etf_cash_component.v1",
		Source: recovery20261016Source, ConfirmedBy: "user_authorized_relay_20261016", ConfirmedAt: at,
		RawPayload: map[string]any{
			"recurring_import": false, "redemption_gateway_order_id": "external-huaxin-30700005138701-110010621508254",
			"source_day_cash_already_in_broker_basis": 1265327.70,
			"source_refund_receivable":                174799.92, "source_cash_component_payable": -1817.21,
			"fee_breakdown":         map[string]any{"buy": 188.55, "component_sales": 1512.91, "redemption_transfer": 38.72},
			"oc_original_total_fee": 1778.888785, "fee_difference_pending_oc_explanation": 38.708785,
			"component_sales_securities": 174, "component_sales_quantity": 157200,
			"audit_document": "docs/BROKER_CASH_EVIDENCE_REVIEW_20261016.md",
		},
	}
}

func recovery20261016Carry(date string) (float64, float64, float64) {
	switch date {
	case "2026-10-09":
		return 0, 172982.71, 258000
	case "2026-10-12":
		return 172982.71, -1817.21, 253000
	case "2026-10-13":
		return -1817.21, 0, 250000
	case "2026-10-14":
		return 0, 0, 2317000
	default:
		return 0, 0, 1997000
	}
}

func recovery20261016CashEntries() []ledger.CashLedgerEntry {
	entries := make([]ledger.CashLedgerEntry, 0, 3)
	group := "broker-final:307000051387:20261009:510310.SH"
	for i, leg := range []struct {
		date, at, id, kind string
		amount             float64
	}{
		{"2026-10-12", "2026-10-12T19:05:02+08:00", "broker-final-387-20261009-refund", "cash_substitution_refund", 174799.92},
		{"2026-10-13", "2026-10-13T19:27:24+08:00", "broker-final-387-20261009-cash-component", "cash_component_payment", -1817.21},
	} {
		at, _ := time.Parse(time.RFC3339, leg.at)
		raw := map[string]any{
			"settlement_kind": "etf_redemption_final_settlement", "source_trade_date": "2026-10-09",
			"source_accrual_amount": leg.amount, "released_estimate": leg.amount,
			"source_accrual_scope": "separate_actual_receivable_or_payable_leg", "settlement_leg": leg.kind,
			"source_net_settlement_carry": 172982.71, "settlement_group_id": group, "receipt_stage": "initial",
			"confirmation_source": "broker_cash_flow_and_meridian_pcf", "recurring_import": false,
			"statement_sha256": "41e259cb9c2dd2d75b006ce37b0fcc44faf12f2e25c64722069317b039c15127",
		}
		if i == 1 {
			raw["receipt_stage"] = "supplemental"
			raw["prior_receipt_entry_id"] = entries[0].EntryID
			raw["broker_serial"] = "2732649990"
		}
		entries = append(entries, ledger.CashLedgerEntry{
			EntryID: leg.id, AccountID: "307000051387", TradeDate: leg.date, LedgerType: "settlement",
			FlowClass: "settlement_adjustment", Amount: leg.amount, Currency: "CNY", EffectiveAt: at,
			CashBucket: "broker_total_cash", CounterpartyBucket: "etf_fund_settlement", Status: "draft",
			IdempotencyKey: leg.id, Source: recovery20261016Source, CreatedBy: "user_authorized_relay_20261016",
			GatewayOrderID: "external-huaxin-30700005138701-110010621508254",
			Description:    "已核实券商清算收付；正退款与负现金差额分开释放", RawPayload: raw,
		})
	}
	at, _ := time.Parse(time.RFC3339, "2026-10-16T09:55:53+08:00")
	entries = append(entries, ledger.CashLedgerEntry{
		EntryID: "user-confirmed-388-deposit-20261016-095553", AccountID: "307000051388", TradeDate: "2026-10-16",
		LedgerType: "deposit", FlowClass: "external_flow", Currency: "CNY", Amount: 33000000, EffectiveAt: at,
		CashBucket: "broker_total_cash", CounterpartyBucket: "bank", Status: "draft",
		IdempotencyKey: "user-confirmed-388-deposit-20261016-095553", Source: "user_confirmed_external_flow",
		CreatedBy: "user_authorized_relay_20261016", Description: "用户确认3300万元外部入金，精确到账09:55:53",
		RawPayload: map[string]any{"confirmation_source": "user_explicit_amount_and_timestamp", "recurring_import": false,
			"timing_evidence": "user confirmed 2026-10-16 09:55:53 Asia/Shanghai; supersedes approximate market-open time"},
	})
	return entries
}

func ensureRecovery20261016Cash(ctx context.Context, repo *ledger.Repository, entry ledger.CashLedgerEntry, at time.Time) error {
	items, err := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: entry.AccountID, TradeDate: entry.TradeDate, Limit: 500})
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.EntryID == entry.EntryID {
			if item.Status != "confirmed" || item.Amount != entry.Amount || !item.EffectiveAt.Equal(entry.EffectiveAt) || item.FlowClass != entry.FlowClass {
				return fmt.Errorf("existing cash evidence conflicts: %s", entry.EntryID)
			}
			return nil
		}
		if item.Status == "confirmed" && item.FlowClass == entry.FlowClass && item.Amount == entry.Amount && item.EffectiveAt.Equal(entry.EffectiveAt) {
			return fmt.Errorf("possible duplicate evidence under a different identity: %s", item.EntryID)
		}
	}
	if _, err := repo.CreateCashLedgerEntry(ctx, entry); err != nil {
		return err
	}
	_, err = repo.ConfirmCashLedgerEntry(ctx, entry.AccountID, entry.EntryID, "user_authorized_relay_20261016", at)
	return err
}

func verifyRecovery20261016PCF(payload map[string]any) error {
	data, ok := payload["data"].([]any)
	if !ok || len(data) != 1 {
		return errors.New("expected one Meridian final cash-component row")
	}
	row, ok := data[0].(map[string]any)
	if !ok || fmt.Sprint(row["security_id"]) != "510310.SH" || fmt.Sprint(row["schema_version"]) != "etf_cash_component.v1" {
		return errors.New("Meridian PCF identity mismatch")
	}
	value := func(key string) float64 {
		number, err := strconv.ParseFloat(fmt.Sprint(row[key]), 64)
		if err != nil {
			return math.NaN()
		}
		return number
	}
	if value("pre_trade_date") != 20261009 || value("trade_date") != 20261012 || value("unit_subscribe_redeem") != 900000 || value("cash_component") != -1817.21 {
		return errors.New("Meridian final cash component no longer matches reviewed settlement")
	}
	return nil
}
