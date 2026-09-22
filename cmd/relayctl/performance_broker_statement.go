package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

const (
	brokerStatementGoldSource = "broker_historical_funds_statement_one_time_audit"
	brokerStatementNAVFormula = "broker_statement_nav.v1"
	brokerStatementNAVSource  = "broker.historical_statement.one_time_audit"
)

type brokerStatementFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Rows   int    `json:"rows"`
}

type brokerStatementAuditReport struct {
	AccountID                   string                         `json:"account_id"`
	Persist                     bool                           `json:"persist"`
	QualityGatePassed           bool                           `json:"quality_gate_passed"`
	DateFrom                    string                         `json:"date_from"`
	DateTo                      string                         `json:"date_to"`
	Files                       map[string]brokerStatementFile `json:"files"`
	FundsRows                   int                            `json:"funds_rows"`
	FundsIdentityFailures       int                            `json:"funds_identity_failures"`
	CashFlowRows                int                            `json:"cash_flow_rows"`
	CashFlowUniqueIDs           int                            `json:"cash_flow_unique_ids"`
	CashBridgeFailures          int                            `json:"cash_bridge_failures"`
	ExternalFlowMismatchDays    int                            `json:"external_flow_mismatch_days"`
	SettlementRows              int                            `json:"settlement_rows"`
	SettlementFinancialRows     int                            `json:"settlement_financial_rows"`
	FinancialFlowUnmatched      int                            `json:"financial_flow_unmatched"`
	ExecutionRows               int                            `json:"execution_rows"`
	ExecutionStatementUnmatched int                            `json:"execution_statement_unmatched"`
	DuplicateExecutionIDs       int                            `json:"duplicate_execution_ids"`
	PositionChangingEvents      int                            `json:"position_changing_events"`
	PositionBalanceMismatches   int                            `json:"position_balance_mismatches"`
	NegativeFinalPositions      int                            `json:"negative_final_positions"`
	FinalPositivePositions      int                            `json:"final_positive_positions"`
	OrderExportDistinct         bool                           `json:"order_export_distinct"`
	GoldRowsSaved               int                            `json:"gold_rows_saved,omitempty"`
	NAVRowsSaved                int                            `json:"nav_rows_saved,omitempty"`
	NAVRowsUnchanged            int                            `json:"nav_rows_unchanged,omitempty"`
	Warnings                    []string                       `json:"warnings,omitempty"`
}

type brokerFundsRow struct {
	TradeDate       string
	CashCents       int64
	DepositCents    int64
	WithdrawalCents int64
	MarketCents     int64
	DailyPnLCents   int64
	TotalCents      int64
	RowNumber       int
}

type brokerCashFlowRow struct {
	TradeDate string
	TradeTime string
	FlowID    string
	Code      string
	Subject   string
	InCents   int64
	OutCents  int64
	Summary   string
}

type brokerSettlementDay struct {
	FeesCents     int64
	BuyAmount     int64
	SellAmount    int64
	ReverseRepo   int64
	ExecutionRows int64
}

type brokerStatementAudit struct {
	report        brokerStatementAuditReport
	funds         []brokerFundsRow
	externalFlows map[string][]brokerCashFlowRow
	dayStats      map[string]brokerSettlementDay
}

type brokerCSV struct {
	header []string
	rows   [][]string
	index  map[string]int
}

