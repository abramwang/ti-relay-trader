package redisstream

import (
	"encoding/json"
	"fmt"
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
	Protocol        string          `json:"protocol"`
	MessageType     string          `json:"message_type"`
	MessageID       string          `json:"message_id"`
	EventType       string          `json:"event_type"`
	EventName       string          `json:"event_name"`
	Action          string          `json:"action"`
	Status          string          `json:"status"`
	Code            string          `json:"code"`
	OriginMessageID string          `json:"origin_message_id"`
	RequestID       string          `json:"request_id"`
	CorrelationID   string          `json:"correlation_id"`
	GatewayOrderID  string          `json:"gateway_order_id"`
	ProducedAt      string          `json:"produced_at"`
	Routing         routingMessage  `json:"routing"`
	Payload         json.RawMessage `json:"payload"`
}

type routingMessage struct {
	AccountID string `json:"account_id"`
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

	bodyValue, ok := values["body"]
	if !ok {
		summary.ParseError = "missing body field"
		return summary
	}
	body, ok := bodyValue.(string)
	if !ok {
		summary.ParseError = fmt.Sprintf("body field has type %T", bodyValue)
		return summary
	}
	summary.BodyBytes = len(body)

	var msg genericMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		summary.ParseError = err.Error()
		return summary
	}

	summary.MessageType = msg.MessageType
	summary.MessageID = msg.MessageID
	if msg.EventType != "" {
		summary.EventType = msg.EventType
	} else {
		summary.EventType = msg.EventName
	}
	summary.Action = msg.Action
	summary.Status = msg.Status
	summary.Code = msg.Code
	summary.OriginMessageID = msg.OriginMessageID
	summary.RequestID = msg.RequestID
	summary.CorrelationID = msg.CorrelationID
	summary.GatewayOrderID = msg.GatewayOrderID
	summary.AccountID = msg.Routing.AccountID
	summary.ProducedAt = msg.ProducedAt
	summary.PayloadKeys = payloadKeys(msg.Payload)

	return summary
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
