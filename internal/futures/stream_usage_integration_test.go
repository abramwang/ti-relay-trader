package futures

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func streamUsageFixture(t *testing.T) (*sql.DB, *QueryStore, context.Context, *StreamStore, StreamHandle, []byte) {
	t.Helper()
	db, q, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, q, ctx, "usage-bound", r, c)
	s, h := openStream(t, db, ctx, StreamConfig{c.commandScope(), "reply", "synthetic-redis-deployment"})
	raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
	return db, q, ctx, s, h, raw
}

func checkStreamUsage(t *testing.T, db *sql.DB, ctx context.Context, s *StreamStore, h StreamHandle) StreamStatus {
	t.Helper()
	got, err := s.Status(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	want := StreamStatus{Cursor: got.Cursor}
	err = db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(octet_length(body)),0),
        count(*) FILTER(WHERE state='pending'),COALESCE(sum(octet_length(body)) FILTER(WHERE state='pending'),0),
        count(*) FILTER(WHERE state='quarantined'),COALESCE(sum(octet_length(body)) FILTER(WHERE state='quarantined'),0),
        count(*) FILTER(WHERE state='delivered'),COALESCE(sum(octet_length(body)) FILTER(WHERE state='delivered'),0)
        FROM futures_stream_inbox WHERE stream_key=$1`, h.stream).Scan(
		&want.Records, &want.Bytes, &want.Pending, &want.PendingBytes,
		&want.Quarantined, &want.QuarantinedBytes, &want.Delivered, &want.DeliveredBytes)
	if err != nil || got != want {
		t.Fatal("transactional counters differ from stored originals", got, want, err)
	}
	return got
}

func streamUsageMigration(t *testing.T, db *sql.DB, ctx context.Context, direction string) error {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/postgres/000059_futures_stream_backlog." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func TestStreamUsageBacklogFullCanDeliverAndRetainHistory(t *testing.T) {
	db, q, ctx, s, h, raw := streamUsageFixture(t)
	first := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw})
	chunk := bytes.Repeat([]byte("x"), maxSnapshotBytes)
	for i := 2; i <= 8; i++ {
		captureStream(t, s, ctx, h, fmt.Sprintf("%d-0", i-1), StreamInput{fmt.Sprintf("%d-0", i), chunk})
	}
	captureStream(t, s, ctx, h, "8-0", StreamInput{"9-0", chunk[:maxSnapshotBytes-len(raw)]})
	full := checkStreamUsage(t, db, ctx, s, h)
	if full.Pending != 9 || full.Bytes != maxStreamBacklogBytes {
		t.Fatal(full)
	}
	if _, err := s.Capture(ctx, h, "9-0", []StreamInput{{"1-0", raw}, {"10-0", []byte("new")}}); !errors.Is(err, ErrStreamBacklog) {
		t.Fatal("full backlog admitted new receipt", err)
	}
	if got := checkStreamUsage(t, db, ctx, s, h); got != full {
		t.Fatal("failed page changed checkpoint or counters", got, full)
	}
	if r := captureStream(t, s, ctx, h, "9-0", StreamInput{"1-0", raw}); !r.Replayed {
		t.Fatal("full backlog must still recognize duplicate originals", r)
	}
	pending, err := s.Pending(ctx, h, 0, 2)
	if err != nil || len(pending) != 2 {
		t.Fatal(pending, err)
	}
	if err := s.Quarantine(ctx, h, pending[1].ID, "synthetic review"); err != nil {
		t.Fatal(err)
	}
	quarantined := checkStreamUsage(t, db, ctx, s, h)
	if quarantined.Quarantined != 1 || quarantined.Pending != 8 {
		t.Fatal(quarantined)
	}
	if _, err := s.Capture(ctx, h, "9-0", []StreamInput{{"10-0", []byte("new")}}); !errors.Is(err, ErrStreamBacklog) {
		t.Fatal("quarantine must not free capacity", err)
	}
	if _, err := s.ForwardQuery(ctx, h, first.ReceiptIDs[0], q, "usage-bound", 0); err != nil {
		t.Fatal("backlog fullness prevented actual query handoff", err)
	}
	before := checkStreamUsage(t, db, ctx, s, h)
	if before.Delivered != 1 || before.Quarantined != 1 || before.Bytes != full.Bytes || before.PendingBytes+before.QuarantinedBytes != full.Bytes-int64(len(raw)) {
		t.Fatal(before)
	}
	captureStream(t, s, ctx, h, "9-0", StreamInput{"10-0", []byte("new")})
	_, next := openStream(t, db, ctx, h.config)
	after := checkStreamUsage(t, db, ctx, s, next)
	if after.Bytes <= maxStreamBacklogBytes || after.Records != 10 || after.Delivered != 1 {
		t.Fatal("historical bytes were erased or still limited", after)
	}
	stored, err := s.Record(ctx, next, first.ReceiptIDs[0])
	if err != nil || !bytes.Equal(stored.Raw, raw) || stored.State != "delivered" {
		t.Fatal(stored, err)
	}
	if err := streamUsageMigration(t, db, ctx, "down"); err == nil {
		t.Fatal("downgrade would impose an exceeded lifetime cap")
	}
	checkStreamUsage(t, db, ctx, s, next)
}

func TestStreamUsageMoreThanLifetimeRecordLimit(t *testing.T) {
	db, q, _, s, h, raw := streamUsageFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw})
	if _, err := s.ForwardQuery(ctx, h, r.ReceiptIDs[0], q, "usage-bound", 0); err != nil {
		t.Fatal(err)
	}
	// Seed synthetic delivered history in bounded transactions, not broker input.
	for start := 2; start <= maxStreamBacklogCount; start += 500 {
		end := min(start+499, maxStreamBacklogCount)
		if _, err := db.ExecContext(ctx, `INSERT INTO futures_stream_inbox(stream_key,entry_id,body,body_sha256,state,batch_id,delivered_at)
            SELECT $1,i::text||'-0','history'::bytea,sha256('history'::bytea),'delivered','usage-bound',clock_timestamp()
            FROM generate_series($2::integer,$3::integer) i`, h.stream, start, end); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_receivers SET cursor_id='50000-0' WHERE stream_key=$1`, h.stream); err != nil {
		t.Fatal(err)
	}
	captureStream(t, s, ctx, h, "50000-0", StreamInput{"50001-0", []byte("new")})
	got := checkStreamUsage(t, db, ctx, s, h)
	if got.Records != 50001 || got.Delivered != 50000 || got.Pending != 1 {
		t.Fatal(got)
	}
	if again := captureStream(t, s, ctx, h, "50001-0", StreamInput{"1-0", raw}); !again.Replayed || again.ReceiptIDs[0] != r.ReceiptIDs[0] {
		t.Fatal(again)
	}
	if err := streamUsageMigration(t, db, ctx, "down"); err == nil {
		t.Fatal("downgrade discarded historical receipts")
	}
	checkStreamUsage(t, db, ctx, s, h)
}

