package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSecuritiesCommandNotFound  = errors.New("registered securities command not found")
	ErrSecuritiesCommandConflict  = errors.New("registered securities command identity conflict")
	ErrSecuritiesCommandUncertain = errors.New("command publication is unconfirmed; reconcile original identity, do not resend")
)

type SecuritiesCommandIntent struct {
	Environment    string `json:"environment"`
	AccountID      string `json:"account_id"`
	StreamKey      string `json:"stream_key"`
	Action         string `json:"action"`
	MessageID      string `json:"message_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Body           string `json:"original_body"`
}

type SecuritiesCommandRecord struct {
	SecuritiesCommandIntent
	ID           string     `json:"command_id"`
	BodySHA256   string     `json:"body_sha256"`
	IntentSHA256 string     `json:"intent_sha256"`
	State        string     `json:"publication_state"`
	StreamID     *string    `json:"stream_id"`
	RegisteredAt time.Time  `json:"registered_at"`
	DispatchedAt *time.Time `json:"dispatched_at"`
	PublishedAt  *time.Time `json:"published_at"`
}

type SecuritiesCommandStore struct {
	db               *sql.DB
	expectedDatabase string
}

// New binaries cannot silently fall back to post-send-only archival after an
// account has registered history. Older binaries still require an operator
// cutover; this read guard is not a multi-writer activation handshake.
func (repo *Repository) CheckLegacySecuritiesPublication(ctx context.Context, account string) error {
	db, ok := repo.exec.(*sql.DB)
	if !ok {
		return ErrInvalidLedgerInput
	}
	installed, err := securitiesCommandTracking(ctx, db)
	if err != nil || !installed {
		return err
	}
	var found bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM securities_command_intents WHERE account_id=$1)`, account).Scan(&found); err != nil {
		return err
	}
	if found {
		return fmt.Errorf("%w: account has registered history; registered publication mode must remain enabled", ErrSecuritiesCommandUncertain)
	}
	return nil
}

func (repo *Repository) SecuritiesCommandStore(expectedDatabase string) (*SecuritiesCommandStore, error) {
	db, ok := repo.exec.(*sql.DB)
	if !ok || expectedDatabase == "" {
		return nil, ErrInvalidLedgerInput
	}
	return &SecuritiesCommandStore{db: db, expectedDatabase: expectedDatabase}, nil
}

var securitiesCommandTables = []string{"securities_command_intents", "securities_command_dispatches", "securities_command_publications"}

