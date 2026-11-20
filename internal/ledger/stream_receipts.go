package ledger

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrLedgerCaptureBinding  = errors.New("ledger capture source binding mismatch")
	ErrLedgerCaptureOwner    = errors.New("ledger capture owner retired")
	ErrLedgerCaptureCursor   = errors.New("ledger capture cursor changed")
	ErrLedgerCaptureBlocked  = errors.New("ledger capture application unresolved")
	ErrLedgerCaptureConflict = errors.New("ledger capture attempt conflict")
)

// SourceID is configured deployment identity; ServerRunID is an observed Redis
// process identity. Neither proves that an individual Stream was never replaced.
type LedgerStreamBinding struct {
	Environment string `json:"environment"`
	AccountID   string `json:"account_id"`
	BrokerID    string `json:"broker_id"`
	GatewayID   string `json:"gateway_id"`
	Role        string `json:"role"`
	SourceID    string `json:"source_id"`
	ServerRunID string `json:"server_run_id"`
	RedisDB     int    `json:"redis_db"`
}

func (b LedgerStreamBinding) Stream() (string, error) {
	if b.Environment != "test" && b.Environment != "production" {
		return "", ErrLedgerCaptureBinding
	}
	if b.BrokerID != "huaxin" || b.RedisDB < 0 || b.RedisDB > 1023 {
		return "", ErrLedgerCaptureBinding
	}
	for _, v := range []string{b.AccountID, b.GatewayID, b.SourceID} {
		if !validBaselineText(v) || strings.ContainsAny(v, ":\r\n\t ") {
			return "", ErrLedgerCaptureBinding
		}
	}
	if len(b.ServerRunID) != 40 {
		return "", ErrLedgerCaptureBinding
	}
	raw, e := hex.DecodeString(b.ServerRunID)
	if e != nil || hex.EncodeToString(raw) != b.ServerRunID {
		return "", ErrLedgerCaptureBinding
	}
	switch b.Role {
	case "reply", "event", "hb", "dlq":
	default:
		return "", ErrLedgerCaptureBinding
	}
	env := b.Environment
	if env == "production" {
		env = "prod"
	}
	return fmt.Sprintf("relay:%s:v1:%s:%s:%s", env, b.BrokerID, b.GatewayID, b.Role), nil
}

type LedgerStreamHandle struct {
	binding       LedgerStreamBinding
	stream, owner string
}

func (h LedgerStreamHandle) Binding() LedgerStreamBinding { return h.binding }
func (h LedgerStreamHandle) Stream() string               { return h.stream }
func (h LedgerStreamHandle) Owner() string                { return h.owner }

type LedgerStreamInput struct {
	EntryID string `json:"entry_id"`
	Body    []byte `json:"body"`
}

type LedgerCaptureStatus struct {
	Cursor        string `json:"cursor"`
	CaptureEpoch  string `json:"capture_epoch"`
	InitialCursor string `json:"initial_cursor"`
	BlockedReason string `json:"blocked_reason"`
	FaultCount    int64  `json:"fault_count"`
	BatchCount    int64  `json:"batch_count"`
	EntryCount    int64  `json:"entry_count"`
	BodyBytes     int64  `json:"body_bytes"`
	EvidenceBytes int64  `json:"evidence_bytes"`
	AnchorSHA256  string `json:"anchor_sha256"`
}

type LedgerReadReceipt struct {
	AttemptID         string              `json:"attempt_id"`
	StartCursor       string              `json:"start_cursor"`
	EndCursor         string              `json:"end_cursor"`
	Outcome           string              `json:"outcome"`
	ErrorCode         string              `json:"error_code"`
	Evidence          json.RawMessage     `json:"evidence"`
	ApplicationReport json.RawMessage     `json:"application_report"`
	Entries           []LedgerStreamInput `json:"entries"`
	Replayed          bool                `json:"replayed"`
}

type LedgerReadApply func(context.Context, *Repository, []LedgerStreamInput) (json.RawMessage, error)

type ledgerReceiptDB interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type LedgerReceiptStore struct{ db ledgerReceiptDB }

func NewLedgerReceiptStore(db *sql.DB) (*LedgerReceiptStore, error) {
	if db == nil {
		return nil, ErrInvalidLedgerInput
	}
	return &LedgerReceiptStore{db}, nil
}

