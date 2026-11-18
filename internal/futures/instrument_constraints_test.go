package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type constraintsCase struct {
	Name    string          `json:"name"`
	Request json.RawMessage `json:"request"`
	Reply   json.RawMessage `json:"reply"`
}

func constraintsCases(t *testing.T) []constraintsCase {
	t.Helper()
	var d struct {
		Cases []constraintsCase `json:"cases"`
	}
	responsibilityFixture(t, "native_facts_candidate.json", &d)
	if len(d.Cases) != 3 {
		t.Fatal("missing cases")
	}
	return d.Cases
}

func constraintsContext() QueryContext {
	s := responsibilityCommandScope()
	g := int64(1)
	return QueryContext{Route: s.Route, Action: "instrument.constraints.query", OriginMessageID: "review-only-native-facts", CounterSessionID: "ctpproc-fixture", ConnectionGeneration: &g, TradeDate: "20261105", NativeBrokerID: s.NativeBrokerID, NativeInvestorID: s.InvestorID, NativeUserID: s.UserID, Constraints: &ConstraintsTarget{"rb2701", "SHFE"}}
}

func TestInstrumentConstraintsSharedCases(t *testing.T) {
	for _, v := range constraintsCases(t) {
		t.Run(v.Name, func(t *testing.T) {
			b, err := NewQueryBatch(constraintsContext(), 0, time.Minute, DefaultBatchLimits())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Accept(v.Reply, 0); err != nil {
				t.Fatal(err)
			}
			if v.Name == "missing_depth_terminal" {
				assertState(t, b, BatchFailed)
				if _, err := b.Candidate(); err == nil {
					t.Fatal("partial facts became a snapshot")
				}
				return
			}
			assertState(t, b, BatchReady)
			c, err := b.Candidate()
			if err != nil || len(c.Constraints) != 1 || c.Assets != nil || c.Positions != nil || c.Orders != nil || c.Fills != nil {
				t.Fatal(c, err)
			}
			f := c.Constraints[0]
			if f.Symbol != "rb2701" || f.Static.PriceTick == nil || *f.Static.PriceTick != "1" || f.Dynamic.LowerLimitPrice == nil || *f.Dynamic.LowerLimitPrice != "5218" {
				t.Fatal(f)
			}
			if v.Name == "complete_with_unavailable_facts" && (f.Dynamic.UpperLimitPrice != nil || f.Dynamic.BidPrice1 != nil || f.Dynamic.AskPrice1 != nil || f.Dynamic.BrokerTradeDate != nil) {
				t.Fatal("missing facts replaced")
			}
			*f.Static.PriceTick = "999"
			c.Context.Constraints.Symbol = "mutated"
			fresh, _ := b.Candidate()
			if *fresh.Constraints[0].Static.PriceTick != "1" || fresh.Context.Constraints.Symbol != "rb2701" {
				t.Fatal("mutable candidate alias")
			}
			if replay, err := b.Accept(v.Reply, time.Second); err != nil || !replay {
				t.Fatal("replay changed result", err)
			}
			if !bytes.Equal(b.AcceptedMessages()[0], v.Reply) {
				t.Fatal("raw changed")
			}
		})
	}
}

