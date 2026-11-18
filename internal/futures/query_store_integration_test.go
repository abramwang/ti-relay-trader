package futures

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

var futuresTestMigrations = []string{
	"000033_futures_query_storage", "000034_futures_trade_facts", "000035_futures_query_trade_applications",
	"000036_futures_event_inbox", "000037_futures_command_registry", "000038_futures_command_bindings",
	"000039_futures_trade_capacity",
	"000040_futures_stream_inbox",
	"000041_futures_heartbeat_windows",
	"000044_futures_query_publication",
	"000045_futures_query_reviews",
	"000047_futures_cancel_rejections",
}

func migrateFuturesTest(t *testing.T, db *sql.DB, ctx context.Context, name, direction string) {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/postgres/" + name + "." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(name, err)
	}
}

// Each test uses a fresh schema in an explicitly supplied disposable database.
// It never reads application config, production DSNs or the stock test DSN.
func queryTestDB(t *testing.T) (*sql.DB, *QueryStore, context.Context) {
	t.Helper()
	dsn := os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RELAY_FUTURES_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "relay_futures_test_" + hex.EncodeToString(nonce[:])
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	for _, name := range futuresTestMigrations {
		migrateFuturesTest(t, db, ctx, name, "up")
	}
	store, err := NewQueryStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, store, ctx
}

func registerQuery(t *testing.T, s *QueryStore, ctx context.Context, id string, c QueryContext) {
	t.Helper()
	if err := s.registerUnbound(ctx, id, c, time.Minute, DefaultBatchLimits()); err != nil {
		t.Fatal(err)
	}
}

func storedCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func source(id string) QuerySource { return QuerySource{"synthetic:reply", id} }