func captureNonce() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Open is explicit first registration, not startup discovery or migration of a
// legacy checkpoint. Existing bindings are never reset, even when Redis is empty.
func (s *LedgerReceiptStore) Open(ctx context.Context, b LedgerStreamBinding, initial string) (LedgerStreamHandle, error) {
	key, err := b.Stream()
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	if initial == "0" {
		initial = "0-0"
	}
	if _, err = CompareCurrentBaselineStreamIDs(initial, "0-0"); err != nil {
		return LedgerStreamHandle{}, err
	}
	owner, err := captureNonce()
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	epoch, err := captureNonce()
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	raw, _ := json.Marshal(b)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	defer tx.Rollback()
	if err = lockLedgerStreamTransition(ctx, tx, key); err != nil {
		return LedgerStreamHandle{}, err
	}
	boundary, err := reviewLedgerStreamBoundary(ctx, tx, key, b.Role, initial)
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	if len(boundary) > 1<<20 {
		return LedgerStreamHandle{}, ErrCurrentBaselineLimit
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO ledger_stream_receivers(stream_key,account_id,binding,capture_epoch,owner_id,initial_cursor,cursor_id)
VALUES($1,$2,$3,$4,$5,$6,$6) ON CONFLICT DO NOTHING`, key, b.AccountID, raw, epoch, owner, initial)
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	if n != 1 {
		return LedgerStreamHandle{}, ErrLedgerCaptureBinding
	}
	// Sequence zero records the explicit local boundary, not a Redis read or
	// proof that the legacy worker completely processed its historical inputs.
	if _, err = tx.ExecContext(ctx, `INSERT INTO ledger_stream_read_batches(stream_key,attempt_id,batch_sequence,owner_id,start_cursor,end_cursor,input_sha256,evidence,evidence_sha256,application_report,application_report_sha256,outcome,error_code,entry_count)
VALUES($1,'bootstrap-v1',0,$2,$3,$3,$4,$5,$4,'{}',$6,'applied','',0)`, key, owner, initial, baselineHash(boundary), string(boundary), baselineHash([]byte(`{}`))); err != nil {
		return LedgerStreamHandle{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ledger_stream_receivers SET evidence_bytes=$2 WHERE stream_key=$1`, key, len(boundary)+2); err != nil {
		return LedgerStreamHandle{}, err
	}
	if err = tx.Commit(); err != nil {
		return LedgerStreamHandle{}, err
	}
	return LedgerStreamHandle{b, key, owner}, nil
}

