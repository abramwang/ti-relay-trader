package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func receiptDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, ctx := healthDatabase(t)
	raw, err := os.ReadFile("../../migrations/postgres/000055_ledger_stream_receipts.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	execRevision(t, ctx, db, string(raw))
	execRevision(t, ctx, db, `CREATE TABLE receipt_effects(n integer)`)
	return db, ctx
}

func receiptBinding() LedgerStreamBinding {
	return LedgerStreamBinding{Environment: "test", AccountID: "acct", BrokerID: "huaxin", GatewayID: "gateway", Role: "event", SourceID: "isolated-test", ServerRunID: strings.Repeat("a", 40)}
}

func receiptApply(ctx context.Context, r *Repository, in []LedgerStreamInput) (json.RawMessage, error) {
	_, err := r.exec.ExecContext(ctx, `INSERT INTO receipt_effects(n) VALUES($1)`, len(in))
	return json.RawMessage(`{"applied":true}`), err
}

func TestCurrentAccountBaselineReceiptAtomicity(t *testing.T) {
	db, ctx := receiptDatabase(t)
	s, _ := NewLedgerReceiptStore(db)
	b := receiptBinding()
	h, err := s.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Open(ctx, b, "0-0"); !errors.Is(err, ErrLedgerCaptureBinding) {
		t.Fatal("reset existing binding", err)
	}
	inputs := []LedgerStreamInput{{EntryID: "1-0", Body: []byte(" {\"raw\":true}\n")}}
	evidence := json.RawMessage(`{"before":"observed","continuity_verified":false}`)
	r, err := s.Record(ctx, h, "attempt-1", "0-0", evidence, inputs, "", receiptApply)
	if err != nil || r.EndCursor != "1-0" || r.Outcome != "applied" {
		t.Fatal(r, err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM receipt_effects`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// Same identity after a lost caller response must not reapply business writes.
	r, err = s.Record(ctx, h, "attempt-1", "0-0", evidence, inputs, "", func(context.Context, *Repository, []LedgerStreamInput) (json.RawMessage, error) {
		t.Fatal("replayed business write")
		return nil, nil
	})
	if err != nil || !r.Replayed || !bytes.Equal(r.Entries[0].Body, inputs[0].Body) {
		t.Fatal(r, err)
	}
	if _, err = s.Record(ctx, h, "attempt-1", "0-0", json.RawMessage(`{}`), inputs, "", receiptApply); !errors.Is(err, ErrLedgerCaptureConflict) {
		t.Fatal(err)
	}
	st, err := s.Status(ctx, h)
	if err != nil || st.AnchorSHA256 != baselineHash(inputs[0].Body) {
		t.Fatal(st, err)
	}
	next, err := s.Resume(ctx, b, h.Owner(), "1-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Status(ctx, h); !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal("stale owner", err)
	}
	if _, err = s.Record(ctx, h, "stale-writer", "1-0", evidence, []LedgerStreamInput{{EntryID: "2-0", Body: []byte("old-owner")}}, "", receiptApply); !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal("retired writer can still apply", err)
	}
	if _, err = s.Resume(ctx, b, h.Owner(), "1-0"); !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal("stale takeover", err)
	}
	if _, err = s.Resume(ctx, b, next.Owner(), "0-0"); !errors.Is(err, ErrLedgerCaptureCursor) {
		t.Fatal("rewound cursor", err)
	}
	changed := b
	changed.ServerRunID = strings.Repeat("b", 40)
	if _, err = s.Resume(ctx, changed, next.Owner(), "1-0"); !errors.Is(err, ErrLedgerCaptureBinding) {
		t.Fatal("silent source switch", err)
	}
	read, err := s.Receipt(ctx, next, "attempt-1")
	if err != nil || !bytes.Equal(read.Evidence, evidence) {
		t.Fatal(read, err)
	}
	// A failed projection retains raw binary bytes and rolls back partial facts.
	bad := []LedgerStreamInput{{EntryID: "2-0", Body: []byte{0xff, 0, 0x20}}}
	r, err = s.Record(ctx, next, "bad-application", "1-0", evidence, bad, "", func(ctx context.Context, r *Repository, in []LedgerStreamInput) (json.RawMessage, error) {
		_, e := r.exec.ExecContext(ctx, `INSERT INTO receipt_effects(n) VALUES(99)`)
		if e != nil {
			return nil, e
		}
		return json.RawMessage(`{"error":"fixture"}`), errors.New("fixture failure")
	})
	if err != nil || r.Outcome != "application_failed" || r.EndCursor != "1-0" {
		t.Fatal(r, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM receipt_effects`).Scan(&n); err != nil || n != 1 {
		t.Fatal("partial business write survived", n, err)
	}
	st, err = s.Status(ctx, next)
	if err != nil || st.BlockedReason != "bad-application" || st.FaultCount != 1 {
		t.Fatal(st, err)
	}
	r, err = s.Receipt(ctx, next, "bad-application")
	if err != nil || !bytes.Equal(r.Entries[0].Body, bad[0].Body) {
		t.Fatal("binary evidence changed", r, err)
	}
	if _, err = s.Record(ctx, next, "new-after-failure", "1-0", evidence, bad, "", receiptApply); !errors.Is(err, ErrLedgerCaptureBlocked) {
		t.Fatal(err)
	}
	if _, err = s.Resume(ctx, b, next.Owner(), "1-0"); !errors.Is(err, ErrLedgerCaptureBlocked) {
		t.Fatal("takeover cleared unresolved work", err)
	}
	for _, query := range []string{`DELETE FROM ledger_stream_read_entries`, `UPDATE ledger_stream_read_batches SET outcome='applied'`, `TRUNCATE ledger_stream_read_entries`} {
		if _, err = db.ExecContext(ctx, query); err == nil {
			t.Fatal("immutable evidence changed", query)
		}
	}
}

func TestCurrentAccountBaselineReceiptConcurrentAndReadFailure(t *testing.T) {
	db, ctx := receiptDatabase(t)
	s, _ := NewLedgerReceiptStore(db)
	b := receiptBinding()
	h, err := s.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	inputs := []LedgerStreamInput{{EntryID: "10-0", Body: []byte("first")}}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Record(ctx, h, "same", "0-0", json.RawMessage(`{}`), inputs, "", receiptApply)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM receipt_effects`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate application", n)
	}
	r0 := revisionNow(t, ctx, db, b.AccountID)
	inputs = []LedgerStreamInput{{EntryID: "11-0", Body: []byte("same-read-again")}}
	r, err := s.Record(ctx, h, "read-error", "10-0", json.RawMessage(`{}`), inputs, "source_after_failed", receiptApply)
	if err != nil || r.Outcome != "read_failed" || r.EndCursor != "10-0" {
		t.Fatal(r, err)
	}
	if revisionNow(t, ctx, db, b.AccountID) == r0 {
		t.Fatal("source failure didn't invalidate baselines")
	}
	r, err = s.Record(ctx, h, "read-ok", "10-0", json.RawMessage(`{}`), inputs, "", receiptApply)
	if err != nil || r.EndCursor != "11-0" {
		t.Fatal("read-only retry could not retain a second observation", r, err)
	}
	st, err := s.Status(ctx, h)
	if err != nil || st.FaultCount != 1 || st.EntryCount != 3 {
		t.Fatal("failure history cleared", st, err)
	}
	inputs = []LedgerStreamInput{{EntryID: "12-0", Body: []byte("original")}}
	if _, err = s.Record(ctx, h, "interrupted", "11-0", json.RawMessage(`{}`), inputs, "read_failed", nil); err != nil {
		t.Fatal(err)
	}
	inputs[0].Body = []byte("replaced")
	r, err = s.Record(ctx, h, "changed", "11-0", json.RawMessage(`{}`), inputs, "", receiptApply)
	if err != nil || r.ErrorCode != "retained_entry_conflict" || r.EndCursor != "11-0" {
		t.Fatal(r, err)
	}
}

func TestCurrentAccountBaselineReceiptTransactionFailure(t *testing.T) {
	db, ctx := receiptDatabase(t)
	s, _ := NewLedgerReceiptStore(db)
	h, err := s.Open(ctx, receiptBinding(), "0-0")
	if err != nil {
		t.Fatal(err)
	}
	// Failure after business work but before receipt/cursor commit rolls back all.
	execRevision(t, ctx, db, `ALTER TABLE ledger_stream_read_entries ADD CONSTRAINT fail_fixture CHECK(entry_id<>'1-0')`)
	_, err = s.Record(ctx, h, "failed-commit", "0-0", json.RawMessage(`{}`), []LedgerStreamInput{{EntryID: "1-0", Body: []byte("raw")}}, "", receiptApply)
	if err == nil {
		t.Fatal("fixture insertion unexpectedly passed")
	}
	st, err := s.Status(ctx, h)
	if err != nil || st.BatchCount != 0 || st.Cursor != "0-0" {
		t.Fatal(st, err)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM receipt_effects`).Scan(&n)
	if n != 0 {
		t.Fatal("business write survived receipt failure")
	}
	execRevision(t, ctx, db, `ALTER TABLE ledger_stream_read_entries DROP CONSTRAINT fail_fixture`)
	if _, err = s.Record(ctx, h, "failed-commit", "0-0", json.RawMessage(`{}`), []LedgerStreamInput{{EntryID: "1-0", Body: []byte("raw")}}, "", receiptApply); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentAccountBaselineReceiptHealthAndLimits(t *testing.T) {
	db, ctx := receiptDatabase(t)
	s, _ := NewLedgerReceiptStore(db)
	b := receiptBinding()
	scope := currentBaselineFixture(0).Scope
	scope.AccountID, scope.GatewayID = b.AccountID, b.GatewayID
	h, err := s.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	readHealth := func() *CurrentBaselineSourceHealth {
		t.Helper()
		v, e := readCurrentBaselineHealth(ctx, db, scope, time.Now().UTC())
		if e != nil || !v.ReceiptTrackingAvailable || v.ContinuityVerified || len(v.Receipts) != 4 {
			t.Fatal(v, e)
		}
		if v.Receipts[1].Role != "event" || !v.Receipts[1].Found || !v.Receipts[1].BindingMatched {
			t.Fatal(v.Receipts)
		}
		return v
	}
	if !hasHealthReason(readHealth(), "event_receipt_observation_missing") {
		t.Fatal("unobserved binding treated as collection")
	}
	r0 := revisionNow(t, ctx, db, b.AccountID)
	// Empty body is retained as zero bytes, not SQL NULL; application may fail.
	r, err := s.Record(ctx, h, "empty-body", "0-0", json.RawMessage(`{}`), []LedgerStreamInput{{EntryID: "1-0"}}, "source_after_failed", nil)
	if err != nil || len(r.Entries[0].Body) != 0 {
		t.Fatal(r, err)
	}
	r, err = s.Receipt(ctx, h, "empty-body")
	if err != nil || len(r.Entries) != 1 || len(r.Entries[0].Body) != 0 {
		t.Fatal(r, err)
	}
	v := readHealth()
	if !hasHealthReason(v, "event_receipt_read_failed") || !hasHealthReason(v, "event_receipt_prior_faults_unreconciled") || *v.Receipts[1].FaultCount != "1" || revisionNow(t, ctx, db, b.AccountID) == r0 {
		t.Fatal(v)
	}
	// An ordinary empty successful observation cannot erase a historical fault.
	r0 = revisionNow(t, ctx, db, b.AccountID)
	if _, err = s.Record(ctx, h, "empty-read", "0-0", json.RawMessage(`{}`), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if revisionNow(t, ctx, db, b.AccountID) != r0 || !hasHealthReason(readHealth(), "event_receipt_prior_faults_unreconciled") {
		t.Fatal("ordinary progress changed revision or cleared fault")
	}
	execRevision(t, ctx, db, `UPDATE ledger_stream_receivers SET evidence_bytes=67108864 WHERE stream_key=$1`, h.Stream())
	if _, err = s.Record(ctx, h, "capacity", "0-0", json.RawMessage(`{}`), nil, "", nil); !errors.Is(err, ErrCurrentBaselineLimit) {
		t.Fatal("retention budget ignored", err)
	}
	execRevision(t, ctx, db, `ALTER TABLE ledger_stream_receivers DISABLE TRIGGER account_baseline_receiver_watch`)
	v, err = readCurrentBaselineHealth(ctx, db, scope, time.Now().UTC())
	if err != nil || v.ReceiptTrackingAvailable || !hasHealthReason(v, "receipt_tracking_unavailable") {
		t.Fatal(v, err)
	}
	for _, direction := range []string{"down", "up"} {
		r0 = revisionNow(t, ctx, db, b.AccountID)
		raw, e := os.ReadFile("../../migrations/postgres/000055_ledger_stream_receipts." + direction + ".sql")
		if e != nil {
			t.Fatal(e)
		}
		execRevision(t, ctx, db, string(raw))
		v, err = readCurrentBaselineHealth(ctx, db, scope, time.Now().UTC())
		if err != nil || v.ReceiptTrackingAvailable != (direction == "up") || revisionNow(t, ctx, db, b.AccountID) == r0 {
			t.Fatal("receipt tracking migration", direction, v, err)
		}
	}
}
