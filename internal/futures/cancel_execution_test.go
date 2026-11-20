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

func cancelServiceFixture(t *testing.T) (*sql.DB, *TestOrderService, *fakeQueryPublisher, *orderMarketStub, context.Context, CancelExecutionRequest, int64) {
	t.Helper()
	db, store, ctx := assessmentDB(t)
	f := &fakeQueryPublisher{}
	s, m, _ := serviceFixture(t, store, f)
	request := orderServiceRequest()
	order, err := s.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	a := s.accounts[request.Order.AccountID]
	a.SubmitEnabled = false
	a.CancelEnabled = true
	s.accounts[request.Order.AccountID] = a
	target, err := s.cancelTarget(ctx, a.Source.Scope)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := beginFuturesTransaction(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(a.Source.Scope.Route)), order.Publication.CommandID)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	receipt := assessmentOrderReceipt(t, db, ctx, reg, target, nil)
	seedServiceCapture(t, store, a.Source.Scope, target)
	m.failed.Store(true)
	s.target = func(context.Context, CommandScope) (TestQueryTarget, error) {
		t.Error("cancel called submit target")
		return TestQueryTarget{}, ErrQueryPublishGate
	}
	s.transport = func(_ TestSourceConfig, _ TestQueryTarget, a TestTradeApproval) (queryPublicationTransport, func(), error) {
		if a.Market != nil || a.CancelReceiptID <= 0 || !a.ExecutionPermission.Cancel || a.ExecutionPermission.Submit {
			t.Error("cancel used quote/submit permission")
		}
		return f, func() {}, nil
	}
	return db, s, f, m, ctx, CancelExecutionRequest{"cancel-1", request.Order.AccountID, order.GatewayOrderID, request.Order.TradeDate}, receipt
}

func TestCancelServiceDurableReplay(t *testing.T) {
	for _, scenario := range []string{"published", "unknown", "preflight", "atomic_gate"} {
		t.Run(scenario, func(t *testing.T) {
			db, s, f, m, ctx, r, _ := cancelServiceFixture(t)
			beforeMarket := m.calls.Load()
			f.appendError = scenario == "unknown"
			f.gateError = scenario == "preflight"
			f.rejected = scenario == "atomic_gate"
			result, err := s.Cancel(ctx, r)
			want := map[string]string{"published": "published", "unknown": "attempted", "preflight": "registered", "atomic_gate": "not_published"}[scenario]
			if (scenario == "published" && err != nil) || (scenario != "published" && err == nil) || result.Publication.State != want || result.BrokerStateKnown || result.RetrySafe {
				t.Fatal(result, err)
			}
			before := f.appends.Load()
			s.store, _ = NewCommandStore(db)
			a := s.accounts[r.AccountID]
			a.CancelEnabled = false
			a.AuthorizationExpiresAt = time.Now().Add(-time.Hour)
			s.accounts[r.AccountID] = a
			s.cancelTarget = func(context.Context, CommandScope) (TestQueryTarget, error) {
				t.Error("replay fetched heartbeat")
				return TestQueryTarget{}, ErrQueryPublishGate
			}
			again, err := s.Cancel(ctx, r)
			if err != nil || !again.Publication.Replayed || again.MessageID != result.MessageID || again.CancelID != result.CancelID || again.Publication.State != want {
				t.Fatal(again, err)
			}
			status, err := s.LookupCancel(ctx, r.AccountID, r.IdempotencyKey)
			if err != nil || status.Publication.State != want || status.MessageID != result.MessageID || f.appends.Load() != before || m.calls.Load() != beforeMarket {
				t.Fatal(status, err)
			}
			if storedCount(t, db, "futures_commands") != 2 || storedCount(t, db, "futures_command_audit") != 2 {
				t.Fatal("duplicate registration")
			}
			for _, change := range []func(*CancelExecutionRequest){func(r *CancelExecutionRequest) { r.GatewayOrderID = "other" }, func(r *CancelExecutionRequest) { r.TradeDate = "20261118" }} {
				other := r
				change(&other)
				if _, err := s.Cancel(ctx, other); !errors.Is(err, ErrOrderRequestConflict) {
					t.Fatal("retargeted cancel", err)
				}
			}
			if _, err := s.Lookup(ctx, r.AccountID, r.IdempotencyKey); !errors.Is(err, ErrOrderRequestConflict) {
				t.Fatal("cancel key used as submit", err)
			}
		})
	}
}