func runPerformanceBrokerStatement(args []string) error {
	flags := flag.NewFlagSet("performance-broker-statement", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv("RELAY_CONFIG_PATH"), "relay YAML config path")
	accountID := flags.String("account", "", "account id")
	dir := flags.String("dir", "reference", "directory containing <account>_资金/资金流水/交割单/委托/成交.csv")
	confirmedBy := flags.String("confirmed-by", "", "operator confirming the broker exports")
	persist := flags.Bool("persist", false, "persist confirmed gold and account-level NAV in one transaction")
	timeout := flags.Duration("timeout", 5*time.Minute, "audit and database operation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	*accountID = strings.TrimSpace(*accountID)
	*dir = strings.TrimSpace(*dir)
	*confirmedBy = strings.TrimSpace(*confirmedBy)
	if *accountID == "" || *dir == "" {
		return errors.New("-account and -dir are required")
	}
	if *persist && *confirmedBy == "" {
		return errors.New("-confirmed-by is required with -persist")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	audit, err := auditBrokerStatementFiles(*accountID, *dir)
	if err != nil {
		return err
	}
	audit.report.Persist = *persist
	if !audit.report.QualityGatePassed {
		_ = writeJSON(audit.report)
		return errors.New("broker statement quality gate failed")
	}
	if !*persist {
		return writeJSON(audit.report)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
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
	repo := ledger.NewRepository(tx)
	confirmedAt := timeutil.Now()
	if err := persistBrokerStatementAudit(ctx, repo, audit, *confirmedBy, confirmedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return writeJSON(audit.report)
}

func auditBrokerStatementFiles(accountID, dir string) (*brokerStatementAudit, error) {
	names := []string{"资金", "资金流水", "交割单", "委托", "成交"}
	paths := make(map[string]string, len(names))
	files := make(map[string]brokerStatementFile, len(names))
	data := make(map[string]*brokerCSV, len(names))
	for _, name := range names {
		path := filepath.Join(dir, accountID+"_"+name+".csv")
		parsed, digest, err := readBrokerCSV(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		paths[name] = path
		data[name] = parsed
		files[name] = brokerStatementFile{Path: path, SHA256: digest, Rows: len(parsed.rows)}
	}

	funds, identityFailures, err := parseBrokerFunds(data["资金"])
	if err != nil {
		return nil, fmt.Errorf("parse 资金: %w", err)
	}
	flows, uniqueFlowIDs, cashBridgeFailures, externalMismatch, err := parseAndAuditCashFlows(data["资金流水"], funds)
	if err != nil {
		return nil, fmt.Errorf("parse 资金流水: %w", err)
	}
	settlementResult, err := auditSettlement(data["交割单"], flows)
	if err != nil {
		return nil, fmt.Errorf("parse 交割单: %w", err)
	}
	executionResult, err := auditExecutions(data["成交"], settlementResult.executionCounter)
	if err != nil {
		return nil, fmt.Errorf("parse 成交: %w", err)
	}

	orderBytes, err := os.ReadFile(paths["委托"])
	if err != nil {
		return nil, err
	}
	fillBytes, err := os.ReadFile(paths["成交"])
	if err != nil {
		return nil, err
	}
	orderDistinct := string(orderBytes) != string(fillBytes)
	report := brokerStatementAuditReport{
		AccountID:                   accountID,
		DateFrom:                    funds[0].TradeDate,
		DateTo:                      funds[len(funds)-1].TradeDate,
		Files:                       files,
		FundsRows:                   len(funds),
		FundsIdentityFailures:       identityFailures,
		CashFlowRows:                len(flows),
		CashFlowUniqueIDs:           uniqueFlowIDs,
		CashBridgeFailures:          cashBridgeFailures,
		ExternalFlowMismatchDays:    externalMismatch,
		SettlementRows:              len(data["交割单"].rows),
		SettlementFinancialRows:     settlementResult.financialRows,
		FinancialFlowUnmatched:      settlementResult.financialUnmatched,
		ExecutionRows:               len(data["成交"].rows),
		ExecutionStatementUnmatched: executionResult.unmatched,
		DuplicateExecutionIDs:       executionResult.duplicateIDs,
		PositionChangingEvents:      settlementResult.positionEvents,
		PositionBalanceMismatches:   settlementResult.positionMismatches,
		NegativeFinalPositions:      settlementResult.negativeFinal,
		FinalPositivePositions:      settlementResult.positiveFinal,
		OrderExportDistinct:         orderDistinct,
	}
	if !orderDistinct {
		report.Warnings = append(report.Warnings, "委托.csv 与 成交.csv 完全相同；无法据此审计未成交、撤单和废单，但不影响账户净值、成交、费用和持仓成本")
	}
	if executionResult.duplicateIDs > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("成交编号存在 %d 个重复值；逐笔经济键仍唯一且已与交割单闭合", executionResult.duplicateIDs))
	}
	first := funds[0]
	zeroAnchor := first.CashCents == 0 && first.MarketCents == 0 && first.TotalCents == 0 && first.DepositCents == 0 && first.WithdrawalCents == 0 && first.DailyPnLCents == 0
	report.QualityGatePassed = zeroAnchor && identityFailures == 0 && uniqueFlowIDs == len(flows) && cashBridgeFailures == 0 && externalMismatch == 0 && settlementResult.financialUnmatched == 0 && executionResult.unmatched == 0 && settlementResult.positionMismatches == 0 && settlementResult.negativeFinal == 0

	externalFlows := make(map[string][]brokerCashFlowRow)
	for _, flow := range flows {
		if flow.Subject == "银证转帐存" || flow.Subject == "银证转帐取" {
			externalFlows[flow.TradeDate] = append(externalFlows[flow.TradeDate], flow)
		}
	}
	return &brokerStatementAudit{report: report, funds: funds, externalFlows: externalFlows, dayStats: settlementResult.dayStats}, nil
}

func readBrokerCSV(path string) (*brokerCSV, string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(body)
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, "", err
	}
	if len(records) < 2 {
		return nil, "", errors.New("CSV has no data rows")
	}
	header := records[0]
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[strings.TrimSpace(name)] = i
	}
	return &brokerCSV{header: header, rows: records[1:], index: index}, hex.EncodeToString(digest[:]), nil
}

func (data *brokerCSV) value(row []string, name string) (string, error) {
	index, ok := data.index[name]
	if !ok {
		return "", fmt.Errorf("missing column %q", name)
	}
	if index >= len(row) {
		return "", fmt.Errorf("row has %d columns; %q is at %d", len(row), name, index)
	}
	return strings.TrimSpace(row[index]), nil
}

func parseBrokerFunds(data *brokerCSV) ([]brokerFundsRow, int, error) {
	rows := make([]brokerFundsRow, 0, len(data.rows))
	seen := make(map[string]bool, len(data.rows))
	for index, raw := range data.rows {
		tradeDate, err := data.value(raw, "交易日")
		if err != nil {
			return nil, 0, err
		}
		if _, err := time.Parse("20060102", tradeDate); err != nil {
			return nil, 0, fmt.Errorf("row %d invalid trade date %q", index+2, tradeDate)
		}
		if seen[tradeDate] {
			return nil, 0, fmt.Errorf("row %d duplicates trade date %s", index+2, tradeDate)
		}
		seen[tradeDate] = true
		readMoney := func(name string) (int64, error) {
			value, valueErr := data.value(raw, name)
			if valueErr != nil {
				return 0, valueErr
			}
			return parseBrokerMoneyCents(value)
		}
		cash, err := readMoney("客户资金")
		if err != nil {
			return nil, 0, err
		}
		deposit, err := readMoney("入金")
		if err != nil {
			return nil, 0, err
		}
		withdrawal, err := readMoney("出金")
		if err != nil {
			return nil, 0, err
		}
		market, err := readMoney("市值")
		if err != nil {
			return nil, 0, err
		}
		pnl, err := readMoney("当日盈亏")
		if err != nil {
			return nil, 0, err
		}
		total, err := readMoney("总资产")
		if err != nil {
			return nil, 0, err
		}
		rows = append(rows, brokerFundsRow{TradeDate: tradeDate, CashCents: cash, DepositCents: deposit, WithdrawalCents: withdrawal, MarketCents: market, DailyPnLCents: pnl, TotalCents: total, RowNumber: index + 2})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate < rows[j].TradeDate })
	failures := 0
	previous := int64(0)
	for _, row := range rows {
		if previous+row.DepositCents-row.WithdrawalCents+row.DailyPnLCents != row.TotalCents {
			failures++
		}
		previous = row.TotalCents
	}
	return rows, failures, nil
}