// Optional only for old readers with none of migration 58 installed. A partial
// installation or disabled observer/immutable trigger is never silently ignored.
func securitiesCommandTracking(ctx context.Context, q currentBaselineReader) (bool, error) {
	var tables, protected int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM unnest($1::text[]) n(name) WHERE to_regclass(n.name) IS NOT NULL`, securitiesCommandTables).Scan(&tables)
	if err != nil || tables == 0 {
		return false, err
	}
	if tables != 3 {
		return false, ErrCurrentBaselineTracking
	}
	err = q.QueryRowContext(ctx, `SELECT count(*) FROM unnest($1::text[]) n(name)
JOIN pg_class c ON c.oid=to_regclass(n.name) JOIN pg_trigger t ON t.tgrelid=c.oid
JOIN pg_proc p ON p.oid=t.tgfoid AND p.pronamespace=c.relnamespace
WHERE t.tgenabled IN ('O','A') AND (
(t.tgname='account_baseline_revision_watch' AND t.tgtype=29 AND p.proname='observe_account_baseline_revision') OR
(t.tgname='account_baseline_revision_truncate' AND t.tgtype=32 AND p.proname='observe_account_baseline_revision') OR
(t.tgname='securities_command_immutable' AND t.tgtype=27 AND p.proname='reject_account_baseline_mutation') OR
(t.tgname='securities_command_no_truncate' AND t.tgtype=34 AND p.proname='reject_account_baseline_mutation'))`, securitiesCommandTables).Scan(&protected)
	if err != nil {
		return false, err
	}
	if protected != 12 {
		return false, ErrCurrentBaselineTracking
	}
	return true, nil
}

func (s *SecuritiesCommandStore) check(ctx context.Context, q currentBaselineReader) error {
	var database string
	if err := q.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return err
	}
	if database != s.expectedDatabase {
		return ErrInvalidLedgerInput
	}
	ok, err := securitiesCommandTracking(ctx, q)
	if err != nil {
		return err
	}
	if !ok {
		return ErrCurrentBaselineTracking
	}
	return nil
}

func securitiesCommandHashes(in SecuritiesCommandIntent) (string, string, error) {
	if in.Environment != "test" || len(in.Body) > 1<<20 || ValidateCurrentBaselineSourceJSON([]byte(in.Body)) != nil {
		return "", "", ErrInvalidLedgerInput
	}
	for _, v := range []string{in.AccountID, in.MessageID, in.IdempotencyKey} {
		if !validBaselineText(v) {
			return "", "", ErrInvalidLedgerInput
		}
	}
	p := strings.Split(in.StreamKey, ":")
	if len(p) != 6 || p[0] != "relay" || p[1] != "test" || p[2] != "v1" || p[3] != "huaxin" || !validBaselineText(p[4]) || p[5] != "cmd.trade" {
		return "", "", ErrInvalidLedgerInput
	}
	if in.Action != "order.submit" && in.Action != "order.batch.submit" && in.Action != "order.cancel" {
		return "", "", ErrInvalidLedgerInput
	}
	var w struct {
		Protocol       string          `json:"protocol"`
		Kind           string          `json:"message_type"`
		MessageID      string          `json:"message_id"`
		IdempotencyKey string          `json:"idempotency_key"`
		Action         string          `json:"action"`
		SentAt         time.Time       `json:"sent_at"`
		Payload        json.RawMessage `json:"payload"`
	}
	var payload struct {
		AccountID string `json:"account_id"`
	}
	if json.Unmarshal([]byte(in.Body), &w) != nil || json.Unmarshal(w.Payload, &payload) != nil || payload.AccountID != in.AccountID || w.Protocol != "relay.stream.v1" || w.Kind != "command" || w.MessageID != in.MessageID || w.IdempotencyKey != in.IdempotencyKey || w.Action != in.Action || w.SentAt.IsZero() {
		return "", "", ErrInvalidLedgerInput
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, w.Payload); err != nil {
		return "", "", err
	}
	intent, _ := json.Marshal([]string{in.Environment, in.AccountID, in.StreamKey, in.Action, in.IdempotencyKey, compact.String()})
	return baselineHash([]byte(in.Body)), baselineHash(intent), nil
}

const securitiesCommandSelect = `SELECT i.command_id::text,i.environment,i.account_id,i.stream_key,i.action,i.message_id,i.idempotency_key,i.body_text,i.body_sha256,i.intent_sha256,i.registered_at,d.dispatched_at,p.published_at,p.stream_id
FROM securities_command_intents i LEFT JOIN securities_command_dispatches d USING(command_id,account_id)
LEFT JOIN securities_command_publications p USING(command_id,account_id) `

func scanSecuritiesCommand(row interface{ Scan(...any) error }) (SecuritiesCommandRecord, error) {
	var r SecuritiesCommandRecord
	err := row.Scan(&r.ID, &r.Environment, &r.AccountID, &r.StreamKey, &r.Action, &r.MessageID, &r.IdempotencyKey, &r.Body, &r.BodySHA256, &r.IntentSHA256, &r.RegisteredAt, &r.DispatchedAt, &r.PublishedAt, &r.StreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrSecuritiesCommandNotFound
	}
	if err != nil {
		return r, err
	}
	body, intent, err := securitiesCommandHashes(r.SecuritiesCommandIntent)
	if err != nil || body != r.BodySHA256 || intent != r.IntentSHA256 {
		return r, ErrCurrentBaselineCorrupt
	}
	r.State = "registered_not_dispatched"
	if r.DispatchedAt != nil {
		r.State = "dispatch_unknown"
	}
	if r.StreamID != nil {
		if r.DispatchedAt == nil || r.PublishedAt == nil {
			return r, ErrCurrentBaselineCorrupt
		}
		if _, err = CompareCurrentBaselineStreamIDs(*r.StreamID, "0-0"); err != nil || *r.StreamID == "0-0" {
			return r, ErrCurrentBaselineCorrupt
		}
		r.State = "published"
	}
	return r, nil
}

func (s *SecuritiesCommandStore) Get(ctx context.Context, account, action, key string) (SecuritiesCommandRecord, error) {
	if err := s.check(ctx, s.db); err != nil {
		return SecuritiesCommandRecord{}, err
	}
	return scanSecuritiesCommand(s.db.QueryRowContext(ctx, securitiesCommandSelect+`WHERE i.environment='test' AND i.account_id=$1 AND i.action=$2 AND i.idempotency_key=$3`, account, action, key))
}

func (s *SecuritiesCommandStore) GetByMessage(ctx context.Context, account, message string) (SecuritiesCommandRecord, error) {
	if err := s.check(ctx, s.db); err != nil {
		return SecuritiesCommandRecord{}, err
	}
	return scanSecuritiesCommand(s.db.QueryRowContext(ctx, securitiesCommandSelect+`WHERE i.account_id=$1 AND i.message_id=$2`, account, message))
}

func (s *SecuritiesCommandStore) Register(ctx context.Context, in SecuritiesCommandIntent) (SecuritiesCommandRecord, bool, error) {
	body, intent, err := securitiesCommandHashes(in)
	if err != nil {
		return SecuritiesCommandRecord{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SecuritiesCommandRecord{}, false, err
	}
	defer tx.Rollback()
	if err = s.check(ctx, tx); err != nil {
		return SecuritiesCommandRecord{}, false, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO securities_command_intents(environment,account_id,stream_key,action,message_id,idempotency_key,body_text,body_sha256,intent_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING RETURNING command_id::text`, in.Environment, in.AccountID, in.StreamKey, in.Action, in.MessageID, in.IdempotencyKey, in.Body, body, intent).Scan(&id)
	created := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SecuritiesCommandRecord{}, false, err
	}
	r, err := scanSecuritiesCommand(tx.QueryRowContext(ctx, securitiesCommandSelect+`WHERE i.environment=$1 AND i.account_id=$2 AND i.action=$3 AND i.idempotency_key=$4`, in.Environment, in.AccountID, in.Action, in.IdempotencyKey))
	if errors.Is(err, ErrSecuritiesCommandNotFound) {
		err = ErrSecuritiesCommandConflict
	}
	if err != nil {
		return r, false, err
	}
	if r.IntentSHA256 != intent {
		return r, false, ErrSecuritiesCommandConflict
	}
	// A message ID must not alias another intent, even when this idempotency key
	// already has an original message. Do not replace the original envelope.
	var aliases int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM securities_command_intents WHERE message_id=$1 AND command_id<>$2::bigint`, in.MessageID, r.ID).Scan(&aliases); err != nil {
		return r, false, err
	}
	if aliases != 0 {
		return r, false, ErrSecuritiesCommandConflict
	}
	if err = tx.Commit(); err != nil {
		return r, false, err
	}
	return r, created, nil
}

// A successful claim is permission for this invocation to call XADD once, not
// evidence that Redis or the broker received it. Existing claims never resume.
func (s *SecuritiesCommandStore) Claim(ctx context.Context, r SecuritiesCommandRecord) error {
	if err := s.check(ctx, s.db); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO securities_command_dispatches(command_id,account_id)
SELECT command_id,account_id FROM securities_command_intents WHERE command_id=$1::bigint AND account_id=$2 AND body_sha256=$3
ON CONFLICT DO NOTHING`, r.ID, r.AccountID, r.BodySHA256)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSecuritiesCommandUncertain
	}
	return nil
}

