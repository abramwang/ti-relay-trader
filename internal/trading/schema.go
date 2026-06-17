package trading

import "time"

type Exchange string

const (
	ExchangeSH Exchange = "SH"
	ExchangeSZ Exchange = "SZ"
	ExchangeBJ Exchange = "BJ"
)

type TradeSide string

const (
	TradeSideBuy        TradeSide = "B"
	TradeSideSell       TradeSide = "S"
	TradeSidePurchase   TradeSide = "P"
	TradeSideRedemption TradeSide = "R"
)

type BusinessType string

const (
	BusinessTypeStock BusinessType = "S"
	BusinessTypeETF   BusinessType = "E"
)

type OffsetType string

const (
	OffsetTypeOpen  OffsetType = "O"
	OffsetTypeClose OffsetType = "C"
)

type AccountStatus string

const (
	AccountStatusEnabled  AccountStatus = "enabled"
	AccountStatusDisabled AccountStatus = "disabled"
	AccountStatusReadonly AccountStatus = "readonly"
)

type OrderStatus string

const (
	OrderStatusCreated         OrderStatus = "created"
	OrderStatusAccepted        OrderStatus = "accepted"
	OrderStatusWorking         OrderStatus = "working"
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusCancelled       OrderStatus = "cancelled"
	OrderStatusRejected        OrderStatus = "rejected"
)

type GatewayStatus string

const (
	GatewayStatusAccepted  GatewayStatus = "accepted"
	GatewayStatusWorking   GatewayStatus = "working"
	GatewayStatusFilled    GatewayStatus = "filled"
	GatewayStatusCancelled GatewayStatus = "cancelled"
	GatewayStatusRejected  GatewayStatus = "rejected"
)

type ReplyStatus string

const (
	ReplyStatusAccepted  ReplyStatus = "accepted"
	ReplyStatusPartial   ReplyStatus = "partial"
	ReplyStatusCompleted ReplyStatus = "completed"
	ReplyStatusRejected  ReplyStatus = "rejected"
	ReplyStatusFailed    ReplyStatus = "failed"
)

type EventType string

const (
	EventTypeOrder EventType = "order.event"
	EventTypeFill  EventType = "fill.event"
)

type ErrorCode string

const (
	ErrorOK                         ErrorCode = "OK"
	ErrorBadCommandBody             ErrorCode = "BAD_COMMAND_BODY"
	ErrorMissingMessageID           ErrorCode = "MISSING_MESSAGE_ID"
	ErrorUnknownAction              ErrorCode = "UNKNOWN_ACTION"
	ErrorInvalidArgument            ErrorCode = "INVALID_ARGUMENT"
	ErrorInvalidBatch               ErrorCode = "INVALID_BATCH"
	ErrorBatchRejected              ErrorCode = "BATCH_REJECTED"
	ErrorOrderSubmitRejected        ErrorCode = "ORDER_SUBMIT_REJECTED"
	ErrorOrderNotFound              ErrorCode = "ORDER_NOT_FOUND"
	ErrorOrderTerminalNotCancelable ErrorCode = "ORDER_TERMINAL_NOT_CANCELABLE"
	ErrorOrderNotReadyForCancel     ErrorCode = "ORDER_NOT_READY_FOR_CANCEL"
	ErrorCancelSubmitFailed         ErrorCode = "CANCEL_SUBMIT_FAILED"
	ErrorQuerySubmitFailed          ErrorCode = "QUERY_SUBMIT_FAILED"
	ErrorQueryFailed                ErrorCode = "QUERY_FAILED"
	ErrorIdempotencyConflict        ErrorCode = "IDEMPOTENCY_CONFLICT"
	ErrorBrokerNotReady             ErrorCode = "BROKER_NOT_READY"
	ErrorBrokerRejected             ErrorCode = "BROKER_REJECTED"
)

type Account struct {
	AccountID      string            `json:"account_id"`
	BrokerID       string            `json:"broker_id"`
	GatewayID      string            `json:"gateway_id"`
	StreamPrefix   string            `json:"stream_prefix,omitempty"`
	Status         AccountStatus     `json:"status"`
	Enabled        bool              `json:"enabled"`
	TradingEnabled bool              `json:"trading_enabled"`
	Simulated      bool              `json:"simulated"`
	Tags           map[string]string `json:"tags,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at,omitempty"`
}

