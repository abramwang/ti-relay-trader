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

	"github.com/redis/go-redis/v9"
)

func streamTestConfig(role string) StreamConfig {
	s := tradeScope()
	return StreamConfig{CommandScope{s.Route, s.NativeBrokerID, s.InvestorID, "FIXTURE_USER"}, role, "synthetic-redis-deployment"}
}
func openStream(t *testing.T, db *sql.DB, ctx context.Context, c StreamConfig) (*StreamStore, StreamHandle) {
	t.Helper()
	s, err := NewStreamStore(db)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}
func captureStream(t *testing.T, s *StreamStore, ctx context.Context, h StreamHandle, cursor string, items ...StreamInput) StreamCapture {
	t.Helper()
	r, err := s.Capture(ctx, h, cursor, items)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStreamCaptureRestartAndIsolation(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	cfg := streamTestConfig("event")
	s, h := openStream(t, db, ctx, cfg)
	items := []StreamInput{{"9-9", []byte{0xff, 0}}, {"10-0", []byte(`{"foreign":"body"}`)}}
	r := captureStream(t, s, ctx, h, "0-0", items...)
	if r.Cursor != "10-0" || r.Replayed {
		t.Fatal(r)
	}
	s, h2 := openStream(t, db, ctx, cfg)
	if _, err := s.Capture(ctx, h, "10-0", []StreamInput{{"11-0", nil}}); !errors.Is(err, ErrStreamOwner) {
		t.Fatal(err)
	}
	if _, err := s.Pending(ctx, h, 0, 10); !errors.Is(err, ErrStreamOwner) {
		t.Fatal(err)
	}
	p, err := s.Pending(ctx, h2, 0, 1)
	if err != nil || len(p) != 1 || !bytes.Equal(p[0].Raw, items[0].Raw) {
		t.Fatal(p, err)
	}
	p, err = s.Pending(ctx, h2, p[0].ID, 10)
	if err != nil || len(p) != 1 || !bytes.Equal(p[0].Raw, items[1].Raw) {
		t.Fatal(p, err)
	}
	if _, err := s.Capture(ctx, h2, "0-0", items); !errors.Is(err, ErrStreamCursor) {
		t.Fatal("stale cursor", err)
	}
	if again := captureStream(t, s, ctx, h2, "10-0", items...); !again.Replayed || again.ReceiptIDs[0] != r.ReceiptIDs[0] {
		t.Fatal(again)
	}
	for _, change := range []func(*StreamConfig){func(c *StreamConfig) { c.SourceID = "restored" }, func(c *StreamConfig) { c.Scope.UserID = "OTHER" }, func(c *StreamConfig) { c.Scope.Route.AccountID = "123456" }} {
		c := cfg
		change(&c)
		if _, err := s.Open(ctx, c); !errors.Is(err, ErrStreamIdentity) {
			t.Fatal(err)
		}
	}
	if _, err := s.Cursor(ctx, h2); err != nil {
		t.Fatal("failed config drift retired live reader", err)
	}
	c := cfg
	c.Scope.Route.Env = "prod"
	_, other := openStream(t, db, ctx, c)
	if cursor, err := s.Cursor(ctx, other); err != nil || cursor != "0-0" {
		t.Fatal(cursor, err)
	}
	if _, err := s.Record(ctx, other, r.ReceiptIDs[0]); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross env read", err)
	}
	status, err := s.Status(ctx, h2)
	if err != nil || status.Pending != 2 || status.Delivered != 0 {
		t.Fatal(status, err)
	}
}

