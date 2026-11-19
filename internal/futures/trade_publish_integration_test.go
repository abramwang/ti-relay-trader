package futures

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func assessmentDB(t *testing.T) (*sql.DB, *CommandStore, context.Context) {
	db, s, ctx := commandTestDB(t)
	migrateFuturesTest(t, db, ctx, "000046_futures_test_trade_publication", "up")
	return db, s, ctx
}

func TestAssessmentDurabilityBudgetAndUnknown(t *testing.T) {
	for _, scenario := range []string{"published", "unknown", "atomic_gate", "preflight", "insert_failed", "completion_failed"} {
		t.Run(scenario, func(t *testing.T) {
			db, s, ctx := assessmentDB(t)
			v, e, target, a := assessmentFixture(t)
			r := registerVector(t, s, ctx, v, e, "registered")
			f := &fakeQueryPublisher{appendError: scenario == "unknown", rejected: scenario == "atomic_gate", gateError: scenario == "preflight"}
			if scenario == "insert_failed" || scenario == "completion_failed" {
				_, err := db.ExecContext(ctx, `CREATE FUNCTION reject_trade_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$`)
				if err != nil {
					t.Fatal(err)
				}
				event := "INSERT"
				if scenario == "completion_failed" {
					event = "UPDATE"
				}
				if _, err := db.ExecContext(ctx, "CREATE TRIGGER reject_trade_publication BEFORE "+event+" ON futures_test_trade_publications FOR EACH ROW EXECUTE FUNCTION reject_trade_publication()"); err != nil {
					t.Fatal(err)
				}
			}
			f.appendHook = func() {
				var state string
				if err := db.QueryRowContext(ctx, `SELECT state FROM futures_test_trade_publications WHERE command_id=$1`, r.CommandID).Scan(&state); err != nil || state != "attempted" {
					t.Error("append before durable commit", err)
				}
			}
			result, err := s.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, f)
			if scenario == "published" {
				if err != nil || result.Published == nil || !*result.Published || result.BrokerAccepted {
					t.Fatal(result, err)
				}
			} else if err == nil {
				t.Fatal("failure accepted")
			}
			if scenario == "unknown" || scenario == "completion_failed" {
				if !errors.Is(err, ErrQueryPublishUnknown) || result.Published != nil {
					t.Fatal(result, err)
				}
			}
			if scenario == "preflight" || scenario == "insert_failed" {
				if f.appends.Load() != 0 || storedCount(t, db, "futures_test_trade_publications") != 0 {
					t.Fatal("preparation sent")
				}
				return
			}
			fresh, _ := NewCommandStore(db)
			again, _ := fresh.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, f)
			if !again.Replayed || f.appends.Load() != 1 || f.inspections.Load() != 1 {
				t.Fatal("replayed trade resent", again)
			}
			changed := a
			changed.AssessmentID = "another-assessment"
			if _, err := fresh.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, changed, f); err == nil {
				t.Fatal("retargeted original")
			}
			other := v
			other.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
				m["message_id"], m["idempotency_key"] = "other-message", "other-idem"
				p := m["payload"].(map[string]any)
				p["gateway_order_id"], p["client_order_id"] = "other-order", "other-client"
			}))
			secondEvidence := registrationEvidence(t, other)
			r2 := registerVector(t, s, ctx, other, secondEvidence, "registered")
			a.CommandSHA256 = commandDigest([]byte(other.Raw))
			if _, err := s.publishTestTrade(ctx, v.scope(), r2.CommandID, r2.Original.MessageID, target, a, f); err == nil || f.appends.Load() != 1 {
				t.Fatal("budget allowed another submit", err)
			}
		})
	}
}

func TestAssessmentConcurrentSinglePublication(t *testing.T) {
	db, s, ctx := assessmentDB(t)
	v, e, target, a := assessmentFixture(t)
	r := registerVector(t, s, ctx, v, e, "registered")
	f := &fakeQueryPublisher{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, f); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.appends.Load() != 1 || storedCount(t, db, "futures_test_trade_publications") != 1 {
		t.Fatal("multiple trades")
	}
}