func appendCancelOrderObservation(t *testing.T, db *sql.DB, ctx context.Context, receipt int64, entry string, change func(map[string]any)) {
	t.Helper()
	var raw []byte
	var key string
	if err := db.QueryRowContext(ctx, `SELECT stream_key,body FROM futures_stream_inbox WHERE receipt_id=$1`, receipt).Scan(&key, &raw); err != nil {
		t.Fatal(err)
	}
	raw = editBody(t, raw, change)
	h := sha256.Sum256(raw)
	if _, err := db.ExecContext(ctx, `INSERT INTO futures_stream_inbox(stream_key,entry_id,body,body_sha256) VALUES($1,$2,$3,$4)`, key, entry, raw, h[:]); err != nil {
		t.Fatal(err)
	}
}

func TestCancelServiceEvidenceRejection(t *testing.T) {
	for _, scenario := range []string{"disabled", "expired", "missing_order", "wrong_day", "wrong_session", "unpublished", "missing_receipt", "corrupt", "quarantined", "filled", "cancelled_then_working", "future", "source_changed", "wrong_origin", "missing_sysid", "changed_sysid", "key_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			db, s, f, m, ctx, r, receipt := cancelServiceFixture(t)
			a := s.accounts[r.AccountID]
			exec := func(query string, args ...any) {
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "disabled":
				a.CancelEnabled = false
			case "expired":
				a.AuthorizationExpiresAt = time.Now().Add(-time.Minute)
			case "missing_order":
				r.GatewayOrderID = "not-a-local-order"
			case "wrong_day":
				r.TradeDate = "20261118"
			case "key_conflict":
				r.IdempotencyKey = orderServiceRequest().IdempotencyKey
			case "wrong_session":
				original := s.cancelTarget
				s.cancelTarget = func(ctx context.Context, scope CommandScope) (TestQueryTarget, error) {
					target, err := original(ctx, scope)
					target.CounterSessionID = "new-session"
					return target, err
				}
			case "unpublished":
				exec(`UPDATE futures_test_trade_publications SET state='attempted',entry_id=NULL,finished_at=NULL`)
			case "missing_receipt":
				exec(`DELETE FROM futures_stream_inbox`)
			case "corrupt":
				exec(`UPDATE futures_stream_inbox SET body=body || ' '::bytea`)
			case "quarantined":
				exec(`UPDATE futures_stream_inbox SET state='quarantined'`)
			case "source_changed":
				exec(`UPDATE futures_stream_receivers SET config_json=jsonb_set(config_json,'{SourceID}','"other"')`)
			default:
				appendCancelOrderObservation(t, db, ctx, receipt, "2-0", func(m map[string]any) {
					p := m["payload"].(map[string]any)
					switch scenario {
					case "filled":
						p["gateway_status"], p["cum_filled_qty"], p["leaves_qty"] = "filled", 1, 0
					case "cancelled_then_working":
						p["gateway_status"], p["leaves_qty"] = "cancelled", 0
					case "future":
						m["produced_at"] = assessmentTime(time.Now().Add(2 * time.Minute))
					case "wrong_origin":
						m["origin_message_id"] = "different-command"
					case "missing_sysid":
						p["native_ids"].(map[string]any)["order_sys_id"] = ""
					case "changed_sysid":
						p["native_ids"].(map[string]any)["order_sys_id"] = "00000456"
					}
				})
				if scenario == "cancelled_then_working" {
					appendCancelOrderObservation(t, db, ctx, receipt, "3-0", func(map[string]any) {})
				}
			}
			s.accounts[r.AccountID] = a
			before := m.calls.Load()
			if _, err := s.Cancel(ctx, r); err == nil {
				t.Fatal("unsafe cancel accepted")
			}
			if f.appends.Load() != 1 || storedCount(t, db, "futures_commands") != 1 || m.calls.Load() != before {
				t.Fatal("unsafe cancel registered/published or queried market")
			}
		})
	}
}

