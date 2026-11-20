package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
)

type CurrentBaselineCommandArchive struct {
	Archive          CurrentBaselineArchive
	IndexedMessageID string
}

// Nil means not collected, not an empty or resolved command set. Archives are
// post-publication observations, not a durable pre-send responsibility registry.
type CurrentBaselineCommandSources struct {
	Archives []CurrentBaselineCommandArchive
	Orders   []CurrentBaselineRecord
}

func readCurrentBaselineCommands(ctx context.Context, tx *sql.Tx, account string, budget *int) (*CurrentBaselineCommandSources, error) {
	out := &CurrentBaselineCommandSources{Archives: []CurrentBaselineCommandArchive{}, Orders: []CurrentBaselineRecord{}}
	// Account-indexed, bounded history, including old gateways and dates. Do not
	// take only the newest N commands or infer completion from a successful reply.
	rows, err := tx.QueryContext(ctx, `SELECT raw_message_pk::text,stream_key,stream_id,direction,stream_role,
COALESCE(body_text,''),COALESCE(parse_error,''),received_at,COALESCE(origin_message_id,'')
FROM raw_stream_messages WHERE account_id=$1 AND stream_role='cmd.trade'
ORDER BY raw_stream_messages.raw_message_pk LIMIT $2`, account, currentBaselineMaxRows+1)
	if err != nil {
		return nil, err
	}
	ids, seen := []string{}, map[string]bool{}
	for rows.Next() {
		var c CurrentBaselineCommandArchive
		r := &c.Archive
		if err = rows.Scan(&r.ArchiveID, &r.Stream, &r.EntryID, &r.Direction, &r.Role, &r.Body, &r.ParseError, &r.ReceivedAt, &c.IndexedMessageID); err != nil {
			rows.Close()
			return nil, err
		}
		*budget += len(r.Body)
		if len(out.Archives) >= currentBaselineMaxRows || *budget > currentBaselineMaxBytes {
			rows.Close()
			return nil, ErrCurrentBaselineLimit
		}
		out.Archives = append(out.Archives, c)
		if c.IndexedMessageID != "" && !seen[c.IndexedMessageID] {
			ids = append(ids, c.IndexedMessageID)
			seen[c.IndexedMessageID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return out, err
	}
	// Include terminal rows solely as command associations, not live risks. The
	// assembler checks every child and immutable intent against original bytes.
	rows, err = tx.QueryContext(ctx, `SELECT order_pk::text,jsonb_build_object(
'record_id','ledger-order:'||order_pk::text,'account_id',account_id,
'gateway_order_id',gateway_order_id,'origin_message_id',origin_message_id,
'trade_date',to_char(trade_date,'YYYYMMDD'),'symbol',symbol,'exchange',exchange,
'trade_side',trade_side,'business_type',business_type,'order_qty',order_qty,
'limit_price',limit_price::text,'status',status,'is_terminal',is_terminal,
'cum_filled_qty',cum_filled_qty,'source_updated_at',updated_at)::text
FROM orders WHERE account_id=$1 AND origin_message_id=ANY($2::text[])
ORDER BY orders.order_pk LIMIT $3`, account, ids, currentBaselineMaxRows+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, body string
		if err = rows.Scan(&key, &body); err != nil {
			return nil, err
		}
		*budget += len(body)
		if len(out.Orders) >= currentBaselineMaxRows || *budget > currentBaselineMaxBytes {
			return nil, ErrCurrentBaselineLimit
		}
		out.Orders = append(out.Orders, CurrentBaselineRecord{Key: "ledger-order:" + key, Document: json.RawMessage(body)})
	}
	return out, rows.Err()
}
