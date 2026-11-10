package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

const recovery20261027Source = "broker_historical_funds_statement_one_time_audit_20261027"

var recovery20261027Hashes = map[string][2]string{
	"307000051387": {"ec536f1552c61c1fb8ddfa082648a9f88e124110863ae3aa48a91d5920592f1e", "72cb594f280f65034d275d1a2baf54b046105a6598ede53862ecbdfbc3d9a8fe"},
	"307000051388": {"ccee098d712c56c773194e04dccd3a6d3586fb68a25e7fcfb450456346bd6121", "afc68dfb93a053f50e8b29e4ea48abda2d096065f52652d16bfeb5fa047bb460"},
}

type recovery20261027Plan struct {
	AccountID    string                            `json:"account_id"`
	Finalization ledger.ETFSettlementFinalization  `json:"finalization"`
	CashEntries  []ledger.CashLedgerEntry          `json:"cash_entries"`
	Bases        []ledger.AssetPositionObservation `json:"asset_bases"`
	Evidence     map[string]any                    `json:"evidence"`
}

type recovery20261027Trade struct {
	Code       string `json:"code"`
	Qty        int64  `json:"qty"`
	GrossCents int64  `json:"gross_cents"`
	Orders     int    `json:"orders"`
}

// A dated, strictly read-only plan: no import route, scheduling or OC queries.
func runPerformanceRecovery20261027(args []string) error {
	flags := flag.NewFlagSet("performance-recovery-20261027", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	output := flags.String("output", "", "new private JSON report (required)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("-output is required; no positional arguments or write options")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("reviewed production ledger required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var databaseName string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&databaseName); err != nil || databaseName != "relay_trader" {
		return errors.New("unexpected database")
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	pcf, err := client.MarketETFComponents(ctx, url.Values{"security_id": {"159915.SZ"}, "trade_date": {"20261021"}, "limit": {"1000"}})
	if err != nil || pcf.StatusCode != 200 {
		return fmt.Errorf("PCF unavailable: %v", err)
	}
	cash, err := client.MarketETFCashComponents(ctx, url.Values{"security_id": {"159915.SZ"}, "trade_date": {"20261022"}, "limit": {"5"}})
	if err != nil || cash.StatusCode != 200 {
		return fmt.Errorf("cash component unavailable: %v", err)
	}
	expected, err := verifyRecovery20261027PCF(pcf.Payload, cash.Payload)
	if err != nil {
		return err
	}
	repo := ledger.NewRepository(tx)
	store := &recovery20261027Preview{performancePreviewStore: &performancePreviewStore{Repository: repo,
		costs: make(map[string]map[string][]ledger.PositionCostState), navs: make(map[string]map[string]ledger.PerformanceNAV)}, plans: make(map[string]recovery20261027Plan)}
	plans := make([]recovery20261027Plan, 0, 2)
	for _, account := range []string{"307000051387", "307000051388"} {
		plan, err := buildRecovery20261027Plan(ctx, tx, account, expected)
		if err != nil {
			return fmt.Errorf("%s: %w", account, err)
		}
		plan.Finalization.RawPayload["meridian_components"] = pcf.Payload
		plan.Finalization.RawPayload["meridian_cash_component"] = cash.Payload
		store.plans[account] = plan
		plans = append(plans, plan)
	}
	service, err := performance.New(performance.Options{Store: store, Calendar: client, Market: client,
		FormulaVersion: cfg.Performance.FormulaVersion, ETFT0FrictionRate: cfg.Performance.ETFT0FrictionRate,
		AutoToleranceCNY: cfg.Performance.AutoToleranceCNY, AutoToleranceBP: cfg.Performance.AutoToleranceBP,
		WarningToleranceCNY: cfg.Performance.WarningToleranceCNY, WarningToleranceBP: cfg.Performance.WarningToleranceBP})
	if err != nil {
		return err
	}
	report := performanceRebuildReport{DateFrom: "2026-10-21", DateTo: "2026-10-27", PreviewChain: true}
	passed := true
	for _, plan := range plans {
		for _, date := range []string{"2026-10-21", "2026-10-22", "2026-10-23", "2026-10-26", "2026-10-27"} {
			item := performanceRebuildItem{AccountID: plan.AccountID, TradeDate: date}
			item.Cost, err = service.CalculateCostLedger(ctx, plan.AccountID, date, performance.CostLedgerOptions{})
			if err == nil {
				item.NAV, err = service.CalculateEconomicNAV(ctx, plan.AccountID, date, performance.EconomicNAVOptions{})
			}
			if err != nil {
				item.Error = err.Error()
			}
			ok := err == nil && performanceResultPublishable(item.Cost.Status, item.NAV.Status)
			if ok {
				for _, basis := range plan.Bases {
					if basis.TradeDate != date {
						continue
					}
					raw := basis.RawPayload
					want := raw["reported_daily_pnl"].(float64) + raw["close_outstanding_etf_settlement_asset"].(float64) - raw["open_outstanding_etf_settlement_asset"].(float64)
					if math.Abs(item.NAV.NAV.AccountDayPnL-want) > 0.01 {
						ok = false
						item.Error = "NAV disagrees with verified economic asset bridge"
					}
				}
			}
			report.Items = append(report.Items, item)
			if !ok {
				passed = false
				break
			}
			store.remember(plan.AccountID, date, item.Cost.Positions, item.NAV.NAV)
		}
	}
	result := map[string]any{"generated_at": timeutil.Now(), "database_read_only": true, "database_writes": 0,
		"broker_commands": 0, "recurring_import": false, "quality_gate_passed": passed, "plans": plans, "preview": report}
	if err := writeETFTransferReviewFile(*output, result); err != nil {
		return err
	}
	if !passed {
		return errors.New("preview remains blocked; see private report")
	}
	return nil
}

func recovery20261027Number(row map[string]any, key string) (float64, error) {
	v, err := strconv.ParseFloat(fmt.Sprint(row[key]), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid numeric field %s", key)
	}
	return v, nil
}

func verifyRecovery20261027PCF(components, cash map[string]any) (map[string]int64, error) {
	return verifyActualSettlementPCF(components, cash, 20261021, 20261022, -652.34)
}

func verifyActualSettlementPCF(components, cash map[string]any, sourceDate, cashDate int, cashAmount float64) (map[string]int64, error) {
	rows, ok := components["data"].([]any)
	if !ok || len(rows) != 100 {
		return nil, errors.New("expected complete 100-row PCF")
	}
	expected := make(map[string]int64)
	seen := make(map[string]bool)
	for _, value := range rows {
		row, ok := value.(map[string]any)
		date, dateErr := recovery20261027Number(row, "trade_date")
		if !ok || fmt.Sprint(row["security_id"]) != "159915.SZ" || dateErr != nil || date != float64(sourceDate) || fmt.Sprint(row["schema_version"]) != "etf_component.v2" {
			return nil, errors.New("PCF identity changed")
		}
		id := fmt.Sprint(row["component_security_id"])
		qty, err := recovery20261027Number(row, "stock_amount")
		fixed, fixedErr := recovery20261027Number(row, "fixed_cash_substitute")
		redeem, redeemErr := recovery20261027Number(row, "redeem_cash_substitute")
		if seen[id] || len(id) != 9 || !strings.HasSuffix(id, ".SZ") || err != nil || qty < 0 || qty != math.Trunc(qty) || fixedErr != nil || redeemErr != nil || fixed != 0 || redeem != 0 {
			return nil, errors.New("PCF has ambiguous identity, quantity or cash substitution")
		}
		seen[id] = true
		if qty == 0 {
			if row["economic_exposure_status"] != "zero_economic_exposure" || row["exclude_from_security_exposure"] != true {
				return nil, errors.New("zero row lacks Meridian zero-exposure proof")
			}
		} else {
			if row["economic_exposure_status"] != "security_quantity" || row["exclude_from_security_exposure"] != false {
				return nil, errors.New("unexpected positive exposure")
			}
			expected[id[:6]] = int64(qty)
		}
	}
	values, ok := cash["data"].([]any)
	if !ok || len(values) != 1 || len(expected) != 98 {
		return nil, errors.New("PCF coverage mismatch")
	}
	row, ok := values[0].(map[string]any)
	if !ok || row["security_id"] != "159915.SZ" || row["schema_version"] != "etf_cash_component.v1" {
		return nil, errors.New("cash component identity mismatch")
	}
	for key, want := range map[string]float64{"trade_date": float64(cashDate), "pre_trade_date": float64(sourceDate), "unit_subscribe_redeem": 1000000, "cash_component": cashAmount} {
		got, err := recovery20261027Number(row, key)
		if err != nil || got != want {
			return nil, fmt.Errorf("cash component mismatch: %s", key)
		}
	}
	return expected, nil
}

var recovery20261027Execution = regexp.MustCompile(`\(([0-9]{6})\)([0-9]+)股\*([0-9]+\.[0-9]{3})$`)

// Broker summaries round average prices to three decimals; gross uses OC fills.
func recovery20261027ExecutionFee(trade recovery20261027Trade, flows []brokerCashFlowRow, buy bool) (int64, []string, error) {
	var qty, net int64
	var roundedGross float64
	ids := make([]string, 0, len(flows))
	for _, flow := range flows {
		match := recovery20261027Execution.FindStringSubmatch(flow.Summary)
		if match == nil || match[1] != trade.Code {
			return 0, nil, errors.New("execution summary identity mismatch")
		}
		n, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil || n <= 0 {
			return 0, nil, errors.New("invalid execution quantity")
		}
		price, err := strconv.ParseFloat(match[3], 64)
		if err != nil || price <= 0 {
			return 0, nil, errors.New("invalid execution price")
		}
		qty += n
		roundedGross += float64(n) * price * 100
		if buy {
			if flow.InCents != 0 {
				return 0, nil, errors.New("buy cash direction")
			}
			net += flow.OutCents
		} else {
			if flow.OutCents != 0 {
				return 0, nil, errors.New("sell cash direction")
			}
			net += flow.InCents
		}
		ids = append(ids, flow.FlowID)
	}
	if qty != trade.Qty || len(flows) != trade.Orders || math.Abs(roundedGross-float64(trade.GrossCents)) > float64(qty)*0.05+0.01 {
		return 0, nil, errors.New("execution cash rows do not match OC quantity/order count/rounded price")
	}
	fee := trade.GrossCents - net
	if buy {
		fee = net - trade.GrossCents
	}
	if fee < 0 || fee > trade.GrossCents/100 {
		return 0, nil, errors.New("implausible cash-derived fee")
	}
	return fee, ids, nil
}

func buildRecovery20261027Plan(ctx context.Context, tx *sql.Tx, account string, expected map[string]int64) (recovery20261027Plan, error) {
	return buildActualSettlementPlan(ctx, tx, account, expected, actualSettlementSpec{
		SourceDate: "20261021", FeeDate: "20261022", CashDate: "20261023", BasisThrough: "20261026",
		Source: recovery20261027Source, Tag: "20261027", CashComponentCents: -65234, DeferredFeeCents: 1306,
		Hashes: recovery20261027Hashes,
		Orders: map[string][2]string{"307000051387": {"12002A620001857", "12002A620002536"}, "307000051388": {"12002A620001972", "12002A620002743"}},
	})
}

// Both reviewed recoveries use one full SZ basket, with two zero-exposure PCF rows.
type actualSettlementSpec struct {
	SourceDate, FeeDate, CashDate, BasisThrough, Source, Tag string
	CashComponentCents, DeferredFeeCents                     int64
	Hashes                                                   map[string][2]string
	Orders                                                   map[string][2]string
}

func (s actualSettlementSpec) carry(date string) (float64, float64) {
	opening, closing := int64(0), int64(0)
	if date > s.SourceDate && date <= s.CashDate {
		opening += s.CashComponentCents
	}
	if date >= s.SourceDate && date < s.CashDate {
		closing += s.CashComponentCents
	}
	if date > s.SourceDate && date <= s.FeeDate {
		opening -= s.DeferredFeeCents
	}
	if date >= s.SourceDate && date < s.FeeDate {
		closing -= s.DeferredFeeCents
	}
	return centsFloat(opening), centsFloat(closing)
}

func buildActualSettlementPlan(ctx context.Context, tx *sql.Tx, account string, expected map[string]int64, spec actualSettlementSpec) (recovery20261027Plan, error) {
	plan := recovery20261027Plan{AccountID: account, Evidence: make(map[string]any)}
	hashes, ok := spec.Hashes[account]
	if !ok {
		return plan, errors.New("account outside reviewed scope")
	}
	files := make([]*brokerCSV, 2)
	for i, suffix := range []string{"_资金.csv", "_资金流水.csv"} {
		data, hash, err := readBrokerCSV("reference/" + account + suffix)
		if err != nil {
			return plan, err
		}
		if hash != hashes[i] {
			return plan, errors.New("source file changed")
		}
		files[i] = data
	}
	funds, _, err := parseBrokerFunds(files[0])
	if err != nil || len(funds) < 2 {
		return plan, errors.New("invalid broker asset identities")
	}
	for _, row := range files[0].rows {
		id, e := files[0].value(row, "客户号")
		if e != nil || id != account {
			return plan, errors.New("funds account mismatch")
		}
	}
	for _, row := range files[1].rows {
		if len(row) != 13 || row[2] != account+"01" || row[7] != account {
			return plan, errors.New("flow account/shape mismatch")
		}
	}
	flows, unique, _, externalFailures, err := parseAndAuditCashFlows(files[1], funds)
	if err != nil || unique != len(flows) || externalFailures != 0 {
		return plan, errors.New("flow uniqueness/external flow gate failed")
	}
	daily := make(map[string]int64)
	for _, f := range flows {
		daily[f.TradeDate] += f.InCents - f.OutCents
	}
	for i, f := range funds {
		if i == 0 {
			continue
		}
		p := funds[i-1]
		if f.CashCents-p.CashCents != daily[f.TradeDate] || f.TotalCents != p.TotalCents+f.DepositCents-f.WithdrawalCents+f.DailyPnLCents {
			return plan, fmt.Errorf("cash/asset bridge fails %s", f.TradeDate)
		}
	}
	ids, ok := spec.Orders[account]
	if !ok {
		return plan, errors.New("reviewed order identities missing")
	}
	buyID, redeemID := ids[0], ids[1]
	gateway := "external-huaxin-" + account + "01-" + redeemID
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE account_id=$1 AND trade_date=$4 AND order_stream_id IN ($2,$3) AND symbol='159915' AND order_qty=1000000 AND cum_filled_qty=1000000 AND status='filled' AND ((order_stream_id=$2 AND trade_side='B') OR (order_stream_id=$3 AND trade_side='R'))`, account, buyID, redeemID, compactToISODate(spec.SourceDate)).Scan(&count); err != nil {
		return plan, err
	}
	if count != 2 {
		return plan, errors.New("T0 buy/redemption no longer match reviewed terminal orders")
	}
	rows, err := tx.QueryContext(ctx, `SELECT component_symbol, sum(component_qty) FROM etf_component_transfers WHERE account_id=$1 AND trade_date=$3 AND gateway_order_id=$2 AND component_symbol<>'159915' AND NOT cash_substitution AND price=0 GROUP BY component_symbol`, account, gateway, compactToISODate(spec.SourceDate))
	if err != nil {
		return plan, err
	}
	transfers := make(map[string]int64)
	for rows.Next() {
		var code string
		var qty int64
		if err := rows.Scan(&code, &qty); err != nil {
			rows.Close()
			return plan, err
		}
		transfers[code] = qty
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	if len(transfers) != 98 {
		return plan, errors.New("component transfer coverage differs")
	}
	for code, qty := range expected {
		if transfers[code] != qty {
			return plan, fmt.Errorf("PCF/transfer quantity mismatch %s", code)
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT symbol, sum(qty), (sum(price*qty)*100)::bigint, count(DISTINCT gateway_order_id) FROM fills WHERE account_id=$1 AND trade_date=$3 AND ((trade_side='S' AND symbol<>'159915') OR (trade_side='B' AND order_stream_id=$2)) GROUP BY symbol,trade_side ORDER BY symbol,trade_side`, account, buyID, compactToISODate(spec.SourceDate))
	if err != nil {
		return plan, err
	}
	trades := make([]recovery20261027Trade, 0)
	for rows.Next() {
		var t recovery20261027Trade
		if err := rows.Scan(&t.Code, &t.Qty, &t.GrossCents, &t.Orders); err != nil {
			rows.Close()
			return plan, err
		}
		if _, ok := expected[t.Code]; ok || t.Code == "159915" {
			trades = append(trades, t)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	var buyGross, sellGross, buyFee, sellFee int64
	executionIDs := make([]string, 0)
	for _, t := range trades {
		buy := t.Code == "159915"
		if (!buy && t.Qty != expected[t.Code]) || (buy && (t.Qty != 1000000 || t.Orders != 1)) {
			return plan, errors.New("component sales or buy quantity ambiguous")
		}
		matches := make([]brokerCashFlowRow, 0)
		for _, f := range flows {
			if f.TradeDate != spec.SourceDate || f.Code != t.Code {
				continue
			}
			if buy {
				if f.Subject == "买入成交清算资金" {
					m := recovery20261027Execution.FindStringSubmatch(f.Summary)
					if m != nil && m[2] == "1000000" {
						matches = append(matches, f)
					}
				}
			} else if f.Subject == "卖出成交清算资金" {
				matches = append(matches, f)
			}
		}
		fee, ids, err := recovery20261027ExecutionFee(t, matches, buy)
		if err != nil {
			return plan, fmt.Errorf("%s: %w", t.Code, err)
		}
		executionIDs = append(executionIDs, ids...)
		if buy {
			buyGross = t.GrossCents
			buyFee = fee
		} else {
			sellGross += t.GrossCents
			sellFee += fee
		}
	}
	if len(trades) != 99 || buyGross == 0 {
		return plan, errors.New("incomplete executions")
	}
	var feeCents, cashCents int64
	feeCodes := make(map[string]bool)
	for _, f := range flows {
		if f.TradeDate < spec.SourceDate || !strings.Contains(f.Summary, "成交日"+spec.SourceDate) {
			continue
		}
		kind := ""
		if strings.HasPrefix(f.Summary, "深圳ETF赎回159915(") && strings.HasSuffix(f.Summary, "过户费，成交日"+spec.SourceDate) {
			code := strings.TrimSuffix(strings.TrimPrefix(f.Summary, "深圳ETF赎回159915("), ")过户费，成交日"+spec.SourceDate)
			if expected[code] == 0 || feeCodes[code] || f.TradeDate != spec.FeeDate || f.InCents != 0 || f.OutCents <= 0 {
				return plan, errors.New("ambiguous deferred component transfer fee")
			}
			feeCodes[code] = true
			feeCents += f.OutCents
			kind = "deferred_redemption_transfer_fee"
		} else if (f.Subject == "ETF现金差额划出" || f.Subject == "ETF现金差额划入") && f.Code == "159915" && strings.HasSuffix(f.Summary, redeemID[5:]) {
			amount := f.InCents - f.OutCents
			if f.TradeDate != spec.CashDate || amount != spec.CashComponentCents || f.InCents < 0 || f.OutCents < 0 || (f.InCents != 0 && f.OutCents != 0) || cashCents != 0 || (amount > 0) != (f.Subject == "ETF现金差额划入") {
				return plan, errors.New("cash component evidence mismatch")
			}
			cashCents = amount
			kind = "cash_component_payment"
			if amount > 0 {
				kind = "cash_component_receipt"
			}
		} else {
			return plan, fmt.Errorf("unclassified source-day cash evidence %s", f.FlowID)
		}
		at, err := time.ParseInLocation("20060102 15:04:05", f.TradeDate+" "+f.TradeTime, timeutil.Location())
		if err != nil {
			return plan, err
		}
		id := "broker-" + spec.Tag + "-" + account + "-" + f.FlowID
		amount := centsFloat(f.InCents - f.OutCents)
		scope := "separate_actual_payable_leg"
		if amount > 0 {
			scope = "separate_actual_receivable_leg"
		}
		plan.CashEntries = append(plan.CashEntries, ledger.CashLedgerEntry{EntryID: id, AccountID: account, TradeDate: compactToISODate(f.TradeDate), LedgerType: "settlement", FlowClass: "settlement_adjustment", Currency: "CNY", Amount: amount, CashBucket: "broker_total_cash", CounterpartyBucket: "etf_fund_settlement", EffectiveAt: at, Status: "confirmed", IdempotencyKey: id, GatewayOrderID: gateway, Source: spec.Source, CreatedBy: "user_authorized_relay_" + spec.Tag, Description: f.Summary,
			RawPayload: map[string]any{"recurring_import": false, "broker_flow_id": f.FlowID, "broker_subject": f.Subject, "statement_sha256": hashes[1], "settlement_kind": "etf_redemption_final_settlement", "source_trade_date": compactToISODate(spec.SourceDate), "source_accrual_amount": amount, "released_estimate": amount, "source_accrual_scope": scope, "settlement_leg": kind, "settlement_group_id": "broker-final:" + account + ":" + spec.SourceDate + ":159915.SZ:" + f.FlowID, "receipt_stage": "initial", "confirmation_source": "broker_cash_flow_and_meridian_pcf"}})
	}
	if len(feeCodes) != 98 || feeCents != spec.DeferredFeeCents || cashCents != spec.CashComponentCents {
		return plan, errors.New("deferred cash coverage incomplete")
	}
	sort.Slice(plan.CashEntries, func(i, j int) bool { return plan.CashEntries[i].EntryID < plan.CashEntries[j].EntryID })
	plan.Finalization = ledger.ETFSettlementFinalization{AccountID: account, SourceTradeDate: compactToISODate(spec.SourceDate), SecurityID: "159915.SZ", Status: "confirmed", SettlementComplete: true, RedemptionQuantity: 1000000, RedemptionUnit: 1000000, BuyGrossAmount: centsFloat(buyGross), ComponentSaleGrossAmount: centsFloat(sellGross), ActualCashComponent: centsFloat(cashCents), ActualTotalFee: centsFloat(buyFee + sellFee + feeCents), SourceCloseSettlementCarry: centsFloat(cashCents - feeCents), GrossContribution: centsFloat(sellGross - buyGross + cashCents), NetContribution: centsFloat(sellGross - buyGross + cashCents - buyFee - sellFee - feeCents), PCFTradeDate: compactToISODate(spec.FeeDate), PCFSchemaVersion: "etf_cash_component.v1", Source: spec.Source,
		RawPayload: map[string]any{"recurring_import": false, "funds_sha256": hashes[0], "flows_sha256": hashes[1], "redemption_gateway_order_id": gateway, "buy_order_stream_id": buyID, "execution_flow_ids": executionIDs, "component_securities": 98, "zero_exposure_pcf_rows": 2, "fee_breakdown": map[string]any{"buy": centsFloat(buyFee), "component_sales": centsFloat(sellFee), "redemption_transfer": centsFloat(feeCents)}}}
	for i, f := range funds {
		if f.TradeDate < spec.SourceDate || f.TradeDate > spec.BasisThrough {
			continue
		}
		if i == 0 || f.DepositCents != 0 || f.WithdrawalCents != 0 {
			return plan, errors.New("unexpected external flow or missing opening basis")
		}
		openCarry, closeCarry := spec.carry(f.TradeDate)
		var principal int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(qty)*10000,0) FROM fills WHERE account_id=$1 AND trade_date=$2 AND symbol='204001' AND trade_side='S'`, account, compactToISODate(f.TradeDate)).Scan(&principal); err != nil {
			return plan, err
		}
		other := f.TotalCents - f.CashCents - f.MarketCents
		if principal != other {
			return plan, fmt.Errorf("broker other asset is not fully explained by repo on %s: %d/%d", f.TradeDate, other, principal)
		}
		// Date-only broker statement: this is a basis, not a fabricated OC observation.
		at, _ := time.ParseInLocation("20060102", f.TradeDate, timeutil.Location())
		plan.Bases = append(plan.Bases, ledger.AssetPositionObservation{AccountID: account, TradeDate: compactToISODate(f.TradeDate), SnapshotType: "reconcile", CashTotal: centsFloat(f.CashCents), CashAvailable: centsFloat(f.CashCents), NetAsset: centsFloat(f.TotalCents), MarketValue: centsFloat(f.MarketCents), ReverseRepoReceivable: centsFloat(principal), Source: spec.Source, CapturedAt: at,
			RawPayload: map[string]any{"economic_nav_base_confirmed": true, "recurring_import": false, "asset_scope": "broker_reported_total_asset_excluding_fund_occupancy", "statement_path": "reference/" + account + "_资金.csv", "statement_sha256": hashes[0], "statement_row_number": f.RowNumber, "reported_open_total_asset": centsFloat(funds[i-1].TotalCents), "reported_close_total_asset": centsFloat(f.TotalCents), "reported_daily_pnl": centsFloat(f.DailyPnLCents), "reported_deposit": float64(0), "reported_withdrawal": float64(0), "open_outstanding_etf_settlement_asset": openCarry, "close_outstanding_etf_settlement_asset": closeCarry, "broker_other_asset": centsFloat(other), "verified_reverse_repo_principal": centsFloat(principal), "observation_precision": "trade_date_only"}})
	}
	if len(plan.Bases) != 4 {
		return plan, errors.New("incomplete broker basis dates")
	}
	plan.Evidence["execution_aggregates"] = trades
	plan.Evidence["verified_adjacent_cash_days"] = len(funds) - 1
	return plan, nil
}

func recovery20261027Carry(date string) (float64, float64) {
	switch date {
	case "20261021":
		return 0, -665.40
	case "20261022":
		return -665.40, -652.34
	case "20261023":
		return -652.34, 0
	default:
		return 0, 0
	}
}

type recovery20261027Preview struct {
	*performancePreviewStore
	plans map[string]recovery20261027Plan
}

func (s *recovery20261027Preview) ListETFSettlementFinalizations(ctx context.Context, account, date string) ([]ledger.ETFSettlementFinalization, error) {
	base, err := s.Repository.ListETFSettlementFinalizations(ctx, account, date)
	if err != nil {
		return nil, err
	}
	if p, ok := s.plans[account]; ok && date == p.Finalization.SourceTradeDate {
		if len(base) != 0 {
			return nil, errors.New("existing finalization requires duplicate review")
		}
		return []ledger.ETFSettlementFinalization{p.Finalization}, nil
	}
	return base, nil
}

func (s *recovery20261027Preview) GetAssetPositionObservation(ctx context.Context, account, date, kind string) (ledger.AssetPositionObservation, error) {
	if kind == "reconcile" {
		for _, b := range s.plans[account].Bases {
			if b.TradeDate == date {
				return b, nil
			}
		}
	}
	return s.Repository.GetAssetPositionObservation(ctx, account, date, kind)
}

func (s *recovery20261027Preview) ListCashLedgerEntries(ctx context.Context, q ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error) {
	base, err := s.Repository.ListCashLedgerEntries(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, e := range s.plans[q.AccountID].CashEntries {
		if (q.TradeDate != "" && e.TradeDate != q.TradeDate) || (q.DateFrom != "" && e.TradeDate < q.DateFrom) || (q.DateTo != "" && e.TradeDate > q.DateTo) || (q.FlowClass != "" && e.FlowClass != q.FlowClass) || (q.Status != "" && e.Status != q.Status) {
			continue
		}
		for _, old := range base {
			if old.EntryID == e.EntryID || old.RawPayload["broker_flow_id"] == e.RawPayload["broker_flow_id"] {
				return nil, errors.New("cash evidence already exists; review before replay")
			}
		}
		base = append(base, e)
	}
	return base, nil
}