func TestStreamCaptureAtomicFailureCapacityAndReplay(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("reply"))
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION stop_cursor() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic cursor failure'; END $$; CREATE TRIGGER stop_cursor BEFORE UPDATE OF cursor_id ON futures_stream_receivers FOR EACH ROW EXECUTE FUNCTION stop_cursor()`); err != nil {
		t.Fatal(err)
	}
	items := []StreamInput{{"1-0", []byte("first")}, {"2-0", []byte("second")}}
	if _, err := s.Capture(ctx, h, "0-0", items); err == nil {
		t.Fatal("failure hidden")
	}
	if storedCount(t, db, "futures_stream_inbox") != 0 {
		t.Fatal("partial commit")
	}
	if cursor, err := s.Cursor(ctx, h); err != nil || cursor != "0-0" {
		t.Fatal(cursor, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER stop_cursor ON futures_stream_receivers`); err != nil {
		t.Fatal(err)
	}
	r := captureStream(t, s, ctx, h, "0-0", items...)
	if _, err := s.Capture(ctx, h, "2-0", []StreamInput{{"2-0", []byte("changed")}, {"3-0", []byte("third")}}); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal(err)
	}
	if storedCount(t, db, "futures_stream_inbox") != 2 {
		t.Fatal("conflict skipped")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_receivers SET audit_count=50000`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, h, "2-0", []StreamInput{{"3-0", []byte("third")}}); !errors.Is(err, ErrStreamCounters) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_receivers SET audit_count=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, h, "2-0", []StreamInput{{"0-1", []byte("missing older")}}); !errors.Is(err, ErrStreamCursor) {
		t.Fatal(err)
	}
	if rec, err := s.Record(ctx, h, r.ReceiptIDs[1]); err != nil || !bytes.Equal(rec.Raw, items[1].Raw) {
		t.Fatal(rec, err)
	}
}

func TestStreamConcurrentCaptureOneCheckpoint(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("event"))
	var wg sync.WaitGroup
	out := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Capture(ctx, h, "0-0", []StreamInput{{"1-0", []byte("same")}})
			out <- err
		}()
	}
	wg.Wait()
	close(out)
	success := 0
	for err := range out {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrStreamCursor) {
			t.Fatal(err)
		}
	}
	if success != 1 || storedCount(t, db, "futures_stream_inbox") != 1 {
		t.Fatal(success)
	}
}

func TestStreamEventAtomicHandoffAndWaitingIdentity(t *testing.T) {
	db, events, _, ctx, key := eventTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("event"))
	raw := fixtureBody(t, "messages.json", "fill")
	r := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw})
	for _, change := range []func(*TradeScope){
		func(scope *TradeScope) { scope.Route.Env = "prod" },
		func(scope *TradeScope) { scope.TradeDate = "20261105" },
	} {
		other := tradeScope()
		change(&other)
		wrong, err := events.RegisterScope(ctx, other, "FIXTURE_USER", eventStream(other))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ForwardEvent(ctx, h, r.ReceiptIDs[0], wrong); err == nil {
			t.Fatal("wrong environment/day accepted")
		}
	}
	if storedCount(t, db, "futures_event_inbox") != 0 {
		t.Fatal("wrong target polluted event inbox")
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic marker failure'; END $$; CREATE TRIGGER fail_handoff BEFORE UPDATE ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION fail_handoff()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForwardEvent(ctx, h, r.ReceiptIDs[0], key); err == nil {
		t.Fatal("marker failure ignored")
	}
	if storedCount(t, db, "futures_event_inbox") != 0 {
		t.Fatal("event committed without handoff")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_handoff ON futures_stream_inbox`); err != nil {
		t.Fatal(err)
	}
	out, err := s.ForwardEvent(ctx, h, r.ReceiptIDs[0], key)
	if err != nil || out.State != "pending" {
		t.Fatal(out, err)
	}
	if again, err := s.ForwardEvent(ctx, h, r.ReceiptIDs[0], key); err != nil || !again.Replayed || again.EventID != out.EventID {
		t.Fatal(again, err)
	}
	processEvent(t, events, ctx, out.EventID, "waiting_identity")
	registerSubmit(t, events, ctx, key)
	processEvent(t, events, ctx, out.EventID, "applied")
	status, err := s.Status(ctx, h)
	if err != nil || status.Delivered != 1 || status.Pending != 0 {
		t.Fatal(status, err)
	}
	if storedCount(t, db, "futures_execution_facts") != 1 {
		t.Fatal("fill not applied")
	}
	if err := s.Quarantine(ctx, h, r.ReceiptIDs[0], "cannot erase downstream responsibility"); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal(err)
	}
}

func TestStreamQueryAtomicHandoffAndUnboundRefusal(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	c := batchContext("positions")
	r, c := registeredQueryCommand(t, db, ctx, c)
	bindQuery(t, q, ctx, "bound", r, c)
	s, h := openStream(t, db, ctx, StreamConfig{c.commandScope(), "reply", "synthetic-redis-deployment"})
	raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
	in := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", raw})
	if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[0], q, "missing", 0); err == nil {
		t.Fatal("unknown request bound from reply")
	}
	other := c
	other.OriginMessageID = "another-query"
	other.Action = "account.asset.query"
	r2, other := registeredQueryCommand(t, db, ctx, other)
	bindQuery(t, q, ctx, "other", r2, other)
	if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[0], q, "other", 0); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("foreign origin was handed to another query", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_query_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic handoff failure'; END $$; CREATE TRIGGER fail_query_handoff BEFORE UPDATE ON futures_stream_inbox FOR EACH ROW EXECUTE FUNCTION fail_query_handoff()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[0], q, "bound", 0); err == nil {
		t.Fatal("failure ignored")
	}
	if storedCount(t, db, "futures_query_observations") != 0 || storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("partial query commit")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_query_handoff ON futures_stream_inbox`); err != nil {
		t.Fatal(err)
	}
	if out, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[0], q, "bound", 0); err != nil || !out.Published || out.State != BatchReady {
		t.Fatal(out, err)
	}
	if out, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[0], q, "bound", time.Second); err != nil || !out.Replayed {
		t.Fatal(out, err)
	}
	if storedCount(t, db, "futures_query_observations") != 1 {
		t.Fatal("duplicate page")
	}
	stored, err := s.Record(ctx, h, in.ReceiptIDs[0])
	if err != nil || !stored.BatchID.Valid || stored.BatchID.String != "bound" {
		t.Fatal(stored, err)
	}
}

