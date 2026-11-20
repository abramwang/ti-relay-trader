package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func handoffDatabaseFixture(t *testing.T) (*sql.DB, context.Context) {
	db, ctx := receiptDatabase(t)
	raw, err := os.ReadFile("../../migrations/postgres/000056_ledger_stream_handoffs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	execRevision(t, ctx, db, string(raw))
	return db, ctx
}

func handoffProbeFixture(context.Context, LedgerStreamBinding, string) (LedgerHandoffWitness, error) {
	return LedgerHandoffWitness{AnchorSHA256: strings.Repeat("a", 64), Evidence: json.RawMessage(`{"synthetic_only":true,"continuity_verified":false}`)}, nil
}

func handoffInputsFixture() []LedgerHandoffInput {
	out := []LedgerHandoffInput{}
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		b := receiptBinding()
		b.Role = role
		out = append(out, LedgerHandoffInput{Binding: b, Cursor: "0-0"})
	}
	return out
}

func TestCurrentAccountBaselineHandoffAtomicAndIdempotent(t *testing.T) {
	db, ctx := handoffDatabaseFixture(t)
	s, _ := NewLedgerReceiptStore(db)
	p, err := s.PrepareHandoff(ctx, "register", handoffInputsFixture(), handoffProbeFixture)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_receivers`).Scan(&n); err != nil || n != 0 {
		t.Fatal("plan wrote", n, err)
	}
	sha, err := LedgerHandoffDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	// CLI indent/decode must retain the semantic plan digest.
	raw, _ := json.MarshalIndent(p, "", "  ")
	var decoded LedgerHandoffPlan
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if roundtrip, _ := LedgerHandoffDigest(decoded); roundtrip != sha {
		t.Fatal("digest changed after private file export")
	}
	r, err := s.ApplyHandoff(ctx, decoded, sha, handoffProbeFixture)
	if err != nil || len(r.Registrations) != 4 || r.Replayed {
		t.Fatal(r, err)
	}
	for _, registration := range r.Registrations {
		if _, err = s.Attach(ctx, registration.Binding, registration.Owner); err != nil {
			t.Fatal(err)
		}
	}
	r2, err := s.ApplyHandoff(ctx, p, sha, func(context.Context, LedgerStreamBinding, string) (LedgerHandoffWitness, error) {
		t.Fatal("replay sampled again")
		return LedgerHandoffWitness{}, nil
	})
	if err != nil || !r2.Replayed || !reflect.DeepEqual(r.Registrations, r2.Registrations) {
		t.Fatal(r2, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_handoffs`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, sql := range []string{`DELETE FROM ledger_stream_handoffs`, `UPDATE ledger_stream_handoffs SET plan='{}'`, `TRUNCATE ledger_stream_handoffs`} {
		if _, err = db.ExecContext(ctx, sql); err == nil {
			t.Fatal("mutable handoff audit")
		}
	}
	inputs := []LedgerHandoffInput{}
	for _, reg := range r.Registrations {
		inputs = append(inputs, LedgerHandoffInput{Binding: reg.Binding, Cursor: "0-0", PreviousOwner: reg.Owner})
	}
	next, err := s.PrepareHandoff(ctx, "takeover", inputs, handoffProbeFixture)
	if err != nil {
		t.Fatal(err)
	}
	nextSHA, _ := LedgerHandoffDigest(next)
	r3, err := s.ApplyHandoff(ctx, next, nextSHA, handoffProbeFixture)
	if err != nil {
		t.Fatal(err)
	}
	if r3.Registrations[0].Owner == r.Registrations[0].Owner {
		t.Fatal("owner not rotated")
	}
	if _, err = s.Attach(ctx, r.Registrations[0].Binding, r.Registrations[0].Owner); !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal(err)
	}
	if _, err = s.ApplyHandoff(ctx, p, sha, handoffProbeFixture); !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal("reexport revived retired owner", err)
	}
}