type Asset struct {
	AccountID      string    `json:"account_id"`
	CashAvailable  float64   `json:"cash_available"`
	CashTotal      float64   `json:"cash_total"`
	NetAsset       float64   `json:"net_asset"`
	MarketValue    float64   `json:"market_value"`
	StockValue     float64   `json:"stock_value,omitempty"`
	FundValue      float64   `json:"fund_value,omitempty"`
	Commission     float64   `json:"commission,omitempty"`
	DayProfit      float64   `json:"day_profit,omitempty"`
	PositionProfit float64   `json:"position_profit,omitempty"`
	CloseProfit    float64   `json:"close_profit,omitempty"`
	Credit         float64   `json:"credit,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

type Position struct {
	AccountID        string    `json:"account_id"`
	TradeDate        string    `json:"trade_date,omitempty"`
	Symbol           string    `json:"symbol"`
	Name             string    `json:"name,omitempty"`
	Exchange         Exchange  `json:"exchange"`
	Quantity         int64     `json:"quantity"`
	SellableQty      int64     `json:"sellable_qty"`
	InitialQty       int64     `json:"initial_qty,omitempty"`
	TodayQty         int64     `json:"today_qty,omitempty"`
	AvgCost          float64   `json:"avg_cost"`
	LastPrice        float64   `json:"last_price,omitempty"`
	MarketValue      float64   `json:"market_value"`
	UnrealizedPnL    float64   `json:"unrealized_pnl,omitempty"`
	DayUnrealizedPnL float64   `json:"day_unrealized_pnl,omitempty"`
	SettledProfit    float64   `json:"settled_profit,omitempty"`
	ShareholderID    string    `json:"shareholder_id,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type SubmitOrderRequest struct {
	AccountID      string       `json:"account_id"`
	ClientOrderID  string       `json:"client_order_id,omitempty"`
	GatewayOrderID string       `json:"gateway_order_id,omitempty"`
	Symbol         string       `json:"symbol"`
	Exchange       Exchange     `json:"exchange"`
	TradeSide      TradeSide    `json:"trade_side"`
	BusinessType   BusinessType `json:"business_type"`
	OffsetType     OffsetType   `json:"offset_type,omitempty"`
	Price          float64      `json:"price"`
	Qty            int64        `json:"qty"`
	IdempotencyKey string       `json:"idempotency_key,omitempty"`
}

type BatchSubmitOrderRequest struct {
	AccountID      string               `json:"account_id"`
	Orders         []SubmitOrderRequest `json:"orders"`
	IdempotencyKey string               `json:"idempotency_key,omitempty"`
}

