package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type fuyingRepoCashProof struct {
	Order        trading.Order         `json:"order"`
	Fills        []trading.Fill        `json:"fills"`
	Debit        brokerCashFlowRow     `json:"debit"`
	Maturity     brokerCashFlowRow     `json:"maturity"`
	Principal    float64               `json:"principal"`
	InterestPaid float64               `json:"interest_paid_not_automatically_accrued"`
	Fee          ledger.OrderFeeRecord `json:"fee_memory_only"`
}

type fuyingCashReview struct {
	AccountID        string                `json:"account_id"`
	GeneratedAt      time.Time             `json:"generated_at"`
	DatabaseReadOnly bool                  `json:"database_read_only"`
	DatabaseWrites   int                   `json:"database_writes"`
	BrokerCommands   int                   `json:"broker_commands"`
	NAVPublishable   bool                  `json:"economic_nav_publishable"`
	FundsHash        string                `json:"funds_sha256"`
	FlowsHash        string                `json:"flows_sha256"`
	AdjacentCashDays int                   `json:"adjacent_cash_days_verified"`
	Repo             []fuyingRepoCashProof `json:"repo_fee_evidence"`
	ETF              fuyingETFCashProof    `json:"overnight_etf_cash_evidence"`
}

var fuyingRepoDays = []string{"2026-10-20", "2026-10-21", "2026-10-26", "2026-10-27", "2026-10-30", "2026-11-03"}
var fuyingRepoExpected = map[string]int64{"110010623782460": 291630, "110010623627969": 217940, "110010625438322": 137360, "110010623445325": 143810, "110010623445326": 99990, "110010625345663": 252300, "110010621111207": 192120}
var fuyingRepoDebit = regexp.MustCompile(`融券成交GC001\(204001\)([0-9]+)手，利率([0-9]+\.[0-9]{3})$`)
var fuyingSHOrder = regexp.MustCompile(`^11001([0-9]{10})$`)

func proveFuyingRepoFee(order trading.Order, fills []trading.Fill, flows []brokerCashFlowRow) (fuyingRepoCashProof, error) {
	p := fuyingRepoCashProof{Order: order, Fills: fills}
	ref := fuyingSHOrder.FindStringSubmatch(order.OrderStreamID)
	if order.AccountID != fuying13Account || order.Symbol != "204001" || order.Exchange != trading.ExchangeSH || order.TradeSide != trading.TradeSideSell || order.BusinessType != trading.BusinessTypeStock || !order.IsTerminal || order.CumFilledQty <= 0 || order.CumFilledQty != fuyingRepoExpected[order.OrderStreamID] || ref == nil || order.GatewayOrderID != fuying23Prefix+order.OrderStreamID {
		return p, errors.New("repo order outside reviewed scope")
	}
	var qty int64
	var weighted float64
	seen := map[string]bool{}
	for _, f := range fills {
		if f.AccountID != order.AccountID || f.GatewayOrderID != order.GatewayOrderID || f.OrderStreamID != order.OrderStreamID || f.TradeDate != order.TradeDate || f.Symbol != order.Symbol || f.Exchange != order.Exchange || f.TradeSide != order.TradeSide || f.BusinessType != order.BusinessType || f.FillID == "" || seen[f.FillID] || f.Qty <= 0 || f.Qty > order.CumFilledQty || f.Price <= 0 || math.IsNaN(f.Price) || math.IsInf(f.Price, 0) {
			return p, errors.New("repo execution evidence mismatch")
		}
		seen[f.FillID] = true
		qty += f.Qty
		weighted += f.Price * float64(f.Qty)
	}
	if qty != order.CumFilledQty || qty%10 != 0 {
		return p, errors.New("repo execution quantity not complete")
	}
	date := strings.ReplaceAll(order.TradeDate, "-", "")
	debits, returns := 0, 0
	for _, f := range flows {
		if f.Subject == "回购融券成交划出资金" && f.TradeDate == date && f.Code == "204001" {
			match := fuyingRepoDebit.FindStringSubmatch(f.Summary)
			if match == nil {
				return p, errors.New("unparsed repo debit")
			}
			lots, e := strconv.ParseInt(match[1], 10, 64)
			rate, e2 := strconv.ParseFloat(match[2], 64)
			if e != nil || e2 != nil {
				return p, errors.New("invalid repo summary")
			}
			if lots != qty/10 {
				continue
			}
			if math.Abs(rate-weighted/float64(qty)) > 0.0005001 || f.InCents != 0 || f.OutCents <= 0 {
				return p, errors.New("repo debit rate/direction mismatch")
			}
			p.Debit = f
			debits++
		}
		if f.Subject == "回购融券购回资金划入" && f.Summary == "融券购回204001:成交日期"+date+"，申报委托号"+ref[1] {
			if f.TradeDate <= date || f.Code != "204001" || f.OutCents != 0 || f.InCents <= 0 {
				return p, errors.New("repo maturity scope/direction mismatch")
			}
			p.Maturity = f
			returns++
		}
	}
	principal := qty * 10000 // Shares at CNY100 face value, expressed in cents.
	fee := p.Debit.OutCents - principal
	if debits != 1 || returns != 1 || p.Debit.FlowID == "" || p.Maturity.FlowID == "" || p.Debit.FlowID == p.Maturity.FlowID || fee <= 0 || fee > principal/100 || p.Maturity.InCents < principal {
		return p, errors.New("repo debit or maturity is incomplete/ambiguous")
	}
	at, err := time.ParseInLocation("20060102 15:04:05", p.Debit.TradeDate+" "+p.Debit.TradeTime, timeutil.Location())
	if err != nil {
		return p, err
	}
	p.Principal = centsFloat(principal)
	p.InterestPaid = centsFloat(p.Maturity.InCents - principal)
	p.Fee = ledger.OrderFeeRecord{AccountID: fuying13Account, FeeRecordID: "broker-768-20261111-" + p.Debit.FlowID, TradeDate: order.TradeDate, RecordScope: "order", GatewayOrderID: order.GatewayOrderID, OrderStreamID: order.OrderStreamID, Symbol: "204001", Exchange: "SH", TradeSide: "S", BusinessType: "S", TotalFee: centsFloat(fee), Currency: "CNY", FeeComplete: true, AssociationComplete: true, FeeSource: "broker_cash_debit_reconciled_one_time_review", FeeAsOf: at,
		RawPayload: map[string]any{"recurring_import": false, "debit_flow_id": p.Debit.FlowID, "maturity_flow_id": p.Maturity.FlowID, "fee_components_unverified": true}}
	return p, nil
}

