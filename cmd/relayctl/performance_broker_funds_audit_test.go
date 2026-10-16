package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBrokerFundsAuditHistoricalSliceRequiresRealPredecessor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "funds.csv")
	body := "客户号,客户资金,入金,出金,市值,当日盈亏,总资产,交易日\n" +
		"acct-1,1000.00,0.00,0.00,0.00,1.00,1000.00,20261008\n" +
		"acct-1,1002.00,0.00,0.00,0.00,2.00,1002.00,20261009\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := parseCLITradeDate("20261008")
	second, _ := parseCLITradeDate("20261009")
	plan, err := buildBrokerFundsAuditPlan(context.Background(), nil, "acct-1", path, "", second, second, 0)
	if err != nil || !plan.report.QualityGatePassed || plan.report.FundsIdentityFailures != 0 {
		t.Fatalf("historical slice rejected: plan=%+v error=%v", plan, err)
	}
	if _, err := buildBrokerFundsAuditPlan(context.Background(), nil, "acct-1", path, "", first, second, 0); err == nil {
		t.Fatal("accepted a recovery day without a real predecessor")
	}
	body = "客户号,客户资金,入金,出金,市值,当日盈亏,总资产,交易日\n" +
		"acct-1,1000.00,0.00,0.00,0.00,1.00,1000.00,20261008\n" +
		"acct-1,1003.00,0.00,0.00,0.00,2.00,1003.00,20261009\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err = buildBrokerFundsAuditPlan(context.Background(), nil, "acct-1", path, "", second, second, 0)
	if err != nil || plan.report.QualityGatePassed || plan.report.FundsIdentityFailures != 1 {
		t.Fatalf("accepted broken adjacent asset identity: plan=%+v error=%v", plan, err)
	}
}

func TestReadBrokerTransactionEvidenceIgnoresManualFreezes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions.csv")
	body := "客户号,方向,成交日期,成交时间\n" +
		"acct-1,手工冻结,20260910,22:30:00\n" +
		"acct-1,卖出,20260910,09:30:02\n" +
		"acct-1,卖出,20260910,09:30:01\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	evidence, err := readBrokerTransactionEvidence("acct-1", path)
	if err != nil {
		t.Fatalf("readBrokerTransactionEvidence() error = %v", err)
	}
	if evidence.rowsByDate["20260910"] != 2 {
		t.Fatalf("rows = %d, want 2", evidence.rowsByDate["20260910"])
	}
	if evidence.firstIntradayByDate["20260910"] != "09:30:01" {
		t.Fatalf("first intraday = %q", evidence.firstIntradayByDate["20260910"])
	}
}
