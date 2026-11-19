package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// ReplyResult is a command-correlated OC observation, not independent SDK
// evidence, broker order state, resend permission or a current readiness flag.
type ReplyResult struct {
	CommandID           int64              `json:"command_id"`
	Action              string             `json:"action"`
	State               string             `json:"state"`
	Code                string             `json:"code,omitempty"`
	Message             string             `json:"message,omitempty"`
	ProducedAt          string             `json:"produced_at,omitempty"`
	ResponseValidated   bool               `json:"response_validated"`
	NativeAuditVerified bool               `json:"native_audit_verified"`
	OrderStateChanged   bool               `json:"order_state_changed"`
	RetrySafe           bool               `json:"retry_safe"`
	Snapshot            *SnapshotCandidate `json:"snapshot,omitempty"`
}

func replyEnvelope(raw []byte, command CommandRegistration) (rawObject, error) {
	o, err := parseReply(raw)
	if err != nil {
		return nil, err
	}
	var route QueryRoute
	c := command.Original
	if json.Unmarshal(o["routing"], &route) != nil || route != command.Scope.Route ||
		textField(o, "protocol") != "relay.stream.v1" || textField(o, "message_type") != "reply" ||
		textField(o, "action") != c.Action || textField(o, "origin_message_id") != c.MessageID ||
		textField(o, "request_id") != c.RequestID || textField(o, "correlation_id") != c.CorrelationID ||
		textField(o, "idempotency_key") != c.IdempotencyKey || !validID(textField(o, "message_id")) ||
		!validID(textField(o, "code")) || !validTime(textField(o, "produced_at")) {
		return nil, ErrBatchIdentity
	}
	return o, nil
}

// Session/day/native identity come from the persisted publication. The reported
// ticket/SDK ID only enforce consistency between pages; they are not attested.
func reportedQueryContext(c CommandRegistration, target TestQueryTarget, raw []byte) (QueryContext, error) {
	b := queryBinding(c)
	q := QueryContext{Route: c.Scope.Route, Action: c.Original.Action, OriginMessageID: c.Original.MessageID,
		CounterSessionID: target.CounterSessionID, ConnectionGeneration: &target.ConnectionGeneration, TradeDate: target.TradeDate,
		NativeBrokerID: c.Scope.NativeBrokerID, NativeInvestorID: c.Scope.InvestorID, NativeUserID: c.Scope.UserID,
		Command: &b, Constraints: c.Original.Constraints}
	o, err := replyEnvelope(raw, c)
	if err != nil {
		return q, err
	}
	if c.Original.Action == "instrument.constraints.query" {
		return q, nil
	}
	var adapter, evidence rawObject
	if json.Unmarshal(o["adapter_context"], &adapter) != nil || json.Unmarshal(adapter["query_evidence"], &evidence) != nil {
		return q, ErrBatchIdentity
	}
	q.Ticket = textField(evidence, "ticket")
	q.SDKRequestID, err = nullableInteger(evidence["sdk_request_id"])
	return q, err
}

func assembleReplyResult(c CommandRegistration, target TestQueryTarget, pages [][]byte) ReplyResult {
	out := ReplyResult{CommandID: c.CommandID, Action: c.Original.Action, State: "collecting"}
	invalid := func(err error) ReplyResult {
		out.State, out.Code, out.Message = "invalid", "REPLY_INVALID", err.Error()
		out.ResponseValidated = false
		out.Snapshot = nil
		return out
	}
	if len(pages) == 0 {
		return out
	}
	if queryResultType(c.Original.Action) != "" {
		q, err := reportedQueryContext(c, target, pages[0])
		if err != nil {
			return invalid(err)
		}
		// Reassembly of durable responses is not a new live receive window.
		// Incomplete pages stay collecting across restarts, never become empty.
		batch, err := NewQueryBatch(q, 0, time.Second, DefaultBatchLimits())
		if err != nil {
			return invalid(err)
		}
		for _, raw := range pages {
			o, err := replyEnvelope(raw, c)
			if err != nil {
				return invalid(err)
			}
			if _, err = batch.Accept(raw, 0); err != nil {
				return invalid(err)
			}
			out.Code, out.Message = textField(o, "code"), textField(o, "message")
			out.ProducedAt = textField(o, "produced_at")
		}
		state, _ := batch.State()
		out.State = string(state)
		out.ResponseValidated = true
		if state == BatchReady {
			snapshot, err := batch.Candidate()
			if err != nil {
				return invalid(err)
			}
			out.Snapshot = &snapshot
		}
		return out
	}
	var first []byte
	for _, raw := range pages {
		o, err := decodeTradeReceipt(raw, c, target)
		if err != nil {
			return invalid(err)
		}
		if first != nil && !bytes.Equal(first, raw) {
			return invalid(fmt.Errorf("conflicting trade command receipts"))
		}
		first = raw
		out.State = textField(o, "status")
		if out.State == "rejected" {
			out.State = "failed"
		}
		out.Code, out.Message, out.ProducedAt = textField(o, "code"), textField(o, "message"), textField(o, "produced_at")
		out.ResponseValidated = true
	}
	return out
}

