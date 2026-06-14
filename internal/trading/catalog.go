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
			"event_type":     {"order.event", "fill.event"},
		},
		HTTPRoutes: []HTTPRouteSpec{
			{Method: "GET", Path: "/healthz", Response: "StatusView", Description: "service health check"},
			{Method: "GET", Path: "/v1/status", Response: "StatusView", Description: "service status"},
			{Method: "GET", Path: "/v1/accounts", Response: "[]Account", Description: "configured accounts"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/asset", Response: "Asset", Description: "account asset"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/asset/refresh", Response: "RefreshQueryResult", Description: "refresh account asset from front gateway"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/positions", Response: "[]Position", Description: "account positions"},
			{Method: "GET", Path: "/v1/accounts/{account_id}/positions/history", Request: "PositionQuery", Response: "[]Position", Description: "historical account position snapshots"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/positions/refresh", Response: "RefreshQueryResult", Description: "refresh account positions from front gateway"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/orders/refresh", Response: "RefreshQueryResult", Description: "refresh account orders from front gateway"},
			{Method: "POST", Path: "/v1/accounts/{account_id}/fills/refresh", Response: "RefreshQueryResult", Description: "refresh account fills from front gateway"},
			{Method: "POST", Path: "/v1/orders", Request: "SubmitOrderRequest", Response: "Order", Description: "submit one order"},
			{Method: "POST", Path: "/v1/orders/batch", Request: "BatchSubmitOrderRequest", Response: "[]Order", Description: "submit order batch"},
			{Method: "POST", Path: "/v1/orders/{gateway_order_id}/cancel", Request: "CancelOrderRequest", Response: "Order", Description: "cancel order"},
			{Method: "GET", Path: "/v1/orders", Request: "OrderQuery", Response: "[]Order", Description: "query today's orders by default"},
			{Method: "GET", Path: "/v1/fills", Request: "FillQuery", Response: "[]Fill", Description: "query today's fills by default"},
			{Method: "GET", Path: "/v1/history/orders", Request: "OrderQuery", Response: "[]Order", Description: "query historical orders"},
			{Method: "GET", Path: "/v1/history/fills", Request: "FillQuery", Response: "[]Fill", Description: "query historical fills"},
			{Method: "GET", Path: "/v1/events/stream", Response: "OrderEvent | FillEvent", Description: "stream order and fill events"},
			{Method: "GET", Path: "/v1/jobs/runs", Response: "[]JobRun", Description: "query latest daily job runs"},
			{Method: "POST", Path: "/v1/jobs/runs", Request: "JobRunRequest", Response: "JobRun", Description: "persist daily job run report"},
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
			"OrderEvent",
			"FillEvent",
			"OrderQuery",
			"FillQuery",
			"PositionQuery",
			"RefreshQueryResult",
			"JobRun",
			"JobRunRequest",
		},
	}
}