func parseAndAuditCashFlows(data *brokerCSV, funds []brokerFundsRow) ([]brokerCashFlowRow, int, int, int, error) {
	rows := make([]brokerCashFlowRow, 0, len(data.rows))
	ids := make(map[string]bool, len(data.rows))
	dailyNet := make(map[string]int64)
	dailyExternal := make(map[string]int64)
	for index, raw := range data.rows {
		var value func(string) (string, error)
		if len(raw) == len(data.header)-1 && len(data.header) == 14 {
			// The Huaxin export advertises 资金余额 but omits that field in every row.
			columns := []string{"交易日期", "交易时间", "资金账户", "流水号", "股票代码", "股票名称", "业务科目", "投资者", "名称", "入金", "出金", "操作摘要", ""}
			byName := make(map[string]string, len(columns))
			for i, name := range columns {
				byName[name] = strings.TrimSpace(raw[i])
			}
			value = func(name string) (string, error) {
				result, ok := byName[name]
				if !ok {
					return "", fmt.Errorf("row %d missing %s", index+2, name)
				}
				return result, nil
			}
		} else {
			value = func(name string) (string, error) { return data.value(raw, name) }
		}
		tradeDate, err := value("交易日期")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		tradeTime, err := value("交易时间")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		flowID, err := value("流水号")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		code, err := value("股票代码")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		subject, err := value("业务科目")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		inRaw, err := value("入金")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		outRaw, err := value("出金")
		if err != nil {
			return nil, 0, 0, 0, err
		}
		summary, _ := value("操作摘要")
		inCents, err := parseBrokerMoneyCents(inRaw)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		outCents, err := parseBrokerMoneyCents(outRaw)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		rows = append(rows, brokerCashFlowRow{TradeDate: tradeDate, TradeTime: tradeTime, FlowID: flowID, Code: brokerCode(code), Subject: subject, InCents: inCents, OutCents: outCents, Summary: summary})
		ids[flowID] = true
		dailyNet[tradeDate] += inCents - outCents
		if subject == "银证转帐存" || subject == "银证转帐取" {
			dailyExternal[tradeDate] += inCents - outCents
		}
	}
	cashFailures := 0
	externalFailures := 0
	previousCash := int64(0)
	for _, row := range funds {
		if row.CashCents-previousCash != dailyNet[row.TradeDate] {
			cashFailures++
		}
		if row.DepositCents-row.WithdrawalCents != dailyExternal[row.TradeDate] {
			externalFailures++
		}
		previousCash = row.CashCents
	}
	return rows, len(ids), cashFailures, externalFailures, nil
}

