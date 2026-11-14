package redisstream

import (
	"errors"

	"ti-relay-trader/internal/trading"
)

// DecodePositionQueryRecords reuses the current ingestion projection without
// applying a snapshot or interpreting an absent page as an empty position set.
func DecodePositionQueryRecords(e EntryEnvelope) ([]trading.Position, error) {
	if e.Protocol != "relay.stream.v1" || e.MessageType != "reply" || e.Action != ActionAccountPositions || e.ResultType != "position_page" || (e.Status != "partial" && e.Status != "completed") {
		return nil, errors.New("not a successful position query page")
	}
	positions, _, err := positionsFromReplyEnvelope(e)
	return positions, err
}

// DecodeTradeQueryRecords reuses ingestion normalization without a writer,
// Redis client, summary-fill synthesis or any persistence side effects.
// The caller must independently validate query scope and page completeness.
func DecodeTradeQueryRecords(e EntryEnvelope) ([]trading.Order, []trading.Fill, []trading.ComponentTransfer, error) {
	if e.Protocol != "relay.stream.v1" || e.MessageType != "reply" || (e.Status != "partial" && e.Status != "completed") {
		return nil, nil, nil, errors.New("not a successful trade query page")
	}
	switch {
	case e.Action == ActionOrderList && e.ResultType == "order_page":
		orders, err := ordersFromReplyEnvelope(e)
		return orders, nil, nil, err
	case e.Action == ActionFillList && e.ResultType == "fill_page":
		fills, transfers, err := fillRecordsFromReplyEnvelope(e)
		return nil, fills, transfers, err
	default:
		return nil, nil, nil, errors.New("unsupported trade query action/result")
	}
}

// DecodeTradeEventRecord is an audit-only projection of a complete event. It
// does not apply partial updates or synthesize an order/summary fill.
func DecodeTradeEventRecord(e EntryEnvelope) (*trading.Order, *trading.Fill, error) {
	if e.Protocol != "relay.stream.v1" || e.MessageType != "event" {
		return nil, nil, errors.New("not a trade event")
	}
	switch e.EventType {
	case "order.event":
		event, complete, err := orderEventFromEnvelope(e)
		if err != nil {
			return nil, nil, err
		}
		if !complete {
			return nil, nil, errors.New("partial order event cannot prove full book projection")
		}
		return &event.Order, nil, nil
	case "fill.event":
		fill, err := fillFromEnvelope(e)
		if err != nil {
			return nil, nil, err
		}
		return nil, &fill, nil
	default:
		return nil, nil, errors.New("unsupported trade event")
	}
}
