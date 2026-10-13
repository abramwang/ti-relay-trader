package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"ti-relay-trader/internal/timeutil"
)

type baselineFixture struct {
	query                 PositionBaselineQuery
	close, broker         baselineSnapshot
	closeJob, captureJob  baselineJobReport
	replies, assetReplies []baselineRawReply
}

func newBaselineFixture(t *testing.T, count int) baselineFixture {
	t.Helper()
	f := baselineFixture{query: PositionBaselineQuery{Environment: "production", AccountID: "a", BrokerID: "huaxin", GatewayID: "g", TradeDate: "2026-10-09", SnapshotType: "close", Limit: 500}}
	start := time.Date(2026, 10, 9, 15, 1, 0, 0, timeutil.Location())
	f.close.Exists, f.broker.Exists = true, true
	f.close.CapturedAt, f.broker.CapturedAt = start.Add(3*time.Second), start.Add(3*time.Second)
	f.close.Raw.RunID, f.broker.Raw.RunID = "close-run", "capture-run"
	f.close.Raw.InputSnapshotType = "broker_close"
	decode := func(value any, target any) {
		body, _ := json.Marshal(value)
		if err := json.Unmarshal(body, target); err != nil {
			t.Fatal(err)
		}
	}
	published := func(run, kind, input string) map[string]any {
		return map[string]any{"ok": true, "result": map[string]any{"run_id": run, "snapshot_type": kind, "input_snapshot_type": input, "completed_at": baselineTime(start.Add(4 * time.Second)), "accounts": []any{map[string]any{"account_id": "a", "asset_snapshot_written": true, "positions_count": count, "position_snapshots_written": count}}}}
	}
	decode(map[string]any{"settlement_snapshot": published("close-run", "close", "broker_close")}, &f.closeJob)
	terminal := func(action, id string, replies int) map[string]any {
		return map[string]any{"account_id": "a", "action": action, "origin_message_id": id, "success": true, "terminal": true, "terminal_count": 1, "reply_count": replies}
	}
	decode(map[string]any{"broker_close_snapshot": published("capture-run", "broker_close", ""), "accounts": []any{map[string]any{"account_id": "a", "refresh_freshness": map[string]any{"ok": true, "asset_fresh": true, "positions_fresh": true, "refresh_started_at": baselineTime(start), "query_terminals": map[string]any{"positions": terminal("account.positions.query", "positions-query", count+1), "asset": terminal("account.asset.query", "asset-query", 1)}}}}}, &f.captureJob)
	reply := func(id, action, result, msg, status string, last bool, payload any, at time.Time) baselineRawReply {
		body, _ := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "reply", "message_id": id, "origin_message_id": msg, "action": action, "result_type": result, "status": status, "produced_at": baselineTime(at), "routing": map[string]any{"env": "prod", "account_id": "a", "broker_id": "huaxin", "gateway_id": "g"}, "chunk": map[string]any{"is_last": last}, "payload": payload, "counter_session_id": "session-1"})
		return baselineRawReply{Body: body, ReceivedAt: at}
	}
	for i := 0; i < count; i++ {
		symbol := fmt.Sprintf("%06d", i)
		row := baselineSnapshotRow{Symbol: symbol, Exchange: "SH", Quantity: 100, SellableQty: 50, CapturedAt: f.close.CapturedAt}
		row.Raw.RunID, row.Raw.InputSnapshotType = "close-run", "broker_close"
		f.close.Rows = append(f.close.Rows, row)
		row.Raw.RunID, row.Raw.InputSnapshotType = "capture-run", ""
		f.broker.Rows = append(f.broker.Rows, row)
		f.replies = append(f.replies, reply(fmt.Sprintf("reply-%d", i), "account.positions.query", "position_page", "positions-query", "partial", false, map[string]any{"items": []any{map[string]any{"account_id": "a", "symbol": symbol, "exchange": "SH", "quantity": 100, "sellable_qty": 50}}}, start.Add(time.Second)))
	}
	f.replies = append(f.replies, reply("final", "account.positions.query", "position_page", "positions-query", "completed", true, map[string]any{"items": []any{}}, start.Add(2*time.Second)))
	f.assetReplies = []baselineRawReply{reply("asset-final", "account.asset.query", "asset_page", "asset-query", "completed", true, map[string]any{"account": map[string]any{"account_id": "a"}}, start.Add(time.Second))}
	return f
}