func TestCancelServiceConcurrentAndSecondAttempt(t *testing.T) {
	db, s, f, _, ctx, r, _ := cancelServiceFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.Cancel(ctx, r)
			if err != nil || result.Publication.CommandID == 0 {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.appends.Load() != 2 || storedCount(t, db, "futures_commands") != 2 {
		t.Fatal("duplicate cancel")
	}
	if _, err := s.LookupCancel(ctx, r.AccountID, r.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	r.IdempotencyKey = "cancel-new-key"
	if _, err := s.Cancel(ctx, r); !errors.Is(err, ErrOrderRequestConflict) {
		t.Fatal("new key bypassed one-cancel budget", err)
	}
	if f.appends.Load() != 2 {
		t.Fatal("automatic replacement cancel")
	}
}

func TestCancelServiceOldWorkingNeedsCurrentCapture(t *testing.T) {
	for _, state := range []string{"running", "stale", "stopped", "owner_changed"} {
		t.Run(state, func(t *testing.T) {
			db, s, f, m, ctx, r, receipt := cancelServiceFixture(t)
			appendCancelOrderObservation(t, db, ctx, receipt, "2-0", func(m map[string]any) {
				m["produced_at"] = assessmentTime(time.Now().Add(-2 * time.Hour))
			})
			var err error
			switch state {
			case "stale":
				_, err = db.ExecContext(ctx, `UPDATE futures_capture_health SET event_checked_at=clock_timestamp()-interval '1 minute'`)
			case "stopped":
				_, err = db.ExecContext(ctx, `UPDATE futures_capture_health SET state='stopped'`)
			case "owner_changed":
				_, err = db.ExecContext(ctx, `UPDATE futures_stream_receivers SET owner_id='other-owner'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := m.calls.Load()
			result, err := s.Cancel(ctx, r)
			if state == "running" {
				if err != nil || result.Publication.State != "published" || f.appends.Load() != 2 {
					t.Fatal(result, err)
				}
			} else if !errors.Is(err, ErrCaptureUnavailable) || f.appends.Load() != 1 {
				t.Fatal("old order bypassed receiver freshness", result, err)
			}
			if m.calls.Load() != before {
				t.Fatal("cancel requested quote")
			}
		})
	}
}

func TestCancelServicePublicationTransactionRequiresCapture(t *testing.T) {
	db, s, _, _, ctx, r, receipt := cancelServiceFixture(t)
	a := s.accounts[r.AccountID]
	target, err := s.cancelTarget(ctx, a.Source.Scope)
	if err != nil {
		t.Fatal(err)
	}
	raw, evidence := buildServiceCancel(a.Source.Scope, r, time.Now())
	reg, err := s.store.Register(ctx, a.Source.Scope, "cmd.trade", raw, evidence)
	if err != nil || reg.Outcome != "registered" {
		t.Fatal(reg, err)
	}
	approval := TestTradeApproval{PermissionProfile: relayTestExecutionProfile, AssessmentID: "relay-order-service-v1", CancelReceiptID: receipt}
	var assessment string
	if err := db.QueryRowContext(ctx, `SELECT assessment_key FROM futures_test_trade_publications WHERE action='order.submit'`).Scan(&assessment); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE futures_capture_health SET state='stopped'`); err != nil {
		t.Fatal(err)
	}
	tx, err := beginFuturesTransaction(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Even a caller bypassing the HTTP service and capture transport cannot
	// borrow its assessment name to cancel against an unobserved order state.
	if err := verifyAssessmentCancel(ctx, tx, assessment, reg, target, approval); !errors.Is(err, ErrCaptureUnavailable) {
		t.Fatal("publication transaction skipped capture", err)
	}
}

func TestCancelServicePublicationRechecksLatest(t *testing.T) {
	db, s, f, _, ctx, r, receipt := cancelServiceFixture(t)
	s.transport = func(TestSourceConfig, TestQueryTarget, TestTradeApproval) (queryPublicationTransport, func(), error) {
		appendCancelOrderObservation(t, db, ctx, receipt, "2-0", func(m map[string]any) {
			p := m["payload"].(map[string]any)
			p["gateway_status"], p["cum_filled_qty"], p["leaves_qty"] = "filled", 1, 0
		})
		return f, func() {}, nil
	}
	result, err := s.Cancel(ctx, r)
	if err == nil || result.Publication.State != "registered" || f.appends.Load() != 1 {
		t.Fatal(result, err)
	}
	again, err := s.Cancel(ctx, r)
	if err != nil || again.Publication.CommandID != result.Publication.CommandID || f.appends.Load() != 1 {
		t.Fatal("registered retry resent", again, err)
	}
}

func TestCancelServiceUsesStreamOrder(t *testing.T) {
	db, s, f, _, ctx, r, receipt := cancelServiceFixture(t)
	appendCancelOrderObservation(t, db, ctx, receipt, "10-0", func(map[string]any) {})
	// A later database insertion need not be a later source observation.
	appendCancelOrderObservation(t, db, ctx, receipt, "9-0", func(m map[string]any) {
		m["produced_at"] = assessmentTime(time.Now().Add(-2 * time.Minute))
	})
	var expected int64
	if err := db.QueryRowContext(ctx, `SELECT receipt_id FROM futures_stream_inbox WHERE entry_id='10-0'`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	s.transport = func(_ TestSourceConfig, _ TestQueryTarget, a TestTradeApproval) (queryPublicationTransport, func(), error) {
		if a.CancelReceiptID != expected {
			t.Error("selected stale receipt", a.CancelReceiptID)
		}
		return f, func() {}, nil
	}
	if result, err := s.Cancel(ctx, r); err != nil || result.Publication.State != "published" {
		t.Fatal(result, err)
	}
}

func TestCancelServiceNewerFillBlocks(t *testing.T) {
	db, s, f, _, ctx, r, receipt := cancelServiceFixture(t)
	a := s.accounts[r.AccountID]
	var fillRaw []byte
	appendCancelOrderObservation(t, db, ctx, receipt, "2-0", func(m map[string]any) {
		order := m["payload"].(map[string]any)
		payload := editBody(t, tradeFixture(t, "fill"), func(p map[string]any) {
			for _, k := range []string{"account_id", "gateway_order_id", "client_order_id", "counter_session_id", "trade_date", "symbol", "exchange", "trade_side", "offset_type", "hedge_type", "native_ids", "business_type", "product_type", "schema_version"} {
				p[k] = order[k]
			}
			ids := p["native_ids"].(map[string]any)
			ids["trade_id"] = "test-trade-1"
			p["fill_id"] = nativeFillIdentityV2(TradeScope{a.Source.Scope.Route, a.Source.Scope.NativeBrokerID, a.Source.Scope.InvestorID, r.TradeDate}, "SHFE", "test-trade-1", ids["order_sys_id"].(string), "B")
			p["qty"], p["price"] = 1, order["limit_price"]
			p["matched_at"] = assessmentTime(time.Now())
		})
		m["event_type"], m["payload"] = "fill.event", json.RawMessage(payload)
		fillRaw, _ = json.Marshal(m)
	})
	if _, err := DecodeTradeEvent(fillRaw, EventContext{Scope: TradeScope{a.Source.Scope.Route, a.Source.Scope.NativeBrokerID, a.Source.Scope.InvestorID, r.TradeDate}}); err != nil {
		t.Fatal("invalid fill fixture", err)
	}
	if _, err := s.Cancel(ctx, r); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal("newer fill did not block", err)
	}
	if f.appends.Load() != 1 || storedCount(t, db, "futures_commands") != 1 {
		t.Fatal("cancel sent after fill")
	}
}

func TestCancelExecutionRequestStrict(t *testing.T) {
	raw := []byte(`{"idempotency_key":"cancel-1","account_id":"300052141","gateway_order_id":"local-order","trade_date":"20261119"}`)
	for _, key := range []string{"receipt_id", "cancel_id", "session", "market", "permission"} {
		changed := editBody(t, raw, func(m map[string]any) { m[key] = "override" })
		if _, err := DecodeCancelExecutionRequest(changed); err == nil {
			t.Fatal("accepted evidence override", key)
		}
	}
	for _, bad := range [][]byte{[]byte(strings.Repeat(" ", 4097)), append(raw, raw...), []byte(strings.Replace(string(raw), `"cancel-1"`, `null`, 1)), []byte(strings.Replace(string(raw), `"20261119"`, `"20260230"`, 1))} {
		if _, err := DecodeCancelExecutionRequest(bad); err == nil {
			t.Fatal("accepted malformed cancel")
		}
	}
	r, err := DecodeCancelExecutionRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(r)
	if _, err := DecodeCancelExecutionRequest(encoded); err != nil {
		t.Fatal(err)
	}
}
