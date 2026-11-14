package redisstream

import (
	"errors"

	"ti-relay-trader/internal/trading"
)

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
