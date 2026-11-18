package futures

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type CancelRejectionContext struct {
	Scope                CommandScope
	CommandRaw           []byte
	CounterSessionID     string
	ConnectionGeneration int64
}

// CancelRejection is an attempt outcome, never an OrderRow or permission to retry.
// This decoder alone does not project, acknowledge or settle a command.
type CancelRejection struct {
	MessageID, OriginMessageID, GatewayOrderID, CancelID   string
	TradeDate, CounterSessionID, Code, Message, OccurredAt string
	OrderStateChanged, RetrySafe, ReconciliationRequired   bool
	NativeErrorID                                          int64
	NativeErrorHex                                         string
	Raw, RawPayload                                        []byte
}

func DecodeCancelRejection(raw []byte, expected CancelRejectionContext) (CancelRejection, error) {
	bad := func() (CancelRejection, error) {
		return CancelRejection{}, fmt.Errorf("invalid futures cancel rejection")
	}
	command, err := DecodeCommand(expected.CommandRaw, expected.Scope, "cmd.trade")
	if err != nil || command.Action != "order.cancel" || command.Cancel == nil ||
		!validID(expected.CounterSessionID) || expected.ConnectionGeneration < 1 || len(raw) > maxSnapshotBytes {
		return bad()
	}
	if _, err := parseReply(raw); err != nil {
		return bad()
	}
	o, err := shape(raw, []string{"protocol", "message_type", "message_id", "action", "event_type", "event_name", "origin_message_id", "request_id", "correlation_id", "idempotency_key", "produced_at", "routing", "payload", "adapter_context"}, nil)
	if err != nil || textField(o, "protocol") != "relay.stream.v1" || textField(o, "message_type") != "event" ||
		textField(o, "action") != "order.cancel" || textField(o, "event_type") != "order.cancel.event" || textField(o, "event_name") != "order.cancel.rejected" ||
		!validTime(textField(o, "produced_at")) || !validID(textField(o, "message_id")) || len(textField(o, "message_id")) > 512 {
		return bad()
	}
	for key, value := range map[string]string{"origin_message_id": command.MessageID, "request_id": command.RequestID, "correlation_id": command.CorrelationID, "idempotency_key": command.IdempotencyKey} {
		if textField(o, key) != value {
			return bad()
		}
	}
	r, err := shape(o["routing"], []string{"env", "broker_id", "gateway_id", "account_id"}, nil)
	if err != nil || (QueryRoute{textField(r, "env"), textField(r, "broker_id"), textField(r, "gateway_id"), textField(r, "account_id")}) != expected.Scope.Route {
		return bad()
	}
	p, err := shape(o["payload"], []string{"account_id", "product_type", "schema_version", "gateway_order_id", "cancel_id", "counter_session_id", "trade_date", "cancel_status", "code", "message", "occurred_at", "order_state_changed", "retry_safe", "reconciliation_required"}, nil)
	if err != nil || textField(p, "account_id") != expected.Scope.Route.AccountID || textField(p, "product_type") != "futures" || textField(p, "schema_version") != "ctp.f0.draft.2" ||
		textField(p, "gateway_order_id") != command.Cancel.GatewayOrderID || textField(p, "cancel_id") != command.Cancel.CancelID || textField(p, "trade_date") != command.Cancel.TradeDate ||
		textField(p, "counter_session_id") != expected.CounterSessionID || textField(p, "cancel_status") != "rejected" || textField(p, "code") != "BROKER_CANCEL_REJECTED" ||
		!validTime(textField(p, "occurred_at")) || !literal(p["order_state_changed"], "false") || !literal(p["retry_safe"], "false") || !literal(p["reconciliation_required"], "true") {
		return bad()
	}
	var audit rawObject
	if json.Unmarshal(o["adapter_context"], &audit) != nil || audit == nil || !literal(audit["native_action_rejected"], "true") || textField(audit, "projection_source") != "order_action_error" {
		return bad()
	}
	generation, err := nullableInteger(audit["connection_generation"])
	if err != nil || generation == nil || *generation != expected.ConnectionGeneration {
		return bad()
	}
	sdk, err := nullableInteger(audit["sdk_request_id"])
	if err != nil || sdk == nil || *sdk < 1 {
		return bad()
	}
	native, err := shape(audit["native_error"], []string{"ErrorID", "ErrorMsg_hex"}, nil)
	if err != nil {
		return bad()
	}
	errorID, err := nullableInteger(native["ErrorID"])
	if err != nil || errorID == nil || *errorID == 0 || textField(p, "message") == "" {
		return bad()
	}
	errorHex := textField(native, "ErrorMsg_hex")
	if decoded, err := hex.DecodeString(errorHex); err != nil || len(decoded) == 0 || len(decoded) > 256 {
		return bad()
	}
	return CancelRejection{MessageID: textField(o, "message_id"), OriginMessageID: command.MessageID,
		GatewayOrderID: command.Cancel.GatewayOrderID, CancelID: command.Cancel.CancelID, TradeDate: command.Cancel.TradeDate,
		CounterSessionID: expected.CounterSessionID, Code: textField(p, "code"), Message: textField(p, "message"), OccurredAt: textField(p, "occurred_at"),
		ReconciliationRequired: true, NativeErrorID: *errorID, NativeErrorHex: errorHex, Raw: bytes.Clone(raw), RawPayload: bytes.Clone(o["payload"])}, nil
}
