package redisstream

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ti-relay-trader/internal/timeutil"
)

type MessageSummary struct {
	Stream          string          `json:"stream"`
	StreamID        string          `json:"stream_id"`
	MessageType     string          `json:"message_type,omitempty"`
	MessageID       string          `json:"message_id,omitempty"`
	EventType       string          `json:"event_type,omitempty"`
	Action          string          `json:"action,omitempty"`
	Status          string          `json:"status,omitempty"`
	Code            string          `json:"code,omitempty"`
	OriginMessageID string          `json:"origin_message_id,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	GatewayOrderID  string          `json:"gateway_order_id,omitempty"`
	AccountID       string          `json:"account_id,omitempty"`
	ProducedAt      string          `json:"produced_at,omitempty"`
	BodyBytes       int             `json:"body_bytes"`
	PayloadKeys     []string        `json:"payload_keys,omitempty"`
	ParseError      string          `json:"parse_error,omitempty"`
	RawFields       map[string]bool `json:"raw_fields,omitempty"`
}

type genericMessage struct {
	Protocol             string          `json:"protocol"`
	MessageType          string          `json:"message_type"`
	MessageID            string          `json:"message_id"`
	EventType            string          `json:"event_type"`
	EventName            string          `json:"event_name"`
	Action               string          `json:"action"`
	Status               string          `json:"status"`
	Code                 string          `json:"code"`
	Message              string          `json:"message"`
	ResultType           string          `json:"result_type"`
	OriginMessageID      string          `json:"origin_message_id"`
	RequestID            string          `json:"request_id"`
	CorrelationID        string          `json:"correlation_id"`
	RequestCorrelationID string          `json:"request_correlation_id"`
	IdempotencyKey       string          `json:"idempotency_key"`
	GatewayOrderID       string          `json:"gateway_order_id"`
	ProducedAt           string          `json:"produced_at"`
	Routing              routingMessage  `json:"routing"`
	Payload              json.RawMessage `json:"payload"`
	AdapterContext       map[string]any  `json:"adapter_context"`
}

type routingMessage struct {
	Env       string `json:"env"`
	BrokerID  string `json:"broker_id"`
	GatewayID string `json:"gateway_id"`
	AccountID string `json:"account_id"`
}

type EntryEnvelope struct {
	Stream               string
	StreamID             string
	BodyText             string
	Protocol             string
	MessageType          string
	MessageID            string
	EventType            string
	Action               string
	Status               string
	Code                 string
	Message              string
	ResultType           string
	OriginMessageID      string
	RequestID            string
	CorrelationID        string
	RequestCorrelationID string
	IdempotencyKey       string
	GatewayOrderID       string
	ProducedAt           time.Time
	Routing              Routing
	Payload              json.RawMessage
	AdapterContext       map[string]any
}

type Routing struct {
	Env       string `json:"env,omitempty"`
	BrokerID  string `json:"broker_id,omitempty"`
	GatewayID string `json:"gateway_id,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

func DecodeEntry(stream, streamID string, values map[string]any) (EntryEnvelope, error) {
	envelope := EntryEnvelope{
		Stream:   stream,
		StreamID: streamID,
		Routing:  routingFromStream(stream),
	}

	bodyValue, ok := values["body"]
	if !ok {
		return envelope, fmt.Errorf("missing body field")
	}
	body, ok := bodyString(bodyValue)
	if !ok {
		return envelope, fmt.Errorf("body field has type %T", bodyValue)
	}
	envelope.BodyText = body

	var msg genericMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		return envelope, err
	}

	envelope.Protocol = msg.Protocol
	envelope.MessageType = msg.MessageType
	envelope.MessageID = msg.MessageID
	if msg.EventType != "" {
		envelope.EventType = msg.EventType
	} else {
		envelope.EventType = msg.EventName
	}
	envelope.Action = msg.Action
	envelope.Status = msg.Status
	envelope.Code = msg.Code
	envelope.Message = msg.Message
	envelope.ResultType = msg.ResultType
	envelope.OriginMessageID = msg.OriginMessageID
	envelope.RequestID = msg.RequestID
	envelope.CorrelationID = msg.CorrelationID
	envelope.RequestCorrelationID = msg.RequestCorrelationID
	envelope.IdempotencyKey = msg.IdempotencyKey
	envelope.GatewayOrderID = msg.GatewayOrderID
	envelope.Payload = msg.Payload
	envelope.AdapterContext = msg.AdapterContext
	envelope.ProducedAt = parseTime(msg.ProducedAt)

	if msg.Routing.Env != "" {
		envelope.Routing.Env = msg.Routing.Env
	}
	if msg.Routing.BrokerID != "" {
		envelope.Routing.BrokerID = msg.Routing.BrokerID
	}
	if msg.Routing.GatewayID != "" {
		envelope.Routing.GatewayID = msg.Routing.GatewayID
	}
	if msg.Routing.AccountID != "" {
		envelope.Routing.AccountID = msg.Routing.AccountID
	}
	return envelope, nil
}

func SummarizeEntry(stream, streamID string, values map[string]any) MessageSummary {
	summary := MessageSummary{
		Stream:    stream,
		StreamID:  streamID,
		RawFields: map[string]bool{},
	}
	for key := range values {
		summary.RawFields[key] = true
	}

	envelope, err := DecodeEntry(stream, streamID, values)
	if err != nil {
		summary.ParseError = err.Error()
		if envelope.BodyText != "" {
			summary.BodyBytes = len(envelope.BodyText)
		}
		return summary
	}

	summary.BodyBytes = len(envelope.BodyText)
	summary.MessageType = envelope.MessageType
	summary.MessageID = envelope.MessageID
	summary.EventType = envelope.EventType
	summary.Action = envelope.Action
	summary.Status = envelope.Status
	summary.Code = envelope.Code
	summary.OriginMessageID = envelope.OriginMessageID
	summary.RequestID = envelope.RequestID
	summary.CorrelationID = envelope.CorrelationID
	summary.GatewayOrderID = envelope.GatewayOrderID
	summary.AccountID = envelope.Routing.AccountID
	if !envelope.ProducedAt.IsZero() {
		summary.ProducedAt = timeutil.FormatRFC3339Nano(envelope.ProducedAt)
	}
	summary.PayloadKeys = payloadKeys(envelope.Payload)

	return summary
}

func bodyString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}

func routingFromStream(stream string) Routing {
	parts := strings.Split(stream, ":")
	if len(parts) < 6 || parts[0] != "relay" || parts[2] != "v1" {
		return Routing{}
	}
	return Routing{
		Env:       parts[1],
		BrokerID:  parts[3],
		GatewayID: parts[4],
	}
}

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func payloadKeys(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
