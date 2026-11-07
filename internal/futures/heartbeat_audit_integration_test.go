package futures

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestHeartbeatAuditPreservesSequenceAndDoesNotRestoreGuard(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	g, at := guardFixture(t)
	s, h := openStream(t, db, ctx, StreamConfig{g.scope, "hb", g.sourceID})
	good := guardBody(t, g, "good", at)
	down := editBody(t, guardBody(t, g, "down", at.Add(time.Second)), func(m map[string]any) {
		m["payload"].(map[string]any)["broker_ready"] = false
	})
	foreign := editBody(t, good, func(m map[string]any) { m["routing"].(map[string]any)["account_id"] = "990000000099" })
	bodies := [][]byte{good, down, {0xff, 0}, foreign, good}
	inputs := make([]StreamInput, len(bodies))
	for i, id := range []string{"1-0", "2-0", "3-0", "4-0", "5-0"} {
		inputs[i] = StreamInput{id, bodies[i]}
	}
	captured := captureStream(t, s, ctx, h, "0-0", inputs...)
	if err := s.Quarantine(ctx, h, captured.ReceiptIDs[1], "synthetic disconnect review"); err != nil {
		t.Fatal(err)
	}
	var after int64
	all := []HeartbeatAuditRecord{}
	for {
		page, err := s.HeartbeatAudit(ctx, h, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].Receipt.ID
	}
	if len(all) != len(bodies) {
		t.Fatal("history filtered or deduplicated", len(all))
	}
	for i, item := range all {
		if !bytes.Equal(item.Receipt.Raw, bodies[i]) || item.Receipt.ID != captured.ReceiptIDs[i] {
			t.Fatal("raw sequence changed", i)
		}
		invalid := i == 2 || i == 3
		if (item.Decoded == nil) != invalid || (item.DecodeError != "") != invalid {
			t.Fatal("incorrect audit decode", i, item.DecodeError)
		}
	}
	if all[1].Receipt.State != "quarantined" || all[1].Decoded.BrokerReady {
		t.Fatal("disconnect evidence hidden")
	}
	if !all[0].Decoded.ProducedAt.Equal(at) || all[0].Decoded.TradeDate != g.expected.Scope.TradeDate {
		t.Fatal("historical source timestamp or night-session day rewritten")
	}
	all[0].Receipt.Raw[0] = 'x'
	if !bytes.Equal(all[0].Decoded.Raw, good) {
		t.Fatal("decoded evidence aliases receipt")
	}
	if !errors.Is(g.Check(), ErrTestSessionMissing) {
		t.Fatal("audit restored runtime freshness")
	}
	status, err := s.Status(ctx, h)
	if err != nil || status.Pending != 4 || status.Quarantined != 1 || status.Delivered != 0 || status.Cursor != "5-0" {
		t.Fatal(status, err)
	}
}

