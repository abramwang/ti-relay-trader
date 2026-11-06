package futures

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Applied is durable fact ingestion, never a current-snapshot/readiness claim.
// Quality must be read separately: late contradictory pages can invalidate it.
type TradeQueryApplication struct {
	BatchID  string
	Applied  bool
	Replayed bool
	RowCount int
}

// ApplyTradeQuery merges complete persisted order/fill pages and the application
// marker in one transaction. Old complete queries may contribute history after
// recovery, but never promote current-session readiness or resume partial pages.
func (s *QueryStore) ApplyTradeQuery(ctx context.Context, id string) (TradeQueryApplication, error) {
	tx, err := s.transaction(ctx)
	if err != nil {
		return TradeQueryApplication{}, err
	}
	defer tx.Rollback()
	stored, err := s.load(ctx, tx, id)
	if err != nil {
		return TradeQueryApplication{}, err
	}
	b := stored.batch
	if b.state != BatchReady || !isTradeQuery(b.context.Action) {
		return TradeQueryApplication{}, fmt.Errorf("complete trade query required")
	}
	result := TradeQueryApplication{BatchID: id, Applied: true}
	err = tx.QueryRowContext(ctx, `SELECT row_count FROM futures_query_trade_applications WHERE batch_id=$1`, id).Scan(&result.RowCount)
	if err == nil {
		result.Replayed = true
		if err := tx.Commit(); err != nil {
			return TradeQueryApplication{}, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TradeQueryApplication{}, err
	}
	if b.rowCount > maxTradeQueryRows {
		return TradeQueryApplication{}, ErrAuditCapacity
	}
	observations := make([]tradeObservation, 0, b.rowCount)
	events := make([]TradeEvent, 0, b.rowCount)
	kind := "order"
	if b.context.Action == "fill.list.query" {
		kind = "fill"
	}
	for page := 0; page <= b.finalIndex; page++ {
		for index, raw := range b.pages[page].items {
			if b.context.Command != nil {
				var event TradeEvent
				if kind == "order" {
					row, err := DecodeOrder(raw, b.context.tradeScope())
					if err != nil {
						return TradeQueryApplication{}, err
					}
					event.Order = &row
				} else {
					row, err := DecodeFill(raw, b.context.tradeScope())
					if err != nil {
						return TradeQueryApplication{}, err
					}
					event.Fill = &row
				}
				events = append(events, event)
			}
			source, _ := json.Marshal([]any{id, page, index})
			digest := sha256.Sum256(source)
			observations = append(observations, tradeObservation{"ctp-query:" + hex.EncodeToString(digest[:]), kind, raw})
		}
	}
	if b.context.Command != nil {
		if err := authorizeTradeQuery(ctx, tx, b.context, events); err != nil {
			return TradeQueryApplication{}, err
		}
	}
	if _, err := observeTradesTx(ctx, tx, b.context.tradeScope(), observations); err != nil {
		return TradeQueryApplication{}, err
	}
	result.RowCount = len(observations)
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_query_trade_applications(batch_id,scope_key,row_count) VALUES($1,$2,$3)`, id, tradeScopeKey(b.context.tradeScope()), result.RowCount); err != nil {
		return TradeQueryApplication{}, err
	}
	if err := tx.Commit(); err != nil {
		return TradeQueryApplication{}, err
	}
	return result, nil
}

// Batch-local cache only: a new transaction rechecks registrations and hashes.
// Missing local identities never become external merely because lookup is empty.
func authorizeTradeQuery(ctx context.Context, tx *sql.Tx, c QueryContext, events []TradeEvent) error {
	gateways := make([]string, 0, len(events))
	seen := map[string]bool{}
	for _, event := range events {
		g := tradeEventGateway(event)
		if !seen[g] {
			gateways = append(gateways, g)
			seen[g] = true
		}
	}
	key := string(commandRouteBytes(c.Route))
	rows, err := tx.QueryContext(ctx, `SELECT identity,command_id FROM futures_command_reservations WHERE route_key=$1 AND trade_date=$2 AND kind='gateway' AND identity=ANY($3::text[])`, key, c.TradeDate, gateways)
	if err != nil {
		return err
	}
	ids := map[string]int64{}
	for rows.Next() {
		var gateway string
		var id int64
		if err = rows.Scan(&gateway, &id); err != nil {
			rows.Close()
			return err
		}
		ids[gateway] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	commands := map[int64]*CommandRegistration{}
	for _, id := range ids {
		if commands[id] != nil {
			continue
		}
		command, err := readRegisteredCommand(ctx, tx, key, id)
		if err != nil {
			return err
		}
		commands[id] = &command
	}
	for _, event := range events {
		state, reason, err := authorizeResolvedTrade(c.tradeScope(), c.NativeUserID, event, false, commands[ids[tradeEventGateway(event)]])
		if err != nil {
			return err
		}
		if state != "applied" {
			return fmt.Errorf("query trade identity %s: %s", state, reason)
		}
	}
	return nil
}

// ListUnappliedTradeQueries supports restart between durable assembly and
// application. It does not resume interrupted/incomplete OC queries.
func (s *QueryStore) ListUnappliedTradeQueries(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("limit must be 1..256")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.batch_id FROM futures_query_batches b
        LEFT JOIN futures_query_trade_applications a USING(batch_id)
        WHERE b.state='ready' AND b.context_json->>'Action' IN ('order.list.query','fill.list.query') AND a.batch_id IS NULL
        ORDER BY b.registered_at,b.batch_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
