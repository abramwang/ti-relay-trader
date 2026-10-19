package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const fuying13RecoverySource = "broker_historical_funds_statement_one_time_audit_fuying13_20261019"
const fuying13Account = "314000045768"

type fuying13RecoveryPlan struct {
	FundsRows            int                      `json:"funds_rows"`
	FlowRows             int                      `json:"flow_rows"`
	VerifiedAdjacentDays int                      `json:"verified_adjacent_days"`
	InceptionDate        string                   `json:"inception_date"`
	PreviousAsset        float64                  `json:"previous_asset"`
	Days                 []brokerFundsAuditDay    `json:"days"`
	CashEntries          []ledger.CashLedgerEntry `json:"cash_entries"`
	RepoDebits           []brokerCashFlowRow      `json:"repo_debits"`
	EvidenceSHA256       map[string]string        `json:"evidence_sha256"`
}

// This one-off recovery is restricted to a reviewed settlement-free segment.
func buildFuying13RecoveryPlan(fundsData, flowData *brokerCSV) (fuying13RecoveryPlan, error) {
	plan := fuying13RecoveryPlan{FundsRows: len(fundsData.rows), FlowRows: len(flowData.rows), InceptionDate: "2026-09-09"}
	for _, row := range fundsData.rows {
		account, err := fundsData.value(row, "客户号")
		if err != nil || strings.TrimSpace(account) != fuying13Account {
			return plan, errors.New("funds account identity mismatch")
		}
	}
	// These reviewed exports omit the advertised balance column, not the summary.
	for _, row := range flowData.rows {
		if len(row) != 13 || row[2] != "31400004576801" || row[7] != fuying13Account {
			return plan, errors.New("unexpected flow shape or account identity")
		}
	}
	funds, _, err := parseBrokerFunds(fundsData)
	if err != nil {
		return plan, err
	}
	flows, unique, _, externalFailures, err := parseAndAuditCashFlows(flowData, funds)
	if err != nil {
		return plan, err
	}
	if len(flows) != unique || externalFailures != 0 {
		return plan, errors.New("duplicate flow identities or inconsistent external flows")
	}
	dailyNet := make(map[string]int64)
	for _, flow := range flows {
		dailyNet[flow.TradeDate] += flow.InCents - flow.OutCents
		if flow.TradeDate < "20260909" || flow.TradeDate > "20261009" {
			continue
		}
		if strings.HasPrefix(flow.Subject, "ETF") || strings.Contains(flow.Summary, "ETF赎回") || strings.Contains(flow.Summary, "ETF申赎") {
			return plan, fmt.Errorf("ETF settlement evidence on %s requires a different recovery", flow.TradeDate)
		}
		if flow.Subject == "回购融券成交划出资金" {
			plan.RepoDebits = append(plan.RepoDebits, flow)
		}
		if flow.Subject != "银证转帐取" && flow.Subject != "银证转帐存" && flow.Subject != "季度利息入本金" {
			continue
		}
		at, err := time.ParseInLocation("20060102 15:04:05", flow.TradeDate+" "+flow.TradeTime, timeutil.Location())
		if err != nil {
			return plan, err
		}
		class := "external_flow"
		if flow.Subject == "季度利息入本金" {
			class = "income_expense"
		}
		plan.CashEntries = append(plan.CashEntries, ledger.CashLedgerEntry{
			EntryID: "broker-fuying13-20261019-" + flow.FlowID, AccountID: fuying13Account,
			TradeDate: compactToISODate(flow.TradeDate), LedgerType: "adjustment", FlowClass: class,
			Currency: "CNY", Amount: centsFloat(flow.InCents - flow.OutCents), CashBucket: "broker_account",
			EffectiveAt: at, Status: "draft", Description: flow.Subject + ": " + flow.Summary,
			Source: fuying13RecoverySource, CreatedBy: "user_authorized_relay_20261019",
			RawPayload: map[string]any{"recurring_import": false, "broker_flow_id": flow.FlowID, "broker_subject": flow.Subject},
		})
	}
	for index, row := range funds {
		if index == 0 {
			continue
		}
		previous := funds[index-1]
		if row.TotalCents != previous.TotalCents+row.DepositCents-row.WithdrawalCents+row.DailyPnLCents ||
			row.CashCents-previous.CashCents != dailyNet[row.TradeDate] {
			return plan, fmt.Errorf("broker asset or cash bridge mismatch on %s", row.TradeDate)
		}
		plan.VerifiedAdjacentDays++
		if row.TradeDate < "20260909" || row.TradeDate > "20261009" {
			continue
		}
		if len(plan.Days) == 0 {
			if row.TradeDate != "20260909" || previous.TradeDate != "20260908" {
				return plan, errors.New("reviewed opening anchor is missing")
			}
			plan.PreviousAsset = centsFloat(previous.TotalCents)
		}
		plan.Days = append(plan.Days, brokerFundsAuditDay{
			TradeDate: compactToISODate(row.TradeDate), ReportedOpenAsset: centsFloat(previous.TotalCents),
			ReportedCloseAsset: centsFloat(row.TotalCents), ReportedDailyPnL: centsFloat(row.DailyPnLCents),
			ReportedDeposit: centsFloat(row.DepositCents), ReportedWithdrawal: centsFloat(row.WithdrawalCents),
			BrokerCash: centsFloat(row.CashCents), BrokerMarketValue: centsFloat(row.MarketCents),
			BrokerOtherAsset: centsFloat(row.TotalCents - row.CashCents - row.MarketCents), StatementRowNumber: row.RowNumber,
		})
	}
	if len(plan.Days) == 0 || plan.Days[len(plan.Days)-1].TradeDate != "2026-10-09" {
		return plan, errors.New("reviewed recovery segment is incomplete")
	}
	return plan, nil
}

