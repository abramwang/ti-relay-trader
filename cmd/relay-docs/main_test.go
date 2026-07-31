package main

import (
	"strings"
	"testing"

	relayconfig "ti-relay-trader/internal/config"
)

func TestPortalAccountRowsPreferDatabaseAliases(t *testing.T) {
	rows := portalAccountRowsHTML(
		[]relayconfig.AccountRouteConfig{{
			AccountID:      "account-1",
			Alias:          "配置别名",
			BrokerID:       "broker-1",
			GatewayID:      "gateway-1",
			Enabled:        true,
			TradingEnabled: false,
		}},
		"生产环境",
		map[string]string{"account-1": "数据库别名"},
	)
	if !strings.Contains(rows, "数据库别名") || strings.Contains(rows, "配置别名") {
		t.Fatalf("rows did not prefer database alias: %s", rows)
	}
	if !strings.Contains(rows, "account-1") || !strings.Contains(rows, "只读") {
		t.Fatalf("rows lost account identity or trading state: %s", rows)
	}
}

func TestPortalAccountRowsEscapeAliases(t *testing.T) {
	rows := portalAccountRowsHTML(
		[]relayconfig.AccountRouteConfig{{AccountID: "account-1"}},
		"测试环境",
		map[string]string{"account-1": `<script>alert("x")</script>`},
	)
	if strings.Contains(rows, "<script>") || !strings.Contains(rows, "&lt;script&gt;") {
		t.Fatalf("rows did not escape alias: %s", rows)
	}
}
