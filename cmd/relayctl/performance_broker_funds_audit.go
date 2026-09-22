package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const brokerFundsAuditSource = "broker_historical_funds_statement_one_time_audit"

type brokerFundsFlowPlan struct {
	EntryID             string  `json:"entry_id"`
	Amount              float64 `json:"amount"`
	EffectiveAt         string  `json:"effective_at"`
	TimingEvidence      string  `json:"timing_evidence"`
	ObservedOpenAsset   float64 `json:"observed_open_asset"`
	ExpectedBeforeFlow  float64 `json:"expected_before_flow"`
	ExpectedAfterFlow   float64 `json:"expected_after_flow"`
	ObservationResidual float64 `json:"observation_residual"`
}

type brokerFundsAuditDay struct {
	TradeDate             string               `json:"trade_date"`
	ReportedOpenAsset     float64              `json:"reported_open_asset"`
	ReportedCloseAsset    float64              `json:"reported_close_asset"`
	ReportedDailyPnL      float64              `json:"reported_daily_pnl"`
	ReportedDeposit       float64              `json:"reported_deposit"`
	ReportedWithdrawal    float64              `json:"reported_withdrawal"`
	BrokerCash            float64              `json:"broker_cash"`
	BrokerMarketValue     float64              `json:"broker_market_value"`
	BrokerOtherAsset      float64              `json:"broker_other_asset"`
	IdentityResidual      float64              `json:"identity_residual"`
	ExternalFlow          *brokerFundsFlowPlan `json:"external_flow,omitempty"`
	StatementRowNumber    int                  `json:"statement_row_number"`
	TransactionDetailRows int                  `json:"transaction_detail_rows"`
}

type brokerFundsAuditReport struct {
	AccountID               string                         `json:"account_id"`
	Persist                 bool                           `json:"persist"`
	QualityGatePassed       bool                           `json:"quality_gate_passed"`
	DateFrom                string                         `json:"date_from"`
	DateTo                  string                         `json:"date_to"`
	Files                   map[string]brokerStatementFile `json:"files"`
	FundsRows               int                            `json:"funds_rows"`
	FundsIdentityFailures   int                            `json:"funds_identity_failures"`
	SelectedRows            int                            `json:"selected_rows"`
	ExternalFlowRows        int                            `json:"external_flow_rows"`
	GoldRowsSaved           int                            `json:"gold_rows_saved,omitempty"`
	ReconcileRowsSaved      int                            `json:"reconcile_rows_saved,omitempty"`
	ExternalFlowRowsSaved   int                            `json:"external_flow_rows_saved,omitempty"`
	ExternalFlowRowsSkipped int                            `json:"external_flow_rows_skipped,omitempty"`
	Days                    []brokerFundsAuditDay          `json:"days"`
	Warnings                []string                       `json:"warnings,omitempty"`
}

type brokerTransactionEvidence struct {
	rowsByDate          map[string]int
	firstIntradayByDate map[string]string
	digest              string
	rows                int
}

type brokerFundsAuditPlan struct {
	report    brokerFundsAuditReport
	rows      []brokerFundsRow
	statement string
	digest    string
}