func TestQueryStoreAtomicCompleteAndReplay(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	last := page(t, "positions", "last", 1, true, "SECOND")
	first := page(t, "positions", "first", 0, false, "FIRST")
	out, err := s.Ingest(ctx, "batch", source("2-0"), last, 0)
	if err != nil || out.State != BatchCollecting || out.Published {
		t.Fatal(out, err)
	}
	if _, err := s.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("partial result visible", err)
	}
	if storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("partial snapshot persisted")
	}
	out, err = s.Ingest(ctx, "batch", source("1-0"), first, time.Second)
	if err != nil || out.State != BatchReady || !out.Published {
		t.Fatal(out, err)
	}
	snap, err := s.Current(ctx, c)
	if err != nil || snap.BatchID != "batch" || len(snap.Candidate.Positions) != 2 || snap.Candidate.Positions[0].Symbol != "FIRST" {
		t.Fatal(snap, err)
	}
	for _, src := range []QuerySource{source("1-0"), source("3-0")} {
		out, err = s.Ingest(ctx, "batch", src, first, time.Second)
		if err != nil || !out.Replayed || out.State != BatchReady {
			t.Fatal(out, err)
		}
	}
	if storedCount(t, db, "futures_query_observations") != 3 || storedCount(t, db, "futures_query_snapshots") != 1 {
		t.Fatal("duplicate projection")
	}
	var saved []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE entry_id='2-0'`).Scan(&saved); err != nil || !bytes.Equal(saved, last) {
		t.Fatal("raw bytes changed", err)
	}
}

func TestQueryStoreAssetPrecisionAndEmptyPositions(t *testing.T) {
	_, s, ctx := queryTestDB(t)
	c := batchContext("asset")
	registerQuery(t, s, ctx, "asset", c)
	raw := editBody(t, fixtureBody(t, "messages.json", "asset"), func(m map[string]any) {
		f := m["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)["futures"].(map[string]any)
		f["balance"], f["deposit"] = "-0", "0.0000000000000000000000001"
	})
	if out, err := s.Ingest(ctx, "asset", source("1-0"), raw, 0); err != nil || !out.Published {
		t.Fatal(out, err)
	}
	snap, err := s.Current(ctx, c)
	if err != nil || snap.Candidate.Assets[0].Futures.Balance != "-0" || snap.Candidate.Assets[0].Futures.Deposit != "0.0000000000000000000000001" {
		t.Fatal(snap, err)
	}
	c = batchContext("positions")
	registerQuery(t, s, ctx, "positions", c)
	raw = editBody(t, fixtureBody(t, "messages.json", "positions"), func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} })
	if out, err := s.Ingest(ctx, "positions", source("1-0"), raw, 0); err != nil || !out.Published {
		t.Fatal(out, err)
	}
	snap, err = s.Current(ctx, c)
	if err != nil || snap.Candidate.Positions == nil || len(snap.Candidate.Positions) != 0 {
		t.Fatal("empty snapshot not explicit", err)
	}
}

func TestQueryStoreConflictRetainsBothBodiesAndBlocks(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	raw := fixtureBody(t, "messages.json", "positions")
	if _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err != nil {
		t.Fatal(err)
	}
	changed := append(bytes.Clone(raw), ' ')
	out, err := s.Ingest(ctx, "batch", source("1-0"), changed, 0)
	if err != nil || out.State != BatchInvalid || out.Disposition != "source_conflict" {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_query_observations") != 2 || storedCount(t, db, "futures_query_snapshots") != 1 {
		t.Fatal("conflict destroyed evidence")
	}
	if _, err := s.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("invalid snapshot remained current", err)
	}
	if _, err := s.Snapshot(ctx, "batch"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("invalid snapshot exposed as valid history", err)
	}
	out, err = s.Ingest(ctx, "batch", source("1-0"), changed, 0)
	if err != nil || !out.Replayed || out.State != BatchInvalid {
		t.Fatal(out, err)
	}
}

func TestQueryStoreRollbackAndLostAcknowledgement(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	_, err := db.ExecContext(ctx, `CREATE FUNCTION reject_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN RAISE EXCEPTION 'injected snapshot failure'; END $$;
        CREATE TRIGGER snapshot_fault BEFORE INSERT ON futures_query_snapshots FOR EACH ROW EXECUTE FUNCTION reject_snapshot()`)
	if err != nil {
		t.Fatal(err)
	}
	raw := fixtureBody(t, "messages.json", "positions")
	if _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err == nil {
		t.Fatal("injected transaction failure ignored")
	}
	if storedCount(t, db, "futures_query_observations") != 0 || storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("partial transaction committed")
	}
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM futures_query_batches`).Scan(&state); err != nil || state != "collecting" {
		t.Fatal(state, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER snapshot_fault ON futures_query_snapshots`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err != nil {
		t.Fatal(err)
	}
	// The sender did not observe the previous commit response, so retries exactly.
	out, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0)
	if err != nil || !out.Replayed || !out.Published {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_query_observations") != 1 || storedCount(t, db, "futures_query_snapshots") != 1 {
		t.Fatal("commit replay duplicated data")
	}
}

func TestQueryStoreBackendDisconnectBeforeCommit(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	_, err := db.ExecContext(ctx, `CREATE FUNCTION disconnect_snapshot_writer() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN PERFORM pg_terminate_backend(pg_backend_pid()); RETURN NEW; END $$;
        CREATE TRIGGER disconnect_writer BEFORE INSERT ON futures_query_snapshots FOR EACH ROW EXECUTE FUNCTION disconnect_snapshot_writer()`)
	if err != nil {
		t.Fatal(err)
	}
	raw := fixtureBody(t, "messages.json", "positions")
	if _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err == nil {
		t.Fatal("terminated transaction reported committed")
	}
	if storedCount(t, db, "futures_query_observations") != 0 || storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("connection loss left half snapshot")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER disconnect_writer ON futures_query_snapshots`); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err != nil || !out.Published {
		t.Fatal("redelivery could not recover", out, err)
	}
}

func TestQueryStoreReceiverRestartFencesPartialAndComplete(t *testing.T) {
	db, old, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, old, ctx, "complete", c)
	raw := fixtureBody(t, "messages.json", "positions")
	if _, err := old.Ingest(ctx, "complete", source("1-0"), raw, 0); err != nil {
		t.Fatal(err)
	}
	c2 := c
	c2.OriginMessageID = "partial-command"
	registerQuery(t, old, ctx, "partial", c2)
	partial := editBody(t, page(t, "positions", "partial", 0, false, "FIRST"), func(m map[string]any) { m["origin_message_id"] = c2.OriginMessageID })
	if _, err := old.Ingest(ctx, "partial", source("2-0"), partial, 0); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewQueryStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("old receiver promoted to current", err)
	}
	if _, err := restarted.Ingest(ctx, "partial", source("3-0"), partial, 0); !errors.Is(err, ErrBatchOwner) {
		t.Fatal("old partial resumed", err)
	}
	if state, err := restarted.Recover(ctx, "partial"); err != nil || state != BatchInterrupted {
		t.Fatal(state, err)
	}
	if state, err := restarted.Recover(ctx, "complete"); err != nil || state != BatchReady {
		t.Fatal(state, err)
	}
	if _, err := restarted.Snapshot(ctx, "complete"); err != nil {
		t.Fatal("complete history lost", err)
	}
	if _, err := old.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("retired receiver still current", err)
	}
	if _, err := old.Ingest(ctx, "complete", source("1-0"), raw, 0); !errors.Is(err, ErrBatchOwner) {
		t.Fatal("retired old writer accepted", err)
	}
	if _, err := old.Ingest(ctx, "partial", source("3-0"), partial, 0); !errors.Is(err, ErrBatchOwner) {
		t.Fatal("retired partial accepted", err)
	}
}