func decodeTradeReceipt(raw []byte, c CommandRegistration, t TestQueryTarget) (rawObject, error) {
	o, err := replyEnvelope(raw, c)
	if err != nil {
		return nil, err
	}
	if !oneOf(c.Original.Action, "order.submit", "order.cancel") {
		return nil, fmt.Errorf("unsupported trade receipt action")
	}
	if _, err = shape(raw, []string{"protocol", "message_type", "message_id", "origin_message_id", "request_id", "correlation_id", "idempotency_key", "action", "routing", "status", "code", "produced_at", "payload", "adapter_context"}, []string{"result_type", "message"}); err != nil {
		return nil, err
	}
	var p, a rawObject
	if json.Unmarshal(o["payload"], &p) != nil || json.Unmarshal(o["adapter_context"], &a) != nil {
		return nil, ErrBatchIdentity
	}
	generation, err := nullableInteger(a["connection_generation"])
	if err != nil || generation == nil || *generation != t.ConnectionGeneration ||
		textField(p, "account_id") != c.Scope.Route.AccountID || textField(p, "product_type") != "futures" ||
		textField(p, "schema_version") != "ctp.f0.draft.2" || textField(p, "counter_session_id") != t.CounterSessionID || textField(p, "trade_date") != t.TradeDate {
		return nil, ErrBatchIdentity
	}
	if c.Original.Action == "order.submit" {
		if len(c.Original.Orders) != 1 || textField(p, "gateway_order_id") != c.Original.Orders[0].GatewayOrderID {
			return nil, ErrBatchIdentity
		}
		if value, ok := p["client_order_id"]; ok && (literal(value, "null") || textField(p, "client_order_id") != c.Original.Orders[0].ClientOrderID) {
			return nil, ErrBatchIdentity
		}
	} else {
		if c.Original.Cancel == nil || textField(p, "gateway_order_id") != c.Original.Cancel.GatewayOrderID {
			return nil, ErrBatchIdentity
		}
		if _, ok := p["cancel_id"]; ok && textField(p, "cancel_id") != c.Original.Cancel.CancelID {
			return nil, ErrBatchIdentity
		}
	}
	request, e1 := nullableInteger(a["sdk_request_id"])
	code, e2 := nullableInteger(a["sdk_return_code"])
	if e1 != nil || e2 != nil || (request != nil && *request <= 0) {
		return nil, ErrBatchIdentity
	}
	status := textField(o, "status")
	if status == "accepted" {
		if textField(o, "result_type") != "order_action_receipt" || textField(o, "code") != "OK" || request == nil || code == nil || *code != 0 {
			return nil, ErrBatchIdentity
		}
	} else if oneOf(status, "failed", "rejected") {
		if textField(o, "code") == "OK" {
			return nil, ErrBatchIdentity
		}
		if _, ok := o["result_type"]; ok && textField(o, "result_type") != "order_action_receipt" {
			return nil, ErrBatchIdentity
		}
		stage := textField(a, "failure_stage")
		if oneOf(stage, "command_validation", "command_admission") && (request != nil || code != nil || !literal(a["sdk_calls"], "0")) {
			return nil, ErrBatchIdentity
		}
	} else {
		return nil, ErrBatchIdentity
	}
	return o, nil
}
