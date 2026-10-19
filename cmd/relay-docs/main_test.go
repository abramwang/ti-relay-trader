package main

import (
	"strings"
	"testing"

	relayconfig "ti-relay-trader/internal/config"
)

func TestTradeTerminalUsesMatchedAtForFillTimes(t *testing.T) {
	script, err := portalAssets.ReadFile("web/static/trade-terminal.js")
	if err != nil {
		t.Fatalf("read trade terminal script: %v", err)
	}
	text := string(script)
	for _, required := range []string{
		`<th>成交时间</th>`,
		`formatTime(fill.matched_at)`,
		`minuteLabel(fill.matched_at)`,
		`测试柜台状态时钟`,
		`hour12: false, timeZone: "Asia/Shanghai"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("trade terminal is missing fill-time contract %q", required)
		}
	}
	if strings.Contains(text, `minuteLabel(fill.matched_at || fill.match_timestamp)`) {
		t.Fatal("trade terminal still falls back from matched_at to match_timestamp")
	}
}

func TestTradeTerminalPrefersPublishedAccountDayPnLToPositionCostEstimate(t *testing.T) {
	script, err := portalAssets.ReadFile("web/static/trade-terminal.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`const published = publishedAccountDayProfit(asset);`,
		`asset.account_id !== accountID`,
		`compactDate(asset.account_day_pnl_trade_date) !== compactDate(tradeDate)`,
		`return finiteNumber(asset.account_day_pnl);`,
		`dayProfit: publishedAccountDayProfit(asset, accountID, tradeDate) ??`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("missing published account-day PnL guard: %s", required)
		}
	}
}

func TestTradeTerminalDefaultsPerformanceToLatestAuthoritativeMonth(t *testing.T) {
	script, err := portalAssets.ReadFile("web/static/trade-terminal.js")
	if err != nil {
		t.Fatalf("read trade terminal script: %v", err)
	}
	text := string(script)
	for _, required := range []string{
		`shiftCompactMonth(day, -1)`,
		`/performance/default-range?reference_date=`,
		`defaultRange.available !== true`,
		`state.performanceRangeMode = "custom"`,
		`已回退最近权威日`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("trade terminal is missing authoritative performance default %q", required)
		}
	}
}

func TestTradeTerminalDoesNotOverrideAssetWithIncompletePositionValuation(t *testing.T) {
	script, err := portalAssets.ReadFile("web/static/trade-terminal.js")
	if err != nil {
		t.Fatalf("read trade terminal script: %v", err)
	}
	text := string(script)
	for _, required := range []string{
		`const liveTotals = livePortfolioTotals();`,
		`quantity !== null && quantity > 0`,
		`rowMarketValue === null || rowMarketValue <= 0`,
		`return null;`,
		`liveTotals ? liveTotals.netAsset : asset.net_asset`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("trade terminal is missing incomplete valuation guard %q", required)
		}
	}
}

func TestTradeTerminalSupportsFuturesMarketDataWithoutEnablingTrading(t *testing.T) {
	script, err := portalAssets.ReadFile("web/static/trade-terminal.js")
	if err != nil {
		t.Fatalf("read trade terminal script: %v", err)
	}
	text := string(script)
	for _, required := range []string{
		`/^(IF|IH|IC|IM)\d{0,4}$/`,
		`/v1/meridian/futures/snapshots?`,
		`/v1/meridian/futures/bars?`,
		`/v1/meridian/stream/futures/snapshots?`,
		`当前 Relay/OC 尚未接入期货交易路由`,
		`当前仅支持股指期货行情，未发送下单请求`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("trade terminal is missing futures market-data guard %q", required)
		}
	}
}

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

func TestBrokerDisplayLabelUsesKnownNameAndUnknownID(t *testing.T) {
	if got := brokerDisplayLabel("huaxin"); got != "华鑫证券 (huaxin)" {
		t.Fatalf("huaxin label = %q", got)
	}
	if got := brokerDisplayLabel("future-broker"); got != "future-broker" {
		t.Fatalf("future broker label = %q", got)
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
