package events

import (
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

func FromLedgerChange(change redisstream.LedgerChange) []Event {
	base := Event{
		AccountIDs:   change.AccountIDs,
		Source:       "redis-ledger-sync",
		Stream:       change.Stream,
		LastStreamID: change.LastStreamID,
		Data: map[string]any{
			"role":            change.Role,
			"orders":          change.Orders,
			"order_events":    change.OrderEvents,
			"cancel_attempts": change.CancelAttempts,
			"cancel_failures": change.CancelFailures,
			"fills":           change.Fills,
			"transfers":       change.Transfers,
			"assets":          change.Assets,
			"positions":       change.Positions,
			"last_stream_id":  change.LastStreamID,
		},
	}
	events := make([]Event, 0, 5+len(change.CancelFailureItems))
	if change.Orders > 0 || change.OrderEvents > 0 {
		event := base
		event.Type = TypeOrderChanged
		events = append(events, event)
	}
	for _, attempt := range change.CancelFailureItems {
		event := base
		event.Type = TypeOrderCancelRejected
		event.Data = map[string]any{
			"role":            change.Role,
			"cancel_attempts": change.CancelAttempts,
			"cancel_failures": change.CancelFailures,
			"cancel_attempt":  cancelAttemptEventData(attempt),
			"last_stream_id":  change.LastStreamID,
		}
		events = append(events, event)
	}
	if change.Fills > 0 || change.Transfers > 0 {
		event := base
		event.Type = TypeFillChanged
		events = append(events, event)
	}
	if change.Assets > 0 {
		event := base
		event.Type = TypeAssetChanged
		events = append(events, event)
	}
	if change.Positions > 0 {
		event := base
		event.Type = TypePositionsChanged
		events = append(events, event)
	}
	return events
}

func PublishLedgerChange(hub *Hub, change redisstream.LedgerChange) {
	for _, event := range FromLedgerChange(change) {
		hub.Publish(event)
	}
}

func cancelAttemptEventData(attempt ledger.OrderCancelAttempt) map[string]any {
	return map[string]any{
		"attempt_id":              attempt.AttemptID,
		"account_id":              attempt.AccountID,
		"trade_date":              attempt.TradeDate,
		"gateway_order_id":        attempt.GatewayOrderID,
		"order_id":                attempt.OrderID,
		"order_stream_id":         attempt.OrderStreamID,
		"origin_message_id":       attempt.OriginMessageID,
		"request_id":              attempt.RequestID,
		"correlation_id":          attempt.CorrelationID,
		"status":                  attempt.Status,
		"code":                    attempt.Code,
		"message":                 attempt.Message,
		"retry_safe":              attempt.RetrySafe,
		"order_state_changed":     attempt.OrderStateChanged,
		"reconciliation_required": attempt.ReconciliationRequired,
		"occurred_at":             attempt.OccurredAt,
		"stream_key":              attempt.StreamKey,
		"stream_id":               attempt.StreamID,
	}
}
