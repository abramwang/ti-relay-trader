package accountbaseline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"ti-relay-trader/internal/ledger"
)

func sourceDatabase(t *testing.T) (*sql.DB, context.Context) {
	return sourceSchemaDatabase(t, false)
}

func sourceSchemaDatabase(t *testing.T, receipts bool) (*sql.DB, context.Context) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	schema := fmt.Sprintf("baseline_sources_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		cancel()
	})
	if receipts {
		files, err := os.ReadDir("../../migrations/postgres")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".up.sql") || f.Name() > "000057_account_baseline_commands.up.sql" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join("../../migrations/postgres", f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, string(raw)); err != nil {
				t.Fatal(f.Name(), err)
			}
		}
		return db, ctx
	}
	raw, err := os.ReadFile("../../migrations/postgres/000052_current_account_baselines.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE raw_stream_messages (
raw_message_pk bigserial PRIMARY KEY,stream_key text,stream_id text,direction text,stream_role text,
account_id text,origin_message_id text,body jsonb,body_text text,parse_error text,received_at timestamptz);
CREATE TABLE orders (order_pk bigserial PRIMARY KEY,account_id text,gateway_order_id text,order_stream_id text,
origin_message_id text,trade_date date,symbol text,exchange text,trade_side text,business_type text,
order_qty bigint,cum_filled_qty bigint,leaves_qty bigint,status text,is_terminal boolean,limit_price numeric,updated_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"accounts", "account_gateway_routes", "order_events", "fills", "positions", "asset_snapshots", "cash_ledger", "etf_component_transfers", "order_cancel_attempts", "test_legacy_order_resolutions"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+"(account_id text)"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = os.ReadFile("../../migrations/postgres/000053_account_baseline_revisions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"000002_stream_checkpoints", "000054_account_baseline_source_health", "000057_account_baseline_commands"} {
		raw, err = os.ReadFile("../../migrations/postgres/" + name + ".up.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx
}

func TestCurrentAccountBaselineIsolatedSourceAssembly(t *testing.T) {
	db, ctx := sourceDatabase(t)
	store, err := ledger.NewCurrentBaselineStore(db)
	if err != nil {
		t.Fatal(err)
	}
	s := fixtureSources(t, 501)
	ids := map[string]string{}
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	delta := start.Sub(fixtureStart)
	originalBodies := map[string]string{}
	// Force position pages across 9/10: ordering a SELECT's text alias used
	// to place the final page ahead of earlier pages, depending on map order.
	if _, err = db.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('raw_stream_messages','raw_message_pk'),6,false)`); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"asset", "positions", "orders", "fills"} {
		q := s.Queries[kind]
		ids[kind] = q.MessageID
		for _, list := range [][]ledger.CurrentBaselineArchive{q.Commands, q.Replies} {
			for _, r := range list {
				// Use a recent synthetic collection window, independent of CI date.
				mutate(t, &r, func(w map[string]any) {
					for _, k := range []string{"sent_at", "produced_at"} {
						if value, ok := w[k].(string); ok {
							at, e := time.Parse(time.RFC3339Nano, value)
							if e != nil {
								t.Fatal(e)
							}
							w[k] = at.Add(delta)
						}
					}
				})
				r.Body = " \n" + r.Body + "\n"
				originalBodies[r.ArchiveID] = r.Body
				origin := q.MessageID
				_, err = db.ExecContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,origin_message_id,body,body_text,received_at) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)`, r.Stream, r.EntryID, r.Direction, r.Role, s.Scope.AccountID, origin, r.Body, r.Body, r.ReceivedAt.Add(delta))
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO orders(account_id,gateway_order_id,trade_date,symbol,exchange,trade_side,business_type,order_qty,cum_filled_qty,leaves_qty,status,is_terminal,limit_price,updated_at) VALUES
('00030484','prior-day','2026-11-18','000001','SZ','B','S',100,0,100,'working',false,10,now()),
('00030484','unsent',NULL,'000001','SZ','B','S',100,0,100,'created',false,10,now()),
('00030484','unknown-terminal','2026-11-19','000001','SZ','B','S',100,0,100,'unknown',true,10,now()),
('00030484','done','2026-11-19','000001','SZ','B','S',100,0,0,'cancelled',true,10,now()),
('other','other-order','2026-11-18','000001','SZ','B','S',100,0,100,'working',false,10,now())`)
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.ReadSources(ctx, "test", s.Scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.UnresolvedOrders) != 3 {
		t.Fatalf("cross-day/unsent/unknown lost or other account leaked: %+v", read.UnresolvedOrders)
	}
	if pages := read.Queries["positions"].Replies; len(pages) != 3 || pages[0].ArchiveID != "9" || pages[1].ArchiveID != "10" || pages[2].ArchiveID != "11" {
		t.Fatal("archive sequence sorted as text", pages)
	}
	if read.SourceHealth == nil || !read.SourceHealth.TrackingAvailable || read.SourceHealth.ContinuityVerified || read.SourceHealth.Heartbeat != nil {
		t.Fatal("missing source observations must not imply healthy/complete", read.SourceHealth)
	}
	if read.Queries["asset"].Replies[0].Body != originalBodies["archive-asset-0"] {
		t.Fatal("original body reserialized")
	}
	if _, err = store.ReadSources(ctx, "production", s.Scope, ids); err == nil {
		t.Fatal("deployment scope mismatch accepted")
	}
	c, err := Assemble(read, "integration", start)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources["positions"]) != 501 || len(c.Resources["orders"]) != 3 {
		t.Fatal("incomplete assembly", reports(t, c))
	}
	m, err := store.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if m.MissingSecurityMeansZero || m.Consistency.CoverageVerified || m.LedgerRevision == nil || m.RevisionEvidence == nil || m.Completeness != "partial" {
		t.Fatal("overstated evidence", m)
	}
	if m.SourceHealth == nil || len(m.SourceHealth.Checkpoints) != 4 {
		t.Fatal("source health not persisted")
	}
	page, err := store.Page(ctx, s.Scope, m.BaselineID, "positions", "", 500)
	if err != nil || page.Count != 500 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	last, err := store.Page(ctx, s.Scope, m.BaselineID, "positions", page.NextCursor, 500)
	if err != nil || last.Count != 1 || !last.PageComplete {
		t.Fatal(last, err)
	}
	if again, err := store.PublishCandidate(ctx, c); err != nil || again.BaselineID != m.BaselineID {
		t.Fatal("idempotency", err)
	}
	// Original text is mandatory. JSONB fallback must not repair missing audit.
	_, err = db.ExecContext(ctx, `UPDATE raw_stream_messages SET body_text=NULL WHERE origin_message_id=$1`, ids["asset"])
	if err != nil {
		t.Fatal(err)
	}
	read, err = store.ReadSources(ctx, "test", s.Scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := Assemble(read, "missing-original", start)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Cash) != 0 || reports(t, failed)["asset"].ProjectionValid {
		t.Fatal("JSONB substituted for source")
	}
	_, err = store.PublishCandidate(ctx, failed)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Manifest(ctx, s.Scope, m.BaselineID)
	if err != nil || string(old.Cash) != string(m.Cash) {
		t.Fatal("source mutation changed fixed publication", err)
	}
	// A failed reply with an unexpected stream is still part of the evidence.
	bad := fixturePage("fills", 1, true, map[string]any{"items": []any{}})
	mutate(t, &bad, func(w map[string]any) { w["status"] = "failed" })
	_, err = db.ExecContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,origin_message_id,body,body_text,received_at) VALUES('unexpected','99-0','out','reply',$1,$2,$3::jsonb,$3,now())`, s.Scope.AccountID, ids["fills"], bad.Body)
	if err != nil {
		t.Fatal(err)
	}
	read, err = store.ReadSources(ctx, "test", s.Scope, ids)
	if err != nil || len(read.Queries["fills"].Replies) != 2 {
		t.Fatal("failure silently filtered", err)
	}
	c, err = Assemble(read, "failed-fills", start)
	if err != nil {
		t.Fatal(err)
	}
	var report []QueryReport
	if json.Unmarshal(c.Queries, &report) != nil || reports(t, c)["fills"].ProjectionValid {
		t.Fatal("failure projected as empty success")
	}
}
