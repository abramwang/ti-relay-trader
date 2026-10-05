package ledger

import (
	"context"
	"time"
)

type TestOrderResolution struct {
	ResolutionID     int64     `json:"resolution_id"`
	GatewayOrderID   string    `json:"gateway_order_id"`
	TradeDate        string    `json:"trade_date"`
	CounterSessionID string    `json:"counter_session_id"`
	ConfirmedBy      string    `json:"confirmed_by"`
	Reason           string    `json:"reason"`
	ConfirmedAt      time.Time `json:"confirmed_at"`
}

const testOrderResolutionSnapshotSQL = `jsonb_build_object(
'trade_date', o.trade_date, 'gateway_order_id', o.gateway_order_id,
'status', o.status, 'gateway_status', o.gateway_status, 'is_terminal', o.is_terminal,
'order_qty', o.order_qty, 'cum_filled_qty', o.cum_filled_qty, 'leaves_qty', o.leaves_qty,
'order_id', o.order_id, 'order_stream_id', o.order_stream_id,
'origin_message_id', o.origin_message_id, 'counter_session_id', o.adapter_context->>'counter_session_id')`

const validTestOrderResolutionSQL = `SELECT 1 FROM test_legacy_order_resolutions r
WHERE r.environment = 'test' AND r.order_pk = o.order_pk AND r.account_id = o.account_id
AND r.trade_date = o.trade_date AND r.gateway_order_id = o.gateway_order_id
AND r.counter_session_id = $2 AND r.order_snapshot = ` + testOrderResolutionSnapshotSQL

// Resolutions are separate from broker facts and valid only for the explicitly
// confirmed session and unchanged order snapshot.
func (repo *Repository) ResolvedTestOrderKeys(ctx context.Context, account, session string) (map[string]TestOrderResolution, error) {
	q, err := repo.queryer()
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT r.resolution_pk, r.gateway_order_id, r.trade_date::text,
r.counter_session_id, r.confirmed_by, r.reason, r.confirmed_at
FROM test_legacy_order_resolutions r JOIN orders o ON o.order_pk = r.order_pk
WHERE r.environment = 'test' AND r.account_id = $1 AND o.account_id = $1
AND r.trade_date = o.trade_date AND r.gateway_order_id = o.gateway_order_id
AND r.counter_session_id = $2 AND r.order_snapshot = `+testOrderResolutionSnapshotSQL, account, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TestOrderResolution{}
	for rows.Next() {
		var item TestOrderResolution
		if err := rows.Scan(&item.ResolutionID, &item.GatewayOrderID, &item.TradeDate, &item.CounterSessionID, &item.ConfirmedBy, &item.Reason, &item.ConfirmedAt); err != nil {
			return nil, err
		}
		out[item.TradeDate+"/"+item.GatewayOrderID] = item
	}
	return out, rows.Err()
}