type brokerSettlementAudit struct {
	financialRows      int
	financialUnmatched int
	positionEvents     int
	positionMismatches int
	negativeFinal      int
	positiveFinal      int
	executionCounter   map[string]int
	dayStats           map[string]brokerSettlementDay
}

func auditSettlement(data *brokerCSV, flows []brokerCashFlowRow) (brokerSettlementAudit, error) {
	result := brokerSettlementAudit{executionCounter: make(map[string]int), dayStats: make(map[string]brokerSettlementDay)}
	financialCounter := make(map[string]int)
	flowCounter := make(map[string]int)
	balances := make(map[string]int64)
	subjectBySide := map[string]string{"买入": "买入成交清算资金", "卖出": "卖出成交清算资金", "融券": "回购融券成交划出资金", "融券购回": "回购融券购回资金划入", "股息税": "红利差别税", "红利": "股票红利划入"}
	deltaBySide := map[string]int64{"买入": 1, "卖出": -1, "送股": 1, "托管转入": 1, "转托转入": 1, "转托转出": -1}
	for _, flow := range flows {
		if _, ok := map[string]bool{"买入成交清算资金": true, "卖出成交清算资金": true, "回购融券成交划出资金": true, "回购融券购回资金划入": true, "红利差别税": true, "股票红利划入": true}[flow.Subject]; ok {
			flowCounter[brokerFinancialKey(flow.TradeDate, flow.Code, flow.Subject, flow.InCents-flow.OutCents)]++
		}
	}
	for _, raw := range data.rows {
		dateValue, err := data.value(raw, "成交日期")
		if err != nil {
			return result, err
		}
		timeValue, err := data.value(raw, "成交时间")
		if err != nil {
			return result, err
		}
		code, err := data.value(raw, "代码")
		if err != nil {
			return result, err
		}
		side, err := data.value(raw, "方向")
		if err != nil {
			return result, err
		}
		qtyRaw, err := data.value(raw, "成交数量")
		if err != nil {
			return result, err
		}
		price, err := data.value(raw, "成交价格")
		if err != nil {
			return result, err
		}
		amountRaw, err := data.value(raw, "成交金额")
		if err != nil {
			return result, err
		}
		paidRaw, err := data.value(raw, "实付金额")
		if err != nil {
			return result, err
		}
		qty, err := parseBrokerInteger(qtyRaw)
		if err != nil {
			return result, err
		}
		amount, err := parseBrokerMoneyCents(amountRaw)
		if err != nil {
			return result, err
		}
		paid, err := parseBrokerMoneyCents(paidRaw)
		if err != nil {
			return result, err
		}
		if subject, ok := subjectBySide[side]; ok {
			result.financialRows++
			financialCounter[brokerFinancialKey(dateValue, brokerCode(code), subject, paid)]++
		}
		if side == "买入" || side == "卖出" || side == "融券" {
			normalizedSide := side
			if normalizedSide == "融券" {
				normalizedSide = "通用回购逆回购"
			}
			result.executionCounter[brokerExecutionKey(dateValue, timeValue, code, normalizedSide, qty, price, amount)]++
			day := result.dayStats[dateValue]
			day.ExecutionRows++
			switch side {
			case "买入":
				day.BuyAmount += amount
			case "卖出":
				day.SellAmount += amount
			case "融券":
				day.ReverseRepo += amount
			}
			for _, feeName := range []string{"实收佣金", "印花税", "交易规费", "过户费"} {
				feeRaw, feeErr := data.value(raw, feeName)
				if feeErr != nil {
					return result, feeErr
				}
				fee, feeErr := parseBrokerMoneyCents(feeRaw)
				if feeErr != nil {
					return result, feeErr
				}
				day.FeesCents += fee
			}
			result.dayStats[dateValue] = day
		}
	}
	for key, count := range financialCounter {
		other := flowCounter[key]
		if count > other {
			result.financialUnmatched += count - other
		}
	}
	for key, count := range flowCounter {
		other := financialCounter[key]
		if count > other {
			result.financialUnmatched += count - other
		}
	}
	for index := len(data.rows) - 1; index >= 0; index-- {
		raw := data.rows[index]
		side, _ := data.value(raw, "方向")
		delta, ok := deltaBySide[side]
		if !ok {
			continue
		}
		code, _ := data.value(raw, "代码")
		qtyRaw, _ := data.value(raw, "成交数量")
		balanceRaw, _ := data.value(raw, "股份余额")
		qty, err := parseBrokerInteger(qtyRaw)
		if err != nil {
			return result, err
		}
		observed, err := parseBrokerInteger(balanceRaw)
		if err != nil {
			return result, err
		}
		balances[code] += delta * qty
		result.positionEvents++
		if balances[code] != observed {
			result.positionMismatches++
		}
	}
	for _, quantity := range balances {
		if quantity < 0 {
			result.negativeFinal++
		}
		if quantity > 0 {
			result.positiveFinal++
		}
	}
	return result, nil
}