func TestInstrumentConstraintsRejectMalformedEvidence(t *testing.T) {
	raw := constraintsCases(t)[0].Reply
	for name, edit := range map[string]func(map[string]any){
		"session":    func(m map[string]any) { m["payload"].(map[string]any)["counter_session_id"] = "other" },
		"generation": func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = 2 },
		"atomic":     func(m map[string]any) { m["adapter_context"].(map[string]any)["atomic_snapshot"] = true },
		"empty":      func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} },
		"duplicate_target": func(m map[string]any) {
			p := m["payload"].(map[string]any)
			p["items"] = append(p["items"].([]any), p["items"].([]any)[0])
		},
		"wrong_symbol":        func(m map[string]any) { constraintsItem(m)["symbol"] = "rb2701C4350" },
		"wrong_day":           func(m map[string]any) { constraintsItem(m)["trade_date"] = "20261106" },
		"production_quote":    func(m map[string]any) { constraintsItem(m)["source_environment"] = "prod" },
		"null_without_marker": func(m map[string]any) { constraintsItem(m)["dynamic"].(map[string]any)["upper_limit_price"] = nil },
		"false_marker":        func(m map[string]any) { constraintsItem(m)["unavailable_numeric_fields"] = []any{"UpperLimitPrice"} },
		"decimal_number":      func(m map[string]any) { constraintsItem(m)["static"].(map[string]any)["price_tick"] = 1 },
		"missing_last":        func(m map[string]any) { constraintsEvidence(m, "depth")["is_last"] = false },
		"error":               func(m map[string]any) { constraintsEvidence(m, "depth")["callback_error_id"] = 42 },
		"sync_error":          func(m map[string]any) { constraintsEvidence(m, "depth")["sdk_return_code"] = -2 },
		"same_sdk_id":         func(m map[string]any) { constraintsEvidence(m, "depth")["sdk_request_id"] = 10 },
		"null_sdk":            func(m map[string]any) { constraintsEvidence(m, "depth")["sdk_request_id"] = nil },
		"missing_raw":         func(m map[string]any) { delete(m["adapter_context"].(map[string]any), "numeric_source_binary64") },
		"wrong_capture_time": func(m map[string]any) {
			constraintsItem(m)["dynamic"].(map[string]any)["captured_at"] = "2026-11-05T09:00:02+08:00"
		},
		"failed_with_items": func(m map[string]any) { m["status"], m["code"] = "failed", "QUERY_FAILED" },
		"partial":           func(m map[string]any) { m["status"] = "partial" },
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := NewQueryBatch(constraintsContext(), 0, time.Minute, DefaultBatchLimits())
			if _, err := b.Accept(editBody(t, raw, edit), 0); err == nil {
				t.Fatal("invalid facts accepted")
			}
			assertState(t, b, BatchInvalid)
		})
	}
}

func constraintsItem(m map[string]any) map[string]any {
	return m["payload"].(map[string]any)["items"].([]any)[0].(map[string]any)
}
func constraintsEvidence(m map[string]any, key string) map[string]any {
	return m["adapter_context"].(map[string]any)["query_evidence"].(map[string]any)[key].(map[string]any)
}

func TestInstrumentConstraintsIntentAndScopeIsolation(t *testing.T) {
	v := constraintsCases(t)[0]
	s := responsibilityCommandScope()
	c, err := DecodeCommand(v.Request, s, "cmd.query")
	if err != nil || c.Constraints == nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Fields[len(c.Fields)-2:], []IntentField{{"symbol", "rb2701"}, {"exchange", "SHFE"}}) {
		t.Fatal("intent must bind exact target")
	}
	t.Logf("candidate intent=%s sha256=%s", c.Intent, commandDigest(c.Intent))
	if commandDigest(c.Intent) != "43a15b367f8f304a85491349f62ed53be43cebdc034bf399ee2b525102b58b03" {
		t.Fatal("shared constraints intent changed")
	}
	changed := editBody(t, v.Request, func(m map[string]any) { m["payload"].(map[string]any)["symbol"] = "rb2702" })
	other, err := DecodeCommand(changed, s, "cmd.query")
	if err != nil || bytes.Equal(c.Intent, other.Intent) || !bytes.Equal(c.IdempotencyLookup, other.IdempotencyLookup) {
		t.Fatal("target missing in intent", err)
	}
	if _, err := DecodeCommand(v.Request, s, "cmd.trade"); err == nil {
		t.Fatal("wrong stream")
	}
	s.Route.Env = "prod"
	if _, err := DecodeCommand(v.Request, s, "cmd.query"); err == nil {
		t.Fatal("candidate enabled in production")
	}
	ctx := constraintsContext()
	otherContext := copyContext(ctx)
	otherContext.Constraints.Symbol = "rb2702"
	if queryScope(ctx) == queryScope(otherContext) {
		t.Fatal("contract snapshots share head")
	}
	legacy := QueryContext{Route: ctx.Route, Action: "account.asset.query"}
	oldScope, _ := json.Marshal(struct {
		Route  QueryRoute
		Action string
	}{legacy.Route, legacy.Action})
	if queryScope(legacy) != commandDigest(oldScope) {
		t.Fatal("legacy snapshot scope changed")
	}
	ctx.SDKRequestID = &[]int64{10}[0]
	if _, err := NewQueryBatch(ctx, 0, time.Minute, DefaultBatchLimits()); err == nil {
		t.Fatal("two SDK requests reduced to one")
	}
}