func TestStreamRestartNeverResumesOldQuery(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	bindQuery(t, q, ctx, "bound", r, c)
	cfg := StreamConfig{c.commandScope(), "reply", "synthetic-redis-deployment"}
	s, h := openStream(t, db, ctx, cfg)
	first := boundReply(t, page(t, "positions", "first", 0, false, "FIRST"), r)
	last := boundReply(t, page(t, "positions", "last", 1, true, "LAST"), r)
	in := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", first}, StreamInput{"2-0", last})
	if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[0], q, "bound", 0); err != nil {
		t.Fatal(err)
	}
	s, h = openStream(t, db, ctx, cfg)
	next, _ := NewQueryStore(db)
	if state, err := next.Recover(ctx, "bound"); err != nil || state != BatchInterrupted {
		t.Fatal(state, err)
	}
	if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[1], next, "bound", time.Second); !errors.Is(err, ErrBatchOwner) {
		t.Fatal(err)
	}
	if _, err := s.ForwardQuery(ctx, h, in.ReceiptIDs[1], q, "bound", time.Second); !errors.Is(err, ErrBatchOwner) {
		t.Fatal(err)
	}
	p, err := s.Pending(ctx, h, 0, 10)
	if err != nil || len(p) != 1 || !bytes.Equal(p[0].Raw, last) {
		t.Fatal(p, err)
	}
	if storedCount(t, db, "futures_query_snapshots") != 0 {
		t.Fatal("old half-pages revived")
	}
}

