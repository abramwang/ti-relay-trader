package futures

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestTerminalDatabaseObservationReadOnly(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	r, c := registeredQueryCommand(t, db, ctx, batchContext("positions"))
	target := publicationTarget()
	target.CounterSessionID = c.CounterSessionID
	target.TradeDate = c.TradeDate
	seedPublishedReview(t, db, ctx, r, target)
	raw := boundReply(t, fixtureBody(t, "messages.json", "positions"), r)
	hash := sha256.Sum256(raw)
	key := queryReplyStream(c.Route)
	receiver, _ := json.Marshal(StreamConfig{Scope: c.commandScope(), Role: "reply", SourceID: target.SourceID})
	_, err := db.ExecContext(ctx, `INSERT INTO futures_stream_receivers(stream_key,config_json,owner_id) VALUES($1,$2,'fixture-owner')`, key, receiver)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO futures_stream_inbox(stream_key,entry_id,body,body_sha256) VALUES($1,'1-0',$2,$3)`, key, raw, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	reader := &TerminalReader{db: db, reviews: map[int64]QueryRequestReview{r.CommandID: {CommandID: r.CommandID, Query: c, Target: target}}}
	a := TerminalAccountConfig{Scope: c.commandScope()}
	for i := 0; i < 2; i++ {
		if i == 1 {
			reader.reviews = map[int64]QueryRequestReview{}
		}
		rows, err := reader.queries(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		want := "ready"
		if i == 1 {
			want = "response_ready"
		}
		if len(rows) != 4 || rows[1].State != want || rows[1].RowCount == nil || *rows[1].RowCount < 1 || rows[0].State != "not_queried" || rows[1].ResponseValidated != (i == 1) || rows[1].Reviewed != (i == 0) {
			t.Fatal(rows)
		}
	}
	var state, cursor string
	if err := db.QueryRowContext(ctx, `SELECT state FROM futures_stream_inbox`).Scan(&state); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT cursor_id FROM futures_stream_receivers`).Scan(&cursor); err != nil || cursor != "0-0" {
		t.Fatal(cursor, err)
	}
	if storedCount(t, db, "futures_query_snapshots") != 0 || storedCount(t, db, "futures_test_query_reviews") != 0 {
		t.Fatal("display published business state")
	}
	_, err = db.ExecContext(ctx, `UPDATE futures_test_query_publications SET state='attempted',entry_id=NULL,finished_at=NULL`)
	if err != nil {
		t.Fatal(err)
	}
	views, err := reader.queries(ctx, a)
	if err != nil || views[1].State != "publication_unknown" || views[1].RowCount != nil {
		t.Fatal(views, err)
	}
	_, err = db.ExecContext(ctx, `UPDATE futures_test_query_publications SET state='published',entry_id='2-0',finished_at=clock_timestamp()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE futures_stream_receivers SET config_json=jsonb_set(config_json,'{SourceID}','"foreign-source"')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.queries(ctx, a); err == nil {
		t.Fatal("foreign transport source accepted")
	}
	if _, err = db.ExecContext(ctx, `UPDATE futures_stream_receivers SET config_json=$1`, receiver); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE futures_stream_inbox SET body_sha256=$1`, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.queries(ctx, a); err == nil {
		t.Fatal("tampered audit accepted")
	}
}
