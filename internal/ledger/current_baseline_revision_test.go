package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func baselineRevisionDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, ctx := currentBaselineDatabase(t)
	entries, err := os.ReadDir("../../migrations/postgres")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") || strings.HasPrefix(name, "000052_") {
			continue
		}
		if name > "000053_account_baseline_revisions.up.sql" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../../migrations/postgres", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
	return db, ctx
}

func revisionNow(t *testing.T, ctx context.Context, q currentBaselineReader, account string) CurrentBaselineRevisionEvidence {
	t.Helper()
	r, err := readCurrentBaselineRevision(ctx, q, account)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func execRevision(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(query, err)
	}
}

func TestCurrentAccountBaselineRevisionInput(t *testing.T) {
	good := CurrentBaselineRevisionEvidence{Epoch: strings.Repeat("a", 32), AccountRevision: "0", UnscopedRevision: "9223372036854775807"}
	if !good.valid() {
		t.Fatal("valid revision rejected")
	}
	for _, bad := range []string{"", "-1", "01", "1e3", "9223372036854775808"} {
		r := good
		r.AccountRevision = bad
		if r.valid() {
			t.Fatal("invalid revision", bad)
		}
	}
	r := good
	r.Epoch = strings.Repeat("A", 32)
	if r.valid() {
		t.Fatal("noncanonical epoch")
	}
	c := currentBaselineFixture(0)
	c.RevisionEvidence = &good
	p, _, err := prepareCurrentBaseline(c)
	if err != nil {
		t.Fatal(err)
	}
	good.AccountRevision = "2"
	if p.RevisionEvidence.AccountRevision != "0" {
		t.Fatal("aliased revision")
	}
}