func (s *SecuritiesCommandStore) Complete(ctx context.Context, r SecuritiesCommandRecord, streamID string) error {
	if n, err := CompareCurrentBaselineStreamIDs(streamID, "0-0"); err != nil || n <= 0 {
		return ErrInvalidLedgerInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.check(ctx, tx); err != nil {
		return err
	}
	stored, err := scanSecuritiesCommand(tx.QueryRowContext(ctx, securitiesCommandSelect+`WHERE i.command_id=$1::bigint`, r.ID))
	if err != nil {
		return err
	}
	if stored.BodySHA256 != r.BodySHA256 || stored.AccountID != r.AccountID || stored.DispatchedAt == nil {
		return ErrSecuritiesCommandConflict
	}
	if stored.StreamID != nil {
		if *stored.StreamID != streamID {
			return ErrSecuritiesCommandConflict
		}
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO securities_command_publications(command_id,account_id,stream_id) VALUES($1::bigint,$2,$3) ON CONFLICT DO NOTHING`, r.ID, r.AccountID, streamID); err != nil {
		return err
	}
	var actual string
	if err = tx.QueryRowContext(ctx, `SELECT stream_id FROM securities_command_publications WHERE command_id=$1::bigint`, r.ID).Scan(&actual); err != nil {
		return err
	}
	if actual != streamID {
		return ErrSecuritiesCommandConflict
	}
	var w struct {
		RequestID     string `json:"request_id"`
		CorrelationID string `json:"correlation_id"`
		Payload       struct {
			GatewayOrderID string `json:"gateway_order_id"`
		} `json:"payload"`
	}
	if err = json.Unmarshal([]byte(stored.Body), &w); err != nil {
		return err
	}
	// Unlike the legacy upsert, never overwrite an occupied physical archive.
	result, err := tx.ExecContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,message_type,action,account_id,gateway_order_id,origin_message_id,request_id,correlation_id,idempotency_key,body,body_text,received_at)
VALUES($1,$2,'in','cmd.trade','command',$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,clock_timestamp()) ON CONFLICT DO NOTHING`, stored.StreamKey, streamID, stored.Action, stored.AccountID, nullString(w.Payload.GatewayOrderID), stored.MessageID, nullString(w.RequestID), nullString(w.CorrelationID), stored.IdempotencyKey, stored.Body, stored.Body)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: command archive location already occupied", ErrSecuritiesCommandConflict)
	}
	return tx.Commit()
}
