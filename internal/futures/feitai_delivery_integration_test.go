package futures

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFeitaiDeliveryDurableEventsAndQueries(t *testing.T) {
	for _, c := range feitaiCases(t) {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", c.Scenario, reverse), func(t *testing.T) {
				db, trades, ctx := tradeTestDB(t)
				events, err := NewEventStore(db)
				if err != nil {
					t.Fatal(err)
				}
				scope := feitaiTradeScope()
				key, err := events.RegisterScope(ctx, scope, feitaiScope().UserID, eventStream(scope))
				if err != nil {
					t.Fatal(err)
				}
				commands := feitaiCommands(t, c)
				for _, command := range commands {
					if command.Original.Action == "order.submit" {
						if _, err := registerEventCommand(t, events, ctx, key, command.Original.Raw); err != nil {
							t.Fatal(err)
						}
					}
				}
				var bodies, pages [][]byte
				for _, output := range c.Outputs {
					raw := []byte(output.Body)
					o, err := parseReply(raw)
					if err != nil {
						t.Fatal(err)
					}
					if textField(o, "message_type") == "event" {
						bodies = append(bodies, raw)
					} else if strings.HasSuffix(textField(o, "action"), ".query") {
						pages = append(pages, raw)
					}
				}
				for i := range bodies {
					j := i
					if reverse {
						j = len(bodies) - 1 - i
					}
					// Repository reconstruction and transport replay must not add executions.
					events, err = NewEventStore(db)
					if err != nil {
						t.Fatal(err)
					}
					source := QuerySource{eventStream(scope), fmt.Sprintf("%d-0", i+1)}
					r, err := events.Receive(ctx, key, source, bodies[j])
					if err != nil {
						t.Fatal(err)
					}
					processEvent(t, events, ctx, r.EventID, "applied")
					duplicate, err := events.Receive(ctx, key, source, bodies[j])
					if err != nil || !duplicate.Replayed || duplicate.EventID != r.EventID {
						t.Fatal("event replay", duplicate, err)
					}
					var saved []byte
					if err := db.QueryRowContext(ctx, "SELECT body FROM futures_event_inbox WHERE event_id=$1", r.EventID).Scan(&saved); err != nil || !bytes.Equal(saved, bodies[j]) {
						t.Fatal("original changed", err)
					}
				}
				report, err := trades.Read(ctx, scope)
				if err != nil {
					t.Fatal(err)
				}
				assertFeitaiReport(t, c, report)

				store, err := NewCommandStore(db)
				if err != nil {
					t.Fatal(err)
				}
				for i, raw := range pages {
					o, err := parseReply(raw)
					if err != nil {
						t.Fatal(err)
					}
					origin := textField(o, "origin_message_id")
					command, err := store.Register(ctx, feitaiScope(), "cmd.query", commands[origin].Original.Raw, RegistrationEvidence{})
					if err != nil || command.Outcome != "registered" {
						t.Fatal(command, err)
					}
					// The target comes from synthetic fixture evidence, not a live attestation.
					assembled := assembleReplyResult(command, cffexTarget(t, raw), [][]byte{raw})
					if assembled.State != "ready" || assembled.Snapshot == nil {
						t.Fatal(assembled)
					}
					queryContext := assembled.Snapshot.Context
					queryContext.Command = nil
					queries, err := NewQueryStore(db)
					if err != nil {
						t.Fatal(err)
					}
					if err := queries.RegisterCommand(ctx, origin, command.CommandID, queryContext, time.Minute, DefaultBatchLimits()); err != nil {
						t.Fatal(err)
					}
					source := QuerySource{queryReplyStream(scope.Route), fmt.Sprintf("%d-0", i+1)}
					for repeat := 0; repeat < 2; repeat++ {
						got, err := queries.Ingest(ctx, origin, source, raw, 0)
						if err != nil || got.State != BatchReady {
							t.Fatal("query page", got, err)
						}
					}
					if _, err := queries.ApplyTradeQuery(ctx, origin); err != nil {
						t.Fatal(err)
					}
					queries, err = NewQueryStore(db)
					if err != nil {
						t.Fatal(err)
					}
					again, err := queries.ApplyTradeQuery(ctx, origin)
					if err != nil || !again.Replayed {
						t.Fatal("query application replay", again, err)
					}
				}
				report, err = trades.Read(ctx, scope)
				if err != nil {
					t.Fatal(err)
				}
				assertFeitaiReport(t, c, report)
				if storedCount(t, db, "futures_execution_facts") != len(report.Executions) {
					t.Fatal("query duplicated event execution")
				}
			})
		}
	}
}
