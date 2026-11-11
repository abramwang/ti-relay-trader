package main

import (
	"fmt"
	"math"
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func fuyingRepoFixture() (trading.Order, []trading.Fill, []brokerCashFlowRow) {
	o := trading.Order{AccountID: fuying13Account, GatewayOrderID: fuying23Prefix + "110010623782460", OrderStreamID: "110010623782460", TradeDate: "2026-10-20", Symbol: "204001", Exchange: trading.ExchangeSH, TradeSide: trading.TradeSideSell, BusinessType: trading.BusinessTypeStock, IsTerminal: true, CumFilledQty: 291630}
	f := trading.Fill{AccountID: o.AccountID, GatewayOrderID: o.GatewayOrderID, OrderStreamID: o.OrderStreamID, TradeDate: o.TradeDate, Symbol: o.Symbol, Exchange: o.Exchange, TradeSide: o.TradeSide, BusinessType: o.BusinessType, FillID: "synthetic-fill", Qty: o.CumFilledQty, Price: 1.38}
	flows := []brokerCashFlowRow{
		{TradeDate: "20261020", TradeTime: "14:30:00", FlowID: "synthetic-debit", Code: "204001", Subject: "回购融券成交划出资金", Summary: "融券成交GC001(204001)29163手，利率1.380", OutCents: 2916329163},
		{TradeDate: "20261021", FlowID: "synthetic-return", Code: "204001", Subject: "回购融券购回资金划入", Summary: "融券购回204001:成交日期20261020，申报委托号0623782460", InCents: 2916410260},
	}
	return o, []trading.Fill{f}, flows
}

func TestFuyingRepoCashProof(t *testing.T) {
	o, f, flows := fuyingRepoFixture()
	p, err := proveFuyingRepoFee(o, f, flows)
	if err != nil || p.Fee.TotalFee != 291.63 || p.InterestPaid != 1102.6 || p.Principal != 29163000 || !p.Fee.FeeComplete || p.Fee.Commission != 0 {
		t.Fatalf("proof %#v %v", p, err)
	}
	for name, mutate := range map[string]func(*trading.Order, []trading.Fill, []brokerCashFlowRow){
		"account":      func(o *trading.Order, _ []trading.Fill, _ []brokerCashFlowRow) { o.AccountID = "other" },
		"missing fill": func(_ *trading.Order, f []trading.Fill, _ []brokerCashFlowRow) { f[0].Qty-- },
		"nan rate":     func(_ *trading.Order, f []trading.Fill, _ []brokerCashFlowRow) { f[0].Price = math.NaN() },
		"quantity": func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) {
			f[0].Summary = "融券成交GC001(204001)29162手，利率1.380"
		},
		"rate": func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) {
			f[0].Summary = "融券成交GC001(204001)29163手，利率1.400"
		},
		"cash direction": func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) { f[0].InCents = 1 },
		"principal":      func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) { f[0].OutCents = 2916300000 },
		"maturity order": func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) { f[1].Summary += "other" },
		"maturity date":  func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) { f[1].TradeDate = "20261020" },
		"missing id":     func(_ *trading.Order, _ []trading.Fill, f []brokerCashFlowRow) { f[0].FlowID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			o, f, c := fuyingRepoFixture()
			mutate(&o, f, c)
			if _, err := proveFuyingRepoFee(o, f, c); err == nil {
				t.Fatal("accepted bad evidence")
			}
		})
	}
	if _, err := proveFuyingRepoFee(o, f, append(flows, flows[0])); err == nil {
		t.Fatal("accepted duplicate debit")
	}
}

func fuyingETFCashFixture() (fuyingETFCashProof, []brokerCashFlowRow, map[string]int64) {
	p := fuyingETFCashProof{}
	expected := map[string]int64{}
	flows := []brokerCashFlowRow{
		{TradeDate: "20261104", FlowID: "commission", Summary: "深圳ETF赎回159915佣金:A620008286", OutCents: 12037},
		{TradeDate: "20261106", FlowID: "cash", Code: "159915", Subject: "ETF现金差额划入", Summary: "深圳ETF申赎159915现金差额代收付，成交日20261104，A620008286", InCents: 337695},
	}
	p.Executions = append(p.Executions, fuyingExecutionCashProof{Trade: recovery20261027Trade{Code: "159915", Qty: 1000000, GrossCents: 245200000, Orders: 1}, Buy: true, Date: "2026-11-03", Flows: []brokerCashFlowRow{{TradeDate: "20261103", Code: "159915", Subject: "买入成交清算资金", FlowID: "buy", Summary: "买ETF(159915)1000000股*2.452", OutCents: 245212260}}})
	for i := 0; i < 98; i++ {
		code := fmt.Sprintf("%06d", 300001+i)
		expected[code] = 100
		fee := int64(13)
		if i == 0 {
			fee = 43
		}
		flows = append(flows, brokerCashFlowRow{TradeDate: "20261105", FlowID: "deferred-" + code, Summary: "深圳ETF赎回159915(" + code + ")过户费，成交日20261104", OutCents: fee})
		p.Executions = append(p.Executions, fuyingExecutionCashProof{Date: "2026-11-04", Trade: recovery20261027Trade{Code: code, Qty: 100, GrossCents: 100000, Orders: 1}, Flows: []brokerCashFlowRow{{TradeDate: "20261104", Code: code, Subject: "卖出成交清算资金", FlowID: "sell-" + code, Summary: "卖股(" + code + ")100股*10.000", InCents: 99900}}})
	}
	return p, flows, expected
}

