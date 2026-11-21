package futures

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func assertCommandPending(t *testing.T, db *sql.DB, id int64, want bool) {
	t.Helper()
	var pending bool
	if err := db.QueryRow(`SELECT pending FROM futures_command_work WHERE command_id=$1`, id).Scan(&pending); err != nil || pending != want {
		t.Fatalf("command %d pending=%v want=%v: %v", id, pending, want, err)
	}
}

func uniqueWorkCommand(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	return editBody(t, raw, func(m map[string]any) {
		m["message_id"], m["idempotency_key"] = name, name
	})
}

func TestCommandBacklogCountReleaseConcurrencyAndHistory(t *testing.T) {
	f := projectionReplyFixture(t, "positions")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f.ctx = ctx
	s, _ := NewCommandStore(f.db)
	for i := 1; i < maxPendingCommands; i++ {
		raw := uniqueWorkCommand(t, f.c.Original.Raw, fmt.Sprint("queued-", i))
		r, err := s.Register(ctx, f.c.Scope, "cmd.query", raw, RegistrationEvidence{})
		if err != nil || r.Outcome != "registered" {
			t.Fatal(i, r, err)
		}
	}
	// Full backlog must still recognize old intentions and retain aliases.
	alias := editBody(t, f.c.Original.Raw, func(m map[string]any) { m["message_id"] = "alias-at-capacity" })
	for _, raw := range [][]byte{f.c.Original.Raw, alias} {
		r, err := s.Register(ctx, f.c.Scope, "cmd.query", raw, RegistrationEvidence{})
		if err != nil || r.Outcome != "replayed" || r.CommandID != f.c.CommandID {
			t.Fatal(r, err)
		}
	}
	conflict := append(append([]byte{}, alias...), ' ')
	if r, err := s.Register(ctx, f.c.Scope, "cmd.query", conflict, RegistrationEvidence{}); err != nil || r.Outcome != "conflict" {
		t.Fatal(r, err)
	}
	counts := map[string]int{}
	for _, table := range []string{"futures_commands", "futures_command_work", "futures_command_audit", "futures_command_messages"} {
		counts[table] = storedCount(t, f.db, table)
	}
	if counts["futures_command_audit"] <= maxPendingCommands {
		t.Fatal("history did not cross old lifetime quota")
	}
	extra := uniqueWorkCommand(t, f.c.Original.Raw, "beyond-limit")
	if _, err := s.Register(ctx, f.c.Scope, "cmd.query", extra, RegistrationEvidence{}); !errors.Is(err, ErrCommandBacklog) {
		t.Fatal(err)
	}
	for table, count := range counts {
		if storedCount(t, f.db, table) != count {
			t.Fatal("capacity rejection partly committed", table)
		}
	}
	f.append(t, f.raw)
	if page, err := f.p.Step(ctx, f.h, 32); err != nil || page.Delivered != 1 {
		t.Fatal(page, err)
	}
	assertCommandPending(t, f.db, f.c.CommandID, false)
	// Two concurrent registrations compete for one free slot.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		raw := uniqueWorkCommand(t, f.c.Original.Raw, fmt.Sprint("race-", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Register(ctx, f.c.Scope, "cmd.query", raw, RegistrationEvidence{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	succeeded, blocked := 0, 0
	for err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrCommandBacklog) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatal(succeeded, blocked)
	}
	// Contradictory evidence is retained even when it takes work over the cap.
	f.append(t, editBody(t, f.raw, func(m map[string]any) { m["message"] = "changed response" }))
	if page, err := f.p.Step(ctx, f.h, 32); err != nil || page.Invalid != 1 {
		t.Fatal(page, err)
	}
	assertCommandPending(t, f.db, f.c.CommandID, true)
	if _, err := s.Register(ctx, f.c.Scope, "cmd.query", extra, RegistrationEvidence{}); !errors.Is(err, ErrCommandBacklog) {
		t.Fatal(err)
	}
	s, _ = NewCommandStore(f.db)
	r, err := s.Register(ctx, f.c.Scope, "cmd.query", alias, RegistrationEvidence{})
	if err != nil || r.CommandID != f.c.CommandID || !bytes.Equal(r.Original.Raw, f.c.Original.Raw) {
		t.Fatal("restart lost original identity", r, err)
	}
	assertBacklogMigrationRejected(t, f.db, ctx, "down")
}