func TestInstrumentConstraintsPersistencePostgres(t *testing.T) {
	db, queries, ctx := queryTestDB(t)
	commands, _ := NewCommandStore(db)
	v := constraintsCases(t)[0]
	c := constraintsContext()
	r, err := commands.Register(ctx, c.commandScope(), "cmd.query", v.Request, RegistrationEvidence{})
	if err != nil || r.Outcome != "registered" {
		t.Fatal(r, err)
	}
	wrong := copyContext(c)
	wrong.Constraints.Symbol = "rb2702"
	if err := queries.RegisterCommand(ctx, "wrong", r.CommandID, wrong, time.Minute, DefaultBatchLimits()); err == nil {
		t.Fatal("wrong target registered")
	}
	bindQuery(t, queries, ctx, "facts", r, c)
	source := QuerySource{queryReplyStream(c.Route), "1-0"}
	result, err := queries.Ingest(ctx, "facts", source, v.Reply, 0)
	if err != nil || result.State != BatchReady || !result.Published {
		t.Fatal(result, err)
	}
	if _, err := queries.Current(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.Current(ctx, wrong); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("wrong contract read", err)
	}
	if again, err := queries.Ingest(ctx, "facts", source, v.Reply, time.Second); err != nil || !again.Replayed || storedCount(t, db, "futures_query_snapshots") != 1 {
		t.Fatal(again, err)
	}
	var original []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE batch_id='facts'`).Scan(&original); err != nil || !bytes.Equal(original, v.Reply) {
		t.Fatal("original not durable", err)
	}
	restarted, _ := NewQueryStore(db)
	snapshot, err := restarted.Snapshot(ctx, "facts")
	if err != nil || len(snapshot.Candidate.Constraints) != 1 {
		t.Fatal(snapshot, err)
	}
	if _, err := restarted.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
		t.Fatal("restart turned history into ready", err)
	}
	f := &fakeQueryPublisher{}
	target := publicationTarget()
	target.CounterSessionID, target.TradeDate = c.CounterSessionID, c.TradeDate
	if _, err := commands.publishTestQuery(ctx, c.commandScope(), r.CommandID, r.Original.MessageID, target, f); err != nil || f.appends.Load() != 1 || storedCount(t, db, "futures_test_query_publications") != 1 {
		t.Fatal("facts query did not use durable publisher", err)
	}
	if replay, err := commands.publishTestQuery(ctx, c.commandScope(), r.CommandID, r.Original.MessageID, target, f); err != nil || !replay.Replayed || f.appends.Load() != 1 {
		t.Fatal("facts query was resent", err)
	}
	// Same idempotency key with another native target must conflict.
	changed := editBody(t, v.Request, func(m map[string]any) {
		m["message_id"] = "different-target"
		m["payload"].(map[string]any)["symbol"] = "rb2702"
	})
	if conflicting, err := commands.Register(ctx, c.commandScope(), "cmd.query", changed, RegistrationEvidence{}); err != nil || conflicting.Outcome != "conflict" {
		t.Fatal(conflicting, err)
	}
}

func TestInstrumentConstraintsFailedAndMissingRemainAuditedPostgres(t *testing.T) {
	for _, v := range constraintsCases(t)[1:] {
		t.Run(v.Name, func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			s, _ := NewCommandStore(db)
			c := constraintsContext()
			r, err := s.Register(ctx, c.commandScope(), "cmd.query", v.Request, RegistrationEvidence{})
			if err != nil {
				t.Fatal(err)
			}
			bindQuery(t, q, ctx, "facts", r, c)
			out, err := q.Ingest(ctx, "facts", QuerySource{queryReplyStream(c.Route), "1-0"}, v.Reply, 0)
			if err != nil {
				t.Fatal(err)
			}
			if v.Name == "missing_depth_terminal" {
				if out.State != BatchFailed || out.Published || storedCount(t, db, "futures_query_snapshots") != 0 {
					t.Fatal("partial query published")
				}
			} else {
				snap, err := q.Snapshot(ctx, "facts")
				if err != nil || snap.Candidate.Constraints[0].Dynamic.UpperLimitPrice != nil {
					t.Fatal("missing fact lost", err)
				}
			}
			if storedCount(t, db, "futures_query_observations") != 1 {
				t.Fatal("raw missing")
			}
		})
	}
}
