package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func currentBaselineDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("RELAY_POSITION_BASELINE_ISOLATED_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL only")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Host, "/tmp/relay-position-baseline-pg.") || cfg.User != "relay_baseline_test" {
		t.Fatal("refusing non-isolated database")
	}
	admin := stdlib.OpenDB(*cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	schema := fmt.Sprintf("baseline_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() {
		db.Close()
		admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		cancel()
	})
	raw, err := os.ReadFile("../../migrations/postgres/000052_current_account_baselines.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func TestCurrentAccountBaselineIsolatedFixedPages(t *testing.T) {
	db, ctx := currentBaselineDatabase(t)
	s, _ := NewCurrentBaselineStore(db)
	c := currentBaselineFixture(501)
	m, err := s.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentBaselineUnverified(t, m)
	first, err := s.Page(ctx, c.Scope, m.BaselineID, "positions", "", 500)
	if err != nil || first.Count != 500 || first.PageComplete || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	next := currentBaselineFixture(2)
	next.CollectionID = "collection-2"
	next.Resources["positions"][0].Document = json.RawMessage(`{"security_id":"000001.SH","quantity":0}`)
	next.Resources["positions"] = append(next.Resources["positions"], CurrentBaselineRecord{"999999.SH", json.RawMessage(`{"security_id":"999999.SH","quantity":200}`)})
	newManifest, err := s.PublishCandidate(ctx, next)
	if err != nil || newManifest.BaselineID == m.BaselineID || newManifest.PublicationGeneration != "2" {
		t.Fatal(newManifest, err)
	}
	// Reconstruct the reader between pages; no process-local snapshot is needed.
	s, _ = NewCurrentBaselineStore(db)
	last, err := s.Page(ctx, c.Scope, m.BaselineID, "positions", first.NextCursor, 500)
	if err != nil || last.Count != 1 || !last.PageComplete || !strings.Contains(string(last.Items[0]), "000500.SH") {
		t.Fatal(last, err)
	}
	all := append(first.Items, last.Items...)
	parts := make([]CurrentBaselineRecord, len(all))
	for i, doc := range all {
		parts[i].Document = doc
	}
	if baselineHash(baselineRowsJSON(parts)) != m.Resources["positions"].ContentSHA256 {
		t.Fatal("mixed generations")
	}
	if reread, err := s.Manifest(ctx, c.Scope, m.BaselineID); err != nil || !reflect.DeepEqual(reread, m) {
		t.Fatal("immutable manifest changed", err)
	}
	latest, err := s.Latest(ctx, c.Scope)
	if err != nil || latest.BaselineID != newManifest.BaselineID {
		t.Fatal(latest, err)
	}
	// Even successful-looking query metadata must not manufacture completeness.
	empty := currentBaselineFixture(0)
	empty.Cash = nil
	empty.CollectionID = "failed-collection"
	empty.Queries = json.RawMessage(`[{"status":"failed","last_page_seen":false}]`)
	em, err := s.PublishCandidate(ctx, empty)
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentBaselineUnverified(t, em)
	latest, err = s.Latest(ctx, c.Scope)
	if err != nil || latest.BaselineID != em.BaselineID || latest.Completeness != "unavailable" {
		t.Fatal("fell back to old version", latest, err)
	}
	ep, err := s.Page(ctx, c.Scope, em.BaselineID, "positions", "", 1)
	if err != nil || ep.Count != 0 || !ep.PageComplete || ep.Items == nil || em.MissingSecurityMeansZero {
		t.Fatal(ep, err)
	}
	if _, err = s.Page(ctx, c.Scope, newManifest.BaselineID, "positions", first.NextCursor, 500); !errors.Is(err, ErrCurrentBaselineInput) {
		t.Fatal("cross-version cursor", err)
	}
	if _, err = s.Page(ctx, c.Scope, m.BaselineID, "orders", first.NextCursor, 500); !errors.Is(err, ErrCurrentBaselineInput) {
		t.Fatal("cross-resource cursor", err)
	}
	for _, limit := range []int{0, -1, 501} {
		if _, err = s.Page(ctx, c.Scope, m.BaselineID, "positions", "", limit); !errors.Is(err, ErrCurrentBaselineInput) {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*CurrentBaselineScope){func(s *CurrentBaselineScope) { s.BrokerID = "other" }, func(s *CurrentBaselineScope) { s.GatewayID = "other" }, func(s *CurrentBaselineScope) { s.CounterSessionID = "new" }, func(s *CurrentBaselineScope) { s.TradeDate = "20261120" }, func(s *CurrentBaselineScope) { n := int64(2); s.ConnectionGeneration = &n }} {
		wrong := c.Scope
		mutate(&wrong)
		if _, err = s.Manifest(ctx, wrong, m.BaselineID); !errors.Is(err, ErrCurrentBaselineScope) {
			t.Fatal("scope leak", wrong, err)
		}
	}
	for _, mutate := range []func(*CurrentBaselineScope){func(s *CurrentBaselineScope) { s.Environment = "production" }, func(s *CurrentBaselineScope) { s.AccountID = "other" }} {
		wrong := c.Scope
		mutate(&wrong)
		if _, err = s.Manifest(ctx, wrong, m.BaselineID); !errors.Is(err, ErrCurrentBaselineUnavailable) {
			t.Fatal("account leak", err)
		}
	}
	changed := currentBaselineFixture(0)
	changed.Scope.CounterSessionID = "new-session"
	changed.CollectionID = "new-session"
	if _, err = s.PublishCandidate(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Latest(ctx, c.Scope); !errors.Is(err, ErrCurrentBaselineScope) {
		t.Fatal("latest fell back across session", err)
	}
}

func TestCurrentAccountBaselineIsolatedConcurrentPublication(t *testing.T) {
	db, ctx := currentBaselineDatabase(t)
	s, _ := NewCurrentBaselineStore(db)
	c := currentBaselineFixture(2)
	var wg sync.WaitGroup
	results := make(chan CurrentBaselineManifest, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m, err := s.PublishCandidate(ctx, c); results <- m; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first CurrentBaselineManifest
	for m := range results {
		if first.BaselineID == "" {
			first = m
		}
		if !reflect.DeepEqual(first, m) || m.PublicationGeneration != "1" {
			t.Fatal("duplicate publication", first, m)
		}
	}
	conflict := currentBaselineFixture(1)
	if _, err := s.PublishCandidate(ctx, conflict); !errors.Is(err, ErrCurrentBaselineConflict) {
		t.Fatal(err)
	}
	results = make(chan CurrentBaselineManifest, 8)
	errs = make(chan error, 8)
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := currentBaselineFixture(1)
			c.CollectionID = fmt.Sprint("new-", i)
			m, e := s.PublishCandidate(ctx, c)
			results <- m
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for m := range results {
		if seen[m.PublicationGeneration] || m.PublicationGeneration == "1" {
			t.Fatal("duplicate generation", m)
		}
		seen[m.PublicationGeneration] = true
	}
	var generation, count int
	if err := db.QueryRowContext(ctx, `SELECT generation FROM account_baseline_heads`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM account_baseline_publications`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if generation != 9 || count != 9 {
		t.Fatal(generation, count)
	}
}

func TestCurrentAccountBaselineIsolatedAtomicityAndIntegrity(t *testing.T) {
	db, ctx := currentBaselineDatabase(t)
	s, _ := NewCurrentBaselineStore(db)
	c := currentBaselineFixture(501)
	// Force failure after the manifest and positions batches have been inserted.
	_, err := db.ExecContext(ctx, `CREATE FUNCTION fail_baseline_order() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.resource='orders' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END; $$; CREATE TRIGGER injected_failure BEFORE INSERT ON account_baseline_rows FOR EACH ROW EXECUTE FUNCTION fail_baseline_order()`)
	if err != nil {
		t.Fatal(err)
	}
	c.Resources["orders"] = []CurrentBaselineRecord{{"pending-no-broker-id", json.RawMessage(`{"gateway_order_id":null,"execution_state":"unknown","trade_date":"20261118"}`)}}
	if _, err = s.PublishCandidate(ctx, c); err == nil {
		t.Fatal("injected failure ignored")
	}
	for _, table := range []string{"account_baseline_heads", "account_baseline_publications", "account_baseline_rows"} {
		var count int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial commit", table, count, err)
		}
	}
	if _, err = db.ExecContext(ctx, `DROP TRIGGER injected_failure ON account_baseline_rows`); err != nil {
		t.Fatal(err)
	}
	m, err := s.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Page(ctx, c.Scope, m.BaselineID, "orders", "", 500)
	if err != nil || p.Count != 1 || !strings.Contains(string(p.Items[0]), `"gateway_order_id":null`) {
		t.Fatal("lost unresolved cross-day command", p, err)
	}
	for _, statement := range []string{`UPDATE account_baseline_publications SET manifest='{}'`, `DELETE FROM account_baseline_publications`, `UPDATE account_baseline_rows SET document='{}'`, `DELETE FROM account_baseline_rows`} {
		if _, err = db.ExecContext(ctx, statement); err == nil {
			t.Fatal("immutable records changed", statement)
		}
	}
	// Simulate corruption with DBA privileges only in this disposable schema.
	if _, err = db.ExecContext(ctx, `ALTER TABLE account_baseline_rows DISABLE TRIGGER USER; DELETE FROM account_baseline_rows WHERE resource='positions' AND ordinal=500; ALTER TABLE account_baseline_rows ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Page(ctx, c.Scope, m.BaselineID, "positions", "", 500); !errors.Is(err, ErrCurrentBaselineCorrupt) {
		t.Fatal("missing final row was returned as a valid first page", err)
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE account_baseline_rows DISABLE TRIGGER USER; UPDATE account_baseline_rows SET document='{}' WHERE resource='orders'; ALTER TABLE account_baseline_rows ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Page(ctx, c.Scope, m.BaselineID, "orders", "", 500); !errors.Is(err, ErrCurrentBaselineCorrupt) {
		t.Fatal("bad row digest", err)
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE account_baseline_publications DISABLE TRIGGER USER; UPDATE account_baseline_publications SET manifest='{}'; ALTER TABLE account_baseline_publications ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Manifest(ctx, c.Scope, m.BaselineID); !errors.Is(err, ErrCurrentBaselineCorrupt) {
		t.Fatal("bad manifest digest", err)
	}
	down, err := os.ReadFile("../../migrations/postgres/000052_current_account_baselines.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(down)); err != nil {
		t.Fatal("migration rollback", err)
	}
}