type brokerExecutionAudit struct{ unmatched, duplicateIDs int }

func auditExecutions(data *brokerCSV, settlement map[string]int) (brokerExecutionAudit, error) {
	result := brokerExecutionAudit{}
	executions := make(map[string]int)
	fillIDs := make(map[string]int)
	for _, raw := range data.rows {
		dateValue, err := data.value(raw, "日期")
		if err != nil {
			return result, err
		}
		timeValue, err := data.value(raw, "时间")
		if err != nil {
			return result, err
		}
		code, err := data.value(raw, "代码")
		if err != nil {
			return result, err
		}
		side, err := data.value(raw, "方向")
		if err != nil {
			return result, err
		}
		qtyRaw, err := data.value(raw, "成交量")
		if err != nil {
			return result, err
		}
		price, err := data.value(raw, "成交价")
		if err != nil {
			return result, err
		}
		amountRaw, err := data.value(raw, "成交金额")
		if err != nil {
			return result, err
		}
		fillID, err := data.value(raw, "成交编号")
		if err != nil {
			return result, err
		}
		qty, err := parseBrokerInteger(qtyRaw)
		if err != nil {
			return result, err
		}
		amount, err := parseBrokerMoneyCents(amountRaw)
		if err != nil {
			return result, err
		}
		executions[brokerExecutionKey(dateValue, timeValue, code, side, qty, price, amount)]++
		fillIDs[fillID]++
	}
	for _, count := range fillIDs {
		if count > 1 {
			result.duplicateIDs += count - 1
		}
	}
	for key, count := range executions {
		if count > settlement[key] {
			result.unmatched += count - settlement[key]
		}
	}
	for key, count := range settlement {
		if count > executions[key] {
			result.unmatched += count - executions[key]
		}
	}
	return result, nil
}