func assessmentOrderBody(t *testing.T, r CommandRegistration, target TestQueryTarget, change func(map[string]any)) []byte {
	t.Helper()
	o := r.Original.Orders[0]
	payload := editBody(t, tradeFixture(t, "order"), func(p map[string]any) {
		p["account_id"], p["gateway_order_id"], p["client_order_id"] = o.AccountID, o.GatewayOrderID, o.ClientOrderID
		p["counter_session_id"], p["trade_date"], p["symbol"] = target.CounterSessionID, target.TradeDate, o.Symbol
		p["order_qty"], p["cum_filled_qty"], p["leaves_qty"], p["limit_price"] = 1, 0, 1, string(o.LimitPrice)
		p["gateway_status"], p["trade_side"], p["offset_type"], p["hedge_type"] = "working", o.TradeSide, o.OffsetType, o.HedgeType
		p["product_type"], p["schema_version"] = "futures", "ctp.f0.draft.2"
		p["native_ids"] = map[string]any{"broker_id": r.Scope.NativeBrokerID, "investor_id": r.Scope.InvestorID, "user_id": r.Scope.UserID, "order_ref": "000000001", "order_sys_id": "00000123", "front_id": 1, "session_id": 2}
	})
	raw, _ := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "event", "event_type": "order.event", "message_id": "synthetic-actual-order", "produced_at": assessmentTime(time.Now()), "routing": r.Scope.Route, "origin_message_id": r.Original.MessageID, "payload": json.RawMessage(payload)})
	if change != nil {
		raw = editBody(t, raw, change)
	}
	return raw
}

func assessmentOrderReceipt(t *testing.T, db *sql.DB, ctx context.Context, r CommandRegistration, target TestQueryTarget, change func(map[string]any)) int64 {
	t.Helper()
	raw := assessmentOrderBody(t, r, target, change)
	config := StreamConfig{Scope: r.Scope, Role: "event", SourceID: target.SourceID}
	configRaw, _ := json.Marshal(config)
	key := strings.TrimSuffix(queryReplyStream(r.Scope.Route), ":reply") + ":event"
	if _, err := db.ExecContext(ctx, `INSERT INTO futures_stream_receivers(stream_key,config_json,owner_id) VALUES($1,$2,'synthetic-owner')`, key, configRaw); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	var id int64
	if err := db.QueryRowContext(ctx, `INSERT INTO futures_stream_inbox(stream_key,entry_id,body,body_sha256) VALUES($1,'1-0',$2,$3) RETURNING receipt_id`, key, raw, h[:]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAssessmentCancelRequiresCapturedOwnOrder(t *testing.T) {
	for _, scenario := range []string{"valid", "missing_receipt", "wrong_origin", "wrong_source", "filled", "wrong_session", "bad_hash"} {
		t.Run(scenario, func(t *testing.T) {
			db, s, ctx := assessmentDB(t)
			v, e, target, a := assessmentFixture(t)
			r := registerVector(t, s, ctx, v, e, "registered")
			f := &fakeQueryPublisher{}
			if _, err := s.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, f); err != nil {
				t.Fatal(err)
			}
			a.CancelReceiptID = assessmentOrderReceipt(t, db, ctx, r, target, func(m map[string]any) {
				switch scenario {
				case "wrong_origin":
					m["origin_message_id"] = "other"
				case "filled":
					p := m["payload"].(map[string]any)
					p["gateway_status"], p["cum_filled_qty"], p["leaves_qty"] = "filled", 1, 0
				case "wrong_session":
					m["payload"].(map[string]any)["counter_session_id"] = "other"
				}
			})
			if scenario == "wrong_source" {
				if _, err := db.ExecContext(ctx, `UPDATE futures_stream_receivers SET config_json=jsonb_set(config_json,'{SourceID}','"other"')`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "bad_hash" {
				if _, err := db.ExecContext(ctx, `UPDATE futures_stream_inbox SET body=body || ' '::bytea`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing_receipt" {
				a.CancelReceiptID = 999999
			}
			o := r.Original.Orders[0]
			cancelV := v
			cancelV.Raw = string(editBody(t, []byte(intentVectors(t)["cancel_target_day"].Raw), func(m map[string]any) {
				m["message_id"], m["idempotency_key"] = "synthetic-cancel", "synthetic-cancel-idem"
				p := m["payload"].(map[string]any)
				p["account_id"], p["gateway_order_id"], p["trade_date"], p["cancel_id"] = o.AccountID, o.GatewayOrderID, o.TradeDate, "cancel-one"
			}))
			cancelR := registerVector(t, s, ctx, cancelV, registrationEvidence(t, cancelV), "registered")
			a.Market = nil
			a.CommandSHA256 = commandDigest([]byte(cancelV.Raw))
			result, err := s.publishTestTrade(ctx, v.scope(), cancelR.CommandID, cancelR.Original.MessageID, target, a, f)
			if scenario == "valid" {
				if err != nil || f.appends.Load() != 2 || result.BrokerAccepted {
					t.Fatal(result, err)
				}
			} else if err == nil || f.appends.Load() != 1 {
				t.Fatal("invalid cancel sent", result, err)
			}
		})
	}
}
