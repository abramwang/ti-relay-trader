package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Re-evaluated inside the publication transaction. Never choose an older
// working observation after a terminal, corrupt or unaccounted fill record.
func latestServiceCancelReceipt(ctx context.Context, tx *sql.Tx, parent CommandRegistration, target TestQueryTarget) (int64, error) {
	if len(parent.Original.Orders) != 1 {
		return 0, ErrStreamIdentity
	}
	o := parent.Original.Orders[0]
	key := strings.TrimSuffix(queryReplyStream(parent.Scope.Route), "reply") + "event"
	var configRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT config_json FROM futures_stream_receivers WHERE stream_key=$1`, key).Scan(&configRaw); err != nil {
		return 0, err
	}
	var config StreamConfig
	if json.Unmarshal(configRaw, &config) != nil || config.Scope != parent.Scope || config.Role != "event" || config.SourceID != target.SourceID {
		return 0, ErrStreamIdentity
	}
	rows, err := tx.QueryContext(ctx, `SELECT receipt_id,entry_id,body,body_sha256,state FROM futures_stream_inbox WHERE stream_key=$1 AND (convert_from(body,'UTF8')::jsonb->>'origin_message_id'=$2 OR convert_from(body,'UTF8')::jsonb->'payload'->>'gateway_order_id'=$3) ORDER BY receipt_id LIMIT 257`, key, parent.Original.MessageID, o.GatewayOrderID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var latestID int64
	latestEntry, fillEntry := "0-0", "0-0"
	var latest *OrderRow
	var produced time.Time
	var systemID string
	count, total := 0, 0
	projection := newTradeProjection(TradeScope{parent.Scope.Route, parent.Scope.NativeBrokerID, parent.Scope.InvestorID, target.TradeDate})
	for rows.Next() {
		var id int64
		var entry, state string
		var raw, digest []byte
		if err := rows.Scan(&id, &entry, &raw, &digest, &state); err != nil {
			return 0, err
		}
		count++
		total += len(raw)
		if count > 256 || total > 4<<20 {
			return 0, ErrAuditCapacity
		}
		h := sha256.Sum256(raw)
		if state == "quarantined" || !bytes.Equal(h[:], digest) {
			return 0, ErrStreamIdentity
		}
		if _, err := streamID(entry); err != nil || entry == "0-0" {
			return 0, ErrStreamCursor
		}
		event, err := DecodeTradeEvent(raw, EventContext{Scope: TradeScope{Route: parent.Scope.Route, NativeBrokerID: parent.Scope.NativeBrokerID, InvestorID: parent.Scope.InvestorID, TradeDate: target.TradeDate}, ExpectedOrigin: &parent.Original.MessageID})
		if err != nil || (event.Order == nil && event.Fill == nil) || !o.matches(event, parent.Original.SubmitCommand) {
			return 0, ErrStreamIdentity
		}
		if event.Fill != nil {
			if event.Fill.CounterSessionID != target.CounterSessionID || (event.Fill.NativeIDs.UserID != "" && event.Fill.NativeIDs.UserID != parent.Scope.UserID) {
				return 0, ErrStreamIdentity
			}
			fresh := projection.report.Executions[event.Fill.FillID] == nil
			projection.fill(entry, *event.Fill)
			if cmp, _ := compareStreamID(entry, fillEntry); fresh && cmp > 0 {
				fillEntry = entry
			}
			continue
		}
		row := event.Order
		projection.order(*row)
		if row.CounterSessionID != target.CounterSessionID || (row.NativeIDs.UserID != "" && row.NativeIDs.UserID != parent.Scope.UserID) {
			return 0, ErrStreamIdentity
		}
		if row.NativeIDs.OrderSysID != "" {
			if systemID != "" && systemID != row.NativeIDs.OrderSysID {
				return 0, ErrStreamIdentity
			}
			systemID = row.NativeIDs.OrderSysID
		}
		if oneOf(row.GatewayStatus, "filled", "cancelled", "rejected") {
			return 0, ErrQueryPublishGate
		}
		if cmp, _ := compareStreamID(entry, latestEntry); cmp > 0 {
			latestID, latestEntry, latest = id, entry, row
			produced, err = time.Parse(time.RFC3339Nano, event.ProducedAt)
			if err != nil {
				return 0, ErrStreamIdentity
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	projection.reconcile()
	filled, done, progressErr := closeOrderProgress(o, projection.report)
	if progressErr != nil || done || filled >= int64(o.OrderQty) || len(projection.report.Conflicts) != 0 || projection.report.PendingCount != 0 || (latest != nil && filled > int64(latest.CumFilledQty)) {
		return 0, ErrQueryPublishGate
	}
	cmp, _ := compareStreamID(fillEntry, latestEntry)
	// Order age is not receiver freshness. The service/publication paths check
	// live capture health separately; a quiet working order can remain cancellable.
	if latest == nil || cmp > 0 || !oneOf(latest.GatewayStatus, "accepted", "working") || latest.LeavesQty <= 0 || strings.TrimSpace(latest.NativeIDs.OrderSysID) == "" || produced.After(time.Now().Add(2*time.Second)) {
		return 0, ErrQueryPublishGate
	}
	return latestID, nil
}
