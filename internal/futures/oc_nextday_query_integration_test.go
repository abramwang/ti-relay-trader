package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Request identity comes from the pinned OC audit, never from the reply under test.
// This exercises storage below transport admission; missing group still blocks live review.
func nextdayAuditQueries(t *testing.T, c ocNextdayCase) map[string]QueryContext {
	t.Helper()
	queries := map[string]QueryContext{}
	for _, line := range strings.Split(strings.TrimSpace(c.Audit), "\n") {
		var wrapper struct {
			Record struct {
				Entry struct {
					Kind       string `json:"kind"`
					Action     string `json:"action"`
					Origin     string `json:"origin_message_id"`
					Session    string `json:"counter_session_id"`
					Day        string `json:"trade_date"`
					Generation int64  `json:"connection_generation"`
					Ticket     int64  `json:"ticket"`
					SDK        int64  `json:"sdk_request_id"`
				} `json:"entry"`
			} `json:"record"`
		}
		if err := json.Unmarshal([]byte(line), &wrapper); err != nil {
			t.Fatal(err)
		}
		e := wrapper.Record.Entry
		if e.Kind != "query_result" {
			continue
		}
		if _, exists := queries[e.Origin]; exists || e.Ticket <= 0 || e.SDK <= 0 {
			t.Fatal("ambiguous audit request")
		}
		queries[e.Origin] = QueryContext{Route: nextdayRoute(), Action: e.Action, OriginMessageID: e.Origin,
			Ticket: strconv.FormatInt(e.Ticket, 10), SDKRequestID: &e.SDK, CounterSessionID: e.Session,
			ConnectionGeneration: &e.Generation, TradeDate: e.Day,
			NativeBrokerID: "4900", NativeInvestorID: "300052141", NativeUserID: "300052141"}
	}
	return queries
}

