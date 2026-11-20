package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Raw observations retain body_text, not a re-serialization of the JSONB column.
type CurrentBaselineArchive struct {
	ArchiveID  string    `json:"archive_id"`
	Stream     string    `json:"stream"`
	EntryID    string    `json:"entry_id"`
	Direction  string    `json:"direction"`
	Role       string    `json:"role"`
	Body       string    `json:"-"`
	ParseError string    `json:"parse_error"`
	ReceivedAt time.Time `json:"received_at"`
}

type CurrentBaselineQuerySources struct {
	MessageID string
	Commands  []CurrentBaselineArchive
	Replies   []CurrentBaselineArchive
}

type CurrentBaselineSources struct {
	Scope            CurrentBaselineScope
	ReadAt           time.Time
	Queries          map[string]CurrentBaselineQuerySources
	UnresolvedOrders []CurrentBaselineRecord
	RevisionEvidence *CurrentBaselineRevisionEvidence
	SourceHealth     *CurrentBaselineSourceHealth
}

var CurrentBaselineQueryActions = map[string]string{
	"asset": "account.asset.query", "positions": "account.positions.query",
	"orders": "order.list.query", "fills": "fill.list.query",
}

// ReadSources requires the deployment environment from trusted configuration.
// The equity orders table has no env column: never infer its environment from a
// caller's account ID. This reads one local transaction, not a broker snapshot.
func (s *CurrentBaselineStore) ReadSources(ctx context.Context, deploymentEnvironment string, scope CurrentBaselineScope, ids map[string]string) (CurrentBaselineSources, error) {
	out := CurrentBaselineSources{Scope: scope, Queries: map[string]CurrentBaselineQuerySources{}, UnresolvedOrders: []CurrentBaselineRecord{}}
	if scope.validate() != nil || deploymentEnvironment != scope.Environment || len(ids) != 4 {
		return out, ErrCurrentBaselineInput
	}
	seen := map[string]bool{}
	for resource := range CurrentBaselineQueryActions {
		id := ids[resource]
		if !validBaselineText(id) || seen[id] {
			return out, ErrCurrentBaselineInput
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&out.ReadAt); err != nil {
		return out, err
	}
	revision, err := readCurrentBaselineRevision(ctx, tx, scope.AccountID)
	if err != nil {
		return out, err
	}
	out.RevisionEvidence = &revision
	out.SourceHealth, err = readCurrentBaselineHealth(ctx, tx, scope, out.ReadAt)
	if err != nil {
		return out, err
	}
	budget := 0
	for _, resource := range []string{"asset", "positions", "orders", "fills"} {
		q := CurrentBaselineQuerySources{MessageID: ids[resource], Commands: []CurrentBaselineArchive{}, Replies: []CurrentBaselineArchive{}}
		// Do not filter failures, unexpected streams or malformed bodies out of
		// the evidence. The assembler must see and reject them explicitly.
		// The command archiver stores its own ID in origin_message_id as well.
		// Use that indexed identity, not a JSONB scan of the account's history.
		rows, e := tx.QueryContext(ctx, `SELECT raw_message_pk::text,stream_key,stream_id,direction,stream_role,COALESCE(body_text,''),COALESCE(parse_error,''),received_at,COALESCE(body->>'message_id'=$2,false)
FROM raw_stream_messages WHERE account_id=$1 AND origin_message_id=$2
ORDER BY raw_stream_messages.raw_message_pk LIMIT $3`, scope.AccountID, q.MessageID, currentBaselineMaxRows+1)
		if e != nil {
			return out, e
		}
		count := 0
		for rows.Next() {
			var r CurrentBaselineArchive
			var isRequest bool
			if e = rows.Scan(&r.ArchiveID, &r.Stream, &r.EntryID, &r.Direction, &r.Role, &r.Body, &r.ParseError, &r.ReceivedAt, &isRequest); e != nil {
				rows.Close()
				return out, e
			}
			count++
			budget += len(r.Body)
			if count > currentBaselineMaxRows || budget > currentBaselineMaxBytes {
				rows.Close()
				return out, ErrCurrentBaselineLimit
			}
			if isRequest || r.Role == "cmd.query" || r.Role == "cmd.trade" {
				q.Commands = append(q.Commands, r)
			} else {
				q.Replies = append(q.Replies, r)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		out.Queries[resource] = q
	}
	// No trade-date, gateway, or source filter: cross-day and not-yet-published
	// local intents must survive a successful empty current-day broker query.
	rows, err := tx.QueryContext(ctx, `SELECT order_pk::text,jsonb_build_object(
'record_id','ledger-order:'||order_pk::text,'source','ledger_unresolved',
'account_id',account_id,'gateway_order_id',gateway_order_id,'order_stream_id',order_stream_id,
'origin_message_id',origin_message_id,'trade_date',to_char(trade_date,'YYYYMMDD'),
'symbol',symbol,'exchange',exchange,'trade_side',trade_side,'business_type',business_type,
'order_qty',order_qty,'cum_filled_qty',cum_filled_qty,'leaves_qty',leaves_qty,
'status',status,'is_terminal',is_terminal,'limit_price',limit_price::text,
'execution_state','reconciliation_required','source_updated_at',updated_at,
'cash_freeze_inclusion','unknown','sellable_freeze_inclusion','unknown')::text
FROM orders WHERE account_id=$1 AND (NOT is_terminal OR status NOT IN ('filled','cancelled','rejected'))
ORDER BY orders.order_pk LIMIT $2`, scope.AccountID, currentBaselineMaxRows+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, doc string
		if err = rows.Scan(&key, &doc); err != nil {
			return out, err
		}
		budget += len(doc)
		if len(out.UnresolvedOrders) >= currentBaselineMaxRows || budget > currentBaselineMaxBytes {
			return out, ErrCurrentBaselineLimit
		}
		out.UnresolvedOrders = append(out.UnresolvedOrders, CurrentBaselineRecord{Key: "ledger-order:" + key, Document: json.RawMessage(doc)})
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return CurrentBaselineSources{}, fmt.Errorf("current baseline source read: %w", err)
	}
	return out, nil
}