func TestHeartbeatAuditOwnershipIsolationAndCorruption(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	g, at := guardFixture(t)
	s, old := openStream(t, db, ctx, StreamConfig{g.scope, "hb", g.sourceID})
	input := []StreamInput{{"1-0", guardBody(t, g, "one", at)}, {"2-0", guardBody(t, g, "two", at.Add(time.Second))}}
	captured := captureStream(t, s, ctx, old, "0-0", input...)
	h, err := s.Open(ctx, old.config)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := s.HeartbeatAudit(ctx, old, 0, 10); page != nil || !errors.Is(err, ErrStreamOwner) {
		t.Fatal("old owner remained readable", err)
	}
	if page, err := s.HeartbeatAudit(ctx, h, 0, 10); err != nil || len(page) != 2 {
		t.Fatal("recovery lost audit", err)
	}
	other := h.config
	other.Scope.Route.GatewayID += "-other"
	_, otherHandle := openStream(t, db, ctx, other)
	if page, err := s.HeartbeatAudit(ctx, otherHandle, 0, 10); err != nil || len(page) != 0 {
		t.Fatal("cross-gateway evidence exposed", err)
	}
	for _, bounds := range [][2]int{{-1, 1}, {0, 0}, {0, 257}} {
		if _, err := s.HeartbeatAudit(ctx, h, int64(bounds[0]), bounds[1]); !errors.Is(err, ErrAuditCapacity) {
			t.Fatal("unbounded audit", err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_stream_inbox SET body=$1 WHERE receipt_id=$2`, []byte("damaged"), captured.ReceiptIDs[1]); err != nil {
		t.Fatal(err)
	}
	if page, err := s.HeartbeatAudit(ctx, h, 0, 10); err == nil || page != nil {
		t.Fatal("corrupt page returned partial evidence", err)
	}
	if cursor, err := s.Cursor(ctx, h); err != nil || cursor != "2-0" || storedCount(t, db, "futures_stream_inbox") != 2 {
		t.Fatal("audit failure advanced checkpoint or deleted history", cursor, err)
	}
}

func TestHeartbeatCannotDispatchIntoTradeStores(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	s, h := openStream(t, db, ctx, streamTestConfig("hb"))
	r := captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", []byte("audit only")})
	if _, err := NewStreamDispatcher(ctx, s, h, nil, []EventDispatchBinding{{"unused", "session"}}, nil); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("heartbeat accepted by trade dispatcher", err)
	}
	if _, err := s.ForwardEvent(ctx, h, r.ReceiptIDs[0], "unused"); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("heartbeat forwarded to events", err)
	}
	if _, err := s.ForwardQuery(ctx, h, r.ReceiptIDs[0], nil, "unused", 0); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("heartbeat forwarded to queries", err)
	}
	_, event := openStream(t, db, ctx, streamTestConfig("event"))
	if _, err := s.HeartbeatAudit(ctx, event, 0, 1); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("non-heartbeat stream accepted by audit", err)
	}
}

func TestHeartbeatRedisArchiveRecovery(t *testing.T) {
	_, s, h, client, reader, ctx := realStreamRole(t, "hb")
	g, at := guardFixture(t)
	g.scope = h.config.Scope
	g.sourceID, g.stream = h.config.SourceID, h.stream
	raw := guardBody(t, g, "archived", at)
	appendRealStream(t, ctx, client, h, "2-0", "body", raw)
	appendRealStream(t, ctx, client, h, "3-0", "body", []byte{0xff, 0})
	source := &TestStreamSource{h.config.Scope, h.config.SourceID, reader}
	if r, err := source.ReadOnce(ctx, s, h, 2); err != nil || r.Cursor != "2-0" || len(r.ReceiptIDs) != 2 {
		t.Fatal(r, err)
	}
	// New local owner and TCP client resume the raw checkpoint, not a guard.
	next, err := s.Open(ctx, h.config)
	if err != nil {
		t.Fatal(err)
	}
	newClient := redis.NewClient(client.Options())
	t.Cleanup(func() { _ = newClient.Close() })
	newReader, err := NewRedisStreamReader(newClient)
	if err != nil {
		t.Fatal(err)
	}
	newSource := &TestStreamSource{h.config.Scope, h.config.SourceID, newReader}
	if r, err := newSource.ReadOnce(ctx, s, next, 2); err != nil || r.Cursor != "3-0" || len(r.ReceiptIDs) != 1 {
		t.Fatal(r, err)
	}
	if _, err := source.ReadOnce(ctx, s, h, 2); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("retired heartbeat reader not fenced", err)
	}
	page, err := s.HeartbeatAudit(ctx, next, 0, 10)
	if err != nil || len(page) != 3 || page[0].DecodeError == "" || page[1].Decoded == nil || page[2].DecodeError == "" {
		t.Fatal("raw history not preserved", err)
	}
	if !bytes.Equal(page[1].Receipt.Raw, raw) || !errors.Is(g.Check(), ErrTestSessionMissing) {
		t.Fatal("archive rewrote body or restored readiness")
	}
	if r, err := newSource.ReadOnce(ctx, s, next, 10); err != nil || !r.Replayed || len(r.ReceiptIDs) != 0 {
		t.Fatal(r, err)
	}
	if groups, err := client.XInfoGroups(ctx, h.stream).Result(); err != nil || len(groups) != 0 {
		t.Fatal("heartbeat archive created consumer group", err)
	}
	assertRealCursor(t, ctx, s, next, "3-0", 3)
}

func TestHeartbeatRedisArchiveFailureAndUnreadLoss(t *testing.T) {
	db, s, h, client, reader, ctx := realStreamRole(t, "hb")
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION stop_hb_cursor() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic heartbeat checkpoint failure'; END $$; CREATE TRIGGER stop_hb_cursor BEFORE UPDATE OF cursor_id ON futures_stream_receivers FOR EACH ROW EXECUTE FUNCTION stop_hb_cursor()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadOnce(ctx, h, reader, 1); err == nil {
		t.Fatal("checkpoint failure hidden")
	}
	assertRealCursor(t, ctx, s, h, "0-0", 0)
	if _, err := db.ExecContext(ctx, `DROP TRIGGER stop_hb_cursor ON futures_stream_receivers`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadOnce(ctx, h, reader, 1); err != nil {
		t.Fatal(err)
	}
	appendRealStream(t, ctx, client, h, "2-0", "body", "unread disconnect")
	appendRealStream(t, ctx, client, h, "3-0", "body", "later ready")
	if err := client.XDel(ctx, h.stream, "2-0").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadOnce(ctx, h, reader, 10); !errors.Is(err, ErrStreamHistory) {
		t.Fatal("unread disconnect loss ignored", err)
	}
	assertRealCursor(t, ctx, s, h, "1-0", 1)
}
