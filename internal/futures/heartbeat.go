package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

const maxHeartbeatBytes = 64 * 1024

// Heartbeat follows the shared draft.2 fixture, not a new runtime schema.
// It has no connection generation or native login identity evidence.
type Heartbeat struct {
	MessageID, CounterSessionID, TradeDate                           string
	ProducedAt                                                       time.Time
	RedisReady, BrokerReady, SettlementConfirmed, OrderSnapshotReady bool
	AcceptingTradeCommands, AcceptingCancelCommands                  bool
	Raw                                                              []byte
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
	obj, err := shape(raw, []string{"protocol", "message_type", "message_id", "produced_at", "routing", "payload"}, []string{"adapter_context"})
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
	if audit, ok := obj["adapter_context"]; ok {
		var fields rawObject
		if json.Unmarshal(audit, &fields) != nil || fields == nil {
			return bad("adapter context")
		}
	}
	flags := []string{"redis_ready", "broker_ready", "settlement_confirmed", "order_snapshot_ready", "accepting_trade_commands", "accepting_cancel_commands"}
	required := append([]string{"product_type", "schema_version", "counter_session_id", "trade_date"}, flags...)
	p, err := shape(obj["payload"], required, nil)
	if err != nil {
		return Heartbeat{}, err
	}
	if textField(p, "product_type") != "futures" || textField(p, "schema_version") != "ctp.f0.draft.2" || !validDay(textField(p, "trade_date")) {
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
	return h, nil
}
