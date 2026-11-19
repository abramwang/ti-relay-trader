package futures

import (
	"bytes"
	"fmt"
	"testing"
)

func TestCFFEXDeliveryDurableEvents(t *testing.T) {
	for _, v := range cffexCases(t) {
		if !oneOf(v.Scenario, "partial_fill_then_cancel", "full_fill_before_order") {
			continue
		}
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", v.Scenario, reverse), func(t *testing.T) {
				db, trades, ctx := tradeTestDB(t)
				events, err := NewEventStore(db)
				if err != nil {
					t.Fatal(err)
				}
				scope := cffexTradeScope()
				key, err := events.RegisterScope(ctx, scope, cffexScope().UserID, eventStream(scope))
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range cffexCommands(t, v) {
					if c.Original.Action != "order.submit" {
						continue
					}
					if _, err := registerEventCommand(t, events, ctx, key, c.Original.Raw); err != nil {
						t.Fatal(err)
					}
				}
				var bodies [][]byte
				for _, b := range v.Outputs {
					raw := cffexOriginal(t, b)
					o, err := parseReply(raw)
					if err != nil {
						t.Fatal(err)
					}
					if textField(o, "message_type") == "event" {
						bodies = append(bodies, raw)
					}
				}
				for i := range bodies {
					j := i
					if reverse {
						j = len(bodies) - 1 - i
					}
					// Reconstruct the repository as well as replay the exact transport row.
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
						t.Fatal("replay", duplicate, err)
					}
					var saved []byte
					if err := db.QueryRowContext(ctx, "SELECT body FROM futures_event_inbox WHERE event_id=$1", r.EventID).Scan(&saved); err != nil || !bytes.Equal(saved, bodies[j]) {
						t.Fatal("original changed", err)
					}
				}
				r, err := trades.Read(ctx, scope)
				if err != nil || r.Blocked || r.PendingCount != 0 || len(r.Orders) != 8 || len(r.Executions) != 8 || r.FillIdentityVersion != "v2" {
					t.Fatal(r, err)
				}
				for _, f := range r.Executions {
					if f.State != "linked" {
						t.Fatal(f)
					}
				}
				for _, o := range r.Orders {
					want := int64(1)
					status := "cancelled"
					if v.Scenario == "full_fill_before_order" {
						want = 101
						status = "filled"
					}
					if int64(o.Order.CumFilledQty) != want || o.Order.GatewayStatus != status {
						t.Fatal(o)
					}
				}
			})
		}
	}
}
