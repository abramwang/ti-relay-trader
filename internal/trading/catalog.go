package trading

const SchemaVersion = "relay.trading.v1alpha1"

type CatalogDocument struct {
	Version                 string              `json:"version"`
	Enums                   map[string][]string `json:"enums"`
	HTTPRoutes              []HTTPRouteSpec     `json:"http_routes"`
	RedisActions            []string            `json:"redis_actions"`
	TerminalOrderStatuses   []OrderStatus       `json:"terminal_order_statuses"`
	TerminalGatewayStatuses []GatewayStatus     `json:"terminal_gateway_statuses"`
	Models                  []string            `json:"models"`
}

type HTTPRouteSpec struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Request     string `json:"request,omitempty"`
	Response    string `json:"response,omitempty"`
	Description string `json:"description"`
}

func Catalog() CatalogDocument {
	return CatalogDocument{
		Version: SchemaVersion,
		Enums: map[string][]string{
			"exchange":       {"SH", "SZ", "BJ"},
			"trade_side":     {"B", "S", "P", "R"},
			"business_type":  {"S", "E"},
			"offset_type":    {"O", "C"},
			"order_status":   {"created", "accepted", "working", "partially_filled", "filled", "cancelled", "rejected"},
			"gateway_status": {"accepted", "working", "filled", "cancelled", "rejected"},
			"reply_status":   {"accepted", "partial", "completed", "rejected", "failed"},
			"event_type":     {"order.event", "fill.event", "transfer.event", "order.cancel.event"},
		},
		HTTPRoutes: []HTTPRouteSpec{
			{Method: "GET", Path: "/healthz", Response: "StatusView", Description: "service health check"},
			{Method: "GET", Path: "/v1/status", Response: "StatusView", Description: "service status"},
			{Method: "GET", Path: "/v1/accounts", Response: "[]Account", Description: "configured accounts"},
			{Method: "GET", Path: "/v1/account-routes", Response: "[]AccountRoute", Description: "configured account routing, permissions, and Redis stream keys"},
			{Method: "PATCH", Path: "/v1/accounts/{account_id}/alias", Request: "AccountAliasRequest", Response: "Account", Description: "persist account display alias"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/asset", Response: "Asset", Description: "account asset"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/asset/refresh", Response: "RefreshQueryResult", Description: "refresh account asset from front gateway"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/positions", Response: "[]Position", Description: "account positions"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/positions/history", Request: "PositionQuery", Response: "[]Position", Description: "historical account position snapshots"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/positions/refresh", Response: "RefreshQueryResult", Description: "refresh account positions from front gateway"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/daily", Request: "trade_date", Response: "DailyPerformance", Description: "daily account equity and PnL summary"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/contributions", Request: "trade_date", Response: "ContributionResult", Description: "read-only security and strategy daily contribution attribution"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/trade-quality", Request: "trade_date or date_from/date_to", Response: "TradeQualityResult", Description: "read-only execution, cancellation, rejection, and order-fill consistency quality"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/inception", Response: "PerformanceInception", Description: "query the audited account performance inception anchor"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/performance/inception", Request: "PerformanceInception", Response: "PerformanceInception", Description: "create or update the audited account performance inception anchor"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/cost-ledger/preview", Request: "trade_date", Response: "CostLedgerResult", Description: "calculate trusted moving-average position costs without persistence"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/performance/cost-ledger/rebuild", Request: "trade_date", Response: "CostLedgerResult", Description: "calculate and persist trusted moving-average position costs"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/series", Request: "date_from/date_to/benchmark_security_id", Response: "PerformanceSeries", Description: "daily account equity, benchmark, excess return, and drawdown series"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/series.csv", Request: "date_from/date_to/benchmark_security_id", Response: "text/csv", Description: "CSV export for account performance and benchmark series"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/economic-nav/preview", Request: "trade_date/status", Response: "EconomicNAVResult", Description: "calculate economic NAV without writing ledger"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/performance/economic-nav/rebuild", Request: "EconomicNAVRequest", Response: "EconomicNAVResult", Description: "calculate and persist current economic NAV version"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/economic-nav/reconcile", Request: "trade_date/observed_trade_date", Response: "EconomicNAVReconcileResult", Description: "preview T+1 observed open asset reconciliation"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/performance/economic-nav/reconcile", Request: "EconomicNAVRequest", Response: "EconomicNAVReconcileResult", Description: "persist T+1 observed open asset reconciliation"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/economic-nav", Request: "trade_date/date_from/date_to", Response: "[]PerformanceNAV", Description: "query current versioned economic NAV rows"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/performance/nav-reconciliations", Request: "trade_date/date_from/date_to", Response: "[]NAVReconciliation", Description: "query economic NAV reconciliation rows"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/performance/nav-reconciliations/confirm", Request: "NAVReconciliationReviewRequest", Response: "NAVReconciliationReviewResult", Description: "confirm reconciliation and finalize current economic NAV"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/performance/nav-reconciliations/block", Request: "NAVReconciliationReviewRequest", Response: "NAVReconciliationReviewResult", Description: "block reconciliation and current economic NAV"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/orders/refresh", Response: "RefreshQueryResult", Description: "refresh account orders from front gateway"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/fills/refresh", Response: "RefreshQueryResult", Description: "refresh account fills from front gateway"},
			{Method: "POST", Path: "/v1/orders", Request: "SubmitOrderRequest", Response: "Order", Description: "submit one order"},
			{Method: "POST", Path: "/v1/orders/batch", Request: "BatchSubmitOrderRequest", Response: "[]Order", Description: "submit order batch"},
			{Method: "POST", Path: "/v1/orders/{gateway_order_id}/cancel", Request: "CancelOrderRequest", Response: "Order", Description: "cancel order"},
			{Method: "GET", Path: "/v1/orders", Request: "OrderQuery", Response: "[]Order", Description: "query today's orders by default"},
			{Method: "GET", Path: "/v1/fills", Request: "FillQuery", Response: "[]Fill", Description: "query today's fills by default"},
			{Method: "GET", Path: "/v1/transfers", Request: "ComponentTransferQuery", Response: "[]ComponentTransfer", Description: "query today's ETF component transfers by default"},
			{Method: "GET", Path: "/v1/history/orders", Request: "OrderQuery", Response: "[]Order", Description: "query historical orders"},
			{Method: "GET", Path: "/v1/history/fills", Request: "FillQuery", Response: "[]Fill", Description: "query historical fills"},
			{Method: "GET", Path: "/v1/history/transfers", Request: "ComponentTransferQuery", Response: "[]ComponentTransfer", Description: "query historical ETF component transfers"},
			{Method: "GET", Path: "/v1/events/stream", Response: "OrderEvent | FillEvent | OrderCancelRejectedEvent", Description: "stream ledger change notifications"},
			{Method: "GET", Path: "/v1/meridian/metadata/instruments", Response: "Meridian instrument payload", Description: "proxy Meridian instrument metadata without redefining fields"},
			{Method: "GET", Path: "/v1/meridian/metadata/adjust-factors", Response: "Meridian adjust factor payload", Description: "proxy Meridian adjustment factors without redefining fields"},
			{Method: "GET", Path: "/v1/meridian/market/bars", Response: "Meridian market_bar.v1 payload", Description: "proxy Meridian market bars without redefining fields"},
			{Method: "GET", Path: "/v1/meridian/market/etf-components", Response: "Meridian etf_component.v1 payload", Description: "proxy Meridian ETF PCF component rows without redefining fields"},
			{Method: "GET", Path: "/v1/meridian/market/etf-cash-components", Response: "Meridian etf_cash_component.v1 payload", Description: "proxy Meridian ETF cash component and redemption unit rows without redefining fields"},
			{Method: "GET", Path: "/v1/meridian/market/etf-pcf-status", Response: "Meridian etf_pcf_status.v1 payload", Description: "proxy Meridian ETF PCF synchronization status"},
			{Method: "GET", Path: "/v1/meridian/stream/market/bars", Response: "Meridian market_bars SSE", Description: "proxy Meridian realtime minute bars without redefining fields"},
			{Method: "GET", Path: "/v1/meridian/stream/market/snapshots", Response: "Meridian market_snapshots SSE", Description: "proxy Meridian level1 snapshot SSE without redefining fields"},
			{Method: "GET", Path: "/v1/jobs/runs", Response: "[]JobRun", Description: "query latest daily job runs"},
			{Method: "POST", Path: "/v1/jobs/runs", Request: "JobRunRequest", Response: "JobRun", Description: "persist daily job run report"},
			{Method: "POST", Path: "/v1/settlements/snapshots", Request: "SettlementSnapshotRequest", Response: "SettlementSnapshotResult", Description: "persist open/close daily asset and settlement snapshots"},
			{Method: "GET", Path: "/v1/reconciliations/breaks", Request: "ReconciliationBreakQuery", Response: "[]ReconciliationBreak", Description: "query settlement reconciliation breaks"},
			{Method: "GET", Path: "/v1/schema", Response: "CatalogDocument", Description: "schema discovery"},
		},
		RedisActions: []string{
			"order.submit",
			"order.batch.submit",
			"order.cancel",
			"account.asset.query",
			"account.positions.query",
			"order.list.query",
			"fill.list.query",
		},
		TerminalOrderStatuses: []OrderStatus{
			OrderStatusFilled,
			OrderStatusCancelled,
			OrderStatusRejected,
		},
		TerminalGatewayStatuses: []GatewayStatus{
			GatewayStatusFilled,
			GatewayStatusCancelled,
			GatewayStatusRejected,
		},
		Models: []string{
			"Account",
			"Asset",
			"Position",
			"SubmitOrderRequest",
			"BatchSubmitOrderRequest",
			"CancelOrderRequest",
			"Order",
			"Fill",
			"ComponentTransfer",
			"OrderEvent",
			"FillEvent",
			"OrderQuery",
			"FillQuery",
			"ComponentTransferQuery",
			"PositionQuery",
			"RefreshQueryResult",
			"JobRun",
			"JobRunRequest",
			"DailyPerformance",
			"PerformanceSeries",
			"ContributionResult",
			"PerformanceInception",
			"CostLedgerResult",
			"EconomicNAVResult",
			"EconomicNAVReconcileResult",
			"PerformanceNAV",
			"NAVReconciliation",
			"NAVReconciliationReviewResult",
		},
	}
}
