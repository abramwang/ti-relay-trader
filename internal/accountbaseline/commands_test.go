package accountbaseline

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func commandPayload(gateway string) map[string]any {
	return map[string]any{"account_id": "00030484", "gateway_order_id": gateway, "trade_date": "20261118", "symbol": "000001", "exchange": "SZ", "trade_side": "B", "business_type": "S", "qty": 100, "price": json.Number("10.125")}
}

func archivedCommand(t *testing.T, id, action string, payload any) ledger.CurrentBaselineCommandArchive {
	t.Helper()
	body, err := json.Marshal(map[string]any{"protocol": "relay.stream.v1", "message_type": "command", "message_id": id, "action": action, "sent_at": fixtureStart, "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.CurrentBaselineCommandArchive{IndexedMessageID: id, Archive: ledger.CurrentBaselineArchive{ArchiveID: id, Stream: "relay:test:v1:huaxin:old-gateway:cmd.trade", EntryID: "100-0", Direction: "in", Role: "cmd.trade", Body: " \n" + string(body) + "\n", ReceivedAt: fixtureStart}}
}

func commandOrder(t *testing.T, message, gateway, status string, terminal bool) ledger.CurrentBaselineRecord {
	t.Helper()
	p := commandPayload(gateway)
	p["record_id"], p["origin_message_id"] = "ledger-order:"+gateway, message
	p["order_qty"], p["limit_price"] = p["qty"], "10.125"
	p["status"], p["is_terminal"] = status, terminal
	delete(p, "price")
	delete(p, "qty")
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.CurrentBaselineRecord{Key: "ledger-order:" + gateway, Document: raw}
}

func commandObservations(t *testing.T, c ledger.CurrentBaselineCandidate) map[string]CommandObservation {
	t.Helper()
	out := map[string]CommandObservation{}
	for _, row := range c.Resources["commands"] {
		var o CommandObservation
		if err := json.Unmarshal(row.Document, &o); err != nil {
			t.Fatal(err)
		}
		if !o.ObservationOnly || o.QuantityAdditive || o.ExecutionVerified || o.ReplyCoverageProven {
			t.Fatal("overstated command evidence", o)
		}
		out[o.RecordID] = o
	}
	return out
}

func TestCurrentAccountBaselineCommandObservations(t *testing.T) {
	s := fixtureSources(t, 0)
	single := archivedCommand(t, "single", "order.submit", commandPayload("missing"))
	batch := archivedCommand(t, "batch", "order.batch.submit", map[string]any{"account_id": s.Scope.AccountID, "orders": []any{commandPayload("done"), commandPayload("missing-child")}})
	cancel := archivedCommand(t, "cancel", "order.cancel", map[string]any{"account_id": s.Scope.AccountID, "gateway_order_id": "done", "cancel_id": "cancel-1"})
	s.TradeCommands = &ledger.CurrentBaselineCommandSources{Archives: []ledger.CurrentBaselineCommandArchive{single, batch, cancel}, Orders: []ledger.CurrentBaselineRecord{commandOrder(t, "batch", "done", "cancelled", true)}}
	c, err := Assemble(s, "commands", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Resources["orders"]) != 0 {
		t.Fatal("command evidence fabricated economic orders")
	}
	os := commandObservations(t, c)
	if os["command-archive:single"].Targets[0].Association != "unrepresented" {
		t.Fatal("empty query erased prior-day command")
	}
	b := os["command-archive:batch"]
	if len(b.Targets) != 2 || b.Targets[0].Association != "ledger_intent_observed" || b.Targets[1].Association != "unrepresented" || *b.Targets[1].ChildIndex != 1 {
		t.Fatal("partial batch lost", b)
	}
	if os["command-archive:cancel"].Targets[0].Association != "cancel_outcome_unverified" {
		t.Fatal("cancel request became cancelled")
	}
	for _, archive := range s.TradeCommands.Archives {
		o := os["command-archive:"+archive.Archive.ArchiveID]
		if o.OriginalBody != archive.Archive.Body || o.Evidence.BodySHA256 != digest([]byte(o.OriginalBody)) || len(o.Flags) != 0 {
			t.Fatal("original bytes/old route scope lost", o)
		}
	}
}

func TestCurrentAccountBaselineCommandsDoNotTrustAcceptance(t *testing.T) {
	s := fixtureSources(t, 0)
	cmd := archivedCommand(t, "accepted", "order.submit", commandPayload("gw"))
	s.TradeCommands = &ledger.CurrentBaselineCommandSources{Archives: []ledger.CurrentBaselineCommandArchive{cmd}, Orders: []ledger.CurrentBaselineRecord{commandOrder(t, "accepted", "gw", "created", false)}}
	// A reply to a trading command is not an order terminal or query page.
	q := s.Queries["orders"]
	q.Replies = append(q.Replies, ledger.CurrentBaselineArchive{Body: `{"message_type":"reply","origin_message_id":"accepted","status":"accepted"}`})
	s.Queries["orders"] = q
	c, err := Assemble(s, "accepted", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	o := commandObservations(t, c)["command-archive:accepted"]
	if o.Targets[0].Association != "ledger_intent_observed" || !strings.Contains(string(o.Targets[0].LedgerRecords[0]), `"created"`) {
		t.Fatal(o)
	}
}

func TestCurrentAccountBaselineCommandConflicts(t *testing.T) {
	for _, kind := range []string{"wrong_environment", "wrong_account", "wrong_index", "malformed", "duplicate_json_key", "missing_date", "intent_conflict", "message_conflict", "repeat", "duplicate_child"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureSources(t, 0)
			a := archivedCommand(t, "id", "order.submit", commandPayload("gw"))
			local := commandOrder(t, "id", "gw", "filled", true)
			s.TradeCommands = &ledger.CurrentBaselineCommandSources{Archives: []ledger.CurrentBaselineCommandArchive{a}, Orders: []ledger.CurrentBaselineRecord{local}}
			p := &s.TradeCommands.Archives[0]
			switch kind {
			case "wrong_environment":
				p.Archive.Stream = strings.Replace(p.Archive.Stream, ":test:", ":prod:", 1)
			case "wrong_account":
				p.Archive.Body = strings.Replace(p.Archive.Body, "00030484", "other", 1)
			case "wrong_index":
				p.IndexedMessageID = "other"
			case "malformed":
				p.Archive.Body = "{"
			case "duplicate_json_key":
				p.Archive.Body = strings.Replace(p.Archive.Body, `"qty":100`, `"qty":100,"qty":200`, 1)
			case "missing_date":
				p.Archive.Body = strings.Replace(p.Archive.Body, `"trade_date":"20261118",`, "", 1)
			case "intent_conflict":
				p.Archive.Body = strings.Replace(p.Archive.Body, `"qty":100`, `"qty":200`, 1)
			case "message_conflict", "repeat":
				b := a
				b.Archive.ArchiveID, b.Archive.EntryID = "second", "101-0"
				if kind == "message_conflict" {
					b.Archive.Body = strings.Replace(b.Archive.Body, `"qty":100`, `"qty":200`, 1)
				}
				s.TradeCommands.Archives = append(s.TradeCommands.Archives, b)
			case "duplicate_child":
				*p = archivedCommand(t, "id", "order.batch.submit", map[string]any{"account_id": s.Scope.AccountID, "orders": []any{commandPayload("gw"), commandPayload("gw")}})
			}
			c, err := Assemble(s, kind, fixtureStart)
			if err != nil {
				t.Fatal(err)
			}
			os := commandObservations(t, c)
			for _, o := range os {
				if kind == "repeat" {
					if o.Targets[0].Association != "ledger_intent_observed" || !strings.Contains(strings.Join(o.Flags, ","), "repeated_command_observation") {
						t.Fatal(o)
					}
				} else {
					for _, target := range o.Targets {
						if target.Association == "ledger_intent_observed" {
							t.Fatalf("%s trusted: %+v", kind, o)
						}
					}
				}
			}
			if len(os) != len(s.TradeCommands.Archives) {
				t.Fatal("conflicting evidence dropped")
			}
		})
	}
}

func TestCurrentAccountBaselineCommandCollectionAbsentVsEmpty(t *testing.T) {
	s := fixtureSources(t, 0)
	c, err := Assemble(s, "absent", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Resources["commands"]; ok {
		t.Fatal("uncollected became empty")
	}
	s.TradeCommands = &ledger.CurrentBaselineCommandSources{}
	c, err = Assemble(s, "empty", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	if rows, ok := c.Resources["commands"]; !ok || len(rows) != 0 {
		t.Fatal("collected empty not identified")
	}
	for i := 0; i < 501; i++ {
		s.TradeCommands.Archives = append(s.TradeCommands.Archives, archivedCommand(t, fmt.Sprint(i), "order.submit", commandPayload(fmt.Sprint(i))))
	}
	c, err = Assemble(s, "many", fixtureStart)
	if err != nil || len(c.Resources["commands"]) != 501 {
		t.Fatal("commands truncated", err)
	}
	s.TradeCommands.Archives = append(s.TradeCommands.Archives, s.TradeCommands.Archives[0])
	if _, err = Assemble(s, "duplicate-archive", fixtureStart); err == nil {
		t.Fatal("duplicate archive identity accepted")
	}
}

func TestCurrentAccountBaselineCommandEvidenceAndLimits(t *testing.T) {
	t.Run("payload bytes", func(t *testing.T) {
		s := fixtureSources(t, 0)
		a := archivedCommand(t, "exact", "order.submit", commandPayload("gw"))
		a.Archive.Body = strings.Replace(a.Archive.Body, `"price":10.125`, `"price" : 10.125000`, 1)
		a.Archive.Body = strings.Replace(a.Archive.Body, `"qty":100`, `"qty":9007199254740993`, 1)
		s.TradeCommands = &ledger.CurrentBaselineCommandSources{Archives: []ledger.CurrentBaselineCommandArchive{a}}
		c, err := Assemble(s, "exact", fixtureStart)
		if err != nil {
			t.Fatal(err)
		}
		o := commandObservations(t, c)["command-archive:exact"]
		if !strings.Contains(o.Targets[0].OriginalPayload, `"price" : 10.125000`) || !strings.Contains(o.Targets[0].OriginalPayload, "9007199254740993") {
			t.Fatal("source tokens were changed", o)
		}
	})
	t.Run("malformed sibling", func(t *testing.T) {
		s := fixtureSources(t, 0)
		a := archivedCommand(t, "id", "order.submit", commandPayload("gw"))
		b := a
		b.Archive.ArchiveID, b.Archive.Body = "bad", "{"
		s.TradeCommands = &ledger.CurrentBaselineCommandSources{Archives: []ledger.CurrentBaselineCommandArchive{a, b}, Orders: []ledger.CurrentBaselineRecord{commandOrder(t, "id", "gw", "filled", true)}}
		c, err := Assemble(s, "sibling", fixtureStart)
		if err != nil {
			t.Fatal(err)
		}
		o := commandObservations(t, c)["command-archive:id"]
		if o.Targets[0].Association != "unverifiable" || !strings.Contains(strings.Join(o.Flags, ","), "command_identity_conflict") {
			t.Fatal("broken indexed sibling ignored", o)
		}
	})
	for _, kind := range []string{"archives", "orders", "children", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureSources(t, 0)
			s.TradeCommands = &ledger.CurrentBaselineCommandSources{}
			switch kind {
			case "archives":
				s.TradeCommands.Archives = make([]ledger.CurrentBaselineCommandArchive, 100001)
			case "orders":
				s.TradeCommands.Orders = make([]ledger.CurrentBaselineRecord, 100001)
			case "children":
				a := archivedCommand(t, "many", "order.batch.submit", map[string]any{"account_id": s.Scope.AccountID, "orders": json.RawMessage("[" + strings.Repeat("null,", 100000) + "null]")})
				s.TradeCommands.Archives = []ledger.CurrentBaselineCommandArchive{a}
			case "bytes":
				s.TradeCommands.Archives = []ledger.CurrentBaselineCommandArchive{{Archive: ledger.CurrentBaselineArchive{ArchiveID: "big", Body: strings.Repeat("x", 64<<20)}}}
			}
			if _, err := Assemble(s, kind, fixtureStart); !errors.Is(err, ledger.ErrCurrentBaselineLimit) {
				t.Fatal("silently truncated limit", err)
			}
		})
	}
}
