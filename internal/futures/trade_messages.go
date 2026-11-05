package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func DecodeOrders(raw []byte, scope TradeScope) ([]OrderRow, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	items, err := rows(raw, SnapshotScope{AccountID: scope.Route.AccountID, TradeDate: scope.TradeDate, NativeBrokerID: scope.NativeBrokerID})
	if err != nil {
		return nil, err
	}
	out := make([]OrderRow, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		row, err := DecodeOrder(item, scope)
		if err != nil {
			return nil, err
		}
		if seen[row.GatewayOrderID] {
			return nil, fmt.Errorf("duplicate order identity in complete query")
		}
		seen[row.GatewayOrderID] = true
		out = append(out, row)
	}
	return out, nil
}

func DecodeFills(raw []byte, scope TradeScope) ([]FillRow, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	items, err := rows(raw, SnapshotScope{AccountID: scope.Route.AccountID, TradeDate: scope.TradeDate, NativeBrokerID: scope.NativeBrokerID})
	if err != nil {
		return nil, err
	}
	out := make([]FillRow, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		row, err := DecodeFill(item, scope)
		if err != nil {
			return nil, err
		}
		if seen[row.FillID] {
			return nil, fmt.Errorf("duplicate execution identity in complete query")
		}
		seen[row.FillID] = true
		out = append(out, row)
	}
	return out, nil
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneTrade(row TradeRow) TradeRow {
	row.NativeIDs.FrontID = clonePtr(row.NativeIDs.FrontID)
	row.NativeIDs.SessionID = clonePtr(row.NativeIDs.SessionID)
	row.NativeIDs.SDKRequestID = clonePtr(row.NativeIDs.SDKRequestID)
	return row
}
func cloneOrder(row OrderRow) OrderRow {
	row.TradeRow = cloneTrade(row.TradeRow)
	row.SubmittedAt = clonePtr(row.SubmittedAt)
	row.RejectCode = clonePtr(row.RejectCode)
	row.RejectMessage = clonePtr(row.RejectMessage)
	return row
}
func cloneFill(row FillRow) FillRow {
	row.TradeRow = cloneTrade(row.TradeRow)
	row.MatchedAt = clonePtr(row.MatchedAt)
	row.Fee = clonePtr(row.Fee)
	return row
}

// EventContext comes from caller-verified routing/native authorization. When a
// known local command exists, ExpectedOrigin must be provided. nil does not prove
// external ownership or authorize skipping the future command registry.
type EventContext struct {
	Scope          TradeScope
	ExpectedOrigin *string
}

type TradeEvent struct {
	MessageID       string
	OriginMessageID string
	ProducedAt      string
	Order           *OrderRow
	Fill            *FillRow
	Raw             []byte
	RawPayload      []byte
}

// DecodeTradeEvent validates the existing order/fill event fixture shape. It is
// not a Redis dispatcher, heartbeat decoder or command receipt handler. Unknown
// fields fail closed except adapter_context audit extensions. Audit raw first.
func DecodeTradeEvent(raw []byte, expected EventContext) (TradeEvent, error) {
	bad := func(reason string) (TradeEvent, error) {
		return TradeEvent{}, fmt.Errorf("invalid trade event: %s", reason)
	}
	if err := expected.Scope.validate(); err != nil {
		return TradeEvent{}, err
	}
	if expected.ExpectedOrigin != nil && !validID(*expected.ExpectedOrigin) {
		return bad("expected origin")
	}
	if len(raw) > maxSnapshotBytes {
		return bad("event byte limit")
	}
	if _, err := parseReply(raw); err != nil {
		return TradeEvent{}, err
	}
	obj, err := shape(raw, []string{"protocol", "message_type", "message_id", "event_type", "produced_at", "routing", "payload"}, []string{"origin_message_id", "request_id", "correlation_id", "idempotency_key", "adapter_context"})
	if err != nil {
		return TradeEvent{}, err
	}
	if textField(obj, "protocol") != "relay.stream.v1" || textField(obj, "message_type") != "event" || !validTime(textField(obj, "produced_at")) {
		return bad("protocol/type/time")
	}
	for _, key := range []string{"message_id", "origin_message_id", "request_id", "correlation_id", "idempotency_key"} {
		if _, ok := obj[key]; ok && !validID(textField(obj, key)) {
			return bad("identifier " + key)
		}
	}
	route, err := shape(obj["routing"], []string{"env", "broker_id", "gateway_id", "account_id"}, nil)
	if err != nil {
		return TradeEvent{}, err
	}
	r := expected.Scope.Route
	if textField(route, "env") != r.Env || textField(route, "broker_id") != r.BrokerID || textField(route, "gateway_id") != r.GatewayID || textField(route, "account_id") != r.AccountID {
		return bad("routing differs from trusted source")
	}
	if expected.ExpectedOrigin != nil && textField(obj, "origin_message_id") != *expected.ExpectedOrigin {
		return bad("local command origin mismatch")
	}
	if audit, ok := obj["adapter_context"]; ok {
		var fields rawObject
		if json.Unmarshal(audit, &fields) != nil || fields == nil {
			return bad("adapter context must be object")
		}
	}
	var payload rawObject
	if json.Unmarshal(obj["payload"], &payload) != nil || textField(payload, "product_type") != "futures" || textField(payload, "schema_version") != "ctp.f0.draft.2" {
		return bad("payload product/schema")
	}
	event := TradeEvent{MessageID: textField(obj, "message_id"), OriginMessageID: textField(obj, "origin_message_id"), ProducedAt: textField(obj, "produced_at"), Raw: bytes.Clone(raw), RawPayload: bytes.Clone(obj["payload"])}
	switch textField(obj, "event_type") {
	case "order.event":
		row, err := DecodeOrder(obj["payload"], expected.Scope)
		if err != nil {
			return TradeEvent{}, err
		}
		event.Order = &row
	case "fill.event":
		row, err := DecodeFill(obj["payload"], expected.Scope)
		if err != nil {
			return TradeEvent{}, err
		}
		event.Fill = &row
	default:
		return bad("unsupported event type")
	}
	return event, nil
}