func runPerformanceBrokerFundsAudit(args []string) error {
	flags := flag.NewFlagSet("performance-broker-funds-audit", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountID := flags.String("account", "", "account id")
	fundsPath := flags.String("funds", "", "broker historical funds CSV")
	transactionPath := flags.String("transaction-detail", "", "broker transaction cash-detail CSV used to bound external-flow timing")
	dateFromValue := flags.String("date-from", "", "first recovery trade date, YYYYMMDD or YYYY-MM-DD")
	dateToValue := flags.String("date-to", "", "last recovery trade date, YYYYMMDD or YYYY-MM-DD")
	confirmedBy := flags.String("confirmed-by", "", "operator confirming the one-time broker evidence")
	tolerance := flags.Float64("open-evidence-tolerance", 100, "maximum CNY difference for open-snapshot flow timing evidence")
	persist := flags.Bool("persist", false, "persist gold, reconcile observations, and confirmed external flows in one transaction")
	timeout := flags.Duration("timeout", 5*time.Minute, "audit and database operation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	*accountID = strings.TrimSpace(*accountID)
	*fundsPath = strings.TrimSpace(*fundsPath)
	*transactionPath = strings.TrimSpace(*transactionPath)
	*confirmedBy = strings.TrimSpace(*confirmedBy)
	if *accountID == "" || *fundsPath == "" || strings.TrimSpace(*dateFromValue) == "" || strings.TrimSpace(*dateToValue) == "" {
		return errors.New("-account, -funds, -date-from, and -date-to are required")
	}
	if *persist && *confirmedBy == "" {
		return errors.New("-confirmed-by is required with -persist")
	}
	if *tolerance < 0 || math.IsNaN(*tolerance) || math.IsInf(*tolerance, 0) {
		return errors.New("-open-evidence-tolerance must be a finite non-negative value")
	}
	dateFrom, err := parseCLITradeDate(*dateFromValue)
	if err != nil {
		return fmt.Errorf("invalid date-from: %w", err)
	}
	dateTo, err := parseCLITradeDate(*dateToValue)
	if err != nil {
		return fmt.Errorf("invalid date-to: %w", err)
	}
	if dateTo.Before(dateFrom) {
		return errors.New("date-to must not be before date-from")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	plan, err := buildBrokerFundsAuditPlan(ctx, ledger.NewRepository(db), *accountID, *fundsPath, *transactionPath, dateFrom, dateTo, *tolerance)
	if err != nil {
		return err
	}
	plan.report.Persist = *persist
	if !plan.report.QualityGatePassed {
		_ = writeJSON(plan.report)
		return errors.New("broker funds audit quality gate failed")
	}
	if !*persist {
		return writeJSON(plan.report)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := persistBrokerFundsAudit(ctx, ledger.NewRepository(tx), plan, *confirmedBy, timeutil.Now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return writeJSON(plan.report)
}

func buildBrokerFundsAuditPlan(ctx context.Context, repo *ledger.Repository, accountID, fundsPath, transactionPath string, dateFrom, dateTo time.Time, tolerance float64) (*brokerFundsAuditPlan, error) {
	data, digest, err := readBrokerCSV(fundsPath)
	if err != nil {
		return nil, fmt.Errorf("read funds: %w", err)
	}
	for index, raw := range data.rows {
		value, valueErr := data.value(raw, "客户号")
		if valueErr != nil {
			return nil, valueErr
		}
		if strings.TrimSpace(value) != accountID {
			return nil, fmt.Errorf("funds row %d belongs to account %q", index+2, value)
		}
	}
	rows, failures, err := parseBrokerFunds(data)
	if err != nil {
		return nil, err
	}
	evidence := brokerTransactionEvidence{rowsByDate: map[string]int{}, firstIntradayByDate: map[string]string{}}
	files := map[string]brokerStatementFile{
		"资金": {Path: fundsPath, SHA256: digest, Rows: len(data.rows)},
	}
	if transactionPath != "" {
		evidence, err = readBrokerTransactionEvidence(accountID, transactionPath)
		if err != nil {
			return nil, err
		}
		files["资金流水"] = brokerStatementFile{Path: transactionPath, SHA256: evidence.digest, Rows: evidence.rows}
	}

	fromCompact := dateFrom.Format("20060102")
	toCompact := dateTo.Format("20060102")
	selected := make([]brokerFundsRow, 0)
	previousByDate := make(map[string]brokerFundsRow)
	for index, row := range rows {
		if row.TradeDate < fromCompact || row.TradeDate > toCompact {
			continue
		}
		if index == 0 {
			return nil, fmt.Errorf("selected first row %s has no preceding broker asset row", row.TradeDate)
		}
		selected = append(selected, row)
		previousByDate[row.TradeDate] = rows[index-1]
	}
	if len(selected) == 0 {
		return nil, errors.New("no broker funds rows in selected date range")
	}
	report := brokerFundsAuditReport{
		AccountID: accountID, DateFrom: compactToISODate(selected[0].TradeDate), DateTo: compactToISODate(selected[len(selected)-1].TradeDate),
		Files: files, FundsRows: len(rows), FundsIdentityFailures: failures, SelectedRows: len(selected), Days: make([]brokerFundsAuditDay, 0, len(selected)),
	}
	for _, row := range selected {
		previous := previousByDate[row.TradeDate]
		identityResidual := row.TotalCents - previous.TotalCents - row.DepositCents + row.WithdrawalCents - row.DailyPnLCents
		otherAsset := row.TotalCents - row.CashCents - row.MarketCents
		day := brokerFundsAuditDay{
			TradeDate: compactToISODate(row.TradeDate), ReportedOpenAsset: centsFloat(previous.TotalCents), ReportedCloseAsset: centsFloat(row.TotalCents),
			ReportedDailyPnL: centsFloat(row.DailyPnLCents), ReportedDeposit: centsFloat(row.DepositCents), ReportedWithdrawal: centsFloat(row.WithdrawalCents),
			BrokerCash: centsFloat(row.CashCents), BrokerMarketValue: centsFloat(row.MarketCents), BrokerOtherAsset: centsFloat(otherAsset),
			IdentityResidual: centsFloat(identityResidual), StatementRowNumber: row.RowNumber, TransactionDetailRows: evidence.rowsByDate[row.TradeDate],
		}
		if identityResidual != 0 || otherAsset < 0 {
			report.Days = append(report.Days, day)
			continue
		}
		externalCents := row.DepositCents - row.WithdrawalCents
		if externalCents != 0 {
			report.ExternalFlowRows++
			flow, flowErr := inferBrokerFundsFlow(ctx, repo, accountID, row, previous, evidence, tolerance)
			if flowErr != nil {
				return nil, flowErr
			}
			day.ExternalFlow = &flow
		}
		report.Days = append(report.Days, day)
	}
	report.QualityGatePassed = failures == 0
	for _, day := range report.Days {
		if math.Abs(day.IdentityResidual) > 0.000001 || day.BrokerOtherAsset < 0 || (day.ReportedDeposit != 0 || day.ReportedWithdrawal != 0) && day.ExternalFlow == nil {
			report.QualityGatePassed = false
		}
	}
	report.Warnings = append(report.Warnings, "one-time audited recovery only; this command is not scheduled and does not replace OC daily data")
	return &brokerFundsAuditPlan{report: report, rows: selected, statement: fundsPath, digest: digest}, nil
}

func readBrokerTransactionEvidence(accountID, path string) (brokerTransactionEvidence, error) {
	data, digest, err := readBrokerCSV(path)
	if err != nil {
		return brokerTransactionEvidence{}, fmt.Errorf("read transaction detail: %w", err)
	}
	result := brokerTransactionEvidence{rowsByDate: make(map[string]int), firstIntradayByDate: make(map[string]string), digest: digest, rows: len(data.rows)}
	for index, raw := range data.rows {
		owner, err := data.value(raw, "客户号")
		if err != nil {
			return result, err
		}
		if owner != accountID {
			return result, fmt.Errorf("transaction detail row %d belongs to account %q", index+2, owner)
		}
		tradeDate, err := data.value(raw, "成交日期")
		if err != nil {
			return result, err
		}
		direction, err := data.value(raw, "方向")
		if err != nil {
			return result, err
		}
		if direction == "手工冻结" {
			continue
		}
		result.rowsByDate[tradeDate]++
		tradeTime, err := data.value(raw, "成交时间")
		if err != nil {
			return result, err
		}
		if tradeTime >= "09:00:00" && (result.firstIntradayByDate[tradeDate] == "" || tradeTime < result.firstIntradayByDate[tradeDate]) {
			result.firstIntradayByDate[tradeDate] = tradeTime
		}
	}
	return result, nil
}

func inferBrokerFundsFlow(ctx context.Context, repo *ledger.Repository, accountID string, row, previous brokerFundsRow, evidence brokerTransactionEvidence, tolerance float64) (brokerFundsFlowPlan, error) {
	dateISO := compactToISODate(row.TradeDate)
	observation, err := repo.GetAssetPositionObservation(ctx, accountID, dateISO, "open")
	if err != nil {
		return brokerFundsFlowPlan{}, fmt.Errorf("external flow %s requires open observation: %w", dateISO, err)
	}
	openPositionValue := observation.PositionMarketValue
	if openPositionValue == 0 && previous.MarketCents != 0 {
		// Older pre-open snapshots stored broker cash without a matching open
		// position snapshot. Previous broker close market value is the opening
		// mark before the day's first execution.
		openPositionValue = centsFloat(previous.MarketCents)
	}
	observedOpen := roundComparison(observation.CashTotal + openPositionValue + observation.ReverseRepoReceivable)
	external := centsFloat(row.DepositCents - row.WithdrawalCents)
	before := centsFloat(previous.TotalCents)
	after := roundComparison(before + external)
	diffBefore := math.Abs(observedOpen - before)
	diffAfter := math.Abs(observedOpen - after)
	plan := brokerFundsFlowPlan{
		EntryID: "broker-funds:" + accountID + ":" + row.TradeDate + ":external-flow", Amount: external,
		ObservedOpenAsset: observedOpen, ExpectedBeforeFlow: before, ExpectedAfterFlow: after,
	}
	var effectiveAt time.Time
	switch {
	case diffAfter <= tolerance && diffAfter < diffBefore:
		effectiveAt = observation.CapturedAt
		plan.TimingEvidence = "external_flow_reflected_by_open_snapshot"
		plan.ObservationResidual = roundComparison(observedOpen - after)
	case diffBefore <= tolerance && diffBefore < diffAfter:
		first := evidence.firstIntradayByDate[row.TradeDate]
		if first == "" {
			return plan, fmt.Errorf("external flow %s is after open but transaction detail has no intraday execution", dateISO)
		}
		parsed, parseErr := time.ParseInLocation("20060102 15:04:05", row.TradeDate+" "+first, timeutil.Location())
		if parseErr != nil {
			return plan, parseErr
		}
		effectiveAt = parsed.Add(-time.Second)
		plan.TimingEvidence = "external_flow_after_open_before_first_execution"
		plan.ObservationResidual = roundComparison(observedOpen - before)
	default:
		return plan, fmt.Errorf("external flow %s open asset %.2f matches neither pre-flow %.2f nor post-flow %.2f within %.2f", dateISO, observedOpen, before, after, tolerance)
	}
	plan.EffectiveAt = timeutil.FormatRFC3339Nano(effectiveAt)
	return plan, nil
}

func persistBrokerFundsAudit(ctx context.Context, repo *ledger.Repository, plan *brokerFundsAuditPlan, confirmedBy string, confirmedAt time.Time) error {
	existingFlows, err := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{
		AccountID: plan.report.AccountID, DateFrom: plan.report.DateFrom, DateTo: plan.report.DateTo, Limit: 500,
	})
	if err != nil {
		return err
	}
	existingByID := make(map[string]ledger.CashLedgerEntry, len(existingFlows))
	for _, item := range existingFlows {
		existingByID[item.EntryID] = item
	}
	daysByDate := make(map[string]brokerFundsAuditDay, len(plan.report.Days))
	for _, day := range plan.report.Days {
		daysByDate[strings.ReplaceAll(day.TradeDate, "-", "")] = day
	}
	for _, row := range plan.rows {
		day := daysByDate[row.TradeDate]
		gold, err := ledger.PreparePerformanceNAVGold(ledger.PerformanceNAVGold{
			AccountID: plan.report.AccountID, TradeDate: day.TradeDate, Status: "confirmed",
			CarriedOpenAsset: day.ReportedOpenAsset, CloseAsset: day.ReportedCloseAsset, DailyPnL: day.ReportedDailyPnL,
			AssetScope: "excluding_fund_occupancy", Source: brokerStatementGoldSource, SourceRef: plan.statement, ConfirmedBy: confirmedBy, ConfirmedAt: confirmedAt,
			RawPayload: map[string]any{
				"input": plan.statement, "row_number": row.RowNumber, "statement_sha256": plan.digest, "recurring_import": false,
				"source_fields": map[string]string{"trade_date": row.TradeDate, "open_asset_excluding_fund_occupancy": formatCents(int64(math.Round(day.ReportedOpenAsset * 100))), "close_asset_excluding_fund_occupancy": formatCents(row.TotalCents), "daily_pnl": formatCents(row.DailyPnLCents)},
			},
		})
		if err != nil {
			return err
		}
		if _, err := repo.UpsertPerformanceNAVGold(ctx, gold); err != nil {
			return err
		}
		plan.report.GoldRowsSaved++

		otherAsset := centsFloat(row.TotalCents - row.CashCents - row.MarketCents)
		flowID := ""
		if day.ExternalFlow != nil {
			flowID = day.ExternalFlow.EntryID
		}
		raw := map[string]any{
			"economic_nav_base_confirmed": true, "recurring_import": false, "asset_scope": "broker_reported_total_asset_excluding_fund_occupancy",
			"statement_path": plan.statement, "statement_sha256": plan.digest, "statement_row_number": row.RowNumber,
			"reported_open_total_asset": day.ReportedOpenAsset, "reported_close_total_asset": day.ReportedCloseAsset, "reported_daily_pnl": day.ReportedDailyPnL,
			"reported_deposit": day.ReportedDeposit, "reported_withdrawal": day.ReportedWithdrawal,
			"broker_customer_funds": day.BrokerCash, "broker_security_market_value": day.BrokerMarketValue, "broker_other_asset": otherAsset,
			"open_outstanding_etf_settlement_asset": 0, "close_outstanding_etf_settlement_asset": 0,
			"external_flow_entry_id": flowID, "recovery_reason": "historical asset snapshot and cash bridge gap",
		}
		capturedAt, _ := time.ParseInLocation("2006-01-02 15:30:00", day.TradeDate+" 15:30:00", timeutil.Location())
		asset := trading.Asset{
			AccountID: plan.report.AccountID, CashAvailable: day.BrokerCash, CashTotal: day.BrokerCash, NetAsset: day.ReportedCloseAsset,
			MarketValue: day.BrokerMarketValue, ReverseRepoReceivable: otherAsset, DayProfit: day.ReportedDailyPnL, UpdatedAt: capturedAt,
		}
		if err := repo.UpsertAssetSnapshotForDate(ctx, asset, day.TradeDate, "reconcile", brokerFundsAuditSource, raw, capturedAt); err != nil {
			return err
		}
		plan.report.ReconcileRowsSaved++

		if day.ExternalFlow == nil {
			continue
		}
		if existing, ok := existingByID[day.ExternalFlow.EntryID]; ok {
			if math.Abs(existing.Amount-day.ExternalFlow.Amount) > 0.000001 || existing.FlowClass != "external_flow" || existing.Status != "confirmed" {
				return fmt.Errorf("existing cash entry %s conflicts with audited external flow", existing.EntryID)
			}
			plan.report.ExternalFlowRowsSkipped++
			continue
		}
		effectiveAt, err := time.Parse(time.RFC3339Nano, day.ExternalFlow.EffectiveAt)
		if err != nil {
			return err
		}
		ledgerType := "deposit"
		if day.ExternalFlow.Amount < 0 {
			ledgerType = "withdraw"
		}
		entry, err := repo.CreateCashLedgerEntry(ctx, ledger.CashLedgerEntry{
			EntryID: day.ExternalFlow.EntryID, AccountID: plan.report.AccountID, TradeDate: day.TradeDate,
			LedgerType: ledgerType, FlowClass: "external_flow", Currency: "CNY", Amount: day.ExternalFlow.Amount,
			CashBucket: "broker_total_cash", CounterpartyBucket: "bank", EffectiveAt: effectiveAt, Status: "draft",
			IdempotencyKey: day.ExternalFlow.EntryID, Description: "券商历史资金表确认的外部资金进出", Source: brokerFundsAuditSource, CreatedBy: confirmedBy,
			RawPayload: map[string]any{
				"statement_path": plan.statement, "statement_sha256": plan.digest, "statement_row_number": row.RowNumber, "recurring_import": false,
				"reported_deposit": day.ReportedDeposit, "reported_withdrawal": day.ReportedWithdrawal,
				"timing_evidence": day.ExternalFlow.TimingEvidence, "observed_open_asset": day.ExternalFlow.ObservedOpenAsset,
				"expected_before_flow": day.ExternalFlow.ExpectedBeforeFlow, "expected_after_flow": day.ExternalFlow.ExpectedAfterFlow,
				"effective_time_is_bounded_not_broker_timestamp": true,
			},
		})
		if err != nil {
			return err
		}
		if _, err := repo.ConfirmCashLedgerEntry(ctx, plan.report.AccountID, entry.EntryID, confirmedBy, confirmedAt); err != nil {
			return err
		}
		plan.report.ExternalFlowRowsSaved++
	}
	return nil
}