func persistBrokerStatementAudit(ctx context.Context, repo *ledger.Repository, audit *brokerStatementAudit, confirmedBy string, confirmedAt time.Time) error {
	current, err := repo.ListPerformanceNAVs(ctx, audit.report.AccountID, "", audit.report.DateTo)
	if err != nil {
		return err
	}
	currentByDate := make(map[string]ledger.PerformanceNAV, len(current))
	for _, item := range current {
		currentByDate[item.TradeDate] = item
	}
	fundsHash := audit.report.Files["资金"].SHA256
	statementHash := audit.report.Files["交割单"].SHA256
	flowsHash := audit.report.Files["资金流水"].SHA256
	fillHash := audit.report.Files["成交"].SHA256
	cumulative := 1.0
	previousTotal := int64(0)
	for _, row := range audit.funds {
		tradeDate := compactToISODate(row.TradeDate)
		gold, err := ledger.PreparePerformanceNAVGold(ledger.PerformanceNAVGold{
			AccountID: audit.report.AccountID, TradeDate: tradeDate, Status: "confirmed",
			CarriedOpenAsset: centsFloat(previousTotal), CloseAsset: centsFloat(row.TotalCents), DailyPnL: centsFloat(row.DailyPnLCents),
			AssetScope: "excluding_fund_occupancy", Source: brokerStatementGoldSource,
			SourceRef: audit.report.Files["资金"].Path, ConfirmedBy: confirmedBy, ConfirmedAt: confirmedAt,
			RawPayload: map[string]any{"input": audit.report.Files["资金"].Path, "row_number": row.RowNumber, "statement_sha256": fundsHash, "recurring_import": false,
				"source_fields": map[string]string{"trade_date": row.TradeDate, "open_asset_excluding_fund_occupancy": formatCents(previousTotal), "close_asset_excluding_fund_occupancy": formatCents(row.TotalCents), "daily_pnl": formatCents(row.DailyPnLCents)}},
		})
		if err != nil {
			return err
		}
		if _, err := repo.UpsertPerformanceNAVGold(ctx, gold); err != nil {
			return err
		}
		audit.report.GoldRowsSaved++
		if row.TotalCents <= 0 {
			previousTotal = row.TotalCents
			continue
		}

		externalNet := row.DepositCents - row.WithdrawalCents
		openNAV := previousTotal
		returnFlows := audit.externalFlows[row.TradeDate]
		quality := []string{"broker_statement_confirmed", "broker_statement_one_time_audit", "account_level_nav_authoritative", "security_attribution_separate"}
		if openNAV <= 0 {
			openNAV = row.TotalCents - row.DailyPnLCents
			externalNet = 0
			returnFlows = nil
			quality = append(quality, "broker_inception_funding_as_open_capital")
		}
		denominator, flowDetails := brokerReturnDenominator(openNAV, returnFlows)
		dailyReturn := 0.0
		if denominator > 0 {
			dailyReturn = centsFloat(row.DailyPnLCents) / denominator
		}
		cumulative = roundBrokerRatio(cumulative * (1 + dailyReturn))
		stats := audit.dayStats[row.TradeDate]
		components := map[string]any{}
		if existing, ok := currentByDate[tradeDate]; ok {
			for key, value := range existing.PnLComponents {
				components[key] = value
			}
			if existing.FormulaVersion != brokerStatementNAVFormula {
				components["superseded_relay_nav"] = map[string]any{
					"formula_version": existing.FormulaVersion,
					"status":          existing.Status,
					"quality_flags":   existing.QualityFlags,
				}
			}
		}
		components["broker_statement"] = map[string]any{
			"cash_total": centsFloat(row.CashCents), "market_value": centsFloat(row.MarketCents), "other_asset": centsFloat(row.TotalCents - row.CashCents - row.MarketCents),
			"reported_open_asset": centsFloat(previousTotal), "reported_deposit": centsFloat(row.DepositCents), "reported_withdrawal": centsFloat(row.WithdrawalCents),
			"reported_daily_pnl": centsFloat(row.DailyPnLCents), "reported_close_asset": centsFloat(row.TotalCents), "row_number": row.RowNumber,
			"funds_sha256": fundsHash, "cash_flows_sha256": flowsHash, "settlement_sha256": statementHash, "executions_sha256": fillHash,
			"external_flow_details": flowDetails, "recurring_import": false,
		}
		components["trading_observation"] = map[string]any{"fills_count": stats.ExecutionRows, "buy_amount": centsFloat(stats.BuyAmount), "sell_amount": centsFloat(stats.SellAmount), "reverse_repo_principal": centsFloat(stats.ReverseRepo), "turnover": centsFloat(stats.BuyAmount + stats.SellAmount), "fee_total": centsFloat(stats.FeesCents), "fee_source": "broker_delivery_statement"}
		components["market_valuation"] = map[string]any{"close_visible_cash": centsFloat(row.CashCents), "close_position_value": centsFloat(row.MarketCents), "other_asset": centsFloat(row.TotalCents - row.CashCents - row.MarketCents), "price_source": "broker_statement"}
		nav := ledger.PerformanceNAV{
			AccountID: audit.report.AccountID, TradeDate: tradeDate, Status: "finalized", FormulaVersion: brokerStatementNAVFormula,
			OpenEconomicNAV: centsFloat(openNAV), ExternalNetFlow: centsFloat(externalNet), AccountDayPnL: centsFloat(row.DailyPnLCents), CloseEconomicNAV: centsFloat(row.TotalCents),
			ReturnDenominator: denominator, DailyReturn: roundBrokerRatio(dailyReturn), CumulativeNAV: cumulative,
			PnLComponents: components, QualityFlags: quality, Source: brokerStatementNAVSource, FinalizedAt: confirmedAt,
		}
		if existing, ok := currentByDate[tradeDate]; ok && brokerNAVUnchanged(existing, nav, fundsHash) {
			audit.report.NAVRowsUnchanged++
		} else {
			if _, err := repo.UpsertPerformanceNAV(ctx, nav); err != nil {
				return err
			}
			audit.report.NAVRowsSaved++
		}
		previousTotal = row.TotalCents
	}
	return nil
}