func TestOCNextdayQueryAndEventPostgres(t *testing.T) {
	pages := 0
	for _, c := range nextdayCases(t) {
		t.Run(c.Scenario, func(t *testing.T) {
			db, queries, ctx := queryTestDB(t)
			commands, _ := NewCommandStore(db)
			trades, _ := NewTradeStore(db)
			events, _ := NewEventStore(db)
			scope := TradeScope{nextdayRoute(), "4900", "300052141", "20261118"}
			key, err := events.RegisterScope(ctx, scope, "300052141", eventStream(scope))
			if err != nil {
				t.Fatal(err)
			}
			contexts := nextdayAuditQueries(t, c)
			cancels := map[string][]byte{}
			for _, in := range c.Inputs {
				var command rawObject
				if err := json.Unmarshal([]byte(in.Body), &command); err != nil {
					t.Fatal(err)
				}
				if q, ok := contexts[textField(command, "message_id")]; ok {
					r, err := commands.Register(ctx, q.commandScope(), "cmd.query", []byte(in.Body), RegistrationEvidence{})
					if err != nil || !oneOf(r.Outcome, "registered", "replayed") {
						t.Fatal(r, err)
					}
					if err := queries.RegisterCommand(ctx, q.OriginMessageID, r.CommandID, q, time.Minute, DefaultBatchLimits()); err != nil {
						t.Fatal(err)
					}
				} else if textField(command, "action") == "order.submit" {
					if _, err := registerEventCommand(t, events, ctx, key, []byte(in.Body)); err != nil {
						t.Fatal(err)
					}
				} else if textField(command, "action") == "order.cancel" {
					cancels[textField(command, "message_id")] = []byte(in.Body)
				}
			}
			externalOrders, queryCount, eventCount := 0, 0, 0
			for i, raw := range c.Outputs {
				var o rawObject
				if err := json.Unmarshal(raw, &o); err != nil {
					t.Fatal(err)
				}
				if q, ok := contexts[textField(o, "origin_message_id")]; ok {
					src := QuerySource{queryReplyStream(q.Route), fmt.Sprintf("%d-0", i+1)}
					out, err := queries.Ingest(ctx, q.OriginMessageID, src, raw, 0)
					if err != nil {
						t.Fatal(err)
					}
					queryCount++
					pages++
					var retained []byte
					if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE entry_id=$1`, src.EntryID).Scan(&retained); err != nil || !bytes.Equal(retained, raw) {
						t.Fatal("query bytes changed", err)
					}
					if c.Scenario == "partial_order" {
						if out.State != BatchFailed || out.Published {
							t.Fatal("partial query became successful", out)
						}
						if _, err := queries.Current(ctx, q); !errors.Is(err, ErrNoSnapshot) {
							t.Fatal("partial query has current snapshot", err)
						}
						if _, err := queries.ApplyTradeQuery(ctx, q.OriginMessageID); err == nil {
							t.Fatal("partial order facts applied")
						}
						continue
					}
					if out.State != BatchReady || !out.Published {
						t.Fatal(out)
					}
					if q.Action == "order.list.query" {
						applied, err := queries.ApplyTradeQuery(ctx, q.OriginMessageID)
						if err != nil || !applied.Applied {
							t.Fatal(applied, err)
						}
						externalOrders += applied.RowCount
						reopened, _ := NewQueryStore(db)
						again, err := reopened.ApplyTradeQuery(ctx, q.OriginMessageID)
						if err != nil || !again.Replayed {
							t.Fatal("query application duplicated", again, err)
						}
					}
				} else if textField(o, "message_type") == "reply" && textField(o, "action") == "order.cancel" {
					command, ok := cancels[textField(o, "origin_message_id")]
					if !ok {
						t.Fatal("cancel receipt without original command")
					}
					current, err := trades.Read(ctx, scope)
					local := current.Orders["fixture-order"]
					if err != nil || current.Blocked || local == nil || local.Order.GatewayStatus != "working" {
						t.Fatal("cancel target not established", err)
					}
					commandScope := CommandScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, "300052141"}
					evidence := RegistrationEvidence{CancelTarget: &CancelTarget{commandScope, local.Order.GatewayOrderID, local.Order.TradeDate}}
					registered, err := commands.Register(ctx, commandScope, "cmd.trade", command, evidence)
					if err != nil || !oneOf(registered.Outcome, "registered", "replayed") {
						t.Fatal("cancel identity after working event", registered, err)
					}
				} else if textField(o, "message_type") == "event" {
					src := QuerySource{eventStream(scope), fmt.Sprintf("%d-0", i+1)}
					r, err := events.Receive(ctx, key, src, raw)
					if err != nil {
						t.Fatal(err)
					}
					processEvent(t, events, ctx, r.EventID, "applied")
					events, _ = NewEventStore(db)
					again, err := events.Receive(ctx, key, src, raw)
					if err != nil || again.EventID != r.EventID {
						t.Fatal("event repeated", err)
					}
					processEvent(t, events, ctx, again.EventID, "applied")
					eventCount++
				}
			}
			if queryCount != len(contexts) {
				t.Fatal("query coverage", queryCount, len(contexts))
			}
			if eventCount == 0 {
				if storedCount(t, db, "futures_order_states") != 0 || storedCount(t, db, "futures_execution_facts") != 0 || storedCount(t, db, "futures_query_snapshots") != 0 {
					t.Fatal("blocked/partial scenario produced business facts")
				}
				return
			}
			r, err := trades.Read(ctx, scope)
			if err != nil || r.Blocked || len(r.Orders) != externalOrders+1 {
				t.Fatal("query/event ledger mismatch", r, err)
			}
			want, fills := "cancelled", 0
			if c.Scenario == "fill_before_order" {
				want, fills = "filled", 1
			} else if c.Scenario == "cancel_rejected" {
				want = "working"
			}
			local := r.Orders["fixture-order"]
			if local == nil || local.Order.GatewayStatus != want || len(r.Executions) != fills {
				t.Fatal("unexpected local final state", r)
			}
		})
	}
	if pages != 9 {
		t.Fatal("missing query pages", pages)
	}
}
