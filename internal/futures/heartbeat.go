package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

const maxHeartbeatBytes = 64 * 1024

// Heartbeat follows draft.2 and the explicit OC TEST runtime profiles.
// Neither a decoded heartbeat nor broker login grants trading authority.
type Heartbeat struct {
	MessageID, CounterSessionID, TradeDate                           string
	State, StateText                                                 string
	Recovery                                                         *HeartbeatRecovery
	ReadOnly                                                         *HeartbeatReadOnly
	Assessment                                                       *HeartbeatAssessment
	ProducedAt                                                       time.Time
	RedisReady, BrokerReady, SettlementConfirmed, OrderSnapshotReady bool
	AcceptingTradeCommands, AcceptingCancelCommands                  bool
	Raw                                                              []byte
}

type HeartbeatReadOnly struct {
	ConnectionGeneration   *int64
	QueryCollectionReady   bool
	TransportRecoveryClear bool
}

// Recovery is transport diagnostics, never broker readiness. In this profile
// TradeDate is empty because the source explicitly supplied null, not a date.
type HeartbeatRecovery struct {
	TransportRecoveryClear bool
	RecoveryIssueCount     int64
}

func DecodeHeartbeat(raw []byte, expected QueryRoute) (Heartbeat, error) {
	bad := func(reason string) (Heartbeat, error) {
		return Heartbeat{}, fmt.Errorf("invalid futures heartbeat: %s", reason)
	}
	if !oneOf(expected.Env, "test", "prod") || !routeBrokerPattern.MatchString(expected.BrokerID) ||
		!accountPattern.MatchString(expected.AccountID) || !validID(expected.GatewayID) || len(expected.GatewayID) > 128 || len(raw) > maxHeartbeatBytes {
		return bad("route or size")
	}
	if _, err := parseReply(raw); err != nil {
		return Heartbeat{}, err
	}
	flags := []string{"redis_ready", "broker_ready", "settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"}
	mirrors := append([]string{"counter_session_id", "state", "state_text"}, flags...)
	obj, err := shape(raw, []string{"protocol", "message_type", "message_id", "produced_at", "routing", "payload"}, append([]string{"adapter_context"}, mirrors...))
	if err != nil {
		return Heartbeat{}, err
	}
	if textField(obj, "protocol") != "relay.stream.v1" || textField(obj, "message_type") != "heartbeat" || !validTime(textField(obj, "produced_at")) {
		return bad("protocol/type/time")
	}
	route, err := shape(obj["routing"], []string{"env", "broker_id", "gateway_id", "account_id"}, nil)
	if err != nil {
		return Heartbeat{}, err
	}
	if (QueryRoute{textField(route, "env"), textField(route, "broker_id"), textField(route, "gateway_id"), textField(route, "account_id")}) != expected {
		return bad("source route mismatch")
	}
	var fields rawObject
	if audit, ok := obj["adapter_context"]; ok {
		if json.Unmarshal(audit, &fields) != nil || fields == nil {
			return bad("adapter context")
		}
	}
	_, runtimeProfile := fields["runtime_mode"]
	mode := textField(fields, "runtime_mode")
	recoveryMode, readOnlyMode := mode == "test_transport_recovery_only", mode == "test_broker_read_only"
	assessmentMode := mode == "test_broker_assessment"
	if runtimeProfile && !recoveryMode && !readOnlyMode && !assessmentMode {
		return bad("unsupported runtime mode")
	}
	required := append([]string{"product_type", "schema_version", "counter_session_id", "trade_date"}, flags...)
	if runtimeProfile {
		required = append(required, "state", "state_text")
	} else {
		for _, key := range mirrors {
			if _, exists := obj[key]; exists {
				return bad("mirror outside recovery profile")
			}
		}
	}
	p, err := shape(obj["payload"], required, nil)
	if err != nil {
		return Heartbeat{}, err
	}
	if textField(p, "product_type") != "futures" || textField(p, "schema_version") != "ctp.f0.draft.2" || (!runtimeProfile && !validDay(textField(p, "trade_date"))) {
		return bad("product/schema/day")
	}
	h := Heartbeat{MessageID: textField(obj, "message_id"), CounterSessionID: textField(p, "counter_session_id"), TradeDate: textField(p, "trade_date"), Raw: bytes.Clone(raw)}
	for _, id := range []string{h.MessageID, h.CounterSessionID} {
		if !validID(id) || len(id) > 128 {
			return bad("identifier")
		}
	}
	h.ProducedAt, _ = time.Parse(time.RFC3339Nano, textField(obj, "produced_at"))
	values := []*bool{&h.RedisReady, &h.BrokerReady, &h.SettlementConfirmed, &h.OrderSnapshotReady, &h.AcceptingTradeCommands, &h.AcceptingCancelCommands}
	for i, key := range flags {
		if !literal(p[key], "true") && !literal(p[key], "false") {
			return bad("boolean " + key)
		}
		*values[i] = literal(p[key], "true")
	}
	if assessmentMode {
		if expected.Env != "test" {
			return bad("assessment TEST only")
		}
		for _, key := range mirrors {
			same := bytes.Equal(bytes.TrimSpace(obj[key]), bytes.TrimSpace(p[key]))
			if oneOf(key, "counter_session_id", "state", "state_text") {
				same = textField(obj, key) == textField(p, key)
			}
			if !same {
				return bad("missing or conflicting assessment mirror " + key)
			}
		}
		if !literal(p["trade_date"], "null") && !validDay(h.TradeDate) {
			return bad("assessment day")
		}
		h.State, h.StateText = textField(p, "state"), textField(p, "state_text")
		a, err := decodeAssessmentHeartbeat(fields, h)
		if err != nil {
			return Heartbeat{}, err
		}
		h.Assessment = &a
		return h, nil
	}
	if readOnlyMode {
		if expected.Env != "test" || h.SettlementConfirmed || h.OrderSnapshotReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
			return bad("read-only scope/readiness")
		}
		for _, key := range mirrors {
			same := bytes.Equal(bytes.TrimSpace(obj[key]), bytes.TrimSpace(p[key]))
			if oneOf(key, "counter_session_id", "state", "state_text") {
				same = textField(obj, key) == textField(p, key)
			}
			if !same {
				return bad("missing or conflicting mirror " + key)
			}
		}
		generation, err := nullableInteger(fields["connection_generation"])
		if err != nil || (generation != nil && *generation < 1) {
			return bad("read-only generation")
		}
		pid, err := nullableInteger(fields["process_id"])
		if err != nil || pid == nil || *pid < 1 {
			return bad("read-only process id")
		}
		for _, key := range []string{"query_collection_ready", "transport_recovery_clear"} {
			if !literal(fields[key], "true") && !literal(fields[key], "false") {
				return bad("read-only boolean " + key)
			}
		}
		if !literal(p["trade_date"], "null") && !validDay(h.TradeDate) {
			return bad("read-only day")
		}
		h.State, h.StateText = textField(p, "state"), textField(p, "state_text")
		h.ReadOnly = &HeartbeatReadOnly{generation, literal(fields["query_collection_ready"], "true"), literal(fields["transport_recovery_clear"], "true")}
		if (h.BrokerReady && (generation == nil || !validDay(h.TradeDate))) ||
			(h.StateText == "read_only" && (h.State != "UP" || !h.RedisReady || !h.BrokerReady || !h.ReadOnly.TransportRecoveryClear)) ||
			(h.StateText != "read_only" && (h.State != "DEGRADED" || !oneOf(h.StateText, "recovery_blocked", "broker_not_ready", "stopping"))) ||
			(h.StateText == "recovery_blocked" && h.ReadOnly.TransportRecoveryClear) ||
			(h.StateText == "broker_not_ready" && h.BrokerReady) ||
			(h.ReadOnly.QueryCollectionReady && (h.StateText != "read_only" || !h.RedisReady || !h.BrokerReady || !h.ReadOnly.TransportRecoveryClear)) {
			return bad("read-only diagnostics conflict")
		}
		return h, nil
	}
	if recoveryMode {
		if expected.Env != "test" || !literal(p["trade_date"], "null") || !literal(fields["connection_generation"], "null") ||
			h.BrokerReady || h.SettlementConfirmed || h.OrderSnapshotReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
			return bad("recovery-only scope/readiness")
		}
		for _, key := range mirrors {
			same := bytes.Equal(bytes.TrimSpace(obj[key]), bytes.TrimSpace(p[key]))
			if oneOf(key, "counter_session_id", "state", "state_text") {
				same = textField(obj, key) == textField(p, key)
			}
			if !same {
				return bad("missing or conflicting mirror " + key)
			}
		}
		h.State, h.StateText = textField(p, "state"), textField(p, "state_text")
		if h.State != "DEGRADED" || !oneOf(h.StateText, "broker_disabled", "recovery_blocked", "stopping") {
			return bad("recovery state")
		}
		clear := fields["transport_recovery_clear"]
		if !literal(clear, "true") && !literal(clear, "false") {
			return bad("transport recovery boolean")
		}
		count, err := nullableInteger(fields["recovery_issue_count"])
		if err != nil || count == nil || *count < 0 {
			return bad("recovery issue count")
		}
		pid, err := nullableInteger(fields["process_id"])
		if err != nil || pid == nil || *pid < 1 {
			return bad("recovery process id")
		}
		h.Recovery = &HeartbeatRecovery{literal(clear, "true"), *count}
		if (h.StateText == "broker_disabled" && !h.Recovery.TransportRecoveryClear) ||
			(h.StateText == "recovery_blocked" && h.Recovery.TransportRecoveryClear) ||
			(h.Recovery.TransportRecoveryClear && *count != 0) {
			return bad("recovery diagnostics conflict")
		}
	}
	return h, nil
}