type CancelOrderRequest struct {
	AccountID      string `json:"account_id"`
	GatewayOrderID string `json:"gateway_order_id"`
	CancelID       string `json:"cancel_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type Order struct {
	AccountID         string         `json:"account_id"`
	ClientOrderID     string         `json:"client_order_id,omitempty"`
	GatewayOrderID    string         `json:"gateway_order_id"`
	OrderID           int64          `json:"order_id,omitempty"`
	OrderStreamID     string         `json:"order_stream_id,omitempty"`
	Symbol            string         `json:"symbol"`
	Name              string         `json:"name,omitempty"`
	Exchange          Exchange       `json:"exchange"`
	TradeSide         TradeSide      `json:"trade_side"`
	BusinessType      BusinessType   `json:"business_type"`
	OffsetType        OffsetType     `json:"offset_type,omitempty"`
	LimitPrice        float64        `json:"limit_price"`
	OrderQty          int64          `json:"order_qty"`
	SubmittedQty      int64          `json:"submitted_qty,omitempty"`
	CumFilledQty      int64          `json:"cum_filled_qty"`
	LeavesQty         int64          `json:"leaves_qty"`
	CancelledQty      int64          `json:"cancelled_qty,omitempty"`
	InvalidQty        int64          `json:"invalid_qty,omitempty"`
	AvgFillPrice      float64        `json:"avg_fill_price,omitempty"`
	Fee               float64        `json:"fee,omitempty"`
	Status            OrderStatus    `json:"status"`
	GatewayStatus     GatewayStatus  `json:"gateway_status"`
	AdapterStatusCode int            `json:"adapter_status_code,omitempty"`
	AdapterStatusName string         `json:"adapter_status_name,omitempty"`
	IsTerminal        bool           `json:"is_terminal"`
	RejectCode        ErrorCode      `json:"reject_code,omitempty"`
	RejectMessage     string         `json:"reject_message,omitempty"`
	OriginMessageID   string         `json:"origin_message_id,omitempty"`
	RequestID         string         `json:"request_id,omitempty"`
	IdempotencyKey    string         `json:"idempotency_key,omitempty"`
	ShareholderID     string         `json:"shareholder_id,omitempty"`
	CreatedAt         time.Time      `json:"created_at,omitempty"`
	AcceptedAt        time.Time      `json:"accepted_at,omitempty"`
	InsertedAt        time.Time      `json:"inserted_at,omitempty"`
	LastUpdatedAt     time.Time      `json:"last_updated_at,omitempty"`
	TerminalAt        time.Time      `json:"terminal_at,omitempty"`
	AdapterContext    map[string]any `json:"adapter_context,omitempty"`
}

type Fill struct {
	FillID         string         `json:"fill_id"`
	AccountID      string         `json:"account_id"`
	GatewayOrderID string         `json:"gateway_order_id"`
	OrderID        int64          `json:"order_id,omitempty"`
	OrderStreamID  string         `json:"order_stream_id,omitempty"`
	Symbol         string         `json:"symbol"`
	Name           string         `json:"name,omitempty"`
	Exchange       Exchange       `json:"exchange"`
	TradeSide      TradeSide      `json:"trade_side"`
	Price          float64        `json:"price"`
	Qty            int64          `json:"qty"`
	Fee            float64        `json:"fee,omitempty"`
	TradeDate      string         `json:"trade_date,omitempty"`
	MatchTimestamp int64          `json:"match_timestamp,omitempty"`
	MatchedAt      time.Time      `json:"matched_at,omitempty"`
	ShareholderID  string         `json:"shareholder_id,omitempty"`
	AdapterContext map[string]any `json:"adapter_context,omitempty"`
}

type OrderEvent struct {
	EventID        string         `json:"event_id"`
	EventType      EventType      `json:"event_type"`
	AccountID      string         `json:"account_id"`
	GatewayOrderID string         `json:"gateway_order_id"`
	Status         OrderStatus    `json:"status"`
	GatewayStatus  GatewayStatus  `json:"gateway_status"`
	IsTerminal     bool           `json:"is_terminal"`
	Order          Order          `json:"order"`
	ProducedAt     time.Time      `json:"produced_at,omitempty"`
	AdapterContext map[string]any `json:"adapter_context,omitempty"`
}

type FillEvent struct {
	EventID        string         `json:"event_id"`
	EventType      EventType      `json:"event_type"`
	AccountID      string         `json:"account_id"`
	GatewayOrderID string         `json:"gateway_order_id"`
	Fill           Fill           `json:"fill"`
	ProducedAt     time.Time      `json:"produced_at,omitempty"`
	AdapterContext map[string]any `json:"adapter_context,omitempty"`
}

type OrderQuery struct {
	AccountID      string      `json:"account_id,omitempty"`
	GatewayOrderID string      `json:"gateway_order_id,omitempty"`
	ClientOrderID  string      `json:"client_order_id,omitempty"`
	Symbol         string      `json:"symbol,omitempty"`
	Exchange       Exchange    `json:"exchange,omitempty"`
	Status         OrderStatus `json:"status,omitempty"`
	TradeDate      string      `json:"trade_date,omitempty"`
	DateFrom       string      `json:"date_from,omitempty"`
	DateTo         string      `json:"date_to,omitempty"`
	History        bool        `json:"history,omitempty"`
	Limit          int         `json:"limit,omitempty"`
	Cursor         string      `json:"cursor,omitempty"`
}

type FillQuery struct {
	AccountID      string   `json:"account_id,omitempty"`
	GatewayOrderID string   `json:"gateway_order_id,omitempty"`
	Symbol         string   `json:"symbol,omitempty"`
	Exchange       Exchange `json:"exchange,omitempty"`
	TradeDate      string   `json:"trade_date,omitempty"`
	DateFrom       string   `json:"date_from,omitempty"`
	DateTo         string   `json:"date_to,omitempty"`
	History        bool     `json:"history,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
}

type PositionQuery struct {
	AccountID string   `json:"account_id"`
	Symbol    string   `json:"symbol,omitempty"`
	Exchange  Exchange `json:"exchange,omitempty"`
	TradeDate string   `json:"trade_date,omitempty"`
	DateFrom  string   `json:"date_from,omitempty"`
	DateTo    string   `json:"date_to,omitempty"`
	History   bool     `json:"history,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Cursor    string   `json:"cursor,omitempty"`
}