func TestCommandBacklogBytesAndRejectedAuditHistory(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(fmt.Sprint(rejected), func(t *testing.T) {
			db, s, _ := commandTestDB(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			v := intentVectors(t)["account.asset.query"]
			if rejected {
				v = intentVectors(t)["submit"]
			}
			blocked := false
			for i := 0; i < 1030; i++ {
				raw := uniqueWorkCommand(t, []byte(v.Raw), fmt.Sprint("byte-limit-", i))
				raw = append(raw, bytes.Repeat([]byte(" "), 65536-len(raw))...)
				r, err := s.Register(ctx, v.scope(), v.Stream, raw, RegistrationEvidence{})
				if !rejected && errors.Is(err, ErrCommandBacklog) {
					blocked = true
					break
				}
				if err != nil || (rejected && r.Outcome != "rejected") || (!rejected && r.Outcome != "registered") {
					t.Fatal(i, r.Outcome, err)
				}
			}
			if !rejected {
				if !blocked || storedCount(t, db, "futures_commands") >= maxPendingCommands {
					t.Fatal("byte cap not independent of count cap")
				}
				return
			}
			var auditBytes int64
			if err := db.QueryRowContext(ctx, `SELECT audit_bytes FROM futures_command_routes`).Scan(&auditBytes); err != nil || auditBytes <= maxPendingCommandBytes {
				t.Fatal(auditBytes, err)
			}
			if storedCount(t, db, "futures_command_work") != 0 {
				t.Fatal("rejected attempts consumed command workload")
			}
			v = intentVectors(t)["account.asset.query"]
			registerVector(t, s, ctx, v, RegistrationEvidence{}, "registered")
			assertBacklogMigrationRejected(t, db, ctx, "down")
		})
	}
}

func assertBacklogMigrationRejected(t *testing.T, db *sql.DB, ctx context.Context, direction string) {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/postgres/000061_futures_command_backlog." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, string(raw)); err == nil {
		t.Fatal("inconsistent or oversized history migrated")
	}
}

func TestCommandBacklogMigrationRevalidatesHistory(t *testing.T) {
	for _, scenario := range []string{"ready", "failed", "partial", "corrupt_result", "corrupt_raw", "corrupt_publication", "unproven_not_published", "reviewed", "not_published", "attempted"} {
		t.Run(scenario, func(t *testing.T) {
			f := projectionReplyFixture(t, "positions")
			raw := f.raw
			if scenario == "reviewed" {
				q, _ := NewQueryStore(f.db)
				c, err := reportedQueryContext(f.c, f.target, raw)
				if err != nil {
					t.Fatal(err)
				}
				c.Command = nil
				bindQuery(t, q, f.ctx, "reviewed", f.c, c)
				if _, err := q.Ingest(f.ctx, "reviewed", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0); err != nil {
					t.Fatal(err)
				}
			} else if oneOf(scenario, "not_published", "attempted", "unproven_not_published") {
				state, reason := scenario, ""
				if scenario != "attempted" {
					state, reason = "not_published", "atomic_gate_changed"
				}
				if scenario == "unproven_not_published" {
					reason = "unverified"
				}
				_, err := f.db.ExecContext(f.ctx, `UPDATE futures_test_query_publications SET state=$1,reason=$2,entry_id=NULL,finished_at=CASE WHEN $1='attempted' THEN NULL ELSE clock_timestamp() END`, state, reason)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if scenario == "failed" {
					raw = failureBody(t, raw)
				} else if scenario == "partial" {
					raw = editBody(t, raw, func(m map[string]any) {
						m["status"] = "partial"
						m["chunk"] = map[string]any{"index": 0, "is_last": false}
					})
				}
				f.append(t, raw)
				if _, err := f.p.Step(f.ctx, f.h, 32); err != nil {
					t.Fatal(err)
				}
			}
			migrateFuturesTest(t, f.db, f.ctx, "000061_futures_command_backlog", "down")
			migrateFuturesTest(t, f.db, f.ctx, "000061_futures_command_backlog", "up")
			assertCommandPending(t, f.db, f.c.CommandID, true)
			switch scenario {
			case "corrupt_publication":
				_, err := f.db.ExecContext(f.ctx, `UPDATE futures_test_query_publications SET entry_id='0-0'`)
				if err != nil {
					t.Fatal(err)
				}
			case "corrupt_result":
				_, err := f.db.ExecContext(f.ctx, `UPDATE futures_reply_results SET result=result||decode('20','hex')`)
				if err != nil {
					t.Fatal(err)
				}
			case "corrupt_raw":
				_, err := f.db.ExecContext(f.ctx, `UPDATE futures_stream_inbox SET body=body||decode('20','hex')`)
				if err != nil {
					t.Fatal(err)
				}
			}
			f.p, _ = NewReplyProjector(f.store, f.c.Scope, f.target)
			page, err := f.p.Step(f.ctx, f.h, 32)
			if oneOf(scenario, "corrupt_result", "corrupt_raw", "corrupt_publication", "unproven_not_published") {
				if err == nil {
					t.Fatal("unverified old result released capacity")
				}
				assertCommandPending(t, f.db, f.c.CommandID, true)
				return
			}
			pending := scenario == "partial" || scenario == "attempted"
			if err != nil || (!pending && page.CommandWorkReleased != 1) || (pending && page.CommandWorkReleased != 0) {
				t.Fatal(page, err)
			}
			assertCommandPending(t, f.db, f.c.CommandID, pending)
		})
	}
}

