package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOCReceivedRepliesPersistOriginalBytes(t *testing.T) {
	for _, trade := range []bool{false, true} {
		for _, example := range ocReplyCases(t, trade) {
			t.Run(example.Name, func(t *testing.T) {
				// Scenarios reuse fixture-idem with different actions, so each is
				// an independent DB. They are not one executable command history.
				db, q, ctx := queryTestDB(t)
				c := ocReplyContext(t, trade, example.Name)
				rawCommand := example.Command
				if trade {
					// No source query command supplied for these new cases. Register
					// an explicit synthetic precondition; never rewrite the OC reply.
					rawCommand, _ = json.Marshal(map[string]any{
						"protocol": "relay.stream.v1", "message_type": "command", "message_id": c.OriginMessageID,
						"request_id": c.Command.RequestID, "correlation_id": c.Command.CorrelationID, "idempotency_key": c.Command.IdempotencyKey,
						"action": c.Action, "sent_at": "2026-11-06T08:59:00+08:00",
						"payload": map[string]any{"account_id": c.Route.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2"},
					})
				}
				commands, _ := NewCommandStore(db)
				r, err := commands.Register(ctx, c.commandScope(), "cmd.query", rawCommand, RegistrationEvidence{})
				if err != nil || r.Outcome != "registered" {
					t.Fatal(r, err)
				}
				c.Command = nil
				bindQuery(t, q, ctx, "oc", r, c)
				inputs := []QueryInput{}
				for i, body := range example.Bodies {
					inputs = append(inputs, QueryInput{QuerySource{queryReplyStream(c.Route), fmt.Sprintf("%d-0", i+1)}, []byte(body), time.Duration(i)})
				}
				out, err := q.IngestBatch(ctx, "oc", inputs)
				want := BatchFailed
				if example.Successful {
					want = BatchReady
				}
				if err != nil || out[len(out)-1].State != want || out[len(out)-1].Published != example.Successful {
					t.Fatal(out, err)
				}
				rows, err := db.QueryContext(ctx, `SELECT body FROM futures_query_observations ORDER BY observation_id`)
				if err != nil {
					t.Fatal(err)
				}
				index := 0
				for rows.Next() {
					var saved []byte
					if err := rows.Scan(&saved); err != nil || index >= len(example.Bodies) || !bytes.Equal(saved, []byte(example.Bodies[index])) {
						t.Fatal("original bytes changed", err)
					}
					index++
				}
				err = rows.Err()
				rows.Close()
				if err != nil || index != len(example.Bodies) {
					t.Fatal("missing raw evidence", err)
				}
				restarted, _ := NewQueryStore(db)
				if state, err := restarted.Recover(ctx, "oc"); err != nil || state != want {
					t.Fatal(state, err)
				}
				if !example.Successful {
					if _, err := restarted.Snapshot(ctx, "oc"); err == nil || storedCount(t, db, "futures_query_snapshots") != 0 {
						t.Fatal("failure became empty success")
					}
					return
				}
				if _, err := restarted.Snapshot(ctx, "oc"); err != nil {
					t.Fatal(err)
				}
				if !trade {
					return
				}
				if !strings.HasPrefix(example.Name, "verified_external_") {
					if _, err := restarted.ApplyTradeQuery(ctx, "oc"); err == nil || storedCount(t, db, "futures_trade_observations") != 0 {
						t.Fatal("unknown local order treated as external")
					}
					v := intentVectors(t)["submit"]
					v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
						m["idempotency_key"] = "synthetic-local-submit"
						m["payload"].(map[string]any)["order_qty"] = 10
					}))
					registerVector(t, commands, ctx, v, registrationEvidence(t, v), "registered")
				}
				if result, err := restarted.ApplyTradeQuery(ctx, "oc"); err != nil || !result.Applied || result.RowCount != 1 {
					t.Fatal(result, err)
				}
				if result, err := restarted.ApplyTradeQuery(ctx, "oc"); err != nil || !result.Replayed || storedCount(t, db, "futures_trade_observations") != 1 {
					t.Fatal("replay duplicated facts", result, err)
				}
				ledger, _ := NewTradeStore(db)
				report, err := ledger.Read(ctx, c.tradeScope())
				if err != nil {
					t.Fatal(err)
				}
				if c.Action == "fill.list.query" && (len(report.Orders) != 0 || report.PendingCount != 1 || !report.Blocked) {
					t.Fatal("isolated fill manufactured an order")
				}
				if c.Action == "order.list.query" && (len(report.Orders) != 1 || report.Blocked) {
					t.Fatal("valid order not applied")
				}
			})
		}
	}
}
