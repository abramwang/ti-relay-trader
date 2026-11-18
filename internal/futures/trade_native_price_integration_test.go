package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNativePriceLocalRedisCapture(t *testing.T) {
	db, store, ctx := assessmentDB(t)
	now := time.Now()
	v, _, target, approval, _ := executionTradeFixture(t, now)
	local := newLocalStorageRedis(t)
	config := TestSourceConfig{Scope: v.scope(), DeploymentID: "synthetic-native-price", RedisURL: "unix://" + local.socket}
	source, err := NewTestStreamSource(config)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target.SourceID = source.String()
	info, err := source.reader.client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	target.ServerRunID = redisStorageField(info, "run_id")
	publisher, err := NewTestQueryPublisher(config, target)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	for _, suffix := range []string{"cmd.query", "cmd.trade"} {
		if err := source.reader.client.XGroupCreateMkStream(ctx, publisher.prefix()+":"+suffix, target.Group, "0-0").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.reader.client.Set(ctx, publisher.prefix()+":cmd.trade:transport-epoch", target.Epoch, 0).Err(); err != nil {
		t.Fatal(err)
	}
	heartbeat := editBody(t, executionAuthorityHeartbeats(t)[0], func(m map[string]any) {
		m["routing"], m["counter_session_id"], m["produced_at"] = v.scope().Route, target.CounterSessionID, assessmentTime(time.Now())
		p := m["payload"].(map[string]any)
		p["counter_session_id"], p["trade_date"] = target.CounterSessionID, target.TradeDate
	})
	residentAppend(t, ctx, source, publisher.prefix()+":hb", "1-0", string(heartbeat))
	q, _, mapping := nativePriceFixture(t, target, v.scope(), now)
	q, err = store.Register(ctx, v.scope(), "cmd.query", q.Original.Raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishTestQuery(ctx, q.CommandID, q.Original.MessageID, publisher); err != nil {
		t.Fatal(err)
	}
	// Generate fake SDK observations after the actual isolated publication.
	_, raw, _ := nativePriceFixture(t, target, v.scope(), time.Now().Add(600*time.Millisecond))
	timer := time.NewTimer(650 * time.Millisecond)
	select {
	case <-ctx.Done():
		timer.Stop()
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	residentAppend(t, ctx, source, queryReplyStream(v.scope().Route), "1-0", string(raw))
	inbox, _ := NewStreamStore(db)
	h, err := inbox.OpenReplyCapture(ctx, source.StreamConfig("reply"), HeartbeatCheckpoint{Cursor: "0-0"}, target)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.ReadOnce(ctx, inbox, h, 32)
	if err != nil || len(capture.ReceiptIDs) != 1 {
		t.Fatal(capture, err)
	}
	prepared, err := store.PrepareTestNativePrice(ctx, v.scope(), target, q.CommandID, capture.ReceiptIDs[0], mapping)
	if err != nil || prepared.SuggestedLimitPrice != "5218" {
		t.Fatal(prepared, err)
	}
	approval.Market = &prepared.Market
	trade, err := NewTestTradePublisher(config, target, approval)
	if err != nil {
		t.Fatal(err)
	}
	defer trade.Close()
	approval.Market.NativeFacts.BodySHA256 = strings.Repeat("f", 64)
	if trade.approval.Market.NativeFacts.BodySHA256 != commandDigest(raw) {
		t.Fatal("publisher shares mutable reference")
	}
	// Read-only preparation never writes a trade or advances business delivery.
	if n, err := source.reader.client.XLen(ctx, publisher.prefix()+":cmd.trade").Result(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	status, err := inbox.Status(ctx, h)
	if err != nil || status.Pending != 1 || status.Delivered != 0 {
		t.Fatal(status, err)
	}
	if storedCount(t, db, "futures_test_trade_publications") != 0 {
		t.Fatal("price preparation reserved a trade")
	}
	// An epoch switch invalidates the retained price source, even though the
	// original PG receipt remains available for audit.
	other := target
	other.Epoch = "other-epoch"
	if _, err := store.PrepareTestNativePrice(ctx, v.scope(), other, q.CommandID, capture.ReceiptIDs[0], mapping); err == nil {
		t.Fatal("wrong binding accepted")
	}
}

func storeNativePriceFixture(t *testing.T, db *sql.DB, store *CommandStore, ctx context.Context, scope CommandScope, target TestQueryTarget) (CommandRegistration, int64, []byte, CatalogBinding) {
	t.Helper()
	now := time.Now()
	q, raw, mapping := nativePriceFixture(t, target, scope, now)
	q, err := store.Register(ctx, scope, "cmd.query", q.Original.Raw, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.publishTestQuery(ctx, scope, q.CommandID, q.Original.MessageID, target, &fakeQueryPublisher{}); err != nil {
		t.Fatal(err)
	}
	// Synthetic SDK clock only. Never mutate a live publication timestamp.
	if _, err := db.ExecContext(ctx, `UPDATE futures_test_query_publications SET attempted_at=$2 WHERE command_id=$1`, q.CommandID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	stream, _ := NewStreamStore(db)
	h, err := stream.OpenReplyCapture(ctx, StreamConfig{Scope: scope, SourceID: target.SourceID, Role: "reply"}, HeartbeatCheckpoint{Cursor: "0-0"}, target)
	if err != nil {
		t.Fatal(err)
	}
	c := captureStream(t, stream, ctx, h, "0-0", StreamInput{"1-0", raw})
	return q, c.ReceiptIDs[0], raw, mapping
}

func TestNativePriceDurableSourceAndPublication(t *testing.T) {
	for _, scenario := range []string{"published", "unknown", "bad_digest", "wrong_source", "wrong_epoch", "wrong_session", "quarantined", "query_unknown", "forged_market", "conflicting_reply", "newer_refresh", "missing_receipt"} {
		t.Run(scenario, func(t *testing.T) {
			db, store, ctx := assessmentDB(t)
			v, _, target, approval, _ := executionTradeFixture(t, time.Now())
			q, receipt, _, mapping := storeNativePriceFixture(t, db, store, ctx, v.scope(), target)
			prepared, err := store.PrepareTestNativePrice(ctx, v.scope(), target, q.CommandID, receipt, mapping)
			if err != nil {
				t.Fatal(err)
			}
			var beforeState string
			if db.QueryRowContext(ctx, `SELECT state FROM futures_stream_inbox WHERE receipt_id=$1`, receipt).Scan(&beforeState) != nil || beforeState != "pending" {
				t.Fatal("preparation changed inbox")
			}
			v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
				m["payload"].(map[string]any)["symbol"] = "rb2701"
				m["payload"].(map[string]any)["limit_price"] = string(prepared.SuggestedLimitPrice)
			}))
			e := registrationEvidence(t, v)
			for id := range e.Mappings {
				e.Mappings[id] = mapping
			}
			approval.Market, approval.CommandSHA256 = &prepared.Market, commandDigest([]byte(v.Raw))
			r := registerVector(t, store, ctx, v, e, "registered")
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "bad_digest":
				exec(`UPDATE futures_stream_inbox SET body=body || ' '::bytea WHERE receipt_id=$1`, receipt)
			case "wrong_source":
				exec(`UPDATE futures_stream_receivers SET config_json=jsonb_set(config_json,'{SourceID}','"other"')`)
			case "wrong_epoch":
				exec(`UPDATE futures_test_reply_sources SET transport_json=jsonb_set(transport_json,'{Epoch}','"other"')`)
			case "wrong_session":
				target.CounterSessionID = "new-session"
			case "quarantined":
				exec(`UPDATE futures_stream_inbox SET state='quarantined' WHERE receipt_id=$1`, receipt)
			case "query_unknown":
				exec(`UPDATE futures_test_query_publications SET state='attempted',entry_id=NULL,finished_at=NULL WHERE command_id=$1`, q.CommandID)
			case "forged_market":
				approval.Market.BestBid = "5220"
			case "conflicting_reply":
				exec(`INSERT INTO futures_stream_inbox(stream_key,entry_id,body,body_sha256) SELECT stream_key,'2-0',body || ' '::bytea,body_sha256 FROM futures_stream_inbox WHERE receipt_id=$1`, receipt)
			case "missing_receipt":
				approval.Market.NativeFacts.ReceiptID++
			case "newer_refresh":
				other := editBody(t, q.Original.Raw, func(m map[string]any) { m["message_id"], m["idempotency_key"] = "refresh", "refresh" })
				fresh, err := store.Register(ctx, v.scope(), "cmd.query", other, RegistrationEvidence{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.publishTestQuery(ctx, v.scope(), fresh.CommandID, fresh.Original.MessageID, target, &fakeQueryPublisher{}); err != nil {
					t.Fatal(err)
				}
			}
			transport := &fakeQueryPublisher{appendError: scenario == "unknown"}
			result, err := store.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, approval, transport)
			if scenario != "published" && scenario != "unknown" {
				if err == nil || transport.appends.Load() != 0 || storedCount(t, db, "futures_test_trade_publications") != 0 {
					t.Fatal("unsafe native publication", result, err)
				}
				if scenario == "newer_refresh" && !strings.Contains(err.Error(), "superseded") {
					t.Fatal("refresh invalidation was not verified", err)
				}
				return
			}
			if scenario == "published" && (err != nil || result.Published == nil || !*result.Published) {
				t.Fatal(result, err)
			}
			if scenario == "unknown" && (!errors.Is(err, ErrQueryPublishUnknown) || result.Published != nil) {
				t.Fatal(result, err)
			}
			// Restarts/replays report the original outcome, even after the facts
			// are quarantined. They do not acquire another sending opportunity.
			exec(`UPDATE futures_stream_inbox SET state='quarantined' WHERE receipt_id=$1`, receipt)
			fresh, _ := NewCommandStore(db)
			again, _ := fresh.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, approval, transport)
			if !again.Replayed || transport.appends.Load() != 1 {
				t.Fatal("replayed native trade resent", again)
			}
			var binding []byte
			if err := db.QueryRowContext(ctx, `SELECT target_json FROM futures_test_trade_publications WHERE command_id=$1`, r.CommandID).Scan(&binding); err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Approval TestTradeApproval `json:"approval"`
			}
			if json.Unmarshal(binding, &saved) != nil || saved.Approval.Market.NativeFacts.BodySHA256 != prepared.Market.NativeFacts.BodySHA256 {
				t.Fatal("native evidence not retained")
			}
		})
	}
}