func forCommand(t *testing.T, raw []byte, c QueryContext) []byte {
	return editBody(t, raw, func(m map[string]any) { m["origin_message_id"] = c.OriginMessageID })
}

func TestQueryStoreSupersessionAndFailedRefresh(t *testing.T) {
	_, s, ctx := queryTestDB(t)
	c1, c2, c3 := batchContext("positions"), batchContext("positions"), batchContext("positions")
	c2.OriginMessageID, c3.OriginMessageID = "new-command", "failed-command"
	registerQuery(t, s, ctx, "old", c1)
	registerQuery(t, s, ctx, "new", c2)
	base := fixtureBody(t, "messages.json", "positions")
	if out, err := s.Ingest(ctx, "new", source("1-0"), forCommand(t, base, c2), 0); err != nil || !out.Published {
		t.Fatal(out, err)
	}
	if out, err := s.Ingest(ctx, "old", source("1-0"), base, 0); err != nil || out.Published {
		t.Fatal("obsolete snapshot overwrote head", out, err)
	}
	registerQuery(t, s, ctx, "old", c1)
	if snap, err := s.Current(ctx, c2); err != nil || snap.BatchID != "new" {
		t.Fatal(snap, err)
	}
	if _, err := s.Snapshot(ctx, "old"); err != nil {
		t.Fatal("valid superseded history lost", err)
	}
	registerQuery(t, s, ctx, "failed", c3)
	if out, err := s.Ingest(ctx, "failed", source("1-0"), forCommand(t, failureBody(t, base), c3), 0); err != nil || out.State != BatchFailed {
		t.Fatal(out, err)
	}
	if snap, err := s.Current(ctx, c3); err != nil || snap.BatchID != "new" {
		t.Fatal("failed refresh cleared old snapshot", snap, err)
	}
	c4 := c3
	c4.OriginMessageID, c4.TradeDate, c4.CounterSessionID = "next-day", "20261105", "next-session"
	registerQuery(t, s, ctx, "next", c4)
	for _, expected := range []QueryContext{c3, c4} {
		if _, err := s.Current(ctx, expected); !errors.Is(err, ErrNoSnapshot) {
			t.Fatal("old session/day exposed as current", err)
		}
	}
}

