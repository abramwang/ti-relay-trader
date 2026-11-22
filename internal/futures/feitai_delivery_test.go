package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

const feitaiFixture = "../../testdata/fixtures/oc_feitai_offline_examples_20261122.json"

type feitaiCase struct {
	Scenario      string                          `json:"scenario"`
	Inputs        []struct{ Body, Stream string } `json:"inputs"`
	Outputs       []struct{ Body, SHA256 string } `json:"outputs"`
	InsertCalls   int                             `json:"insert_calls"`
	CancelCalls   int                             `json:"cancel_calls"`
	Pending       int                             `json:"pending"`
	ProductionAPI bool                            `json:"production_api_mode"`
	Partial       *int                            `json:"injected_partial_qty"`
	Rejection     *int                            `json:"injected_rejection_code"`
}

type feitaiExpected struct {
	Symbol, Status                string
	Qty, Filled, Inserts, Cancels int
}

// OC's injected scenarios are fixtures, not implementations of broker rules.
var feitaiExpectations = map[string]feitaiExpected{
	"working_2":                  {"m2701", "cancelled", 2, 0, 1, 1},
	"partial_5_fill_1":           {"l2701F", "cancelled", 5, 1, 1, 1},
	"partial_5_fill_2":           {"l2701F", "cancelled", 5, 2, 1, 1},
	"partial_5_fill_4":           {"l2701F", "cancelled", 5, 4, 1, 1},
	"rejection_3_then_continue":  {"i2701", "rejected", 3, 0, 2, 0},
	"rejection_10_then_continue": {"i2701", "rejected", 10, 0, 2, 0},
	"filled_1":                   {"m2701", "filled", 1, 1, 1, 0},
}