func TestCurrentAccountBaselineRevisionAtomicWrites(t *testing.T) {
	db, ctx := baselineRevisionDatabase(t)
	r := revisionNow(t, ctx, db, "acct-1")
	if r.AccountRevision != "0" || r.UnscopedRevision != "0" {
		t.Fatal(r)
	}
	execRevision(t, ctx, db, `INSERT INTO accounts(account_id,broker_id) VALUES('acct-1','huaxin'),('other','huaxin')`)
	r = revisionNow(t, ctx, db, "acct-1")
	if r.AccountRevision != "1" {
		t.Fatal(r)
	}
	execRevision(t, ctx, db, `UPDATE accounts SET account_name='changed' WHERE account_id='other'`)
	if revisionNow(t, ctx, db, "acct-1") != r {
		t.Fatal("other account invalidated this account")
	}
	execRevision(t, ctx, db, `UPDATE accounts SET account_name=account_name WHERE account_id='acct-1'`)
	if revisionNow(t, ctx, db, "acct-1") != r {
		t.Fatal("no-op advanced revision")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO positions(account_id,symbol,exchange,quantity) VALUES('acct-1','000001','SZ',100)`); err != nil {
		t.Fatal(err)
	}
	if revisionNow(t, ctx, tx, "acct-1").AccountRevision != "2" {
		t.Fatal("same transaction missing revision")
	}
	if revisionNow(t, ctx, db, "acct-1") != r {
		t.Fatal("uncommitted revision leaked")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if revisionNow(t, ctx, db, "acct-1") != r {
		t.Fatal("rollback advanced revision")
	}
	execRevision(t, ctx, db, `INSERT INTO positions(account_id,symbol,exchange,quantity) VALUES('acct-1','000001','SZ',100)`)
	execRevision(t, ctx, db, `UPDATE positions SET account_id='other' WHERE account_id='acct-1'`)
	if revisionNow(t, ctx, db, "acct-1").AccountRevision != "3" || revisionNow(t, ctx, db, "other").AccountRevision != "3" {
		t.Fatal("account reassignment must touch both")
	}
	r = revisionNow(t, ctx, db, "acct-1")
	execRevision(t, ctx, db, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,body_text) VALUES('test-hb','1-0','out','hb','acct-1','{}')`)
	if revisionNow(t, ctx, db, "acct-1") != r {
		t.Fatal("ordinary heartbeat treated as economic revision")
	}
	execRevision(t, ctx, db, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,body_text) VALUES('test-event','1-0','out','event','malformed')`)
	if revisionNow(t, ctx, db, "acct-1").UnscopedRevision != "1" || revisionNow(t, ctx, db, "other").UnscopedRevision != "1" {
		t.Fatal("unscoped event not observable by all accounts")
	}
	execRevision(t, ctx, db, `TRUNCATE cash_ledger`)
	if revisionNow(t, ctx, db, "acct-1").UnscopedRevision != "2" {
		t.Fatal("truncate bypassed observation")
	}
	for _, query := range []string{`DELETE FROM account_baseline_revisions`, `UPDATE account_baseline_revisions SET revision=1`, `TRUNCATE account_baseline_revisions`, `DELETE FROM account_baseline_revision_epoch`, `TRUNCATE account_baseline_revision_epoch`} {
		if _, err = db.ExecContext(ctx, query); err == nil {
			t.Fatal("revision reset allowed", query)
		}
	}
}

func TestCurrentAccountBaselineRevisionConcurrentAndPublication(t *testing.T) {
	db, ctx := baselineRevisionDatabase(t)
	s, _ := NewCurrentBaselineStore(db)
	execRevision(t, ctx, db, `INSERT INTO accounts(account_id,broker_id) VALUES('acct-1','huaxin')`)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := db.ExecContext(ctx, `INSERT INTO positions(account_id,symbol,exchange,quantity) VALUES('acct-1',$1,'SZ',100)`, fmt.Sprintf("%06d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r := revisionNow(t, ctx, db, "acct-1")
	if r.AccountRevision != "9" {
		t.Fatal("lost concurrent revision", r)
	}
	c := currentBaselineFixture(501)
	c.RevisionEvidence = &r
	m, err := s.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if m.LedgerRevision == nil || *m.LedgerRevision != "9" || m.RevisionEvidence == nil || m.Consistency.CoverageVerified {
		t.Fatal("wrong revision or proof", m)
	}
	v, err := s.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "unverifiable" {
		t.Fatal(v, err)
	}
	page, err := s.Page(ctx, c.Scope, m.BaselineID, "positions", "", 500)
	if err != nil || page.LedgerRevision == nil || *page.LedgerRevision != "9" {
		t.Fatal(page, err)
	}
	// Hold a real business write open. A new publication must wait for it and
	// reject the obsolete read, not commit a candidate alongside newer facts.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE positions SET quantity=200 WHERE account_id='acct-1' AND symbol='000000'`); err != nil {
		t.Fatal(err)
	}
	c2 := c
	c2.CollectionID = "stale-read"
	done := make(chan error, 1)
	go func() { _, err := s.PublishCandidate(ctx, c2); done <- err }()
	select {
	case err := <-done:
		t.Fatal("publication did not wait for writer", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrCurrentBaselineStale) {
		t.Fatal("obsolete source published", err)
	}
	if again, err := s.PublishCandidate(ctx, c); err != nil || again.BaselineID != m.BaselineID {
		t.Fatal("idempotent retry lost original", err)
	}
	v, err = s.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "invalidated" {
		t.Fatal(v, err)
	}
	execRevision(t, ctx, db, `UPDATE positions SET quantity=100 WHERE account_id='acct-1' AND symbol='000000'`)
	v, err = s.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "invalidated" || *v.CurrentLedgerRevision != "11" {
		t.Fatal("restored balance revived old baseline", v, err)
	}
	last, err := s.Page(ctx, c.Scope, m.BaselineID, "positions", page.NextCursor, 500)
	if err != nil || last.Count != 1 || *last.LedgerRevision != "9" {
		t.Fatal("changed fixed pages", last, err)
	}
	r = revisionNow(t, ctx, db, "acct-1")
	c2.RevisionEvidence = &r
	newManifest, err := s.PublishCandidate(ctx, c2)
	if err != nil || newManifest.PublicationGeneration != "2" {
		t.Fatal("failed publication consumed generation", err)
	}
	scope := c.Scope
	scope.CounterSessionID = "session-2"
	v, err = s.CheckValidity(ctx, "test", scope, newManifest.BaselineID)
	if err != nil || v.Status != "invalidated" {
		t.Fatal("session change not rejected", v, err)
	}
	if _, err = s.CheckValidity(ctx, "production", c.Scope, newManifest.BaselineID); !errors.Is(err, ErrCurrentBaselineInput) {
		t.Fatal("deployment mismatch", err)
	}
	other := c.Scope
	other.AccountID = "other"
	if _, err = s.CheckValidity(ctx, "test", other, newManifest.BaselineID); !errors.Is(err, ErrCurrentBaselineUnavailable) {
		t.Fatal("cross-account disclosure", err)
	}
}