func runFuying13Recovery20261019(args []string) error {
	flags := flag.NewFlagSet("performance-fuying13-recovery-20261019", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	persist := flags.Bool("persist", false, "commit only if every selected cost and NAV passes")
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
		return errors.New("recovery only applies to the reviewed production ledger")
	}
	paths := []string{"reference/314000045768_资金.csv", "reference/314000045768_资金流水.csv"}
	want := []string{"42aeddc2b0e16ed84f0cee464aac8cc61a5ba38f178417eb8a269534c81a9d9f", "77c891fd35f2f669c666a5927dd16d5f9785b296dd76c167e74c75db2516cc39"}
	data := make([]*brokerCSV, 2)
	hashes := make(map[string]string)
	for index, path := range paths {
		var hash string
		data[index], hash, err = readBrokerCSV(path)
		if err != nil {
			return err
		}
		if hash != want[index] {
			return fmt.Errorf("reviewed evidence changed: %s", path)
		}
		hashes[path] = hash
	}
	plan, err := buildFuying13RecoveryPlan(data[0], data[1])
	if err != nil {
		return err
	}
	plan.EvidenceSHA256 = hashes
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var databaseName string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&databaseName); err != nil || databaseName != "relay_trader" {
		return errors.New("unexpected recovery database")
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext('relay-fuying13-recovery-20261019'))"); err != nil {
		return err
	}
	repo := ledger.NewRepository(tx)
	if inception, err := repo.GetPerformanceInception(ctx, fuying13Account); err == nil {
		if inception.RawPayload["recovery_source"] != fuying13RecoverySource {
			return errors.New("an unrelated inception already exists")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := timeutil.Now()
	if _, err := repo.UpsertPerformanceInception(ctx, ledger.PerformanceInception{
		AccountID: fuying13Account, InceptionDate: plan.InceptionDate, Status: "confirmed", CleanStart: false,
		OpeningPositionSource: "oc_open_snapshot", CostSource: "meridian_pre_close_mark_to_market",
		Description: "One-time verified segment; existing holdings marked to prior close, not account inception or historical acquisition cost",
		ConfirmedBy: "user_authorized_relay_20261019", ConfirmedAt: now,
		RawPayload: map[string]any{"recovery_source": fuying13RecoverySource, "evidence_sha256": hashes,
			"recurring_import": false, "verified_segment_end": "2026-10-09", "earlier_etf_settlements_unresolved": true},
	}); err != nil {
		return err
	}
	for _, entry := range plan.CashEntries {
		entry.RawPayload["evidence_sha256"] = hashes
		if err := ensureFuying13RecoveryCash(ctx, repo, entry, now); err != nil {
			return err
		}
	}
	feesSaved := 0
	for _, flow := range plan.RepoDebits {
		orders, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: compactToISODate(flow.TradeDate), Symbol: "204001", Limit: 500})
		if err != nil {
			return err
		}
		if len(orders) != 1 || orders[0].Exchange != trading.ExchangeSH || orders[0].CumFilledQty <= 0 || orders[0].Status != trading.OrderStatusFilled {
			return fmt.Errorf("repo debit lacks a unique completed order on %s", flow.TradeDate)
		}
		order := orders[0]
		principalCents := order.CumFilledQty * 10000
		feeCents := flow.OutCents - flow.InCents - principalCents
		if feeCents < 0 || !strings.Contains(flow.Summary, fmt.Sprintf(")%d手", order.CumFilledQty/10)) {
			return fmt.Errorf("repo debit quantity or principal mismatch on %s", flow.TradeDate)
		}
		records, err := repo.ListOrderFeeRecords(ctx, ledger.OrderFeeRecordQuery{AccountID: fuying13Account, TradeDate: compactToISODate(flow.TradeDate), GatewayOrderID: order.GatewayOrderID, Limit: 500})
		if err != nil {
			return err
		}
		if len(records) != 0 {
			continue
		}
		at, _ := time.ParseInLocation("20060102 15:04:05", flow.TradeDate+" "+flow.TradeTime, timeutil.Location())
		if err := repo.UpsertOrderFeeRecord(ctx, ledger.OrderFeeRecord{
			AccountID: fuying13Account, FeeRecordID: "broker-fuying13-repo-" + flow.FlowID,
			TradeDate: compactToISODate(flow.TradeDate), RecordScope: "order", GatewayOrderID: order.GatewayOrderID,
			OrderID: order.OrderID, OrderStreamID: order.OrderStreamID, Symbol: "204001", Exchange: "SH",
			TradeSide: string(order.TradeSide), BusinessType: string(order.BusinessType),
			OrderAmount: centsFloat(principalCents), Turnover: centsFloat(principalCents),
			OtherFee: centsFloat(feeCents), TotalFee: centsFloat(feeCents), Currency: "CNY", FeeComplete: true,
			AssociationComplete: true, FeeSource: "broker_cash_flow_principal_delta", FeeAsOf: at,
			RawPayload: map[string]any{"recurring_import": false, "evidence_sha256": hashes, "broker_flow_id": flow.FlowID,
				"principal": centsFloat(principalCents), "debit": centsFloat(flow.OutCents), "broker_summary": flow.Summary,
				"fee_breakdown_unverified": true, "source": fuying13RecoverySource},
		}); err != nil {
			return err
		}
		feesSaved++
	}
	for _, day := range plan.Days {
		var redemptions int
		var repoPrincipal float64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE business_type='E' AND cum_filled_qty>0),
			COALESCE(sum(cum_filled_qty*100) FILTER (WHERE symbol='204001' AND exchange='SH'),0)
			FROM orders WHERE account_id=$1 AND trade_date=$2`, fuying13Account, day.TradeDate).Scan(&redemptions, &repoPrincipal); err != nil {
			return err
		}
		if redemptions != 0 || math.Abs(repoPrincipal-day.BrokerOtherAsset) > 0.01 {
			return fmt.Errorf("unresolved ETF activity or other asset is not verified repo principal on %s", day.TradeDate)
		}
		raw := map[string]any{
			"economic_nav_base_confirmed": true, "recurring_import": false,
			"asset_scope":    "broker_reported_total_asset_excluding_fund_occupancy",
			"statement_path": paths[0], "statement_sha256": hashes[paths[0]], "statement_row_number": day.StatementRowNumber,
			"reported_open_total_asset": day.ReportedOpenAsset, "reported_close_total_asset": day.ReportedCloseAsset,
			"reported_daily_pnl": day.ReportedDailyPnL, "reported_deposit": day.ReportedDeposit, "reported_withdrawal": day.ReportedWithdrawal,
			"open_outstanding_etf_settlement_asset": 0, "close_outstanding_etf_settlement_asset": 0,
			"verified_reverse_repo_principal": repoPrincipal, "evidence_sha256": hashes,
			"recovery_reason": "settlement-free segment after prior ETF cash receipts; other assets independently matched to OC reverse repo principal",
			"audit_document":  "docs/FUYING13_PERFORMANCE_RECOVERY_20261019.md",
		}
		at, _ := time.ParseInLocation("2006-01-02 15:04:05", day.TradeDate+" 15:30:00", timeutil.Location())
		if err := repo.UpsertAssetSnapshotForDate(ctx, trading.Asset{
			AccountID: fuying13Account, CashTotal: day.BrokerCash, CashAvailable: day.BrokerCash,
			MarketValue: day.BrokerMarketValue, NetAsset: day.ReportedCloseAsset, ReverseRepoReceivable: repoPrincipal,
			DayProfit: day.ReportedDailyPnL, UpdatedAt: at,
		}, day.TradeDate, "reconcile", fuying13RecoverySource, raw, at); err != nil {
			return err
		}
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
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
	report := performanceRebuildReport{DateFrom: plan.InceptionDate, DateTo: "2026-10-09", Persist: *persist}
	for _, day := range plan.Days {
		item := performanceRebuildItem{AccountID: fuying13Account, TradeDate: day.TradeDate}
		item.Cost, err = service.CalculateCostLedger(ctx, fuying13Account, day.TradeDate, performance.CostLedgerOptions{Persist: true})
		if err != nil {
			return err
		}
		item.NAV, err = service.CalculateEconomicNAV(ctx, fuying13Account, day.TradeDate, performance.EconomicNAVOptions{})
		if err != nil {
			return err
		}
		report.Items = append(report.Items, item)
		if !performanceResultPublishable(item.Cost.Status, item.NAV.Status) || math.Abs(item.NAV.NAV.AccountDayPnL-day.ReportedDailyPnL) > 0.01 {
			_ = writeJSON(map[string]any{"audit": plan, "rebuild": report})
			return fmt.Errorf("transaction rolled back: cost/NAV or broker PnL mismatch on %s", day.TradeDate)
		}
		published, err := service.CalculateEconomicNAV(ctx, fuying13Account, day.TradeDate, performance.EconomicNAVOptions{Persist: true, Status: "provisional"})
		if err != nil {
			return err
		}
		report.Items[len(report.Items)-1].NAV = published
	}
	if err := writeJSON(map[string]any{"audit": plan, "rebuild": report, "missing_repo_fees_saved": feesSaved}); err != nil {
		return err
	}
	if *persist {
		return tx.Commit()
	}
	return nil
}

func ensureFuying13RecoveryCash(ctx context.Context, repo *ledger.Repository, entry ledger.CashLedgerEntry, at time.Time) error {
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
			return fmt.Errorf("possible duplicate cash evidence: %s", item.EntryID)
		}
	}
	if _, err := repo.CreateCashLedgerEntry(ctx, entry); err != nil {
		return err
	}
	_, err = repo.ConfirmCashLedgerEntry(ctx, entry.AccountID, entry.EntryID, entry.CreatedBy, at)
	return err
}
