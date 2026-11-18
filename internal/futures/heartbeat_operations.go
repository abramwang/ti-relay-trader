package futures

import "encoding/json"

// nil Operations means a legacy heartbeat, not an explicit permission.
type HeartbeatOperations struct {
	AcceptingQueryCommands bool   `json:"accepting_query_commands"`
	QueryReason            string `json:"query_reason"`
	SubmitReason           string `json:"submit_reason"`
	CancelReason           string `json:"cancel_reason"`
}

func decodeHeartbeatOperations(envelope, payload, adapter rawObject, h Heartbeat, assessment bool) (*HeartbeatOperations, error) {
	_, top := envelope["accepting_query_commands"]
	_, body := payload["accepting_query_commands"]
	_, reasonsPresent := adapter["operation_reasons"]
	var admission rawObject
	_ = json.Unmarshal(adapter["admission_evidence"], &admission)
	_, executionPresent := admission["query_execution"]
	if !top && !body && !reasonsPresent && !executionPresent {
		return nil, nil
	}
	if !assessment || !top || !body || !reasonsPresent || !executionPresent {
		return nil, ErrQueryPublishGate
	}
	value := payload["accepting_query_commands"]
	if (!literal(value, "true") && !literal(value, "false")) ||
		literal(value, "true") != literal(envelope["accepting_query_commands"], "true") ||
		literal(value, "false") != literal(envelope["accepting_query_commands"], "false") {
		return nil, ErrQueryPublishGate
	}
	reasons, err := shape(adapter["operation_reasons"], []string{"query", "submit", "cancel"}, nil)
	if err != nil {
		return nil, err
	}
	execution, err := shape(admission["query_execution"], []string{"ready", "reason"}, nil)
	if err != nil {
		return nil, err
	}
	o := &HeartbeatOperations{literal(value, "true"), textField(reasons, "query"), textField(reasons, "submit"), textField(reasons, "cancel")}
	for _, pair := range []struct {
		ready  bool
		reason string
	}{{o.AcceptingQueryCommands, o.QueryReason}, {h.AcceptingTradeCommands, o.SubmitReason}, {h.AcceptingCancelCommands, o.CancelReason}} {
		if !validID(pair.reason) || len(pair.reason) > 128 || (pair.reason == "ready") != pair.ready {
			return nil, ErrQueryPublishGate
		}
	}
	if o.QueryReason == "journal_capacity_exhausted" || o.SubmitReason == "journal_capacity_exhausted" || o.CancelReason == "journal_capacity_exhausted" {
		if textField(admission, "authorization_owner") != "relay" {
			return nil, ErrQueryPublishGate
		}
	}
	if !oneOf(o.QueryReason, "ready", "broker_not_ready", "recovery_blocked", "query_window_closed", "query_command_limit", "journal_capacity_exhausted", "initialization_pending", "query_scheduler_busy", "stopping") ||
		(!literal(execution["ready"], "true") && !literal(execution["ready"], "false")) ||
		literal(execution["ready"], "true") != o.AcceptingQueryCommands || textField(execution, "reason") != o.QueryReason {
		return nil, ErrQueryPublishGate
	}
	return o, nil
}
