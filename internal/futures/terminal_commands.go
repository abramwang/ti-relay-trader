package futures

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrCommandHistoryRequest = errors.New("invalid futures command history request")

// Command history is independent of current execution permissions and heartbeat.
// Publication observations must never be interpreted as broker order outcomes.
type CommandHistoryOptions struct {
	BeforeID int64
	Limit    int
}

func (o CommandHistoryOptions) Validate() error {
	if o.BeforeID < 0 || o.Limit < 1 || o.Limit > 100 {
		return ErrCommandHistoryRequest
	}
	return nil
}

type CommandHistoryItem struct {
	CommandID        int64            `json:"command_id"`
	AccountID        string           `json:"account_id"`
	GatewayID        string           `json:"gateway_id"`
	Action           string           `json:"action"`
	MessageID        string           `json:"message_id"`
	IdempotencyKey   string           `json:"idempotency_key"`
	RegisteredAt     time.Time        `json:"registered_at"`
	Orders           []SubmitIntent   `json:"orders,omitempty"`
	Cancel           *HistoryCancel   `json:"cancel,omitempty"`
	Publication      QueryPublication `json:"publication"`
	BrokerStateKnown bool             `json:"broker_state_known"`
	RetrySafe        bool             `json:"retry_safe"`
	Response         *ReplyResult     `json:"response,omitempty"`
}

type HistoryCancel struct {
	GatewayOrderID string `json:"gateway_order_id"`
	TradeDate      string `json:"trade_date"`
	CancelID       string `json:"cancel_id"`
}

type CommandHistoryPage struct {
	Route        QueryRoute           `json:"route"`
	Items        []CommandHistoryItem `json:"items"`
	NextBeforeID int64                `json:"next_before_id,omitempty"`
}

// Commands reads only the configured account's current route. It includes CLI
// and HTTP intentions even without a publication, receipt or business order.
func (r *TerminalReader) Commands(ctx context.Context, account string, options CommandHistoryOptions) (CommandHistoryPage, error) {
	if !accountPattern.MatchString(account) || options.Validate() != nil {
		return CommandHistoryPage{}, ErrCommandHistoryRequest
	}
	var scope CommandScope
	found := false
	for _, a := range r.manifest.Accounts {
		if a.Scope.Route.AccountID == account {
			scope, found = a.Scope, true
			break
		}
	}
	if !found {
		return CommandHistoryPage{}, ErrOrderRequestMissing
	}
	if scope.validate() != nil || scope.Route.Env != "test" || r.db == nil {
		return CommandHistoryPage{}, ErrStreamIdentity
	}
	// The page and its publication states share one read-only database snapshot;
	// no receiver changes, Redis reads, registrations or publishing are performed.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return CommandHistoryPage{}, err
	}
	defer tx.Rollback()
	key := string(commandRouteBytes(scope.Route))
	rows, err := tx.QueryContext(ctx, `SELECT command_id,registered_at FROM futures_commands
		WHERE route_key=$1 AND stream_suffix='cmd.trade' AND ($2::bigint=0 OR command_id<$2)
		ORDER BY command_id DESC LIMIT $3`, key, options.BeforeID, options.Limit+1)
	if err != nil {
		return CommandHistoryPage{}, err
	}
	type record struct {
		id int64
		at time.Time
	}
	var records []record
	for rows.Next() {
		var row record
		if err := rows.Scan(&row.id, &row.at); err != nil {
			rows.Close()
			return CommandHistoryPage{}, err
		}
		records = append(records, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return CommandHistoryPage{}, err
	}
	out := CommandHistoryPage{Route: scope.Route, Items: make([]CommandHistoryItem, 0, min(len(records), options.Limit))}
	if len(records) > options.Limit {
		records = records[:options.Limit]
		out.NextBeforeID = records[len(records)-1].id
	}
	for _, row := range records {
		reg, err := readRegisteredCommand(ctx, tx, key, row.id)
		if err != nil {
			return CommandHistoryPage{}, err
		}
		if reg.Scope != scope || !oneOf(reg.Original.Action, "order.submit", "order.batch.submit", "order.cancel") {
			return CommandHistoryPage{}, ErrStreamIdentity
		}
		pub, err := readServicePublication(ctx, tx, reg)
		if err != nil {
			return CommandHistoryPage{}, err
		}
		// A GET is not a transport replay and never grants a resend permission.
		pub.Replayed = false
		item := CommandHistoryItem{CommandID: reg.CommandID, AccountID: account,
			GatewayID: scope.Route.GatewayID, Action: reg.Original.Action,
			MessageID: reg.Original.MessageID, IdempotencyKey: reg.Original.IdempotencyKey,
			RegisteredAt: row.at, Orders: reg.Original.Orders, Publication: pub}
		if c := reg.Original.Cancel; c != nil {
			item.Cancel = &HistoryCancel{c.GatewayOrderID, c.TradeDate, c.CancelID}
		}
		if pub.State == "published" {
			var raw []byte
			if err := tx.QueryRowContext(ctx, `SELECT target_json->'target' FROM futures_test_trade_publications WHERE command_id=$1`, reg.CommandID).Scan(&raw); err != nil {
				return CommandHistoryPage{}, err
			}
			target, err := DecodeTestQueryTarget(raw)
			if err != nil {
				return CommandHistoryPage{}, err
			}
			item.Response, _, _, err = readReplyResult(ctx, tx, reg.CommandID, target)
			if err != nil {
				return CommandHistoryPage{}, err
			}
			if item.Response != nil && item.Response.Action != item.Action {
				return CommandHistoryPage{}, ErrBatchIdentity
			}
		}
		out.Items = append(out.Items, item)
	}
	if err := tx.Commit(); err != nil {
		return CommandHistoryPage{}, err
	}
	return out, nil
}