func brokerNAVUnchanged(existing, next ledger.PerformanceNAV, fundsHash string) bool {
	if existing.FormulaVersion != brokerStatementNAVFormula || existing.Source != brokerStatementNAVSource {
		return false
	}
	statement, _ := existing.PnLComponents["broker_statement"].(map[string]any)
	if fmt.Sprint(statement["funds_sha256"]) != fundsHash {
		return false
	}
	return math.Abs(existing.OpenEconomicNAV-next.OpenEconomicNAV) < 0.000001 &&
		math.Abs(existing.ExternalNetFlow-next.ExternalNetFlow) < 0.000001 &&
		math.Abs(existing.CloseEconomicNAV-next.CloseEconomicNAV) < 0.000001 &&
		math.Abs(existing.AccountDayPnL-next.AccountDayPnL) < 0.000001 &&
		math.Abs(existing.ReturnDenominator-next.ReturnDenominator) < 0.000001 &&
		math.Abs(existing.DailyReturn-next.DailyReturn) < 0.000000000001 &&
		math.Abs(existing.CumulativeNAV-next.CumulativeNAV) < 0.000000000001
}

func brokerReturnDenominator(openCents int64, flows []brokerCashFlowRow) (float64, []map[string]any) {
	weightedCents := float64(openCents)
	details := make([]map[string]any, 0, len(flows))
	for _, flow := range flows {
		amount := flow.InCents - flow.OutCents
		weight := brokerFlowWeight(flow.TradeTime)
		weightedCents += float64(amount) * weight
		details = append(details, map[string]any{"flow_id": flow.FlowID, "amount": centsFloat(amount), "weight": roundBrokerRatio(weight), "effective_time": flow.TradeTime, "summary": flow.Summary})
	}
	if weightedCents <= 0 {
		weightedCents = float64(openCents)
	}
	return math.Round(weightedCents*10_000) / 1_000_000, details
}