func feitaiCases(t *testing.T) []feitaiCase {
	t.Helper()
	raw, err := os.ReadFile(feitaiFixture)
	if err != nil {
		t.Fatal(err)
	}
	if commandDigest(raw) != "9b2e46f280e6a4b6b7e34a1151bb800a6d279286042a8c4b1202a28c1e4f0ec6" {
		t.Fatal("OC source fixture bytes changed")
	}
	var d struct {
		Synthetic     bool         `json:"synthetic_only"`
		SDK           bool         `json:"real_sdk_calls"`
		Redis         bool         `json:"real_redis"`
		RulesVerified bool         `json:"counter_rules_verified"`
		Cases         []feitaiCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if !d.Synthetic || d.SDK || d.Redis || d.RulesVerified || len(d.Cases) != len(feitaiExpectations) {
		t.Fatal("incorrect fixture boundary")
	}
	seen := map[string]bool{}
	for _, c := range d.Cases {
		want, ok := feitaiExpectations[c.Scenario]
		if !ok || seen[c.Scenario] || !c.ProductionAPI || c.Pending != 0 || c.InsertCalls != want.Inserts || c.CancelCalls != want.Cancels {
			t.Fatal("scenario or injected call counts", c.Scenario)
		}
		seen[c.Scenario] = true
		if strings.HasPrefix(c.Scenario, "partial_") {
			if c.Partial == nil || *c.Partial != want.Filled {
				t.Fatal("partial scenario")
			}
		} else if c.Partial != nil {
			t.Fatal("unexpected partial injection")
		}
		if want.Status == "rejected" {
			if c.Rejection == nil || *c.Rejection != 42 {
				t.Fatal("rejection injection")
			}
		} else if c.Rejection != nil {
			t.Fatal("unexpected rejection injection")
		}
		for _, b := range c.Outputs {
			if commandDigest([]byte(b.Body)) != b.SHA256 {
				t.Fatal("output bytes changed", c.Scenario)
			}
		}
	}
	return d.Cases
}

func feitaiScope() CommandScope {
	return CommandScope{QueryRoute{"test", "ctp_fixture_feitai", "feitai-offline-only", "990000001022"}, "1022", "001022", "FIXTUREUSER"}
}

func feitaiTradeScope() TradeScope {
	s := feitaiScope()
	return TradeScope{s.Route, s.NativeBrokerID, s.InvestorID, "20261117"}
}

func feitaiCommands(t *testing.T, c feitaiCase) map[string]CommandRegistration {
	t.Helper()
	out := map[string]CommandRegistration{}
	duplicates, submits, cancels, queries := 0, 0, 0, 0
	for _, input := range c.Inputs {
		role := input.Stream[strings.LastIndex(input.Stream, ":")+1:]
		if input.Stream != "relay:test:v1:ctp_fixture_feitai:feitai-offline-only:"+role {
			t.Fatal("fixture route")
		}
		command, err := DecodeCommand([]byte(input.Body), feitaiScope(), role)
		if err != nil {
			t.Fatal(err)
		}
		if old, ok := out[command.MessageID]; ok {
			if !bytes.Equal(old.Original.Raw, command.Raw) {
				t.Fatal("duplicate command changed")
			}
			duplicates++
			continue
		}
		want := feitaiExpectations[c.Scenario]
		switch command.Action {
		case "order.submit":
			qty := want.Qty
			if submits > 0 {
				qty = 1
			}
			if len(command.Orders) != 1 {
				t.Fatal("not a single order")
			}
			o := command.Orders[0]
			if o.Symbol != want.Symbol || o.Exchange != "DCE" || int(o.OrderQty) != qty || o.LimitPrice != "2964" || o.TradeSide != "B" || o.OffsetType != "O" {
				t.Fatal("request terms changed", o)
			}
			submits++
		case "order.cancel":
			cancels++
		case "order.list.query", "fill.list.query":
			queries++
		default:
			t.Fatal("unexpected action", command.Action)
		}
		out[command.MessageID] = CommandRegistration{CommandID: int64(len(out) + 1), Scope: feitaiScope(), Original: command}
	}
	if duplicates != 1 || submits != c.InsertCalls || cancels != c.CancelCalls || queries != 2 {
		t.Fatal("command counts", duplicates, submits, cancels, queries)
	}
	return out
}

func assertFeitaiReport(t *testing.T, c feitaiCase, r TradeReport) {
	t.Helper()
	want := feitaiExpectations[c.Scenario]
	fills := 1
	if c.Scenario == "working_2" {
		fills = 0
	}
	if r.Blocked || r.PendingCount != 0 || len(r.Orders) != want.Inserts || len(r.Executions) != fills {
		t.Fatalf("ledger: %+v", r)
	}
	o := r.Orders["feitai-offline-order"]
	if o == nil || o.Blocked || o.Order.GatewayStatus != want.Status || int(o.Order.OrderQty) != want.Qty || int(o.Order.CumFilledQty) != want.Filled || int(o.Order.LeavesQty) != want.Qty-want.Filled {
		t.Fatalf("original order: %+v", o)
	}
	if want.Status == "rejected" {
		if o.Order.RejectCode == nil || *o.Order.RejectCode != "42" || o.Order.NativeIDs.OrderSysID != "" {
			t.Fatal("rejection facts changed", o)
		}
		control := r.Orders["feitai-explicit-control"]
		if control == nil || control.Order.GatewayStatus != "filled" || control.Order.CumFilledQty != 1 || control.Order.OrderQty != 1 || control.Order.LeavesQty != 0 {
			t.Fatal("explicit control order lost", control)
		}
	}
	for _, f := range r.Executions {
		qty, gateway := want.Filled, "feitai-offline-order"
		if want.Status == "rejected" {
			qty, gateway = 1, "feitai-explicit-control"
		}
		if r.FillIdentityVersion != "v2" || f.State != "linked" || f.LinkedGateway != gateway || int(f.Fill.Qty) != qty || f.Fill.Price != "2964" || f.Fill.Symbol != want.Symbol || f.Fill.NativeIDs.TradeID != "000005678" || f.Fill.Fee != nil || f.Fill.FeeComplete {
			t.Fatal("fill identity, economics or missing fee changed", f)
		}
	}
}

func TestFeitaiDeliveryRuntime(t *testing.T) {
	inputs, outputs := 0, 0
	for _, c := range feitaiCases(t) {
		t.Run(c.Scenario, func(t *testing.T) {
			commands := feitaiCommands(t, c)
			inputs += len(c.Inputs)
			p := newTradeProjection(feitaiTradeScope())
			replies := map[string]bool{}
			for i, b := range c.Outputs {
				outputs++
				raw := []byte(b.Body)
				o, err := parseReply(raw)
				if err != nil {
					t.Fatal(err)
				}
				origin := textField(o, "origin_message_id")
				command, ok := commands[origin]
				if !ok {
					t.Fatal("unregistered origin", origin)
				}
				if textField(o, "message_type") == "reply" {
					got := assembleReplyResult(command, cffexTarget(t, raw), [][]byte{raw, raw})
					state := "accepted"
					if strings.HasSuffix(command.Original.Action, ".query") {
						state = "ready"
					}
					if got.State != state || !got.ResponseValidated || got.NativeAuditVerified || got.OrderStateChanged || got.RetrySafe {
						t.Fatalf("reply %d: %+v", i, got)
					}
					if replies[origin] {
						t.Fatal("repeated command produced another reply")
					}
					replies[origin] = true
					if got.Snapshot != nil {
						for _, row := range got.Snapshot.Orders {
							p.order(row)
						}
						for _, row := range got.Snapshot.Fills {
							p.fill(fmt.Sprintf("query-%d", i), row)
						}
					}
					continue
				}
				e, err := DecodeTradeEvent(raw, EventContext{feitaiTradeScope(), &origin})
				if err != nil || !bytes.Equal(e.Raw, raw) {
					t.Fatal("event", i, err)
				}
				if command.Original.Action != "order.submit" {
					t.Fatal("event borrowed cancel/query origin")
				}
				if e.Order != nil {
					p.order(*e.Order)
				} else if e.Fill != nil {
					p.fill(fmt.Sprintf("event-%d", i), *e.Fill)
				} else {
					t.Fatal("missing event")
				}
				wrong := feitaiTradeScope()
				wrong.Route.Env = "prod"
				if _, err := DecodeTradeEvent(raw, EventContext{wrong, &origin}); err == nil {
					t.Fatal("TEST event crossed production scope")
				}
			}
			if len(replies) != len(commands) {
				t.Fatal("missing command receipt")
			}
			p.reconcile()
			assertFeitaiReport(t, c, p.report)
		})
	}
	if inputs != 34 || outputs != 46 {
		t.Fatal("source inventory", inputs, outputs)
	}
}