func (f baselineFixture) assemble() (PositionBaselinePage, error) {
	return assemblePositionBaseline(f.query, f.close, f.broker, f.closeJob, f.captureJob, f.replies, f.assetReplies)
}

func TestPositionBaselineCompletenessAndPagination(t *testing.T) {
	for _, count := range []int{0, 1, 501, 2001, 10001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newBaselineFixture(t, count)
			full, err := f.assemble()
			if err != nil || full.Baseline.Completeness != "complete" || !full.Baseline.MissingSecurityMeansZero || full.Baseline.TotalRows != count {
				t.Fatalf("baseline=%+v err=%v", full.Baseline, err)
			}
			repeated, _ := f.assemble()
			if repeated.Baseline.BaselineID != full.Baseline.BaselineID {
				t.Fatal("same source changed identity")
			}
			query := f.query
			seen := 0
			for {
				page, err := paginatePositionBaseline(full, query)
				if err != nil || page.Offset != seen || page.Baseline.BaselineID != full.Baseline.BaselineID {
					t.Fatalf("page failed: %v", err)
				}
				seen += page.Count
				if page.PageComplete {
					break
				}
				query.Cursor, query.BaselineID = page.NextCursor, page.Baseline.BaselineID
			}
			if seen != count {
				t.Fatalf("truncated: %d/%d", seen, count)
			}
		})
	}
}

func TestPositionBaselineIncompleteEvidenceFailsClosed(t *testing.T) {
	for _, name := range []string{"missing_asset", "missing_rows", "stale_row", "partial_query", "missing_page", "duplicate_page", "no_last", "no_items", "asset_failed", "wrong_account", "wrong_environment", "wrong_session", "missing_session", "incomplete_publication", "wrong_generation"} {
		t.Run(name, func(t *testing.T) {
			f := newBaselineFixture(t, 2)
			var body map[string]any
			_ = json.Unmarshal(f.replies[0].Body, &body)
			switch name {
			case "missing_asset":
				f.close.Exists = false
			case "missing_rows":
				f.close.Rows = f.close.Rows[:1]
			case "stale_row":
				f.close.Rows[0].Symbol = "999999"
			case "partial_query":
				f.captureJob.Accounts[0].Freshness.Terminals.Positions.Success = false
			case "missing_page":
				f.replies = f.replies[1:]
			case "duplicate_page":
				f.replies[1] = f.replies[0]
			case "no_last":
				f.replies = f.replies[:2]
			case "no_items":
				body["payload"] = map[string]any{}
			case "asset_failed":
				f.assetReplies = nil
			case "wrong_account":
				body["routing"].(map[string]any)["account_id"] = "b"
			case "wrong_environment":
				body["routing"].(map[string]any)["env"] = "test"
			case "wrong_session":
				body["counter_session_id"] = "session-2"
			case "missing_session":
				delete(body, "counter_session_id")
			case "incomplete_publication":
				f.closeJob.Settlement.Result.Accounts[0].Written = nil
			case "wrong_generation":
				f.close.Rows[0].Raw.RunID = "other-run"
			}
			if name == "no_items" || name == "wrong_account" || name == "wrong_environment" || name == "wrong_session" || name == "missing_session" {
				f.replies[0].Body, _ = json.Marshal(body)
			}
			page, err := f.assemble()
			if err != nil || page.Baseline.Completeness == "complete" || page.Baseline.MissingSecurityMeansZero {
				t.Fatalf("unsafe proof accepted: %+v err=%v", page.Baseline, err)
			}
		})
	}
	f := newBaselineFixture(t, 0)
	f.close.Exists, f.broker.Exists = false, false
	f.closeJob, f.captureJob = baselineJobReport{}, baselineJobReport{}
	f.replies, f.assetReplies = nil, nil
	page, _ := f.assemble()
	if page.Baseline.Completeness != "unavailable" || page.Baseline.MissingSecurityMeansZero {
		t.Fatal("no history fabricated an empty account")
	}
}

