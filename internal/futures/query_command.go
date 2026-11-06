package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// QueryCommandBinding is internal persisted context, not a new wire field.
type QueryCommandBinding struct {
	CommandID                                int64
	RequestID, CorrelationID, IdempotencyKey string
}

func (c QueryContext) commandScope() CommandScope {
	return CommandScope{c.Route, c.NativeBrokerID, c.NativeInvestorID, c.NativeUserID}
}

func validQueryCommandContext(c QueryContext) bool {
	b := c.Command
	return b != nil && b.CommandID > 0 && c.commandScope().validate() == nil &&
		validID(b.RequestID) && len(b.RequestID) <= 128 && validID(b.CorrelationID) && len(b.CorrelationID) <= 128 && validID(b.IdempotencyKey) && len(b.IdempotencyKey) <= 128
}

func queryReplyStream(r QueryRoute) string {
	return fmt.Sprintf("relay:%s:v1:%s:%s:reply", r.Env, r.BrokerID, r.GatewayID)
}

func queryBinding(command CommandRegistration) QueryCommandBinding {
	c := command.Original
	return QueryCommandBinding{command.CommandID, c.RequestID, c.CorrelationID, c.IdempotencyKey}
}

// RegisterCommand binds one durable query command to one immutable OC request.
// OC ticket/session/generation/day must be independently verified by the caller;
// they cannot be learned from an arbitrary first reply. Alias origins are rejected
// rather than rewriting incoming audit. A restart never reissues the old command.
func (s *QueryStore) RegisterCommand(ctx context.Context, id string, commandID int64, c QueryContext, timeout time.Duration, limits BatchLimits) error {
	if commandID <= 0 || c.Command != nil || c.commandScope().validate() != nil {
		return ErrBatchIdentity
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := string(commandRouteBytes(c.Route))
	var found int64
	if err = tx.QueryRowContext(ctx, `SELECT command_id FROM futures_commands WHERE route_key=$1 AND command_id=$2 FOR UPDATE`, key, commandID).Scan(&found); err != nil {
		return err
	}
	command, err := readRegisteredCommand(ctx, tx, key, commandID)
	if err != nil {
		return err
	}
	if command.Scope != c.commandScope() || command.Original.Action != c.Action || queryResultType(c.Action) == "" || command.Original.MessageID != c.OriginMessageID {
		return ErrBatchIdentity
	}
	bind := queryBinding(command)
	c.Command = &bind
	var old string
	err = tx.QueryRowContext(ctx, `SELECT batch_id FROM futures_query_commands WHERE command_id=$1`, commandID).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && old != id {
		return ErrBatchIdentity
	}
	// Do not upgrade an already populated unbound prototype into trusted evidence.
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_query_batches WHERE batch_id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrBatchIdentity
		}
	}
	if err = s.registerTx(ctx, tx, id, c, timeout, limits); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO futures_query_commands(batch_id,command_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, commandID); err != nil {
		return err
	}
	if err = verifyQueryCommand(ctx, tx, id, c); err != nil {
		return err
	}
	return tx.Commit()
}

func verifyQueryCommand(ctx context.Context, tx *sql.Tx, id string, c QueryContext) error {
	var commandID int64
	err := tx.QueryRowContext(ctx, `SELECT command_id FROM futures_query_commands WHERE batch_id=$1`, id).Scan(&commandID)
	if errors.Is(err, sql.ErrNoRows) && c.Command == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if !validQueryCommandContext(c) || c.Command.CommandID != commandID {
		return ErrBatchIdentity
	}
	command, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(c.Route)), commandID)
	if err != nil {
		return err
	}
	if command.Scope != c.commandScope() || command.Original.Action != c.Action || command.Original.MessageID != c.OriginMessageID || queryBinding(command) != *c.Command {
		return ErrBatchIdentity
	}
	return nil
}

func (s *QueryStore) verifySnapshotCommand(ctx context.Context, id string, c QueryContext) error {
	tx, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT context_json FROM futures_query_batches WHERE batch_id=$1`, id).Scan(&raw); err != nil {
		return err
	}
	var saved QueryContext
	if err = json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	if !reflect.DeepEqual(c, saved) {
		return ErrBatchIdentity
	}
	return verifyQueryCommand(ctx, tx, id, c)
}
