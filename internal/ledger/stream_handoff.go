package ledger

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type LedgerStreamRegistration struct {
	Binding LedgerStreamBinding `json:"binding"`
	Owner   string              `json:"owner"`
}

type LedgerHandoffInput struct {
	Binding       LedgerStreamBinding `json:"binding"`
	Cursor        string              `json:"cursor"`
	PreviousOwner string              `json:"previous_owner,omitempty"`
}

type LedgerHandoffWitness struct {
	AnchorSHA256 string          `json:"anchor_sha256"`
	Evidence     json.RawMessage `json:"evidence"`
}

// Implementations only observe Redis; no XADD, ACK, group or key mutation.
type LedgerHandoffProbe func(context.Context, LedgerStreamBinding, string) (LedgerHandoffWitness, error)

type LedgerHandoffEntry struct {
	Input  LedgerHandoffInput   `json:"input"`
	State  json.RawMessage      `json:"state"`
	Source LedgerHandoffWitness `json:"source"`
}

type LedgerHandoffPlan struct {
	Version       string               `json:"version"`
	HandoffID     string               `json:"handoff_id"`
	Action        string               `json:"action"`
	Database      string               `json:"database"`
	Schema        string               `json:"schema"`
	RevisionEpoch string               `json:"revision_epoch"`
	PreparedAt    time.Time            `json:"prepared_at"`
	Entries       []LedgerHandoffEntry `json:"entries"`
}

type LedgerHandoffReceipt struct {
	HandoffID     string                     `json:"handoff_id"`
	PlanSHA256    string                     `json:"plan_sha256"`
	Registrations []LedgerStreamRegistration `json:"registrations"`
	Sources       []LedgerHandoffWitness     `json:"sources"`
	CommittedAt   time.Time                  `json:"committed_at"`
	Replayed      bool                       `json:"replayed"`
}

func validHandoffToken(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func handoffInputs(action string, inputs []LedgerHandoffInput) ([]LedgerHandoffInput, error) {
	if (action != "register" && action != "takeover") || len(inputs) == 0 || len(inputs) > 128 {
		return nil, ErrInvalidLedgerInput
	}
	inputs = append([]LedgerHandoffInput(nil), inputs...)
	sort.Slice(inputs, func(i, j int) bool {
		a, _ := inputs[i].Binding.Stream()
		b, _ := inputs[j].Binding.Stream()
		return a < b
	})
	previous := ""
	for _, in := range inputs {
		key, err := in.Binding.Stream()
		if err != nil || key == previous || in.Binding.Environment != "test" {
			return nil, ErrLedgerCaptureBinding
		}
		if _, err = CompareCurrentBaselineStreamIDs(in.Cursor, "0-0"); err != nil || in.Cursor == "0" {
			return nil, ErrLedgerCaptureCursor
		}
		if (action == "register" && in.PreviousOwner != "") || (action == "takeover" && !validHandoffToken(in.PreviousOwner)) {
			return nil, ErrLedgerCaptureOwner
		}
		previous = key
	}
	return inputs, nil
}

func handoffDatabase(ctx context.Context, tx *sql.Tx) (string, string, string, error) {
	var name, schema, epoch string
	var protected int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid=to_regclass('ledger_stream_handoffs')
AND ((tgname='ledger_stream_handoffs_immutable' AND tgtype=27) OR (tgname='ledger_stream_handoffs_no_truncate' AND tgtype=34))
AND tgenabled IN ('O','A')`).Scan(&protected); err != nil {
		return "", "", "", err
	}
	if protected != 2 {
		return "", "", "", ErrCurrentBaselineTracking
	}
	err := tx.QueryRowContext(ctx, `SELECT current_database(),current_schema(),epoch FROM account_baseline_revision_epoch WHERE singleton`).Scan(&name, &schema, &epoch)
	return name, schema, epoch, err
}

func handoffState(ctx context.Context, tx *sql.Tx, action string, in LedgerHandoffInput, lock bool) (json.RawMessage, string, error) {
	key, _ := in.Binding.Stream()
	if action == "register" {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ledger_stream_receivers WHERE stream_key=$1)`, key).Scan(&exists); err != nil {
			return nil, "", err
		}
		if exists {
			return nil, "", ErrLedgerCaptureBinding
		}
		state, err := reviewLedgerStreamBoundary(ctx, tx, key, in.Binding.Role, in.Cursor)
		if len(state) > 1<<20 {
			return nil, "", ErrCurrentBaselineLimit
		}
		return state, "", err
	}
	h := LedgerStreamHandle{in.Binding, key, in.PreviousOwner}
	st, err := captureStatus(ctx, tx, h, lock)
	if err != nil {
		return nil, "", err
	}
	if st.Cursor != in.Cursor {
		return nil, "", ErrLedgerCaptureCursor
	}
	if st.BlockedReason != "" {
		return nil, "", ErrLedgerCaptureBlocked
	}
	if _, err = readLedgerReceipt(ctx, tx, key, "bootstrap-v1"); err != nil {
		return nil, "", err
	}
	state, err := json.Marshal(st)
	return state, st.AnchorSHA256, err
}