func TestPositionBaselineExplicitUpperBound(t *testing.T) {
	f := newBaselineFixture(t, 0)
	f.close.Rows = make([]baselineSnapshotRow, PositionBaselineMaxRows+1)
	if _, err := f.assemble(); !errors.Is(err, ErrPositionBaselineTooLarge) {
		t.Fatalf("over-limit was not explicit: %v", err)
	}
}

func TestPositionBaselineChangedGenerationRejectsOldCursor(t *testing.T) {
	f := newBaselineFixture(t, 3)
	full, _ := f.assemble()
	q := f.query
	q.Limit = 1
	first, _ := paginatePositionBaseline(full, q)
	f.close.Rows[0].Quantity++
	changed, _ := f.assemble()
	q.Cursor = first.NextCursor
	if _, err := paginatePositionBaseline(changed, q); !errors.Is(err, ErrPositionBaselineChanged) {
		t.Fatalf("mixed generations: %v", err)
	}
	q.BaselineID = "other-id"
	if _, err := paginatePositionBaseline(full, q); !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatalf("cursor scope mismatch: %v", err)
	}
}

func TestPositionBaselineLegacySessionAndCollectionEnd(t *testing.T) {
	f := newBaselineFixture(t, 1)
	for _, replies := range [][]baselineRawReply{f.replies, f.assetReplies} {
		for i := range replies {
			var body map[string]any
			_ = json.Unmarshal(replies[i].Body, &body)
			delete(body, "counter_session_id")
			replies[i].Body, _ = json.Marshal(body)
		}
	}
	f.assetReplies[0].ReceivedAt = f.broker.CapturedAt
	page, err := f.assemble()
	if err != nil || page.Baseline.Completeness != "complete" || page.Baseline.CounterSessionID != "" || page.Baseline.Proof["counter_session_provided"] || page.Baseline.CollectionCompletedAt != baselineTime(f.broker.CapturedAt) {
		t.Fatalf("legacy optional session / latest collection time: %+v err=%v", page.Baseline, err)
	}
}