func TestCommandBacklogAtomicHandoffAndMissingMetadata(t *testing.T) {
	f := projectionReplyFixture(t, "positions")
	id := f.append(t, f.raw)
	_, err := f.db.ExecContext(f.ctx, `CREATE FUNCTION fail_work() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic workload failure'; END $$; CREATE TRIGGER fail_work_update BEFORE UPDATE ON futures_command_work FOR EACH ROW EXECUTE FUNCTION fail_work()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Step(f.ctx, f.h, 32); err == nil {
		t.Fatal("workload failure hidden")
	}
	raw, err := f.store.Record(f.ctx, f.h, id)
	if err != nil || raw.State != "pending" || f.result(t) != nil {
		t.Fatal("partial handoff", raw, err)
	}
	assertCommandPending(t, f.db, f.c.CommandID, true)
	if _, err := f.db.ExecContext(f.ctx, `DROP TRIGGER fail_work_update ON futures_command_work; DELETE FROM futures_command_work`); err != nil {
		t.Fatal(err)
	}
	s, _ := NewCommandStore(f.db)
	if _, err := s.Register(f.ctx, f.c.Scope, "cmd.query", f.c.Original.Raw, RegistrationEvidence{}); !errors.Is(err, ErrCommandWork) {
		t.Fatal("missing work metadata accepted", err)
	}
	assertBacklogMigrationRejected(t, f.db, f.ctx, "down")
}

func TestCommandBacklogPublicationCompletionAtomic(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["account.asset.query"]
	r := registerVector(t, s, ctx, v, RegistrationEvidence{}, "registered")
	_, err := db.ExecContext(ctx, `CREATE FUNCTION fail_work() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic workload failure'; END $$; CREATE TRIGGER fail_work_update BEFORE UPDATE ON futures_command_work FOR EACH ROW EXECUTE FUNCTION fail_work()`)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeQueryPublisher{rejected: true}
	for i := 0; i < 2; i++ {
		result, err := s.publishTestQuery(ctx, r.Scope, r.CommandID, r.Original.MessageID, publicationTarget(), p)
		if !errors.Is(err, ErrQueryPublishUnknown) || result.State != "attempted" || p.appends.Load() != 1 {
			t.Fatal(result, err, p.appends.Load())
		}
	}
	assertCommandPending(t, db, r.CommandID, true)
}

func TestCommandBacklogMigrationRefusesLostAudit(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["account.asset.query"]
	registerVector(t, s, ctx, v, RegistrationEvidence{}, "registered")
	migrateFuturesTest(t, db, ctx, "000061_futures_command_backlog", "down")
	if _, err := db.ExecContext(ctx, `DELETE FROM futures_command_audit`); err != nil {
		t.Fatal(err)
	}
	assertBacklogMigrationRejected(t, db, ctx, "up")
}

func TestCommandBacklogHistoricalScanIsBounded(t *testing.T) {
	f := projectionReplyFixture(t, "positions")
	s, _ := NewCommandStore(f.db)
	var last CommandRegistration
	for i := 0; i < 35; i++ {
		raw := uniqueWorkCommand(t, f.c.Original.Raw, fmt.Sprint("scan-", i))
		var err error
		last, err = s.Register(f.ctx, f.c.Scope, "cmd.query", raw, RegistrationEvidence{})
		if err != nil {
			t.Fatal(err)
		}
	}
	seedPublishedReview(t, f.db, f.ctx, last, f.target)
	f.append(t, boundReply(t, f.raw, last))
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.Delivered != 1 {
		t.Fatal(page, err)
	}
	migrateFuturesTest(t, f.db, f.ctx, "000061_futures_command_backlog", "down")
	migrateFuturesTest(t, f.db, f.ctx, "000061_futures_command_backlog", "up")
	f.p, _ = NewReplyProjector(f.store, f.c.Scope, f.target)
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.CommandWorkReleased != 0 {
		t.Fatal(page, err)
	}
	assertCommandPending(t, f.db, last.CommandID, true)
	if page, err := f.p.Step(f.ctx, f.h, 32); err != nil || page.CommandWorkReleased != 1 {
		t.Fatal(page, err)
	}
	assertCommandPending(t, f.db, last.CommandID, false)
	assertCommandPending(t, f.db, f.c.CommandID, true)
}