func brokerFlowWeight(value string) float64 {
	parsed, err := time.Parse("15:04:05", strings.TrimSpace(value))
	if err != nil {
		return 0.5
	}
	seconds := parsed.Hour()*3600 + parsed.Minute()*60 + parsed.Second()
	start := 9*3600 + 30*60
	end := 15 * 3600
	if seconds <= start {
		return 1
	}
	if seconds >= end {
		return 0
	}
	return float64(end-seconds) / float64(end-start)
}

func parseBrokerMoneyCents(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" || value == "--" || value == "-" {
		return 0, nil
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "+")
	value = strings.TrimPrefix(value, "-")
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid money %q", value)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		if strings.Trim(fraction[2:], "0") != "" {
			return 0, fmt.Errorf("money has sub-cent precision %q", value)
		}
		fraction = fraction[:2]
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	cents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid money %q", value)
	}
	result := whole*100 + cents
	if negative {
		result = -result
	}
	return result, nil
}

func parseBrokerInteger(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" || value == "--" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func normalizeBrokerDecimal(value string) string {
	value = strings.TrimSpace(strings.TrimLeft(value, "+"))
	if strings.Contains(value, ".") {
		value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	}
	if value == "" || value == "-0" {
		return "0"
	}
	return value
}

func brokerCode(value string) string { return strings.SplitN(strings.TrimSpace(value), ".", 2)[0] }

func brokerFinancialKey(dateValue, code, subject string, signedCents int64) string {
	return strings.Join([]string{dateValue, brokerCode(code), subject, strconv.FormatInt(signedCents, 10)}, "|")
}

func brokerExecutionKey(dateValue, timeValue, code, side string, qty int64, price string, amountCents int64) string {
	return strings.Join([]string{dateValue, timeValue, strings.TrimSpace(code), side, strconv.FormatInt(qty, 10), normalizeBrokerDecimal(price), strconv.FormatInt(amountCents, 10)}, "|")
}

func compactToISODate(value string) string {
	if len(value) == 8 {
		return value[:4] + "-" + value[4:6] + "-" + value[6:]
	}
	return value
}

func centsFloat(value int64) float64 { return float64(value) / 100 }
func formatCents(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%02d", sign, value/100, value%100)
}
func roundBrokerRatio(value float64) float64 {
	return math.Round(value*1_000_000_000_000) / 1_000_000_000_000
}