func TestPositionBaselineIsolatedSQLLargeAndRebuild(t *testing.T) {
	dsn := os.Getenv("RELAY_POSITION_BASELINE_ISOLATED_DATABASE_URL")
	if dsn == "" {
		t.Skip("opt-in connection-local TEMP table integration")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// These connection-local tables shadow real names; no durable ledger is written.
	for _, ddl := range []string{
		`CREATE TEMP TABLE asset_snapshots(account_id text,trade_date date,snapshot_type text,captured_at timestamptz,raw_payload jsonb)`,
		`CREATE TEMP TABLE position_snapshots(account_id text,trade_date date,snapshot_type text,symbol text,exchange text,quantity bigint,sellable_qty bigint,captured_at timestamptz,raw_payload jsonb)`,
		`CREATE TEMP TABLE job_runs(trade_date date,job_name text,finished_at timestamptz,updated_at timestamptz,report_json jsonb)`,
		`CREATE TEMP TABLE raw_stream_messages(raw_message_pk bigint,account_id text,origin_message_id text,stream_role text,body text,parse_error text,received_at timestamptz)`,
	} {
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	f := newBaselineFixture(t, 10001)
	for kind, snapshot := range map[string]baselineSnapshot{"close": f.close, "broker_close": f.broker} {
		raw, _ := json.Marshal(snapshot.Raw)
		if _, err := conn.ExecContext(ctx, `INSERT INTO pg_temp.asset_snapshots VALUES('a','2026-10-09',$1,$2,$3)`, kind, snapshot.CapturedAt, string(raw)); err != nil {
			t.Fatal(err)
		}
		rows := make([]map[string]any, 0, len(snapshot.Rows))
		for _, r := range snapshot.Rows {
			rows = append(rows, map[string]any{"symbol": r.Symbol, "exchange": r.Exchange, "quantity": r.Quantity, "sellable_qty": r.SellableQty, "captured_at": r.CapturedAt, "raw_payload": r.Raw})
		}
		body, _ := json.Marshal(rows)
		if _, err := conn.ExecContext(ctx, `INSERT INTO pg_temp.position_snapshots SELECT 'a','2026-10-09',$1,symbol,exchange,quantity,sellable_qty,captured_at,raw_payload FROM jsonb_to_recordset($2::jsonb) AS r(symbol text,exchange text,quantity bigint,sellable_qty bigint,captured_at timestamptz,raw_payload jsonb)`, kind, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	for job, report := range map[string]baselineJobReport{"post_close_settlement": f.closeJob, "post_close_capture": f.captureJob} {
		body, _ := json.Marshal(report)
		if _, err := conn.ExecContext(ctx, `INSERT INTO pg_temp.job_runs VALUES('2026-10-09',$1,$2,$2,$3)`, job, f.close.CapturedAt.Add(time.Second), string(body)); err != nil {
			t.Fatal(err)
		}
	}
	archive := []map[string]any{}
	for _, raw := range append(f.replies, f.assetReplies...) {
		var reply baselineReplyEnvelope
		_ = json.Unmarshal(raw.Body, &reply)
		archive = append(archive, map[string]any{"raw_message_pk": len(archive) + 1, "origin_message_id": reply.OriginMessageID, "body": string(raw.Body), "received_at": raw.ReceivedAt})
	}
	body, _ := json.Marshal(archive)
	if _, err := conn.ExecContext(ctx, `INSERT INTO pg_temp.raw_stream_messages SELECT raw_message_pk,'a',origin_message_id,'reply',body,'',received_at FROM jsonb_to_recordset($1::jsonb) AS r(raw_message_pk bigint,origin_message_id text,body text,received_at timestamptz)`, string(body)); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(conn)
	first, err := repo.GetPositionBaseline(ctx, f.query)
	if err != nil || first.Baseline.Completeness != "complete" || first.Baseline.TotalRows != 10001 {
		t.Fatalf("large SQL baseline %+v err=%v", first.Baseline, err)
	}
	q := f.query
	q.BaselineID = first.Baseline.BaselineID
	all := append([]PositionBaselineRow{}, first.Positions...)
	page := first
	for !page.PageComplete {
		q.Cursor = page.NextCursor
		page, err = repo.GetPositionBaseline(ctx, q)
		if err != nil || page.Baseline.BaselineID != first.Baseline.BaselineID || page.Offset != len(all) {
			t.Fatalf("SQL page mismatch: %v", err)
		}
		all = append(all, page.Positions...)
	}
	if len(all) != 10001 || PositionBaselineRowsDigest(all) != first.Baseline.ContentSHA256 {
		t.Fatal("SQL baseline silently truncated or mixed rows")
	}
	if _, err := conn.ExecContext(ctx, `UPDATE pg_temp.position_snapshots SET quantity=quantity+1 WHERE snapshot_type='close' AND symbol='000000'`); err != nil {
		t.Fatal(err)
	}
	q.Cursor = first.NextCursor
	if _, err := repo.GetPositionBaseline(ctx, q); !errors.Is(err, ErrPositionBaselineChanged) {
		t.Fatalf("SQL rebuild mixed generations: %v", err)
	}
}

func TestPositionBaselineExistingHistoricalReadOnly(t *testing.T) {
	dsn := os.Getenv("RELAY_POSITION_BASELINE_LIVE_DATABASE_URL")
	if dsn == "" {
		t.Skip("opt-in read-only live baseline acceptance")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, sample := range []struct {
		account, date string
		count         int
	}{{"314000046830", "2026-10-09", 13}, {"314000045768", "2026-10-09", 1}, {"307000051388", "2026-10-09", 7}, {"307000051387", "2026-10-09", 1}, {"307000051389", "2026-09-18", 0}} {
		q := PositionBaselineQuery{Environment: "production", AccountID: sample.account, BrokerID: "huaxin", GatewayID: sample.account, TradeDate: sample.date, SnapshotType: "close", Limit: 2}
		page, err := NewRepository(db).GetPositionBaseline(ctx, q)
		if err != nil || page.Baseline.Completeness != "complete" || page.Baseline.TotalRows != sample.count {
			t.Fatalf("%s %s: %+v err=%v", sample.account, sample.date, page.Baseline, err)
		}
		t.Logf("%s %s count=%d id=%s", sample.account, sample.date, sample.count, page.Baseline.BaselineID)
	}
}
