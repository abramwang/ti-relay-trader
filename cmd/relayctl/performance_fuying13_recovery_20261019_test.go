package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuying13RecoveryAuditsHistoricalSliceWithoutZeroInception(t *testing.T) {
	funds, flows := fuying13RecoveryFixture(t, "", "")
	plan, err := buildFuying13RecoveryPlan(funds, flows)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PreviousAsset != 2000 || plan.VerifiedAdjacentDays != 2 || len(plan.Days) != 2 {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if len(plan.CashEntries) != 1 || plan.CashEntries[0].Amount != -1000 || plan.CashEntries[0].EffectiveAt.Format("15:04:05-07:00") != "13:46:30+08:00" {
		t.Fatalf("external flow timing or classification lost: %+v", plan.CashEntries)
	}
}

func TestFuying13RecoveryRejectsUnprovenInputs(t *testing.T) {
	for _, tc := range []struct{ name, fundsOld, fundsNew, flowsOld, flowsNew string }{
		{"asset bridge", "1001.00,20261009", "1001.01,20261009", "", ""},
		{"cash bridge", "", "", "1000,0,trade", "999,0,trade"},
		{"account", fuying13Account + ",2000.00", "other,2000.00", "", ""},
		{"flow account", "", "", "31400004576801", "other01"},
		{"ETF settlement", "", "", "卖出成交清算资金", "ETF现金替代退款"},
		{"ETF fee", "", "", "trade", "深圳ETF赎回费用"},
		{"duplicate flow", "", "", ",f2,", ",f1,"},
		{"opening anchor", "20260908", "20260907", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			funds, flows := fuying13RecoveryFixture(t, tc.fundsOld+"\x00"+tc.fundsNew, tc.flowsOld+"\x00"+tc.flowsNew)
			if _, err := buildFuying13RecoveryPlan(funds, flows); err == nil {
				t.Fatal("accepted unproven recovery")
			}
		})
	}
}

func fuying13RecoveryFixture(t *testing.T, fundsReplacement, flowsReplacement string) (*brokerCSV, *brokerCSV) {
	t.Helper()
	funds := "客户号,客户资金,入金,出金,市值,当日盈亏,总资产,交易日\n" +
		fuying13Account + ",2000.00,0,0,0,0,2000.00,20260908\n" +
		fuying13Account + ",3000.00,0,0,0,1000,3000.00,20260909\n" +
		fuying13Account + ",1001.00,0,1000,0,-999,1001.00,20261009\n"
	flowHeader := "交易日期,交易时间,资金账户,流水号,股票代码,股票名称,业务科目,投资者,名称,入金,出金,资金余额,操作摘要,\n"
	flows := flowHeader +
		"20260909,18:00:00,31400004576801,f1,600000,stock,卖出成交清算资金," + fuying13Account + ",product,1000,0,trade,\n" +
		"20261009,18:00:00,31400004576801,f2,600000,stock,买入成交清算资金," + fuying13Account + ",product,0,999,trade,\n" +
		"20261009,13:46:30,31400004576801,f3,,,银证转帐取," + fuying13Account + ",product,0,1000,bank withdrawal,\n"
	read := func(name, body, replacement string) *brokerCSV {
		parts := strings.Split(replacement, "\x00")
		if len(parts) == 2 && parts[0] != "" {
			body = strings.ReplaceAll(body, parts[0], parts[1])
		}
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		data, _, err := readBrokerCSV(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	return read("funds.csv", funds, fundsReplacement), read("flows.csv", flows, flowsReplacement)
}
