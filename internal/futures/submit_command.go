package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// SubmitIntent is pre-send identity evidence, never an actual broker order.
type SubmitIntent struct {
	AccountID      string `json:"account_id"`
	GatewayOrderID string `json:"gateway_order_id"`
	ClientOrderID  string `json:"client_order_id"`
	TradeDate      string `json:"trade_date"`
	Symbol         string `json:"symbol"`
	Exchange       string `json:"exchange"`
	BusinessType   string `json:"business_type"`
	TradeSide      string `json:"trade_side"`
	OffsetType     string `json:"offset_type"`
	HedgeType      string `json:"hedge_type"`
	OrderQty       Count  `json:"order_qty"`
	LimitPrice     Price  `json:"limit_price"`
}

type SubmitCommand struct {
	MessageID, RequestID, CorrelationID, IdempotencyKey, Action string
	Orders                                                      []SubmitIntent
	Raw                                                         []byte
}

// DecodeSubmitCommand implements the existing single/batch fixture contract.
// It does not prove market mapping, permissions, price bands or SDK readiness.
func DecodeSubmitCommand(raw []byte, scope TradeScope) (SubmitCommand, error) {
	if err := scope.validate(); err != nil {
		return SubmitCommand{}, err
	}
	if len(raw) > maxSnapshotBytes {
		return SubmitCommand{}, ErrAuditCapacity
	}
	if _, err := parseReply(raw); err != nil {
		return SubmitCommand{}, err
	}
	obj, err := shape(raw, []string{"protocol", "message_type", "message_id", "request_id", "correlation_id", "idempotency_key", "action", "sent_at", "payload"}, nil)
	if err != nil {
		return SubmitCommand{}, err
	}
	bad := func() (SubmitCommand, error) { return SubmitCommand{}, fmt.Errorf("invalid submit command") }
	if textField(obj, "protocol") != "relay.stream.v1" || textField(obj, "message_type") != "command" || !validTime(textField(obj, "sent_at")) {
		return bad()
	}
	for _, key := range []string{"message_id", "request_id", "correlation_id", "idempotency_key"} {
		if !validID(textField(obj, key)) || len(textField(obj, key)) > 512 {
			return bad()
		}
	}
	action := textField(obj, "action")
	if !oneOf(action, "order.submit", "order.batch.submit") {
		return bad()
	}
	var rows []json.RawMessage
	if action == "order.submit" {
		rows = []json.RawMessage{obj["payload"]}
	} else {
		parent, err := shape(obj["payload"], []string{"account_id", "product_type", "schema_version", "orders"}, nil)
		if err != nil {
			return SubmitCommand{}, err
		}
		if textField(parent, "account_id") != scope.Route.AccountID || textField(parent, "product_type") != "futures" || textField(parent, "schema_version") != "ctp.f0.draft.2" {
			return bad()
		}
		if json.Unmarshal(parent["orders"], &rows) != nil {
			return bad()
		}
	}
	if len(rows) == 0 || len(rows) > 100 {
		return bad()
	}
	result := SubmitCommand{MessageID: textField(obj, "message_id"), RequestID: textField(obj, "request_id"), CorrelationID: textField(obj, "correlation_id"), IdempotencyKey: textField(obj, "idempotency_key"), Action: action, Raw: bytes.Clone(raw)}
	gateways, clients := map[string]bool{}, map[string]bool{}
	for _, rawRow := range rows {
		required := []string{"account_id", "gateway_order_id", "client_order_id", "trade_date", "symbol", "exchange", "business_type", "trade_side", "offset_type", "hedge_type", "order_qty", "limit_price"}
		optional := []string{"product_type", "schema_version"}
		if action == "order.submit" {
			required = append(required, optional...)
			optional = nil
		}
		fields, err := shape(rawRow, required, optional)
		if err != nil {
			return SubmitCommand{}, err
		}
		for key, want := range map[string]string{"product_type": "futures", "schema_version": "ctp.f0.draft.2"} {
			if _, ok := fields[key]; ok && textField(fields, key) != want {
				return bad()
			}
		}
		var row SubmitIntent
		if err := json.Unmarshal(rawRow, &row); err != nil {
			return SubmitCommand{}, err
		}
		if row.AccountID != scope.Route.AccountID || row.TradeDate != scope.TradeDate || row.Exchange != "SHFE" || !native(row.Symbol, 80, false) || row.BusinessType != "F" || !oneOf(row.TradeSide, "B", "S") || !oneOf(row.OffsetType, "O", "C", "CT", "CY") || row.HedgeType != "speculation" || row.OrderQty <= 0 || equalDecimal(string(row.LimitPrice), "0") {
			return bad()
		}
		if !validID(row.GatewayOrderID) || len(row.GatewayOrderID) > 512 || strings.HasPrefix(row.GatewayOrderID, "external-ctp-v1-") || !validID(row.ClientOrderID) || len(row.ClientOrderID) > 512 || gateways[row.GatewayOrderID] || clients[row.ClientOrderID] {
			return bad()
		}
		gateways[row.GatewayOrderID] = true
		clients[row.ClientOrderID] = true
		result.Orders = append(result.Orders, row)
	}
	return result, nil
}

func (intent SubmitIntent) matches(event TradeEvent, command SubmitCommand) bool {
	var row TradeRow
	if event.Order != nil {
		row = event.Order.TradeRow
		if event.Order.OrderQty != intent.OrderQty || !equalDecimal(string(event.Order.LimitPrice), string(intent.LimitPrice)) {
			return false
		}
	} else {
		row = event.Fill.TradeRow
		if event.Fill.Qty > intent.OrderQty {
			return false
		}
	}
	if row.AccountID != intent.AccountID || row.GatewayOrderID != intent.GatewayOrderID || row.TradeDate != intent.TradeDate || row.Symbol != intent.Symbol || row.Exchange != intent.Exchange || row.BusinessType != intent.BusinessType || row.TradeSide != intent.TradeSide || row.OffsetType != intent.OffsetType || row.HedgeType != intent.HedgeType || (row.ClientOrderID != "" && row.ClientOrderID != intent.ClientOrderID) {
		return false
	}
	return event.OriginMessageID == command.MessageID &&
		(event.RequestID == "" || event.RequestID == command.RequestID) &&
		(event.CorrelationID == "" || event.CorrelationID == command.CorrelationID) &&
		(event.IdempotencyKey == "" || event.IdempotencyKey == command.IdempotencyKey)
}