func handoffWitness(ctx context.Context, probe LedgerHandoffProbe, in LedgerHandoffInput, anchor string) (LedgerHandoffWitness, error) {
	if probe == nil {
		return LedgerHandoffWitness{}, ErrInvalidLedgerInput
	}
	w, err := probe(ctx, in.Binding, in.Cursor)
	if err != nil {
		return w, err
	}
	if len(w.Evidence) > 1<<20 || ValidateCurrentBaselineSourceJSON(w.Evidence) != nil {
		return w, ErrInvalidLedgerInput
	}
	if in.Cursor != "0-0" && !validHandoffToken(w.AnchorSHA256) {
		return w, ErrLedgerCaptureCursor
	}
	if anchor != "" && anchor != w.AnchorSHA256 {
		return w, ErrLedgerCaptureConflict
	}
	return w, nil
}

// Prepare is a read-only DB transaction. Locks only reject competing writers;
// absence of a lock cannot prove that a pre-guard binary was stopped.
func (s *LedgerReceiptStore) PrepareHandoff(ctx context.Context, action string, inputs []LedgerHandoffInput, probe LedgerHandoffProbe) (LedgerHandoffPlan, error) {
	var p LedgerHandoffPlan
	inputs, err := handoffInputs(action, inputs)
	if err != nil {
		return p, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	p.Version = "ledger.receipt-handoff.v1"
	p.Action = action
	p.HandoffID, err = captureNonce()
	if err != nil {
		return p, err
	}
	p.Database, p.Schema, p.RevisionEpoch, err = handoffDatabase(ctx, tx)
	if err != nil {
		return p, err
	}
	p.PreparedAt = time.Now().UTC()
	for _, in := range inputs {
		key, _ := in.Binding.Stream()
		if err = lockLedgerStreamTransition(ctx, tx, key); err != nil {
			return p, err
		}
		state, anchor, e := handoffState(ctx, tx, action, in, false)
		if e != nil {
			return p, e
		}
		w, e := handoffWitness(ctx, probe, in, anchor)
		if e != nil {
			return p, e
		}
		p.Entries = append(p.Entries, LedgerHandoffEntry{in, state, w})
	}
	if _, err = handoffPlanBytes(p); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func handoffPlanBytes(p LedgerHandoffPlan) ([]byte, error) {
	if p.Version != "ledger.receipt-handoff.v1" || !validHandoffToken(p.HandoffID) || p.PreparedAt.IsZero() || p.Database == "" || p.Schema == "" || p.RevisionEpoch == "" {
		return nil, ErrInvalidLedgerInput
	}
	inputs := make([]LedgerHandoffInput, len(p.Entries))
	for i, e := range p.Entries {
		inputs[i] = e.Input
		if len(e.State) > 1<<20 || ValidateCurrentBaselineSourceJSON(e.State) != nil || len(e.Source.Evidence) > 1<<20 || ValidateCurrentBaselineSourceJSON(e.Source.Evidence) != nil {
			return nil, ErrInvalidLedgerInput
		}
	}
	ordered, err := handoffInputs(p.Action, inputs)
	if err != nil {
		return nil, err
	}
	for i := range inputs {
		if inputs[i] != ordered[i] {
			return nil, ErrInvalidLedgerInput
		}
	}
	raw, err := json.Marshal(p)
	if len(raw) > 4<<20 {
		return nil, ErrCurrentBaselineLimit
	}
	return raw, err
}

func LedgerHandoffDigest(p LedgerHandoffPlan) (string, error) {
	raw, err := handoffPlanBytes(p)
	if err != nil {
		return "", err
	}
	return baselineHash(raw), nil
}

// Apply is all-or-nothing across streams. A saved receipt allows export retry
// after a lost response without rotating owners or replaying business inputs.
func (s *LedgerReceiptStore) ApplyHandoff(ctx context.Context, p LedgerHandoffPlan, expectedSHA string, probe LedgerHandoffProbe) (LedgerHandoffReceipt, error) {
	var result LedgerHandoffReceipt
	raw, err := handoffPlanBytes(p)
	if err != nil {
		return result, err
	}
	digest := baselineHash(raw)
	if digest != expectedSHA {
		return result, ErrLedgerCaptureConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	name, schema, epoch, err := handoffDatabase(ctx, tx)
	if err != nil {
		return result, err
	}
	if name != p.Database || schema != p.Schema || epoch != p.RevisionEpoch {
		return result, ErrLedgerCaptureBinding
	}
	for _, e := range p.Entries {
		key, _ := e.Input.Binding.Stream()
		if err = lockLedgerStreamTransition(ctx, tx, key); err != nil {
			return result, err
		}
	}
	if err = lockLedgerStreamTransition(ctx, tx, "handoff:"+p.HandoffID); err != nil {
		return result, err
	}
	var storedPlan, storedSHA, receipt, receiptSHA string
	err = tx.QueryRowContext(ctx, `SELECT plan,plan_sha256,receipt,receipt_sha256 FROM ledger_stream_handoffs WHERE handoff_id=$1`, p.HandoffID).Scan(&storedPlan, &storedSHA, &receipt, &receiptSHA)
	if err == nil {
		if storedSHA != digest || baselineHash([]byte(storedPlan)) != digest || baselineHash([]byte(receipt)) != receiptSHA {
			return result, ErrLedgerCaptureConflict
		}
		if err = json.Unmarshal([]byte(receipt), &result); err != nil {
			return result, ErrCurrentBaselineCorrupt
		}
		if result.HandoffID != p.HandoffID || result.PlanSHA256 != digest || len(result.Registrations) != len(p.Entries) {
			return result, ErrCurrentBaselineCorrupt
		}
		for i, r := range result.Registrations {
			if r.Binding != p.Entries[i].Input.Binding || !validHandoffToken(r.Owner) {
				return result, ErrCurrentBaselineCorrupt
			}
			key, _ := r.Binding.Stream()
			if _, err = captureStatus(ctx, tx, LedgerStreamHandle{r.Binding, key, r.Owner}, false); err != nil {
				return result, err
			}
		}
		result.Replayed = true
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	// Observe every stream before any transition. A failure rolls back all.
	for _, e := range p.Entries {
		state, anchor, err := handoffState(ctx, tx, p.Action, e.Input, true)
		if err != nil {
			return result, err
		}
		compact, err := json.Marshal(e.State)
		if err != nil || baselineHash(state) != baselineHash(compact) {
			return result, ErrLedgerCaptureConflict
		}
		if anchor != "" && anchor != e.Source.AnchorSHA256 {
			return result, ErrLedgerCaptureConflict
		}
		w, err := handoffWitness(ctx, probe, e.Input, e.Source.AnchorSHA256)
		if err != nil {
			return result, err
		}
		result.Sources = append(result.Sources, w)
	}
	for _, e := range p.Entries {
		in := e.Input
		key, _ := in.Binding.Stream()
		owner, err := captureNonce()
		if err != nil {
			return result, err
		}
		if p.Action == "register" {
			epoch, err := captureNonce()
			if err != nil {
				return result, err
			}
			binding, _ := json.Marshal(in.Binding)
			if err = insertLedgerStreamReceiver(ctx, tx, in.Binding, key, binding, epoch, owner, in.Cursor, e.State); err != nil {
				return result, err
			}
		} else if _, err = tx.ExecContext(ctx, `UPDATE ledger_stream_receivers SET owner_id=$2,updated_at=now() WHERE stream_key=$1`, key, owner); err != nil {
			return result, err
		}
		result.Registrations = append(result.Registrations, LedgerStreamRegistration{in.Binding, owner})
	}
	result.HandoffID = p.HandoffID
	result.PlanSHA256 = digest
	result.CommittedAt = time.Now().UTC()
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if len(encoded) > 4<<20 {
		return result, ErrCurrentBaselineLimit
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ledger_stream_handoffs(handoff_id,plan_sha256,plan,receipt,receipt_sha256) VALUES($1,$2,$3,$4,$5)`, p.HandoffID, digest, string(raw), string(encoded), baselineHash(encoded)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
