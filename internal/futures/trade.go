package futures

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var pricePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$`)

// Price preserves decimal text. The uint64 bound applies before trimming zeros.
type Price string

func (p *Price) UnmarshalJSON(raw []byte) error {
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) > 64 || !pricePattern.MatchString(value) {
		return fmt.Errorf("invalid decimal price")
	}
	if _, err := strconv.ParseUint(strings.ReplaceAll(value, ".", ""), 10, 64); err != nil {
		return fmt.Errorf("price unscaled value exceeds uint64")
	}
	*p = Price(value)
	return nil
}

type TradeScope struct {
	Route          QueryRoute
	NativeBrokerID string
	InvestorID     string
	TradeDate      string
}

func (s TradeScope) validate() error {
	c := QueryContext{Route: s.Route, Action: "account.positions.query", OriginMessageID: "validation", CounterSessionID: "validation", NativeBrokerID: s.NativeBrokerID, TradeDate: s.TradeDate}
	if _, err := NewQueryBatch(c, 0, time.Second, DefaultBatchLimits()); err != nil {
		return err
	}
	if !validDay(s.TradeDate) || !native(s.InvestorID, 12, false) {
		return fmt.Errorf("explicit TradingDay and native investor required")
	}
	return nil
}

type NativeIDs struct {
	BrokerID     string `json:"broker_id"`
	InvestorID   string `json:"investor_id"`
	UserID       string `json:"user_id,omitempty"`
	OrderRef     string `json:"order_ref,omitempty"`
	OrderSysID   string `json:"order_sys_id,omitempty"`
	TradeID      string `json:"trade_id,omitempty"`
	FrontID      *int64 `json:"front_id,omitempty"`
	SessionID    *int64 `json:"session_id,omitempty"`
	SDKRequestID *int64 `json:"sdk_request_id,omitempty"`
}

type TradeRow struct {
	AccountID        string    `json:"account_id"`
	GatewayOrderID   string    `json:"gateway_order_id"`
	ClientOrderID    string    `json:"client_order_id,omitempty"`
	CounterSessionID string    `json:"counter_session_id"`
	TradeDate        string    `json:"trade_date"`
	Symbol           string    `json:"symbol"`
	Exchange         string    `json:"exchange"`
	BusinessType     string    `json:"business_type"`
	TradeSide        string    `json:"trade_side"`
	OffsetType       string    `json:"offset_type"`
	HedgeType        string    `json:"hedge_type"`
	CapturedAt       string    `json:"captured_at,omitempty"`
	ProductType      string    `json:"product_type,omitempty"`
	SchemaVersion    string    `json:"schema_version,omitempty"`
	NativeIDs        NativeIDs `json:"native_ids"`
}

type OrderRow struct {
	TradeRow
	OrderQty      Count   `json:"order_qty"`
	CumFilledQty  Count   `json:"cum_filled_qty"`
	LeavesQty     Count   `json:"leaves_qty"`
	LimitPrice    Price   `json:"limit_price"`
	GatewayStatus string  `json:"gateway_status"`
	SubmittedAt   *string `json:"submitted_at"`
	RejectCode    *string `json:"reject_code,omitempty"`
	RejectMessage *string `json:"reject_message,omitempty"`
}

type FillRow struct {
	TradeRow
	FillID      string  `json:"fill_id"`
	Qty         Count   `json:"qty"`
	Price       Price   `json:"price"`
	MatchedAt   *string `json:"matched_at"`
	Fee         *Money  `json:"fee"`
	FeeComplete bool    `json:"fee_complete"`
}

func nativeIdentity(kind string, s TradeScope, exchange, id string) string {
	parts := []string{s.Route.Env, s.Route.BrokerID, s.Route.AccountID, s.Route.GatewayID, s.TradeDate, exchange, id}
	var framed strings.Builder
	framed.WriteString("ctp.identity.v1/" + kind + "/")
	for _, part := range parts {
		fmt.Fprintf(&framed, "%d:%s", len([]byte(part)), part)
	}
	digest := sha256.Sum256([]byte(framed.String()))
	prefix := "ctp-fill-v1-"
	if kind == "order" {
		prefix = "external-ctp-v1-"
	}
	return prefix + hex.EncodeToString(digest[:])
}

// v2 is limited to SHFE/CFFEX by decodeTrade. Inputs have already passed native
// normalization validation; framing never trims or rewrites source identifiers.
func nativeFillIdentityV2(s TradeScope, exchange, tradeID, orderSysID, side string) string {
	parts := []string{s.Route.Env, s.Route.BrokerID, s.Route.AccountID, s.Route.GatewayID, s.TradeDate, exchange, tradeID, orderSysID, side}
	var framed strings.Builder
	framed.WriteString("ctp.identity.v2/fill/")
	for _, part := range parts {
		fmt.Fprintf(&framed, "%d:%s", len([]byte(part)), part)
	}
	digest := sha256.Sum256([]byte(framed.String()))
	return "ctp-fill-v2-" + hex.EncodeToString(digest[:])
}

// Call only after DecodeFill; legacy synthetic projection tests use opaque IDs.
func fillIdentityVersion(id string) string {
	if strings.HasPrefix(id, "ctp-fill-v2-") {
		return "v2"
	}
	return "v1"
}

func decodeTrade(raw []byte, scope TradeScope, kind string) (rawObject, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotBytes {
		return nil, fmt.Errorf("trade row byte limit")
	}
	if _, err := parseReply(raw); err != nil {
		return nil, err
	}
	required := []string{"account_id", "gateway_order_id", "counter_session_id", "trade_date", "symbol", "exchange", "business_type", "trade_side", "offset_type", "hedge_type", "native_ids"}
	optional := []string{"client_order_id", "captured_at", "product_type", "schema_version"}
	if kind == "order" {
		required = append(required, "order_qty", "cum_filled_qty", "leaves_qty", "limit_price", "gateway_status", "submitted_at")
		optional = append(optional, "reject_code", "reject_message")
	} else {
		required = append(required, "fill_id", "qty", "price", "matched_at", "fee", "fee_complete")
	}
	obj, err := shape(raw, required, optional)
	if err != nil {
		return nil, err
	}
	if textField(obj, "account_id") != scope.Route.AccountID || textField(obj, "trade_date") != scope.TradeDate ||
		!validID(textField(obj, "gateway_order_id")) || !validID(textField(obj, "counter_session_id")) ||
		!native(textField(obj, "symbol"), 80, false) || !native(textField(obj, "exchange"), 8, false) ||
		textField(obj, "business_type") != "F" || !oneOf(textField(obj, "trade_side"), "B", "S") ||
		!oneOf(textField(obj, "offset_type"), "O", "C", "CT", "CY") || textField(obj, "hedge_type") != "speculation" {
		return nil, fmt.Errorf("invalid trade scope, economics or enum")
	}
	for _, k := range []string{"client_order_id", "captured_at", "product_type", "schema_version"} {
		if _, ok := obj[k]; !ok {
			continue
		}
		value := textField(obj, k)
		if (k == "client_order_id" && !validID(value)) || (k == "captured_at" && !validTime(value)) ||
			(k == "product_type" && value != "futures") || (k == "schema_version" && value != "ctp.f0.draft.2") {
			return nil, fmt.Errorf("invalid optional %s", k)
		}
	}
	ids, err := shape(obj["native_ids"], []string{"broker_id", "investor_id"}, []string{"user_id", "order_ref", "order_sys_id", "trade_id", "front_id", "session_id", "sdk_request_id"})
	if err != nil {
		return nil, err
	}
	if textField(ids, "broker_id") != scope.NativeBrokerID || textField(ids, "investor_id") != scope.InvestorID {
		return nil, fmt.Errorf("native investor authorization mismatch")
	}
	for _, field := range []struct {
		key   string
		size  int
		empty bool
	}{{"user_id", 15, false}, {"order_ref", 12, true}, {"order_sys_id", 20, true}, {"trade_id", 20, false}} {
		if value, ok := ids[field.key]; ok {
			var text string
			if literal(value, "null") || json.Unmarshal(value, &text) != nil || !native(text, field.size, field.empty) {
				return nil, fmt.Errorf("invalid native %s", field.key)
			}
		}
	}
	for _, key := range []string{"front_id", "session_id", "sdk_request_id"} {
		if value, ok := ids[key]; ok {
			if _, err := nullableInteger(value); err != nil {
				return nil, err
			}
		}
	}
	if strings.HasPrefix(textField(obj, "gateway_order_id"), "external-ctp-v1-") {
		if textField(ids, "order_sys_id") == "" || textField(obj, "gateway_order_id") != nativeIdentity("order", scope, textField(obj, "exchange"), textField(ids, "order_sys_id")) {
			return nil, fmt.Errorf("external order identity mismatch")
		}
	}
	timeKey := "submitted_at"
	if kind == "fill" {
		timeKey = "matched_at"
		if _, ok := ids["sdk_request_id"]; ok {
			return nil, fmt.Errorf("fill has no native SDK request ID")
		}
		fillID := textField(obj, "fill_id")
		expectedID := nativeIdentity("fill", scope, textField(obj, "exchange"), textField(ids, "trade_id"))
		if fillIdentityVersion(fillID) == "v2" {
			if !oneOf(textField(obj, "exchange"), "SHFE", "CFFEX") {
				return nil, fmt.Errorf("fill identity v2 requires SHFE or CFFEX")
			}
			expectedID = nativeFillIdentityV2(scope, textField(obj, "exchange"), textField(ids, "trade_id"), textField(ids, "order_sys_id"), textField(obj, "trade_side"))
		}
		if !native(textField(ids, "order_sys_id"), 20, false) || !native(textField(ids, "trade_id"), 20, false) || fillID != expectedID {
			return nil, fmt.Errorf("fill native identity mismatch")
		}
		if !literal(obj["fee_complete"], "true") && !literal(obj["fee_complete"], "false") {
			return nil, fmt.Errorf("explicit fee_complete boolean required")
		}
	}
	if !literal(obj[timeKey], "null") && !validTime(textField(obj, timeKey)) {
		return nil, fmt.Errorf("invalid source time")
	}
	return obj, nil
}

func DecodeOrder(raw []byte, scope TradeScope) (OrderRow, error) {
	if _, err := decodeTrade(raw, scope, "order"); err != nil {
		return OrderRow{}, err
	}
	var row OrderRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return OrderRow{}, err
	}
	if row.OrderQty <= 0 || int64(row.CumFilledQty)+int64(row.LeavesQty) > int64(row.OrderQty) ||
		!oneOf(row.GatewayStatus, "accepted", "working", "filled", "cancelled", "rejected") ||
		(oneOf(row.GatewayStatus, "accepted", "working") && int64(row.CumFilledQty)+int64(row.LeavesQty) != int64(row.OrderQty)) ||
		(row.GatewayStatus == "filled" && (row.CumFilledQty != row.OrderQty || row.LeavesQty != 0)) {
		return OrderRow{}, fmt.Errorf("invalid order quantities or state")
	}
	return row, nil
}

func DecodeFill(raw []byte, scope TradeScope) (FillRow, error) {
	if _, err := decodeTrade(raw, scope, "fill"); err != nil {
		return FillRow{}, err
	}
	var row FillRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return FillRow{}, err
	}
	if row.Qty <= 0 || equalDecimal(string(row.Price), "0") || (row.FeeComplete && row.Fee == nil) {
		return FillRow{}, fmt.Errorf("invalid fill quantity, price or fee evidence")
	}
	return row, nil
}

func equalDecimal(a, b string) bool {
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		return false
	}
	y, ok := new(big.Rat).SetString(b)
	return ok && x.Cmp(y) == 0
}

func equalInstant(a, b string) bool {
	x, err := time.Parse(time.RFC3339Nano, a)
	if err != nil {
		return false
	}
	y, err := time.Parse(time.RFC3339Nano, b)
	return err == nil && x.Equal(y)
}