// Resume requires the exact previous owner/cursor after the old reader was
// stopped. It does not clear application failures or change Redis identity.
func (s *LedgerReceiptStore) Resume(ctx context.Context, b LedgerStreamBinding, previousOwner, cursor string) (LedgerStreamHandle, error) {
	key, err := b.Stream()
	if err != nil {
		return LedgerStreamHandle{}, err
	}
	h := LedgerStreamHandle{b, key, previousOwner}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return h, err
	}
	defer tx.Rollback()
	if err = lockLedgerStreamTransition(ctx, tx, key); err != nil {
		return h, err
	}
	st, err := captureStatus(ctx, tx, h, true)
	if err != nil {
		return h, err
	}
	if st.Cursor != cursor {
		return h, ErrLedgerCaptureCursor
	}
	if st.BlockedReason != "" {
		return h, ErrLedgerCaptureBlocked
	}
	owner, err := captureNonce()
	if err != nil {
		return h, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ledger_stream_receivers SET owner_id=$2,updated_at=now() WHERE stream_key=$1`, key, owner); err != nil {
		return h, err
	}
	if err = tx.Commit(); err != nil {
		return h, err
	}
	h.owner = owner
	return h, nil
}

func captureStatus(ctx context.Context, q currentBaselineReader, h LedgerStreamHandle, lock bool) (LedgerCaptureStatus, error) {
	var out LedgerCaptureStatus
	key, err := h.binding.Stream()
	if err != nil || key != h.stream || h.owner == "" {
		return out, ErrLedgerCaptureBinding
	}
	raw, _ := json.Marshal(h.binding)
	query := `SELECT binding=$2::jsonb,owner_id,cursor_id,capture_epoch,initial_cursor,blocked_reason,fault_count,batch_count,entry_count,body_bytes,evidence_bytes
FROM ledger_stream_receivers WHERE stream_key=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var same bool
	var owner string
	err = q.QueryRowContext(ctx, query, h.stream, raw).Scan(&same, &owner, &out.Cursor, &out.CaptureEpoch, &out.InitialCursor, &out.BlockedReason, &out.FaultCount, &out.BatchCount, &out.EntryCount, &out.BodyBytes, &out.EvidenceBytes)
	if err != nil {
		return out, err
	}
	if !same {
		return out, ErrLedgerCaptureBinding
	}
	if owner != h.owner {
		return out, ErrLedgerCaptureOwner
	}
	if out.Cursor != out.InitialCursor {
		err = q.QueryRowContext(ctx, `SELECT e.body_sha256 FROM ledger_stream_read_entries e JOIN ledger_stream_read_batches b USING(stream_key,attempt_id)
WHERE e.stream_key=$1 AND e.entry_id=$2 AND b.outcome='applied' ORDER BY b.batch_sequence DESC LIMIT 1`, h.stream, out.Cursor).Scan(&out.AnchorSHA256)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *LedgerReceiptStore) Status(ctx context.Context, h LedgerStreamHandle) (LedgerCaptureStatus, error) {
	return captureStatus(ctx, s.db, h, false)
}

func copyLedgerStreamInputs(in []LedgerStreamInput) []LedgerStreamInput {
	out := make([]LedgerStreamInput, len(in))
	for i, r := range in {
		out[i] = LedgerStreamInput{r.EntryID, append([]byte{}, r.Body...)}
	}
	return out
}

// Record commits original bytes, outcome and cursor atomically with the existing
// ledger writer. The savepoint rolls back partial business changes on failure,
// while the immutable receipt remains to explain why the cursor did not advance.
func (s *LedgerReceiptStore) Record(ctx context.Context, h LedgerStreamHandle, attempt, start string, evidence json.RawMessage, inputs []LedgerStreamInput, readError string, apply LedgerReadApply) (LedgerReadReceipt, error) {
	out := LedgerReadReceipt{AttemptID: attempt, StartCursor: start, EndCursor: start, Outcome: "applied", Evidence: bytes.Clone(evidence), ApplicationReport: json.RawMessage(`{}`), Entries: copyLedgerStreamInputs(inputs)}
	inputs, evidence = out.Entries, out.Evidence
	if !validBaselineText(attempt) || attempt == "bootstrap-v1" || len(inputs) > 256 || len(evidence) > 1<<20 || len(readError) > 256 || ValidateCurrentBaselineSourceJSON(evidence) != nil {
		return out, ErrInvalidLedgerInput
	}
	if _, err := CompareCurrentBaselineStreamIDs(start, "0-0"); err != nil {
		return out, err
	}
	size := 0
	last := start
	for _, r := range inputs {
		cmp, e := CompareCurrentBaselineStreamIDs(r.EntryID, last)
		if e != nil || cmp <= 0 {
			return out, ErrLedgerCaptureCursor
		}
		size += len(r.Body)
		last = r.EntryID
	}
	if size > 4<<20 {
		return out, ErrCurrentBaselineLimit
	}
	key, err := json.Marshal(struct {
		Start     string
		Evidence  json.RawMessage
		Inputs    []LedgerStreamInput
		ReadError string
	}{start, evidence, inputs, readError})
	if err != nil {
		return out, err
	}
	hash := baselineHash(key)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	st, err := captureStatus(ctx, tx, h, true)
	if err != nil {
		return out, err
	}
	var oldHash string
	err = tx.QueryRowContext(ctx, `SELECT input_sha256 FROM ledger_stream_read_batches WHERE stream_key=$1 AND attempt_id=$2`, h.stream, attempt).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			return out, ErrLedgerCaptureConflict
		}
		out, err = readLedgerReceipt(ctx, tx, h.stream, attempt)
		if err != nil {
			return out, err
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if st.Cursor != start {
		return out, ErrLedgerCaptureCursor
	}
	if st.BlockedReason != "" {
		return out, ErrLedgerCaptureBlocked
	}
	if st.BatchCount >= 100000 || st.EntryCount+int64(len(inputs)) > 100000 || st.BodyBytes+st.EvidenceBytes+int64(size+len(evidence)) > 64<<20 {
		return out, ErrCurrentBaselineLimit
	}
	blocked := ""
	fault := int64(0)
	for _, r := range inputs {
		var conflict bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ledger_stream_read_entries WHERE stream_key=$1 AND entry_id=$2 AND body_sha256<>$3)`, h.stream, r.EntryID, baselineHash(r.Body)).Scan(&conflict); err != nil {
			return out, err
		}
		if conflict {
			readError = "retained_entry_conflict"
			blocked = attempt
		}
	}
	if readError != "" {
		out.Outcome = "read_failed"
		out.ErrorCode = readError
		fault = 1
	} else if len(inputs) > 0 {
		if apply == nil {
			return out, ErrInvalidLedgerInput
		}
		if _, err = tx.ExecContext(ctx, `SAVEPOINT ledger_read_application`); err != nil {
			return out, err
		}
		report, appErr := apply(ctx, NewRepository(tx), copyLedgerStreamInputs(inputs))
		if len(report) == 0 {
			report = json.RawMessage(`{}`)
		}
		if len(report) > 1<<20 || ValidateCurrentBaselineSourceJSON(report) != nil {
			report = json.RawMessage(`{"report_unavailable":true}`)
			appErr = ErrInvalidLedgerInput
		}
		out.ApplicationReport = bytes.Clone(report)
		if appErr != nil {
			if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT ledger_read_application`); err != nil {
				return out, err
			}
			out.Outcome = "application_failed"
			out.ErrorCode = "ledger_application_failed"
			blocked = attempt
			fault = 1
		} else {
			out.EndCursor = last
		}
		if _, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT ledger_read_application`); err != nil {
			return out, err
		}
	}
	evidenceBytes := len(evidence) + len(out.ApplicationReport)
	if st.BodyBytes+st.EvidenceBytes+int64(size+evidenceBytes) > 64<<20 {
		return out, ErrCurrentBaselineLimit
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ledger_stream_read_batches(stream_key,attempt_id,batch_sequence,owner_id,start_cursor,end_cursor,input_sha256,evidence,evidence_sha256,application_report,application_report_sha256,outcome,error_code,entry_count)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, h.stream, attempt, st.BatchCount+1, h.owner, start, out.EndCursor, hash, string(evidence), baselineHash(evidence), string(out.ApplicationReport), baselineHash(out.ApplicationReport), out.Outcome, out.ErrorCode, len(inputs)); err != nil {
		return out, err
	}
	for i, r := range inputs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO ledger_stream_read_entries(stream_key,attempt_id,ordinal,entry_id,body,body_sha256) VALUES($1,$2,$3,$4,$5,$6)`, h.stream, attempt, i, r.EntryID, r.Body, baselineHash(r.Body)); err != nil {
			return out, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE ledger_stream_receivers SET cursor_id=$2,blocked_reason=$3,fault_count=fault_count+$4,
batch_count=batch_count+1,entry_count=entry_count+$5,body_bytes=body_bytes+$6,evidence_bytes=evidence_bytes+$7,updated_at=now() WHERE stream_key=$1`, h.stream, out.EndCursor, blocked, fault, len(inputs), size, evidenceBytes)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

func readLedgerReceipt(ctx context.Context, q *sql.Tx, key, attempt string) (LedgerReadReceipt, error) {
	out := LedgerReadReceipt{AttemptID: attempt, Entries: []LedgerStreamInput{}}
	var evidence, hash, report, reportHash string
	var count int
	err := q.QueryRowContext(ctx, `SELECT start_cursor,end_cursor,evidence,evidence_sha256,application_report,application_report_sha256,outcome,error_code,entry_count FROM ledger_stream_read_batches WHERE stream_key=$1 AND attempt_id=$2`, key, attempt).Scan(&out.StartCursor, &out.EndCursor, &evidence, &hash, &report, &reportHash, &out.Outcome, &out.ErrorCode, &count)
	if err != nil {
		return out, err
	}
	if count < 0 || count > 256 || len(evidence) > 1<<20 || len(report) > 1<<20 || baselineHash([]byte(evidence)) != hash || baselineHash([]byte(report)) != reportHash {
		return out, ErrCurrentBaselineCorrupt
	}
	out.Evidence = json.RawMessage(evidence)
	out.ApplicationReport = json.RawMessage(report)
	rows, err := q.QueryContext(ctx, `SELECT ordinal,entry_id,body,body_sha256 FROM ledger_stream_read_entries WHERE stream_key=$1 AND attempt_id=$2 ORDER BY ordinal`, key, attempt)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var r LedgerStreamInput
		var ordinal int
		var sha string
		if err = rows.Scan(&ordinal, &r.EntryID, &r.Body, &sha); err != nil {
			return out, err
		}
		size += len(r.Body)
		if ordinal != len(out.Entries) || ordinal >= count || size > 4<<20 || baselineHash(r.Body) != sha {
			return out, ErrCurrentBaselineCorrupt
		}
		out.Entries = append(out.Entries, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Entries) != count {
		return out, ErrCurrentBaselineCorrupt
	}
	return out, nil
}

func (s *LedgerReceiptStore) Receipt(ctx context.Context, h LedgerStreamHandle, attempt string) (LedgerReadReceipt, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return LedgerReadReceipt{}, err
	}
	defer tx.Rollback()
	if _, err = captureStatus(ctx, tx, h, false); err != nil {
		return LedgerReadReceipt{}, err
	}
	out, err := readLedgerReceipt(ctx, tx, h.stream, attempt)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