func TestFuyingOvernightActualCashAndDeferredRelease(t *testing.T) {
	p, f, e := fuyingETFCashFixture()
	if err := completeFuyingETFCash(&p, f, e); err != nil {
		t.Fatal(err)
	}
	if p.Summary["total_actual_fees"] != 354.01 || p.Summary["whole_cycle_cash_pnl"] != -2350977.06 || p.Summary["economic_nav_publishable"] != false {
		t.Fatalf("synthetic cash totals: %#v", p.Summary)
	}
	opening := p.Summary["nov04_close_separate_carry"].(float64)
	next := p.Summary["nov05_close_separate_carry"].(float64)
	if math.Abs(-13.04+next-opening) > 1e-8 || math.Abs(3376.95-next) > 1e-8 {
		t.Fatal("cash posting created new PnL")
	}
	for name, mutate := range map[string]func(*fuyingETFCashProof, *[]brokerCashFlowRow){
		"missing source commission": func(_ *fuyingETFCashProof, f *[]brokerCashFlowRow) { *f = (*f)[1:] },
		"duplicate cash":            func(_ *fuyingETFCashProof, f *[]brokerCashFlowRow) { *f = append(*f, (*f)[1]) },
		"missing deferred fee":      func(_ *fuyingETFCashProof, f *[]brokerCashFlowRow) { *f = (*f)[:len(*f)-1] },
		"wrong receipt day":         func(_ *fuyingETFCashProof, f *[]brokerCashFlowRow) { (*f)[1].TradeDate = "20261105" },
		"unclassified refund": func(_ *fuyingETFCashProof, f *[]brokerCashFlowRow) {
			*f = append(*f, brokerCashFlowRow{FlowID: "unknown", Summary: "159915退款成交日20261104", InCents: 1})
		},
		"core cost contamination": func(p *fuyingETFCashProof, _ *[]brokerCashFlowRow) { p.Executions[0].Date = "2026-11-04" },
		"cash reused":             func(p *fuyingETFCashProof, _ *[]brokerCashFlowRow) { p.Executions[1].Flows[0].FlowID = "cash" },
		"partial component":       func(p *fuyingETFCashProof, _ *[]brokerCashFlowRow) { p.Executions[1].Trade.Qty-- },
		"duplicate component":     func(p *fuyingETFCashProof, _ *[]brokerCashFlowRow) { p.Executions[2] = p.Executions[1] },
	} {
		t.Run(name, func(t *testing.T) {
			p, f, e := fuyingETFCashFixture()
			mutate(&p, &f)
			if err := completeFuyingETFCash(&p, f, e); err == nil {
				t.Fatal("accepted incomplete cash scope")
			}
		})
	}
}

func TestFuyingRepoFeePreviewPaginationAndIsolation(t *testing.T) {
	o, f, c := fuyingRepoFixture()
	p, err := proveFuyingRepoFee(o, f, c)
	if err != nil {
		t.Fatal(err)
	}
	q := ledger.OrderFeeRecordQuery{AccountID: fuying13Account, TradeDate: o.TradeDate, Limit: 1}
	old := []ledger.OrderFeeRecord{{AccountID: fuying13Account, TradeDate: o.TradeDate, FeeRecordID: "old", GatewayOrderID: "other"}}
	got, err := mergeFuyingRepoFees(old, []ledger.OrderFeeRecord{p.Fee}, q)
	if err != nil || len(got) != 1 || got[0].GatewayOrderID != o.GatewayOrderID {
		t.Fatalf("first page: %v %v", got, err)
	}
	q.Cursor = "1"
	got, err = mergeFuyingRepoFees(old, []ledger.OrderFeeRecord{p.Fee}, q)
	if err != nil || len(got) != 1 || got[0].FeeRecordID != "old" {
		t.Fatal("pagination lost old record")
	}
	q.Cursor = "2"
	got, err = mergeFuyingRepoFees(old, []ledger.OrderFeeRecord{p.Fee}, q)
	if err != nil || len(got) != 0 {
		t.Fatal("repeated overlay on later page")
	}
	q.Cursor = "-1"
	if _, err = mergeFuyingRepoFees(nil, []ledger.OrderFeeRecord{p.Fee}, q); err == nil {
		t.Fatal("negative cursor")
	}
	q.Cursor = ""
	if _, err = mergeFuyingRepoFees([]ledger.OrderFeeRecord{p.Fee}, []ledger.OrderFeeRecord{p.Fee}, q); err == nil {
		t.Fatal("silently overwrote prior evidence")
	}
	for _, change := range []func(*ledger.OrderFeeRecordQuery){
		func(q *ledger.OrderFeeRecordQuery) { q.AccountID = "307000051387" }, func(q *ledger.OrderFeeRecordQuery) { q.TradeDate = "2026-10-21" }, func(q *ledger.OrderFeeRecordQuery) { q.GatewayOrderID = "other" }, func(q *ledger.OrderFeeRecordQuery) { v := false; q.FeeComplete = &v },
	} {
		qq := q
		change(&qq)
		got, err := mergeFuyingRepoFees(nil, []ledger.OrderFeeRecord{p.Fee}, qq)
		if err != nil || len(got) != 0 {
			t.Fatal("preview leaked evidence")
		}
	}
}

func TestFuyingCashReviewRejectsWriteFlags(t *testing.T) {
	for _, args := range [][]string{{"-persist"}, {"-apply"}, {"-account", "other"}, {"-date", "20261105"}, {"unexpected"}} {
		if err := runFuyingCashReview(args); err == nil {
			t.Fatal("accepted write or expanded scope")
		}
	}
	if err := runFuying13Review20261110([]string{"-broker-cash-preview", "-output", "unused"}); err == nil {
		t.Fatal("cash preview skipped earlier gates")
	}
}