func TestCurrentAccountBaselineRevisionTrackerAndEpoch(t *testing.T) {
	db, ctx := baselineRevisionDatabase(t)
	s, _ := NewCurrentBaselineStore(db)
	execRevision(t, ctx, db, `INSERT INTO accounts(account_id,broker_id) VALUES('acct-1','huaxin')`)
	c := currentBaselineFixture(1)
	r := revisionNow(t, ctx, db, "acct-1")
	c.RevisionEvidence = &r
	m, err := s.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	execRevision(t, ctx, db, `ALTER TABLE positions DISABLE TRIGGER account_baseline_revision_watch`)
	if _, err = readCurrentBaselineRevision(ctx, db, "acct-1"); !errors.Is(err, ErrCurrentBaselineTracking) {
		t.Fatal("disabled observer accepted", err)
	}
	v, err := s.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "unverifiable" || v.CurrentLedgerRevision != nil {
		t.Fatal(v, err)
	}
	c2 := c
	c2.CollectionID = "disabled-observer"
	if _, err = s.PublishCandidate(ctx, c2); !errors.Is(err, ErrCurrentBaselineTracking) {
		t.Fatal("published without observer", err)
	}
	execRevision(t, ctx, db, `ALTER TABLE positions ENABLE TRIGGER account_baseline_revision_watch`)
	// Reinstalling tracking creates a different epoch; an identical numeric
	// revision in another tracking lifetime must not restore an old baseline.
	for _, suffix := range []string{"down", "up"} {
		raw, e := os.ReadFile("../../migrations/postgres/000053_account_baseline_revisions." + suffix + ".sql")
		if e != nil {
			t.Fatal(e)
		}
		execRevision(t, ctx, db, string(raw))
	}
	execRevision(t, ctx, db, `UPDATE accounts SET account_name='updated' WHERE account_id='acct-1'`)
	now := revisionNow(t, ctx, db, "acct-1")
	if now.AccountRevision != r.AccountRevision || now.Epoch == r.Epoch {
		t.Fatal("bad epoch test", r, now)
	}
	v, err = s.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "invalidated" {
		t.Fatal("epoch reset revived publication", v, err)
	}
	legacy := currentBaselineFixture(0)
	legacy.CollectionID = "legacy-without-revision"
	old, err := s.PublishCandidate(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.CheckValidity(ctx, "test", legacy.Scope, old.BaselineID)
	if err != nil || v.Status != "unverifiable" {
		t.Fatal("legacy promoted", v, err)
	}
	if old.LedgerRevision != nil || old.RevisionEvidence != nil {
		t.Fatal("invented legacy revision")
	}
}

func TestCurrentAccountBaselineRevisionTrackedTables(t *testing.T) {
	db, ctx := baselineRevisionDatabase(t)
	// Verify the actual migrated schema, not only hand-built test tables.
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=current_schema()::regnamespace AND t.tgname IN ('account_baseline_revision_watch','account_baseline_revision_truncate')`).Scan(&count); err != nil || count != 24 {
		t.Fatal("missing writer hooks", count, err)
	}
	execRevision(t, ctx, db, `INSERT INTO accounts(account_id,broker_id) VALUES('acct-1','huaxin')`)
	r := revisionNow(t, ctx, db, "acct-1")
	for _, role := range []string{"cmd.trade", "cmd.query", "reply", "event", "dlq"} {
		execRevision(t, ctx, db, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id) VALUES($1,'1-0','in',$2,'acct-1')`, "stream-"+role, role)
	}
	n := revisionNow(t, ctx, db, "acct-1")
	start, _ := strconv.Atoi(r.AccountRevision)
	end, _ := strconv.Atoi(n.AccountRevision)
	if end-start != 5 {
		t.Fatal("raw responsibilities missed", r, n)
	}
}