func TestCurrentAccountBaselineHandoffRejectsStaleOrPartial(t *testing.T) {
	for _, fault := range []string{"occupied", "checkpoint_changed", "source_failed", "late_sql", "wrong_digest", "wrong_schema", "wrong_epoch", "source_anchor"} {
		t.Run(fault, func(t *testing.T) {
			db, ctx := handoffDatabaseFixture(t)
			s, _ := NewLedgerReceiptStore(db)
			inputs := handoffInputsFixture()
			p, err := s.PrepareHandoff(ctx, "register", inputs, handoffProbeFixture)
			if err != nil {
				t.Fatal(err)
			}
			probe := LedgerHandoffProbe(handoffProbeFixture)
			repo := NewRepository(db)
			switch fault {
			case "occupied":
				key, _ := inputs[0].Binding.Stream()
				lease, e := repo.AcquireStreamWriter(ctx, []string{key}, false)
				if e != nil {
					t.Fatal(e)
				}
				defer lease.Close()
			case "checkpoint_changed":
				key, _ := inputs[0].Binding.Stream()
				if err = repo.UpsertStreamCheckpoint(ctx, StreamCheckpoint{StreamKey: key, Role: inputs[0].Binding.Role, LastStreamID: "1-0"}); err != nil {
					t.Fatal(err)
				}
			case "source_failed":
				calls := 0
				probe = func(ctx context.Context, b LedgerStreamBinding, c string) (LedgerHandoffWitness, error) {
					calls++
					if calls == 4 {
						return LedgerHandoffWitness{}, errors.New("isolated source failure")
					}
					return handoffProbeFixture(ctx, b, c)
				}
			case "late_sql":
				execRevision(t, ctx, db, `ALTER TABLE ledger_stream_handoffs ADD CONSTRAINT fail_commit CHECK(false)`)
			case "wrong_schema":
				p.Schema = "other"
			case "wrong_epoch":
				p.RevisionEpoch = "other"
			case "source_anchor":
				probe = func(ctx context.Context, b LedgerStreamBinding, c string) (LedgerHandoffWitness, error) {
					w, _ := handoffProbeFixture(ctx, b, c)
					w.AnchorSHA256 = strings.Repeat("b", 64)
					return w, nil
				}
			}
			sha, _ := LedgerHandoffDigest(p)
			if fault == "wrong_digest" {
				sha = strings.Repeat("0", 64)
			}
			if _, err = s.ApplyHandoff(ctx, p, sha, probe); err == nil {
				t.Fatal("invalid handoff applied")
			}
			for _, table := range []string{"ledger_stream_receivers", "ledger_stream_read_batches", "ledger_stream_handoffs"} {
				var n int
				if err = db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
					t.Fatal("partial apply", table, n, err)
				}
			}
		})
	}
}

func TestCurrentAccountBaselineHandoffTakeoverFencesInFlightWriter(t *testing.T) {
	db, ctx := handoffDatabaseFixture(t)
	s, _ := NewLedgerReceiptStore(db)
	b := receiptBinding()
	h, err := s.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareHandoff(ctx, "takeover", []LedgerHandoffInput{{Binding: b, Cursor: "0-0", PreviousOwner: h.Owner()}}, handoffProbeFixture)
	if err != nil {
		t.Fatal(err)
	}
	sha, _ := LedgerHandoffDigest(p)
	entered, release := make(chan struct{}), make(chan struct{})
	applyDone := make(chan error, 1)
	go func() {
		_, e := s.ApplyHandoff(ctx, p, sha, func(ctx context.Context, b LedgerStreamBinding, c string) (LedgerHandoffWitness, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return LedgerHandoffWitness{}, ctx.Err()
			}
			return handoffProbeFixture(ctx, b, c)
		})
		applyDone <- e
	}()
	select {
	case <-entered:
	case e := <-applyDone:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	writeDone := make(chan error, 1)
	go func() {
		_, e := s.Record(ctx, h, "old-writer", "0-0", json.RawMessage(`{}`), []LedgerStreamInput{{EntryID: "1-0", Body: []byte("unapplied")}}, "", receiptApply)
		writeDone <- e
	}()
	select {
	case e := <-writeDone:
		close(release)
		<-applyDone
		t.Fatal("writer crossed handoff", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err = <-applyDone; err != nil {
		t.Fatal(err)
	}
	if err = <-writeDone; !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM receipt_effects`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestCurrentAccountBaselineHandoffBlockedResponsibilityRetained(t *testing.T) {
	db, ctx := handoffDatabaseFixture(t)
	s, _ := NewLedgerReceiptStore(db)
	b := receiptBinding()
	h, err := s.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Record(ctx, h, "bad", "0-0", json.RawMessage(`{}`), []LedgerStreamInput{{EntryID: "1-0", Body: []byte("bad")}}, "", func(context.Context, *Repository, []LedgerStreamInput) (json.RawMessage, error) {
		return json.RawMessage(`{}`), errors.New("unknown application")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareHandoff(ctx, "takeover", []LedgerHandoffInput{{Binding: b, Cursor: "0-0", PreviousOwner: h.Owner()}}, handoffProbeFixture); !errors.Is(err, ErrLedgerCaptureBlocked) {
		t.Fatal(err)
	}
	st, err := s.Status(ctx, h)
	if err != nil || st.BlockedReason != "bad" || st.FaultCount != 1 {
		t.Fatal(st, err)
	}
}
