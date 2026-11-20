package futures

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCFFEXCloseCapacityStoredFactsReplay(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			db, trades, ctx := tradeTestDB(t)
			commands, _ := NewCommandStore(db)
			f := newCloseCapacityFixture(t)
			reg := f.add(t, 5, "S")
			stored, err := commands.Register(ctx, f.scope, "cmd.trade", reg.Original.Raw, reg.Evidence)
			if err != nil || stored.Outcome != "registered" {
				t.Fatal(stored, err)
			}
			f.commands = []CommandRegistration{stored}
			working, cancelled := closeCapacityOrder(stored, "working", 2), closeCapacityOrder(stored, "cancelled", 2)
			fill := closeCapacityFill(f.report.Scope, working, "f1", 2)
			o, _ := json.Marshal(working)
			c, _ := json.Marshal(cancelled)
			x, _ := json.Marshal(fill)
			inputs := []struct {
				kind string
				raw  []byte
			}{{"order", o}, {"fill", x}, {"order", c}}
			indices := []int{0, 1, 2}
			if reverse {
				indices = []int{2, 1, 0}
			}
			for _, index := range indices {
				if _, err := trades.Observe(ctx, f.report.Scope, fmt.Sprintf("fixture/%d", index), inputs[index].kind, inputs[index].raw); err != nil {
					t.Fatal(err)
				}
			}
			// New readers reconstruct the same quantity from persisted commands and
			// facts. Repeated observations do not create additional fill deductions.
			for i := 0; i < 2; i++ {
				trades, _ = NewTradeStore(db)
				commands, _ = NewCommandStore(db)
				again, err := commands.Register(ctx, f.scope, "cmd.trade", reg.Original.Raw, reg.Evidence)
				if err != nil || again.Outcome != "replayed" || again.CommandID != stored.CommandID {
					t.Fatal(again, err)
				}
				f.commands = []CommandRegistration{again}
				out, err := trades.Observe(ctx, f.report.Scope, "fixture/1", "fill", x)
				if err != nil || !out.Replayed {
					t.Fatal(out, err)
				}
				f.report, err = trades.Read(ctx, f.report.Scope)
				if err != nil {
					t.Fatal(err)
				}
				capacity, err := f.calculate("S")
				if err != nil || capacity.Available != 6 || capacity.ClosedSinceQuery != 2 || capacity.LocalReserved != 0 {
					t.Fatal(capacity, err)
				}
			}
			if storedCount(t, db, "futures_commands") != 1 || storedCount(t, db, "futures_trade_observations") != 3 || storedCount(t, db, "futures_execution_facts") != 1 {
				t.Fatal("duplicate commands or execution facts")
			}
			// A later contradictory execution keeps its original evidence and
			// invalidates capacity instead of silently changing available lots.
			bad := editBody(t, x, func(m map[string]any) { m["qty"] = 3 })
			if _, err := trades.Observe(ctx, f.report.Scope, "fixture/conflict", "fill", bad); err != nil {
				t.Fatal(err)
			}
			f.report, err = trades.Read(ctx, f.report.Scope)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.calculate("S"); err == nil {
				t.Fatal("conflicting persisted fact became capacity")
			}
		})
	}
}