func TestStreamUsageMigrationPreservesEvidenceAndRejectsCorruption(t *testing.T) {
	db, q, ctx, s, h, raw := streamUsageFixture(t)
	r := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw}, StreamInput{"2-0", []byte("quarantine")}, StreamInput{"3-0", []byte("pending")})
	if _, err := s.ForwardQuery(ctx, h, r.ReceiptIDs[0], q, "usage-bound", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Quarantine(ctx, h, r.ReceiptIDs[1], "retain original"); err != nil {
		t.Fatal(err)
	}
	want := checkStreamUsage(t, db, ctx, s, h)
	if err := streamUsageMigration(t, db, ctx, "down"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, h, "3-0", []StreamInput{{"4-0", []byte("no schema")}}); err == nil {
		t.Fatal("writer continued without required migration")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_receivers SET audit_bytes=audit_bytes+1 WHERE stream_key=$1`, h.stream); err != nil {
		t.Fatal(err)
	}
	if err := streamUsageMigration(t, db, ctx, "up"); err == nil {
		t.Fatal("migration silently repaired mismatched evidence")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_receivers SET audit_bytes=audit_bytes-1 WHERE stream_key=$1`, h.stream); err != nil {
		t.Fatal(err)
	}
	if err := streamUsageMigration(t, db, ctx, "up"); err != nil {
		t.Fatal(err)
	}
	if got := checkStreamUsage(t, db, ctx, s, h); got != want {
		t.Fatal(got, want)
	}
	stored, err := s.Record(ctx, h, r.ReceiptIDs[0])
	if err != nil || !bytes.Equal(stored.Raw, raw) {
		t.Fatal(stored, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_usage_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic usage failure'; END $$;
        CREATE TRIGGER fail_usage_update BEFORE UPDATE OF pending_count ON futures_stream_receivers FOR EACH ROW EXECUTE FUNCTION fail_usage_update()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, h, "3-0", []StreamInput{{"4-0", []byte("atomic")}}); err == nil {
		t.Fatal("counter failure ignored")
	}
	if got := checkStreamUsage(t, db, ctx, s, h); got != want {
		t.Fatal("counter failure partially committed original or cursor", got, want)
	}
}

func TestStreamUsageLossDoesNotRewriteAccounting(t *testing.T) {
	for _, change := range []string{"delete", "body"} {
		t.Run(change, func(t *testing.T) {
			db, _, ctx, s, h, raw := streamUsageFixture(t)
			r := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw})
			before := checkStreamUsage(t, db, ctx, s, h)
			statement := `DELETE FROM futures_stream_inbox WHERE receipt_id=$1`
			if change == "body" {
				statement = `UPDATE futures_stream_inbox SET body='changed'::bytea WHERE receipt_id=$1`
			}
			if _, err := db.ExecContext(ctx, statement, r.ReceiptIDs[0]); err != nil {
				t.Fatal(err)
			}
			// Usage is a transaction-maintained observation, not a re-audit of
			// every body. An out-of-band loss must not manufacture free space.
			usage, err := s.Status(ctx, h)
			if err != nil || usage != before {
				t.Fatal("loss silently reduced retained responsibility", usage, before, err)
			}
			if _, err := s.Record(ctx, h, r.ReceiptIDs[0]); err == nil {
				t.Fatal("lost/changed original remained readable as valid")
			}
			if err := streamUsageMigration(t, db, ctx, "down"); err == nil {
				t.Fatal("downgrade hid evidence loss")
			}
		})
	}
}
