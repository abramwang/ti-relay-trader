package main

import (
	"strings"
	"testing"

	"ti-relay-trader/internal/performance"
)

func refreshFixture() brokerRefreshDay {
	return brokerRefreshDay{Funds: map[string]string{"客户号": fuying13Account, "交易日": "20261113", "客户资金": "6000354.22", "总资产": "37580098.22", "市值": "20262744.00", "入金": "0.00", "出金": "0.00", "当日盈亏": "-921767.05"}, Fees: brokerRefreshFees{Total: 269040, RepoPrincipal: 1131700000, Rows: 110, Components: map[string]int64{"实收佣金": 269040, "印花税": 0, "交易规费": 0, "过户费": 0}}}
}

func TestBrokerRefreshPreservesFailedTransferCash(t *testing.T) {
	d, err := refreshedBrokerAsset("2026-11-13", performance.ReconstructionAssetDay{}, refreshFixture(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if d.ReportedClose != 37580098.22 || d.ObservedOpen != 38501865.27 || d.OtherCents != 0 {
		t.Fatalf("cash removed or repo counted twice: %+v", d)
	}
	fees := d.Evidence["broker_statement_fees"].(map[string]any)
	if fees["total"] != 2690.40 || fees["order_level_verified"] != false || fees["already_in_asset_and_cash"] != true {
		t.Fatal("statement fee scope changed")
	}
	if err := runFuyingTransferPreview([]string{"--config", "unused", "--output", "unused"}); err == nil || !strings.Contains(err.Error(), "transfer failed") {
		t.Fatal("obsolete transfer scenario still available", err)
	}
}

func TestBrokerRefreshRejectsChangedScope(t *testing.T) {
	for name, mutate := range map[string]func(*brokerRefreshDay){
		"account": func(d *brokerRefreshDay) { d.Funds["客户号"] = "307000051387" },
		"date":    func(d *brokerRefreshDay) { d.Funds["交易日"] = "20261112" },
		"outflow": func(d *brokerRefreshDay) { d.Funds["出金"] = "5000000.00" },
		"fee":     func(d *brokerRefreshDay) { d.Fees.Total++ },
	} {
		t.Run(name, func(t *testing.T) {
			d := refreshFixture()
			mutate(&d)
			if _, err := refreshedBrokerAsset("2026-11-13", performance.ReconstructionAssetDay{}, d, strings.Repeat("a", 64)); err == nil {
				t.Fatal("bad refresh accepted")
			}
		})
	}
}