func loadFuyingCashReview(ctx context.Context, tx *sql.Tx, client researchMarket) (fuyingCashReview, error) {
	r := fuyingCashReview{AccountID: fuying13Account, GeneratedAt: timeutil.Now(), DatabaseReadOnly: true}
	spec := brokerBasis20261110Specs[0]
	fundsCSV, fh, err := readBrokerCSV("reference/" + fuying13Account + "_资金.csv")
	if err != nil {
		return r, err
	}
	flowsCSV, ch, err := readBrokerCSV("reference/" + fuying13Account + "_资金流水.csv")
	if err != nil {
		return r, err
	}
	if spec.Account != fuying13Account || fh != spec.FundsHash || ch != spec.FlowsHash {
		return r, errors.New("broker files changed; refresh scope review")
	}
	if _, err := buildBrokerBasis20261110(spec, fundsCSV, flowsCSV); err != nil {
		return r, err
	}
	funds, _, err := parseBrokerFunds(fundsCSV)
	if err != nil {
		return r, err
	}
	flows, _, _, _, err := parseAndAuditCashFlows(flowsCSV, funds)
	if err != nil {
		return r, err
	}
	r.FundsHash, r.FlowsHash, r.AdjacentCashDays = fh, ch, len(funds)-1
	repo := ledger.NewRepository(tx)
	used := map[string]bool{}
	for _, day := range fuyingRepoDays {
		orders, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, TradeDate: day, Symbol: "204001", Exchange: trading.ExchangeSH, Limit: 500})
		if err != nil || len(orders) == 0 || len(orders) >= 500 {
			return r, fmt.Errorf("repo orders incomplete %s: %v", day, err)
		}
		fills, err := repo.ListFills(ctx, trading.FillQuery{AccountID: fuying13Account, TradeDate: day, Symbol: "204001", Exchange: trading.ExchangeSH, Limit: 500})
		if err != nil || len(fills) >= 500 {
			return r, fmt.Errorf("repo fills incomplete %s: %v", day, err)
		}
		for _, o := range orders {
			var selected []trading.Fill
			for _, f := range fills {
				if f.GatewayOrderID == o.GatewayOrderID {
					selected = append(selected, f)
				}
			}
			p, err := proveFuyingRepoFee(o, selected, flows)
			if err != nil {
				return r, err
			}
			for _, id := range []string{p.Debit.FlowID, p.Maturity.FlowID, o.GatewayOrderID} {
				if used[id] {
					return r, errors.New("repo evidence reused")
				}
				used[id] = true
			}
			old, err := repo.ListOrderFeeRecords(ctx, ledger.OrderFeeRecordQuery{AccountID: fuying13Account, TradeDate: day, GatewayOrderID: o.GatewayOrderID, Limit: 500})
			if err != nil || len(old) != 0 {
				return r, fmt.Errorf("repo fee history changed; review conflict: %v", err)
			}
			p.Fee.RawPayload["statement_sha256"] = ch
			r.Repo = append(r.Repo, p)
		}
	}
	if len(r.Repo) != len(fuyingRepoExpected) {
		return r, errors.New("reviewed repo coverage changed")
	}
	pcf, err := client.MarketETFComponents(ctx, url.Values{"security_id": {"159915.SZ"}, "trade_date": {"20261104"}, "limit": {"1000"}})
	if err != nil || pcf.StatusCode != 200 {
		return r, errors.New("source PCF unavailable")
	}
	cash, err := client.MarketETFCashComponents(ctx, url.Values{"security_id": {"159915.SZ"}, "trade_date": {"20261105"}, "limit": {"5"}})
	if err != nil || cash.StatusCode != 200 {
		return r, errors.New("cash PCF unavailable")
	}
	expected, err := verifyActualSettlementPCF(pcf.Payload, cash.Payload, 20261104, 20261105, 3376.95)
	if err != nil {
		return r, err
	}
	r.ETF, err = proveFuyingETFCash(ctx, tx, flows, expected)
	if err != nil {
		return r, err
	}
	r.ETF.PCF, r.ETF.CashPCF = pcf.Payload, cash.Payload
	return r, nil
}

func runFuyingCashReview(args []string) error {
	f := flag.NewFlagSet("performance-fuying13-cash-review-20261111", flag.ContinueOnError)
	configPath := f.String("config", "", "production config")
	out := f.String("output", "", "new private report")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *out == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*out); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("production ledger required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	report, err := loadFuyingCashReview(ctx, tx, client)
	if err != nil {
		return err
	}
	if err := writeETFTransferReviewFile(*out, report); err != nil {
		return err
	}
	return writeJSON(map[string]any{"repo_fees_verified": len(report.Repo), "etf_review": report.ETF.Summary, "database_writes": 0, "economic_nav_publishable": false})
}