func TestQueryStoreAuditFailureForeignAndExpiry(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	foreign := editBody(t, fixtureBody(t, "messages.json", "positions"), func(m map[string]any) { m["origin_message_id"] = "other-command" })
	out, err := s.Ingest(ctx, "batch", source("1-0"), foreign, 0)
	if err != nil || out.Disposition != "foreign" || out.State != BatchCollecting {
		t.Fatal(out, err)
	}
	if state, err := s.Advance(ctx, "batch", time.Minute); err != nil || state != BatchExpired {
		t.Fatal(state, err)
	}
	out, err = s.Ingest(ctx, "batch", source("2-0"), fixtureBody(t, "messages.json", "positions"), time.Minute)
	if err != nil || out.State != BatchExpired || out.Disposition != "rejected" {
		t.Fatal(out, err)
	}
	c.OriginMessageID = "bad-json"
	registerQuery(t, s, ctx, "bad", c)
	raw := []byte{0xff, 0, '{'}
	out, err = s.Ingest(ctx, "bad", source("1-0"), raw, 0)
	if err != nil || out.State != BatchInvalid {
		t.Fatal(out, err)
	}
	var saved []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE batch_id='bad'`).Scan(&saved); err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("malformed bytes lost", err)
	}
	if _, err := s.Advance(ctx, "bad", time.Second); err != nil {
		t.Fatal("invalid history cannot be loaded", err)
	}
}

func TestQueryStoreConcurrentReplay(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	registerQuery(t, s, ctx, "batch", batchContext("positions"))
	raw := fixtureBody(t, "messages.json", "positions")
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if storedCount(t, db, "futures_query_observations") != 1 || storedCount(t, db, "futures_query_snapshots") != 1 {
		t.Fatal("concurrent replay duplicated data")
	}
}

func TestQueryStoreConcurrentPagesAndRouteIsolation(t *testing.T) {
	_, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "pages", c)
	pages := [][]byte{page(t, "positions", "first", 0, false, "FIRST"), page(t, "positions", "last", 1, true, "SECOND")}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, raw := range pages {
		wg.Add(1)
		go func(i int, raw []byte) {
			defer wg.Done()
			_, err := s.Ingest(ctx, "pages", source(string(rune('a'+i))), raw, 0)
			results <- err
		}(i, raw)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if snap, err := s.Current(ctx, c); err != nil || len(snap.Candidate.Positions) != 2 {
		t.Fatal(snap, err)
	}
	for _, dimension := range []string{"env", "broker", "gateway", "account"} {
		other := batchContext("positions")
		switch dimension {
		case "env":
			other.Route.Env = "prod"
		case "broker":
			other.Route.BrokerID = "ctp_other"
		case "gateway":
			other.Route.GatewayID = "other-gateway"
		case "account":
			other.Route.AccountID = "990000000002"
		}
		if _, err := s.Current(ctx, other); !errors.Is(err, ErrNoSnapshot) {
			t.Fatal("route crossed before registration", err)
		}
		registerQuery(t, s, ctx, dimension, other)
		raw := editBody(t, fixtureBody(t, "messages.json", "positions"), func(m map[string]any) {
			m["routing"] = other.Route
			m["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)["account_id"] = other.Route.AccountID
		})
		if out, err := s.Ingest(ctx, dimension, source("1-0"), raw, 0); err != nil || !out.Published {
			t.Fatal(out, err)
		}
		if snap, err := s.Current(ctx, other); err != nil || snap.BatchID != dimension {
			t.Fatal("wrong route projection", snap, err)
		}
	}
	if snap, err := s.Current(ctx, c); err != nil || snap.BatchID != "pages" {
		t.Fatal("other routes overwrote original", snap, err)
	}
}

func TestQueryStoreInterruptAndAuditBackpressure(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	if _, err := s.Ingest(ctx, "batch", source("oversized"), make([]byte, maxAuditBytes+1), 0); !errors.Is(err, ErrAuditCapacity) {
		t.Fatal("oversized evidence accepted", err)
	}
	if storedCount(t, db, "futures_query_observations") != 0 {
		t.Fatal("capacity rejection persisted partial body")
	}
	if state, err := s.Interrupt(ctx, "batch", "disconnect"); err != nil || state != BatchInterrupted {
		t.Fatal(state, err)
	}
	if out, err := s.Ingest(ctx, "batch", source("late"), fixtureBody(t, "messages.json", "positions"), 0); err != nil || out.State != BatchInterrupted {
		t.Fatal(out, err)
	}
	if _, err := s.Snapshot(ctx, "batch"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("interrupted batch published", err)
	}
	c.OriginMessageID = "empty-body"
	registerQuery(t, s, ctx, "empty", c)
	if out, err := s.Ingest(ctx, "empty", source("empty"), nil, 0); err != nil || out.State != BatchInvalid {
		t.Fatal("empty raw body not audited", out, err)
	}
	if _, err := s.Advance(ctx, "empty", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestQueryStoreIdentityIntegrityAndMigrationDown(t *testing.T) {
	db, s, ctx := queryTestDB(t)
	c := batchContext("positions")
	registerQuery(t, s, ctx, "batch", c)
	registerQuery(t, s, ctx, "batch", c)
	if err := s.registerUnbound(ctx, "alias", c, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal("command registered twice", err)
	}
	other := c
	other.NativeBrokerID = "4901"
	if err := s.registerUnbound(ctx, "batch", other, time.Minute, DefaultBatchLimits()); !errors.Is(err, ErrBatchIdentity) {
		t.Fatal("context changed", err)
	}
	raw := fixtureBody(t, "messages.json", "positions")
	if _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_query_snapshots SET candidate='broken'::bytea`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(ctx, "batch"); err == nil {
		t.Fatal("snapshot corruption ignored")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_query_observations SET body='broken'::bytea`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, "batch", source("1-0"), raw, 0); err == nil {
		t.Fatal("audit corruption ignored")
	}
	for i := len(futuresTestMigrations) - 1; i >= 0; i-- {
		migrateFuturesTest(t, db, ctx, futuresTestMigrations[i], "down")
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema()`).Scan(&n); err != nil || n != 0 {
		t.Fatal("migration down left tables", n, err)
	}
}
