package main

import (
	"strings"
	"testing"
)

func basis20261110Fixture() (brokerBasis20261110Spec, *brokerCSV, *brokerCSV) {
	makeCSV := func(header string, rows [][]string) *brokerCSV {
		h := strings.Split(header, ",")
		index := map[string]int{}
		for i, k := range h {
			index[k] = i
		}
		return &brokerCSV{header: h, index: index, rows: rows}
	}
	s := brokerBasis20261110Spec{Account: "314000045768", From: "20261104", FundsHash: "reviewed-funds", FlowsHash: "reviewed-flows", Days: 1}
	f := makeCSV("客户号,客户资金,入金,出金,市值,当日盈亏,总资产,交易日,", [][]string{
		{s.Account, "100000", "0", "0", "20000", "0", "120000", "20261103", ""},
		{s.Account, "90000", "0", "0", "20000", "500", "120500", "20261104", ""},
	})
	c := makeCSV("交易日期,交易时间,资金账户,流水号,股票代码,股票名称,业务科目,投资者,名称,入金,出金,资金余额,操作摘要,", [][]string{
		{"20261104", "14:00:00", s.Account + "01", "repo", "204001", "GC001", "回购融券成交划出资金", s.Account, "test", "0", "10000.10", "B融券成交GC001(204001)10手，利率1.000", ""},
		{"20261104", "15:00:00", s.Account + "01", "interest", "", "", "利息归本", s.Account, "test", "0.10", "0", "interest", ""},
	})
	return s, f, c
}

func TestBrokerBasis20261110PreservesReportedTotalWithoutInventingRepo(t *testing.T) {
	s, f, c := basis20261110Fixture()
	days, err := buildBrokerBasis20261110(s, f, c)
	if err != nil || len(days) != 1 {
		t.Fatalf("%v %v", days, err)
	}
	d := days[0]
	if d.Asset.NetAsset != 120500 || d.Asset.ReverseRepoReceivable != 10000 || d.Raw["broker_unclassified_asset_component"] != float64(500) {
		t.Fatal("reported total or repo proof overwritten")
	}
	if d.Raw["broker_asset_base_authoritative"] != true || d.Raw["economic_nav_base_confirmed"] != false || d.Asset.CashAvailable != 0 {
		t.Fatal("asset authority must not assert economic NAV, clearing or available cash")
	}
	if _, ok := d.Raw["close_outstanding_etf_settlement_asset"]; ok {
		t.Fatal("missing settlement evidence must not become zero rights")
	}
	if d.Raw["observation_precision"] != "trade_date_only" || d.Gold.AssetScope != "broker_reported_total_asset" {
		t.Fatal("inferred broker timestamp or economic scope")
	}
}

func TestBrokerBasis20261110RejectsBadEvidence(t *testing.T) {
	cases := map[string]func(*brokerBasis20261110Spec, *brokerCSV, *brokerCSV){
		"wrong fund owner":   func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { f.rows[0][0] = "314000046830" },
		"wrong flow owner":   func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.rows[0][7] = "314000046830" },
		"wrong cash account": func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.rows[0][2] = "wrong" },
		"duplicate flow":     func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.rows[1][3] = c.rows[0][3] },
		"cash mismatch":      func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { f.rows[1][1] = "90001" },
		"asset mismatch":     func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { f.rows[1][6] = "120501" },
		"repo symbol":        func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.rows[0][4] = "131810" },
		"repo units":         func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.rows[0][11] = "unknown units" },
		"repo debit":         func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.rows[0][10] = "9999.99" },
		"layout":             func(_ *brokerBasis20261110Spec, f, c *brokerCSV) { c.header[11] = "unknown" },
		"missing date":       func(s *brokerBasis20261110Spec, f, c *brokerCSV) { s.Days = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, f, c := basis20261110Fixture()
			mutate(&s, f, c)
			if _, err := buildBrokerBasis20261110(s, f, c); err == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
}