func TestStreamQuarantineIntegrityAndDowngrade(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("reply"))
	in := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", []byte{0xff, 0}})
	if err := s.Quarantine(ctx, h, in.ReceiptIDs[0], "malformed UTF-8: investigation required"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Pending(ctx, h, 0, 10); err != nil || len(p) != 0 {
		t.Fatal(p, err)
	}
	if status, err := s.Status(ctx, h); err != nil || status.Quarantined != 1 || status.Delivered != 0 {
		t.Fatal(status, err)
	}
	if r, err := s.Record(ctx, h, in.ReceiptIDs[0]); err != nil || !bytes.Equal(r.Raw, []byte{0xff, 0}) {
		t.Fatal(r, err)
	}
	raw, err := os.ReadFile("../../migrations/postgres/000040_futures_stream_inbox.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, string(raw))
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("destructive downgrade")
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_inbox SET body='corrupt'::bytea`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(ctx, h, in.ReceiptIDs[0]); err == nil {
		t.Fatal("checksum ignored")
	}
}

type fakeStreamReader struct {
	info      *redis.XInfoStream
	rows      []redis.XStream
	err       error
	read      func(*redis.XReadArgs)
	infoCalls int
	onInfo    func(int)
}

func (f *fakeStreamReader) XInfoStream(ctx context.Context, key string) *redis.XInfoStreamCmd {
	f.infoCalls++
	if f.onInfo != nil {
		f.onInfo(f.infoCalls)
	}
	c := redis.NewXInfoStreamCmd(ctx, key)
	if f.err != nil {
		c.SetErr(f.err)
	} else {
		v := *f.info
		c.SetVal(&v)
	}
	return c
}
func (f *fakeStreamReader) ReadStream(ctx context.Context, key, cursor string, limit int) (any, error) {
	if f.read != nil {
		f.read(&redis.XReadArgs{Streams: []string{key, cursor}, Count: int64(limit), Block: -1})
	}
	if len(f.rows) == 0 {
		return nil, redis.Nil
	}
	var result []any
	for _, stream := range f.rows {
		var entries []any
		for _, message := range stream.Messages {
			var fields []any
			for key, value := range message.Values {
				fields = append(fields, key, value)
			}
			entries = append(entries, []any{message.ID, fields})
		}
		result = append(result, []any{stream.Stream, entries})
	}
	return result, nil
}

func TestStreamReadOnceFencesInFlightAndLoss(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	cfg := streamTestConfig("event")
	s, h := openStream(t, db, ctx, cfg)
	reader := &fakeStreamReader{info: &redis.XInfoStream{Length: 1, EntriesAdded: 1, FirstEntry: redis.XMessage{ID: "1-0"}, LastGeneratedID: "1-0", MaxDeletedEntryID: "0-0"}, rows: []redis.XStream{{Stream: h.stream, Messages: []redis.XMessage{{ID: "1-0", Values: map[string]any{"body": "bad JSON retained"}}}}}}
	var next StreamHandle
	reader.read = func(a *redis.XReadArgs) {
		if a.Block >= 0 || a.Count != 10 || len(a.Streams) != 2 || a.Streams[1] != "0-0" {
			t.Fatal(a)
		}
		var err error
		next, err = s.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ReadOnce(ctx, h, reader, 10); !errors.Is(err, ErrStreamOwner) {
		t.Fatal(err)
	}
	if storedCount(t, db, "futures_stream_inbox") != 0 {
		t.Fatal("retired reader moved cursor")
	}
	reader.read = nil
	reader.onInfo = func(n int) {
		if n%2 == 0 {
			reader.info.MaxDeletedEntryID = "1-0"
		}
	}
	if _, err := s.ReadOnce(ctx, next, reader, 10); !errors.Is(err, ErrStreamHistory) {
		t.Fatal(err)
	}
	if storedCount(t, db, "futures_stream_inbox") != 0 {
		t.Fatal("loss skipped")
	}
	reader.onInfo = nil
	reader.info.MaxDeletedEntryID = "0-0"
	out, err := s.ReadOnce(ctx, next, reader, 10)
	if err != nil || out.Cursor != "1-0" {
		t.Fatal(out, err)
	}
	reader.rows = nil
	if out, err := s.ReadOnce(ctx, next, reader, 10); err != nil || out.Cursor != "1-0" || len(out.ReceiptIDs) != 0 {
		t.Fatal(out, err)
	}
	reader.err = fmt.Errorf("synthetic missing stream")
	if _, err := s.ReadOnce(ctx, next, reader, 10); err == nil {
		t.Fatal("missing stream became empty success")
	}
}

func TestStreamDatabaseDisconnectDoesNotMoveCursor(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("reply"))
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION cut_stream_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_terminate_backend(pg_backend_pid()); RETURN NEW; END $$; CREATE TRIGGER cut_stream_commit BEFORE UPDATE OF cursor_id ON futures_stream_receivers FOR EACH ROW EXECUTE FUNCTION cut_stream_commit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(ctx, h, "0-0", []StreamInput{{"1-0", []byte("durability test")}}); err == nil {
		t.Fatal("disconnect ignored")
	}
	if cursor, err := s.Cursor(ctx, h); err != nil || cursor != "0-0" || storedCount(t, db, "futures_stream_inbox") != 0 {
		t.Fatal(cursor, err)
	}
}
