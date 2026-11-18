package futures

import "encoding/json"

// The existing OC runtime name now also carries execution-only evidence.
// Never infer this contract from absent legacy fields or from ready=true.
func (a HeartbeatAssessment) relayExecution() bool { return a.AuthorizationOwner == "relay" }

func (a *HeartbeatAssessment) decodeRelayExecution(raw rawObject, h Heartbeat) error {
	if textField(raw, "authorization_owner") != "relay" || h.Operations == nil || h.Operations.QueryReason == "query_window_closed" {
		return ErrQueryPublishGate
	}
	for _, key := range []string{"assessment_id", "expires_at", "permission_profile", "symbol", "exchange"} {
		if _, exists := raw[key]; exists {
			return ErrQueryPublishGate
		}
	}
	l, err := shape(raw["process_lifecycle"], []string{"mode", "process_duration_seconds"}, nil)
	if err != nil || textField(l, "mode") != "resident_execution.v1" {
		return ErrQueryPublishGate
	}
	n, err := nullableInteger(l["process_duration_seconds"])
	if err != nil || n == nil || *n < 0 || *n > 86400 {
		return ErrQueryPublishGate
	}
	if !literal(raw["per_command_native_validation"], "true") {
		return ErrQueryPublishGate
	}
	if json.Unmarshal(raw["constraint_symbol"], &a.ConstraintSymbol) != nil || literal(raw["constraint_symbol"], "null") ||
		(a.ConstraintSymbol != "" && !native(a.ConstraintSymbol, 80, false)) {
		return ErrQueryPublishGate
	}
	for key, dst := range map[string]*bool{"trade_preparation_pending": &a.TradePreparationPending, "trade_response_pending": &a.TradeResponsePending} {
		if !literal(raw[key], "true") && !literal(raw[key], "false") {
			return ErrQueryPublishGate
		}
		*dst = literal(raw[key], "true")
	}
	a.AuthorizationOwner, a.ResidentDurationSeconds = "relay", *n
	return a.decodeResidentDiagnostics(raw, h)
}
